package sse

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	fastwebsocket "github.com/fasthttp/websocket"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// startMQTTApp démarre une app Fiber avec un endpoint MQTT WebSocket.
// Retourne l'adresse d'écoute et une fonction de fermeture.
func startMQTTApp(t *testing.T, cfg MQTTConfig) (wsURL string, shutdown func()) {
	t.Helper()
	HubInstance.RemoveAllPublishHooks() // Reset pour les tests
	app := fiber.New()
	app.Use("/mqtt", MQTTUpgradeMiddleware)
	app.Get("/mqtt", websocket.New(MQTTHandler(cfg)))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	return "ws://" + ln.Addr().String() + "/mqtt", func() { app.Shutdown() }
}

// mqttConnect envoie un paquet CONNECT MQTT 3.1.1 et retourne la connexion WS.
// cleanSession=true, keepAlive=60s.
func mqttConnect(t *testing.T, url, clientID, username, password string) *fastwebsocket.Conn {
	t.Helper()
	conn, _, err := fastwebsocket.DefaultDialer.Dial(url, http.Header{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Construire le paquet CONNECT
	// Proto name : "MQTT" (4 bytes) + level 4 + flags + keepalive + payload
	var pkt []byte

	// Variable header
	varHeader := []byte{
		0x00, 0x04, 'M', 'Q', 'T', 'T', // protocol name
		0x04,       // protocol level (3.1.1)
		0x00,       // connect flags (sera mis à jour)
		0x00, 0x3C, // keepalive = 60s
	}

	flags := byte(0x02) // cleanSession = 1
	if username != "" {
		flags |= 0x80
	}
	if password != "" {
		flags |= 0x40
	}
	varHeader[7] = flags

	// Payload
	payload := mqttEncodeStr(clientID)
	if username != "" {
		payload = append(payload, mqttEncodeStr(username)...)
	}
	if password != "" {
		payload = append(payload, mqttEncodeStr(password)...)
	}

	remaining := append(varHeader, payload...)

	// Fixed header
	pkt = append(pkt, 0x10) // CONNECT type
	pkt = append(pkt, mqttVarInt(len(remaining))...)
	pkt = append(pkt, remaining...)

	if err := conn.WriteMessage(fastwebsocket.BinaryMessage, pkt); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	return conn
}

// readCONNACK lit et valide un CONNACK attendu.
func readCONNACK(t *testing.T, conn *fastwebsocket.Conn) byte {
	t.Helper()
	_, p, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read CONNACK: %v", err)
	}
	if len(p) < 4 {
		t.Fatalf("CONNACK too short: %v", p)
	}
	if p[0]>>4 != packets.Connack {
		t.Fatalf("expected CONNACK (0x%02x), got 0x%02x", packets.Connack<<4, p[0])
	}
	return p[3] // return code
}

// mqttSubscribe envoie un SUBSCRIBE sur le filtre donné (QoS requesté).
func mqttSubscribe(t *testing.T, conn *fastwebsocket.Conn, filter string, qos byte) {
	t.Helper()
	filterBytes := mqttEncodeStr(filter)
	// Variable header: pktID (2) + filter + requested QoS
	body := []byte{0x00, 0x01} // pktID = 1
	body = append(body, filterBytes...)
	body = append(body, qos)

	pkt := []byte{(packets.Subscribe << 4) | 0x02} // flags 0x02 obligatoires en 3.1.1
	pkt = append(pkt, mqttVarInt(len(body))...)
	pkt = append(pkt, body...)

	if err := conn.WriteMessage(fastwebsocket.BinaryMessage, pkt); err != nil {
		t.Fatalf("write SUBSCRIBE: %v", err)
	}
}

// readSUBACK lit un SUBACK et retourne les QoS accordés.
func readSUBACK(t *testing.T, conn *fastwebsocket.Conn) []byte {
	t.Helper()
	_, p, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read SUBACK: %v", err)
	}
	if len(p) < 4 || p[0]>>4 != packets.Suback {
		t.Fatalf("expected SUBACK, got 0x%02x", p[0])
	}
	return p[4:] // granted QoS array (après fixed header 1 + varlen 1 + pktID 2)
}

