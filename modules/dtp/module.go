package dtp

import (
	"crypto/sha256"
	"fmt"
	"http-server/modules"
	"log"

	"github.com/dop251/goja"
	"github.com/google/uuid"
	"github.com/limba/dtp/pkg/dtp"
	"github.com/limba/dtp/pkg/dtpclient"
)

type Module struct{}

func (m *Module) Name() string {
	return "dtp"
}

func (m *Module) Doc() string {
	return "DTP Client module"
}

// ToJSObject exposes the module as a SharedObject (processor.RegisterGlobal).
func (m *Module) ToJSObject(vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()
	m.Loader(nil, vm, obj)
	return obj
}

func (m *Module) Loader(_ any, vm *goja.Runtime, moduleObject *goja.Object) {
	// CommonJS support: if exports exists, use it as the target
	module := moduleObject
	if exp := moduleObject.Get("exports"); exp != nil && !goja.IsUndefined(exp) {
		module = exp.ToObject(vm)
	}

	module.Set("newClient", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			panic(vm.ToValue("newClient requires at least 3 arguments: address, deviceID, secret"))
		}

		addr := call.Argument(0).String()
		deviceIDStr := call.Argument(1).String()
		secret := []byte(call.Argument(2).String())

		transport := "tcp"
		if len(call.Arguments) > 3 {
			transport = call.Arguments[3].String()
		}

		mode := "sync"
		if len(call.Arguments) > 4 {
			mode = call.Arguments[4].String()
		}

		id, err := uuid.Parse(deviceIDStr)
		if err != nil {
			// Fallback: hash name to UUID
			h := sha256.Sum256([]byte(deviceIDStr))
			id, _ = uuid.FromBytes(h[:16])
		}

		client := dtpclient.NewClient(addr, id, secret, transport, mode)

		handlers := make(map[string][]goja.Callable)

		return vm.ToValue(map[string]interface{}{
			"connect": func() goja.Value {
				err := client.Connect()
				if err != nil {
					// Error event
					if h, ok := handlers["error"]; ok {
						for _, fn := range h {
							_, _ = fn(goja.Undefined(), vm.ToValue(err.Error()))
						}
					}
					panic(vm.ToValue(fmt.Sprintf("connect failed: %v", err)))
				}
				// Connect event
				if h, ok := handlers["connect"]; ok {
					for _, fn := range h {
						_, _ = fn(goja.Undefined())
					}
				}
				return goja.Undefined()
			},
			"on": func(call goja.FunctionCall) goja.Value {
				event := call.Argument(0).String()
				handler := call.Argument(1)
				if fn, ok := goja.AssertFunction(handler); ok {
					// Register internal JS event
					if event == "connect" || event == "error" {
						handlers[event] = append(handlers[event], fn)
						return goja.Undefined()
					}

					// Map string to dtp.Type
					var msgType dtp.Type
					switch event {
					case "ack":
						msgType = dtp.TypeACK
					case "nack":
						msgType = dtp.TypeNACK
					case "err":
						msgType = dtp.TypeError
					case "data":
						msgType = dtp.TypeData
					case "ping":
						msgType = dtp.TypePing
					case "pong":
						msgType = dtp.TypePong
					case "cmd":
						msgType = dtp.TypeCmd
					case "event":
						msgType = dtp.TypeEvent
					default:
						// Try integer
						msgType = dtp.Type(call.Argument(0).ToInteger())
					}

					client.On(msgType, func(messageID uint64, packet *dtp.Packet) {
						// Convert packet to JS object
						pktObj := vm.NewObject()
						pktObj.Set("ID", packet.ID)
						pktObj.Set("Type", int(packet.Type))
						pktObj.Set("Subtype", int(packet.Subtype))
						pktObj.Set("Payload", string(packet.Payload))

						_, err := fn(goja.Undefined(), pktObj, vm.ToValue(messageID))
						if err != nil {
							log.Printf("DTP JS handler error: %v", err)
						}
					})
				}
				return goja.Undefined()
			},
			"sendData": func(call goja.FunctionCall) goja.Value {
				subtypeStr := call.Argument(0).String()
				payload := call.Argument(1).Export()
				needAck := true
				if len(call.Arguments) > 2 {
					needAck = call.Argument(2).ToBoolean()
				}

				// Convert subtype from string if possible (e.g. "SENSOR_DATA" -> 0x01)
				subtype := dtp.TypeData.SubTypeFromString(subtypeStr)

				if err := client.SendData(subtype, payload, needAck); err != nil {
					panic(vm.ToValue(fmt.Sprintf("sendData failed: %v", err)))
				}
				return goja.Undefined()
			},
			"ping": func() goja.Value {
				payload, err := client.Ping()
				if err != nil {
					panic(vm.ToValue(fmt.Sprintf("ping failed: %v", err)))
				}
				return vm.ToValue(string(payload))
			},
			"disconnect": func() goja.Value {
				if err := client.Disconnect(); err != nil {
					panic(vm.ToValue(fmt.Sprintf("disconnect failed: %v", err)))
				}
				return goja.Undefined()
			},
		})
	})
}

func init() {
	modules.RegisterModule(&Module{})
}
