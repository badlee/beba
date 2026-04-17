package sse

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// startSSEServer starts a Fiber SSE server with an optional scripted runner and returns addr + shutdown fn.
func startSSEServer(t *testing.T, runner *ScriptedRunner) (addr string, shutdown func()) {
	t.Helper()
	app := fiber.New()
	if runner != nil {
		app.Get("/sse", func(c fiber.Ctx) error {
			return Handler(c, runner)
		})
	} else {
		app.Get("/sse", func(c fiber.Ctx) error {
			return Handler(c)
		})
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go app.Listener(ln)
	return ln.Addr().String(), func() { app.Shutdown() }
}

// readSSELine reads one line from the SSE stream with a 2s deadline.
func readSSELine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("SSE read error: %v", err)
	}
	return strings.TrimRight(line, "\r\n")
}

// skipSSEPreamble skips the initial id: and retry: lines that every SSE connection sends.
func skipSSEPreamble(t *testing.T, r *bufio.Reader) {
	t.Helper()
	// id: <uuid>\n\n
	// retry: 5000\n\n
	for i := 0; i < 4; i++ {
		r.ReadString('\n')
	}
}

// ────────────────────────────────────────────────────────────────
// 1. subscribe + send — SSE delivers subscribed channel messages to the client
// ────────────────────────────────────────────────────────────────

func TestScriptedSSE_SubscribeAndSend(t *testing.T) {
	runner := &ScriptedRunner{
		Code: `
			subscribe("sse_test", (msg) => {
				send(msg.data, "message");
			});
		`,
	}
	addr, stop := startSSEServer(t, runner)
	defer stop()

	resp, err := http.Get("http://" + addr + "/sse?channels=sse_test")
	if err != nil {
		t.Fatalf("SSE connect failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q, want text/event-stream", ct)
	}

	reader := bufio.NewReader(resp.Body)
	skipSSEPreamble(t, reader)

	// Publish a hub message on the channel after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		HubInstance.Publish(&Message{Channel: "sse_test", Event: "update", Data: "hello-sse"})
	}()

	// Scan until we find the expected data line
	found := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line := readSSELine(t, reader)
		if strings.HasPrefix(line, "data: hello-sse") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'data: hello-sse' in SSE stream")
	}
}

// ────────────────────────────────────────────────────────────────
// 2. publish — JS publishes to Hub; other SSE client receives it
// ────────────────────────────────────────────────────────────────

func TestScriptedSSE_PublishBroadcast(t *testing.T) {
	// Passive SSE listener (no script)
	passiveAddr, stopPassive := startSSEServer(t, nil)
	defer stopPassive()

	// The scripted SSE connection that publishes on connect
	runner := &ScriptedRunner{
		Code: `
			// publish immediately on script execution
			publish("broadcast_chan", "from-js");
		`,
	}
	scriptedAddr, stopScripted := startSSEServer(t, runner)
	defer stopScripted()

	// Subscribe the passive client to broadcast_chan
	passiveResp, err := http.Get("http://" + passiveAddr + "/sse?channels=broadcast_chan")
	if err != nil {
		t.Fatalf("passive SSE connect failed: %v", err)
	}
	defer passiveResp.Body.Close()
	passiveReader := bufio.NewReader(passiveResp.Body)
	skipSSEPreamble(t, passiveReader)

	// Connect the scripted client (triggers the publish)
	go func() {
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get("http://" + scriptedAddr + "/sse?channels=broadcast_chan")
		if err == nil {
			time.Sleep(500 * time.Millisecond)
			resp.Body.Close()
		}
	}()

	found := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line := readSSELine(t, passiveReader)
		if strings.Contains(line, "from-js") {
			found = true
			break
		}
	}
	if !found {
		t.Error("passive SSE client did not receive published message")
	}
}

// ────────────────────────────────────────────────────────────────
// 3. onClose — fired when client disconnects
// ────────────────────────────────────────────────────────────────

func TestScriptedSSE_OnClose(t *testing.T) {
	var closeFired atomic.Bool

	HubInstance.RemoveAllPublishHooks()
	HubInstance.AddPublishHook(func(msg *Message) {
		if msg.Channel == "sse_onclose_signal" {
			closeFired.Store(true)
		}
	})
	defer HubInstance.RemoveAllPublishHooks()

	runner := &ScriptedRunner{
		Code: `
			onClose(() => {
				publish("sse_onclose_signal", "closed");
			});
		`,
	}
	addr, stop := startSSEServer(t, runner)
	defer stop()

	resp, err := http.Get("http://" + addr + "/sse")
	if err != nil {
		t.Fatalf("SSE connect failed: %v", err)
	}
	// Abruptly close the response body to simulate client disconnect
	time.Sleep(150 * time.Millisecond)
	resp.Body.Close()

	deadline := time.Now().Add(time.Second)
	for !closeFired.Load() && time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
	}
	if !closeFired.Load() {
		t.Error("onClose was not fired after SSE client disconnect")
	}
}

// ────────────────────────────────────────────────────────────────
// 4. SSE is passive — onMessage does NOT fire for SSE
// ────────────────────────────────────────────────────────────────

func TestScriptedSSE_NoOnMessage(t *testing.T) {
	var msgFired atomic.Bool
	HubInstance.RemoveAllPublishHooks()
	HubInstance.AddPublishHook(func(msg *Message) {
		if msg.Channel == "sse_msg_signal" {
			msgFired.Store(true)
		}
	})
	defer HubInstance.RemoveAllPublishHooks()

	runner := &ScriptedRunner{
		Code: `
			onMessage(() => {
				// This should NEVER be called for SSE
				publish("sse_msg_signal", "wrongly_called");
			});
		`,
	}
	addr, stop := startSSEServer(t, runner)
	defer stop()

	resp, err := http.Get("http://" + addr + "/sse")
	if err != nil {
		t.Fatalf("SSE connect failed: %v", err)
	}
	defer resp.Body.Close()

	time.Sleep(300 * time.Millisecond)

	if msgFired.Load() {
		t.Error("onMessage should NOT fire for SSE connections (SSE is server-push only)")
	}
}
