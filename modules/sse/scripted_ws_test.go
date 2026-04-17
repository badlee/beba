package sse

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	fastwebsocket "github.com/fasthttp/websocket"
	fiberwebsocket "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

// parseWSResponse décodes a raw WS frame into ResponseMsg.
func parseWSResponse(t *testing.T, raw []byte) ResponseMsg {
	t.Helper()
	var r ResponseMsg
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("cannot unmarshal WS response %q: %v", raw, err)
	}
	return r
}

// readWSMsg reads one text frame with a 2-second deadline.
func readWSMsg(t *testing.T, conn *fastwebsocket.Conn) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	return p
}

// sendWSJSON sends a JSON-encoded Message to the server.
func sendWSJSON(t *testing.T, conn *fastwebsocket.Conn, msg Message) {
	t.Helper()
	payload, _ := json.Marshal(msg)
	if err := conn.WriteMessage(fastwebsocket.TextMessage, payload); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}
}

// startWSServer spins up a Fiber WebSocket server and returns the address.
func startWSServer(t *testing.T, runner *ScriptedRunner) (addr string, shutdown func()) {
	t.Helper()
	app := fiber.New()
	app.Get("/ws", WSUpgradeMiddleware, fiberwebsocket.New(func(conn *fiberwebsocket.Conn) {
		WSHandler(conn, runner)
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go app.Listener(ln)
	return ln.Addr().String(), func() { app.Shutdown() }
}

// ────────────────────────────────────────────────────────────────
// 1. onMessage — ping → pong (text)
// ────────────────────────────────────────────────────────────────

func TestScriptedWSHandler(t *testing.T) {
	resetHub()
	runner := &ScriptedRunner{
		Code: `
			onMessage((raw) => {
				const msg = (typeof raw === "string") ? JSON.parse(raw) : raw;
				if (msg.data === "ping") {
					send("pong");
				} else {
					publish("test_chan", "echo: " + msg.data);
				}
			});
			subscribe("test_chan", (msg) => {
				send("chan_data: " + msg.data);
			});
		`,
		Protocol: "ws",
	}

	addr, stop := startWSServer(t, runner)
	defer stop()

	conn, _, err := fastwebsocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	// 1a. ping → pong
	sendWSJSON(t, conn, Message{Data: "ping"})
	r := parseWSResponse(t, readWSMsg(t, conn))
	if r.Data != "pong" {
		t.Errorf("got %q, want %q", r.Data, "pong")
	}

	// 1b. hello → publish → subscribe → send
	sendWSJSON(t, conn, Message{Data: "hello"})
	r = parseWSResponse(t, readWSMsg(t, conn))
	if r.Data != "chan_data: echo: hello" {
		t.Errorf("got %q, want %q", r.Data, "chan_data: echo: hello")
	}
}

// ────────────────────────────────────────────────────────────────
// 2. send(data, channel) — explicit channel
// ────────────────────────────────────────────────────────────────

func TestScriptedWS_SendWithChannel(t *testing.T) {
	runner := &ScriptedRunner{
		Code: `
			onMessage((raw) => {
				const msg = (typeof raw === "string") ? JSON.parse(raw) : raw;
				send(msg.data, "direct");
			});
		`,
	}
	addr, stop := startWSServer(t, runner)
	defer stop()

	conn, _, _ := fastwebsocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	defer conn.Close()

	sendWSJSON(t, conn, Message{Data: "hello-channel"})
	r := parseWSResponse(t, readWSMsg(t, conn))
	if r.Data != "hello-channel" {
		t.Errorf("send with channel: got %q, want %q", r.Data, "hello-channel")
	}
}

// ────────────────────────────────────────────────────────────────
// 3. unsubscribe — callbacks stop firing after unsubscribe
// ────────────────────────────────────────────────────────────────

func TestScriptedWS_Unsubscribe(t *testing.T) {
	runner := &ScriptedRunner{
		Code: `
			var count = 0;
			subscribe("unsub_chan", (msg) => {
				count++;
				send("hit:" + count);
			});
			onMessage((raw) => {
				const msg = (typeof raw === "string") ? JSON.parse(raw) : raw;
				if (msg.data === "unsub") {
					unsubscribe("unsub_chan");
					send("unsubscribed");
				} else {
					publish("unsub_chan", msg.data);
				}
			});
		`,
	}
	addr, stop := startWSServer(t, runner)
	defer stop()

	conn, _, _ := fastwebsocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	defer conn.Close()

	// Publish once → should trigger the subscribe callback
	sendWSJSON(t, conn, Message{Data: "trigger"})
	r := parseWSResponse(t, readWSMsg(t, conn))
	if r.Data != "hit:1" {
		t.Errorf("before unsubscribe: got %q, want %q", r.Data, "hit:1")
	}

	// Unsubscribe
	sendWSJSON(t, conn, Message{Data: "unsub"})
	r = parseWSResponse(t, readWSMsg(t, conn))
	if r.Data != "unsubscribed" {
		t.Errorf("unsubscribe ack: got %q, want %q", r.Data, "unsubscribed")
	}

	// Publish again — nothing should come back (timeout expected)
	sendWSJSON(t, conn, Message{Data: "trigger"})
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Error("expected timeout after unsubscribe, but got a message")
	}
}

// ────────────────────────────────────────────────────────────────
// 4. close() — JS closes the connection
// ────────────────────────────────────────────────────────────────

func TestScriptedWS_JSClose(t *testing.T) {
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
	addr, stop := startWSServer(t, runner)
	defer stop()

	conn, _, _ := fastwebsocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	defer conn.Close()

	sendWSJSON(t, conn, Message{Data: "bye"})
	// Read "closing"
	r := parseWSResponse(t, readWSMsg(t, conn))
	if r.Data != "closing" {
		t.Errorf("close signal: got %q, want %q", r.Data, "closing")
	}

	// Connection should be closed by the server
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Error("expected connection to be closed by server after close()")
	}
}

// ────────────────────────────────────────────────────────────────
// 5. onClose — fired when client disconnects
// ────────────────────────────────────────────────────────────────

func TestScriptedWS_OnClose(t *testing.T) {
	closeCh := make(chan struct{}, 1)

	// Use a dedicated Hub channel watched via a hook added BEFORE the runtime starts.
	HubInstance.AddPublishHook(func(msg *Message) {
		if msg.Channel == "ws_onclose_signal" {
			select {
			case closeCh <- struct{}{}:
			default:
			}
		}
	})
	defer HubInstance.RemoveAllPublishHooks()

	runner := &ScriptedRunner{
		Code: `
			onClose(() => {
				publish("ws_onclose_signal", "closed");
			});
		`,
	}
	addr, stop := startWSServer(t, runner)
	defer stop()

	conn, _, _ := fastwebsocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	time.Sleep(100 * time.Millisecond)
	conn.Close()

	select {
	case <-closeCh:
		// success
	case <-time.After(2 * time.Second):
		t.Error("onClose was not fired after client disconnect")
	}
}

// ────────────────────────────────────────────────────────────────
// 6. onError — fired on read error (server-injected)
// ────────────────────────────────────────────────────────────────

func TestScriptedWS_OnError(t *testing.T) {
	errorCh := make(chan string, 1)

	HubInstance.AddPublishHook(func(msg *Message) {
		if msg.Channel == "ws_error_signal" {
			select {
			case errorCh <- msg.Data:
			default:
			}
		}
	})
	defer HubInstance.RemoveAllPublishHooks()

	runner := &ScriptedRunner{
		Code: `
			onError((err) => {
				publish("ws_error_signal", "err:" + err);
			});
		`,
	}
	addr, stop := startWSServer(t, runner)
	defer stop()

	conn, _, _ := fastwebsocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	time.Sleep(100 * time.Millisecond)
	conn.NetConn().Close()

	select {
	case msg := <-errorCh:
		if msg == "" {
			t.Error("onError received empty message")
		}
		t.Logf("onError fired: %s", msg)
	case <-time.After(2 * time.Second):
		t.Error("onError was not fired after abrupt client disconnect")
	}
}

// ────────────────────────────────────────────────────────────────
// 7. binary message — onMessage receives []byte
// ────────────────────────────────────────────────────────────────

func TestScriptedWS_BinaryMessage(t *testing.T) {
	resetHub()
	runner := &ScriptedRunner{
		Code: `
			onMessage((raw) => {
				// binary arrives as Uint8Array in goja
				if (typeof raw !== "string") {
					send("binary:" + raw.length);
				}
			});
		`,
	}
	addr, stop := startWSServer(t, runner)
	defer stop()

	conn, _, _ := fastwebsocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	defer conn.Close()

	binaryData := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	conn.WriteMessage(fastwebsocket.BinaryMessage, binaryData)

	r := parseWSResponse(t, readWSMsg(t, conn))
	if r.Data != "binary:5" {
		t.Errorf("binary message: got %q, want %q", r.Data, "binary:5")
	}
}

type ResponseMsg struct {
	Channel string `json:"channel"`
	Data    string `json:"data"`
}
