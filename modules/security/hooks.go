package security

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

	"http-server/plugins/httpserver"

	"github.com/dop251/goja"
)

// ErrReject is the sentinel error returned when JS calls reject().
type ErrReject struct {
	Reason string
}

func (e *ErrReject) Error() string { return e.Reason }

// ConnContext holds the per-connection data exposed as the `CONN` object in JS.
type ConnContext struct {
	IP          string
	Port        string
	CurrentRate int64
	Total       int64
}

// RunIPHook executes a CONNECTION IP hook (file or inline).
// Returns (allow, err). err is non-nil if evaluation itself fails, allow=false on reject().
func RunIPHook(hook httpserver.WAFHook, conn net.Conn, rate int64, total int64) (bool, error) {
	ipStr, portStr, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		ipStr = conn.RemoteAddr().String()
		portStr = ""
	}

	connCtx := &ConnContext{
		IP:          ipStr,
		Port:        portStr,
		CurrentRate: rate,
		Total:       total,
	}

	return runHookWithVM(hook, func(vm *goja.Runtime) {
		conn := vm.NewObject()
		conn.Set("ip", connCtx.IP)
		conn.Set("port", connCtx.Port)
		conn.Set("current_rate", connCtx.CurrentRate)
		conn.Set("total_connections", connCtx.Total)
		vm.Set("CONN", conn)
	})
}

// RunGeoHook executes a CONNECTION GEO hook (file or inline).
func RunGeoHook(hook httpserver.WAFHook, conn net.Conn, geoCtx *GeoContext) (bool, error) {
	return runHookWithVM(hook, func(vm *goja.Runtime) {
		if geoCtx == nil {
			geoCtx = &GeoContext{}
		}
		geo := vm.NewObject()
		geo.Set("country", geoCtx.Country)
		geo.Set("city", geoCtx.City)
		geo.Set("latitude", geoCtx.Latitude)
		geo.Set("longitude", geoCtx.Longitude)
		geo.Set("asn", geoCtx.ASN)
		geo.Set("isp", geoCtx.ISP)
		vm.Set("GEO", geo)
	})
}

// runHookWithVM builds a goja VM, calls setup, wires allow/reject/log, runs the code.
// Returns (true, nil) for allow, (false, nil) for reject, (false, err) on hard error.
func runHookWithVM(hook httpserver.WAFHook, setup func(vm *goja.Runtime)) (bool, error) {
	vm := goja.New()

	// Signal channels via panic (same pattern goja uses internally)
	type allowSignal struct{}
	type rejectSignal struct{ reason string }

	vm.Set("allow", func(call goja.FunctionCall) goja.Value {
		panic(allowSignal{})
	})

	vm.Set("reject", func(call goja.FunctionCall) goja.Value {
		reason := "rejected"
		if len(call.Arguments) > 0 {
			reason = call.Arguments[0].String()
		}
		panic(rejectSignal{reason: reason})
	})

	vm.Set("log", func(call goja.FunctionCall) goja.Value {
		parts := make([]interface{}, len(call.Arguments))
		for i, a := range call.Arguments {
			parts[i] = a.Export()
		}
		log.Println(parts...)
		return goja.Undefined()
	})

	// Inject named args (e.g., [whitelist=127.0.0.1])
	if len(hook.Args) > 0 {
		args := vm.NewObject()
		for k, v := range hook.Args {
			args.Set(k, v)
		}
		vm.Set("args", args)
	}

	// Call the user-provided setup to inject CONN/GEO
	setup(vm)

	// Determine code to run
	var code string
	if hook.Inline {
		code = hook.Handler
	} else if hook.Handler != "" {
		src, err := os.ReadFile(filepath.Clean(hook.Handler))
		if err != nil {
			return false, fmt.Errorf("security hook: cannot read %s: %w", hook.Handler, err)
		}
		code = string(src)
	} else {
		// No hook body — allow by default
		return true, nil
	}

	// Run with signal recovery
	allowed := true // default: allow (if code falls through without calling allow/reject)
	var runErr error

	func() {
		defer func() {
			if r := recover(); r != nil {
				switch sig := r.(type) {
				case allowSignal:
					allowed = true
				case rejectSignal:
					allowed = false
					runErr = &ErrReject{Reason: sig.reason}
				default:
					allowed = false
					runErr = fmt.Errorf("security hook panic: %v", r)
				}
			}
		}()
		_, runErr = vm.RunString(code)
	}()

	if runErr != nil {
		if _, isReject := runErr.(*ErrReject); isReject {
			return false, runErr // controlled rejection
		}
		return false, runErr // hard error
	}

	return allowed, nil
}
