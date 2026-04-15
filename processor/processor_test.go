package processor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"http-server/plugins/config"
)

// newTestConfig returns a minimal AppConfig suitable for tests.
func newTestConfig() *config.AppConfig {
	return &config.AppConfig{
		NoHtmx: true, // disable HTMX injection to keep test output predictable
	}
}

// --------------------------------------------------------------------------
// New() — JS Runtime
// --------------------------------------------------------------------------

func TestNew_BasicJSExecution(t *testing.T) {
	vm := NewEmpty()
	val, err := vm.RunString(`1 + 1`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val.Export().(int64) != 2 {
		t.Errorf("expected 2, got %v", val.Export())
	}
}

func TestNew_PrintCapture(t *testing.T) {
	vm := NewEmpty()
	_, err := vm.RunString(`print("hello world")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outFn, ok := vm.Get("__output").Export().(func() string)
	if !ok {
		// __output is set as a Go func — use RunString to check
		res, err := vm.RunString(`__output()`)
		if err != nil {
			t.Fatalf("error getting output: %v", err)
		}
		got := res.String()
		if !strings.Contains(got, "hello world") {
			t.Errorf("expected 'hello world' in output, got %q", got)
		}
	} else {
		got := outFn()
		if !strings.Contains(got, "hello world") {
			t.Errorf("expected 'hello world' in output, got %q", got)
		}
	}
}

func TestNew_ProcessEnv(t *testing.T) {
	os.Setenv("TEST_PROC_KEY", "test_proc_value")
	defer os.Unsetenv("TEST_PROC_KEY")

	vm := NewEmpty()
	val, err := vm.RunString(`process.env["TEST_PROC_KEY"]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val.String() != "test_proc_value" {
		t.Errorf("expected 'test_proc_value', got %q", val.String())
	}
}

// --------------------------------------------------------------------------
// ProcessString()
// --------------------------------------------------------------------------

func TestProcessString_PlainText(t *testing.T) {
	out, err := ProcessString("Hello, World!", "/tmp", nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", out)
	}
}

func TestProcessString_MustacheVariable(t *testing.T) {
	// <?js ?> sets a variable, Mustache renders it
	content := `<?js var greeting = "Bonjour"; ?> {{greeting}}`
	out, err := ProcessString(content, "/tmp", nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Bonjour") {
		t.Errorf("expected 'Bonjour' in output, got %q", out)
	}
}

func TestProcessString_InlineExpression(t *testing.T) {
	// <?= expr ?> outputs inline
	content := `Result: <?= 2 * 21 ?>`
	out, err := ProcessString(content, "/tmp", nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("expected '42' in output, got %q", out)
	}
}

func TestProcessString_ScriptServer(t *testing.T) {
	content := `<script server>var x = 10 + 5;</script>{{x}}`
	out, err := ProcessString(content, "/tmp", nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "15") {
		t.Errorf("expected '15' in output, got %q", out)
	}
}

func TestProcessString_Settings(t *testing.T) {
	// 'settings' is filtered from the Mustache context (by design, to avoid serialization issues).
	// To render settings in templates, the JS code must copy it to an exposed global.
	content := `<?js var siteName = settings.siteName; ?> {{siteName}}`
	settings := map[string]string{"siteName": "TestSite"}
	out, err := ProcessString(content, "/tmp", nil, newTestConfig(), settings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "TestSite") {
		t.Errorf("expected 'TestSite' in output, got %q", out)
	}
}

func TestProcessString_HTMLCommentStripped(t *testing.T) {
	content := `<!-- this is a comment -->visible`
	out, err := ProcessString(content, "/tmp", nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "this is a comment") {
		t.Errorf("comment should have been stripped, got %q", out)
	}
	if !strings.Contains(out, "visible") {
		t.Errorf("expected 'visible' in output, got %q", out)
	}
}

func TestProcessString_JSError(t *testing.T) {
	content := `<?js this is not valid JS ?>`
	_, err := ProcessString(content, "/tmp", nil, newTestConfig())
	if err == nil {
		t.Error("expected error for invalid JS, got nil")
	}
}

// --------------------------------------------------------------------------
// Process() — []byte variant
// --------------------------------------------------------------------------

func TestProcess_ByteOutput(t *testing.T) {
	content := []byte(`<?js var msg = "byte test"; ?> {{msg}}`)
	out, err := Process(content, "/tmp", nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "byte test") {
		t.Errorf("expected 'byte test' in output, got %q", string(out))
	}
}

// --------------------------------------------------------------------------
// ProcessFile()
// --------------------------------------------------------------------------

func TestProcessFile_SimpleTemplate(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.html")
	err := os.WriteFile(f, []byte(`<?js var name = "World"; ?>Hello {{name}}!`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ProcessFile(f, nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Hello World!") {
		t.Errorf("expected 'Hello World!', got %q", out)
	}
}

func TestProcessFile_MissingFile(t *testing.T) {
	_, err := ProcessFile("/nonexistent/path/template.html", nil, newTestConfig())
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestProcessFile_ScriptServerSrcTag(t *testing.T) {
	dir := t.TempDir()

	// External JS file
	jsFile := filepath.Join(dir, "logic.js")
	err := os.WriteFile(jsFile, []byte(`var computed = 7 * 6;`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Template that loads it
	tplFile := filepath.Join(dir, "page.html")
	err = os.WriteFile(tplFile, []byte(`<script server src="logic.js"></script>Result: {{computed}}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	out, err := ProcessFile(tplFile, nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("expected '42' in output, got %q", out)
	}
}

// --------------------------------------------------------------------------
// executeJS() internal
// --------------------------------------------------------------------------

func TestExecuteJS_InlineExpression(t *testing.T) {
	vm := NewEmpty()
	result, err := vm.ExecuteJS(`<?= "inline" ?>`, "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "inline") {
		t.Errorf("expected 'inline', got %q", result)
	}
}

func TestExecuteJS_SetsVariable(t *testing.T) {
	vm := NewEmpty()
	result, err := vm.ExecuteJS(`<?js var x = 99; ?>Value: {{x}}`, "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// JS block is removed, Mustache substitution is left to Process
	if !strings.Contains(result, "Value: {{x}}") {
		t.Errorf("expected template placeholder preserved, got %q", result)
	}
	// Verify x was set in the VM
	val := vm.Get("x")
	if val == nil || val.Export().(int64) != 99 {
		t.Errorf("expected x=99 in VM, got %v", val)
	}
}

// --------------------------------------------------------------------------
// injectHTMX() — internal
// --------------------------------------------------------------------------

func TestInjectHTMX_SkippedWhenNoHtmx(t *testing.T) {
	cfg := &config.AppConfig{NoHtmx: true}
	html := `<html><head></head><body>content</body></html>`
	out := injectHTMX(html, cfg)
	if strings.Contains(out, "htmx") {
		t.Errorf("HTMX should not be injected when NoHtmx=true, got %q", out)
	}
}

func TestInjectHTMX_NotFullDocument(t *testing.T) {
	cfg := &config.AppConfig{NoHtmx: false, HtmxURL: "https://unpkg.com/htmx.org"}
	partial := `<div>partial content</div>`
	out := injectHTMX(partial, cfg)
	// No <html> tag → should return unchanged
	if out != partial {
		t.Errorf("partial content should not be modified, got %q", out)
	}
}
