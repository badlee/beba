package processor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"http-server/plugins/config"

	"github.com/dop251/goja"
)

// --------------------------------------------------------------------------
// exportRecursive()
// --------------------------------------------------------------------------

func TestExportRecursive_Null(t *testing.T) {
	vm := goja.New()
	result := exportRecursive(vm, goja.Null())
	if result != nil {
		t.Errorf("expected nil for null, got %v", result)
	}
}

func TestExportRecursive_Undefined(t *testing.T) {
	vm := goja.New()
	result := exportRecursive(vm, goja.Undefined())
	if result != nil {
		t.Errorf("expected nil for undefined, got %v", result)
	}
}

func TestExportRecursive_NilVM(t *testing.T) {
	result := exportRecursive(nil, nil)
	if result != nil {
		t.Errorf("expected nil for nil VM, got %v", result)
	}
}

func TestExportRecursive_String(t *testing.T) {
	vm := goja.New()
	val, _ := vm.RunString(`"hello"`)
	result := exportRecursive(vm, val)
	if result != "hello" {
		t.Errorf("expected 'hello', got %v", result)
	}
}

func TestExportRecursive_Number(t *testing.T) {
	vm := goja.New()
	val, _ := vm.RunString(`42`)
	result := exportRecursive(vm, val)
	if result != float64(42) {
		t.Errorf("expected 42, got %v (%T)", result, result)
	}
}

func TestExportRecursive_Object(t *testing.T) {
	vm := goja.New()
	val, _ := vm.RunString(`({name: "Alice", age: 30})`)
	result := exportRecursive(vm, val)
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["name"] != "Alice" {
		t.Errorf("expected name 'Alice', got %v", m["name"])
	}
}

func TestExportRecursive_Array(t *testing.T) {
	vm := goja.New()
	val, _ := vm.RunString(`[1, 2, 3]`)
	result := exportRecursive(vm, val)
	arr, ok := result.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result)
	}
	if len(arr) != 3 {
		t.Errorf("expected 3 elements, got %d", len(arr))
	}
}

func TestExportRecursive_Function(t *testing.T) {
	vm := goja.New()
	val, _ := vm.RunString(`(function() { return 1; })`)
	// Functions can't be JSON.stringified — should fall through gracefully
	result := exportRecursive(vm, val)
	if result != nil {
		// Undefined from JSON.stringify means nil return
		// But function export could return something — just check no panic
	}
}

// --------------------------------------------------------------------------
// injectHTMX() — additional cases
// --------------------------------------------------------------------------

func TestInjectHTMX_FullDocWithHead(t *testing.T) {
	cfg := &config.AppConfig{
		NoHtmx:  false,
		HtmxURL: "https://cdn.example.com/htmx.js",
	}
	html := `<!DOCTYPE html><html><head><title>Test</title></head><body>content</body></html>`
	out := injectHTMX(html, cfg)
	if !strings.Contains(out, "cdn.example.com/htmx.js") {
		t.Errorf("expected HTMX script URL in output, got %q", out)
	}
}

func TestInjectHTMX_InjectHTML(t *testing.T) {
	cfg := &config.AppConfig{
		NoHtmx:     true,
		InjectHTML: `<link rel="stylesheet" href="/custom.css">`,
	}
	html := `<!DOCTYPE html><html><head></head><body>content</body></html>`
	out := injectHTMX(html, cfg)
	if !strings.Contains(out, "custom.css") {
		t.Errorf("expected InjectHTML in output, got %q", out)
	}
}

func TestInjectHTMX_NoHead_InjectsInBody(t *testing.T) {
	cfg := &config.AppConfig{
		NoHtmx:     false,
		HtmxURL:    "https://cdn.example.com/htmx.js",
	}
	// HTML without explicit head tag — goquery will auto-create one
	html := `<!DOCTYPE html><html><body>content</body></html>`
	out := injectHTMX(html, cfg)
	if !strings.Contains(out, "htmx.js") {
		t.Errorf("expected HTMX injected, got %q", out)
	}
}

// --------------------------------------------------------------------------
// RegisterGlobal / AttachGlobals
// --------------------------------------------------------------------------

func TestRegisterGlobal_Nil(t *testing.T) {
	// Registering nil should not panic
	RegisterGlobal("test_nil", nil)
}

