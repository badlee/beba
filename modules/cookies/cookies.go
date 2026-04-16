package cookies

import (
	"http-server/modules"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
)

type Module struct{}

type FiberCookiesCtx interface {
	Cookies(key string, defaultValue ...string) string
	Cookie(cookie *fiber.Cookie) *fiber.Cookie
	ClearCookie(key string, path ...string)
}

type FakeCookiesCtx struct {
}

func (f *FakeCookiesCtx) Cookies(key string, defaultValue ...string) string {
	return ""
}

func (f *FakeCookiesCtx) Cookie(cookie *fiber.Cookie) *fiber.Cookie {
	return cookie
}

func (f *FakeCookiesCtx) ClearCookie(key string, path ...string) {
}

func (s *Module) Name() string {
	return "cookies"
}

func (s *Module) Doc() string {
	return "Cookies module"
}

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

	// Expose cookies
	cookies := module
	ctx, ok := c.(FiberCookiesCtx)
	if !ok {
		ctx = &FakeCookiesCtx{}
	}
	cookies.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		return vm.ToValue(ctx.Cookies(key))
	})
	cookies.Set("set", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).String()
		// Basic cookie set, could be extended with config
		ctx.Cookie(&fiber.Cookie{
			Name:  key,
			Value: val,
		})
		return goja.Undefined()
	})
	cookies.Set("remove", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		ctx.ClearCookie(key)
		return goja.Undefined()
	})
	cookies.Set("has", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		return vm.ToValue(ctx.Cookies(key) != "")
	})
}

func init() {
	modules.RegisterModule(&Module{})
}
