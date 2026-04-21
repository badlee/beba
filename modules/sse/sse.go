package sse

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"beba/modules"
	"log"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const (
	shardCount   = 64
	clientBuf    = 1024
	ringSize     = 2048
	heartbeatInt = 15 * time.Second
	flushInt     = 50 * time.Millisecond
	batchSize    = 16
)

// -------------------- TYPES --------------------
type Module struct{}

type Message struct {
	ID        string `json:"id,omitempty"`
	Channel   string `json:"channel,omitempty"`
	Event     string `json:"event"`
	Data      string `json:"data"`
	Source    string `json:"-"` // "mqtt", "sse", "ws", "js"
	SenderSID string `json:"-"` // prevent loops
}
type Client struct {
	sid      string
	ConnID   string // Unique per physical connection for loop prevention
	message  chan *Message
	channels []string
	closed   atomic.Bool
	ctx      context.Context
	cancel   context.CancelFunc
	chMu     sync.Mutex
}

func (c *Client) HasChannel(channel string) bool {
	c.chMu.Lock()
	defer c.chMu.Unlock()
	for _, ch := range c.channels {
		if ch == channel {
			return true
		}
	}
	return false
}

func (c *Client) Messages() <-chan *Message {
	return c.message
}

func (c *Client) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	c.closed.Store(true)
}

type Topic struct {
	ring       [ringSize]*Message
	writeIndex uint64
	subs       map[*Client]struct{}
}

type Shard struct {
	subscribe   chan subReq // fix: valeur, pas pointeur
	unsubscribe chan subReq // fix: valeur, pas pointeur
	publish     chan *Message
	topics      map[string]*Topic
}

type subReq struct {
	client  *Client
	channel string
}

type Hub struct {
	shards       [shardCount]*Shard
	publishHooks []func(*Message)
	muHooks      sync.RWMutex
}

var HubInstance *Hub

func GetHub() *Hub {
	if HubInstance == nil {
		HubInstance = NewHub()
	}
	return HubInstance
}

func (h *Hub) Reset() {
	h.muHooks.Lock()
	h.publishHooks = nil
	h.muHooks.Unlock()

	for i := 0; i < shardCount; i++ {
		oldShard := h.shards[i]
		h.shards[i] = &Shard{
			subscribe:   make(chan subReq, 256),
			unsubscribe: make(chan subReq, 256),
			publish:     make(chan *Message, 256),
			topics:      make(map[string]*Topic),
		}
		// We can't easily kill the old shard's goroutine without a stop channel,
		// but since we replace it in the hub, new requests won't reach it.
		// In a real system we'd have a context/stop signal per shard.
		go h.shards[i].run()
		_ = oldShard // let it fade out
	}
}

// -------------------- MODULE API --------------------
func (s *Module) Name() string { return "sse" }
func (s *Module) Doc() string  { return "Ultra-fast SSE 1M+ connections" }

// ToJSObject exposes the module as a SharedObject (processor.RegisterGlobal).
func (m *Module) ToJSObject(vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()
	m.Loader(nil, vm, obj)
	return obj
}

