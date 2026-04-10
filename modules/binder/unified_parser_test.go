package binder

import (
	"os"
	"testing"
)

func TestUnifiedParser(t *testing.T) {
	content := `
HTTP 0.0.0.0:80 redirect=/
    AUTH [secret=123,timeout=30]
    END AUTH
    ON click once=true
        print("Click!");
    END ON
    WORKER "background.js" [interval=1000]
    WORKER [mode=async]
        print("Worker running");
    END WORKER
END HTTP

TCP 0.0.0.0:1234 DTP [timeout=10s]
END TCP
`
	tmpfile, err := os.CreateTemp("", "unified_test.bind")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	config, _, err := ParseFile(tmpfile.Name())
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(config.Groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(config.Groups))
	}

	// HTTP Group
	httpGroup := config.Groups[0]
	if httpGroup.Directive != "HTTP" {
		t.Errorf("Expected HTTP group, got %s", httpGroup.Directive)
	}
	httpProto := httpGroup.Items[0]
	if httpProto.Name != "HTTP" {
		t.Errorf("Expected HTTP proto, got %s", httpProto.Name)
	}
	if httpProto.Args["redirect"] != "/" {
		t.Errorf("Expected redirect=/, got %s", httpProto.Args["redirect"])
	}

	// AUTH
	if len(httpProto.Auth) != 1 {
		t.Fatalf("Expected 1 auth, got %d", len(httpProto.Auth))
	}
	if httpProto.Auth[0].Configs["secret"] != "123" {
		t.Errorf("Expected secret=123, got %s", httpProto.Auth[0].Configs["secret"])
	}
	if httpProto.Auth[0].Configs["timeout"] != "30" {
		t.Errorf("Expected timeout=30, got %s", httpProto.Auth[0].Configs["timeout"])
	}

	// EVENT
	events := httpProto.GetRoutes("ON")
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}
	if events[0].Path != "click" {
		t.Errorf("Expected event click, got %s", events[0].Path)
	}
	if events[0].Args.GetBool("once") {
		t.Errorf("Expected once=true, got %s", events[0].Args["once"])
	}

	// WORKERS
	workers := httpProto.GetRoutes("WORKER")
	if len(workers) != 2 {
		t.Fatalf("Expected 2 workers, got %d", len(workers))
	}
	// Worker 0
	if workers[0].Path != "background.js" {
		t.Errorf("Expected worker 0 file background.js, got %s", workers[0].Path)
	}
	if workers[0].Args.Get("interval") != "1000" {
		t.Errorf("Expected interval=1000, got %s", workers[0].Args.Get("interval"))
	}
	// Worker 1
	if workers[1].Path != "" {
		t.Errorf("Expected worker 1 path empty, got %s", workers[1].Path)
	}
	if workers[1].Args.Get("mode") != "async" {
		t.Errorf("Expected mode=async, got %s", workers[1].Args.Get("mode"))
	}
	if workers[1].Handler != `print("Worker running");` {
		t.Errorf("Expected worker 1 handler code, got '%s'", workers[1].Handler)
	}

	// TCP Group with DTP
	tcpGroup := config.Groups[1]
	if tcpGroup.Directive != "TCP" {
		t.Errorf("Expected TCP group, got %s", tcpGroup.Directive)
	}
	dtpProto := tcpGroup.Items[0]
	if dtpProto.Name != "DTP" {
		t.Errorf("Expected DTP proto, got %s", dtpProto.Name)
	}
	if dtpProto.Args["timeout"] != "10s" {
		t.Errorf("Expected timeout=10s, got %s", dtpProto.Args["timeout"])
	}
}
