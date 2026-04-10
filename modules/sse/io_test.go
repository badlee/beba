package sse

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
)

func TestSIOHandler(t *testing.T) {
	app := fiber.New()
	
	// Middleware to simulate upgrade check as per io.go comments
	app.Use("/sio", func(c fiber.Ctx) error {
		// In test, IsWebSocketUpgrade might not work the same way as in a real server 
		// if we use app.Test, but here we use a real listener.
		return c.Next()
	})
	app.Get("/sio", SIOHandler())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	go app.Listener(ln)
	defer app.Shutdown()

	// Connect to socket.io (which is just WS in this implementation)
	url := "ws://" + addr + "/sio"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	// 1. Wait for connection to be registered in sioRegistry
	time.Sleep(100 * time.Millisecond)
	
	sioReg.mu.RLock()
	clientCount := len(sioReg.clients)
	sioReg.mu.RUnlock()
	
	if clientCount == 0 {
		t.Fatal("Client not found in sioRegistry")
	}

	// 2. Test receiving a message from Hub via SIOPublish
	testMsg := Message{Channel: "global", Event: "sio_event", Data: "hello_sio"}
	SIOPublish(testMsg.Channel, testMsg.Event, testMsg.Data)

	_, p, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var received sioMessage
	if err := json.Unmarshal(p, &received); err != nil {
		t.Fatalf("Unmarshal failed: %v. Raw: %s", err, string(p))
	}
	if received.Data != "hello_sio" {
		t.Errorf("received data = %q, want %q", received.Data, "hello_sio")
	}

	// 3. Test sending a message to Hub
	clientMsg := sioMessage{Channel: "global", Event: "from_sio", Data: "hi_from_sio"}
	payload, _ := json.Marshal(clientMsg)
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	ch := newTestClient(t, "listener", []string{"global"})
	time.Sleep(100 * time.Millisecond)
	
	// Resend to ensure reception
	conn.WriteMessage(websocket.TextMessage, payload)

	msg := recv(t, ch, "hub received from sio")
	if msg.Data != "hi_from_sio" {
		t.Errorf("hub received %q, want %q", msg.Data, "hi_from_sio")
	}
}
