package binder

import (
	"strings"
	"testing"
)

func TestCrudAdminParser(t *testing.T) {
	// 1. Setup a valid binder configuration
	dsl := `
CRUD 'postgres://user:pass@localhost/mydb' [default]
	NAME api
	AUTH USER root "pwd"

	ADMIN DEFINE
		PAGE "/metrics" [title="Vue Système" icon=bar-chart order=10] BEGIN
			<h1>Métriques</h1>
		END PAGE
		LINK "https://grafana.com" [title="Grafana Analytics" icon=monitoring order=20]
	END ADMIN

END CRUD
`
	// 2. Parse the DSL into RouteConfig using the root binder parser
	cfg, _, err := ParseConfig(dsl)
	if err != nil {
		t.Fatalf("Failed to parse CRUD DSL: %v", err)
	}

	if len(cfg.Groups) == 0 || len(cfg.Groups[0].Items) == 0 {
		t.Fatal("Expected at least 1 route block (CRUD)")
	}

	crudBlock := cfg.Groups[0].Items[0]

	// 3. Delegate to the crud protocol config parser
	crudCfg, err := parseCrudDirectiveConfig(crudBlock)
	if err != nil {
		t.Fatalf("parseCrudDirectiveConfig failed: %v", err)
	}

	// 4. Validate Admin structure
	if len(crudCfg.AdminPages) != 1 {
		t.Errorf("Expected 1 Admin Page, got %d", len(crudCfg.AdminPages))
	} else {
		page := crudCfg.AdminPages[0]
		if page.Path != "/metrics" {
			t.Errorf("Expected page path /metrics, got %s", page.Path)
		}
		if page.Title != "Vue Système" {
			t.Errorf("Expected page title 'Vue Système', got %s", page.Title)
		}
		if page.Icon != "bar-chart" {
			t.Errorf("Expected page icon 'bar-chart', got %s", page.Icon)
		}
		if page.Order != 10 {
			t.Errorf("Expected page order 10, got %d", page.Order)
		}
		if !strings.Contains(page.Template, "<h1>Métriques</h1>") {
			t.Errorf("Expected template to contain headers, got %s", page.Template)
		}
	}

	if len(crudCfg.AdminLinks) != 1 {
		t.Errorf("Expected 1 Admin Link, got %d", len(crudCfg.AdminLinks))
	} else {
		link := crudCfg.AdminLinks[0]
		if link.URL != "https://grafana.com" {
			t.Errorf("Expected link URL https://grafana.com, got %s", link.URL)
		}
		if link.Title != "Grafana Analytics" {
			t.Errorf("Expected link title 'Grafana Analytics', got %s", link.Title)
		}
		if link.Icon != "monitoring" {
			t.Errorf("Expected link icon 'monitoring', got %s", link.Icon)
		}
		if link.Order != 20 {
			t.Errorf("Expected link order 20, got %d", link.Order)
		}
	}
}
