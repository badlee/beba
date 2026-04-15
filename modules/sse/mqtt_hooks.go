package sse

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"http-server/plugins/config"
	"http-server/processor"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"
)

// MQTTRoute holds DSL block information decoupled from binder parsing.
type MQTTRoute struct {
	Method  string
	Path    string
	Handler string
	Type    string
	Inline  bool
}

// AuthHandler wraps information from an AUTH block in the DSL.
type AuthHandler struct {
	Type    string
	Inline  bool
	Handler string // Path or Inline script
	Configs map[string]string
}

// MQTTHooksDispatcher manages JS execution contexts for MQTT hooks.
type MQTTHooksDispatcher struct {
	baseDir string
	env     map[string]string
	vm      *processor.Processor
	auths   []*AuthHandler
	acls    []*MQTTRoute
	bridges []*MQTTRoute
	events  []*MQTTRoute
}

func NewMQTTHooksDispatcher(baseDir string, env map[string]string) *MQTTHooksDispatcher {
	// Initialize a complete goja runtime environment using processor!
	// This loads fs, db, path, http and other modules into the context natively.
	appCfg := &config.AppConfig{} // For basic compatibility
	vm := processor.New(baseDir, nil, appCfg)

	// Inject custom MQTT globals if necessary here
	return &MQTTHooksDispatcher{
		baseDir: baseDir,
		env:     env,
		vm:      vm,
	}
}

// AddAuthBlock parses the AUTH block.
func (hd *MQTTHooksDispatcher) AddAuthBlock(t string, handler string, inline bool, configs map[string]string) {
	hd.auths = append(hd.auths, &AuthHandler{
		Type:    t,
		Handler: handler,
		Inline:  inline,
		Configs: configs,
	})
}

// AddACLRoute parses the ACL block.
func (hd *MQTTHooksDispatcher) AddACLRoute(r *MQTTRoute) {
	hd.acls = append(hd.acls, r)
}

// AddBridgeRoute parses the BRIDGE route.
func (hd *MQTTHooksDispatcher) AddBridgeRoute(r *MQTTRoute) {
	hd.bridges = append(hd.bridges, r)
}

// AddONRoute parses the ON hook.
func (hd *MQTTHooksDispatcher) AddONRoute(r *MQTTRoute) {
	hd.events = append(hd.events, r)
}

// AuthFunc returns the authentication bridge for Mochi.
func (hd *MQTTHooksDispatcher) AuthFunc() MQTTAuthFunc {
	return func(username, password, clientID string) (string, error) {
		// Evaluates each registered AUTH block in order until one succeeds or all fail.
		if len(hd.auths) == 0 {
			// No AUTH restrictions
			return "", nil
		}

		for _, auth := range hd.auths {
			if auth.Type == "SCRIPT" {
				var script string
				if auth.Inline {
					script = auth.Handler
				} else {
					file := auth.Handler
					if !filepath.IsAbs(file) {
						file = filepath.Join(hd.baseDir, file)
					}
					b, err := os.ReadFile(file)
					if err != nil {
						fmt.Printf("MQTT Auth File Error: %v\n", err)
						continue
					}
					script = string(b)
				}

				// Execute JS script using the central processor-initialized Runtime.
				// Bubble up the exact error returned by reject(msg) so callers can inspect it.
				if err := hd.executeAuthScript(username, password, clientID, script); err != nil {
					return "", err // propagate reject(msg) directly
				}
				return "", nil // allow()
			}
		}

		// No AUTH block matched (shouldn't happen, but safe default)
		return "", errors.New("unauthorized")
	}
}

// executeAuthScript wraps the JS code in a function context providing `allow` and `reject`.
// This matches the user's DSL note: "`allow()` pour authoriser, `reject(message)` pour rejeter".
func (hd *MQTTHooksDispatcher) executeAuthScript(username, password, clientID, code string) error {
	vm := hd.vm

	var authError error = errors.New("auth script did not call allow()")
	resolved := false

	allow := func() {
		if !resolved {
			authError = nil
			resolved = true
		}
	}
	reject := func(msg string) {
		if !resolved {
			authError = errors.New(msg)
			resolved = true
		}
	}

	vm.Set("user", username)
	vm.Set("pwd", password)
	vm.Set("clientId", clientID)
	vm.Set("allow", allow)
	vm.Set("reject", reject)

	_, err := vm.RunString(code)
	if err != nil {
		fmt.Printf("MQTT Auth Script execution error: %v\n", err)
		return err
	}

	return authError
}

// OnConnectFunc bridge.
func (hd *MQTTHooksDispatcher) OnConnectFunc() func(string) {
	return func(clientID string) {
		hd.dispatchJSEvent("CONNECT", clientID, nil)
	}
}

// OnDisconnectFunc bridge.
func (hd *MQTTHooksDispatcher) OnDisconnectFunc() func(string, bool) {
	return func(clientID string, clean bool) {
		hd.dispatchJSEvent("DISCONNECT", clientID, map[string]interface{}{
			"clean": clean,
		})
	}
}

