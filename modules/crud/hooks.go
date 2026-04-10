package crud

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dop251/goja"
	"http-server/processor"
)

// hookCtx is the context passed to every hook script.
type hookCtx struct {
	// Raw JS values injected into the VM
	user      interface{} // *User or nil
	namespace interface{} // *Namespace
	schema    interface{} // *CrudSchema or nil
	doc       interface{} // map[string]any or nil  (single doc)
	docs      interface{} // []map[string]any or nil (list)
	prev      interface{} // map[string]any or nil  (before-update snapshot)
	args      map[string]string
	baseDir   string
}

// hookResult is returned by runHook.
type hookResult struct {
	rejected bool
	message  string
	// modified doc (set when modify() is called from a onRead/onUpdate hook)
	modifiedDoc  map[string]any
	// modified docs (set when modify() is called from a onList hook)
	modifiedDocs []map[string]any
}

// runHook executes one hook (inline code or file path) with the given context.
// Returns a hookResult; if rejected is true, the operation must be aborted.
func runHook(code string, isFile bool, ctx hookCtx) (hookResult, error) {
	if code == "" {
		return hookResult{}, nil
	}

	vm := processor.New(ctx.baseDir, nil, nil)

	// ── Inject variables ───────────────────────────────────────────────────
	vm.Set("user",      ctx.user)
	vm.Set("namespace", ctx.namespace)
	vm.Set("schema",    ctx.schema)

	if ctx.doc != nil {
		vm.Set("doc", vm.ToValue(ctx.doc))
	} else {
		vm.Set("doc", goja.Null())
	}
	if ctx.docs != nil {
		vm.Set("docs", vm.ToValue(ctx.docs))
	} else {
		vm.Set("docs", goja.Null())
	}
	if ctx.prev != nil {
		vm.Set("prev", vm.ToValue(ctx.prev))
	} else {
		vm.Set("prev", goja.Null())
	}

	argsObj := vm.NewObject()
	for k, v := range ctx.args {
		argsObj.Set(k, v)
	}
	vm.Set("args", argsObj)

	// ── Control functions ──────────────────────────────────────────────────
	var res hookResult

	vm.Set("reject", func(msg string) {
		res.rejected = true
		res.message  = msg
	})

	// modify(doc) for single-doc hooks (onRead, onUpdate)
	vm.Set("modify", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		exported := call.Arguments[0].Export()
		switch v := exported.(type) {
		case map[string]interface{}:
			res.modifiedDoc = v
		case []interface{}:
			res.modifiedDocs = make([]map[string]any, 0, len(v))
			for _, item := range v {
				if m, ok := item.(map[string]interface{}); ok {
					res.modifiedDocs = append(res.modifiedDocs, m)
				}
			}
		}
		return goja.Undefined()
	})

	// ── Resolve and run code ───────────────────────────────────────────────
	src := code
	if isFile {
		full := code
		if !filepath.IsAbs(full) {
			full = filepath.Join(ctx.baseDir, full)
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return hookResult{}, fmt.Errorf("hook: cannot read %q: %w", full, err)
		}
		src = string(b)
	}

	if _, err := vm.RunString(src); err != nil {
		return hookResult{}, fmt.Errorf("hook script error: %w", err)
	}

	return res, nil
}

// runHookSet runs the hook identified by action from a HookSet.
// It returns (rejected, message, error).
func runHookSet(hs *HookSet, action string, ctx hookCtx) (hookResult, error) {
	if hs == nil {
		return hookResult{}, nil
	}
	var code string
	var isFile bool
	switch action {
	case "onList":
		code, isFile = hs.OnList, hs.OnListFile
	case "onRead":
		code, isFile = hs.OnRead, hs.OnReadFile
	case "onCreate":
		code, isFile = hs.OnCreate, hs.OnCreateFile
	case "onUpdate":
		code, isFile = hs.OnUpdate, hs.OnUpdateFile
	case "onDelete":
		code, isFile = hs.OnDelete, hs.OnDeleteFile
	case "onListTrash":
		code, isFile = hs.OnListTrash, hs.OnListTrashFile
	case "onReadTrash":
		code, isFile = hs.OnReadTrash, hs.OnReadTrashFile
	case "onDeleteTrash":
		code, isFile = hs.OnDeleteTrash, hs.OnDeleteTrashFile
	}
	if code == "" {
		return hookResult{}, nil
	}
	return runHook(code, isFile, ctx)
}

// errRejected wraps a hook rejection as a typed error.
type errRejected struct{ msg string }

func (e *errRejected) Error() string { return e.msg }

func rejectErr(msg string) error { return &errRejected{msg: msg} }

func isRejection(err error) bool {
	_, ok := err.(*errRejected)
	return ok
}
