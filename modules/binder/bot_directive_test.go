package binder

import (
	"testing"
)

func TestBotDirectiveParsing(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name: "Basic @BOT",
			input: `HTTP 0.0.0.0:80
	GET @BOT "/test" TEXT "ok"
END HTTP`,
			wantErr: false,
		},
		{
			name: "@BOT with JS Challenge",
			input: `HTTP 0.0.0.0:80
	GET @BOT[js_challenge=true] "/test" TEXT "ok"
END HTTP`,
			wantErr: false,
		},
		{
			name: "@BOT with threshold and path",
			input: `HTTP 0.0.0.0:80
	GET @BOT[js_challenge=true threshold=40 path="/verify"] "/test" TEXT "ok"
END HTTP`,
			wantErr: false,
		},
		{
			name: "@BOT in MIDDLEWARE (Global)",
			input: `HTTP 0.0.0.0:80
	MIDDLEWARE @BOT[js_challenge=true]
	GET "/test" TEXT "ok"
END HTTP`,
			wantErr: false,
		},
		{
			name: "@BOT with invalid threshold (FAIL - but parser should handle it as string)",
			input: `HTTP 0.0.0.0:80
	GET @BOT[threshold=abc] "/test" TEXT "ok"
END HTTP`,
			wantErr: false, // The parser accepts key=value, validation happens at runtime/init
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParseConfig(tt.input, "test.bind")
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