func (s *Module) Loader(c any, vm *goja.Runtime, moduleObject *goja.Object) {
	// CommonJS support: if exports exists, use it as the target
	module := moduleObject
	if exp := moduleObject.Get("exports"); exp != nil && !goja.IsUndefined(exp) {
		module = exp.ToObject(vm)
	}

	sseObj := module
	ctx, isFiberCtx := c.(fiber.Ctx)

	sseObj.Set("attach", func(call goja.FunctionCall) goja.Value {
		val := call.Argument(0).Export()
		key := "sid"
		if len(call.Arguments) > 1 {
			key = call.Arguments[1].String()
		}
		sid := ""
		if s, ok := val.(string); ok {
			sid = s
		} else if obj, ok := val.(*goja.Object); ok {
			getFn := obj.Get("get")
			if fn, ok := goja.AssertFunction(getFn); ok {
				res, _ := fn(goja.Undefined(), vm.ToValue(key))
				sid = res.String()
			}
		}
		if sid != "" && isFiberCtx {
			ctx.Locals("sse_sid", sid)
		}
		return goja.Undefined()
	})

	sseObj.Set("to", func(call goja.FunctionCall) goja.Value {
		channel := call.Argument(0).String()
		pubObj := vm.NewObject()
		pubObj.Set("publish", func(call goja.FunctionCall) goja.Value {
			event := call.Argument(0).String()
			data := call.Argument(1).String()
			sid := ""
			if isFiberCtx {
				if v := ctx.Locals("sse_sid"); v != nil {
					sid = v.(string)
				}
			}
			HubInstance.Publish(&Message{
				Channel:   channel,
				Event:     event,
				Data:      data,
				Source:    "js",
				SenderSID: sid,
			})
			return goja.Undefined()
		})
		return pubObj
	})

	sseObj.Set("publish", func(call goja.FunctionCall) goja.Value {
		event := call.Argument(0).String()
		data := call.Argument(1).String()
		sid := ""
		if isFiberCtx {
			if v := ctx.Locals("sse_sid"); v != nil {
				sid = v.(string)
			}
		}
		HubInstance.Publish(&Message{
			Channel:   "global",
			Event:     event,
			Data:      data,
			Source:    "js",
			SenderSID: sid,
		})
		return goja.Undefined()
	})

	sseObj.Set("send", func(call goja.FunctionCall) goja.Value {
		event := call.Argument(0).String()
		data := call.Argument(1).String()
		if !isFiberCtx {
			return goja.Undefined()
		}
		sid := ctx.Locals("sse_sid")
		if sid == nil {
			sid = ctx.Cookies("sid")
		}
		if s, ok := sid.(string); ok && s != "" {
			HubInstance.Publish(&Message{Channel: "sid:" + s, Event: event, Data: data})
		}
		return goja.Undefined()
	})

}

// -------------------- HUB --------------------
func NewHub() *Hub {
	h := &Hub{}
	for i := 0; i < shardCount; i++ {
		sh := &Shard{
			subscribe:   make(chan subReq, 8192), // fix: chan subReq
			unsubscribe: make(chan subReq, 8192), // fix: chan subReq
			publish:     make(chan *Message, 8192),
			topics:      make(map[string]*Topic),
		}
		h.shards[i] = sh
		go sh.run()
	}
	return h
}

