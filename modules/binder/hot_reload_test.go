package binder

import (
	"http-server/plugins/config"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHotReload_IncludeFiles(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "binder-hot-reload-*")
	defer os.RemoveAll(tmpDir)

	mainPath := filepath.Join(tmpDir, "main.bind")
	incPath := filepath.Join(tmpDir, "inc.bind")

	// Initial content
	os.WriteFile(mainPath, []byte(`HTTP 0.0.0.0:8080
	INCLUDE "inc.bind"
END HTTP`), 0644)
	os.WriteFile(incPath, []byte(`GET / BEGIN
	"Initial"
END GET`), 0644)

	// 1. Verify ParseFile returns both files
	bcfg, files, err := ParseFile(mainPath)
	if err != nil {
		t.Fatalf("Initial ParseFile failed: %v", err)
	}

	foundMain, foundInc := false, false
	mainAbs, _ := filepath.Abs(mainPath)
	incAbs, _ := filepath.Abs(incPath)

	for _, f := range files {
		if f == mainAbs {
			foundMain = true
		}
		if f == incAbs {
			foundInc = true
		}
	}

	if !foundMain || !foundInc {
		t.Errorf("Missing files in ParseFile return: main=%v, inc=%v", foundMain, foundInc)
	}

	if len(bcfg.Groups[0].Items[0].Routes) != 1 || bcfg.Groups[0].Items[0].Routes[0].Handler != "\"Initial\"" {
		t.Errorf("Unexpected initial config state: got '%s'", bcfg.Groups[0].Items[0].Routes[0].Handler)
	}

	// 2. Simulate modification of included file
	time.Sleep(100 * time.Millisecond) // Ensure timestamp change for some systems
	os.WriteFile(incPath, []byte(`GET / BEGIN
	"Updated"
END GET`), 0644)

	// Re-parse
	bcfg2, _, err := ParseFile(mainPath)
	if err != nil {
		t.Fatalf("Updated ParseFile failed: %v", err)
	}

	if len(bcfg2.Groups[0].Items[0].Routes) != 1 || bcfg2.Groups[0].Items[0].Routes[0].Handler != "\"Updated\"" {
		t.Errorf("Expected handler '\"Updated\"', got '%s'", bcfg2.Groups[0].Items[0].Routes[0].Handler)
	}
}

type dummyDirective struct{}

func (d *dummyDirective) Name() string                    { return "DUMMY" }
func (d *dummyDirective) Address() string                 { return "" }
func (d *dummyDirective) Start() ([]net.Listener, error)  { return nil, nil }
func (d *dummyDirective) Match(peek []byte) (bool, error) { return true, nil }
func (d *dummyDirective) Handle(conn net.Conn) error      { conn.Close(); return nil }

func (d *dummyDirective) HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error {
	return nil
}

func (d *dummyDirective) Close() error { return nil }

func TestManager_StopStart(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "binder-stop-start-*")
	defer os.RemoveAll(tmpDir)

	mainPath := filepath.Join(tmpDir, "main.bind")
	os.WriteFile(mainPath, []byte(`DUMMY 127.0.0.1:0
END DUMMY`), 0644)

	bcfg, _, err := ParseFile(mainPath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	manager := NewManager(&config.AppConfig{})
	manager.RegisterDirective("DUMMY", func(cfg *DirectiveConfig) (Directive, error) {
		return &dummyDirective{}, nil
	})

	// Start in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- manager.Start(bcfg)
	}()

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Stop it
	if err := manager.Stop(); err != nil {
		t.Errorf("Manager.Stop failed: %v", err)
	}

	// Verify Start returns nil (since it was stopped)
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("Manager.Start returned error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Errorf("Timed out waiting for Manager.Start to return after Stop")
	}
}