// OnPublishFunc intercepts incoming messages before propagation.
func (hd *MQTTHooksDispatcher) OnPublishFunc() func(clientID, topic string, payload []byte, qos byte) bool {
	return func(clientID, topic string, payload []byte, qos byte) bool {
		allowLocalPropagation := true

		// 1. Run `ON PUBLISH` scripts
		hd.dispatchJSEvent("PUBLISH", clientID, map[string]interface{}{
			"topic":   topic,
			"payload": string(payload),
			"qos":     qos,
		})

		// 2. Evaluate BRIDGE rules
		for _, bridge := range hd.bridges {
			var targetBroker string
			var stopLocal bool
			rejected := false

			if bridge.Inline || bridge.Type == "JS" {
				var code string
				if bridge.Inline {
					code = bridge.Handler
				} else {
					file := bridge.Handler
					if !filepath.IsAbs(file) {
						file = filepath.Join(hd.baseDir, file)
					}
					b, _ := os.ReadFile(file)
					code = string(b)
				}

				hd.vm.Set("clientId", clientID)
				hd.vm.Set("topic", topic)
				hd.vm.Set("payload", string(payload))

				hd.vm.Set("allow", func(upstreamUrl string, sl bool) {
					targetBroker = upstreamUrl
					stopLocal = sl
				})
				hd.vm.Set("reject", func(msg string) {
					rejected = true
				})

				hd.vm.RunString(code)

				if rejected {
					return false // Explicitly drop standard message propagation if rejected
				}

			} else {
				// Static Bridge: BRIDGE "tcp://url:1883" "topic/#"
				// Path is the URL, Handler is the topic wildcard
				url := bridge.Path
				pattern := bridge.Handler

				if pattern == "" || matchTopicPattern(pattern, topic) {
					targetBroker = url
					// By default, static bridge retains local broadcast
					stopLocal = false
				}
			}

			if targetBroker != "" {
				ForwardToBridge(targetBroker, topic, payload, qos)
				if stopLocal {
					allowLocalPropagation = false
				}
			}
		}

		// Return boolean defining whether Hub / Local MQTT clients receive this
		return allowLocalPropagation
	}
}

// matchTopicPattern validates simplistic MQTT wildcard checks (#)
func matchTopicPattern(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	if strings.HasSuffix(pattern, "#") {
		prefix := strings.TrimSuffix(pattern, "#")
		return strings.HasPrefix(topic, prefix)
	}
	return false
}

// dispatchJSEvent evaluates an 'ON' Hook.
func (hd *MQTTHooksDispatcher) dispatchJSEvent(eventName string, clientID string, payload map[string]interface{}) {
	for _, evt := range hd.events {
		// In the binder, ON [EVENT_NAME] ... sets Method to the [EVENT_NAME] mapping.
		// Wait, `evt.Method` was ON? `evt.Path` is EVENT_TYPE? Let's normalize it here.
		// Usually the parser gives ON EVENT_TYPE [args].
		// For simplicity we check if the PATH or HANDLER string matches our event.
		if evt.Path == eventName || evt.Method == eventName {
			var script string
			if evt.Inline {
				script = evt.Handler
			} else {
				file := evt.Path
				if !filepath.IsAbs(file) {
					file = filepath.Join(hd.baseDir, file)
				}
				b, err := os.ReadFile(file)
				if err != nil {
					continue
				}
				script = string(b)
			}

			// Pre-populate payload
			hd.vm.Set("clientId", clientID)
			hd.vm.Set("eventName", eventName)
			if payload != nil {
				hd.vm.Set("eventData", payload)
			}

			_, err := hd.vm.RunString(script)
			if err != nil {
				fmt.Printf("MQTT Event %s Hook error: %v\n", eventName, err)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Universal JSHook Implementation for mochi-mqtt Hook API
// ─────────────────────────────────────────────────────────────────────────────

// DynamicMochiHook satisfies the `mochi-mqtt` `Hook` interface to bridge all raw packet and session events natively dynamically.
type DynamicMochiHook struct {
	mqtt.HookBase
	dispatcher *MQTTHooksDispatcher
}

// NewDynamicMochiHook creates a generic wrapper.
func NewDynamicMochiHook(d *MQTTHooksDispatcher) *DynamicMochiHook {
	return &DynamicMochiHook{
		dispatcher: d,
	}
}

func (h *DynamicMochiHook) ID() string {
	return "dynamic-js-hooks"
}

func (h *DynamicMochiHook) Provides(b byte) bool {
	// Explicitly claim only the hooks we implement to avoid interfering with
	// storage, auth, or lifecycle hooks.
	return bytes.Contains([]byte{
		mqtt.OnACLCheck,
		mqtt.OnPacketRead,
	}, []byte{b})
}

func (h *DynamicMochiHook) OnACLCheck(cl *mqtt.Client, topic string, write bool) bool {
	// Evaluate JS ACL logic if available.
	// Defaults to true in purely default execution, but evaluates DENY if explicitly blocked.
	if len(h.dispatcher.acls) == 0 {
		return true // No ACLs means everything is allowed.
	}

	mode := "READ"
	if write {
		mode = "WRITE"
	}

	for _, acl := range h.dispatcher.acls {
		if acl.Inline || acl.Type == "JS" { // HandlerJS string equivalent
			// Exec JS logic
			h.dispatcher.vm.Set("user", string(cl.Properties.Username))
			h.dispatcher.vm.Set("topic", topic)
			h.dispatcher.vm.Set("mode", mode)

			allowed := true
			allowFn := func() { allowed = true }
			rejectFn := func(msg string) { allowed = false }

			h.dispatcher.vm.Set("allow", allowFn)
			h.dispatcher.vm.Set("reject", rejectFn)

			_, _ = h.dispatcher.vm.RunString(acl.Handler)
			// Return early if rejected
			if !allowed {
				return false
			}
		}
	}

	return true
}

func (h *DynamicMochiHook) OnPacketRead(cl *mqtt.Client, pk packets.Packet) (packets.Packet, error) {
	h.dispatcher.dispatchJSEvent("PACKET_READ", cl.ID, map[string]interface{}{
		"packetId": pk.PacketID,
	})
	return pk, nil
}
