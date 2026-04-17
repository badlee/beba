package sse

import (
	"http-server/plugins/config"
	"http-server/processor"
	"log"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
)

type ScriptedRunner struct {
	Code     string
	IsInline bool
	Protocol string
	Config   *config.AppConfig
}

type jsEvent struct {
	Name string
	Data any
}

const eventShutdown = "__shutdown__"

type Runtime struct {
	client       *Client
	proc         *processor.Processor
	sendFn       func(channel, data string) error
	closeFn      func()
	ctx          any
	subs         map[string][]goja.Callable
	events       map[string][]goja.Callable
	mu           sync.RWMutex
	eventChan    chan jsEvent
	lifecycle    chan jsEvent
	shutdownDone chan struct{}
	shutdownOnce sync.Once
}

func NewRuntime(client *Client, send func(c, d string) error, close func(), ctx any) *Runtime {
	return &Runtime{
		client:       client,
		sendFn:       send,
		closeFn:      close,
		ctx:          ctx,
		subs:         make(map[string][]goja.Callable),
		events:       make(map[string][]goja.Callable),
		eventChan:    make(chan jsEvent, 2048),
		lifecycle:    make(chan jsEvent, 10),
		shutdownDone: make(chan struct{}),
	}
}

func (r *Runtime) HasSub(channel string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.subs[channel]) > 0
}

func (r *Runtime) Run(code string, baseDir string, appCfg *config.AppConfig) error {
	fctx, _ := r.ctx.(fiber.Ctx)
	r.proc = processor.New(baseDir, fctx, appCfg)
	vm := r.proc.Runtime

	// onMessage(callback)
	vm.Set("onMessage", func(call goja.FunctionCall) goja.Value {
		cb, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		r.mu.Lock()
		r.events["message"] = append(r.events["message"], cb)
		r.mu.Unlock()
		return goja.Undefined()
	})

	// onClose(callback)
	vm.Set("onClose", func(call goja.FunctionCall) goja.Value {
		cb, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		r.mu.Lock()
		r.events["close"] = append(r.events["close"], cb)
		r.mu.Unlock()
		return goja.Undefined()
	})

	// onError(callback)
	vm.Set("onError", func(call goja.FunctionCall) goja.Value {
		cb, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		r.mu.Lock()
		r.events["error"] = append(r.events["error"], cb)
		r.mu.Unlock()
		return goja.Undefined()
	})

	// Inject Context
	if r.ctx != nil {
		vm.Set("ctx", r.ctx)
	}

	// close()
	vm.Set("close", func(call goja.FunctionCall) goja.Value {
		if r.closeFn != nil {
			r.closeFn()
		}
		return goja.Undefined()
	})

	// send(data, channel="global")
	vm.Set("send", func(call goja.FunctionCall) goja.Value {
		data := call.Argument(0).String()
		channel := "global"
		if len(call.Arguments) > 1 {
			channel = call.Arguments[1].String()
		}
		if r.sendFn != nil {
			if err := r.sendFn(channel, data); err != nil {
				log.Printf("realtime: send error: %v", err)
			}
		}
		return goja.Undefined()
	})

	// publish(channel, data)
	vm.Set("publish", func(call goja.FunctionCall) goja.Value {
		channel := call.Argument(0).String()
		data := call.Argument(1).String()
		HubInstance.Publish(&Message{
			Channel:   channel,
			Data:      data,
			Source:    "js",
			SenderSID: r.client.ConnID,
		})
		return goja.Undefined()
	})

	// to(channel).publish(data)
	vm.Set("to", func(call goja.FunctionCall) goja.Value {
		channel := call.Argument(0).String()
		obj := vm.NewObject()
		obj.Set("publish", func(call goja.FunctionCall) goja.Value {
			data := call.Argument(0).String()
			HubInstance.Publish(&Message{
				Channel:   channel,
				Data:      data,
				Source:    "js",
				SenderSID: r.client.ConnID,
			})
			return goja.Undefined()
		})
		return obj
	})

	// subscribe(channel, callback)
	vm.Set("subscribe", func(call goja.FunctionCall) goja.Value {
		channel := call.Argument(0).String()
		cb, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			return goja.Undefined()
		}

		r.mu.Lock()
		if _, exists := r.subs[channel]; !exists {
			HubInstance.Subscribe(r.client, channel)
			r.client.chMu.Lock()
			r.client.channels = append(r.client.channels, channel)
			r.client.chMu.Unlock()
		}
		r.subs[channel] = append(r.subs[channel], cb)
		r.mu.Unlock()

		return goja.Undefined()
	})

	// unsubscribe(channel, callback)
	vm.Set("unsubscribe", func(call goja.FunctionCall) goja.Value {
		channel := call.Argument(0).String()
		// callback comparison in goja is tricky.
		// For now, let's just clear all if no callback is passed, or just ignore specific removal if it's hard.
		// But in JS, they might expect it to work.
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.subs, channel)
		// We should probably unsubscribe from hub too if no more subs
		HubInstance.Unsubscribe(r.client, channel)

		return goja.Undefined()
	})

	_, err := vm.RunString(code)

	// Start message loop in background AFTER script compilation
	go r.eventLoop()

	return err
}

