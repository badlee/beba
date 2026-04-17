package sse

// mqtt.go — Broker MQTT 3.1.1/5.0 intégré au Hub SSE, propulsé par mochi-mqtt.

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
	"gorm.io/gorm"
)

// ─────────────────────────────────────────────────────────────────────────────
// Public API
// ─────────────────────────────────────────────────────────────────────────────

type MQTTAuthFunc func(username, password, clientID string) (clientIDOut string, err error)

type MQTTConfig struct {
	ListenerAddress string
	StorageDB       *gorm.DB
	DynamicHook     *DynamicMochiHook
	Auth            MQTTAuthFunc
	MaxPacketSize   int
	OnPublish       func(clientID, topic string, payload []byte, qos byte) bool
	OnConnect       func(clientID string)
	OnDisconnect    func(clientID string, cleanDisconnect bool)
	SharedServer    *MQTTServer // optional shared instance
}

// ─────────────────────────────────────────────────────────────────────────────
// MQTTServer — serveur mochi-mqtt avec intégration Hub SSE
// ─────────────────────────────────────────────────────────────────────────────

type MQTTServer struct {
	server *mqtt.Server
	bridge *mqttWSListener
	hookFn func(*Message)
}

func (ms *MQTTServer) Server() *mqtt.Server { return ms.server }

