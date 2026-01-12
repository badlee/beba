package processor

import (
	"encoding/json"
	"fmt"
	"http-server/modules"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"http-server/plugins/require"

	"github.com/PuerkitoBio/goquery"
	"github.com/cbroglie/mustache"
	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"

	"http-server/modules/sse"
)

func init() {

}

// ProcessFile reads a file, executes embedded JS, and renders Mustache
func ProcessFile(path string, c fiber.Ctx, sseManager *sse.Manager) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	c.Locals("sseManager", sseManager)
	dir := filepath.Dir(path)

	var registry = require.NewRegistry(
		require.WithInstance(c),
		require.WithLoader[fiber.Ctx](func(path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, path))
		}),
		require.WithGlobalFolders[fiber.Ctx]("node_modules", "js_modules"),
		require.WithPathResolver[fiber.Ctx](func(base, path string) string {
			return filepath.Join(base, path)
		}),
	) // this can be shared by multiple runtimes
	vm := goja.New()
	modules.Register(registry) // attache all modules
	registry.Enable(vm)
	// buffer.Enable(vm)

	// Capture print output if needed?
	// For parity with some PHP-like envs, print output goes to output.
	// We can implement a rudimentary output buffer.
	var outputBuffer strings.Builder
	vm.Set("print", func(call goja.FunctionCall) goja.Value {
		for _, arg := range call.Arguments {
			outputBuffer.WriteString(fmt.Sprint(arg.Export()))
		}
		return goja.Undefined()
	})

	vm.Set("include", func(call goja.FunctionCall) goja.Value {
		file := call.Argument(0).String()
		fullPath := filepath.Join(dir, file)
		res, err := ProcessFile(fullPath, c, sseManager)
		if err != nil {
			return vm.ToValue(fmt.Sprintf("Include Error: %v", err))
		}
		return vm.ToValue(res)
	})

	// Remove HTML comments before processing
	reComments := regexp.MustCompile(`(?s)<!--.*?-->`)
	cleanContent := reComments.ReplaceAllString(string(content), "")

	processedContent, err := executeJS(vm, cleanContent, filepath.Dir(path))
	if err != nil {
		return "", err
	}

	// Prepare context for Mustache
	data := make(map[string]interface{})
	for _, k := range vm.GlobalObject().Keys() {
		// Skip known modules/internals to avoid stringify issues
		if k == "db" || k == "require" || k == "console" || k == "sse" || k == "include" || k == "print" {
			continue
		}
		val := vm.GlobalObject().Get(k)
		if val != nil {
			data[k] = exportRecursive(vm, val)
		}
	}

	// If outputBuffer has content, where does it go?
	// Usually `print` in `<?js ... ?>` outputs *at the location of the block*?
	// But our executeJS logic assumes blocks are replaced/removed.
	// If `print` is used, maybe we should've handled it inside executeJS.
	// Let's assume `print` appends to the buffer, but unless we know WHERE, it's hard.
	// Simplified: `print` output is prepended or just ignored for now?
	// User example uses `var title = ...` and then `{{title}}`. This relies on context.
	// No explicit `print` usage in example.
	// But `<?= ... ?>` is explicit output.

	// Provider for partials
	// Looking in the file's directory
	provider := &mustache.FileProvider{
		Paths:      []string{filepath.Dir(path)},
		Extensions: []string{".html", ".mustache", ".js", ".htm"},
	}

	rendered, err := mustache.RenderPartials(processedContent, provider, data)
	if err != nil {
		return "", err
	}

	// Flush any pending SSE events
	defer sse.Flush(c)

	// Final step: Inject HTMX if it's a full HTML document
	return injectHTMX(rendered), nil
}

func exportRecursive(vm *goja.Runtime, val goja.Value) interface{} {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) || vm == nil {
		return nil
	}

	// Use JSON.stringify for deep export
	jsonObj := vm.Get("JSON")
	if jsonObj == nil || goja.IsUndefined(jsonObj) {
		return val.Export()
	}

	stringifyVal := jsonObj.ToObject(vm).Get("stringify")
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

func injectHTMX(content string) string {
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

	// Check if this looks like a full page (presence of <html> tag)
	// We want to inject only if it's a full document
	if strings.Contains(strings.ToLower(content), "<html") {
		htmxScript := `<script src="https://unpkg.com/htmx.org@2.0.0"></script>`
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

	return content
}

// executeJS parses <?js ... ?>, <?= ... ?>, and <script server ...> in sequence
func executeJS(vm *goja.Runtime, content string, baseDir string) (string, error) {
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
				val, err := vm.RunString(code)
				if err != nil {
					errReturn = fmt.Errorf("JS Error in <?= ... ?>: %v", err)
					return ""
				}
				return fmt.Sprint(val.Export())
			} else {
				_, err := vm.RunString(code)
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

				_, err = vm.RunString(string(scriptContent))
				if err != nil {
					errReturn = fmt.Errorf("JS Error in <script server src=\"%s\">: %v", src, err)
					return ""
				}
			}

			if strings.TrimSpace(code) != "" {
				_, err := vm.RunString(code)
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
