package binder

import (
	"testing"

	"http-server/modules/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMQTTDirective_Storage(t *testing.T) {
	// Initialize a test DB connection
	dbConn, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	// Register the test connection as the default DB
	db.NewConnection(dbConn, "DEFAULT")

	t.Run("Without STORAGE directive", func(t *testing.T) {
		cfg := &DirectiveConfig{
			Name:    "MQTT",
			Address: ":21883",
		}
		
		dir, err := NewMQTTDirective(cfg)
		if err != nil {
			t.Fatalf("failed to parse config: %v", err)
		}
		
		listeners, err := dir.Start()
		if err != nil {
			t.Fatalf("failed to start directive: %v", err)
		}
		
		if dir.server.Server() == nil {
			t.Fatal("expected server to be initialized")
		}
		
		// Close to cleanup
		dir.Close()
		for _, l := range listeners {
			l.Close()
		}
	})

	t.Run("With STORAGE directive", func(t *testing.T) {
		cfg := &DirectiveConfig{
			Name:    "MQTT",
			Address: ":21883",
			Routes: []*RouteConfig{
				{
					Method: "STORAGE",
					Path:   "default",
				},
			},
		}
		
		dir, err := NewMQTTDirective(cfg)
		if err != nil {
			t.Fatalf("failed to parse config: %v", err)
		}
		
		listeners, err := dir.Start()
		if err != nil {
			t.Fatalf("failed to start directive: %v", err)
		}
		
		// Ensure it initialized
		if dir.server.Server() == nil {
			t.Fatal("expected server to be initialized")
		}
		
		// Close to cleanup
		dir.Close()
		for _, l := range listeners {
			l.Close()
		}
	})
}
