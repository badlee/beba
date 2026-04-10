package binder

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"http-server/plugins/config"
)

// Helper parser pour http.bind (similaire à celui de mail_protocol_test.go)
func parseTestBind(t *testing.T, content string) *Config {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "http.bind")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(cfg.Groups) > 0 && len(cfg.Groups[0].Items) > 0 {
		cfg.Groups[0].Items[0].AppConfig = &config.AppConfig{
			SecretKey: "", // sera auto-généré par HTTPServer
		}
	}
	return cfg
}

func getHTTPDir(t *testing.T, cfg *Config) *HTTPDirective {
	t.Helper()
	if len(cfg.Groups) == 0 || len(cfg.Groups[0].Items) == 0 {
		t.Fatal("no directive group parsed")
	}
	return NewHTTPDirective(cfg.Groups[0].Items[0])
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestHTTPProtocol_Middlewares(t *testing.T) {
	cfg := parseTestBind(t, `
HTTP :8080 BEGIN
	GET /test @CORS(origins=*) @ETAG(weak=true) BEGIN
		return "ok"
	END GET
END HTTP
`)
	dir := getHTTPDir(t, cfg)
	app := dir.App.App

	req := httptest.NewRequest("GET", "/test", nil)
	// Trigger CORS explicitly via Origin
	req.Header.Set("Origin", "http://example.com")
	
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}

	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS middleware failed, got: %s", resp.Header.Get("Access-Control-Allow-Origin"))
	}
	
	if resp.Header.Get("Etag") == "" {
		t.Error("ETAG middleware failed, header is empty")
	}
}

func TestHTTPProtocol_Handlers(t *testing.T) {
	cfg := parseTestBind(t, `
HTTP :8080 BEGIN
	GET /json JSON BEGIN
		{"status": "ok"}
	END GET

	GET /text TEXT BEGIN
		plain text
	END GET

	ERROR 404 JSON BEGIN
		{"error": "not found"}
	END ERROR
END HTTP
`)
	dir := getHTTPDir(t, cfg)
	app := dir.App.App

	t.Run("JSON Handler", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/json", nil)
		resp, _ := app.Test(req)
		contentType := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(contentType, "application/json") {
			t.Errorf("expected json content type, got %s", contentType)
		}
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), `"status": "ok"`) {
			t.Errorf("unexpected body: %s", b)
		}
	})

	t.Run("TEXT Handler", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/text", nil)
		resp, _ := app.Test(req)
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), "plain text") {
			t.Errorf("expected short plain text, got %q", string(b))
		}
	})

	t.Run("IO Handler Validation", func(t *testing.T) {
		cfgIO := parseTestBind(t, `
HTTP :8081 BEGIN
	IO /socket.io
END HTTP
`)
		dirIO := getHTTPDir(t, cfgIO)
		appIO := dirIO.App.App

		req := httptest.NewRequest("GET", "/socket.io", nil)
		// A non-websocket request should return 426 Upgrade Required
		resp, _ := appIO.Test(req)
		if resp.StatusCode != 426 {
			t.Errorf("expected 426 Upgrade Required for HTTP to IO route, got %d", resp.StatusCode)
		}
		
		// Note: we don't fully test ws connection dialing through app.Test because fasthttp Hijack
		// doesn't work well completely purely in-memory via Test(). The io_test.go module
		// covers the live TCP port integration. Here we just assert the route compiled properly.
	})
}