// NewMQTTServer crée, configure et démarre un MQTTServer.
func NewMQTTServer(cfg MQTTConfig, opts *mqtt.Options) (*MQTTServer, error) {
	if opts == nil {
		opts = &mqtt.Options{
			InlineClient: true,
		}
	} else {
		opts.InlineClient = true
	}
	if cfg.MaxPacketSize > 0 {
		if opts.Capabilities == nil {
			opts.Capabilities = mqtt.NewDefaultServerCapabilities()
		}
		opts.Capabilities.MaximumPacketSize = uint32(cfg.MaxPacketSize)
	}
	opts.Logger = slog.New(slog.NewTextHandler(log.Writer(), &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	srv := mqtt.New(opts)

	if cfg.Auth != nil {
		if err := srv.AddHook(&authHook{fn: cfg.Auth}, nil); err != nil {
			return nil, err
		}
	} else {
		if err := srv.AddHook(new(auth.AllowHook), nil); err != nil {
			return nil, err
		}
	}

	if err := srv.AddHook(&hubHook{cfg: cfg}, nil); err != nil {
		return nil, err
	}

	if cfg.OnConnect != nil || cfg.OnDisconnect != nil {
		if err := srv.AddHook(&lifecycleHook{cfg: cfg}, nil); err != nil {
			return nil, err
		}
	}

	bridge := newMQTTWSListener("fiber-ws")
	if err := srv.AddListener(bridge); err != nil {
		return nil, err
	}

	if cfg.ListenerAddress != "" {
		tcp := listeners.NewTCP(listeners.Config{
			ID:      "tcp-proxy",
			Address: cfg.ListenerAddress,
		})
		if err := srv.AddListener(tcp); err != nil {
			return nil, err
		}
	} else {
		// ── Ghost Listener ──
		// Mochi v2 exits Serve() if no network-bound listeners are active.
		if err := srv.AddListener(listeners.NewTCP(listeners.Config{
			ID:      "ghost",
			Address: "127.0.0.1:0",
		})); err != nil {
			return nil, err
		}
	}

	if cfg.StorageDB != nil {
		hook := NewMochiDBHook(cfg.StorageDB)
		if err := srv.AddHook(hook, nil); err != nil {
			return nil, err
		}
	}

	if cfg.DynamicHook != nil {
		if err := srv.AddHook(cfg.DynamicHook, nil); err != nil {
			return nil, err
		}
	}

	log.Printf("mqtt: Calling srv.Serve()...")
	go func() {
		err := srv.Serve()
		if err != nil {
			log.Printf("mqtt: srv.Serve() exited with error: %v", err)
		} else {
			log.Printf("mqtt: srv.Serve() exited normally (nil).")
		}
	}()

	ms := &MQTTServer{server: srv, bridge: bridge}

	ms.hookFn = func(msg *Message) {
		if msg.Source != "mqtt" {
			_ = srv.Publish(msg.Channel, []byte(msg.Data), false, 0)
		}
	}
	HubInstance.AddPublishHook(ms.hookFn)
	return ms, nil
}

func (ms *MQTTServer) ServeConn(conn net.Conn) {
	// Don't inject if the bridge is closing
	select {
	case <-ms.bridge.done:
		conn.Close()
		return
	default:
	}
	
	go func() {
		// Use a local ID or the remote address for logging
		err := ms.server.EstablishConnection("tcp-proxy", conn)
		if err != nil {
			// Expected if server is closing
			conn.Close()
		}
	}()
}

func (ms *MQTTServer) Close() error {
	ms.bridge.Close(nil) // First stop accepting new bridge connections
	if ms.hookFn != nil {
		HubInstance.RemovePublishHook(ms.hookFn)
		ms.hookFn = nil
	}
	return ms.server.Close()
}

// ─────────────────────────────────────────────────────────────────────────────
// Fiber WebSocket handler
// ─────────────────────────────────────────────────────────────────────────────

func MQTTUpgradeMiddleware(c fiber.Ctx) error {
	if websocket.IsWebSocketUpgrade(c) {
		return c.Next()
	}
	return fiber.ErrUpgradeRequired
}

func MQTTHandler(cfg MQTTConfig, runner ...*ScriptedRunner) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		// Unified bridge for Fiber WebSocket/MQTT
		sid := ws.Params("id", ws.Query("id", "mqtt-client"))
		hubCtx, hubCancel := context.WithCancel(context.Background())
		defer hubCancel()

		hubClient := &Client{
			sid:     sid,
			ConnID:  sid + "_" + uuid.New().String(), // Unique for MQTT
			message: make(chan *Message, clientBuf),
			ctx:     hubCtx,
			cancel:  hubCancel,
		}

		var scripted *Runtime
		
		// Unified Cleanup
		defer func() {
			if scripted != nil {
				scripted.Shutdown()
			}
			hubCancel()
			hubClient.closed.Store(true)
			// Cleanup all channels from Hub
			hubClient.chMu.Lock()
			channels := append([]string{}, hubClient.channels...)
			hubClient.chMu.Unlock()
			for _, ch := range channels {
				HubInstance.Unsubscribe(hubClient, ch)
			}
			log.Printf("mqtt: client %s disconnected", sid)
		}()

		if len(runner) > 0 && runner[0] != nil {
			r := runner[0]
			scripted = NewRuntime(hubClient, func(topic, data string) error {
				HubInstance.Publish(&Message{
					Channel:   topic,
					Data:      data,
					Source:    "js",
					SenderSID: hubClient.ConnID,
				})
				return nil
			}, func() {
				ws.Close()
			}, ws)

			if err := scripted.Run(r.Code, ".", r.Config); err != nil {
				log.Printf("MQTT JS runtime error: %v", err)
			}

			mqttHook := func(msg *Message) {
				if msg.Source == "mqtt" {
					scripted.Emit("message", map[string]string{
						"topic":   msg.Channel,
						"payload": msg.Data,
					})
				}
			}
			HubInstance.AddPublishHook(mqttHook)
			defer HubInstance.RemovePublishHook(mqttHook)

			// Background loop: Hub -> JS (hub_message)
			go func() {
				for {
					select {
					case <-hubClient.ctx.Done():
						return
					case msg, ok := <-hubClient.message:
						if !ok {
							return
						}
						scripted.Emit("hub_message", msg)
					}
				}
			}()
		}

		ms := cfg.SharedServer
		var localServer bool
		if ms == nil {
			localServer = true
			var err error
			ms, err = NewMQTTServer(cfg, nil)
			if err != nil {
				log.Printf("MQTTHandler: failed to start local bridge server: %v", err)
				ws.Close()
				return
			}
		} else {
			// give the shared server a moment to be truly ready
		}
		
		if localServer {
			defer ms.Close()
		}

		ms.bridge.serve(ws)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// mqttWSListener
// ─────────────────────────────────────────────────────────────────────────────

type mqttWSListener struct {
	id      string
	connCh  chan *wsNetConn
	done    chan struct{}
	closeWG sync.WaitGroup
	logger  *slog.Logger
}

func newMQTTWSListener(id string) *mqttWSListener {
	return &mqttWSListener{
		id:     id,
		connCh: make(chan *wsNetConn, 256),
		done:   make(chan struct{}),
		logger: slog.Default(),
	}
}

func (l *mqttWSListener) serve(ws *websocket.Conn) {
	nc := newWSNetConn(ws)
	select {
	case l.connCh <- nc:
		<-nc.done 
	case <-l.done:
		ws.Close()
	}
}

func (l *mqttWSListener) ID() string       { return l.id }
func (l *mqttWSListener) Address() string  { return "fiber-ws://" + l.id }
func (l *mqttWSListener) Protocol() string { return "ws" }

func (l *mqttWSListener) Init(logger *slog.Logger) error {
	if logger != nil {
		l.logger = logger
	}
	return nil
}

func (l *mqttWSListener) Serve(establishFn listeners.EstablishFn) {
	l.closeWG.Add(1)
	defer l.closeWG.Done()
	for {
		select {
		case nc := <-l.connCh:
			go func(c *wsNetConn) {
				defer close(c.done)
				// establishFn blocks until client disconnects or server closes
				_ = establishFn(l.id, c)
			}(nc)
		case <-l.done:
			return
		}
	}
}

func (l *mqttWSListener) Close(closeClients listeners.CloseFn) {
	select {
	case <-l.done:
		return
	default:
		close(l.done)
	}
	l.closeWG.Wait()
}

// ─────────────────────────────────────────────────────────────────────────────
// wsNetConn
// ─────────────────────────────────────────────────────────────────────────────

type wsNetConn struct {
	ws   *websocket.Conn
	buf  []byte        
	done chan struct{} 
	once sync.Once
}

func newWSNetConn(ws *websocket.Conn) *wsNetConn {
	return &wsNetConn{ws: ws, done: make(chan struct{})}
}

func (c *wsNetConn) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		mt, data, err := c.ws.ReadMessage()
		if err != nil {
			return 0, err
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		c.buf = data
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *wsNetConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsNetConn) Close() error {
	var err error
	c.once.Do(func() { err = c.ws.Close() })
	return err
}

func (c *wsNetConn) LocalAddr() net.Addr  { return &net.TCPAddr{} }
func (c *wsNetConn) RemoteAddr() net.Addr { return &net.TCPAddr{} }

func (c *wsNetConn) SetDeadline(t time.Time) error {
	if c.ws.NetConn() != nil {
		return c.ws.NetConn().SetDeadline(t)
	}
	return nil
}
func (c *wsNetConn) SetReadDeadline(t time.Time) error {
	if c.ws.NetConn() != nil {
		return c.ws.NetConn().SetReadDeadline(t)
	}
	return nil
}
func (c *wsNetConn) SetWriteDeadline(t time.Time) error {
	if c.ws.NetConn() != nil {
		return c.ws.NetConn().SetWriteDeadline(t)
	}
	return nil
}

var _ net.Conn = (*wsNetConn)(nil)
var _ io.ReadWriteCloser = (*wsNetConn)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// Hooks
// ─────────────────────────────────────────────────────────────────────────────

type authHook struct {
	mqtt.HookBase
	fn MQTTAuthFunc
}

func (h *authHook) ID() string { return "binder-auth" }
func (h *authHook) Provides(b byte) bool {
	return b == mqtt.OnConnectAuthenticate
}
func (h *authHook) OnConnectAuthenticate(cl *mqtt.Client, pk packets.Packet) bool {
	username := string(pk.Connect.Username)
	password := string(pk.Connect.Password)
	_, err := h.fn(username, password, cl.ID)
	return err == nil
}

type hubHook struct {
	mqtt.HookBase
	cfg MQTTConfig
}

func (h *hubHook) ID() string { return "binder-hub" }
func (h *hubHook) Provides(b byte) bool {
	return b == mqtt.OnPublish
}
func (h *hubHook) OnPublish(cl *mqtt.Client, pk packets.Packet) (packets.Packet, error) {
	topic := pk.TopicName
	payload := pk.Payload
	qos := pk.FixedHeader.Qos
	clientID := ""
	if cl != nil {
		clientID = cl.ID
	}
	if clientID == "" || clientID == "inline" {
		return pk, nil
	}
	if h.cfg.OnPublish != nil && !h.cfg.OnPublish(clientID, topic, payload, qos) {
		return pk, nil
	}
	HubInstance.Publish(&Message{
		Channel: topic,
		Event:   topic,
		Data:    string(payload),
		Source:  "mqtt",
	})
	return pk, nil
}

type lifecycleHook struct {
	mqtt.HookBase
	cfg MQTTConfig
}

func (h *lifecycleHook) ID() string { return "binder-lifecycle" }
func (h *lifecycleHook) Provides(b byte) bool {
	return b == mqtt.OnConnect || b == mqtt.OnDisconnect
}
func (h *lifecycleHook) OnConnect(cl *mqtt.Client, pk packets.Packet) error {
	if h.cfg.OnConnect != nil {
		h.cfg.OnConnect(cl.ID)
	}
	return nil
}
func (h *lifecycleHook) OnDisconnect(cl *mqtt.Client, err error, expire bool) {
	if h.cfg.OnDisconnect != nil {
		h.cfg.OnDisconnect(cl.ID, err == nil)
	}
}

func MQTTStaticAuth(users map[string]string) MQTTAuthFunc {
	return func(username, password, _ string) (string, error) {
		if username == "" {
			return "", errors.New("mqtt: username required")
		}
		pw, ok := users[username]
		if !ok || subtle.ConstantTimeCompare([]byte(pw), []byte(password)) != 1 {
			return "", errors.New("mqtt: invalid credentials")
		}
		return "", nil
	}
}

func MQTTAllowAllAuth() MQTTAuthFunc {
	return func(_, _, _ string) (string, error) { return "", nil }
}