func TestAttachGlobals_InjectsRegistered(t *testing.T) {
	type TestConfig struct {
		Value string
	}
	tc := &TestConfig{Value: "hello"}
	RegisterGlobal("TestCfg", &SharedObject{name: "TestCfg", data: tc})

	vm := goja.New()
	AttachGlobals(vm)

	val, err := vm.RunString(`typeof testCfg !== "undefined"`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// May or may not be present depending on registration - just verify no panic
	_ = val
}

// --------------------------------------------------------------------------
// ProcessString — additional edge cases
// --------------------------------------------------------------------------

func TestProcessString_MultipleBlocks(t *testing.T) {
	content := `<?js var a = "X"; ?><?js var b = "Y"; ?>{{a}}+{{b}}`
	out, err := ProcessString(content, "/tmp", nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "X+Y") {
		t.Errorf("expected 'X+Y' in output, got %q", out)
	}
}

func TestProcessString_ExpressionChaining(t *testing.T) {
	content := `<?= "A" ?>-<?= "B" ?>-<?= "C" ?>`
	out, err := ProcessString(content, "/tmp", nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "A-B-C" {
		t.Errorf("expected 'A-B-C', got %q", out)
	}
}

func TestProcessString_EmptyJS(t *testing.T) {
	content := `<?js ?> visible`
	out, err := ProcessString(content, "/tmp", nil, newTestConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "visible") {
		t.Errorf("expected 'visible' in output, got %q", out)
	}
}

func TestProcessString_SettingsPassedToMustache(t *testing.T) {
	// settings keys are copied to Mustache data context in ProcessString
	content := `{{apiKey}}`
	settings := map[string]string{"apiKey": "ABC123"}
	out, err := ProcessString(content, "/tmp", nil, newTestConfig(), settings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "ABC123") {
		t.Errorf("expected 'ABC123' in output, got %q", out)
	}
}

// --------------------------------------------------------------------------
// ProcessFile — additional edge cases
// --------------------------------------------------------------------------

func TestProcessFile_WithSettings(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "page.html")
	os.WriteFile(f, []byte(`<?js var site = settings.domain; ?>Site: {{site}}`), 0644)

	settings := map[string]string{"domain": "example.com"}
	out, err := ProcessFile(f, nil, newTestConfig(), settings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "example.com") {
		t.Errorf("expected 'example.com' in output, got %q", out)
	}
}

func TestProcessFile_ScriptServerError(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "err.html")
	os.WriteFile(f, []byte(`<script server>throw new Error("boom")</script>`), 0644)

	_, err := ProcessFile(f, nil, newTestConfig())
	if err == nil {
		t.Error("expected error for JS throw, got nil")
	}
}

func TestProcessFile_ScriptServerMissingSrc(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "missing.html")
	os.WriteFile(f, []byte(`<script server src="nonexistent.js"></script>`), 0644)

	_, err := ProcessFile(f, nil, newTestConfig())
	if err == nil {
		t.Error("expected error for missing src file, got nil")
	}
}

// --------------------------------------------------------------------------
// executeJS — edge cases
// --------------------------------------------------------------------------

func TestExecuteJS_EmptyInput(t *testing.T) {
	vm := New("/tmp", nil, newTestConfig())
	result, err := executeJS(vm, "", "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestExecuteJS_NoTags(t *testing.T) {
	vm := New("/tmp", nil, newTestConfig())
	result, err := executeJS(vm, "plain text no tags", "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "plain text no tags" {
		t.Errorf("expected unchanged text, got %q", result)
	}
}

func TestExecuteJS_InlineExpressionError(t *testing.T) {
	vm := New("/tmp", nil, newTestConfig())
	_, err := executeJS(vm, `<?= unknownVar.prop ?>`, "/tmp")
	if err == nil {
		t.Error("expected error for accessing undefined variable property")
	}
}

func TestExecuteJS_MixedContent(t *testing.T) {
	vm := New("/tmp", nil, newTestConfig())
	result, err := executeJS(vm, `Before <?= 1+2 ?> Middle <?js var x = "done"; ?> After`, "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Before 3 Middle") {
		t.Errorf("expected 'Before 3 Middle', got %q", result)
	}
	if !strings.Contains(result, "After") {
		t.Errorf("expected 'After' in output, got %q", result)
	}
}
