package processor

import (
	"encoding/json"
	"fmt"

	"os"
	"path/filepath"
	"regexp"
	"strings"

	"beba/modules"
	"beba/plugins/config"
	"beba/plugins/require"

	"github.com/PuerkitoBio/goquery"
	"github.com/cbroglie/mustache"
	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	coreRequire "github.com/dop251/goja_nodejs/require"
	"github.com/gofiber/fiber/v3"
)

var globalObject map[string]require.ModuleLoader
var vmConfig *config.AppConfig = &config.AppConfig{}
var vmDir string = "/tmp"

func SetVMConfig(cfg *config.AppConfig) {
	vmConfig = cfg
}

func SetVMDir(dir string) {
	vmDir = dir
}

type Processor struct {
	*goja.Runtime
	initialized bool
}

func NewEmpty() *Processor {
	return New("", nil, &config.AppConfig{
		NoHtmx: true, // disable HTMX injection to keep test output predictable

	})
}

func NewVM(ctx ...fiber.Ctx) *Processor {
	var c fiber.Ctx
	if len(ctx) > 0 {
		c = ctx[0]
	}
	return New(vmDir, c, vmConfig)
}

func New(dir string, c fiber.Ctx, appCfg ...*config.AppConfig) *Processor {
	var registry *require.Registry
	vm := &Processor{goja.New(), false}
	opts := []require.Option{}
	var cfg *config.AppConfig
	if len(appCfg) > 0 && appCfg[0] != nil {
		cfg = appCfg[0]
	} else {
		cfg = vmConfig
	}
	if c != nil {
		opts = append(opts, require.WithInstance(c))
	}
	if dir != "" {
		opts = append(opts, []require.Option{
			require.WithLoader(func(path string) ([]byte, error) {
				return os.ReadFile(filepath.Join(dir, path))
			}),
			require.WithGlobalFolders("libs", "modules", "js_modules", "node_modules"),
			require.WithPathResolver(func(base, path string) string {
				return filepath.Join(base, path)
			}),
		}...) // this can be shared by multiple runtimes
	}
	registry = require.NewRegistry(opts...) // this can be shared by multiple runtimes

	new(coreRequire.Registry).Enable(vm.Runtime)
	buffer.Enable(vm.Runtime)
	registry.Enable(vm.Runtime)

	modules.Register(registry) // attache all modules
	vm.AttachGlobals()

	// Capture print output if needed?
	// For parity with some PHP-like envs, print output goes to output.
	// We can implement a rudimentary output buffer.
	var outputBuffer strings.Builder
	vm.Set("print", func(call goja.FunctionCall) goja.Value {
		out := ""
		for i, arg := range call.Arguments {
			if i > 0 {
				out += " "
			}
			out += fmt.Sprint(arg.Export())
		}
		outputBuffer.WriteString(out)
		outputBuffer.WriteString("\n")
		// Also print to stdout for debugging/logs if not a standard fiber request
		// (standard fiber requests use outputBuffer for template injection)
		println(out)
		return goja.Undefined()
	})
	vm.Set("__output", func() string {
		return outputBuffer.String()
	})
	if c != nil {
		vm.Set("context", c) // Expose context

		// Expose Locals to JS
		localsObj := vm.NewObject()
		vm.Set("Locals", localsObj)
		// For Mustache compatibility and ease of use, we can't easily iterate all locals in Fiber v3
		// but we can proxy them if we use a helper.
		// For now, let's manually expose known important ones and provide a getter.
		vm.Set("getLocals", func(key string) any {
			return c.Locals(key)
		})

		// Exposer les variables de routage FsRouter
		if params, ok := c.Locals("_fsrouter_params").(map[string]string); ok {
			paramsObj := vm.NewObject()
			for k, v := range params {
				paramsObj.Set(k, v)
			}
			vm.Set("params", paramsObj)
		}
		if catchall, ok := c.Locals("_fsrouter_catchall").(string); ok {
			vm.Set("catchall", catchall)
		}

		// Map specific common locals for templates (content, errorCode, errorMessage)
		for _, key := range []string{"content", "errorCode", "errorMessage"} {
			if val := c.Locals(key); val != nil {
				vm.Set(key, val)
				localsObj.Set(key, val) // Also set in Locals object
			}
		}

		// Allow JS to throw specific fiber errors that the Binder ERROR block can catch
		vm.Set("throwError", func(code int, msg string) goja.Value {
			err := fiber.NewError(code, string(msg))
			panic(vm.ToValue(err.Error() + fmt.Sprintf("::__FIBER_ERROR__%d", code)))
		})
		if cfg != nil {
			vm.Set("include", func(call goja.FunctionCall) goja.Value {
				file := call.Argument(0).String()
				fullPath := filepath.Join(dir, file)
				res, err := ProcessFile(fullPath, c, cfg)
				if err != nil {
					return vm.ToValue(fmt.Sprintf("Include Error: %v", err))
				}
				return vm.ToValue(res)
			})
		}
	} else {
		vm.Set("include", func(call goja.FunctionCall) goja.Value {
			file := call.Argument(0).String()
			fullPath := filepath.Join(dir, file)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				vm.Interrupt(vm.NewGoError(fmt.Errorf("Include Error: %v", err)))
			} else {
				_, err = vm.RunString(string(content))
				if err != nil {
					vm.Interrupt(vm.NewGoError(fmt.Errorf("Include Error: %v", err)))
				}
			}
			return vm.ToValue(nil)
		})
	}

	return vm
}

