package main

import (
	"fmt"
	"http-server/plugins/config"
	"http-server/processor"
	"os"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
)

// mockCtx implements a minimal fiber.Ctx for testing
type mockCtx struct {
	fiber.Ctx
	locals  map[string]interface{}
	headers map[string]string
	cookies map[string]string
	out     strings.Builder
}

func (m *mockCtx) String() string {
	return "mockCtx"
}

func (m *mockCtx) Locals(key any, value ...any) any {
	if m.locals == nil {
		m.locals = make(map[string]interface{})
	}
	if len(value) > 0 {
		m.locals[key.(string)] = value[0]
	}
	return m.locals[key.(string)]
}

func (m *mockCtx) Set(key, val string) {
	if m.headers == nil {
		m.headers = make(map[string]string)
	}
	m.headers[key] = val
}

func (m *mockCtx) Get(key string, defaultValue ...string) string {
	if val, ok := m.headers[key]; ok {
		return val
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

func (m *mockCtx) Cookies(name string, defaultValue ...string) string {
	if val, ok := m.cookies[name]; ok {
		return val
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

func (m *mockCtx) SendString(s string) error {
	m.out.WriteString(s)
	return nil
}

func (m *mockCtx) Status(n int) fiber.Ctx {
	return m
}

func (m *mockCtx) App() *fiber.App {
	return fiber.New()
}

func (m *mockCtx) Protocol() string {
	return "http"
}

func (m *mockCtx) Method(args ...string) string {
	if len(args) > 0 {
		return args[0]
	}
	return "GET"
}

func runTemplateTest(filePath string, cfg *config.AppConfig) error {
	// 1. Redirect Logs
	stdoutFile, _ := os.Create(cfg.Stdout)
	stderrFile, _ := os.Create(cfg.Stderr)
	defer stdoutFile.Close()
	defer stderrFile.Close()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	os.Stdout = stdoutFile
	os.Stderr = stderrFile
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	// multi writer if we want to still see it? User said "redirige ... vers des fichiers"
	// Let's just redirect.

	// 2. Render Template
	ctx := &mockCtx{
		locals: make(map[string]interface{}),
	}

	rendered, err := processor.ProcessFile(filePath, ctx, cfg)
	if err != nil {
		return fmt.Errorf("Render Error: %v", err)
	}

	// 3. Find elements
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rendered))
	if err != nil {
		return fmt.Errorf("Parse Error: %v", err)
	}

	var targets *goquery.Selection
	if cfg.Find != "" {
		targets = doc.Find(cfg.Find)
	} else {
		// If no find selector, treat the whole body or doc as target?
		targets = doc.Selection
	}

	if targets.Length() == 0 && cfg.Find != "" {
		return fmt.Errorf("No elements found matching: %s", cfg.Find)
	}

	// 4. Match validation
	originalExpr := cfg.Match
	if cfg.Match != "" {
		valid := false

		// Try RegExp first
		if strings.HasPrefix(cfg.Match, "/") {
			lastSlash := strings.LastIndex(cfg.Match, "/")
			if lastSlash > 0 {
				reStr := cfg.Match[1:lastSlash]
				flags := regexp.MustCompile("[^imsug]").ReplaceAllString(strings.ToLower(cfg.Match[lastSlash+1:]), "")
				cfg.Match = fmt.Sprintf("(/%s/%s).test(text)", strings.TrimSpace(reStr), strings.TrimSpace(flags))

				// // Map JS flags to Go flags
				// // i (case-insensitive), g (global - ignored in match), m (multiline), s (dotall), u (unicode)
				// goFlags := ""
				// for _, f := range flags {
				// 	switch f {
				// 	case 'i':
				// 		goFlags += "i"
				// 	case 'm':
				// 		goFlags += "m"
				// 	case 's':
				// 		goFlags += "s"
				// 	case 'u':
				// 		// Go regex is unicode by default usually, but 'U' is ungreedy.
				// 		// In JS 'u' is unicode. In Go, we'll map 'u' to nothing or similar if not needed.
				// 	case 'g':
				// 		// Global is not really applicable for a single MatchString
				// 	}
				// }

				// if goFlags != "" {
				// 	reStr = "(?" + goFlags + ")" + reStr
				// }

				// re, err := regexp.Compile(reStr)
				// if err != nil {
				// 	return fmt.Errorf("Invalid RegExp: %v", err)
				// }

				// targets.Each(func(i int, s *goquery.Selection) {
				// 	text := s.Text()
				// 	if re.MatchString(text) {
				// 		valid = true
				// 	}
				// })
			}
		}
		// Try JS expression
		vm := goja.New()

		targets.Each(func(i int, s *goquery.Selection) {
			stdout, _ := os.ReadFile(cfg.Stdout)
			stderr, _ := os.ReadFile(cfg.Stderr)

			html, _ := s.Html()
			vm.Set("text", s.Text())
			vm.Set("html", html)
			vm.Set("stdout", string(stdout))
			vm.Set("stderr", string(stderr))

			code := fmt.Sprintf("(%s);", cfg.Match)
			res, err := vm.RunString(code)
			if err == nil {
				if res.ToBoolean() {
					valid = true
				}
			}
		})

		if !valid {
			fmt.Fprintln(oldStdout, "--- Rendered HTML ---\n", rendered, "\n-------------------")
			return fmt.Errorf("Match failed for expression: %s", originalExpr)
		}
	}

	fmt.Fprintln(oldStdout, "Test Passed!")
	return nil
}

func printCapturedLogs(testStdout string, testStderr string) {
	stdout, _ := os.ReadFile(testStdout)
	stderr, _ := os.ReadFile(testStderr)

	if len(stdout) > 0 {
		fmt.Println("\n--- Captured Stdout ---")
		fmt.Print(string(stdout))
	}
	if len(stderr) > 0 {
		fmt.Println("\n--- Captured Stderr ---")
		fmt.Print(string(stderr))
	}
}
