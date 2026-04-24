package binder

import (
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"beba/modules/db"
	"beba/modules/security"
	"beba/modules/sse"
	"beba/plugins/httpserver"

	mqtt "github.com/mochi-mqtt/server/v2"
	"gorm.io/gorm"
)

// ─────────────────────────────────────────────────────────────────────────────
// MQTTDirective
// ─────────────────────────────────────────────────────────────────────────────

type MQTTDirective struct {
	server *sse.MQTTServer
	cfg    *DirectiveConfig
	hooks  *sse.MQTTHooksDispatcher // For Phase 4 (JS Hooks)
}

func NewMQTTDirective(config *DirectiveConfig) (*MQTTDirective, error) {
	fmt.Printf("MQTT: Config parsed for address %s\n", config.Address)

	directive := &MQTTDirective{
		cfg: config,
	}

	// ── 1. Prepare Authentication, ACLs and JS Hooks ──
	envMap := make(map[string]string)
	for k, v := range config.Env {
		envMap[k] = v
	}
	directive.hooks = sse.NewMQTTHooksDispatcher(config.BaseDir, envMap)
	parseMQTTAuthAndACL(directive.hooks, config)

	// Note: Engine instantiation (SSE MQTT Server) is now deferred to Start()
	// to ensure DATABASE dependencies are resolved after groups have started.

	return directive, nil
}

// Name identifies the directive.
func (d *MQTTDirective) Name() string {
	return "MQTT"
}

// Address returns the listen address (usually :1883).
func (d *MQTTDirective) Address() string {
	return d.cfg.Address
}

