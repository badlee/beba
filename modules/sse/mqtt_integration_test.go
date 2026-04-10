package sse

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// newPahoClient creates a test paho MQTT client connected to the given address.
func newPahoClient(t *testing.T, addr, id, user, pass string) paho.Client {
	t.Helper()
	opts := paho.NewClientOptions()
	opts.AddBroker("tcp://" + addr)
	opts.SetClientID(id)
	if user != "" {
		opts.SetUsername(user)
		opts.SetPassword(pass)
	}
	opts.SetConnectTimeout(5 * time.Second)
	opts.SetAutoReconnect(false)
	return paho.NewClient(opts)
}

// startTestBroker starts a real Mochi TCP broker on a dynamic port with auth and ACL.
// Returns the broker address and a teardown func.
func startTestBroker(t *testing.T) (string, func()) {
	t.Helper()

	hd := NewMQTTHooksDispatcher(".", map[string]string{})

	// AUTH: admin/test or guest (no password required)
	hd.AddAuthBlock("SCRIPT", `
		if (user === "admin" && pwd === "test") {
			allow();
		} else if (user === "guest") {
			allow();
		} else {
			reject("unauthorized: " + user);
		}
	`, true, nil)

	// ACL: guests cannot write
	hd.AddACLRoute(&MQTTRoute{
		Type:   "JS",
		Inline: true,
		Handler: `
			if (user === "guest" && mode === "WRITE") {
				reject("guests are read-only");
			} else {
				allow();
			}
		`,
	})

	// ON PUBLISH logger
	hd.AddONRoute(&MQTTRoute{
		Method:  "PUBLISH",
		Type:    "JS",
		Inline:  true,
		Handler: `print("[TEST BROKER] " + clientId + " → " + eventData.topic + ": " + eventData.payload);`,
	})

	cfg := MQTTConfig{
		ListenerAddress: ":0",
		Auth:            hd.AuthFunc(),
		OnConnect:       hd.OnConnectFunc(),
		OnDisconnect:    hd.OnDisconnectFunc(),
		OnPublish:       hd.OnPublishFunc(),
		DynamicHook:     NewDynamicMochiHook(hd),
	}

	srv, err := NewMQTTServer(cfg, nil)
	if err != nil {
		t.Fatalf("startTestBroker: %v", err)
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

// TestMQTTIntegration_PubSubDelivery verifies end-to-end message delivery.
func TestMQTTIntegration_PubSubDelivery(t *testing.T) {
	addr, teardown := startTestBroker(t)
	defer teardown()

	// Subscriber (guest)
	sub := newPahoClient(t, addr, "test-sub", "guest", "")
	if tok := sub.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("sub connect: %v", tok.Error())
	}
	defer sub.Disconnect(100)

	var mu sync.Mutex
	received := []string{}
	sub.Subscribe("sensors/#", 0, func(_ paho.Client, msg paho.Message) {
		mu.Lock()
		received = append(received, fmt.Sprintf("%s=%s", msg.Topic(), string(msg.Payload())))
		mu.Unlock()
	})
	time.Sleep(100 * time.Millisecond)

	// Publisher (admin)
	pub := newPahoClient(t, addr, "test-pub", "admin", "test")
	if tok := pub.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("pub connect: %v", tok.Error())
	}
	defer pub.Disconnect(100)

	pub.Publish("sensors/temp", 0, false, "22.5")
	pub.Publish("sensors/humidity", 0, false, "68")
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(received), received)
	}
	t.Logf("✅ Received: %v", received)
}

// TestMQTTIntegration_BadAuthRejected ensures invalid credentials are blocked.
func TestMQTTIntegration_BadAuthRejected(t *testing.T) {
	addr, teardown := startTestBroker(t)
	defer teardown()

	cl := newPahoClient(t, addr, "hacker", "hacker", "wrong")
	tok := cl.Connect()
	tok.Wait()

	if tok.Error() == nil && cl.IsConnected() {
		cl.Disconnect(100)
		t.Fatal("expected connection to be refused")
	}
	t.Logf("✅ Connection correctly refused: %v", tok.Error())
}

