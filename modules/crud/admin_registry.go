package crud

import "sort"

// ─────────────────────────────────────────────────────────────────────────────
// Admin Registry — modular extension API
// ─────────────────────────────────────────────────────────────────────────────

// AdminPage represents a custom page that can be added to the admin UI.
type AdminPage struct {
	Path     string // URL path relative to /_admin (e.g. "/metrics")
	Title    string // Display title in the nav
	Icon     string // Icon name (lucide-style, e.g. "chart-bar")
	Template string // HTML template content (Mustache + JS). If empty, Handler is used.
	Order    int    // Sort order in the menu (lower = higher)
}

// AdminLink represents an external link added to the admin sidebar.
type AdminLink struct {
	Title    string // Display text
	Icon     string // Icon name
	URL      string // Full URL (opens in new tab)
	Order    int    // Sort order
	External bool   // true = opens in new tab
}

var (
	adminPages []AdminPage
	adminLinks []AdminLink
)

// ClearAdminRegistry resets all registered custom pages and links.
// Useful for hot-reloading configurations.
func ClearAdminRegistry() {
	adminPages = nil
	adminLinks = nil
}

// RegisterAdminPage adds a custom page to the admin interface.
// The page will appear in the sidebar and be accessible at /_admin{page.Path}.
func RegisterAdminPage(page AdminPage) {
	adminPages = append(adminPages, page)
}

// RegisterAdminLink adds an external link to the admin sidebar.
func RegisterAdminLink(link AdminLink) {
	link.External = true
	adminLinks = append(adminLinks, link)
}

// sortedAdminPages returns all registered pages sorted by Order.
func sortedAdminPages() []AdminPage {
	pages := make([]AdminPage, len(adminPages))
	copy(pages, adminPages)
	sort.Slice(pages, func(i, j int) bool { return pages[i].Order < pages[j].Order })
	return pages
}

// sortedAdminLinks returns all registered links sorted by Order.
func sortedAdminLinks() []AdminLink {
	links := make([]AdminLink, len(adminLinks))
	copy(links, adminLinks)
	sort.Slice(links, func(i, j int) bool { return links[i].Order < links[j].Order })
	return links
}

// menuEntry is passed to templates for nav rendering.
type menuEntry struct {
	Title    string `json:"title"`
	Path     string `json:"path"`
	Icon     string `json:"icon"`
	Active   bool   `json:"active"`
	External bool   `json:"external"`
}

// buildMenu builds the full sidebar menu for a given active path.
func buildMenu(prefix, activePath string) []menuEntry {
	entries := []menuEntry{
		{Title: "Dashboard", Path: prefix, Icon: "layout-dashboard", Active: activePath == "" || activePath == "/"},
		{Title: "Collections", Path: prefix + "/collections", Icon: "database", Active: activePath == "/collections"},
		{Title: "Namespaces", Path: prefix + "/namespaces", Icon: "folder", Active: activePath == "/namespaces"},
		{Title: "Users", Path: prefix + "/users", Icon: "users", Active: activePath == "/users"},
		{Title: "Roles", Path: prefix + "/roles", Icon: "shield", Active: activePath == "/roles"},
	}

	for _, p := range sortedAdminPages() {
		entries = append(entries, menuEntry{
			Title:  p.Title,
			Path:   prefix + p.Path,
			Icon:   p.Icon,
			Active: activePath == p.Path,
		})
	}

	for _, l := range sortedAdminLinks() {
		entries = append(entries, menuEntry{
			Title:    l.Title,
			Path:     l.URL,
			Icon:     l.Icon,
			External: true,
		})
	}

	return entries
}
