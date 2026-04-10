package httpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestBotMiddleware(t *testing.T) {
	app := fiber.New()
	cfg := &BotConfig{
		Enabled:         true,
		BlockCommonBots: true,
		JSChallenge:     true,
		ChallengeSecret: "test-secret",
		ScoreThreshold:  50,
	}

	app.Use(BotMiddleware(cfg))
	app.Get("/", func(c fiber.Ctx) error {
		return c.SendString("Success")
	})

	// 1. Common Bot (curl) -> Blocked
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "curl/7.64.1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("Expected 403 for curl bot, got %d", resp.StatusCode)
	}

	// 2. Suspicious (No UA) -> Redirect to Challenge
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusSeeOther && resp.StatusCode != fiber.StatusFound {
		t.Errorf("Expected 302/303 redirect for suspicious request, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/_waf/challenge") {
		t.Errorf("Expected redirect to /_waf/challenge, got %s", loc)
	}

	// 3. Real Browser (Safari) -> Pass
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Version/14.0.3 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("Expected 200 for real browser, got %d", resp.StatusCode)
	}
}

func TestBotChallengeFlow(t *testing.T) {
	app := fiber.New()
	cfg := &BotConfig{
		Enabled:         true,
		JSChallenge:     true,
		ChallengeSecret: "test-secret",
	}

	app.All("/_waf/challenge", BotChallengeHandler(cfg))
	app.Get("/target", func(c fiber.Ctx) error {
		return c.SendString("Target Reached")
	})

	// 1. Get Challenge Page
	req := httptest.NewRequest("GET", "/_waf/challenge?next=/target", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("Expected 200 for challenge page, got %d", resp.StatusCode)
	}

	// 2. Post Correct Solution -> Issue Cookie & Redirect
	nonce := "123456"
	solution := validatePoW_Helper(nonce, "test-secret")
	
	req = httptest.NewRequest("POST", "/_waf/challenge?next=/target", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	
	form := "nonce=" + nonce + "&solution=" + solution
	req.Body = io.NopCloser(strings.NewReader(form))
	req.ContentLength = int64(len(form))
	
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("Test failed: %v", err) // Let's see why it's failing
	}
	if resp == nil {
		t.Fatal("Response is nil")
	}

	if resp.StatusCode != fiber.StatusSeeOther && resp.StatusCode != fiber.StatusFound {
		t.Errorf("Expected 302/303 redirect after solving challenge, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/target" {
		t.Errorf("Expected redirect to /target, got %s", loc)
	}
	
	cookies := resp.Header.Values("Set-Cookie")
	found := false
	for _, c := range cookies {
		if strings.Contains(c, "waf_bot_auth") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Cookie 'waf_bot_auth' not found in response")
	}
}

func validatePoW_Helper(nonce, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(nonce))
	return hex.EncodeToString(h.Sum(nil))
}