// TestMQTTIntegration_GuestReadOnly ensures guests cannot publish.
func TestMQTTIntegration_GuestReadOnly(t *testing.T) {
	addr, teardown := startTestBroker(t)
	defer teardown()

	// Subscribe with admin to detect any rogue guest message
	admin := newPahoClient(t, addr, "acl-admin", "admin", "test")
	if tok := admin.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("admin connect: %v", tok.Error())
	}
	defer admin.Disconnect(100)

	gotMsg := false
	admin.Subscribe("sensors/+", 0, func(_ paho.Client, msg paho.Message) {
		if string(msg.Payload()) == "tamper" {
			gotMsg = true
		}
	})
	time.Sleep(100 * time.Millisecond)

	// Guest tries to publish (should be blocked by OnACLCheck)
	guest := newPahoClient(t, addr, "acl-guest", "guest", "")
	if tok := guest.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("guest connect: %v", tok.Error())
	}
	defer guest.Disconnect(100)

	guest.Publish("sensors/temp", 0, false, "tamper")
	time.Sleep(300 * time.Millisecond)

	if gotMsg {
		t.Fatal("expected guest WRITE to be blocked by ACL, but message was delivered")
	}
	t.Log("✅ Guest write correctly blocked by JS ACL")
}

// TestMQTTIntegration_QoSAndRetain verifies QoS levels, Retained messages, and persistent session re-emissions.
func TestMQTTIntegration_QoSAndRetain(t *testing.T) {
	addr, teardown := startTestBroker(t)
	defer teardown()

	// 1. Publisher publishes a retained message
	pub := newPahoClient(t, addr, "qos-pub", "admin", "test")
	if tok := pub.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("pub connect: %v", tok.Error())
	}
	defer pub.Disconnect(100)

	if tok := pub.Publish("state/device1", 1, true, "online"); tok.Wait() && tok.Error() != nil {
		t.Fatalf("publish retain: %v", tok.Error())
	}
	time.Sleep(100 * time.Millisecond)

	// 2. Subscriber connects AFTER publish, should get retained message
	sub1 := newPahoClient(t, addr, "qos-sub1", "admin", "test")
	if tok := sub1.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("sub1 connect: %v", tok.Error())
	}

	var mu sync.Mutex
	retainedMsg := ""
	sub1.Subscribe("state/device1", 1, func(_ paho.Client, msg paho.Message) {
		mu.Lock()
		retainedMsg = string(msg.Payload())
		mu.Unlock()
	})
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	if retainedMsg != "online" {
		t.Fatalf("expected retained message 'online', got '%s'", retainedMsg)
	}
	mu.Unlock()
	t.Log("✅ Retained message correctly delivered")
	sub1.Disconnect(100)

	// 3. Persistent session re-emission test
	// Create client with CleanSession=false
	opts := paho.NewClientOptions()
	opts.AddBroker("tcp://" + addr)
	opts.SetClientID("persistent-sub")
	opts.SetUsername("admin")
	opts.SetPassword("test")
	opts.SetCleanSession(false)

	subPersist := paho.NewClient(opts)
	if tok := subPersist.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("persistent sub connect: %v", tok.Error())
	}

	// Subscribe to QoS 1 topic
	subPersist.Subscribe("queue/jobs", 1, nil)
	time.Sleep(100 * time.Millisecond)
	subPersist.Disconnect(100) // Disconnect!

	// 4. Publish a message while subscriber is offline
	if tok := pub.Publish("queue/jobs", 1, false, "job-123"); tok.Wait() && tok.Error() != nil {
		t.Fatalf("publish while offline: %v", tok.Error())
	}
	time.Sleep(100 * time.Millisecond)

	// 5. Reconnect persistent subscriber and check if it receives the queued message
	var queuedMsg string
	subPersist = paho.NewClient(opts)
	// Must set a default handler to catch offline-queued messages emitted right after connack
	subPersist.AddRoute("queue/jobs", func(_ paho.Client, msg paho.Message) {
		mu.Lock()
		queuedMsg = string(msg.Payload())
		mu.Unlock()
	})

	if tok := subPersist.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("persistent sub reconnect: %v", tok.Error())
	}

	time.Sleep(300 * time.Millisecond)
	subPersist.Disconnect(100)

	mu.Lock()
	if queuedMsg != "job-123" {
		t.Fatalf("expected offline queued message 'job-123', got '%s'", queuedMsg)
	}
	mu.Unlock()
	t.Log("✅ Persistent session re-emitted offline QoS 1 message")
}
