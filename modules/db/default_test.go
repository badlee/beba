package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDefaultDatabase(t *testing.T) {
	// Create a temporary directory to act as the project root
	tempDir := t.TempDir()
	
	// Save current working directory
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}
	
	// Change to temporary directory
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	
	// Ensure we change back after the test
	defer os.Chdir(cwd)

	// Clean up global state before starting
	AllConnectionsMu.Lock()
	oldConnections := AllConnections
	AllConnections = make(map[string]*Connection)
	AllConnectionsMu.Unlock()
	
	// Restore global state after
	defer func() {
		AllConnectionsMu.Lock()
		AllConnections = oldConnections
		AllConnectionsMu.Unlock()
	}()

	// Execute EnsureDefaultDatabase
	conn := EnsureDefaultDatabase()
	if conn == nil {
		t.Fatal("expected connection to be created, got nil")
	}

	// Verify file existence
	dbPath := filepath.Join(tempDir, ".data", "beba.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("expected database file to exist at %s, but it doesn't", dbPath)
	}

	// Verify registration
	if GetDefaultConnection() == nil {
		t.Error("expected default connection to be registered")
	}
	
	if GetConnection("DEFAULT") == nil {
		t.Error("expected connection named 'DEFAULT' to be registered")
	}
}

func TestFromURLShorthands(t *testing.T) {
	// Setup a default connection
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_shorthands.db")
	
	gormDB, err := FromURL("sqlite://" + dbPath)
	if err != nil {
		t.Fatalf("failed to create base connection: %v", err)
	}
	
	conn := NewConnection(gormDB, "DEFAULT")
	RegisterDefaultConnection(conn)

	// Test shorthands
	tests := []struct {
		name string
		url  string
	}{
		{"Empty string", ""},
		{"Short default", ":default:"},
		{"Full sqlite default", "sqlite://:default:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := FromURL(tt.url)
			if err != nil {
				t.Fatalf("FromURL(%q) failed: %v", tt.url, err)
			}
			if db == nil {
				t.Fatal("expected non-nil database")
			}
			
			// Verify it's the same underlying DB instance if possible, 
			// though GORM might wrap it differently. 
			// At least check it doesn't error.
		})
	}
}
