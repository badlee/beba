package require

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"text/template"

	js "github.com/dop251/goja"
	"github.com/dop251/goja/parser"
)

type ModuleLoader[T any] func(T, *js.Runtime, *js.Object)

// SourceLoader represents a function that returns a file data at a given path.
// The function should return ModuleFileDoesNotExistError if the file either doesn't exist or is a directory.
// This error will be ignored by the resolver and the search will continue. Any other errors will be propagated.
type SourceLoader func(path string) ([]byte, error)

// PathResolver is a function that should return a canonical path of the path parameter relative to the base. The base
// is expected to be already canonical as it would be a result of a previous call to the PathResolver for all cases
// except for the initial evaluation, but it's a responsibility of the caller to ensure that the name of the script
// is a canonical path. To match Node JS behaviour, it should resolve symlinks.
// The path parameter is the argument of the require() call. The returned value will be supplied to the SourceLoader.
type PathResolver func(base, path string) string

var (
	InvalidModuleError          = errors.New("Invalid module")
	IllegalModuleNameError      = errors.New("Illegal module name")
	NoSuchBuiltInModuleError    = errors.New("No such built-in module")
	ModuleFileDoesNotExistError = errors.New("module file does not exist")
)

var native, builtin map[string]any

// Registry contains a cache of compiled modules which can be used by multiple Runtimes
type Registry[T any] struct {
	sync.Mutex
	instance T
	native   map[string]ModuleLoader[T]
	compiled map[string]*js.Program

	srcLoader     SourceLoader
	pathResolver  PathResolver
	globalFolders []string
}

type RequireModule[T any] struct {
	r           *Registry[T]
	runtime     *js.Runtime
	modules     map[string]*js.Object
	nodeModules map[string]*js.Object
}

func NewRegistry[T any](opts ...Option[T]) *Registry[T] {
	r := &Registry[T]{}

	for _, opt := range opts {
		opt(r)
	}
	fmt.Printf("SET INSTANCE %v\n\n\n", r.instance)

	return r
}

func NewRegistryWithLoader[T any](instance T, srcLoader SourceLoader) *Registry[T] {
	return NewRegistry(WithInstance(instance), WithLoader[T](srcLoader))
}

type Option[T any] func(*Registry[T])

// WithLoader sets a function which will be called by the require() function in order to get a source code for a
// module at the given path. The same function will be used to get external source maps.
// Note, this only affects the modules loaded by the require() function. If you need to use it as a source map
// loader for code parsed in a different way (such as runtime.RunString() or eval()), use (*Runtime).SetParserOptions()
func WithLoader[T any](srcLoader SourceLoader) Option[T] {
	return func(r *Registry[T]) {
		r.srcLoader = srcLoader
	}
}

// WithInstance sets the instance to be used by the registry.
func WithInstance[T any](instance T) Option[T] {
	return func(r *Registry[T]) {
		r.instance = instance
	}
}

// WithPathResolver sets a function which will be used to resolve paths (see PathResolver). If not specified, the
// DefaultPathResolver is used.
func WithPathResolver[T any](pathResolver PathResolver) Option[T] {
	return func(r *Registry[T]) {
		r.pathResolver = pathResolver
	}
}

// WithGlobalFolders appends the given paths to the registry's list of
// global folders to search if the requested module is not found
// elsewhere.  By default, a registry's global folders list is empty.
// In the reference Node.js implementation, the default global folders
// list is $NODE_PATH, $HOME/.node_modules, $HOME/.node_libraries and
// $PREFIX/lib/node, see
// https://nodejs.org/api/modules.html#modules_loading_from_the_global_folders.
func WithGlobalFolders[T any](globalFolders ...string) Option[T] {
	return func(r *Registry[T]) {
		r.globalFolders = globalFolders
	}
}

// Enable adds the require() function to the specified runtime.
func (r *Registry[T]) Enable(runtime *js.Runtime) *RequireModule[T] {
	rrt := &RequireModule[T]{
		r:           r,
		runtime:     runtime,
		modules:     make(map[string]*js.Object),
		nodeModules: make(map[string]*js.Object),
	}

	runtime.Set("require", rrt.require)
	return rrt
}

func (r *Registry[T]) RegisterNativeModule(name string, loader ModuleLoader[T]) {
	r.Lock()
	defer r.Unlock()

	if r.native == nil {
		r.native = make(map[string]ModuleLoader[T])
	}
	name = filepathClean(name)
	r.native[name] = loader
}

