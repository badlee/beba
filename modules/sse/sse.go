package sse

import (
	"bufio"
	"fmt"
	"http-server/modules"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
)

type Module struct{}

func init() {
	modules.RegisterModule(&Module{})

}

func (s *Module) Name() string {
	return "sse"
}

func (s *Module) Doc() string {
	return "SSE module"
}

type QueuedMessage struct {
	Channel string
	Event   string
	Data    string
}

func (s *Module) Loader(ctx fiber.Ctx, vm *goja.Runtime, module *goja.Object) {
	// Expose
	sseObj := vm.NewObject()

	// attach(val, [key])
	sseObj.Set("attach", func(call goja.FunctionCall) goja.Value {
		val := call.Argument(0).Export()
		key := "sid"
		if len(call.Arguments) > 1 {
			key = call.Arguments[1].String()
		}

		if s, ok := val.(string); ok {
			ctx.Locals("sse_sid", s)
		} else if obj, ok := val.(*goja.Object); ok {
			// Try to get sid from cookies object
			getFn := obj.Get("get")
			if fn, ok := goja.AssertFunction(getFn); ok {
				res, _ := fn(goja.Undefined(), vm.ToValue(key))
				ctx.Locals("sse_sid", res.String())
			}
		}
		return goja.Undefined()
	})

	// to(channel).publish(event, data)
	sseObj.Set("to", func(call goja.FunctionCall) goja.Value {
		channel := call.Argument(0).String()
		pubObj := vm.NewObject()
		pubObj.Set("publish", func(call goja.FunctionCall) goja.Value {
			event := call.Argument(0).String()
			data := call.Argument(1).String()
			queueMessage(ctx, channel, event, data)
			return goja.Undefined()
		})
		return pubObj
	})

	// Global broadcast (alias for to("global"))
	sseObj.Set("publish", func(call goja.FunctionCall) goja.Value {
		event := call.Argument(0).String()
		data := call.Argument(1).String()
		queueMessage(ctx, "global", event, data)
		return goja.Undefined()
	})

	// To current session
	sseObj.Set("send", func(call goja.FunctionCall) goja.Value {
		event := call.Argument(0).String()
		data := call.Argument(1).String()

		sid := ctx.Locals("sse_sid")
		if sid == nil {
			sid = ctx.Cookies("sid")
		}

		if s, ok := sid.(string); ok && s != "" {
			queueMessage(ctx, "sid:"+s, event, data)
		}
		return goja.Undefined()
	})

	module.Set("exports", sseObj)
}

func queueMessage(ctx fiber.Ctx, channel, event, data string) {
	var queue []QueuedMessage
	if val := ctx.Locals("sse_queue"); val != nil {
		queue = val.([]QueuedMessage)
	}
	queue = append(queue, QueuedMessage{Channel: channel, Event: event, Data: data})
	ctx.Locals("sse_queue", queue)
}

func Flush(ctx fiber.Ctx) {
	sseManager := ctx.Locals("sseManager").(*Manager)
	if sseManager == nil {
		return
	}

	val := ctx.Locals("sse_queue")
	if val == nil {
		return
	}

	queue := val.([]QueuedMessage)
	if len(queue) == 0 {
		return
	}

	// Wait a bit before sending to allow connection establishment
	go func() {
		time.Sleep(500 * time.Millisecond)
		for _, msg := range queue {
			sseManager.SendToChannel(msg.Channel, msg.Event, msg.Data)
		}
	}()
}

type Message struct {
	Event string
	Data  string
}

type Manager struct {
	clients map[chan Message]map[string]bool // channel -> set of channel names
	mu      sync.Mutex
}

func NewManager() *Manager {
	return &Manager{
		clients: make(map[chan Message]map[string]bool),
	}
}

func (m *Manager) AddClient(channels []string) chan Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan Message, 10)
	chanMap := make(map[string]bool)
	for _, c := range channels {
		if c != "" {
			chanMap[c] = true
		}
	}
	m.clients[ch] = chanMap
	return ch
}

func (m *Manager) RemoveClient(ch chan Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, ch)
	close(ch)
}

func (m *Manager) SendToChannel(channel, event, data string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg := Message{Event: event, Data: data}
	for ch, subs := range m.clients {
		if subs[channel] {
			select {
			case ch <- msg:
			default:
			}
		}
	}
}

func (m *Manager) Handler(c fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")

	// Channels to subscribe to
	chans := []string{"global"}
	if sid := c.Cookies("sid"); sid != "" {
		chans = append(chans, "sid:"+sid)
	}

	// Parse channels from query: ?channels=news,chat
	if qChans := c.Query("channels"); qChans != "" {
		parts := strings.Split(qChans, ",")
		for _, p := range parts {
			chans = append(chans, strings.TrimSpace(p))
		}
	}

	ch := m.AddClient(chans)

	return c.SendStreamWriter(func(w *bufio.Writer) {
		defer m.RemoveClient(ch)
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if msg.Event != "" {
					fmt.Fprintf(w, "event: %s\n", msg.Event)
				}
				fmt.Fprintf(w, "data: %s\n\n", msg.Data)
				w.Flush()
			case <-time.After(15 * time.Second):
				// Heartbeat
				fmt.Fprintf(w, ": heartbeat\n\n")
				w.Flush()
			}
		}
	})
}
