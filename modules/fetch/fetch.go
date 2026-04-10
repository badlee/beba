package fetch

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"http-server/modules"
	"http-server/processor"

	"github.com/dop251/goja"
)

type Module struct {
	client *http.Client
}

func (m *Module) Name() string {
	return "fetch"
}

func (m *Module) Doc() string {
	return "Standard Web fetch() API for HTTP(s) and local file resources"
}

func (m *Module) ToJSObject(vm *goja.Runtime) goja.Value {
	if m.client == nil {
		m.client = &http.Client{Timeout: 30 * time.Second}
	}
	return vm.ToValue(m.fetchFn(vm))
}

func (m *Module) Loader(_ any, vm *goja.Runtime, moduleObject *goja.Object) {
	// Also explicitly set fetch into the VM in case loader logic targets exports instead
	vm.Set("fetch", m.ToJSObject(vm))
}

func (m *Module) fetchFn(vm *goja.Runtime) func(call goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		promise, resolve, reject := vm.NewPromise()

		if len(call.Arguments) == 0 {
			reject(vm.ToValue(vm.NewTypeError("fetch requires at least 1 argument (url)")))
			return vm.ToValue(promise)
		}

		targetURL := call.Arguments[0].String()
		u, err := url.Parse(targetURL)
		if err != nil {
			reject(vm.ToValue(vm.NewTypeError("Failed to parse URL: " + err.Error())))
			return vm.ToValue(promise)
		}

		method := "GET"
		var bodyReader io.Reader
		headers := make(http.Header)

		// Parse second param options if provided
		if len(call.Arguments) > 1 {
			opts := call.Arguments[1]
			if optsObj, ok := opts.(*goja.Object); ok {
				if mVal := optsObj.Get("method"); mVal != nil && !goja.IsUndefined(mVal) {
					method = strings.ToUpper(mVal.String())
				}
				if hVal := optsObj.Get("headers"); hVal != nil && !goja.IsUndefined(hVal) {
					if hObj, ok := hVal.(*goja.Object); ok {
						for _, k := range hObj.Keys() {
							headers.Set(k, hObj.Get(k).String())
						}
					}
				}
				if bVal := optsObj.Get("body"); bVal != nil && !goja.IsUndefined(bVal) {
					// We take basic string exports, ignoring sophisticated streaming chunks in this MVP
					bodyReader = strings.NewReader(bVal.String())
				}
			}
		}

		// Javascript execution under Proxy Handlers runs inside separate thread Goroutines but often lacks
		// the heavy native Node.js EventLoop required for seamless async background suspension, so we run the
		// network IO synchronously but bind it strictly inside JS Promise syntaxes for full JS thread continuity:
		
		isFetchRemote := strings.HasPrefix(targetURL, "http://") || strings.HasPrefix(targetURL, "https://")
		if !isFetchRemote {
			// Restricted to file processing natively
			filePath := u.Path
			if filePath == "" && targetURL != "" && !strings.HasPrefix(targetURL, "file://") {
				filePath = targetURL
			}

			data, err := os.ReadFile(filePath)
			if err != nil {
				reject(vm.ToValue(fmt.Errorf("NetworkError: Failed to read local file: %v", err)))
				return vm.ToValue(promise)
			}

			respObj := m.createResponse(vm, 200, "OK", data, make(http.Header))
			resolve(respObj)
			return vm.ToValue(promise)
		}

		// Remote Network Fetch
		req, err := http.NewRequest(method, targetURL, bodyReader)
		if err != nil {
			reject(vm.ToValue(vm.NewTypeError("RequestError: " + err.Error())))
			return vm.ToValue(promise)
		}
		req.Header = headers

		resp, err := m.client.Do(req)
		if err != nil {
			reject(vm.ToValue(fmt.Errorf("NetworkError: %v", err)))
			return vm.ToValue(promise)
		}
		defer resp.Body.Close()

		bodyData, err := io.ReadAll(resp.Body)
		if err != nil {
			reject(vm.ToValue(fmt.Errorf("TypeError: Failed to buffer response body: %v", err)))
			return vm.ToValue(promise)
		}

		respObj := m.createResponse(vm, resp.StatusCode, resp.Status, bodyData, resp.Header)
		resolve(respObj)

		return vm.ToValue(promise)
	}
}

func (m *Module) createResponse(vm *goja.Runtime, status int, statusText string, body []byte, headers http.Header) goja.Value {
	resp := vm.NewObject()
	resp.Set("status", status)
	resp.Set("statusText", statusText)
	resp.Set("ok", status >= 200 && status < 300)

	headersObj := vm.NewObject()
	for k, valMap := range headers {
		if len(valMap) > 0 {
			headersObj.Set(k, valMap[0])
		}
	}
	resp.Set("headers", headersObj)

	// Implementation of standard .text() returning a Promise
	resp.Set("text", func(call goja.FunctionCall) goja.Value {
		p, res, _ := vm.NewPromise()
		res(string(body))
		return vm.ToValue(p)
	})

	// Implementation of standard .json() returning a Promise
	resp.Set("json", func(call goja.FunctionCall) goja.Value {
		p, res, rej := vm.NewPromise()
		var v interface{}
		err := json.Unmarshal(body, &v)
		if err != nil {
			rej(vm.ToValue(vm.NewTypeError("SyntaxError: Failed to parse JSON body: " + err.Error())))
		} else {
			res(vm.ToValue(v))
		}
		return vm.ToValue(p)
	})

	return resp
}

func init() {
	mod := &Module{
		client: &http.Client{Timeout: 30 * time.Second},
	}
	processor.RegisterGlobal("fetch", mod)
	modules.RegisterModule(mod)
}
