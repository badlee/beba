package sse

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	fws "github.com/fasthttp/websocket"
	fiberwebsocket "github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

const mosquittoPub = "/usr/local/bin/mosquitto_pub"
const mosquittoSub = "/usr/local/bin/mosquitto_sub"

// startMQTTScriptedApp starts a native TCP MQTT broker with an optional scripted runner
// attached via the Hub publish hook. Returns the TCP port and a shutdown function.
func startMQTTScriptedApp(t *testing.T, runner *ScriptedRunner, cfg MQTTConfig) (port int, shutdown func()) {
	t.Helper()
	// No global cleanup here anymore, we do it in tests or via returned shutdown

	// Allocate a free port, then release it so mochi can bind it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg.ListenerAddress = fmt.Sprintf("127.0.0.1:%d", port)

	ms, err := NewMQTTServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewMQTTServer: %v", err)
	}

	// Retrieve the actual bound port if we used :0
	if strings.HasSuffix(cfg.ListenerAddress, ":0") {
		// This is a bit hacky but works for mochi-mqtt v2 listeners
		// We can't easily get it since Serve() is in a goroutine.
		// However, the test allocated a free port previously.
		// Let's go back to the allocation method but KEEP the listener open until we are sure.
	}

	// If a runner is requested, attach Hub hook that feeds every incoming MQTT
	// publish (Source=="mqtt") into the scripted runtime's onMessage.
	// We piggyback off startMQTTApp's WS endpoint only when needed; for TCP tests
	// we only need the broker.
	var wsShutdown func()
	if runner != nil {
		app := fiber.New()
		// Ensure the handler uses the EXACT SAME server instance we just created
		sharedCfg := cfg
		sharedCfg.SharedServer = ms
		app.Use("/mqtt", MQTTUpgradeMiddleware)
		app.Get("/mqtt", fiberwebsocket.New(func(conn *fiberwebsocket.Conn) {
			MQTTHandler(sharedCfg, runner)(conn)
		}))
		wsLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go app.Listener(wsLn, fiber.ListenConfig{DisableStartupMessage: true})
		
		var clientConn *fws.Conn
		wsShutdown = func() { 
			if clientConn != nil {
				clientConn.Close()
			}
			app.Shutdown() 
		}

		// Trigger the handler in the background so the JS runtime starts
		go func() {
			time.Sleep(200 * time.Millisecond) // wait for server to start
			u := fmt.Sprintf("ws://%s/mqtt", wsLn.Addr().String())
			opts := pahomqtt.NewClientOptions().AddBroker(u).SetClientID("runner-trigger")
			c := pahomqtt.NewClient(opts)
			if tok := c.Connect(); tok.Wait() && tok.Error() == nil {
				// Keep it alive so the JS runtime doesn't shutdown immediately
				time.Sleep(5 * time.Second)
				c.Disconnect(250)
			}
		}()
	}

	// wait for broker to be ready 
	time.Sleep(200 * time.Millisecond) 
	
	return port, func() {
		ms.Close()
		if wsShutdown != nil {
			wsShutdown()
		}
	}
}

