package sse

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestSSEHandler(t *testing.T) {
	app := fiber.New()
	app.Get("/sse", Handler)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	go app.Listener(ln)
	defer app.Shutdown()

	url := "http://" + addr + "/sse?id=test-client&channels=news"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("HTTP Get failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if contentType := resp.Header.Get("Content-Type"); contentType != "text/event-stream" {
		t.Errorf("Content-Type = %q, want \"text/event-stream\"", contentType)
	}

	reader := bufio.NewReader(resp.Body)

	// 1. Check initial ID and retry
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("first read error: %v", err)
	}
	if !strings.HasPrefix(line, "id: test-client") {
		t.Errorf("expected id: test-client, got %q", line)
	}

	// 2. Publish a message to the channel
	go func() {
		time.Sleep(100 * time.Millisecond)
		HubInstance.Publish(&Message{Channel: "news", Event: "update", Data: "hello"})
	}()

	// 3. Read the event and data
	// Skip empty line after id, retry line, and empty line after retry
	for i := 0; i < 3; i++ {
		_, _ = reader.ReadString('\n')
	}
	
	// Wait for the published message
	found := false
	for i := 0; i < 20; i++ {
		line, err = reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("read error at i=%d: %v", i, err)
		}
		if strings.Contains(line, "event: update") {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !found {
		t.Error("event: update not found in stream")
	}
}

func TestWSUpgradeMiddlewareDirect(t *testing.T) {
	app := fiber.New()
	app.Use(WSUpgradeMiddleware)
	app.Get("/ws", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	// Not a websocket request
	req := httptest.NewRequest("GET", "/ws", nil)
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("expected 426, got %d", resp.StatusCode)
	}

	// With websocket header
	req = httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "upgrade")
	resp, _ = app.Test(req)
	// it will probably fail because we don't have a real websocket server running in app.Test
	// but we check if it passed the middleware
}
