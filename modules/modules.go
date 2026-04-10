package modules

import (
	"reflect"
	"sort"
	"strings"

	"http-server/plugins/require"

	"github.com/dop251/goja"
)

type Module[T any] interface {
	Name() string
	Doc() string
	Loader(value T, vm *goja.Runtime, global *goja.Object)
}

var modules = make(map[string]Module[any])

func RegisterModule(module Module[any]) {
	modules[module.Name()] = module
}

func GetModule(name string) (Module[any], bool) {
	module, ok := modules[name]
	return module, ok
}

func ListModules() []string {
	var names []string
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func HasModule(name string) bool {
	_, ok := modules[name]
	return ok
}

func Register(registry *require.Registry) {
	for _, module := range modules {
		registry.RegisterNativeModule(strings.ToLower(strings.TrimSpace(module.Name())), func(t any, r *goja.Runtime, o *goja.Object) {
			module.Loader(t, r, o)
		})
	}
}

func is(i any) func(v any) bool {
	return func(v any) bool {
		if v == nil {
			return i == nil
		}
		if i == nil {
			return v == nil
		}
		return reflect.TypeOf(v) == reflect.TypeOf(i)
	}
}
