package sse

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	fastwebsocket "github.com/fasthttp/websocket"
	fiberwebsocket "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

func TestWSUpgradeMiddleware(t *testing.T) {
	app := fiber.New()
	app.Get("/ws", WSUpgradeMiddleware, func(c fiber.Ctx) error {
		return c.SendString("upgraded")
	})

	// Test non-websocket request
	req, _ := http.NewRequest("GET", "/ws", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusUpgradeRequired {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusUpgradeRequired)
	}
}

// TestWSHandler focuses on the bridging logic.
// We'll use a real listener to test the websocket connection.
func TestWSHandler(t *testing.T) {
	app := fiber.New()
	app.Get("/ws", fiberwebsocket.New(WSHandler))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	go app.Listener(ln)
	defer app.Shutdown()

	// Connect to websocket
	url := "ws://" + addr + "/ws"
	conn, _, err := fastwebsocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	// 1. Test receiving a message from Hub
	testMsg := &Message{Channel: "global", Event: "test", Data: "hello_ws"}
	HubInstance.Publish(testMsg)

	// Read message from websocket
	_, p, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	var received Message
	if err := json.Unmarshal(p, &received); err != nil {
		t.Fatalf("Unmarshal failed: %v. Raw: %s", err, string(p))
	}
	if received.Data != "hello_ws" {
		t.Errorf("received data = %q, want %q", received.Data, "hello_ws")
	}

	// 2. Test sending a message to Hub
	clientMsg := Message{Channel: "global", Event: "from_client", Data: "hi_hub"}
	payload, _ := json.Marshal(clientMsg)
	if err := conn.WriteMessage(fastwebsocket.TextMessage, payload); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	// Verify Hub received it (we need a separate client to listen)
	ch := newTestClient(t, "listener", []string{"global"})
	time.Sleep(100 * time.Millisecond) // wait for propagation

	// Resend from WS because the listener might have missed the first one if not subscribed yet
	conn.WriteMessage(fastwebsocket.TextMessage, payload)

	msg := recv(t, ch, "hub received from ws")
	if msg.Data != "hi_hub" {
		t.Errorf("hub received %q, want %q", msg.Data, "hi_hub")
	}
}