// ProcessString renders a template string with embedded JS and Mustache.
// settings (optional) will be injected as the global `settings` object.
func Process(content []byte, dir string, c fiber.Ctx, cfg *config.AppConfig, settings ...map[string]string) ([]byte, error) {
	res, err := ProcessString(string(content), dir, c, cfg, settings...)
	if err != nil {
		return nil, err
	}
	return []byte(res), nil
}

// ProcessString renders a template string with embedded JS and Mustache.
// settings (optional) will be injected as the global `settings` object.
func ProcessString(content string, dir string, c fiber.Ctx, cfg *config.AppConfig, settings ...map[string]string) (string, error) {
	vm := New(dir, c, cfg)

	// Inject settings if provided
	if len(settings) > 0 && settings[0] != nil {
		settingsObj := vm.NewObject()
		for k, v := range settings[0] {
			settingsObj.Set(k, v)
		}
		vm.Set("settings", settingsObj)
	}

	processedContent, err := vm.ExecuteJS(content, dir)
	if err != nil {
		return "", err
	}

	// Prepare context for Mustache
	data := make(map[string]interface{})
	if len(settings) > 0 {
		for k, v := range settings[0] {
			data[k] = v
		}
	}
	for _, k := range vm.GlobalObject().Keys() {
		if k == "db" || k == "require" || k == "console" || k == "sse" || k == "include" || k == "print" || k == "settings" || k == "config" {
			continue
		}
		val := vm.GlobalObject().Get(k)
		if val != nil {
			data[k] = vm.Export(val)
		}
	}

	// If outputBuffer has content, where does it go?
	// Usually `print` in `<?js ... ?>` outputs *at the location of the block*?
	// But our ExecuteJS logic assumes blocks are replaced/removed.
	// If `print` is used, maybe we should've handled it inside ExecuteJS.
	// Let's assume `print` appends to the buffer, but unless we know WHERE, it's hard.
	// Simplified: `print` output is prepended or just ignored for now?
	// User example uses `var title = ...` and then `{{title}}`. This relies on context.
	// No explicit `print` usage in example.
	// But `<?= ... ?>` is explicit output.

	// Provider for partials
	// Looking in the file's directory
	var rendered string
	if dir != "" {
		provider := &mustache.FileProvider{
			Paths:      []string{dir},
			Extensions: []string{".html", ".mustache", ".tmpl", ".htm"},
		}

		rendered, err = mustache.RenderPartials(processedContent, provider, data)
		if err != nil {
			return "", err
		}
	} else {
		rendered, err = mustache.Render(processedContent, data)
	}

	// Final step: Inject HTMX if it's a full HTML document
	return injectHTMX(rendered, cfg), nil
}

// ProcessFile reads a file (from cache or disk), executes embedded JS, and renders Mustache.
// If the Fiber context carries a "_fsrouter_cache" Local implementing ReadFile,
// it is used to avoid repeated disk I/O.
func ProcessFile(path string, c fiber.Ctx, cfg *config.AppConfig, settings ...map[string]string) (string, error) {
	var content []byte
	var err error

	// Utiliser le cache FsRouter si disponible (injecté via c.Locals)
	type fileCacheReader interface {
		ReadFile(string) ([]byte, error)
	}
	if c != nil {
		if cache, ok := c.Locals("_fsrouter_cache").(fileCacheReader); ok {
			content, err = cache.ReadFile(path)
		}
	}
	if content == nil && err == nil {
		content, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	return ProcessString(string(content), dir, c, cfg, settings...)
}

func (p *Processor) Export(val goja.Value) interface{} {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) || p.Runtime == nil {
		return nil
	}

	// Use JSON.stringify for deep export
	jsonObj := p.Get("JSON")
	if jsonObj == nil || goja.IsUndefined(jsonObj) {
		return val.Export()
	}

	stringifyVal := jsonObj.ToObject(p.Runtime).Get("stringify")
	if stringifyVal == nil || goja.IsUndefined(stringifyVal) {
		return val.Export()
	}

	stringify, ok := goja.AssertFunction(stringifyVal)
	if !ok {
		return val.Export()
	}

	res, err := stringify(goja.Undefined(), val)
	if err != nil {
		return val.Export()
	}

	jsonStr := res.String()
	if jsonStr == "" || jsonStr == "undefined" || jsonStr == "null" {
		return nil
	}

	var result interface{}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return val.Export()
	}

	return result
}

