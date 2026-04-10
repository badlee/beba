package binder

import (
	"strings"
	"testing"
)

func TestMiddlewareSyntax(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name: "Named single-line",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE @HELMET @CSRF
END HTTP`,
			wantErr: false,
		},
		{
			name: "JS file path",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE handlers/auth.js
END HTTP`,
			wantErr: false,
		},
		{
			name: "JS inline block",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE BEGIN
		print("hello");
	END MIDDLEWARE
END HTTP`,
			wantErr: false,
		},
		{
			name: "Named + Block (FAIL)",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE @HELMET BEGIN
		print("illegal");
	END MIDDLEWARE
END HTTP`,
			wantErr: true,
		},
		{
			name: "JS file path with priority",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE handlers/auth.js [priority=10]
END HTTP`,
			wantErr: false,
		},
		{
			name: "JS inline block with priority",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE BEGIN [priority=5]
		print("hello");
	END MIDDLEWARE
END HTTP`,
			wantErr: false,
		},
		{
			name: "Args before file (FAIL)",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE [priority=10] handlers/auth.js
END HTTP`,
			wantErr: true,
		},
		{
			name: "Args before BEGIN (FAIL)",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE [priority=10] BEGIN
		print("hello");
	END MIDDLEWARE
END HTTP`,
			wantErr: true,
		},
		{
			name: "Named single-line with priority",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE @HELMET @CSRF [priority=20]
END HTTP`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParseConfig(tt.input, "test.bind")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil {
				msg := err.Error()
				if !strings.Contains(msg, "cannot have BEGIN/END blocks") &&
					!strings.Contains(msg, "arguments must come after") {
					t.Errorf("Expected specific error message, got: %v", err)
				}
			}
		})
	}
}
