package config

import (
	"testing"
)

func TestParseFlagsString(t *testing.T) {
	cfg, err := parseFlagsArgs([]string{"--port", "9090"})
	if err != nil {
		t.Fatalf("parseFlagsArgs failed: %v", err)
	}
	if cfg.Port != 9090 {
		t.Errorf("Expected port 9090, got %d", cfg.Port)
	}
}

func TestParseFlagsShorthand(t *testing.T) {
	cfg, err := parseFlagsArgs([]string{"-p", "3000"})
	if err != nil {
		t.Fatalf("parseFlagsArgs failed: %v", err)
	}
	if cfg.Port != 3000 {
		t.Errorf("Expected port 3000, got %d", cfg.Port)
	}
}

func TestParseFlagsBool(t *testing.T) {
	cfg, err := parseFlagsArgs([]string{"--gzip"})
	if err != nil {
		t.Fatalf("parseFlagsArgs failed: %v", err)
	}
	if !cfg.Gzip {
		t.Error("Expected Gzip to be true")
	}
}

func TestParseFlagsAddress(t *testing.T) {
	cfg, err := parseFlagsArgs([]string{"--address", "192.168.1.1"})
	if err != nil {
		t.Fatalf("parseFlagsArgs failed: %v", err)
	}
	if cfg.Address != "192.168.1.1" {
		t.Errorf("Expected address '192.168.1.1', got %q", cfg.Address)
	}
}

func TestParseFlagsSlice(t *testing.T) {
	cfg, err := parseFlagsArgs([]string{"--bind", "a.bind", "--bind", "b.bind"})
	if err != nil {
		t.Fatalf("parseFlagsArgs failed: %v", err)
	}
	if len(cfg.BindFiles) != 2 {
		t.Fatalf("Expected 2 bind files, got %d", len(cfg.BindFiles))
	}
	if cfg.BindFiles[0] != "a.bind" || cfg.BindFiles[1] != "b.bind" {
		t.Errorf("Unexpected bind files: %v", cfg.BindFiles)
	}
}

func TestParseFlagsUnknown(t *testing.T) {
	_, err := parseFlagsArgs([]string{"--nonexistent-flag", "value"})
	if err == nil {
		t.Fatal("Expected error for unknown flag")
	}
}

func TestParseFlagsEmpty(t *testing.T) {
	cfg, err := parseFlagsArgs([]string{})
	if err != nil {
		t.Fatalf("parseFlagsArgs with empty args failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("Expected non-nil config")
	}
}

func TestParseFlagsMultipleBools(t *testing.T) {
	cfg, err := parseFlagsArgs([]string{"--gzip", "--cors", "--silent"})
	if err != nil {
		t.Fatalf("parseFlagsArgs failed: %v", err)
	}
	if !cfg.Gzip {
		t.Error("Expected Gzip true")
	}
	if !cfg.CORS {
		t.Error("Expected Cors true")
	}
	if !cfg.Silent {
		t.Error("Expected Silent true")
	}
}

func TestParseFlagsMixedTypes(t *testing.T) {
	cfg, err := parseFlagsArgs([]string{
		"--port", "4000",
		"--address", "0.0.0.0",
		"--gzip",
		"--silent",
	})
	if err != nil {
		t.Fatalf("parseFlagsArgs failed: %v", err)
	}
	if cfg.Port != 4000 {
		t.Errorf("Expected port 4000, got %d", cfg.Port)
	}
	if cfg.Address != "0.0.0.0" {
		t.Errorf("Expected address '0.0.0.0', got %q", cfg.Address)
	}
	if !cfg.Gzip || !cfg.Silent {
		t.Error("Expected Gzip and Silent true")
	}
}

func TestFlagNames(t *testing.T) {
	names := FlagNames()
	if len(names) == 0 {
		t.Fatal("Expected at least one flag name")
	}

	// Should include well-known flags
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	for _, expected := range []string{"port", "address", "gzip", "silent"} {
		if !found[expected] {
			t.Errorf("Expected flag %q in FlagNames()", expected)
		}
	}
}
