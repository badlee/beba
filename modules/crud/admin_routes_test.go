package crud

import (
	"http-server/plugins/httpserver"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAdminTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Namespace{}, &User{}, &Role{}, &CrudSchema{}, &CrudDocument{}, &Session{}, &OAuthState{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAdminRoutes_LoginRendering(t *testing.T) {
	db := setupAdminTestDB(t)
	app := fiber.New()
	prefix := "/api/crud"
	
	// We need a dummy httpserver.HTTP instance or just fiber.App for testing
	// but mountAdmin takes *httpserver.HTTP.
	// Since httpserver.HTTP is a wrapper around fiber.App, we can use it.
	h := &httpserver.HTTP{App: app}
	mountAdmin(h, prefix, db, "secret")

	// 1. Test GET /login rendering
	req := httptest.NewRequest("GET", prefix+"/_admin/login", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestAdminRoutes_ProtectedRedirect(t *testing.T) {
	db := setupAdminTestDB(t)
	app := fiber.New()
	prefix := "/api/crud"
	h := &httpserver.HTTP{App: app}
	mountAdmin(h, prefix, db, "secret")

	// 1. Test GET / dashboard redirects to /login if not authenticated
	req := httptest.NewRequest("GET", prefix+"/_admin/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusSeeOther { // 303 in Fiber v3
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	
	location := resp.Header.Get("Location")
	if location != prefix+"/_admin/login" {
		t.Errorf("Location = %q, want %q", location, prefix+"/_admin/login")
	}
}

func TestAdminRoutes_DashboardWithAuth(t *testing.T) {
	db := setupAdminTestDB(t)
	app := fiber.New()
	prefix := "/api/crud"
	h := &httpserver.HTTP{App: app}
	secret := "secret"
	mountAdmin(h, prefix, db, secret)

	// Create root user
	u := &User{ID: "root", Username: "admin", IsActive: true}
	db.Create(u)
	sess, _ := createSession(db, u, "")
	token, _ := signJWT(sess, u, secret)

	// 2. Test GET / dashboard with cookie
	req := httptest.NewRequest("GET", prefix+"/_admin/", nil)
	req.AddCookie(&http.Cookie{Name: "_admin_token", Value: token})
	
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
