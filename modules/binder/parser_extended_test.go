package binder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfig_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	includeFile := filepath.Join(tmpDir, "include.bind")
	os.WriteFile(includeFile, []byte("SET included_val \"true\""), 0644)

	mainContent := `
	HTTP :8080
		ENV SET test_env "abc"
		INCLUDE ` + includeFile + `
		ROUTE /test
			SET route_val "123"
		END ROUTE
	END HTTP

	PROTOCOL custom_proto [test=1]
		SET proto_val "456"
	END PROTOCOL
	`

	cfg, files, err := ParseConfig(mainContent, tmpDir)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	if len(files) < 1 {
		t.Errorf("Expected at least 1 file (include), got %d", len(files))
	}

	foundHttp := false
	for _, g := range cfg.Groups {
		if g.Directive == "HTTP" && g.Address == ":8080" {
			foundHttp = true
		}
	}
	if !foundHttp {
		t.Error("HTTP group not found")
	}

	foundCustom := false
	for _, reg := range cfg.Groups {
		if reg.Directive == "PROTOCOL" && reg.Address == "custom_proto" {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Error("Custom protocol registration not found")
	}
}

func TestParseConfig_EnvRemoving(t *testing.T) {
	content := `
	HTTP :8081
		ENV SET test_env "abc"
		REMOVE test_env
		ROUTE /
			SET val "ok"
		END ROUTE
	END HTTP
	`
	cfg, _, err := ParseConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	
	if len(cfg.Groups) == 0 {
		t.Fatal("no groups parsed")
	}
}

func TestParseRouteFromString_NotFound(t *testing.T) {
	content := `
	HTTP :8082
		# No routes here
	END HTTP
	`
	_, _, err := ParseRouteFromString(content)
	if err == nil {
		t.Error("Expected error for no route found, got nil")
	}
}

func TestParseConfig_Define(t *testing.T) {
	content := `
	HTTP :8083
		DEFINE my_group
			ROUTE /shared
				SET val "shared"
			END ROUTE
		END DEFINE
		ROUTE /a
			INCLUDE my_group
		END ROUTE
	END HTTP
	`
	cfg, _, err := ParseConfig(content)
	if err != nil {
		t.Fatalf("ParseConfig with DEFINE failed: %v", err)
	}
	
	if len(cfg.Groups) != 1 {
		t.Fatalf("Expected 1 HTTP group, got %d", len(cfg.Groups))
	}
}

