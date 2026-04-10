package crud

import (
	"testing"
)

func TestAdminRegistryPages(t *testing.T) {
	ClearAdminRegistry()
	defer ClearAdminRegistry()

	RegisterAdminPage(AdminPage{Path: "/metrics", Title: "Metrics", Icon: "chart-bar", Order: 2})
	RegisterAdminPage(AdminPage{Path: "/logs", Title: "Logs", Icon: "file-text", Order: 1})

	pages := sortedAdminPages()
	if len(pages) != 2 {
		t.Fatalf("Expected 2 pages, got %d", len(pages))
	}
	// Sorted by Order: Logs (1) then Metrics (2)
	if pages[0].Title != "Logs" {
		t.Errorf("Expected first page 'Logs', got %q", pages[0].Title)
	}
	if pages[1].Title != "Metrics" {
		t.Errorf("Expected second page 'Metrics', got %q", pages[1].Title)
	}
}

func TestAdminRegistryLinks(t *testing.T) {
	ClearAdminRegistry()
	defer ClearAdminRegistry()

	RegisterAdminLink(AdminLink{Title: "GitHub", Icon: "github", URL: "https://github.com", Order: 2})
	RegisterAdminLink(AdminLink{Title: "Docs", Icon: "book", URL: "https://docs.example.com", Order: 1})

	links := sortedAdminLinks()
	if len(links) != 2 {
		t.Fatalf("Expected 2 links, got %d", len(links))
	}
	if links[0].Title != "Docs" {
		t.Errorf("Expected first link 'Docs', got %q", links[0].Title)
	}
	if links[1].Title != "GitHub" {
		t.Errorf("Expected second link 'GitHub', got %q", links[1].Title)
	}
	// RegisterAdminLink should force External=true
	if !links[0].External || !links[1].External {
		t.Error("Expected all links to be External")
	}
}

func TestAdminRegistryClear(t *testing.T) {
	ClearAdminRegistry()

	RegisterAdminPage(AdminPage{Path: "/a", Title: "A"})
	RegisterAdminLink(AdminLink{Title: "B", URL: "http://b.com"})

	ClearAdminRegistry()

	if len(sortedAdminPages()) != 0 {
		t.Error("Expected 0 pages after clear")
	}
	if len(sortedAdminLinks()) != 0 {
		t.Error("Expected 0 links after clear")
	}
}

func TestBuildMenu(t *testing.T) {
	ClearAdminRegistry()
	defer ClearAdminRegistry()

	RegisterAdminPage(AdminPage{Path: "/metrics", Title: "Metrics", Icon: "chart-bar", Order: 1})
	RegisterAdminLink(AdminLink{Title: "GitHub", Icon: "github", URL: "https://github.com", Order: 1})

	menu := buildMenu("/_admin", "/collections")

	// Should have 5 built-in + 1 custom page + 1 link = 7
	if len(menu) != 7 {
		t.Fatalf("Expected 7 menu entries, got %d", len(menu))
	}

	// Check active state
	var collectionsActive bool
	var dashboardActive bool
	for _, e := range menu {
		if e.Title == "Collections" && e.Active {
			collectionsActive = true
		}
		if e.Title == "Dashboard" && e.Active {
			dashboardActive = true
		}
	}
	if !collectionsActive {
		t.Error("Expected Collections to be active")
	}
	if dashboardActive {
		t.Error("Expected Dashboard to NOT be active")
	}

	// Check Dashboard active when path is ""
	menuDash := buildMenu("/_admin", "")
	for _, e := range menuDash {
		if e.Title == "Dashboard" && !e.Active {
			t.Error("Expected Dashboard active when path is empty")
		}
	}

	// Check external link is present
	found := false
	for _, e := range menu {
		if e.Title == "GitHub" && e.External {
			found = true
		}
	}
	if !found {
		t.Error("Expected GitHub external link in menu")
	}
}