func (r *Runtime) Emit(event string, data any) {
	if r.client.ctx.Err() != nil {
		return
	}
	// queue for execution inside the eventLoop's goroutine 
	select {
	case r.eventChan <- jsEvent{Name: event, Data: data}:
	default:
		log.Printf("realtime: eventChan full, dropping event [%s]", event)
	}
}

// EmitDirect calls callbacks synchronously. Use with caution.
func (r *Runtime) EmitDirect(event string, data any) {
	if r.proc == nil || r.proc.Runtime == nil {
		return
	}
	vm := r.proc.Runtime
	r.mu.RLock()
	callbacks := append([]goja.Callable{}, r.events[event]...)
	r.mu.RUnlock()
	for _, cb := range callbacks {
		if _, err := cb(goja.Undefined(), vm.ToValue(data)); err != nil {
			log.Printf("realtime direct JS error [%s]: %v", event, err)
		}
	}
}

// Shutdown queues the "close" heartbeat and a sentinel, then waits for completion.
func (r *Runtime) Shutdown() {
	r.shutdownOnce.Do(func() {
		// Priority lifecycle events
		select {
		case r.lifecycle <- jsEvent{Name: "close", Data: nil}:
		default:
		}
		select {
		case r.lifecycle <- jsEvent{Name: eventShutdown, Data: nil}:
		default:
		}
	})
	select {
	case <-r.shutdownDone:
	case <-time.After(5 * time.Second):
		log.Printf("realtime: shutdown wait timeout")
	}
}

func (r *Runtime) eventLoop() {
	if r.proc == nil || r.proc.Runtime == nil {
		return
	}
	vm := r.proc.Runtime
	defer close(r.shutdownDone)

	for {
		// Priority 1: Check lifecycle channel FIRST and EXCLUSIVELY to ensure
		// shutdown/close are never delayed by a message storm.
		select {
		case evt := <-r.lifecycle:
			if evt.Name == eventShutdown {
				// Final drain of lifecycle events (especially 'close')
				// to ensure onClose callbacks run before we exit.
			drain:
				for {
					select {
					case e := <-r.lifecycle:
						if e.Name != eventShutdown {
							r.dispatchEvent(vm, e)
						}
					default:
						break drain
					}
				}
				return
			}
			r.dispatchEvent(vm, evt)
			continue // Check priority channel again
		default:
		}

		// Priority 2: Normal select between message events and priority events
		select {
		case evt := <-r.lifecycle:
			if evt.Name == eventShutdown {
				goto finalShutdown
			}
			r.dispatchEvent(vm, evt)
		case evt := <-r.eventChan:
			r.dispatchEvent(vm, evt)
		case <-time.After(10 * time.Second):
			// Keep-alive or check context
			if r.client != nil && r.client.ctx.Err() != nil {
				return
			}
		}
	}

finalShutdown:
	// Partial drain if we used goto
	for {
		select {
		case e := <-r.lifecycle:
			if e.Name != eventShutdown {
				r.dispatchEvent(vm, e)
			}
		default:
			return
		}
	}
}

func (r *Runtime) dispatchEvent(vm *goja.Runtime, evt jsEvent) {
	switch evt.Name {
	case "hub_message":
		msg, ok := evt.Data.(*Message)
		if !ok {
			// might be passed as a value in some contexts
			m, mOk := evt.Data.(Message)
			if mOk {
				msg = &m
			} else {
				return
			}
		}

		// Prevent infinite loops: ignore messages sent by this client
		if msg.SenderSID == r.client.ConnID {
			return
		}

		mObj := vm.NewObject()
		mObj.Set("id", msg.ID)
		mObj.Set("channel", msg.Channel)
		mObj.Set("event", msg.Event)
		mObj.Set("data", msg.Data)

		r.mu.RLock()
		callbacks := append([]goja.Callable{}, r.events["message"]...)
		subsCbs := append([]goja.Callable{}, r.subs[msg.Channel]...)
		r.mu.RUnlock()

		// General listeners
		for _, cb := range callbacks {
			if _, err := cb(goja.Null(), mObj); err != nil {
				log.Printf("realtime message error: %v", err)
			}
		}
		// Subscription listeners
		for _, cb := range subsCbs {
			if _, err := cb(goja.Undefined(), mObj); err != nil {
				log.Printf("realtime sub error [%s]: %v", msg.Channel, err)
			}
		}

	case "close":
		r.mu.RLock()
		callbacks := append([]goja.Callable{}, r.events["close"]...)
		r.mu.RUnlock()
		for _, cb := range callbacks {
			if _, err := cb(goja.Undefined()); err != nil {
				log.Printf("realtime onClose error: %v", err)
			}
		}

	default:
		// Generic events (message, error, etc.)
		r.mu.RLock()
		callbacks := append([]goja.Callable{}, r.events[evt.Name]...)
		r.mu.RUnlock()
		for _, cb := range callbacks {
			if _, err := cb(goja.Undefined(), vm.ToValue(evt.Data)); err != nil {
				log.Printf("realtime event error [%s]: %v", evt.Name, err)
			}
		}
	}
}


// Global script cache (compiled only once)
var scriptCache sync.Map

func GetOrCompile(code string, isInline bool) (string, error) {
	// If it's a file path, we might want to read it.
	// But in the binder, the code is already provided as string.
	return code, nil
}