// Start boots the native tcp listener (already booted by NewMQTTServer internally, returning dummy here for Directive compat).
func (d *MQTTDirective) Start() ([]net.Listener, error) {
	// ── 1. Resolve Storage (Lazy resolution to allow DATABASE directive to start first) ──
	var storageDB *gorm.DB
	storageRoutes := d.cfg.GetRoutes("STORAGE")
	if len(storageRoutes) > 0 {
		dbName := storageRoutes[0].Path
		conn := db.GetConnection(dbName)
		if conn == nil {
			conn = db.GetDefaultConnection()
		}

		if conn != nil {
			storageDB = conn.GetDB()
		} else {
			log.Printf("MQTT WARNING: STORAGE %s specified but no DB connection found at startup!", dbName)
		}
	}

	// ── 2. Parse SECURITY (WAF/Connection level Filters) ──
	securityRoutes := d.cfg.GetRoutes("SECURITY")
	if len(securityRoutes) > 0 {
		policyName := securityRoutes[0].Path
		wafConfig := httpserver.GetWAF(policyName)
		if wafConfig != nil {
			wafConfig.AppName = d.cfg.Name
			// Register connection-level policy to the global security engine
			if wafConfig.Connection != nil {
				security.GetEngine().LoadPolicy(policyName, wafConfig.Connection)
			}
		}
	}

	// ── 3. Instantiate Engine if not already done ──
	// Note: We move full instantiation here because it depends on the DB connection
	// which is only guaranteed to be registered after DATABASE.Start()
	if d.server == nil {
		opts := &mqtt.Options{
			Capabilities: mqtt.NewDefaultServerCapabilities(),
		}
		optionsRoutes := d.cfg.GetRoutes("OPTIONS")
		if len(optionsRoutes) > 0 {
			parseMQTTOptions(opts, optionsRoutes[0])
		}

		// Prepare MQTTConfig
		mqttConfig := sse.MQTTConfig{
			ListenerAddress: "", // Don't let mochi-mqtt listen, we handle it via Start() and ServeConn
			StorageDB:       storageDB,
		}

		// Hooks and remaining config
		mqttConfig.Auth = d.hooks.AuthFunc()
		mqttConfig.OnConnect = d.hooks.OnConnectFunc()
		mqttConfig.OnDisconnect = d.hooks.OnDisconnectFunc()
		mqttConfig.OnPublish = d.hooks.OnPublishFunc()
		mqttConfig.DynamicHook = sse.NewDynamicMochiHook(d.hooks)

		srv, err := sse.NewMQTTServer(mqttConfig, opts)
		if err != nil {
			return nil, fmt.Errorf("MQTT: Failed to initialize Mochi Engine: %v", err)
		}
		d.server = srv
	}

	// Let the binder Manager handle Accept() enforcing Security checks
	log.Printf("MQTT: Listening on %s", d.cfg.Address)

	if os.Getenv("BEBA_VHOST_CHILD") != "1" && d.cfg.AppConfig != nil && d.cfg.AppConfig.Socket != "" {
		network := "unix"
		if runtime.GOOS == "windows" && strings.HasPrefix(d.cfg.AppConfig.Socket, `\\.\pipe\`) {
			network = "npipe"
		}
		if network == "unix" {
			_ = os.Remove(d.cfg.AppConfig.Socket)
		}
		ln, err := net.Listen(network, d.cfg.AppConfig.Socket)
		if err != nil {
			log.Printf("MQTT: Failed to listen on socket %s: %v", d.cfg.AppConfig.Socket, err)
			return nil, err
		}
		return []net.Listener{ln}, nil
	}

	ln, err := net.Listen("tcp", d.cfg.Address)
	if err != nil {
		log.Printf("MQTT: Failed to listen on %s: %v", d.cfg.Address, err)
		return nil, err
	}
	return []net.Listener{ln}, nil
}

// Match is used by the Manager to sniff incoming protocol bytes.
func (d *MQTTDirective) Match(peekData []byte) (bool, error) {
	if len(peekData) < 8 {
		return false, nil
	}
	// Control Packet Type 1 (CONNECT) is 0x10.
	if peekData[0] != 0x10 {
		return false, nil
	}

	// The protocol name "MQTT" or "MQIsdp" usually starts at offset 4 or 5
	// depending on the length of the Remaining Length field (1-4 bytes).
	// We'll search in the first 16 bytes for safety.
	searchLen := len(peekData)
	if searchLen > 16 {
		searchLen = 16
	}
	s := string(peekData[:searchLen])
	if strings.Contains(s, "MQTT") || strings.Contains(s, "MQIsd") {
		return true, nil
	}
	return false, nil
}

// Handle injects the connection into the MQTT server.
func (d *MQTTDirective) Handle(conn net.Conn) error {
	if d.server == nil {
		conn.Close()
		return fmt.Errorf("MQTT server not initialized")
	}
	d.server.ServeConn(conn)
	return nil
}

func (d *MQTTDirective) HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error {
	return nil
}

// Close gracefully terminates the broker.
func (d *MQTTDirective) Close() error {
	if d.server != nil {
		return d.server.Close()
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func parseMQTTOptions(opts *mqtt.Options, route *RouteConfig) {
	for _, child := range route.Routes {
		valStr := child.Path
		if valStr == "" {
			valStr = child.Handler
		}
		switch child.Method {
		case "MAX_CLIENTS":
			if v, err := strconv.Atoi(valStr); err == nil {
				opts.Capabilities.MaximumClients = int64(v)
			}
		case "MESSAGE_EXPIRY":
			if d, err := time.ParseDuration(valStr); err == nil {
				opts.Capabilities.MaximumMessageExpiryInterval = int64(d.Seconds())
			}
		case "WRITES_PENDING":
			if v, err := strconv.Atoi(valStr); err == nil {
				opts.Capabilities.MaximumClientWritesPending = int32(v)
			}
		case "SESSION_EXPIRY":
			if d, err := time.ParseDuration(valStr); err == nil {
				opts.Capabilities.MaximumSessionExpiryInterval = uint32(d.Seconds())
			}
		case "MAX_PACKET_SIZE":
			if v, err := strconv.Atoi(valStr); err == nil {
				opts.Capabilities.MaximumPacketSize = uint32(v)
			}
		case "MAX_PACKETS":
			// MaximumPacketID is no longer exported or not available in this mochi-mqtt version
		case "MAX_RECEIVE":
			if v, err := strconv.Atoi(valStr); err == nil {
				opts.Capabilities.ReceiveMaximum = uint16(v)
			}
		case "MAX_INFLIGHT":
			if v, err := strconv.Atoi(valStr); err == nil {
				opts.Capabilities.MaximumInflight = uint16(v)
			}
		case "MAX_ALIAS":
			if v, err := strconv.Atoi(valStr); err == nil {
				opts.Capabilities.TopicAliasMaximum = uint16(v)
			}
		case "AVAILABLE_SHARED_SUB":
			if isON(valStr) {
				opts.Capabilities.SharedSubAvailable = 1
			} else {
				opts.Capabilities.SharedSubAvailable = 0
			}
		case "MIN_PROTOCOL":
			if v, err := strconv.Atoi(valStr); err == nil {
				opts.Capabilities.MinimumProtocolVersion = byte(v)
			}
		case "NOT_OBSCURE":
			// Compatibilities
			opts.Capabilities.Compatibilities.ObscureNotAuthorized = !isON(valStr)
		case "PASSIVE_DISCONNECT":
			opts.Capabilities.Compatibilities.PassiveClientDisconnect = isON(valStr)
		case "ALWAYS_RETURN_RESPONSE_INFO":
			opts.Capabilities.Compatibilities.AlwaysReturnResponseInfo = isON(valStr)
		case "RESTORE_ON_RESTART":
			opts.Capabilities.Compatibilities.RestoreSysInfoOnRestart = isON(valStr)
		case "NO_INHERITED_PROPERTIES":
			opts.Capabilities.Compatibilities.NoInheritedPropertiesOnAck = isON(valStr)
		case "MAX_QOS":
			if v, err := strconv.Atoi(valStr); err == nil {
				opts.Capabilities.MaximumQos = byte(v)
			}
		case "RETAIN":
			if isON(valStr) {
				opts.Capabilities.RetainAvailable = 1
			} else {
				opts.Capabilities.RetainAvailable = 0
			}
		case "HAS_WILDCARD_SUB":
			if isON(valStr) {
				opts.Capabilities.WildcardSubAvailable = 1
			} else {
				opts.Capabilities.WildcardSubAvailable = 0
			}
		case "HAS_SUB_ID":
			if isON(valStr) {
				opts.Capabilities.SubIDAvailable = 1
			} else {
				opts.Capabilities.SubIDAvailable = 0
			}
		}
	}
}

func parseMQTTAuthAndACL(hooks *sse.MQTTHooksDispatcher, cfg *DirectiveConfig) {
	// Use unified auth managers
	hooks.SetAuthManagers(cfg.Auth)
	
	// Parse ACL blocks
	aclRoutes := cfg.GetRoutes("ACL")
	for _, acl := range aclRoutes {
		r := &sse.MQTTRoute{
			Method:  acl.Method,
			Path:    acl.Path,
			Handler: acl.Handler,
			Type:    string(acl.Type),
			Inline:  acl.Inline,
		}
		hooks.AddACLRoute(r)
	}

	// Parse ON hooks
	onRoutes := cfg.GetRoutes("ON")
	for _, on := range onRoutes {
		r := &sse.MQTTRoute{
			Method:  on.Method,
			Path:    on.Path,
			Handler: on.Handler,
			Type:    string(on.Type),
			Inline:  on.Inline,
		}
		hooks.AddONRoute(r)
	}
	
	// Parse BRIDGE hooks
	bridgeRoutes := cfg.GetRoutes("BRIDGE")
	for _, br := range bridgeRoutes {
		r := &sse.MQTTRoute{
			Method:  br.Method,
			Path:    br.Path,
			Handler: br.Handler,
			Type:    string(br.Type),
			Inline:  br.Inline,
		}
		hooks.AddBridgeRoute(r)
	}
}

func isON(val string) bool {
	v := strings.ToUpper(strings.TrimSpace(val))
	return v == "ON" || v == "TRUE" || v == "1"
}
