package fs

import (
	"fmt"
	"os"

	"http-server/modules"
	"http-server/processor"

	"github.com/dop251/goja"
)

type Module struct{}

func (m *Module) Name() string {
	return "fs"
}

func (m *Module) Doc() string {
	return "Standard Node.js-like fs (File System) API for JS environment"
}

func (m *Module) ToJSObject(vm *goja.Runtime) goja.Value {
	fsObj := vm.NewObject()

	// Sync Methods
	fsObj.Set("readFileSync", m.readFileSync(vm))
	fsObj.Set("writeFileSync", m.writeFileSync(vm))
	fsObj.Set("appendFileSync", m.appendFileSync(vm))
	fsObj.Set("existsSync", m.existsSync(vm))
	fsObj.Set("statSync", m.statSync(vm))
	fsObj.Set("readdirSync", m.readdirSync(vm))
	fsObj.Set("mkdirSync", m.mkdirSync(vm))
	fsObj.Set("unlinkSync", m.unlinkSync(vm))

	// Async (Promise) Methods equivalent
	fsObj.Set("readFile", m.asyncWrapper(vm, m.readFileSync(vm)))
	fsObj.Set("writeFile", m.asyncWrapper(vm, m.writeFileSync(vm)))
	fsObj.Set("appendFile", m.asyncWrapper(vm, m.appendFileSync(vm)))
	fsObj.Set("stat", m.asyncWrapper(vm, m.statSync(vm)))
	fsObj.Set("readdir", m.asyncWrapper(vm, m.readdirSync(vm)))
	fsObj.Set("mkdir", m.asyncWrapper(vm, m.mkdirSync(vm)))
	fsObj.Set("unlink", m.asyncWrapper(vm, m.unlinkSync(vm)))

	return fsObj
}

func (m *Module) Loader(_ any, vm *goja.Runtime, moduleObject *goja.Object) {
	// Expose globally into the JS namespace
	vm.Set("fs", m.ToJSObject(vm))
}

// ------ Synchronous Implementations ------

func (m *Module) readFileSync(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("path required"))
		}
		path := call.Arguments[0].String()
		data, err := os.ReadFile(path)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("ENOENT: no such file or directory, open '%s'", path)))
		}
		
		// Return string format inherently (mimics Node.js readFileSync(..., 'utf8'))
		return vm.ToValue(string(data))
	}
}

func (m *Module) writeFileSync(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.NewTypeError("path and data required"))
		}
		path := call.Arguments[0].String()
		data := call.Arguments[1].String() 
		
		err := os.WriteFile(path, []byte(data), 0644)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("EACCES: permission denied, write '%s'", path)))
		}
		return goja.Undefined()
	}
}

func (m *Module) appendFileSync(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.NewTypeError("path and data required"))
		}
		path := call.Arguments[0].String()
		data := call.Arguments[1].String()
		
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("EACCES: permission denied, append '%s'", path)))
		}
		defer f.Close()
		
		if _, err := f.WriteString(data); err != nil {
			panic(vm.NewGoError(fmt.Errorf("EIO: i/o error, append '%s'", path)))
		}
		return goja.Undefined()
	}
}

func (m *Module) existsSync(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("path required"))
		}
		path := call.Arguments[0].String()
		_, err := os.Stat(path)
		return vm.ToValue(err == nil)
	}
}

func (m *Module) statSync(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("path required"))
		}
		path := call.Arguments[0].String()
		info, err := os.Stat(path)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("ENOENT: no such file or directory, stat '%s'", path)))
		}
		
		statObj := vm.NewObject()
		statObj.Set("size", info.Size())
		statObj.Set("mtimeMs", info.ModTime().UnixMilli())
		statObj.Set("isDirectory", func() bool { return info.IsDir() })
		statObj.Set("isFile", func() bool { return !info.IsDir() })
		
		return statObj
	}
}

func (m *Module) readdirSync(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("path required"))
		}
		path := call.Arguments[0].String()
		entries, err := os.ReadDir(path)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("ENOENT: no such directory, scandir '%s'", path)))
		}
		
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return vm.ToValue(names)
	}
}

func (m *Module) mkdirSync(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("path required"))
		}
		path := call.Arguments[0].String()
		
		// Option recursive via { recursive: true }
		recursive := false
		if len(call.Arguments) > 1 {
			if opts, ok := call.Arguments[1].(*goja.Object); ok {
				if rec := opts.Get("recursive"); rec != nil && rec.Export() == true {
					recursive = true
				}
			}
		}
		
		var err error
		if recursive {
			err = os.MkdirAll(path, 0755)
		} else {
			err = os.Mkdir(path, 0755)
		}
		
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("EACCES: directory creation failed '%s': %v", path, err)))
		}
		return goja.Undefined()
	}
}

func (m *Module) unlinkSync(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("path required"))
		}
		path := call.Arguments[0].String()
		err := os.Remove(path)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("ENOENT: cannot remove file '%s': %v", path, err)))
		}
		return goja.Undefined()
	}
}

// ------ Asynchronous Wrappers (Promises) ------

func (m *Module) asyncWrapper(vm *goja.Runtime, syncFn func(call goja.FunctionCall) goja.Value) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) (ret goja.Value) {
		promise, resolve, reject := vm.NewPromise()
		ret = vm.ToValue(promise) // always return the promise, even on panic

		defer func() {
			if r := recover(); r != nil {
				reject(vm.ToValue(r))
			}
		}()

		val := syncFn(call)
		resolve(val)
		return
	}
}

func init() {
	mod := &Module{}
	processor.RegisterGlobal("fs", mod)
	modules.RegisterModule(mod)
}
