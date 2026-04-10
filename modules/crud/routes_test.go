package crud

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"http-server/plugins/httpserver"
	"http-server/types"

	_ "modernc.org/sqlite"
)

// Helper to quickly setup db
func setupRoutesDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.New(sqlite.Config{
		DriverName: "sqlite",
		DSN:        "file::memory:?cache=shared",
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	if err := Seed(db); err != nil {
		t.Fatalf("failed to seed: %v", err)
	}
	return db
}

func TestCRUDRoutes_Integration(t *testing.T) {
	db := setupRoutesDB(t)

	// In-memory HTTP Server
	cfg := httpserver.Config{
		AppName: "crud-test",
		Secret:  "supersecret",
	}
	app := httpserver.New(cfg)

	// Mount the CRUD routes onto the app
	err := mountRoutes(app, "/api", db, nil, "supersecret", "", &types.NoAuth{})
	if err != nil {
		t.Fatalf("mountRoutes failed: %v", err)
	}

	var ns Namespace
	db.Where("slug = ?", "global").First(&ns)
	rootUser := User{
		ID:           newID(),
		Username:     "root",
		Email:        "root@example.com",
		PasswordHash: bcryptHash("root"),
		NamespaceID:  &ns.ID,
		IsActive:     true,
	}
	db.Create(&rootUser)

	// Helper to send request and return response body as map
	callAPI := func(method, path string, body map[string]any, token string) (int, map[string]any) {
		var reqBody io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, reqBody)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test error: %v", err)
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		var res map[string]any
		json.Unmarshal(respBody, &res)
		return resp.StatusCode, res
	}

	// 0. Test Root Redirect to Admin
	reqRoot := httptest.NewRequest("GET", "/api", nil)
	respRoot, _ := app.Test(reqRoot)
	if respRoot.StatusCode != 303 {
		t.Errorf("expected 303 for root redirect, got %d", respRoot.StatusCode)
	}
	if respRoot.Header.Get("Location") != "/api/_admin" {
		t.Errorf("expected redirect to /api/_admin, got %s", respRoot.Header.Get("Location"))
	}

	reqRootSlash := httptest.NewRequest("GET", "/api/", nil)
	respRootSlash, _ := app.Test(reqRootSlash)
	if respRootSlash.StatusCode != 303 {
		t.Errorf("expected 303 for root slash redirect, got %d", respRootSlash.StatusCode)
	}
	if respRootSlash.Header.Get("Location") != "/api/_admin" {
		t.Errorf("expected redirect to /api/_admin, got %s", respRootSlash.Header.Get("Location"))
	}

	// 1. Auth: Login as root
	status, res := callAPI("POST", "/api/auth/login", map[string]any{
		"identity":  "root",
		"password":  "root",
		"namespace": "global",
	}, "")
	if status != 200 {
		t.Errorf("login failed: %v", res)
	}
	token, ok := res["token"].(string)
	if !ok || token == "" {
		t.Fatalf("missing token in login response")
	}

	// 2. Namespaces: create one
	status, res = callAPI("POST", "/api/namespaces", map[string]any{
		"name": "Test NS",
		"slug": "test_ns",
	}, token)
	if status != 201 {
		t.Errorf("namespace create failed: %v", res)
	}

	// 3. Schemas: create a schema in global (so root user has access)
	status, res = callAPI("POST", "/api/schemas", map[string]any{
		"name":         "Products",
		"slug":         "products",
		"namespace_id": ns.ID, // Use the global NS ID
		"soft_delete":  true,
		"fields": []map[string]any{
			{"name": "price", "type": "number"},
			{"name": "title", "type": "text"},
		},
	}, token)
	if status != 201 {
		t.Errorf("schema create failed: %v", res)
	}

	// 4. Documents: Create a product
	status, docRes := callAPI("POST", "/api/products", map[string]any{
		"data": map[string]any{
			"title": "Laptop",
			"price": 1200.50,
		},
		"meta": map[string]any{
			"version": 1,
		},
	}, token)
	if status != 201 {
		t.Errorf("document create failed: %v", docRes)
	}
	docID, _ := docRes["id"].(string)

	// 5. Documents: Read the product
	status, _ = callAPI("GET", "/api/products/"+docID, nil, token)
	if status != 200 {
		t.Errorf("document read failed: %d", status)
	}

	// 6. Documents: Update the product
	status, _ = callAPI("PUT", "/api/products/"+docID, map[string]any{
		"data": map[string]any{
			"price": 999.99,
		},
	}, token)
	if status != 200 {
		t.Errorf("document update failed: %d", status)
	}

	// 7. Documents: List products
	status, _ = callAPI("POST", "/api/products/query", map[string]any{
		"filter": map[string]any{"price": map[string]any{"$lt": 1000}},
	}, token)
	if status != 200 {
		t.Errorf("document query failed: %d", status)
	}

	req := httptest.NewRequest("GET", "/api/products", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Errorf("document list failed: %d", resp.StatusCode)
	}

	// 8. Documents: Delete product (Soft Delete)
	status, _ = callAPI("DELETE", "/api/products/"+docID, nil, token)
	if status != 200 {
		t.Errorf("document delete failed: %d", status)
	}

	// 9. Trash: Read trashed product
	reqTrash := httptest.NewRequest("GET", "/api/products/trash", nil)
	reqTrash.Header.Set("Authorization", "Bearer "+token)
	respTrash, _ := app.Test(reqTrash)
	if respTrash.StatusCode != 200 {
		t.Errorf("trash list failed: %d", respTrash.StatusCode)
	}

	// 10. Auth: Logout
	status, _ = callAPI("POST", "/api/auth/logout", nil, token)
	if status != 200 {
		t.Errorf("logout failed: %d", status)
	}

	// 11. Unauthorized access test
	status, _ = callAPI("GET", "/api/products/"+docID, nil, "invalid-token")
	if status != 401 && status != 403 {
		t.Errorf("expected 401 or 403 for invalid token, got %v", status)
	}
}
