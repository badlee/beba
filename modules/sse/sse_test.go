package sse

import (
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
)

type mockCtx struct {
	fiber.Ctx
	locals  map[string]interface{}
	cookies map[string]string
}

func (m *mockCtx) Locals(key any, value ...any) any {
	if len(value) > 0 {
		m.locals[key.(string)] = value[0]
	}
	return m.locals[key.(string)]
}

func (m *mockCtx) Cookies(name string, defaultValue ...string) string {
	if val, ok := m.cookies[name]; ok {
		return val
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

func TestSSEIsolation(t *testing.T) {
	manager := NewManager()
	vm := goja.New()

	mod := &Module{}
	export := vm.NewObject()

	// Setup session 1
	ctx1 := &mockCtx{locals: map[string]interface{}{"sseManager": manager}, cookies: map[string]string{"sid": "session1"}}
	mod.Loader(ctx1, vm, export)
	sse1 := export.Get("exports").(*goja.Object)

	ch1 := manager.AddClient([]string{"global", "sid:session1"})
	ch2 := manager.AddClient([]string{"global", "sid:session2"})

	// Helper to call JS function
	callJS := func(obj *goja.Object, name string, args ...any) {
		val := obj.Get(name)
		fn, ok := goja.AssertFunction(val)
		if !ok {
			t.Fatalf("Method %s is not a function", name)
		}
		jsArgs := make([]goja.Value, len(args))
		for i, a := range args {
			jsArgs[i] = vm.ToValue(a)
		}
		_, err := fn(goja.Undefined(), jsArgs...)
		if err != nil {
			t.Fatalf("Error calling %s: %v", name, err)
		}
	}

	// Helper for assertions
	assertMsg := func(ch chan Message, expectedData string, name string) {
		select {
		case msg := <-ch:
			if msg.Data != expectedData {
				t.Errorf("%s: expected %s, got %s", name, expectedData, msg.Data)
			}
		case <-time.After(1 * time.Second): // Wait for 500ms delay + buffer
			t.Errorf("%s: timeout waiting for message", name)
		}
	}

	assertNoMsg := func(ch chan Message, name string) {
		select {
		case msg := <-ch:
			t.Errorf("%s: unexpected message: %v", name, msg)
		case <-time.After(50 * time.Millisecond):
			// OK
		}
	}

	// 1. Queue a global message
	callJS(sse1, "publish", "event", "global_msg")

	// Verify it's NOT sent yet
	assertNoMsg(ch1, "ch1 before flush")

	// 2. Flush
	Flush(ctx1)

	// Wait for the 500ms delay in Flush's goroutine
	assertMsg(ch1, "global_msg", "ch1 after flush")
	assertMsg(ch2, "global_msg", "ch2 after flush")

	// 3. Test attach and send
	ctx1.Locals("sse_queue", nil) // reset queue
	callJS(sse1, "attach", "custom_session")
	callJS(sse1, "send", "event", "private_msg")

	// We need a client for custom_session
	chC := manager.AddClient([]string{"sid:custom_session"})

	Flush(ctx1)
	assertMsg(chC, "private_msg", "chC private")
	assertNoMsg(ch1, "ch1 private isolation")
}
