package modules

import (
	"sort"
	"strings"

	"http-server/plugins/require"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
)

type Module interface {
	Name() string
	Doc() string
	Loader(fiber.Ctx, *goja.Runtime, *goja.Object)
}

var modules = make(map[string]Module)

func RegisterModule(module Module) {
	modules[module.Name()] = module
}

func GetModule(name string) (Module, bool) {
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

func Register(registry *require.Registry[fiber.Ctx]) {
	for _, module := range modules {
		registry.RegisterNativeModule(strings.ToLower(strings.TrimSpace(module.Name())), module.Loader)
	}
}
