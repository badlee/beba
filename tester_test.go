package main

import (
	"os"
	"testing"
)

func TestRunTemplateTest(t *testing.T) {
	// Create a temporary test template
	tmplBody := `
<!DOCTYPE html>
<html>
<body>
	<h1 id="test-title">Hello World</h1>
	<span class="status">OK</span>
	<script server>
		const console = require("console");
		console.log("Internal Log");
	</script>
</body>
</html>
`
	tmpFile := "temp_test_tmpl.html"
	err := os.WriteFile(tmpFile, []byte(tmplBody), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile)
	defer os.Remove("stdout.log")
	defer os.Remove("stderr.log")

	tests := []struct {
		name     string
		find     string
		match    string
		expected bool
	}{
		{
			name:     "Find by Tag and RegExp Match",
			find:     "h1",
			match:    "/Hello/",
			expected: true,
		},
		{
			name:     "Find by ID and RegExp Match",
			find:     "#test-title",
			match:    "/World/",
			expected: true,
		},
		{
			name:     "Find by Class and JS Match",
			find:     ".status",
			match:    "text == 'OK'",
			expected: true,
		},
		{
			name:     "Case-insensitive RegExp Match",
			find:     "h1",
			match:    "/HELLO/i",
			expected: true,
		},
		{
			name:     "Fail on Wrong RegExp",
			find:     "h1",
			match:    "/Goodbye/",
			expected: false,
		},
		{
			name:     "Fail on Missing Element",
			find:     ".missing",
			match:    "/any/",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runTemplateTest(tmpFile, tt.find, tt.match, "stdout.log", "stderr.log")
			if (err == nil) != tt.expected {
				t.Errorf("expected success=%v, got err=%v", tt.expected, err)
			}
		})
	}

	// Verify log redirection with custom path
	customStdout := "custom_stdout.log"
	err = runTemplateTest(tmpFile, "h1", "/Hello/", customStdout, "stderr.log")
	if err != nil {
		t.Errorf("Failed with custom stdout: %v", err)
	}
	if _, err := os.Stat(customStdout); os.IsNotExist(err) {
		t.Error("custom_stdout.log was not created")
	}
	os.Remove(customStdout)
}
