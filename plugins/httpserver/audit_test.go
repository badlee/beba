package httpserver

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestSecurityLogger_Signed(t *testing.T) {
	path := "test_audit.log"
	secret := "test-secret"
	os.Remove(path)
	defer os.Remove(path)

	cfg := &AuditConfig{
		Enabled: true,
		Path:    path,
		Signed:  true,
	}
	
	// Reset singleton for test
	secLogOnce = sync.Once{}
	InitSecurityLogger(cfg, secret)

	app := fiber.New()
	app.Get("/test", func(c fiber.Ctx) error {
		GlobalSecLogger.RecordEvent(c, "test_event", map[string]any{"msg": "hello"})
		return c.SendString("ok")
	})

	// First event
	req := httptest.NewRequest("GET", "/test", nil)
	app.Test(req)

	// Second event (to test chaining)
	app.Get("/test2", func(c fiber.Ctx) error {
		GlobalSecLogger.RecordEvent(c, "test_event_2", map[string]any{"msg": "world"})
		return c.SendString("ok")
	})
	req2 := httptest.NewRequest("GET", "/test2", nil)
	app.Test(req2)

	// Verify file content and signature
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Failed to open audit log: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lastHash := ""

	// Line 1
	if scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "|")
		if len(parts) != 2 {
			t.Errorf("Line 1: Expected signed line with |, got %s", line)
		}
		
		content := parts[0]
		signature := parts[1]
		
		// Verify HMAC
		h := hmac.New(sha256.New, []byte(secret))
		h.Write([]byte(lastHash + content))
		expected := hex.EncodeToString(h.Sum(nil))
		
		if signature != expected {
			t.Errorf("Line 1: Signature mismatch! Got %s, want %s", signature, expected)
		}
		lastHash = signature
	}

	// Line 2
	if scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "|")
		if len(parts) != 2 {
			t.Errorf("Line 2: Expected signed line with |, got %s", line)
		}
		
		content := parts[0]
		signature := parts[1]
		
		// Verify HMAC
		h := hmac.New(sha256.New, []byte(secret))
		h.Write([]byte(lastHash + content))
		expected := hex.EncodeToString(h.Sum(nil))
		
		if signature != expected {
			t.Errorf("Line 2: Signature mismatch! Got %s, want %s", signature, expected)
		}
	}
}
