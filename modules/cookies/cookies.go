package cookies

import (
	"http-server/modules"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
)

type Module struct{}

func init() {
	modules.RegisterModule(&Module{})

}

func (s *Module) Name() string {
	return "cookies"
}

func (s *Module) Doc() string {
	return "Cookies module"
}

func (s *Module) Loader(ctx fiber.Ctx, vm *goja.Runtime, module *goja.Object) {
	// Expose cookies
	cookies := vm.NewObject()
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
	module.Set("exports", cookies)
}
