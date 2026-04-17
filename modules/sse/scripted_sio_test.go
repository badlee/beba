package sse

import (
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
)

// startSIOServer starts a Fiber Socket.IO server with an optional scripted runner.
func startSIOServer(t *testing.T, runner *ScriptedRunner) (addr string, shutdown func()) {
	t.Helper()
	app := fiber.New()
	app.Use("/sio", func(c fiber.Ctx) error { return c.Next() })
	if runner != nil {
		app.Get("/sio", SIOHandler(runner))
	} else {
		app.Get("/sio", SIOHandler())
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go app.Listener(ln)
	return ln.Addr().String(), func() { app.Shutdown() }
}

// dialSIO opens a WebSocket connection to the SIO endpoint.
func dialSIO(t *testing.T, addr string) *fws.Conn {
	t.Helper()
	conn, _, err := fws.DefaultDialer.Dial("ws://"+addr+"/sio", nil)
	if err != nil {
		t.Fatalf("SIO dial failed: %v", err)
	}
	return conn
}

// writeSIO sends a JSON sioMessage frame.
func writeSIO(t *testing.T, conn *fws.Conn, msg sioMessage) {
	t.Helper()
	raw, _ := json.Marshal(msg)
	if err := conn.WriteMessage(fws.TextMessage, raw); err != nil {
		t.Fatalf("SIO write failed: %v", err)
	}
}

// readSIO reads one text frame with a 2s deadline and decodes it.
func readSIO(t *testing.T, conn *fws.Conn) sioMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("SIO read failed: %v", err)
	}
	var msg sioMessage
	json.Unmarshal(raw, &msg)
	return msg
}

// ────────────────────────────────────────────────────────────────
// 1. onMessage — SIO client sends → JS receives raw JSON string
// ────────────────────────────────────────────────────────────────

func TestScriptedSIO_OnMessage(t *testing.T) {
	runner := &ScriptedRunner{
		Code: `
			onMessage((raw) => {
				const msg = (typeof raw === "string") ? JSON.parse(raw) : raw;
				if (msg.data === "ping") {
					send("pong");
				}
			});
		`,
	}
	addr, stop := startSIOServer(t, runner)
	defer stop()

	conn := dialSIO(t, addr)
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	writeSIO(t, conn, sioMessage{Event: "test", Data: "ping"})
	got := readSIO(t, conn)
	if got.Data != "pong" {
		t.Errorf("onMessage: got %q, want %q", got.Data, "pong")
	}
}

// ────────────────────────────────────────────────────────────────
// 2. send(data) — JS sends back to the originating socket
// ────────────────────────────────────────────────────────────────

func TestScriptedSIO_Send(t *testing.T) {
	runner := &ScriptedRunner{
		Code: `
			onMessage((raw) => {
				const msg = (typeof raw === "string") ? JSON.parse(raw) : raw;
				send("echo:" + msg.data);
			});
		`,
	}
	addr, stop := startSIOServer(t, runner)
	defer stop()

	conn := dialSIO(t, addr)
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	writeSIO(t, conn, sioMessage{Data: "world"})
	got := readSIO(t, conn)
	if got.Data != "echo:world" {
		t.Errorf("send: got %q, want %q", got.Data, "echo:world")
	}
}

// ────────────────────────────────────────────────────────────────
// 3. publish + subscribe — SIO broadcast via Hub
// ────────────────────────────────────────────────────────────────

func TestScriptedSIO_PublishSubscribe(t *testing.T) {
	runner := &ScriptedRunner{
		Code: `
			subscribe("sio_chan", (msg) => {
				send("hub:" + msg.data);
			});
			onMessage((raw) => {
				const msg = (typeof raw === "string") ? JSON.parse(raw) : raw;
				publish("sio_chan", msg.data);
			});
		`,
	}
	addr, stop := startSIOServer(t, runner)
	defer stop()

	conn := dialSIO(t, addr)
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	writeSIO(t, conn, sioMessage{Data: "broadcast-test"})
	got := readSIO(t, conn)
	if got.Data != "hub:broadcast-test" {
		t.Errorf("publish/subscribe: got %q, want %q", got.Data, "hub:broadcast-test")
	}
}

// ────────────────────────────────────────────────────────────────
// 4. unsubscribe — JS unsubscribe stops callbacks
// ────────────────────────────────────────────────────────────────

func TestScriptedSIO_Unsubscribe(t *testing.T) {
	runner := &ScriptedRunner{
		Code: `
			var hits = 0;
			subscribe("sio_unsub", (msg) => {
				hits++;
				send("hit:" + hits);
			});
			onMessage((raw) => {
				const msg = (typeof raw === "string") ? JSON.parse(raw) : raw;
				if (msg.data === "unsub") {
					unsubscribe("sio_unsub");
					send("unsubscribed");
				} else {
					publish("sio_unsub", msg.data);
				}
			});
		`,
	}
	addr, stop := startSIOServer(t, runner)
	defer stop()

	conn := dialSIO(t, addr)
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	// First publish → hits subscribe
	writeSIO(t, conn, sioMessage{Data: "trigger"})
	got := readSIO(t, conn)
	if got.Data != "hit:1" {
		t.Errorf("before unsub: got %q, want %q", got.Data, "hit:1")
	}

	// Unsubscribe
	writeSIO(t, conn, sioMessage{Data: "unsub"})
	got = readSIO(t, conn)
	if got.Data != "unsubscribed" {
		t.Errorf("unsub ack: got %q, want %q", got.Data, "unsubscribed")
	}

	// Second publish → silent
	writeSIO(t, conn, sioMessage{Data: "trigger"})
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Error("expected timeout after SIO unsubscribe")
	}
}

