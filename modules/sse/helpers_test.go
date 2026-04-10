package sse

import (
	"context"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// newTestClient crée un client connecté au hub et retourne son channel de réception.
// Le cleanup (unsubscribe + closed) est enregistré dans t.Cleanup.
func newTestClient(t *testing.T, sid string, channels []string) chan *Message {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan *Message, 32)
	client := &Client{
		sid:      sid,
		message:  ch,
		channels: channels,
		ctx:      ctx,
		cancel:   cancel,
	}
	for _, c := range channels {
		HubInstance.Subscribe(client, c)
	}
	t.Cleanup(func() {
		cancel()
		client.closed.Store(true)
		for _, c := range channels {
			HubInstance.Unsubscribe(client, c)
		}
	})
	return ch
}

// drain vide le channel pour éviter les interférences entre sous-tests.
func drain(ch chan *Message) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// recv attend un message non-heartbeat sur ch avec timeout.
func recv(t *testing.T, ch chan *Message, label string) *Message {
	t.Helper()
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case msg := <-ch:
			if msg.Event == "heartbeat" {
				continue // ignorer les heartbeats
			}
			return msg
		case <-deadline:
			t.Fatalf("%s: timeout — aucun message reçu", label)
			return nil
		}
	}
}

// noRecv vérifie qu'aucun message non-heartbeat n'arrive dans la fenêtre.
func noRecv(t *testing.T, ch chan *Message, label string) {
	t.Helper()
	deadline := time.After(50 * time.Millisecond)
	for {
		select {
		case msg := <-ch:
			if msg.Event == "heartbeat" {
				continue
			}
			t.Errorf("%s: message inattendu reçu: event=%q data=%q", label, msg.Event, msg.Data)
			return
		case <-deadline:
			return // OK
		}
	}
}

// callJS appelle une méthode d'un objet goja par son nom.
func callJS(t *testing.T, vm *goja.Runtime, obj *goja.Object, name string, args ...any) {
	t.Helper()
	fn, ok := goja.AssertFunction(obj.Get(name))
	if !ok {
		t.Fatalf("callJS: %s n'est pas une fonction", name)
	}
	jsArgs := make([]goja.Value, len(args))
	for i, a := range args {
		jsArgs[i] = vm.ToValue(a)
	}
	if _, err := fn(goja.Undefined(), jsArgs...); err != nil {
		t.Fatalf("callJS %s: %v", name, err)
	}
}

// sseObj initialise le module SSE avec un mockCtx et retourne l'objet exports.
func sseObj(t *testing.T, vm *goja.Runtime, cookies map[string]string) *goja.Object {
	t.Helper()
	mod := &Module{}
	export := vm.NewObject()
	ctx := newFiberMock(cookies)
	mod.Loader(ctx, vm, export)
	if exp := export.Get("exports"); exp != nil && !goja.IsUndefined(exp) {
		return exp.ToObject(vm)
	}
	return export
}
