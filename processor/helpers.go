package processor

import (
	"errors"
	"http-server/plugins/js"

	"github.com/dop251/goja"
)

// processor helpers
func (p *Processor) GetFunction(code string, functionBuilder ...string) string {
	return js.GetFunction(code, functionBuilder...)
}
func (p *Processor) EnsureReturnStrict(code string) (string, bool) {
	return js.EnsureReturnStrict(code)
}
func (p *Processor) IsFunction(code string) bool {
	return js.IsFunction(code)
}

func (p *Processor) ToJSON(v goja.Value) (string, error) {
	return js.ToJSON(p.Runtime, v)
}

// GLOBALS JS helpers
func GetFunction(code string, functionBuilder ...string) string {
	return js.GetFunction(code, functionBuilder...)
}
func EnsureReturnStrict(code string) (string, bool) {
	return js.EnsureReturnStrict(code)
}
func IsFunction(code string) bool {
	return js.IsFunction(code)
}

func ToJSON(runtime any, v goja.Value) (string, error) {
	if vm, ok := runtime.(*goja.Runtime); ok {
		return js.ToJSON(vm, v)
	} else if vm, ok := runtime.(goja.Runtime); ok {
		return js.ToJSON((&vm), v)
	} else if vm, ok := runtime.(*Processor); ok {
		return js.ToJSON(vm.Runtime, v)
	} else if vm, ok := runtime.(Processor); ok {
		return js.ToJSON((&vm).Runtime, v)
	}
	return "", errors.New("invalid runtime type")
}
