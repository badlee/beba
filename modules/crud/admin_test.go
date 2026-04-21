package crud

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"beba/plugins/httpserver"

	"github.com/gofiber/fiber/v3"
)

func TestAdminRoutes_Integration(t *testing.T) {
	db := setupTestDB(t)

	// In module.go, bcryptHash is instantiated in init(), ensure it works
	if bcryptHash == nil {
		t.Skip("bcryptHash is not initialized in tests context")
	}

	// Create root user (NamespaceID is NULL by default in GORM if not set on pointer)
	rootUser := User{
		ID:           newID(),
		Username:     "rootadmin",
		PasswordHash: bcryptHash("admin123"),
		IsActive:     true,
	}
	if err := db.Create(&rootUser).Error; err != nil {
		t.Fatalf("failed to create root user: %v", err)
	}

	app := &httpserver.HTTP{
		App: fiber.New(),
	}
	secret := "test-admin-secret"

	// Register a custom admin page
	RegisterAdminPage(AdminPage{
		Path:     "/metrics",
		Title:    "System Metrics",
		Template: `<h1>Metrics View</h1>`,
	})

	// Mount admin routes
	mountAdmin(app, "/api", "/api", db, secret)

	// 1. Unauthenticated request to dashboard -> Redirect to login
	reqNoAuth := httptest.NewRequest("GET", "/api/_admin/", nil)
	respNoAuth, err := app.Test(reqNoAuth)
	if err != nil {
		t.Fatalf("failed request: %v", err)
	}
	if respNoAuth.StatusCode < 300 || respNoAuth.StatusCode >= 400 {
		t.Fatalf("expected redirect to login, got %d", respNoAuth.StatusCode)
	}
	if !strings.HasSuffix(respNoAuth.Header.Get("Location"), "/api/_admin/login") {
		t.Fatalf("expected redirect location /api/_admin/login, got %s", respNoAuth.Header.Get("Location"))
	}

	// 2. GET /login -> rendering login page
	reqLogin := httptest.NewRequest("GET", "/api/_admin/login", nil)
	respLogin, err := app.Test(reqLogin)
	if err != nil {
		t.Fatalf("failed request: %v", err)
	}
	if respLogin.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on login, got %d", respLogin.StatusCode)
	}
	bodyLogin, _ := io.ReadAll(respLogin.Body)
	if !strings.Contains(string(bodyLogin), "Login") {
		t.Fatalf("login page missing 'Login' text: %s", string(bodyLogin))
	}

	// 3. POST /login with valid credentials -> set Cookie and Redirect
	reqPostLogin := httptest.NewRequest("POST", "/api/_admin/login", strings.NewReader("username=rootadmin&password=admin123"))
	reqPostLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	respPostLogin, err := app.Test(reqPostLogin)
	if err != nil {
		t.Fatalf("failed request: %v", err)
	}
	if respPostLogin.StatusCode < 300 || respPostLogin.StatusCode >= 400 {
		t.Fatalf("expected redirect after login, got %d", respPostLogin.StatusCode)
	}

	cookies := respPostLogin.Cookies()
	var adminCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "_admin_token" {
			adminCookie = c
			break
		}
	}
	if adminCookie == nil || adminCookie.Value == "" {
		t.Fatalf("expected _admin_token cookie to be set")
	}

	// Helper to make authenticated requests
	doAuthReq := func(method, path string) *http.Response {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(adminCookie)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("failed req %s: %v", path, err)
		}
		return resp
	}

	// 4. GET Dashboard with Cookie
	respDash := doAuthReq("GET", "/api/_admin/")
	bodyDash, _ := io.ReadAll(respDash.Body)
	if respDash.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on dashboard, got %d, body: %s", respDash.StatusCode, string(bodyDash))
	}
	dashHTML := string(bodyDash)
	if !strings.Contains(dashHTML, "Dashboard") || !strings.Contains(dashHTML, "Collections") {
		t.Fatalf("dashboard missing expected content")
	}

	// 5. Custom Page route rendering
	respMetrics := doAuthReq("GET", "/api/_admin/metrics")
	bodyMetrics, _ := io.ReadAll(respMetrics.Body)
	if respMetrics.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on custom metrics page, got %d", respMetrics.StatusCode)
	}
	if !strings.Contains(string(bodyMetrics), "Metrics View") {
		t.Fatalf("metrics page missing expected content")
	}

	// 6. Test DataTables (namespaces, users, roles)
	for _, route := range []string{"/api/_admin/namespaces", "/api/_admin/users", "/api/_admin/roles"} {
		resp := doAuthReq("GET", route)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on %s, got %d", route, resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "CRUD Admin") {
			t.Fatalf("route %s did not render layout properly", route)
		}
	}

	// 7. SSE streaming tests cause fiber.App.Test to block or timeout,
	// so we skip testing the actual stream. The stream logic is covered by sse package tests.
}
