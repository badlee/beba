package httpserver

import (
	"bytes"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
)

// --------------------------------------------------------------------------
// Config Defaults & Construction
// --------------------------------------------------------------------------

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{}
	if cfg.AppName != "" {
		t.Errorf("expected empty AppName by default, got %q", cfg.AppName)
	}
	if cfg.ReadTimeout != 0 {
		t.Errorf("expected zero ReadTimeout by default, got %v", cfg.ReadTimeout)
	}
	if cfg.Secret != "" {
		t.Errorf("expected empty Secret by default, got %q", cfg.Secret)
	}
}

func TestConfig_FullConstruction(t *testing.T) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	cfg := Config{
		AppName:      "test-server",
		Stdout:       outBuf,
		Stderr:       errBuf,
		Fields:       []string{FieldIP, FieldStatus, FieldMethod, FieldURL, FieldLatency},
		Messages:     []string{"Server Error", "Client Error", "OK"},
		Levels:       []zerolog.Level{zerolog.ErrorLevel, zerolog.WarnLevel, zerolog.InfoLevel},
		Level:        zerolog.DebugLevel,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
		Secret:       "super-secret",
	}

	if cfg.AppName != "test-server" {
		t.Errorf("expected 'test-server', got %q", cfg.AppName)
	}
	if cfg.ReadTimeout != 30*time.Second {
		t.Errorf("expected 30s, got %v", cfg.ReadTimeout)
	}
	if len(cfg.Messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(cfg.Messages))
	}
	if cfg.Secret != "super-secret" {
		t.Errorf("expected 'super-secret', got %q", cfg.Secret)
	}
}

// --------------------------------------------------------------------------
// Field Constants
// --------------------------------------------------------------------------

func TestConfig_FieldConstants(t *testing.T) {
	// All known field constants must be non-empty and unique
	fields := []string{
		FieldReferer, FieldProtocol, FieldPID, FieldPort, FieldIP, FieldIPs,
		FieldHost, FieldPath, FieldURL, FieldUserAgent, FieldLatency, FieldStatus,
		FieldBody, FieldResBody, FieldQueryParams, FieldBytesReceived, FieldBytesSent,
		FieldRoute, FieldMethod, FieldRequestID, FieldError, FieldReqHeaders, FieldResHeaders,
	}
	seen := make(map[string]bool)
	for _, f := range fields {
		if f == "" {
			t.Error("field constant should not be empty")
		}
		if seen[f] {
			t.Errorf("duplicate field constant: %q", f)
		}
		seen[f] = true
	}
}

// --------------------------------------------------------------------------
// Config Logger Routing
// --------------------------------------------------------------------------

func TestConfig_LoggerCtx_DefaultsToStdout(t *testing.T) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	cfg := Config{}
	cfg.stdoutLogger = zerolog.New(outBuf)
	cfg.stderrLogger = zerolog.New(errBuf)

	// Debug/Info/Trace should route to stdoutLogger
	for _, level := range []zerolog.Level{zerolog.DebugLevel, zerolog.InfoLevel, zerolog.TraceLevel} {
		l := cfg.loggerCtx(nil, level).Logger()
		l.Log().Msg("stdout test")
		if outBuf.Len() == 0 {
			t.Errorf("expected output for level %v, got nothing", level)
		}
		outBuf.Reset()
	}
}

func TestConfig_LoggerCtx_ErrorToStderr(t *testing.T) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	cfg := Config{}
	cfg.stdoutLogger = zerolog.New(outBuf)
	cfg.stderrLogger = zerolog.New(errBuf)

	// Error/Warn should route to stderrLogger
	for _, level := range []zerolog.Level{zerolog.WarnLevel, zerolog.ErrorLevel} {
		l := cfg.loggerCtx(nil, level).Logger()
		l.Log().Msg("stderr test")
		if errBuf.Len() == 0 {
			t.Errorf("expected error output for level %v, got nothing", level)
		}
		errBuf.Reset()
	}
}

func TestConfig_LoggerCtx_CustomGetLogger(t *testing.T) {
	called := false
	cfg := Config{
		GetLogger: func(c fiber.Ctx, level zerolog.Level) zerolog.Logger {
			called = true
			return zerolog.Nop()
		},
	}

	cfg.loggerCtx(nil, zerolog.InfoLevel)
	if !called {
		t.Error("expected GetLogger to be called")
	}
}

// --------------------------------------------------------------------------
// Config Field Assignment & Callbacks
// --------------------------------------------------------------------------

func TestConfig_WrapHeaders(t *testing.T) {
	cfg := Config{WrapHeaders: true, FieldsSnakeCase: true}
	if !cfg.WrapHeaders {
		t.Error("WrapHeaders should be true")
	}
	if !cfg.FieldsSnakeCase {
		t.Error("FieldsSnakeCase should be true")
	}
}

func TestConfig_SkipFieldCallback(t *testing.T) {
	skipped := false
	cfg := Config{
		SkipField: func(field string, c fiber.Ctx) bool {
			if field == "secret_field" {
				skipped = true
				return true
			}
			return false
		},
	}
	result := cfg.SkipField("secret_field", nil)
	if !result || !skipped {
		t.Error("SkipField callback did not work as expected")
	}
}

func TestConfig_Timeouts(t *testing.T) {
	cfg := Config{
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}
	if cfg.ReadTimeout != 30*time.Second {
		t.Errorf("expected 30s, got %v", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 0 {
		t.Errorf("expected 0 (unlimited), got %v", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 120*time.Second {
		t.Errorf("expected 120s, got %v", cfg.IdleTimeout)
	}
}