func NewClient(sid string, channels []string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		sid:      sid,
		ConnID:   uuid.New().String(),
		message:  make(chan *Message, clientBuf),
		channels: channels,
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (h *Hub) shard(key string) *Shard {
	return h.shards[fnv64(key)%shardCount]
}

func (h *Hub) Subscribe(client *Client, channel string) {
	h.shard(channel).subscribe <- subReq{client: client, channel: channel}
}

func (h *Hub) Unsubscribe(client *Client, channel string) {
	h.shard(channel).unsubscribe <- subReq{client: client, channel: channel}
}

func (h *Hub) Publish(msg *Message) {
	if msg == nil || msg.Channel == "" {
		return
	}
	h.shard(msg.Channel).publish <- msg
}

// AddPublishHook enregistre un callback appelé à chaque publication Hub (ex: MQTT sync)
func (h *Hub) AddPublishHook(fn func(*Message)) {
	log.Printf("sse: Hub.AddPublishHook - waiting for lock...")
	h.muHooks.Lock()
	h.publishHooks = append(h.publishHooks, fn)
	h.muHooks.Unlock()
	log.Printf("sse: Hub.AddPublishHook - hook registered (total=%d)", len(h.publishHooks))
}

// RemovePublishHook désenregistre un callback
func (h *Hub) RemovePublishHook(fn func(*Message)) {
	h.muHooks.Lock()
	defer h.muHooks.Unlock()
	targetPtr := reflect.ValueOf(fn).Pointer()
	for i, hook := range h.publishHooks {
		if reflect.ValueOf(hook).Pointer() == targetPtr {
			h.publishHooks = append(h.publishHooks[:i], h.publishHooks[i+1:]...)
			break
		}
	}
}

// RemoveAllPublishHooks réinitialise les hooks (utile pour les tests)
func (h *Hub) RemoveAllPublishHooks() {
	h.muHooks.Lock()
	h.publishHooks = nil
	h.muHooks.Unlock()
}

// -------------------- SHARD LOOP --------------------
func (s *Shard) run() {
	ticker := time.NewTicker(heartbeatInt)
	defer ticker.Stop()
	for {
		select {
		case sub := <-s.subscribe:
			t, ok := s.topics[sub.channel]
			if !ok {
				t = &Topic{subs: make(map[*Client]struct{})}
				s.topics[sub.channel] = t
			}
			t.subs[sub.client] = struct{}{}

		case unsub := <-s.unsubscribe:
			if t, ok := s.topics[unsub.channel]; ok {
				delete(t.subs, unsub.client)
				if len(t.subs) == 0 {
					delete(s.topics, unsub.channel) // libère le ring buffer
				}
			}

		case msg := <-s.publish:
			t, ok := s.topics[msg.Channel]
			if !ok {
				t = &Topic{subs: make(map[*Client]struct{})}
				s.topics[msg.Channel] = t // fix: assignation unique
			}
			index := atomic.AddUint64(&t.writeIndex, 1) - 1
			t.ring[index%ringSize] = msg

			// Sync aux hooks (ex: Mochi MQTT) — fix: release lock before executing to avoid deadlocks
			HubInstance.muHooks.RLock()
			var hooks []func(*Message)
			if len(HubInstance.publishHooks) > 0 {
				hooks = make([]func(*Message), len(HubInstance.publishHooks))
				copy(hooks, HubInstance.publishHooks)
			}
			HubInstance.muHooks.RUnlock()

			if len(hooks) > 0 {
				for _, hook := range hooks {
					hook(msg)
				}
			}

			for c := range t.subs {
				if c.closed.Load() {
					continue
				}
				select {
				case c.message <- msg:
				default:
				}
			}

		case <-ticker.C:
			for _, t := range s.topics {
				for c := range t.subs {
					if c.closed.Load() {
						continue
					}
					select {
					case c.message <- &Message{Event: "heartbeat"}:
					default:
					}
				}
			}
		}
	}
}

// -------------------- FIBER SSE HANDLER --------------------
func Handler(c fiber.Ctx, runner ...*ScriptedRunner) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")
	c.Set("X-Accel-Buffering", "no")
	c.Set("Keep-Alive", "timeout=60")

	// fix: context.Background() — le contexte Fiber est recyclé après le retour du handler
	ctx, cancel := context.WithCancel(context.Background())
	reqDone := c.Context().Done()
	sid, channels := parseChannels(c)
	c.Response().Header.Set("Client-Id", sid)
	c.Response().Header.Set("X-Client-Id", sid)

	client := &Client{
		sid:      sid,
		ConnID:   sid, // For SSE, we can keep sid if it's unique, but Handler might use sid-cookie
		message:  make(chan *Message, clientBuf),
		channels: channels,
		ctx:      ctx,
		cancel:   cancel,
	}
	if client.ConnID == "" || client.ConnID == sid {
		client.ConnID = uuid.New().String()
	}

	for _, ch := range channels {
		HubInstance.Subscribe(client, ch)
	}

	var scripted *Runtime
	if len(runner) > 0 && runner[0] != nil {
		r := runner[0]
		scripted = NewRuntime(client, nil, func() {
			cancel()
			client.closed.Store(true)
		}, c)
		if err := scripted.Run(r.Code, ".", r.Config); err != nil {
			log.Printf("SSE JS runtime error: %v", err)
		}
	}

	return c.SendStreamWriter(func(w *bufio.Writer) {
		// fix: cleanup garanti à la sortie du stream writer
		defer func() {
			cancel()
			client.closed.Store(true)
			for _, ch := range client.channels {
				HubInstance.Unsubscribe(client, ch)
			}
			if scripted != nil {
				scripted.Shutdown()
			}
		}()

		// Update scripted runtime with real sendFn
		if scripted != nil {
			scripted.sendFn = func(channel, data string) error {
				if channel == "" {
					channel = "message"
				}
				fmt.Fprintf(w, "event: %s\n", channel)
				fmt.Fprintf(w, "data: %s\n\n", data)
				return w.Flush()
			}
		}

		fmt.Fprintf(w, "id: %s\n\n", sid)
		fmt.Fprintf(w, "retry: 5000\n\n")
		w.Flush()

		batch := make([]*Message, 0, batchSize)
		flushBatch := func() {
			for _, msg := range batch {
				if msg.Event != "heartbeat" {
					if msg.Event != "" {
						fmt.Fprintf(w, "event: %s\n", msg.Event)
					}
					fmt.Fprintf(w, "data: %s\n\n", msg.Data)
				} else {
					fmt.Fprintf(w, ": heartbeat\n\n") // commentaire SSE, ignoré par EventSource
				}
			}
			if len(batch) > 0 {
				w.Flush()
				batch = batch[:0]
			}
		}
		flushTicker := time.NewTicker(flushInt)
		defer flushTicker.Stop()
		for {
			select {
			case msg := <-client.message:
				if msg == nil {
					return
				}
				
				if scripted != nil && scripted.HasSub(msg.Channel) {
					scripted.Emit("hub_message", msg)
				} else {
					batch = append(batch, msg)
					if len(batch) >= batchSize {
						flushBatch()
					}
				}
			case <-flushTicker.C:
				if len(batch) > 0 {
					flushBatch() // flush silencieux — pas de heartbeat ici
				} else {
					fmt.Fprintf(w, ": heartbeat\n\n")
					if err := w.Flush(); err != nil {
						return
					}
				}
			case <-ctx.Done():
				return
			case <-reqDone:
				return
			}
		}
	})
}

