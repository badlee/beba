package sse

import (
	"os"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// TestMain ensures Hub isolation between tests.
func TestMain(m *testing.M) {
	// Root initialization
	if HubInstance == nil {
		HubInstance = &Hub{
			shards: [shardCount]*Shard{},
		}
		for i := 0; i < shardCount; i++ {
			HubInstance.shards[i] = &Shard{
				subscribe:   make(chan subReq, 256),
				unsubscribe: make(chan subReq, 256),
				publish:     make(chan *Message, 256),
				topics:      make(map[string]*Topic),
			}
			go HubInstance.shards[i].run()
		}
	}
	code := m.Run()
	os.Exit(code)
}

// resetHub cleans everything for the next test.
func resetHub() {
	if HubInstance != nil {
		HubInstance.Reset()
	}
}

func newTestClient(t *testing.T, sid string, channels []string) chan *Message {
	t.Helper()
	ch := make(chan *Message, 1024)
	client := &Client{
		sid:      sid,
		message:  ch,
		channels: channels,
	}
	for _, ch := range channels {
		HubInstance.Subscribe(client, ch)
	}
	t.Cleanup(func() {
		for _, ch := range channels {
			HubInstance.Unsubscribe(client, ch)
		}
	})
	return ch
}

func recv(t *testing.T, ch chan *Message, label string) *Message {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: timeout waiting for message", label)
		return nil
	}
}

func noRecv(t *testing.T, ch chan *Message, label string) {
	t.Helper()
	select {
	case msg := <-ch:
		if msg.Event == "heartbeat" {
			// ignore heartbeats in noRecv
			noRecv(t, ch, label)
			return
		}
		t.Fatalf("%s: expected nothing, got %+v", label, msg)
	case <-time.After(100 * time.Millisecond):
		// OK
	}
}

func callJS(t *testing.T, vm *goja.Runtime, obj *goja.Object, method string, args ...any) goja.Value {
	t.Helper()
	fn, ok := goja.AssertFunction(obj.Get(method))
	if !ok {
		t.Fatalf("%s is not a function", method)
	}
	vArgs := make([]goja.Value, len(args))
	for i, a := range args {
		vArgs[i] = vm.ToValue(a)
	}
	res, err := fn(obj, vArgs...)
	if err != nil {
		t.Fatalf("%s() error: %v", method, err)
	}
	return res
}

func sseObj(t *testing.T, vm *goja.Runtime, locals map[string]string) *goja.Object {
	t.Helper()
	mod := &Module{}
	export := vm.NewObject()
	mock := newFiberMock(locals)
	mod.Loader(mock, vm, export)
	
	exp := export.Get("exports")
	if exp != nil && !goja.IsUndefined(exp) {
		return exp.ToObject(vm)
	}
	return export
}

type SharedResponseMsg struct {
	Channel string `json:"channel"`
	Data    string `json:"data"`
}
