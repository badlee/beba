package httpserver

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestWAFHooks_Execution(t *testing.T) {
	cfg := &WAFConfig{
		Enabled: true,
		Engine:  "On",
		Rules: []string{
			"SecRule ARGS:block \"@contains deny\" \"id:1,phase:2,deny,status:403\"",
		},
		Hooks: map[string]WAFHook{
			"CONNECTION": {
				Event:   "CONNECTION",
				Inline:  true,
				Handler: `context.Set("X-Hook-Connection", "true");`,
			},
			"URI": {
				Event:   "URI",
				Inline:  true,
				Handler: `context.Set("X-Hook-URI", "true");`,
			},
			"REQUEST_HEADERS": {
				Event:   "REQUEST_HEADERS",
				Inline:  true,
				Handler: `context.Set("X-Hook-Req-Headers", "true");`,
			},
			"REQUEST_BODY": {
				Event:   "REQUEST_BODY",
				Inline:  true,
				Handler: `context.Set("X-Hook-Req-Body", "true");`,
			},
			"INTERRUPTED": {
				Event:   "INTERRUPTED",
				Inline:  true,
				Handler: `context.Set("X-Hook-Interrupted", "true");`,
			},
			"RESPONSE_HEADERS": {
				Event:   "RESPONSE_HEADERS",
				Inline:  true,
				Handler: `context.Set("X-Hook-Res-Headers", "true");`,
			},
		},
	}

	app := fiber.New()
	app.Use(WAFMiddleware(cfg))
	
	app.Get("/test", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	t.Run("Normal Request Lifecycle", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		
		headers := []string{"X-Hook-Connection", "X-Hook-URI", "X-Hook-Req-Headers", "X-Hook-Res-Headers"}
		for _, h := range headers {
			if resp.Header.Get(h) != "true" {
				t.Errorf("Hook for %s not triggered", h)
			}
		}
	})

	t.Run("Blocked Request Interruption", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?block=deny", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		
		if resp.StatusCode != 403 {
			t.Errorf("Expected status 403, got %d", resp.StatusCode)
		}
		if resp.Header.Get("X-Hook-Interrupted") != "true" {
			t.Errorf("INTERRUPTED hook not triggered")
		}
	})
}