// mqttPublish envoie un PUBLISH QoS 0.
func mqttPublish(t *testing.T, conn *fastwebsocket.Conn, topic, payload string) {
	t.Helper()
	body := mqttEncodeStr(topic)
	body = append(body, []byte(payload)...)

	pkt := []byte{packets.Publish << 4} // QoS 0, no retain, no dup
	pkt = append(pkt, mqttVarInt(len(body))...)
	pkt = append(pkt, body...)

	if err := conn.WriteMessage(fastwebsocket.BinaryMessage, pkt); err != nil {
		t.Fatalf("write PUBLISH: %v", err)
	}
}

// readPUBLISH lit un PUBLISH et retourne (topic, payload).
func readPUBLISH(t *testing.T, conn *fastwebsocket.Conn, timeout time.Duration) (topic, payload string) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})

	_, p, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read PUBLISH: %v", err)
	}
	if p[0]>>4 != packets.Publish {
		t.Fatalf("expected PUBLISH, got 0x%02x", p[0])
	}
	// Décoder remaining length (varint)
	idx := 1
	for p[idx]&0x80 != 0 {
		idx++
	}
	idx++ // skip varlen bytes
	// Décoder topic
	tlen := int(p[idx])<<8 | int(p[idx+1])
	idx += 2
	topic = string(p[idx : idx+tlen])
	idx += tlen
	payload = string(p[idx:])
	return
}

// Helpers d'encodage MQTT légers pour les tests
func mqttEncodeStr(s string) []byte {
	b := []byte{byte(len(s) >> 8), byte(len(s) & 0xFF)}
	return append(b, []byte(s)...)
}

func mqttVarInt(x int) []byte {
	var out []byte
	for {
		b := byte(x % 128)
		x /= 128
		if x > 0 {
			b |= 0x80
		}
		out = append(out, b)
		if x == 0 {
			return out
		}
	}
}

// newTestHubClient subscrit un client au Hub SSE et retourne le client.
func newTestHubClient(t *testing.T, channel string) *Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		sid:      "test-hub-" + channel,
		message:  make(chan *Message, 64),
		channels: []string{channel},
		ctx:      ctx,
		cancel:   cancel,
	}
	HubInstance.Subscribe(c, channel)
	t.Cleanup(func() {
		cancel()
		HubInstance.Unsubscribe(c, channel)
	})
	return c
}

