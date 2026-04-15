package js

import (
	"strings"
	"testing"

	"github.com/dop251/goja/parser"
)

func TestEnsureReturnStrict(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected string
		ok       bool
	}{
		{
			name:     "Simple math",
			src:      "1 + 1",
			expected: "return 1 + 1",
			ok:       true,
		},
		{
			name:     "String concatenation",
			src:      "name + ' <' + email + '>'",
			expected: "return name + ' <' + email + '>'",
			ok:       true,
		},
		{
			name:     "Console log",
			src:      "console.log('JS: Preparing to save user: ' + name)",
			expected: "return console.log('JS: Preparing to save user: ' + name)",
			ok:       true,
		},
		{
			name:     "Template-like string",
			src:      "'Hello '+ s +' from ' + name",
			expected: "return 'Hello '+ s +' from ' + name",
			ok:       true,
		},
		{
			name: "Multi-line with console.log and findOne",
			src: `console.log('lookUp', email)
	findOne({ email: email })`,
			expected: `console.log('lookUp', email)
	return findOne({ email: email })`,
			ok: true,
		},
		{
			name:     "Already has return",
			src:      "function f() { return 42; }",
			expected: "function f() { return 42; }",
			ok:       false,
		},
		{
			name:     "Throw statement",
			src:      "throw new Error('fail')",
			expected: "throw new Error('fail')",
			ok:       false,
		},
		{
			name:     "Function literal expression",
			src:      "(function() {})",
			expected: "return (function() {})",
			ok:       true,
		},
		{
			name:     "Object literal expression",
			src:      "({ a: 1 })",
			expected: "return ({ a: 1 })",
			ok:       true,
		},
		{
			name:     "Array literal",
			src:      "[1, 2, 3]",
			expected: "return [1, 2, 3]",
			ok:       true,
		},
		{
			name:     "Empty string",
			src:      "",
			expected: "",
			ok:       false,
		},
		{
			name:     "Invalid JS",
			src:      "const a =",
			expected: "const a =",
			ok:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := EnsureReturnStrict(tt.src)
			if ok != tt.ok {
				t.Errorf("EnsureReturnStrict() ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.expected {
				t.Errorf("EnsureReturnStrict() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestContainsReturn(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{"Simple return", "return 1", true},
		{"No return", "1 + 1", false},
		{"Nested in if", "if (true) { return 1 }", true},
		{"Nested in block", "{ let a = 1; return a; }", true},
		{"Nested in function", "function f() { return 1 }", true},
		{"Nested in arrow", "() => { return 1 }", true},
		{"Not in arrow body expression", "() => 1", false},
		{"In call argument valid", "foo(function() { return 1 })", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.src
			if tt.want && !strings.Contains(src, "function") && !strings.Contains(src, "=>") {
				// Wrap in a function to make 'return' valid at top level for parsing
				src = "function _tester() { " + src + " }"
			}
			prg, err := parser.ParseFile(nil, "", src, 0)
			if err != nil {
				t.Fatalf("Failed to parse %q: %v", src, err)
			}
			if got := containsReturn(prg); got != tt.want {
				t.Errorf("containsReturn() = %v, want %v for src %q", got, tt.want, src)
			}
		})
	}
}

func TestIsFunction(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{"Function declaration", "function foo() {}", true},
		{"Function expression", "(function() {})", true},
		{"Arrow function", "(x) => x", true},
		{"Async function", "async function() {}", true},
		{"IIFE function", "(function() {})()", true},
		{"IIFE arrow", "((x) => x)(1)", true},
		{"Not a function", "const a = 1", false},
		{"Math expression", "1 + 1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsFunction(tt.src); got != tt.want {
				t.Errorf("IsFunction(%q) = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestGetFunction(t *testing.T) {
	tests := []struct {
		name            string
		code            string
		functionBuilder []string
		want            string
	}{
		{
			name: "Simple expression",
			code: "a + b",
			want: "(function() { return a + b; })",
		},
		{
			name: "Existing function",
			code: "function(x) { return x + 1; }",
			want: "(function(x) { return x + 1; })",
		},
		{
			name:            "Custom builder",
			code:            "console.log('hi')",
			functionBuilder: []string{"async function() { %s }"},
			want:            "(async function() { console.log('hi') })",
		},
		{
			name: "With automatic return",
			code: "findOne({id: 1})",
			want: "(function() { return findOne({id: 1}); })",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetFunction(tt.code, tt.functionBuilder...); got != tt.want {
				t.Errorf("GetFunction() = %q, want %q", got, tt.want)
			}
		})
	}
}
