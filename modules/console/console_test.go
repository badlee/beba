package console

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

type MockPrinter struct {
	logs []string
}

func (m *MockPrinter) Log(s string)   { m.logs = append(m.logs, "LOG: "+s) }
func (m *MockPrinter) Debug(s string) { m.logs = append(m.logs, "DEBUG: "+s) }
func (m *MockPrinter) Info(s string)  { m.logs = append(m.logs, "INFO: "+s) }
func (m *MockPrinter) Warn(s string)  { m.logs = append(m.logs, "WARN: "+s) }
func (m *MockPrinter) Error(s string) { m.logs = append(m.logs, "ERROR: "+s) }

func TestConsoleOutput(t *testing.T) {
	vm := goja.New()
	
	printer := &MockPrinter{}
	mod := &Module{printer: printer}
	
	exports := vm.NewObject()
	moduleObj := vm.NewObject()
	moduleObj.Set("exports", exports)
	
	mod.Loader(nil, vm, moduleObj)
	
	vm.Set("console", exports)

	script := `
		console.log("hello", "world");
		console.error("fatal", "error");
		console.warn("warning");
		console.info("notice");
		console.debug("trace");
	`
	
	_, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("JS Execution failed: %v", err)
	}
	
	logStr := strings.Join(printer.logs, "|")
	
	expected := []string{
		"LOG: hello world",
		"ERROR: fatal error",
		"WARN: warning",
		"INFO: notice",
		"DEBUG: trace",
	}
	
	for _, exp := range expected {
		if !strings.Contains(logStr, exp) {
			t.Errorf("Expected to find %q in logs, got %q (Log string: %v)", exp, logStr, logStr)
		}
	}
}