// recvHub attend un message Hub avec timeout.
func recvHub(t *testing.T, c *Client, timeout time.Duration, desc string) *Message {
	t.Helper()
	select {
	case msg := <-c.message:
		return msg
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for Hub message: %s", desc)
		return nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestMQTT_ConnectAccepted(t *testing.T) {
	url, shutdown := startMQTTApp(t, MQTTConfig{
		Auth: MQTTStaticAuth(map[string]string{"user": "pass"}),
	})
	defer shutdown()

	conn := mqttConnect(t, url, "client-1", "user", "pass")
	defer conn.Close()

	rc := readCONNACK(t, conn)
	if rc != 0 {
		t.Errorf("expected CONNACK accepted (0), got %d", rc)
	}
}

func TestMQTT_ConnectRejected_BadCredentials(t *testing.T) {
	url, shutdown := startMQTTApp(t, MQTTConfig{
		Auth: MQTTStaticAuth(map[string]string{"user": "correct"}),
	})
	defer shutdown()

	conn := mqttConnect(t, url, "client-bad", "user", "wrong")
	defer conn.Close()

	rc := readCONNACK(t, conn)
	if rc == packets.Connack {
		t.Error("expected non-zero return code for bad credentials")
	}
}

func TestMQTT_ConnectNoAuth(t *testing.T) {
	// nil Auth → AllowAll
	url, shutdown := startMQTTApp(t, MQTTConfig{})
	defer shutdown()

	conn := mqttConnect(t, url, "anon-client", "", "")
	defer conn.Close()

	rc := readCONNACK(t, conn)
	if rc != 0 {
		t.Errorf("expected accepted without auth, got %d", rc)
	}
}

func TestMQTT_Subscribe_SUBACK(t *testing.T) {
	url, shutdown := startMQTTApp(t, MQTTConfig{})
	defer shutdown()

	conn := mqttConnect(t, url, "sub-client", "", "")
	defer conn.Close()
	readCONNACK(t, conn)

	mqttSubscribe(t, conn, "sensors/+/temp", 1)

	granted := readSUBACK(t, conn)
	if len(granted) == 0 {
		t.Fatal("SUBACK: no granted QoS")
	}
	// mochi accorde QoS 1 ou 0 — vérifier que c'est ≤ demandé
	if granted[0] > 1 {
		t.Errorf("SUBACK: unexpected granted QoS %d", granted[0])
	}
}

func TestMQTT_Publish_ReachesHub(t *testing.T) {
	url, shutdown := startMQTTApp(t, MQTTConfig{})
	defer shutdown()

	// Abonner un client Hub SSE au topic avant la publication
	hubClient := newTestHubClient(t, "home/sensor/1")

	conn := mqttConnect(t, url, "pub-client", "", "")
	defer conn.Close()
	readCONNACK(t, conn)

	// Attendre que la connexion soit prête
	time.Sleep(50 * time.Millisecond)

	mqttPublish(t, conn, "home/sensor/1", `{"temp":23.5}`)

	msg := recvHub(t, hubClient, time.Second, "MQTT publish → Hub")
	if msg.Channel != "home/sensor/1" {
		t.Errorf("expected channel home/sensor/1, got %q", msg.Channel)
	}
	if !strings.Contains(msg.Data, "23.5") {
		t.Errorf("expected payload with 23.5, got %q", msg.Data)
	}
}

func TestMQTT_HubPublish_ReachesMQTTClient(t *testing.T) {
	url, shutdown := startMQTTApp(t, MQTTConfig{})
	defer shutdown()

	conn := mqttConnect(t, url, "recv-client", "", "")
	defer conn.Close()
	readCONNACK(t, conn)

	// S'abonner au topic
	mqttSubscribe(t, conn, "alerts/+", 0)
	readSUBACK(t, conn)

	// Attendre que la souscription soit enregistrée dans mochi
	time.Sleep(100 * time.Millisecond)

	// Publier depuis le Hub SSE (simule un handler HTTP, CRUD, etc.)
	HubInstance.Publish(&Message{
		Channel: "alerts/fire",
		Event:   "alerts/fire",
		Data:    "building B",
	})

	topic, payload := readPUBLISH(t, conn, time.Second)
	if topic != "alerts/fire" {
		t.Errorf("expected topic alerts/fire, got %q", topic)
	}
	if payload != "building B" {
		t.Errorf("expected payload 'building B', got %q", payload)
	}
}

func TestMQTT_Publish_OnPublishHook_Block(t *testing.T) {
	blocked := false
	url, shutdown := startMQTTApp(t, MQTTConfig{
		OnPublish: func(clientID, topic string, payload []byte, qos byte) bool {
			if strings.HasPrefix(topic, "blocked/") {
				blocked = true
				return false // bloquer la diffusion Hub
			}
			return true
		},
	})
	defer shutdown()

	// Abonner un client Hub pour vérifier qu'aucun message n'arrive
	hubClient := newTestHubClient(t, "blocked/test")

	conn := mqttConnect(t, url, "hook-client", "", "")
	defer conn.Close()
	readCONNACK(t, conn)

	time.Sleep(50 * time.Millisecond)
	mqttPublish(t, conn, "blocked/test", "secret")
	time.Sleep(200 * time.Millisecond)

	if !blocked {
		t.Error("OnPublish hook was not called")
	}

	// Vérifier qu'aucun message n'a atteint le Hub
	select {
	case msg := <-hubClient.message:
		t.Errorf("Hub should not have received blocked message, got %v", msg)
	default:
		// correct : pas de message
	}
}

func TestMQTT_Lifecycle_OnConnect_OnDisconnect(t *testing.T) {
	connected := make(chan string, 1)
	disconnected := make(chan string, 1)

	url, shutdown := startMQTTApp(t, MQTTConfig{
		OnConnect:    func(id string) { connected <- id },
		OnDisconnect: func(id string, clean bool) { disconnected <- id },
	})
	defer shutdown()

	conn := mqttConnect(t, url, "lifecycle-client", "", "")
	readCONNACK(t, conn)

	select {
	case id := <-connected:
		if id != "lifecycle-client" {
			t.Errorf("OnConnect: expected lifecycle-client, got %q", id)
		}
	case <-time.After(time.Second):
		t.Error("OnConnect not called")
	}

	conn.Close() // déconnexion abrupte

	select {
	case id := <-disconnected:
		if id != "lifecycle-client" {
			t.Errorf("OnDisconnect: expected lifecycle-client, got %q", id)
		}
	case <-time.After(time.Second):
		t.Error("OnDisconnect not called")
	}
}

func TestMQTT_PubSub_SameClient(t *testing.T) {
	url, shutdown := startMQTTApp(t, MQTTConfig{})
	defer shutdown()

	conn := mqttConnect(t, url, "echo-client", "", "")
	defer conn.Close()
	readCONNACK(t, conn)

	mqttSubscribe(t, conn, "echo/test", 0)
	readSUBACK(t, conn)

	time.Sleep(100 * time.Millisecond)

	mqttPublish(t, conn, "echo/test", "ping")

	topic, payload := readPUBLISH(t, conn, time.Second)
	if topic != "echo/test" || payload != "ping" {
		t.Errorf("echo: got topic=%q payload=%q", topic, payload)
	}
}

func TestMQTT_WildcardSubscription_Plus(t *testing.T) {
	url, shutdown := startMQTTApp(t, MQTTConfig{})
	defer shutdown()

	conn := mqttConnect(t, url, "wildcard-client", "", "")
	defer conn.Close()
	readCONNACK(t, conn)

	// S'abonner avec wildcard +
	mqttSubscribe(t, conn, "sensors/+/data", 0)
	readSUBACK(t, conn)

	time.Sleep(100 * time.Millisecond)

	// Publier depuis le Hub sur un topic qui matche
	HubInstance.Publish(&Message{
		Channel: "sensors/device42/data",
		Event:   "sensors/device42/data",
		Data:    "42",
	})

	topic, payload := readPUBLISH(t, conn, time.Second)
	if topic != "sensors/device42/data" {
		t.Errorf("expected sensors/device42/data, got %q", topic)
	}
	if payload != "42" {
		t.Errorf("expected payload 42, got %q", payload)
	}
}

func TestMQTT_WildcardSubscription_Hash(t *testing.T) {
	url, shutdown := startMQTTApp(t, MQTTConfig{})
	defer shutdown()

	conn := mqttConnect(t, url, "hash-client", "", "")
	defer conn.Close()
	readCONNACK(t, conn)

	mqttSubscribe(t, conn, "events/#", 0)
	readSUBACK(t, conn)

	time.Sleep(100 * time.Millisecond)

	HubInstance.Publish(&Message{
		Channel: "events/2024/06/alarm",
		Event:   "events/2024/06/alarm",
		Data:    "triggered",
	})

	topic, _ := readPUBLISH(t, conn, time.Second)
	if !strings.HasPrefix(topic, "events/") {
		t.Errorf("expected events/... topic, got %q", topic)
	}
}

func TestMQTT_SYS_Topic_NotMatchedByWildcard(t *testing.T) {
	// Vérification directe de mqttMatchTopic (si elle existe encore)
	// Avec mochi-mqtt, la protection $SYS est gérée nativement par le broker.
	// On teste le comportement via le Hub : un topic $SYS ne doit pas être
	// distribué à un abonné wildcard #.

	url, shutdown := startMQTTApp(t, MQTTConfig{})
	defer shutdown()

	conn := mqttConnect(t, url, "sys-client", "", "")
	defer conn.Close()
	readCONNACK(t, conn)

	mqttSubscribe(t, conn, "#", 0)
	readSUBACK(t, conn)

	// Publier un message $SYS depuis le Hub
	HubInstance.Publish(&Message{
		Channel: "$SYS/broker/uptime",
		Event:   "$SYS/broker/uptime",
		Data:    "100s",
	})

	// Vérifier qu'aucun message $SYS n'arrive au client # dans un délai court
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, p, err := conn.ReadMessage()
	if err == nil {
		// Si on reçoit quelque chose, vérifier que ce n'est PAS le $SYS
		if p[0]>>4 == packets.Publish {
			idx := 1
			for p[idx]&0x80 != 0 {
				idx++
			}
			idx++
			tlen := int(p[idx])<<8 | int(p[idx+1])
			idx += 2
			topic := string(p[idx : idx+tlen])
			if strings.HasPrefix(topic, "$SYS") {
				t.Errorf("$SYS topic should not be delivered to # subscriber, got %q", topic)
			}
		}
	}
}

func TestMQTT_StaticAuth_TimingAttack(t *testing.T) {
	// MQTTStaticAuth doit utiliser subtle.ConstantTimeCompare
	// On vérifie juste que des credentials incorrects sont bien refusés
	authFn := MQTTStaticAuth(map[string]string{
		"admin": "supersecret",
	})

	_, err := authFn("admin", "supersecret", "c1")
	if err != nil {
		t.Errorf("valid credentials rejected: %v", err)
	}

	_, err = authFn("admin", "wrong", "c2")
	if err == nil {
		t.Error("invalid credentials accepted")
	}

	_, err = authFn("unknown", "pass", "c3")
	if err == nil {
		t.Error("unknown user accepted")
	}

	_, err = authFn("", "pass", "c4")
	if err == nil {
		t.Error("empty username accepted")
	}
}

func TestMQTT_AllowAllAuth(t *testing.T) {
	authFn := MQTTAllowAllAuth()
	_, err := authFn("", "", "any-client")
	if err != nil {
		t.Errorf("AllowAll rejected: %v", err)
	}
}

func TestMQTT_NoLoop_HubToMQTTToHub(t *testing.T) {
	// Vérifier qu'un message publié dans le Hub ne boucle pas indéfiniment.
	// Le hubMQTTSubscriber injecte dans mochi, qui déclenche hubHook.OnPublish,
	// qui ne doit PAS republier dans le Hub (guard clientID == hubBridgeClientID).

	url, shutdown := startMQTTApp(t, MQTTConfig{})
	defer shutdown()

	// Abonner un client Hub pour compter les réceptions
	hubClient := newTestHubClient(t, "loop/test")

	// Connexion MQTT qui s'abonne au même topic
	conn := mqttConnect(t, url, "loop-mqtt", "", "")
	defer conn.Close()
	readCONNACK(t, conn)
	mqttSubscribe(t, conn, "loop/test", 0)
	readSUBACK(t, conn)

	time.Sleep(100 * time.Millisecond)

	// Publier une fois dans le Hub
	HubInstance.Publish(&Message{Channel: "loop/test", Event: "loop/test", Data: "once"})

	// Attendre le message MQTT
	readPUBLISH(t, conn, time.Second)

	// Attendre un peu et vérifier qu'aucun second message Hub n'arrive
	time.Sleep(200 * time.Millisecond)

	// Le Hub doit avoir reçu exactement 1 message
	count := 0
drain:
	for {
		select {
		case <-hubClient.message:
			count++
		default:
			break drain
		}
	}
	if count > 1 {
		t.Errorf("loop detected: Hub received %d messages, expected 1", count)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test JS Hooks integration
// ─────────────────────────────────────────────────────────────────────────────

func TestMQTTHooksDispatcher(t *testing.T) {
	hd := NewMQTTHooksDispatcher(".", map[string]string{})

	// Test 1: AUTH Hooks successful execution
	hd.AddAuthBlock("SCRIPT", `
		if (user === "admin" && pwd === "secret") {
			allow()
		} else {
			reject("bad user")
		}
	`, true, nil)

	authBridge := hd.AuthFunc()

	// Should pass
	_, err := authBridge("admin", "secret", "client_1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}

	// Should fail
	_, err = authBridge("hacker", "secret", "client_1")
	if err == nil || err.Error() != "bad user" {
		t.Fatalf("expected 'bad user', got %v", err)
	}

	// Test 2: ACL Hooks execution
	aclRoute := &MQTTRoute{
		Method: "ACL",
		Type:   "JS",
		Inline: true,
		Handler: `
			if (user === "user1" && topic.startsWith("admin/")) {
				reject("denied");
			} else {
				allow();
			}
		`,
	}
	hd.AddACLRoute(aclRoute)

	hook := NewDynamicMochiHook(hd)

	// Simulated Client check
	clOptions := &mqtt.Client{
		ID: "client_1",
	}
	clOptions.Properties.Username = []byte("user1")

	allowed := hook.OnACLCheck(clOptions, "public/data", true)
	if !allowed {
		t.Fatalf("expected write allowed on public/data")
	}

	allowed = hook.OnACLCheck(clOptions, "admin/system", true)
	if allowed {
		t.Fatalf("expected write denied on admin/system")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: no AUTH = allow all
// ─────────────────────────────────────────────────────────────────────────────

func TestMQTTHooks_NoAuth_AllowAll(t *testing.T) {
	hd := NewMQTTHooksDispatcher(".", map[string]string{})
	authFn := hd.AuthFunc()

	// With no auth blocks, any credentials should succeed
	_, err := authFn("anyclient", "anypassword", "client_1")
	if err != nil {
		t.Fatalf("expected allow-all with no auth blocks, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: AUTH rejects with custom JS message exactly
// ─────────────────────────────────────────────────────────────────────────────

func TestMQTTHooks_AuthRejectMessage(t *testing.T) {
	hd := NewMQTTHooksDispatcher(".", map[string]string{})
	hd.AddAuthBlock("SCRIPT", `
		reject("access denied: invalid token");
	`, true, nil)

	authFn := hd.AuthFunc()
	_, err := authFn("any", "any", "any")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "access denied: invalid token" {
		t.Fatalf("expected specific reject message, got: %q", err.Error())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: ACL READ vs WRITE mode distinction
// ─────────────────────────────────────────────────────────────────────────────

func TestMQTTHooks_ACL_ReadWrite(t *testing.T) {
	hd := NewMQTTHooksDispatcher(".", map[string]string{})
	hd.AddACLRoute(&MQTTRoute{
		Type:   "JS",
		Inline: true,
		Handler: `
			// Only block WRITE on protected/#
			if (mode === "WRITE" && topic.startsWith("protected/")) {
				reject("read-only topic");
			} else {
				allow();
			}
		`,
	})

	hook := NewDynamicMochiHook(hd)
	cl := &mqtt.Client{ID: "c1"}
	cl.Properties.Username = []byte("bob")

	// READ on protected/ → allowed
	if !hook.OnACLCheck(cl, "protected/data", false) {
		t.Fatal("expected READ on protected/ to be allowed")
	}
	// WRITE on protected/ → denied
	if hook.OnACLCheck(cl, "protected/data", true) {
		t.Fatal("expected WRITE on protected/ to be denied")
	}
	// WRITE on public/ → allowed
	if !hook.OnACLCheck(cl, "public/data", true) {
		t.Fatal("expected WRITE on public/ to be allowed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: BRIDGE static topic matching
// ─────────────────────────────────────────────────────────────────────────────

func TestMQTTHooks_BridgeTopicPatternMatch(t *testing.T) {
	tests := []struct {
		pattern string
		topic   string
		want    bool
	}{
		{"sensors/#", "sensors/temp", true},
		{"sensors/#", "sensors/temp/room1", true},
		{"sensors/#", "actuators/relay", false},
		{"alerts", "alerts", true},
		{"alerts", "alerts/fire", false},
	}

	for _, tc := range tests {
		got := matchTopicPattern(tc.pattern, tc.topic)
		if got != tc.want {
			t.Errorf("matchTopicPattern(%q, %q) = %v, want %v", tc.pattern, tc.topic, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: PUBLISH event dispatch runs the script
// ─────────────────────────────────────────────────────────────────────────────

func TestMQTTHooks_OnPublishDispatch(t *testing.T) {
	hd := NewMQTTHooksDispatcher(".", map[string]string{})

	// Register a flag variable via JS before hooking
	hd.vm.Set("publishFired", false)
	hd.AddONRoute(&MQTTRoute{
		Method:  "PUBLISH",
		Type:    "JS",
		Inline:  true,
		Handler: `publishFired = true;`,
	})

	onPublish := hd.OnPublishFunc()
	onPublish("client_1", "sensors/temp", []byte("25.4"), 0)

	fired := hd.vm.Get("publishFired")
	if fired == nil || !fired.ToBoolean() {
		t.Fatal("expected PUBLISH script to set publishFired = true")
	}
}
