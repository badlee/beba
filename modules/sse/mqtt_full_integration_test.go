package sse_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"http-server/modules/binder"
	"http-server/modules/db"
	"http-server/modules/security"
	"http-server/modules/sse"
	"http-server/plugins/config"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// mockConn for security bypass testing
type mockConn struct {
	net.Conn
	addr net.Addr
}

func (m *mockConn) RemoteAddr() net.Addr { return m.addr }

func TestMQTT_FullIntegration(t *testing.T) {
	// 1. Setup Environment
	appCfg := &config.AppConfig{}
	m := binder.NewManager(appCfg)
	security.GetEngine().Reset()
	defer security.GetEngine().Reset()
	sse.HubInstance.RemoveAllPublishHooks()
	defer sse.HubInstance.RemoveAllPublishHooks()
	
	// Create a temporary SQLite DB for persistence
	dbFile := t.TempDir() + "/mqtt_persis.db"
	
	bindConfig := fmt.Sprintf(`
DATABASE "sqlite://%s"
    NAME "mqtt_db"
END DATABASE

SECURITY "mqtt_firewall"
    CONNECTION DENY "1.2.3.4"
    CONNECTION ALLOW "127.0.0.1"
END SECURITY

TCP "127.0.0.1:0"
    MQTT ":0"
        STORAGE "mqtt_db"
        SECURITY "mqtt_firewall"
        
        OPTIONS DEFINE
            RETAIN ON
        END OPTIONS
        
        AUTH BEGIN
            allow();
        END AUTH
    END MQTT
END TCP
`, dbFile)

	// Helper to get active address with error detection
	getAddr := func(mgr *binder.Manager, errCh <-chan error) string {
		start := time.Now()
		for {
			select {
			case err := <-errCh:
				t.Fatalf("Manager started with error: %v", err)
			default:
				addrs := mgr.GetAddresses()
				if len(addrs) > 0 {
					return addrs[0]
				}
				if time.Since(start) > 10*time.Second {
					t.Fatal("Manager failed to provide active listener address in time")
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}

	// 2. Start Manager
	cfg, _, err := binder.ParseConfig(bindConfig)
	if err != nil {
		t.Fatalf("Failed to parse bind config: %v", err)
	}

	errCh1 := make(chan error, 1)
	go func() {
		if err := m.Start(cfg); err != nil {
			errCh1 <- err
		}
	}()
	defer m.Stop()

	addr := getAddr(m, errCh1)

	t.Run("Security Blocking", func(t *testing.T) {
		blocked := &mockConn{addr: &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 1234}}
		if security.GetEngine().AllowConnection(blocked, "mqtt_firewall") {
			t.Error("Expected 1.2.3.4 to be blocked by mqtt_firewall")
		}
		
		allowed := &mockConn{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}
		if !security.GetEngine().AllowConnection(allowed, "mqtt_firewall") {
			t.Error("Expected 127.0.0.1 to be allowed by mqtt_firewall")
		}
	})

	t.Run("PubSub Persistence with Restart", func(t *testing.T) {
		opts := mqtt.NewClientOptions().AddBroker("tcp://" + addr).SetClientID("test_client")
		opts.SetCleanSession(false)
		opts.SetConnectTimeout(5 * time.Second)
		
		client := mqtt.NewClient(opts)
		if token := client.Connect(); token.Wait() && token.Error() != nil {
			t.Fatalf("Failed to connect: %v", token.Error())
		}
		
		topic := "test/persistence"
		if token := client.Subscribe(topic, 1, nil); token.Wait() && token.Error() != nil {
			t.Fatalf("Failed to subscribe: %v", token.Error())
		}
		
		payload := "persisted message after restart"
		if token := client.Publish(topic, 1, true, payload); token.Wait() && token.Error() != nil {
			t.Fatalf("Failed to publish: %v", token.Error())
		}
		
		// Wait a bit for async storage write if any (though Mochi hooks are usually sync)
		time.Sleep(500 * time.Millisecond)

		// --- EXTRACTED VERIFICATION: Check DB before restart ---
		verifiedDB, err := db.FromURL("sqlite://" + dbFile)
		if err != nil {
			t.Fatalf("Failed to open DB for verification: %v", err)
		}
		var count int64
		verifiedDB.Table("mqtt_retaineds").Count(&count)
		t.Logf("Retained messages in DB before restart: %d", count)
		if count == 0 {
			t.Error("No retained messages found in DB before restart! Storage hook might not be working.")
		}
		// --------------------------------------------------------

		client.Disconnect(100)
		
		// --- STABILIZATION: FULL RESTART ---
		m.Stop()
		time.Sleep(2 * time.Second) // Ensure OS releases sockets and files
		
		cfg2, _, err := binder.ParseConfig(bindConfig)
		if err != nil {
			t.Fatalf("Failed to re-parse bind config: %v", err)
		}

		m2 := binder.NewManager(appCfg)
		errCh2 := make(chan error, 1)
		go func() {
			if err := m2.Start(cfg2); err != nil {
				errCh2 <- err
			}
		}()
		defer m2.Stop()
		
		addr2 := getAddr(m2, errCh2)
		// ----------------------------------
		// STABILIZATION: Allow Mochi's async srv.Serve() goroutine to finish restoring 
		// retained messages and subscriptions from the hook before the client connects.
		time.Sleep(1 * time.Second)
		
		received := make(chan string, 1)
		opts2 := mqtt.NewClientOptions().AddBroker("tcp://" + addr2).SetClientID("test_client_2")
		opts2.SetConnectTimeout(5 * time.Second)
		client2 := mqtt.NewClient(opts2)
		if token := client2.Connect(); token.Wait() && token.Error() != nil {
			t.Fatalf("Failed to connect 2nd client: %v", token.Error())
		}
		
		client2.Subscribe(topic, 1, func(c mqtt.Client, m mqtt.Message) {
			received <- string(m.Payload())
		})
		
		select {
		case msg := <-received:
			if msg != payload {
				t.Errorf("Expected %q, got %q", payload, msg)
			}
		case <-time.After(15 * time.Second):
			t.Error("Timed out waiting for persisted retained message")
		}
		client2.Disconnect(100)
	})
}