// scriptedPahoConnect creates a connected paho MQTT client on a TCP port.
func scriptedPahoConnect(t *testing.T, port int, id string) pahomqtt.Client {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	c := newPahoClient(t, addr, id, "", "")
	
	var err error
	for i := 0; i < 10; i++ {
		if tok := c.Connect(); tok.Wait() && tok.Error() != nil {
			err = tok.Error()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return c
	}
	t.Fatalf("paho connect [%s] failed after 10 retries: %v", id, err)
	return nil
}

// ────────────────────────────────────────────────────────────────
// 1. onMessage (paho) — JS receives { topic, payload }
// ────────────────────────────────────────────────────────────────

func TestScriptedMQTT_OnMessage_Paho(t *testing.T) {
	var gotTopic, gotPayload atomic.Value

	HubInstance.RemoveAllPublishHooks()
	HubInstance.AddPublishHook(func(msg *Message) {
		if msg.Channel == "mqtt_msg_result" {
			var parts map[string]string
			if json.Unmarshal([]byte(msg.Data), &parts) == nil {
				gotTopic.Store(parts["topic"])
				gotPayload.Store(parts["payload"])
			}
		}
	})
	defer HubInstance.RemoveAllPublishHooks()

	runner := &ScriptedRunner{
		Code: `
			onMessage((msg) => {
				const m = (typeof msg === "string") ? JSON.parse(msg) : msg;
				publish("mqtt_msg_result", JSON.stringify({
					topic:   m.topic   !== undefined ? m.topic   : (m.channel || ""),
					payload: m.payload !== undefined ? m.payload : (m.data    || "")
				}));
			});
		`,
	}

	port, stop := startMQTTScriptedApp(t, runner, MQTTConfig{})
	defer stop()

	cli := scriptedPahoConnect(t, port, "paho-pub-1")
	defer cli.Disconnect(200)
	time.Sleep(100 * time.Millisecond)

	cli.Publish("sensors/temp", 0, false, "42.5")

	deadline := time.Now().Add(2 * time.Second)
	for gotTopic.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if gotTopic.Load() == nil {
		t.Fatal("onMessage was not called after MQTT publish")
	}
	if gotTopic.Load().(string) != "sensors/temp" {
		t.Errorf("topic: got %q, want %q", gotTopic.Load(), "sensors/temp")
	}
	if gotPayload.Load().(string) != "42.5" {
		t.Errorf("payload: got %q, want %q", gotPayload.Load(), "42.5")
	}
}

// ────────────────────────────────────────────────────────────────
// 2. JS publish → paho subscriber receives it
// ────────────────────────────────────────────────────────────────

func TestScriptedMQTT_JSPublish_PahoReceives(t *testing.T) {
	received := make(chan string, 1)

	runner := &ScriptedRunner{
		Code: `
			onMessage((msg) => {
				const m = (typeof msg === "string") ? JSON.parse(msg) : msg;
				const v = m.payload !== undefined ? m.payload : (m.data || "");
				publish("js/output", "echo:" + v);
			});
		`,
	}

	port, stop := startMQTTScriptedApp(t, runner, MQTTConfig{})
	defer stop()

	subCli := scriptedPahoConnect(t, port, "paho-sub-2")
	defer subCli.Disconnect(200)
	subCli.Subscribe("js/output", 0, func(_ pahomqtt.Client, msg pahomqtt.Message) {
		select {
		case received <- string(msg.Payload()):
		default:
		}
	})
	time.Sleep(150 * time.Millisecond)

	pubCli := scriptedPahoConnect(t, port, "paho-pub-2")
	defer pubCli.Disconnect(200)
	pubCli.Publish("input/data", 0, false, "hello-js")

	select {
	case msg := <-received:
		if !strings.Contains(msg, "hello-js") {
			t.Errorf("got %q, expected to contain 'hello-js'", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("paho subscriber did not receive JS-published message")
	}
}

// ────────────────────────────────────────────────────────────────
// 3. subscribe (JS) + Hub → paho subscriber
// ────────────────────────────────────────────────────────────────

func TestScriptedMQTT_Subscribe_HubToMQTT(t *testing.T) {
	received := make(chan string, 1)

	runner := &ScriptedRunner{
		Code: `
			subscribe("alert_channel", (msg) => {
				publish("mqtt/alerts", msg.data);
			});
		`,
	}

	port, stop := startMQTTScriptedApp(t, runner, MQTTConfig{})
	defer stop()

	subCli := scriptedPahoConnect(t, port, "alert-sub-3")
	defer subCli.Disconnect(200)
	subCli.Subscribe("mqtt/alerts", 0, func(_ pahomqtt.Client, msg pahomqtt.Message) {
		select {
		case received <- string(msg.Payload()):
		default:
		}
	})
	time.Sleep(150 * time.Millisecond)

	HubInstance.Publish(&Message{Channel: "alert_channel", Data: "fire!", Event: "alert"})

	select {
	case msg := <-received:
		if msg != "fire!" {
			t.Errorf("got %q, want %q", msg, "fire!")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MQTT subscriber did not receive Hub-published message via JS subscribe")
	}
}

// ────────────────────────────────────────────────────────────────
// 4. unsubscribe — JS stops receiving after unsubscribe
// ────────────────────────────────────────────────────────────────

func TestScriptedMQTT_Unsubscribe(t *testing.T) {
	count := make(chan int, 10)

	runner := &ScriptedRunner{
		Code: `
			var hits = 0;
			subscribe("unsub_chan", (msg) => {
				hits++;
				if (hits === 1) {
					unsubscribe("unsub_chan");
				}
				publish("mqtt/count", String(hits));
			});
		`,
	}

	port, stop := startMQTTScriptedApp(t, runner, MQTTConfig{})
	defer stop()

	subCli := scriptedPahoConnect(t, port, "unsub-sub-4")
	defer subCli.Disconnect(200)
	subCli.Subscribe("mqtt/count", 0, func(_ pahomqtt.Client, msg pahomqtt.Message) {
		var n int
		fmt.Sscanf(string(msg.Payload()), "%d", &n)
		count <- n
	})
	time.Sleep(150 * time.Millisecond)

	// First publish — should hit subscribe callback
	HubInstance.Publish(&Message{Channel: "unsub_chan", Data: "first", Event: "test"})
	select {
	case n := <-count:
		if n != 1 {
			t.Errorf("first hit: got %d, want 1", n)
		}
	case <-time.After(time.Second):
		t.Fatal("first Hub message not received")
	}

	// Second publish — should be silenced by unsubscribe
	HubInstance.Publish(&Message{Channel: "unsub_chan", Data: "second", Event: "test"})
	select {
	case n := <-count:
		t.Errorf("got unexpected message after unsubscribe: hit=%d", n)
	case <-time.After(400 * time.Millisecond):
		// correct: silence
	}
}

// ────────────────────────────────────────────────────────────────
// 5. onClose — fired when MQTT client disconnects
// ────────────────────────────────────────────────────────────────

func TestScriptedMQTT_OnClose(t *testing.T) {
	var closeFired atomic.Bool

	HubInstance.RemoveAllPublishHooks()
	HubInstance.AddPublishHook(func(msg *Message) {
		if msg.Channel == "mqtt_close_signal" {
			closeFired.Store(true)
		}
	})
	defer HubInstance.RemoveAllPublishHooks()

	runner := &ScriptedRunner{
		Code: `
			onClose(() => {
				publish("mqtt_close_signal", "closed");
			});
		`,
	}

	port, stop := startMQTTScriptedApp(t, runner, MQTTConfig{})
	defer stop()

	cli := scriptedPahoConnect(t, port, "onclose-cli-5")
	time.Sleep(100 * time.Millisecond)
	cli.Disconnect(100)

	deadline := time.Now().Add(2 * time.Second)
	for !closeFired.Load() && time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
	}
	if !closeFired.Load() {
		t.Error("onClose was not fired after MQTT client disconnected")
	}
}

// ────────────────────────────────────────────────────────────────
// 6. mosquitto_pub CLI → onMessage
// ────────────────────────────────────────────────────────────────

func TestScriptedMQTT_MosquittoPub_OnMessage(t *testing.T) {
	if _, err := exec.LookPath(mosquittoPub); err != nil {
		t.Skipf("mosquitto_pub not found: %v", err)
	}

	var gotPayload atomic.Value

	HubInstance.RemoveAllPublishHooks()
	HubInstance.AddPublishHook(func(msg *Message) {
		if msg.Channel == "cli_result" {
			var m map[string]string
			if json.Unmarshal([]byte(msg.Data), &m) == nil {
				if v := m["payload"]; v != "" {
					gotPayload.Store(v)
				}
			}
		}
	})
	defer HubInstance.RemoveAllPublishHooks()

	runner := &ScriptedRunner{
		Code: `
			onMessage((msg) => {
				const m = (typeof msg === "string") ? JSON.parse(msg) : msg;
				const v = m.payload !== undefined ? m.payload : (m.data || "");
				publish("cli_result", JSON.stringify({payload: v}));
			});
		`,
	}

	port, stop := startMQTTScriptedApp(t, runner, MQTTConfig{})
	defer stop()

	time.Sleep(200 * time.Millisecond)
	cmd := exec.Command(mosquittoPub,
		"-h", "127.0.0.1",
		"-p", fmt.Sprintf("%d", port),
		"-t", "cli/test",
		"-m", "from-cli",
		"--id", "mosquitto-pub-test",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mosquitto_pub failed: %v — output: %s", err, out)
	}

	deadline := time.Now().Add(2 * time.Second)
	for gotPayload.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if gotPayload.Load() == nil {
		t.Fatal("onMessage not called after mosquitto_pub")
	}
	if gotPayload.Load().(string) != "from-cli" {
		t.Errorf("payload: got %q, want %q", gotPayload.Load(), "from-cli")
	}
}

// ────────────────────────────────────────────────────────────────
// 7. mosquitto_sub — receives JS publish
// ────────────────────────────────────────────────────────────────

func TestScriptedMQTT_MosquittoSub_JSPublish(t *testing.T) {
	if _, err := exec.LookPath(mosquittoSub); err != nil {
		t.Skipf("mosquitto_sub not found: %v", err)
	}
	if _, err := exec.LookPath(mosquittoPub); err != nil {
		t.Skipf("mosquitto_pub not found: %v", err)
	}

	runner := &ScriptedRunner{
		Code: `
			onMessage((msg) => {
				const m = (typeof msg === "string") ? JSON.parse(msg) : msg;
				const v = m.payload !== undefined ? m.payload : (m.data || "");
				if (v === "trigger") {
					publish("js/response", "triggered-by-js");
				}
			});
		`,
	}

	port, stop := startMQTTScriptedApp(t, runner, MQTTConfig{})
	defer stop()

	time.Sleep(200 * time.Millisecond)

	// Start mosquitto_sub — exits after receiving 1 message, with 3s timeout
	subCmd := exec.Command(mosquittoSub,
		"-h", "127.0.0.1",
		"-p", fmt.Sprintf("%d", port),
		"-t", "js/response",
		"-C", "1",
		"-W", "3",
	)
	subDone := make(chan []byte, 1)
	go func() {
		out, _ := subCmd.Output()
		subDone <- out
	}()

	time.Sleep(300 * time.Millisecond)

	// Trigger via mosquitto_pub
	pubCmd := exec.Command(mosquittoPub,
		"-h", "127.0.0.1",
		"-p", fmt.Sprintf("%d", port),
		"-t", "input/cmd",
		"-m", "trigger",
	)
	if out, err := pubCmd.CombinedOutput(); err != nil {
		t.Fatalf("mosquitto_pub failed: %v — %s", err, out)
	}

	select {
	case out := <-subDone:
		if !strings.Contains(string(out), "triggered-by-js") {
			t.Errorf("mosquitto_sub got %q, expected 'triggered-by-js'", string(out))
		}
	case <-time.After(4 * time.Second):
		subCmd.Process.Kill()
		t.Fatal("mosquitto_sub did not receive JS-published message in time")
	}
}
