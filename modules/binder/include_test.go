package binder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInclude(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "binder-test-*")
	defer os.RemoveAll(tmpDir)

	// main.bind
	mainContent := `
HTTP 0.0.0.0:8080
    INCLUDE "routes.bind"
END HTTP
`
	mainPath := filepath.Join(tmpDir, "main.bind")
	os.WriteFile(mainPath, []byte(mainContent), 0644)

	// routes.bind
	routesContent := `
    GET / "Hello"
    END GET
    INCLUDE "sub/admin.bind"
`
	routesPath := filepath.Join(tmpDir, "routes.bind")
	os.WriteFile(routesPath, []byte(routesContent), 0644)

	// sub/admin.bind
	os.Mkdir(filepath.Join(tmpDir, "sub"), 0755)
	adminContent := `
    GET /admin "Admin Area"
    END GET
`
	adminPath := filepath.Join(tmpDir, "sub", "admin.bind")
	os.WriteFile(adminPath, []byte(adminContent), 0644)

	config, files, err := ParseFile(mainPath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}

	if len(config.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(config.Groups))
	}

	group := config.Groups[0]
	if group.Directive != "HTTP" {
		t.Errorf("expected HTTP directive, got %s", group.Directive)
	}

	if len(group.Items) == 0 {
		t.Fatalf("expected items in group, got 0")
	}

	item := group.Items[0]
	if len(item.Routes) != 2 {
		t.Errorf("expected 2 routes, got %d", len(item.Routes))
	}

	if item.Routes[0].Path != "/" {
		t.Errorf("expected route /, got %s", item.Routes[0].Path)
	}
	if item.Routes[1].Path != "/admin" {
		t.Errorf("expected route /admin, got %s", item.Routes[1].Path)
	}
}

func TestCircularInclude(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "binder-circular-*")
	defer os.RemoveAll(tmpDir)

	// a.bind -> INCLUDE b.bind
	os.WriteFile(filepath.Join(tmpDir, "a.bind"), []byte(`INCLUDE "b.bind"`), 0644)
	// b.bind -> INCLUDE a.bind
	os.WriteFile(filepath.Join(tmpDir, "b.bind"), []byte(`INCLUDE "a.bind"`), 0644)

	_, _, err := ParseFile(filepath.Join(tmpDir, "a.bind"))
	if err == nil {
		t.Fatal("expected error for circular include, got nil")
	}
	if !strings.Contains(err.Error(), "circular include") {
		t.Errorf("expected circular include error, got: %v", err)
	}
}

func TestInvalidInclude(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "binder-invalid-*")
	defer os.RemoveAll(tmpDir)

	// main.bind -> INCLUDE missing.bind
	os.WriteFile(filepath.Join(tmpDir, "main.bind"), []byte(`INCLUDE "missing.bind"`), 0644)

	_, _, err := ParseFile(filepath.Join(tmpDir, "main.bind"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}

	// main2.bind -> INCLUDE dir/
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "main2.bind"), []byte(`INCLUDE "subdir"`), 0644)

	_, _, err = ParseFile(filepath.Join(tmpDir, "main2.bind"))
	if err == nil {
		t.Fatal("expected error for directory include, got nil")
	}
}
