package sse_test

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"http-server/modules/binder"
	"http-server/modules/security"
	"http-server/plugins/config"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func TestMQTT_BindFiles(t *testing.T) {
	t.Run("Security Bind", func(t *testing.T) {
		runBindTest(t, "../../examples/mqtt/security.bind", func(t *testing.T, addr string) {
			// 1.2.3.4 should be blocked
			blocked := &mockConn{addr: &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 1234}}
			if security.GetEngine().AllowConnection(blocked, "mqtt_filter") {
				t.Error("Expected 1.2.3.4 to be blocked by mqtt_filter")
			}

			// 127.0.0.1 should be allowed
			allowed := &mockConn{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
			if !security.GetEngine().AllowConnection(allowed, "mqtt_filter") {
				t.Error("Expected 127.0.0.1 to be allowed by mqtt_filter")
			}
		})
	})

	t.Run("Storage Bind", func(t *testing.T) {
		tmpDB := t.TempDir() + "/mqtt_storage.db"
		runBindTestWithDB(t, "../../examples/mqtt/storage.bind", tmpDB, func(t *testing.T, addr string) {
			opts := mqtt.NewClientOptions().AddBroker("tcp://" + addr).SetClientID("storage_client")
			opts.SetCleanSession(false)
			client := mqtt.NewClient(opts)
			if token := client.Connect(); token.Wait() && token.Error() != nil {
				t.Fatalf("Failed to connect: %v", token.Error())
			}

			client.Publish("test/store", 1, true, "hello storage")
			client.Disconnect(100)

			// Verify file exists
			if _, err := os.Stat(tmpDB); os.IsNotExist(err) {
				t.Errorf("Database file %s was not created", tmpDB)
			}
		})
	})

	t.Run("Full Bind", func(t *testing.T) {
		tmpDB := t.TempDir() + "/mqtt_full.db"
		runBindTestWithDB(t, "../../examples/mqtt/full.bind", tmpDB, func(t *testing.T, addr string) {
			// Security check
			blocked := &mockConn{addr: &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 1234}}
			if security.GetEngine().AllowConnection(blocked, "mqtt_security") {
				t.Error("Expected 1.2.3.4 to be blocked by mqtt_security")
			}

			// Persistence check
			opts := mqtt.NewClientOptions().AddBroker("tcp://" + addr).SetClientID("full_client")
			opts.SetCleanSession(false)
			client := mqtt.NewClient(opts)
			if token := client.Connect(); token.Wait() && token.Error() != nil {
				t.Fatalf("Failed to connect: %v", token.Error())
			}
			client.Publish("full/test", 1, true, "full message")
			client.Disconnect(100)

			if _, err := os.Stat(tmpDB); os.IsNotExist(err) {
				t.Errorf("Database file %s was not created", tmpDB)
			}
		})
	})
}

func runBindTest(t *testing.T, bindPath string, fn func(t *testing.T, addr string)) {
	runBindTestWithDB(t, bindPath, "", fn)
}

func runBindTestWithDB(t *testing.T, bindPath string, dbPath string, fn func(t *testing.T, addr string)) {
	appCfg := &config.AppConfig{}
	m := binder.NewManager(appCfg)

	content, err := os.ReadFile(bindPath)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", bindPath, err)
	}

	sContent := string(content)
	if dbPath != "" {
		// Mock replacement for test DB path
		sContent = strings.ReplaceAll(sContent, "mqtt_storage_test.db", dbPath)
		sContent = strings.ReplaceAll(sContent, "mqtt_full_test.db", dbPath)
	}

	cfg, _, err := binder.ParseConfig(sContent)
	if err != nil {
		t.Fatalf("Failed to parse config from %s: %v", bindPath, err)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := m.Start(cfg); err != nil {
			errCh <- err
		}
	}()
	defer m.Stop()

	// Wait for any address to be active
	var addr string
	start := time.Now()
	for {
		addrs := m.GetAddresses()
		if len(addrs) > 0 {
			addr = addrs[0]
			break
		}
		if time.Since(start) > 2*time.Second {
			t.Fatal("Manager failed to provide active listener address in time")
		}
		time.Sleep(50 * time.Millisecond)
	}

	fn(t, addr)
}