func injectHTMX(content string, cfg *config.AppConfig) string {
	// Check if this looks like a full page (presence of <html> tag)
	if !strings.Contains(strings.ToLower(content), "<html") {
		return content
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return content
	}

	// Check for <html> and <!DOCTYPE html>
	// goquery parses even partials as full documents sometimes, so we check carefully
	htmlTag := doc.Find("html")
	if htmlTag.Length() == 0 {
		return content
	}

	htmxScript := ""
	if !cfg.NoHtmx {
		htmxScript = fmt.Sprintf("\n<!-- HTMX -->\n<script src=\"%s\"></script>\n<!-- End HTMX -->\n", cfg.HtmxURL)
	}
	if cfg.InjectHTML != "" {
		htmxScript += fmt.Sprintf("\n<!-- Inject HTML -->\n%s\n<!-- End Inject HTML -->\n", cfg.InjectHTML)
	}
	head := doc.Find("head")
	if head.Length() > 0 {
		head.AppendHtml(htmxScript)
	} else {
		body := doc.Find("body")
		if body.Length() > 0 {
			body.PrependHtml(htmxScript)
		} else {
			// Fallback: append to html
			htmlTag.AppendHtml(htmxScript)
		}
	}
	res, _ := doc.Html()
	return res
}

func (p *Processor) ExecuteFile(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return p.ExecuteJS(string(content), filepath.Dir(filePath), true)
}

// ExecuteJS parses <?js ... ?>, <?= ... ?>, and <script server ...> in sequence
func (p *Processor) ExecuteJS(content string, baseDir string, isNotATemplate ...bool) (string, error) {
	isJsScript := false
	if len(isNotATemplate) > 0 {
		isJsScript = isNotATemplate[0]
	}
	if isJsScript {
		val, err := p.RunString(content)
		if err != nil {
			return "", fmt.Errorf("JS Error in <?= ... ?>: %v", err)
		}
		jsonObj := p.Get("JSON").ToObject(p.Runtime)
		stringify := jsonObj.Get("stringify")
		stringifyFn, ok := goja.AssertFunction(stringify)
		if !ok {
			return "", fmt.Errorf("stringify is not a function in script")
		}
		res, err := stringifyFn(goja.Undefined(), val)
		if err != nil {
			return "", err
		}
		return res.String(), nil
	}

	// Remove HTML comments
	reComments := regexp.MustCompile(`(?s)<!--.*?-->`)
	content = reComments.ReplaceAllString(content, "")

	// Remove Mustache comments
	reComments = regexp.MustCompile(`(?s){{!.*?}}`)
	content = reComments.ReplaceAllString(content, "")

	// Combined regex to match both PHP-style and <script server> tags
	// Group 1: PHP type, Group 2: PHP code
	// Group 3: Script src, Group 4: Script code
	re := regexp.MustCompile(`(?i)(?:<\?(=|js)?\s*([\s\S]*?)\s*\?>)|(?:<script\s+server(?:\s+src="([^"]+)")?\s*>(?:([\s\S]*?)<\/script>)?)`)

	var errReturn error

	result := re.ReplaceAllStringFunc(content, func(match string) string {
		if errReturn != nil {
			return ""
		}

		submatches := re.FindStringSubmatch(match)
		if len(submatches) == 0 {
			return match
		}

		// Check if it's a PHP tag
		if strings.HasPrefix(match, "<?") {
			tagType := submatches[1] // "=" or "js" or ""
			code := submatches[2]

			if tagType == "=" {
				val, err := p.RunString(code)
				if err != nil {
					errReturn = fmt.Errorf("JS Error in <?= ... ?>: %v", err)
					return ""
				}
				return fmt.Sprint(val.Export())
			} else {
				_, err := p.RunString(code)
				if err != nil {
					errReturn = fmt.Errorf("JS Error in <?js ... ?>: %v", err)
					return ""
				}
				return ""
			}
		}

		// Check if it's a <script server> tag
		if strings.HasPrefix(strings.ToLower(match), "<script") {
			src := submatches[3]
			code := submatches[4]

			if src != "" {
				fullPath := src
				if !filepath.IsAbs(src) {
					fullPath = filepath.Join(baseDir, src)
				}

				scriptContent, err := os.ReadFile(fullPath)
				if err != nil {
					errReturn = fmt.Errorf("JS Error: could not read script src %s: %v", src, err)
					return ""
				}

				_, err = p.Runtime.RunString(string(scriptContent))
				if err != nil {
					errReturn = fmt.Errorf("JS Error in <script server src=\"%s\">: %v", src, err)
					return ""
				}
			}

			if strings.TrimSpace(code) != "" {
				_, err := p.Runtime.RunString(code)
				if err != nil {
					if jsErr, ok := err.(*goja.Exception); ok {
						stack := jsErr.Stack()
						if len(stack) > 1 {
							pos := stack[1].Position()
							errReturn = fmt.Errorf("JS Error in <script server>: %v:%v, %v", pos.Line, pos.Column, jsErr.Error())
						} else {
							errReturn = fmt.Errorf("JS Error in <script server>: %v", jsErr.Error())
						}
					} else {
						errReturn = fmt.Errorf("JS Error in <script server>: %v", err)
					}
					return ""
				}
			}
			return ""
		}

		return match
	})

	if errReturn != nil {
		return "", errReturn
	}

	return result, nil
}
