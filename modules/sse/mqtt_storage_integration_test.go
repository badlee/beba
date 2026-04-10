package sse

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"http-server/modules/db"
	paho "github.com/eclipse/paho.mqtt.golang"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	dbPath := filepath.Join(t.TempDir(), "mqtt_test.db")
	dbConn, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	db.NewConnection(dbConn, "TEST")

	return dbConn
}

func startBroker(t *testing.T, withStorage bool, storageDB *gorm.DB) (string, func()) {
	var sDB *gorm.DB
	if withStorage {
		sDB = storageDB
	}

	cfg := MQTTConfig{
		ListenerAddress: ":0",
		StorageDB:       sDB,
	}

	srv, err := NewMQTTServer(cfg, nil)
	if err != nil {
		t.Fatalf("failed to start broker: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startTestBroker listen: %v", err)
	}
	addr := ln.Addr().String()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			srv.ServeConn(conn)
		}
	}()

	return addr, func() {
		ln.Close()
		srv.Close()
	}
}

func TestMQTTIntegration_StoragePersistence(t *testing.T) {
	storageDB := setupTestDB(t)

	// --- SCENARIO 1: WITH STORAGE ---
	t.Run("With Storage - Messages survive restart", func(t *testing.T) {
		addr, teardown := startBroker(t, true, storageDB)
		
		// Publisher connects and publishes retained message
		pub := newPahoClient(t, addr, "pub1", "", "")
		if tok := pub.Connect(); tok.Wait() && tok.Error() != nil { t.Fatalf("connect: %v", tok.Error()) }
		
		if tok := pub.Publish("storage/test", 1, true, "survive"); tok.Wait() && tok.Error() != nil {
			t.Fatalf("publish: %v", tok.Error())
		}
		pub.Disconnect(100)
		
		// Stop the broker
		teardown()
		time.Sleep(100 * time.Millisecond)
		
		// Restart the broker with the SAME storage
		addr2, teardown2 := startBroker(t, true, storageDB)
		defer teardown2()
		
		// Subscriber connects and should immediately receive the retained message
		sub := newPahoClient(t, addr2, "sub1", "", "")
		if tok := sub.Connect(); tok.Wait() && tok.Error() != nil { t.Fatalf("connect: %v", tok.Error()) }
		defer sub.Disconnect(100)
		
		msgChan := make(chan string, 1)
		sub.Subscribe("storage/test", 1, func(_ paho.Client, msg paho.Message) {
			msgChan <- string(msg.Payload())
		})
		
		select {
		case msg := <-msgChan:
			if msg != "survive" {
				t.Fatalf("expected 'survive', got '%s'", msg)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for retained message after restart (STORAGE failed)")
		}
	})

	// --- SCENARIO 2: WITHOUT STORAGE ---
	t.Run("Without Storage - Messages lost on restart", func(t *testing.T) {
		addr, teardown := startBroker(t, false, nil)
		
		// Publisher connects and publishes retained message
		pub := newPahoClient(t, addr, "pub2", "", "")
		if tok := pub.Connect(); tok.Wait() && tok.Error() != nil { t.Fatalf("connect: %v", tok.Error()) }
		
		if tok := pub.Publish("nostorage/test", 1, true, "lost"); tok.Wait() && tok.Error() != nil {
			t.Fatalf("publish: %v", tok.Error())
		}
		pub.Disconnect(100)
		
		// Stop the broker
		teardown()
		time.Sleep(100 * time.Millisecond)
		
		// Restart the broker
		addr2, teardown2 := startBroker(t, false, nil)
		defer teardown2()
		
		// Subscriber connects and should NOT receive anything
		sub := newPahoClient(t, addr2, "sub2", "", "")
		if tok := sub.Connect(); tok.Wait() && tok.Error() != nil { t.Fatalf("connect: %v", tok.Error()) }
		defer sub.Disconnect(100)
		
		msgChan := make(chan string, 1)
		sub.Subscribe("nostorage/test", 1, func(_ paho.Client, msg paho.Message) {
			msgChan <- string(msg.Payload())
		})
		
		select {
		case <-msgChan:
			t.Fatal("received retained message, but expected it to be lost (WITHOUT STORAGE failed)")
		case <-time.After(500 * time.Millisecond):
			// Success, message was lost in RAM as expected
		}
	})
}