// -------------------- UTIL --------------------
func fnv64(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}

type SSECtx interface {
	Params(key string, defaultValue ...string) string
	Query(key string, defaultValue ...string) string
	Cookies(key string, defaultValue ...string) string
	Locals(key any, defaultValue ...any) any
}

func parseChannels(c SSECtx) (string, []string) {
	// Channels to subscribe to
	sid := uuid.New().String()
	chans := []string{"global"}
	if l := c.Locals("channels"); l != nil {
		// force reset channels to use only the provided ones
		chans = []string{}
		if s, ok := l.(string); ok && s != "" {
			parts := strings.Split(s, ",")
			for _, p := range parts {
				chans = append(chans, strings.TrimSpace(p))
			}
		} else if ss, ok := l.([]string); ok {
			chans = append(chans, ss...)
		}
	} else {

		if p := c.Params("id"); p != "" {
			sid = p
		} else if p := c.Query("id"); p != "" {
			sid = p
		} else if p := c.Cookies("sid"); p != "" {
			sid = p
		} else if p := c.Locals("id"); p != nil {
			sid = p.(string)
		}

		chans = append(chans, "sid:"+strings.TrimSpace(sid))

		if p := c.Params("channel"); p != "" {
			chans = append(chans, strings.TrimSpace(p))
		}
		if p := c.Query("channel"); p != "" {
			chans = append(chans, strings.TrimSpace(p))
		}

		// Parse channels from query: ?channels=news,chat
		if qChans := c.Query("channels"); qChans != "" {
			parts := strings.Split(qChans, ",")
			for _, p := range parts {
				chans = append(chans, strings.TrimSpace(p))
			}
		}
	}
	// Deduplication des channels
	seen := make(map[string]struct{}, len(chans))
	unique := chans[:0]
	for _, ch := range chans {
		if _, ok := seen[ch]; !ok {
			seen[ch] = struct{}{}
			unique = append(unique, ch)
		}
	}
	return sid, unique
}

// -------------------- INIT --------------------
func init() {
	HubInstance = NewHub()
	modules.RegisterModule(&Module{})
}
