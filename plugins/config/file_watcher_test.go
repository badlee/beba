package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHotReload_FileFormats(t *testing.T) {
	formats := []struct {
		ext     string
		content func(silent bool, port int) string
	}{
		{
			ext: ".json",
			content: func(silent bool, port int) string {
				return fmt.Sprintf(`{"silent": %v, "port": %d}`, silent, port)
			},
		},
		{
			ext: ".yaml",
			content: func(silent bool, port int) string {
				return fmt.Sprintf("silent: %v\nport: %d\n", silent, port)
			},
		},
		{
			ext: ".toml",
			content: func(silent bool, port int) string {
				return fmt.Sprintf("silent = %v\nport = %d\n", silent, port)
			},
		},
	}

	for _, f := range formats {
		t.Run("Format_"+f.ext, func(t *testing.T) {
			tmpDir := t.TempDir()
			confPath := filepath.Join(tmpDir, "app"+f.ext)

			// Initial state: silent=false, port=8080
			err := os.WriteFile(confPath, []byte(f.content(false, 8080)), 0644)
			if err != nil {
				t.Fatalf("failed to write initial config: %v", err)
			}

			// Load initial
			initial, err := LoadConfigFile(confPath)
			if err != nil {
				t.Fatalf("failed to load initial config: %v", err)
			}
			fullInitial := DefaultConfig()
			MergeInto(fullInitial, initial)
			fullInitial.ConfigFiles = []string{confPath}
			fullInitial.Address = "127.0.0.1"

			loadFn := func() (*AppConfig, error) {
				cfg := DefaultConfig()
				cfg.Address = "127.0.0.1"
				cfg.ConfigFiles = []string{confPath} // Preserve path to avoid static warning
				c, err := LoadConfigFile(confPath)
				if err != nil {
					return nil, err
				}
				MergeInto(cfg, c)
				return cfg, nil
			}

			w, err := NewWatcher(loadFn, fullInitial)
			if err != nil {
				t.Fatalf("failed to create watcher: %v", err)
			}
			defer w.Close()

			err = w.Watch(confPath)
			if err != nil {
				t.Fatalf("failed to watch file: %v", err)
			}

			changeChan := make(chan []FieldChange, 1)
			w.OnChange(func(next *AppConfig, changes []FieldChange) {
				changeChan <- changes
			})

			// 1. Dynamic Change: Silent false -> true (non-zero value for merge)
			err = os.WriteFile(confPath, []byte(f.content(true, 8080)), 0644)
			if err != nil {
				t.Fatalf("failed to update config (dynamic): %v", err)
			}

			select {
			case changes := <-changeChan:
				found := false
				for _, c := range changes {
					if c.Field == "Silent" {
						found = true
						if c.Type != ChangeHotReload {
							t.Errorf("expected ChangeHotReload for Silent, got %v", c.Type)
						}
						if w.Config().Silent != true {
							t.Errorf("Config Silent should be true, got %v", w.Config().Silent)
						}
					}
				}
				if !found {
					t.Errorf("Change for Silent not found in OnChange for %s. Changes: %v", f.ext, changes)
				}
			case <-time.After(1 * time.Second):
				t.Fatalf("timed out waiting for dynamic change for %s", f.ext)
			}

			// 2. Static Change: Port 8080 -> 9000
			err = os.WriteFile(confPath, []byte(f.content(true, 9000)), 0644)
			if err != nil {
				t.Fatalf("failed to update config (static): %v", err)
			}

			select {
			case changes := <-changeChan:
				foundPort := false
				for _, c := range changes {
					if c.Field == "Port" {
						foundPort = true
						if c.Type != ChangeStatic {
							t.Errorf("expected ChangeStatic for Port, got %v", c.Type)
						}
						if w.Config().Port != 8080 {
							t.Errorf("Config Port should remain 8080, got %v", w.Config().Port)
						}
					}
				}
				if !foundPort {
					t.Errorf("Change for Port not found in OnChange for %s", f.ext)
				}
			case <-time.After(1 * time.Second):
				t.Fatalf("timed out waiting for static change for %s", f.ext)
			}
		})
	}
}

func TestHotReload_EnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	writeEnv := func(silent string, port string) {
		content := fmt.Sprintf("APP_SILENT=%s\nAPP_PORT=%s\n", silent, port)
		os.WriteFile(envPath, []byte(content), 0644)
	}

	writeEnv("false", "8080")

	LoadEnvFiles([]string{envPath})
	initial := DefaultConfig()
	initial.EnvFiles = []string{envPath}
	initial.Address = "127.0.0.1"

	loadFn := func() (*AppConfig, error) {
		cfg := DefaultConfig()
		cfg.Address = "127.0.0.1"
		cfg.EnvFiles = []string{envPath} // Preserve path
		LoadEnvFiles([]string{envPath})
		MergeInto(cfg, LoadEnv("APP_"))
		return cfg, nil
	}

	w, err := NewWatcher(loadFn, initial)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer w.Close()

	err = w.Watch(envPath)
	if err != nil {
		t.Fatalf("failed to watch .env: %v", err)
	}

	changeChan := make(chan []FieldChange, 1)
	w.OnChange(func(next *AppConfig, changes []FieldChange) {
		changeChan <- changes
	})

	// 1. Dynamic Change
	writeEnv("true", "8080")
	select {
	case changes := <-changeChan:
		found := false
		for _, c := range changes {
			if c.Field == "Silent" {
				found = true
				if w.Config().Silent != true {
					t.Errorf("Config Silent should be true, got %v", w.Config().Silent)
				}
			}
		}
		if !found {
			t.Error("Silent change not detected via .env")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for .env dynamic change")
	}

	// 2. Static Change
	writeEnv("true", "9000")
	select {
	case changes := <-changeChan:
		found := false
		for _, c := range changes {
			if c.Field == "Port" {
				found = true
				if w.Config().Port != 8080 {
					t.Errorf("Config Port should remain 8080, got %v", w.Config().Port)
				}
			}
		}
		if !found {
			t.Error("Port change not detected via .env")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for .env static change")
	}
}
