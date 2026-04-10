package httpserver

import (
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

func TestWSProxy(t *testing.T) {
	// Backend App
	backend := fiber.New()
	backend.Get("/proxy/ws", websocket.New(func(c *websocket.Conn) {
		for {
			mt, msg, err := c.ReadMessage()
			if err != nil {
				break
			}
			msg = append([]byte("Echo: "), msg...)
			if err := c.WriteMessage(mt, msg); err != nil {
				break
			}
		}
	}))

	go func() {
		_ = backend.Listen(":9991", fiber.ListenConfig{DisableStartupMessage: true})
	}()
	time.Sleep(100 * time.Millisecond)

	// Proxy App
	proxyApp := fiber.New()
	proxyApp.Use("/proxy", WSProxy("ws://localhost:9991"))

	go func() {
		_ = proxyApp.Listen(":9992", fiber.ListenConfig{DisableStartupMessage: true})
	}()
	time.Sleep(100 * time.Millisecond)

	// Client
	dialer := fws.Dialer{}
	conn, _, err := dialer.Dial("ws://localhost:9992/proxy/ws", nil)
	if err != nil {
		t.Fatalf("Failed to connect to proxy: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(fws.TextMessage, []byte("Hello WS")); err != nil {
		t.Fatalf("Failed to write to proxy: %v", err)
	}

	mt, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read from proxy: %v", err)
	}

	if mt != fws.TextMessage {
		t.Fatalf("Expected plain text message, got type %d", mt)
	}

	if string(msg) != "Echo: Hello WS" {
		t.Fatalf("Expected 'Echo: Hello WS', got %q", string(msg))
	}

	_ = proxyApp.Shutdown()
	_ = backend.Shutdown()
}
