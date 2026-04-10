package path

import (
	"path/filepath"
	"strings"

	"http-server/modules"
	"http-server/processor"

	"github.com/dop251/goja"
)

type Module struct{}

func (m *Module) Name() string {
	return "path"
}

func (m *Module) Doc() string {
	return "Node.js-compatible path manipulation module"
}

func (m *Module) ToJSObject(vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()

	obj.Set("join", func(call goja.FunctionCall) goja.Value {
		var parts []string
		for _, arg := range call.Arguments {
			parts = append(parts, arg.String())
		}
		return vm.ToValue(filepath.Join(parts...))
	})

	obj.Set("resolve", func(call goja.FunctionCall) goja.Value {
		var parts []string
		for _, arg := range call.Arguments {
			parts = append(parts, arg.String())
		}
		result := filepath.Join(parts...)
		abs, err := filepath.Abs(result)
		if err != nil {
			return vm.ToValue(result)
		}
		return vm.ToValue(abs)
	})

	obj.Set("basename", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		p := call.Arguments[0].String()
		base := filepath.Base(p)
		// Optional second arg: extension to strip
		if len(call.Arguments) > 1 {
			ext := call.Arguments[1].String()
			base = strings.TrimSuffix(base, ext)
		}
		return vm.ToValue(base)
	})

	obj.Set("dirname", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(".")
		}
		return vm.ToValue(filepath.Dir(call.Arguments[0].String()))
	})

	obj.Set("extname", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		return vm.ToValue(filepath.Ext(call.Arguments[0].String()))
	})

	obj.Set("isAbsolute", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(false)
		}
		return vm.ToValue(filepath.IsAbs(call.Arguments[0].String()))
	})

	obj.Set("normalize", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(".")
		}
		return vm.ToValue(filepath.Clean(call.Arguments[0].String()))
	})

	obj.Set("relative", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue("")
		}
		rel, err := filepath.Rel(call.Arguments[0].String(), call.Arguments[1].String())
		if err != nil {
			return vm.ToValue("")
		}
		return vm.ToValue(rel)
	})

	obj.Set("parse", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.NewObject()
		}
		p := call.Arguments[0].String()
		parsed := vm.NewObject()
		parsed.Set("root", filepath.VolumeName(p)+string(filepath.Separator))
		parsed.Set("dir", filepath.Dir(p))
		parsed.Set("base", filepath.Base(p))
		parsed.Set("ext", filepath.Ext(p))
		base := filepath.Base(p)
		ext := filepath.Ext(p)
		if ext != "" {
			parsed.Set("name", base[:len(base)-len(ext)])
		} else {
			parsed.Set("name", base)
		}
		return parsed
	})

	obj.Set("sep", string(filepath.Separator))

	return obj
}

func (m *Module) Loader(_ any, vm *goja.Runtime, moduleObject *goja.Object) {
	vm.Set("path", m.ToJSObject(vm))
}

func init() {
	mod := &Module{}
	processor.RegisterGlobal("path", mod)
	modules.RegisterModule(mod)
}
