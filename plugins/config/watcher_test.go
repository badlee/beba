package config

import (
	"testing"
)

func TestHotReload_DynamicFields(t *testing.T) {
	initial := &AppConfig{
		Port:       8080,
		Address:    "127.0.0.1",
		Gzip:       true,
		DirListing: true,
	}

	// Mock loadFn that returns a modified config
	reloaded := &AppConfig{
		Port:       8080,
		Address:    "127.0.0.1",
		Gzip:       false, // Dynamic change
		DirListing: false, // Dynamic change
	}
	loadFn := func() (*AppConfig, error) {
		return reloaded, nil
	}

	w, err := NewWatcher(loadFn, initial)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer w.Close()

	// Capture changes
	var receivedChanges []FieldChange
	w.OnChange(func(next *AppConfig, changes []FieldChange) {
		receivedChanges = changes
	})

	// Trigger manual reload
	if err := w.reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	// Verify current config
	current := w.Config()
	if current.Gzip != false {
		t.Errorf("Expected Gzip to be false, got %v", current.Gzip)
	}
	if current.DirListing != false {
		t.Errorf("Expected DirListing to be false, got %v", current.DirListing)
	}

	// Verify changes list
	if len(receivedChanges) != 2 {
		t.Errorf("Expected 2 changes, got %d", len(receivedChanges))
	}
	for _, c := range receivedChanges {
		if c.Type != ChangeHotReload {
			t.Errorf("Expected ChangeHotReload for field %s, got %v", c.Field, c.Type)
		}
	}
}

func TestHotReload_StaticFields(t *testing.T) {
	initial := &AppConfig{
		Port:    8080,
		Address: "127.0.0.1",
		Cert:    "cert.pem",
		Gzip:    true,
	}

	// Mock loadFn: mix of static (should be ignored) and dynamic (should be applied)
	reloaded := &AppConfig{
		Port:    9000,        // Static change -> should be REVERTED to 8080
		Address: "127.0.0.1", // Unchanged
		Cert:    "new.pem",   // Static change -> should be REVERTED to cert.pem
		Gzip:    false,       // Dynamic change -> should be APPLIED
	}
	loadFn := func() (*AppConfig, error) {
		return reloaded, nil
	}

	w, err := NewWatcher(loadFn, initial)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer w.Close()

	var receivedChanges []FieldChange
	w.OnChange(func(next *AppConfig, changes []FieldChange) {
		receivedChanges = changes
	})

	if err := w.reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	current := w.Config()

	// Static fields MUST stay at initial values
	if current.Port != 8080 {
		t.Errorf("Expected Port to remain 8080, got %v", current.Port)
	}
	if current.Cert != "cert.pem" {
		t.Errorf("Expected Cert to remain cert.pem, got %v", current.Cert)
	}

	// Dynamic field MUST be updated
	if current.Gzip != false {
		t.Errorf("Expected Gzip to be updated to false, got %v", current.Gzip)
	}

	// Verify change metadata
	foundPort, foundCert, foundGzip := false, false, false
	for _, c := range receivedChanges {
		switch c.Field {
		case "Port":
			foundPort = true
			if c.Type != ChangeStatic {
				t.Errorf("Expected ChangeStatic for Port")
			}
		case "Cert":
			foundCert = true
			if c.Type != ChangeStatic {
				t.Errorf("Expected ChangeStatic for Cert")
			}
		case "Gzip":
			foundGzip = true
			if c.Type != ChangeHotReload {
				t.Errorf("Expected ChangeHotReload for Gzip")
			}
		}
	}

	if !foundPort || !foundCert || !foundGzip {
		t.Errorf("Missing expected change metadata (Port:%v, Cert:%v, Gzip:%v)", foundPort, foundCert, foundGzip)
	}
}

func TestDiffConfigs(t *testing.T) {
	c1 := &AppConfig{Port: 80, Address: "127.0.0.1", Gzip: true}
	c2 := &AppConfig{Port: 81, Address: "127.0.0.1", Gzip: false}

	changes := diffConfigs(c1, c2)
	if len(changes) != 2 {
		t.Errorf("Expected 2 changes, got %d", len(changes))
	}

	for _, c := range changes {
		switch c.Field {
		case "Port":
			if c.Type != ChangeStatic {
				t.Errorf("Port should be static")
			}
		case "Gzip":
			if c.Type != ChangeHotReload {
				t.Errorf("Gzip should be hot-reloadable")
			}
		}
	}
}