// DefaultSourceLoader is used if none was set (see WithLoader()). It simply loads files from the host's filesystem.
func DefaultSourceLoader(filename string) ([]byte, error) {
	f, err := os.Open(filename)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			err = ModuleFileDoesNotExistError
		} else if runtime.GOOS == "windows" {
			if errors.Is(err, syscall.Errno(0x7b)) { // ERROR_INVALID_NAME, The filename, directory name, or volume label syntax is incorrect.
				err = ModuleFileDoesNotExistError
			}
		}
		return nil, err
	}

	defer f.Close()
	// On some systems (e.g. plan9 and FreeBSD) it is possible to use the standard read() call on directories
	// which means we cannot rely on read() returning an error, we have to do stat() instead.
	if fi, err := f.Stat(); err == nil {
		if fi.IsDir() {
			return nil, ModuleFileDoesNotExistError
		}
	} else {
		return nil, err
	}
	return io.ReadAll(f)
}

// DefaultPathResolver is used if none was set (see WithPathResolver). It converts the path using filepath.FromSlash(),
// then joins it with base and resolves symlinks on the resulting path.
// Note, it does not make the path absolute, so to match nodejs behaviour, the initial script name should be set
// to an absolute path.
// The implementation is somewhat suboptimal because it runs filepath.EvalSymlinks() on the joint path, not using the
// fact that the base path is already resolved. This is because there is no way to resolve symlinks only in a portion
// of a path without re-implementing a significant part of filepath.FromSlash().
func DefaultPathResolver(base, path string) string {
	p := filepath.Join(base, filepath.FromSlash(path))
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return p
}

func (r *Registry[T]) getSource(p string) ([]byte, error) {
	srcLoader := r.srcLoader
	if srcLoader == nil {
		srcLoader = DefaultSourceLoader
	}
	return srcLoader(p)
}

func (r *Registry[T]) getCompiledSource(p string) (*js.Program, error) {
	r.Lock()
	defer r.Unlock()

	prg := r.compiled[p]
	if prg == nil {
		buf, err := r.getSource(p)
		if err != nil {
			return nil, err
		}
		s := string(buf)

		if filepath.Ext(p) == ".json" {
			s = "module.exports = JSON.parse('" + template.JSEscapeString(s) + "')"
		}

		source := "(function(exports,require,module,__filename,__dirname){" + s + "\n})"
		parsed, err := js.Parse(p, source, parser.WithSourceMapLoader(r.srcLoader))
		if err != nil {
			return nil, err
		}
		prg, err = js.CompileAST(parsed, false)
		if err == nil {
			if r.compiled == nil {
				r.compiled = make(map[string]*js.Program)
			}
			r.compiled[p] = prg
		}
		return prg, err
	}
	return prg, nil
}

func (r *RequireModule[T]) require(call js.FunctionCall) js.Value {
	ret, err := r.Require(call.Argument(0).String())
	if err != nil {
		if _, ok := err.(*js.Exception); !ok {
			panic(r.runtime.NewGoError(err))
		}
		panic(err)
	}
	return ret
}

func filepathClean(p string) string {
	return path.Clean(p)
}

// Require can be used to import modules from Go source (similar to JS require() function).
func (r *RequireModule[T]) Require(p string) (ret js.Value, err error) {
	module, err := r.resolve(p)
	if err != nil {
		return
	}
	ret = module.Get("exports")
	return
}

func Require[T any](runtime *js.Runtime, name string) js.Value {
	if r, ok := js.AssertFunction(runtime.Get("require")); ok {
		mod, err := r(js.Undefined(), runtime.ToValue(name))
		if err != nil {
			panic(err)
		}
		return mod
	}
	panic(runtime.NewTypeError("Please enable require for this runtime using new(require.Registry).Enable(runtime)"))
}

// RegisterNativeModule registers a module that isn't loaded through a SourceLoader, but rather through
// a provided ModuleLoader. Typically, this will be a module implemented in Go (although theoretically
// it can be anything, depending on the ModuleLoader implementation).
// Such modules take precedence over modules loaded through a SourceLoader, i.e. if a module name resolves as
// native, the native module is loaded, and the SourceLoader is not consulted.
// The binding is global and affects all instances of Registry.
// It should be called from a package init() function as it may not be used concurrently with require() calls.
// For registry-specific bindings see Registry.RegisterNativeModule.
func RegisterNativeModule[T any](name string, loader ModuleLoader[T]) {
	if native == nil {
		native = make(map[string]any)
	}
	name = filepathClean(name)
	native[name] = loader
}

// RegisterCoreModule registers a nodejs core module. If the name does not start with "node:", the module
// will also be loadable as "node:<name>". Hence, for "builtin" modules (such as buffer, console, etc.)
// the name should not include the "node:" prefix, but for prefix-only core modules (such as "node:test")
// it should include the prefix.
func RegisterCoreModule[T any](name string, loader ModuleLoader[T]) {
	if builtin == nil {
		builtin = make(map[string]any)
	}
	name = filepathClean(name)
	builtin[name] = loader
}