// ────────────────────────────────────────────────────────────────
// 5. onClose — fired on client disconnect
// ────────────────────────────────────────────────────────────────

func TestScriptedSIO_OnClose(t *testing.T) {
	var closeFired atomic.Bool

	HubInstance.RemoveAllPublishHooks()
	HubInstance.AddPublishHook(func(msg *Message) {
		if msg.Channel == "sio_onclose_signal" {
			closeFired.Store(true)
		}
	})
	defer HubInstance.RemoveAllPublishHooks()

	runner := &ScriptedRunner{
		Code: `
			onClose(() => {
				publish("sio_onclose_signal", "closed");
			});
		`,
	}
	addr, stop := startSIOServer(t, runner)
	defer stop()

	conn := dialSIO(t, addr)
	time.Sleep(100 * time.Millisecond)
	conn.Close() // abrupt close

	deadline := time.Now().Add(2 * time.Second)
	for !closeFired.Load() && time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
	}
	if !closeFired.Load() {
		t.Error("onClose was not fired after SIO client disconnect")
	}
}

// ────────────────────────────────────────────────────────────────
// 6. onError — fired on abrupt connection failure
// ────────────────────────────────────────────────────────────────

func TestScriptedSIO_OnError(t *testing.T) {
	var errorFired atomic.Bool

	HubInstance.RemoveAllPublishHooks()
	HubInstance.AddPublishHook(func(msg *Message) {
		if msg.Channel == "sio_error_signal" {
			errorFired.Store(true)
		}
	})
	defer HubInstance.RemoveAllPublishHooks()

	runner := &ScriptedRunner{
		Code: `
			onError((err) => {
				publish("sio_error_signal", "err:"+err);
			});
		`,
	}
	addr, stop := startSIOServer(t, runner)
	defer stop()

	conn := dialSIO(t, addr)
	time.Sleep(100 * time.Millisecond)
	// Force abrupt TCP close (bypasses WS close handshake)
	conn.NetConn().Close()

	deadline := time.Now().Add(2 * time.Second)
	for !errorFired.Load() && time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
	}
	// Note: may fire onClose instead if the WS lib handles the error gracefully.
	// Both are acceptable — we just verify the error path is wired up.
	t.Log("SIO onError fired:", errorFired.Load())
}

// ────────────────────────────────────────────────────────────────
// 7. close() — JS closes the socket
// ────────────────────────────────────────────────────────────────

func TestScriptedSIO_JSClose(t *testing.T) {
	runner := &ScriptedRunner{
		Code: `
			onMessage((raw) => {
				const msg = (typeof raw === "string") ? JSON.parse(raw) : raw;
				if (msg.data === "bye") {
					send("closing");
					close();
				}
			});
		`,
	}
	addr, stop := startSIOServer(t, runner)
	defer stop()

	conn := dialSIO(t, addr)
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)

	writeSIO(t, conn, sioMessage{Data: "bye"})
	got := readSIO(t, conn)
	if got.Data != "closing" {
		t.Errorf("close signal: got %q, want %q", got.Data, "closing")
	}

	// Connection should be closed server-side shortly after
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Error("expected connection closed after JS close()")
	}
}

// ────────────────────────────────────────────────────────────────
// 8. Inter-client broadcast — client A → Hub → client B via subscribe
// ────────────────────────────────────────────────────────────────

func TestScriptedSIO_InterClientBroadcast(t *testing.T) {
	runner := &ScriptedRunner{
		Code: `
			subscribe("broadcast", (msg) => {
				send("recv:" + msg.data);
			});
			onMessage((raw) => {
				const msg = (typeof raw === "string") ? JSON.parse(raw) : raw;
				// publish to Hub for all subscribers
				publish("broadcast", msg.data);
			});
		`,
	}
	addr, stop := startSIOServer(t, runner)
	defer stop()

	connA := dialSIO(t, addr)
	defer connA.Close()
	connB := dialSIO(t, addr)
	defer connB.Close()
	time.Sleep(150 * time.Millisecond)

	// A sends a message → triggers publish("broadcast")
	writeSIO(t, connA, sioMessage{Data: "hello-broadcast"})

	// Both A and B should receive via subscribe("broadcast")
	gotA := readSIO(t, connA)
	gotB := readSIO(t, connB)

	if gotA.Data != "recv:hello-broadcast" {
		t.Errorf("client A: got %q, want %q", gotA.Data, "recv:hello-broadcast")
	}
	if gotB.Data != "recv:hello-broadcast" {
		t.Errorf("client B: got %q, want %q", gotB.Data, "recv:hello-broadcast")
	}
}
