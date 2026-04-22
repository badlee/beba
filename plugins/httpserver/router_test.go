package httpserver

// fsrouter_test.go — Tests du routeur file-based FsRouter.
//
// Chaque test crée un répertoire temporaire via t.TempDir(), construit
// une arborescence de fichiers, puis envoie des requêtes via fiber.App.Test().
//
// Helpers :
//   writeFile(t, dir, relPath, content) — crée un fichier (répertoires inclus)
//   newRouter(t, root, ...opts)         — app Fiber + FsRouter prêt à tester
//   do(t, app, method, path)            — (statusCode, body)

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"beba/plugins/config"

	"github.com/gofiber/fiber/v3"
)

// ==================== HELPERS ====================

// writeFile crée dir/relPath avec le contenu donné (répertoires intermédiaires inclus).
func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("writeFile mkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
}

// newRouter construit un fiber.App avec FsRouter + AppConfig minimal.
// opts permet de surcharger la RouterConfig avant création.
func newRouter(t *testing.T, root string, opts ...func(*RouterConfig)) *fiber.App {
	t.Helper()
	app := fiber.New()
	cfg := RouterConfig{
		Root:        root,
		TemplateExt: ".html",
		IndexFile:   "index",
		AppConfig:   &config.AppConfig{NoHtmx: true},
	}
	for _, fn := range opts {
		fn(&cfg)
	}

	h, err := FsRouter(cfg)
	if err != nil {
		t.Fatalf("FsRouter failed: %v", err)
	}
	app.Use(h)
	return app
}

// do envoie une requête sur l'app et retourne (statusCode, bodyTrimmed).
func do(t *testing.T, app *fiber.App, method, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: -1})
	if err != nil {
		t.Fatalf("app.Test %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

// ==================== ROUTES STATIQUES ====================

func TestFsRouter_IndexHTML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", `Hello Root`)

	code, body := do(t, newRouter(t, dir), "GET", "/")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "Hello Root") {
		t.Errorf("expected 'Hello Root', got %q", body)
	}
}

func TestFsRouter_StaticPage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "about.html", `About Page`)

	code, body := do(t, newRouter(t, dir), "GET", "/about")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "About Page") {
		t.Errorf("expected 'About Page', got %q", body)
	}
}

func TestFsRouter_NestedIndex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "blog/index.html", `Blog Home`)

	code, body := do(t, newRouter(t, dir), "GET", "/blog")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "Blog Home") {
		t.Errorf("expected 'Blog Home', got %q", body)
	}
}

func TestFsRouter_UnknownPage_404(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", `Home`)

	code, _ := do(t, newRouter(t, dir), "GET", "/does-not-exist")

	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestFsRouter_TrailingSlash_Normalized(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "about.html", `About`)

	// Par défaut StrictSlash=false → /about/ == /about
	code, _ := do(t, newRouter(t, dir), "GET", "/about/")

	if code != http.StatusOK {
		t.Errorf("expected 200 for /about/ (non-strict), got %d", code)
	}
}

func TestFsRouter_StrictSlash_404(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "about.html", `About`)

	code, _ := do(t, newRouter(t, dir, func(c *RouterConfig) {
		c.StrictSlash = true
	}), "GET", "/about/")

	if code != http.StatusNotFound {
		t.Errorf("expected 404 in strict mode for /about/, got %d", code)
	}
}

// ==================== ROUTES DYNAMIQUES ====================

func TestFsRouter_DynamicParam_HTML(t *testing.T) {
	dir := t.TempDir()
	// params est injecté dans le VM avant rendu du template
	writeFile(t, dir, "blog/[slug].html", `<?js var s = params ? params.slug : ""; ?> Slug: {{s}}`)

	code, body := do(t, newRouter(t, dir), "GET", "/blog/hello-world")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "hello-world") {
		t.Errorf("expected slug in body, got %q", body)
	}
}

func TestFsRouter_DynamicParam_JS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "users/[id].js", `context.SendString("user:" + params.id);`)

	code, body := do(t, newRouter(t, dir), "GET", "/users/42")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "user:42") {
		t.Errorf("expected 'user:42', got %q", body)
	}
}

func TestFsRouter_CatchAll(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "files/[...rest].js", `context.SendString("rest:" + (catchall || ""));`)

	code, body := do(t, newRouter(t, dir), "GET", "/files/a/b/c")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "a/b/c") {
		t.Errorf("expected catch-all path in body, got %q", body)
	}
}

func TestFsRouter_MultipleParams(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orgs/[org]/repos/[repo].js",
		`context.SendString(params.org + "/" + params.repo);`)

	code, body := do(t, newRouter(t, dir), "GET", "/orgs/acme/repos/widget")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "acme/widget") {
		t.Errorf("expected 'acme/widget', got %q", body)
	}
}

// ==================== PRIORITÉ DE ROUTING ====================

func TestFsRouter_Priority_StaticBeforeDynamic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "blog/new.html", `Static New`)
	writeFile(t, dir, "blog/[slug].html", `Dynamic {{s}}`)

	app := newRouter(t, dir)

	// /blog/new → statique gagne
	_, body := do(t, app, "GET", "/blog/new")
	if !strings.Contains(body, "Static New") {
		t.Errorf("static route should win over dynamic, got %q", body)
	}

	// /blog/other → dynamique
	code2, body2 := do(t, app, "GET", "/blog/other")
	if code2 != http.StatusOK {
		t.Errorf("dynamic route should match, got %d", code2)
	}
	_ = body2 // template non rendu complètement sans params exposés, mais le status suffit
}

func TestFsRouter_Priority_DynamicBeforeCatchAll(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "api/[id].js", `context.SendString("id:" + params.id);`)
	writeFile(t, dir, "api/[...rest].js", `context.SendString("catch:" + (catchall||""));`)

	app := newRouter(t, dir)

	// /api/123 → un seul segment → [id].js gagne sur [...rest].js
	_, body := do(t, app, "GET", "/api/123")
	if !strings.Contains(body, "id:123") {
		t.Errorf("dynamic [id] should win over catch-all, got %q", body)
	}

	// /api/a/b → deux segments → seul le catch-all matche
	_, body2 := do(t, app, "GET", "/api/a/b")
	if !strings.Contains(body2, "a/b") {
		t.Errorf("catch-all should match multi-segment, got %q", body2)
	}
}

// ==================== MÉTHODES HTTP (suffixe nom de fichier) ====================

func TestFsRouter_MethodSuffix_GET_POST(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "users.GET.js", `context.SendString("list");`)
	writeFile(t, dir, "users.POST.js", `context.SendString("created");`)

	app := newRouter(t, dir)

	_, bodyGet := do(t, app, "GET", "/users")
	if !strings.Contains(bodyGet, "list") {
		t.Errorf("GET /users: expected 'list', got %q", bodyGet)
	}

	_, bodyPost := do(t, app, "POST", "/users")
	if !strings.Contains(bodyPost, "created") {
		t.Errorf("POST /users: expected 'created', got %q", bodyPost)
	}
}

func TestFsRouter_MethodSuffix_DELETE(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "items/[id].DELETE.js", `context.SendString("deleted:" + params.id);`)

	code, body := do(t, newRouter(t, dir), "DELETE", "/items/7")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "deleted:7") {
		t.Errorf("expected 'deleted:7', got %q", body)
	}
}

func TestFsRouter_MethodSuffix_WrongMethod_405(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "items.GET.js", `context.SendString("ok");`)

	// POST sur une route qui n'existe qu'en GET → 405
	code, _ := do(t, newRouter(t, dir), "POST", "/items")
	if code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for wrong method, got %d", code)
	}
}

// ==================== MODULE.EXPORTS ====================

func TestFsRouter_Export_GET(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "products.js", `
module.exports = {
	GET: function(ctx) {
		ctx.SendString("products list");
	},
};
`)
	code, body := do(t, newRouter(t, dir), "GET", "/products")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "products list") {
		t.Errorf("expected 'products list', got %q", body)
	}
}

func TestFsRouter_Export_MultiMethod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cart.js", `
module.exports = {
	GET:    function(ctx) { ctx.SendString("get cart");   },
	POST:   function(ctx) { ctx.SendString("add to cart"); },
	DELETE: function(ctx) { ctx.SendString("clear cart"); },
};
`)
	app := newRouter(t, dir)

	cases := []struct{ method, want string }{
		{"GET", "get cart"},
		{"POST", "add to cart"},
		{"DELETE", "clear cart"},
	}
	for _, tc := range cases {
		code, body := do(t, app, tc.method, "/cart")
		if code != http.StatusOK {
			t.Errorf("%s /cart: expected 200, got %d", tc.method, code)
		}
		if !strings.Contains(body, tc.want) {
			t.Errorf("%s /cart: expected %q, got %q", tc.method, tc.want, body)
		}
	}
}

func TestFsRouter_Export_ANY_FallbackMethod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ping.js", `
module.exports = {
	GET: function(ctx) { ctx.SendString("pong GET"); },
	ANY: function(ctx) { ctx.SendString("pong ANY"); },
};
`)
	app := newRouter(t, dir)

	// GET → clé spécifique
	_, bodyGet := do(t, app, "GET", "/ping")
	if !strings.Contains(bodyGet, "pong GET") {
		t.Errorf("GET: expected 'pong GET', got %q", bodyGet)
	}

	// POST → fallback sur ANY
	code, bodyPost := do(t, app, "POST", "/ping")
	if code != http.StatusOK {
		t.Errorf("POST: expected 200 via ANY, got %d", code)
	}
	if !strings.Contains(bodyPost, "pong ANY") {
		t.Errorf("POST: expected 'pong ANY', got %q", bodyPost)
	}

	// PUT → fallback sur ANY
	_, bodyPut := do(t, app, "PUT", "/ping")
	if !strings.Contains(bodyPut, "pong ANY") {
		t.Errorf("PUT: expected 'pong ANY', got %q", bodyPut)
	}
}

func TestFsRouter_Export_MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "strict.js", `
module.exports = {
	GET: function(ctx) { ctx.SendString("ok"); },
};
`)
	// DELETE n'est ni défini ni couvert par ANY → 405
	code, _ := do(t, newRouter(t, dir), "DELETE", "/strict")
	if code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", code)
	}
}

func TestFsRouter_Export_WithDynamicParams(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders/[id].js", `
module.exports = {
	GET: function(ctx, p) {
		ctx.SendString("order:" + p.id);
	},
	DELETE: function(ctx, p) {
		ctx.SendString("deleted:" + p.id);
	},
};
`)
	app := newRouter(t, dir)

	_, bodyGet := do(t, app, "GET", "/orders/99")
	if !strings.Contains(bodyGet, "order:99") {
		t.Errorf("GET /orders/99: expected 'order:99', got %q", bodyGet)
	}

	_, bodyDel := do(t, app, "DELETE", "/orders/99")
	if !strings.Contains(bodyDel, "deleted:99") {
		t.Errorf("DELETE /orders/99: expected 'deleted:99', got %q", bodyDel)
	}
}

func TestFsRouter_Export_ReturnValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "echo.js", `
module.exports = {
	GET: function() {
		return "return value response";
	},
};
`)
	code, body := do(t, newRouter(t, dir), "GET", "/echo")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "return value response") {
		t.Errorf("expected return value in body, got %q", body)
	}
}

func TestFsRouter_Export_ThrowError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "protected.js", `
module.exports = {
	GET: function(ctx) {
		throwError(401, "Unauthorized");
	},
};
`)
	code, _ := do(t, newRouter(t, dir), "GET", "/protected")

	if code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", code)
	}
}

func TestFsRouter_Export_PrintBuffer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "greet.js", `
module.exports = {
	GET: function(ctx) {
		print("Hello from print()");
	},
};
`)
	code, body := do(t, newRouter(t, dir), "GET", "/greet")

	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "Hello from print()") {
		t.Errorf("expected print() output, got %q", body)
	}
}

func TestFsRouter_Export_WithSettings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "info.js", `
module.exports = {
	GET: function(ctx, params, s) {
		ctx.SendString(s.appName || "no settings");
	},
};
`)
	app := fiber.New()
	h, err := FsRouter(RouterConfig{
		Root:      dir,
		AppConfig: &config.AppConfig{NoHtmx: true},
		Settings:  map[string]string{"appName": "TestApp"},
	})
	if err != nil {
		t.Fatalf("FsRouter failed: %v", err)
	}
	app.Use(h)

	code, body := do(t, app, "GET", "/info")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "TestApp") {
		t.Errorf("expected 'TestApp' from settings, got %q", body)
	}
}

// Export + route non-export sur le même URL pattern (suffixe .POST.js vs export GET)
func TestFsRouter_Export_CoexistsWithSuffixRoute(t *testing.T) {
	dir := t.TempDir()
	// POST via suffixe de nom de fichier
	writeFile(t, dir, "items.POST.js", `context.SendString("suffix POST");`)
	// GET via module.exports dans un fichier sans suffixe
	writeFile(t, dir, "items.js", `
module.exports = {
	GET: function(ctx) { ctx.SendString("export GET"); },
};
`)
	app := newRouter(t, dir)

	_, bodyGet := do(t, app, "GET", "/items")
	if !strings.Contains(bodyGet, "export GET") {
		t.Errorf("GET: expected 'export GET', got %q", bodyGet)
	}

	_, bodyPost := do(t, app, "POST", "/items")
	if !strings.Contains(bodyPost, "suffix POST") {
		t.Errorf("POST: expected 'suffix POST', got %q", bodyPost)
	}
}

// ==================== GROUPES DE LAYOUT ====================

func TestFsRouter_LayoutGroup_URL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "(auth)/dashboard.html", `Dashboard`)
	writeFile(t, dir, "(auth)/profile.html", `Profile`)

	app := newRouter(t, dir)

	// Les URLs ne doivent pas contenir (auth)
	code, body := do(t, app, "GET", "/dashboard")
	if code != http.StatusOK {
		t.Errorf("/dashboard: expected 200, got %d", code)
	}
	if !strings.Contains(body, "Dashboard") {
		t.Errorf("/dashboard: expected content, got %q", body)
	}

	// /profile accessible directement
	code2, body2 := do(t, app, "GET", "/profile")
	if code2 != http.StatusOK {
		t.Errorf("/profile: expected 200, got %d", code2)
	}
	if !strings.Contains(body2, "Profile") {
		t.Errorf("/profile: got %q", body2)
	}

	// /(auth)/dashboard ne doit PAS exister
	code3, _ := do(t, app, "GET", "/(auth)/dashboard")
	if code3 == http.StatusOK {
		t.Error("(auth)/dashboard should not be accessible directly")
	}
}

// ==================== MIDDLEWARE ====================

func TestFsRouter_Middleware_SetsHeader(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_middleware.js", `
context.Set("X-Middleware", "applied");
next();
`)
	writeFile(t, dir, "page.html", `Page`)

	req := httptest.NewRequest("GET", "/page", nil)
	resp, err := newRouter(t, dir).Test(req, fiber.TestConfig{Timeout: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Middleware") != "applied" {
		t.Errorf("expected X-Middleware header, got %q", resp.Header.Get("X-Middleware"))
	}
}

func TestFsRouter_Middleware_ShortCircuit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_middleware.js", `
context.Status(403).SendString("Forbidden");
// ne pas appeler next() → court-circuit
`)
	writeFile(t, dir, "secret.html", `Secret Content`)

	code, body := do(t, newRouter(t, dir), "GET", "/secret")

	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
	if !strings.Contains(body, "Forbidden") {
		t.Errorf("expected 'Forbidden', got %q", body)
	}
	// Le contenu de secret.html ne doit pas être rendu
	if strings.Contains(body, "Secret Content") {
		t.Error("short-circuit failed: secret content leaked")
	}
}

func TestFsRouter_Middleware_Cascade(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_middleware.js", `context.Set("X-Root","1"); next();`)
	writeFile(t, dir, "api/_middleware.js", `context.Set("X-API","1"); next();`)
	writeFile(t, dir, "api/users.js", `context.SendString("users");`)

	req := httptest.NewRequest("GET", "/api/users", nil)
	resp, err := newRouter(t, dir).Test(req, fiber.TestConfig{Timeout: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Root") != "1" {
		t.Error("root middleware not applied")
	}
	if resp.Header.Get("X-API") != "1" {
		t.Error("api middleware not applied")
	}
}

func TestFsRouter_Middleware_OnlySubtree(t *testing.T) {
	dir := t.TempDir()
	// Middleware seulement dans /api
	writeFile(t, dir, "api/_middleware.js", `context.Set("X-API","1"); next();`)
	writeFile(t, dir, "api/data.js", `context.SendString("data");`)
	writeFile(t, dir, "public.html", `Public`)

	app := newRouter(t, dir)

	// /api/data → middleware appliqué
	reqAPI := httptest.NewRequest("GET", "/api/data", nil)
	respAPI, _ := app.Test(reqAPI, fiber.TestConfig{Timeout: -1})
	defer respAPI.Body.Close()
	if respAPI.Header.Get("X-API") != "1" {
		t.Error("api middleware should apply to /api/data")
	}

	// /public → middleware non appliqué
	reqPub := httptest.NewRequest("GET", "/public", nil)
	respPub, _ := app.Test(reqPub, fiber.TestConfig{Timeout: -1})
	defer respPub.Body.Close()
	if respPub.Header.Get("X-API") == "1" {
		t.Error("api middleware should NOT apply to /public")
	}
}

func TestFsRouter_Middleware_ThrowError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_middleware.js", `throwError(401, "Need auth");`)
	writeFile(t, dir, "page.html", `Page`)

	code, _ := do(t, newRouter(t, dir), "GET", "/page")

	if code != http.StatusUnauthorized {
		t.Errorf("expected 401 from middleware, got %d", code)
	}
}

// ==================== 404 HANDLERS ====================

func TestFsRouter_404_Default(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", `Home`)

	code, _ := do(t, newRouter(t, dir), "GET", "/nonexistent")

	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestFsRouter_404_CustomTemplate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "404.html", `Custom Not Found`)

	code, body := do(t, newRouter(t, dir), "GET", "/nonexistent")

	if code != http.StatusNotFound {
		t.Errorf("expected 404 status, got %d", code)
	}
	if !strings.Contains(body, "Custom Not Found") {
		t.Errorf("expected custom 404 body, got %q", body)
	}
}

func TestFsRouter_404_CustomJS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "404.js", `context.SendString("js not found");`)

	code, body := do(t, newRouter(t, dir), "GET", "/missing")

	if code != http.StatusNotFound {
		t.Errorf("expected 404 status, got %d", code)
	}
	if !strings.Contains(body, "js not found") {
		t.Errorf("expected JS 404 body, got %q", body)
	}
}

func TestFsRouter_404_Nested_ClosestWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "404.html", `Root 404`)
	writeFile(t, dir, "api/404.html", `API 404`)
	writeFile(t, dir, "api/users.js", `context.SendString("ok");`)

	app := newRouter(t, dir)

	// /api/unknown → api/404.html (plus proche)
	code1, body1 := do(t, app, "GET", "/api/unknown")
	if code1 != http.StatusNotFound {
		t.Errorf("/api/unknown: expected 404, got %d", code1)
	}
	if !strings.Contains(body1, "API 404") {
		t.Errorf("/api/unknown: expected 'API 404', got %q", body1)
	}

	// /unknown → racine 404.html
	code2, body2 := do(t, app, "GET", "/unknown")
	if code2 != http.StatusNotFound {
		t.Errorf("/unknown: expected 404, got %d", code2)
	}
	if !strings.Contains(body2, "Root 404") {
		t.Errorf("/unknown: expected 'Root 404', got %q", body2)
	}
}

func TestFsRouter_404_CustomGoHandler(t *testing.T) {
	dir := t.TempDir()

	app := fiber.New()
	h, err := FsRouter(RouterConfig{
		Root:      dir,
		AppConfig: &config.AppConfig{NoHtmx: true},
		NotFound: func(c fiber.Ctx) error {
			return c.Status(404).SendString("go custom 404")
		},
	})
	if err != nil {
		t.Fatalf("FsRouter failed: %v", err)
	}
	app.Use(h)

	code, body := do(t, app, "GET", "/missing")
	if code != 404 {
		t.Errorf("expected 404, got %d", code)
	}
	if !strings.Contains(body, "go custom 404") {
		t.Errorf("expected go custom 404, got %q", body)
	}
}

// ==================== ERROR HANDLER ====================

func TestFsRouter_ErrorHandler_JS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "broken.js", `this is @@@@ not valid JS`)

	called := false
	app := fiber.New()
	h, fsErr := FsRouter(RouterConfig{
		Root:      dir,
		AppConfig: &config.AppConfig{NoHtmx: true},
		ErrorHandler: func(c fiber.Ctx, err error) error {
			called = true
			return c.Status(500).SendString("caught: " + err.Error())
		},
	})
	if fsErr != nil {
		t.Fatalf("FsRouter failed: %v", fsErr)
	}
	app.Use(h)

	code, body := do(t, app, "GET", "/broken")

	if code != 500 {
		t.Errorf("expected 500, got %d", code)
	}
	if !called {
		t.Error("ErrorHandler should have been called")
	}
	if !strings.Contains(body, "caught:") {
		t.Errorf("expected error handler body, got %q", body)
	}
}

func TestFsRouter_ErrorHandler_Template(t *testing.T) {
	dir := t.TempDir()
	// Template JS invalide
	writeFile(t, dir, "broken.html", `<?js this is not valid ?> text`)

	called := false
	app := fiber.New()
	h, fsErr := FsRouter(RouterConfig{
		Root:        dir,
		TemplateExt: ".html",
		AppConfig:   &config.AppConfig{NoHtmx: true},
		ErrorHandler: func(c fiber.Ctx, err error) error {
			called = true
			return c.Status(500).SendString("template error caught")
		},
	})
	if fsErr != nil {
		t.Fatalf("FsRouter failed: %v", fsErr)
	}
	app.Use(h)

	code, _ := do(t, app, "GET", "/broken")

	if code != 500 {
		t.Errorf("expected 500, got %d", code)
	}
	if !called {
		t.Error("ErrorHandler should have been called for template error")
	}
}

// ==================== SETTINGS ====================

func TestFsRouter_Settings_Template(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "page.html", `<?js var t = settings.title; ?> {{t}}`)

	app := fiber.New()
	h, err := FsRouter(RouterConfig{
		Root:        dir,
		TemplateExt: ".html",
		IndexFile:   "index",
		AppConfig:   &config.AppConfig{NoHtmx: true},
		Settings:    map[string]string{"title": "My Site"},
	})
	if err != nil {
		t.Fatalf("FsRouter failed: %v", err)
	}
	app.Use(h)

	code, body := do(t, app, "GET", "/page")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "My Site") {
		t.Errorf("expected settings.title in body, got %q", body)
	}
}

func TestFsRouter_Settings_JSHandler(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "conf.js", `
context.SendString(settings.env || "no-settings");
`)
	app := fiber.New()
	h, err := FsRouter(RouterConfig{
		Root:      dir,
		AppConfig: &config.AppConfig{NoHtmx: true},
		Settings:  map[string]string{"env": "production"},
	})
	if err != nil {
		t.Fatalf("FsRouter failed: %v", err)
	}
	app.Use(h)

	_, body := do(t, app, "GET", "/conf")
	if !strings.Contains(body, "production") {
		t.Errorf("expected settings.env, got %q", body)
	}
}

// ==================== PROBE MODULE.EXPORTS (unit) ====================

func TestProbeModuleExports_ValidHTTPMethods(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handler.js")
	if err := os.WriteFile(path, []byte(`
module.exports = {
	GET:    function() {},
	POST:   function() {},
	DELETE: function() {},
};
`), 0644); err != nil {
		t.Fatal(err)
	}

	methods := probeModuleExports(path)
	if len(methods) == 0 {
		t.Fatal("expected methods, got none")
	}
	found := map[string]bool{}
	for _, m := range methods {
		found[m] = true
	}
	for _, want := range []string{"GET", "POST", "DELETE"} {
		if !found[want] {
			t.Errorf("expected %s in methods %v", want, methods)
		}
	}
}

func TestProbeModuleExports_NoExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.js")
	if err := os.WriteFile(path, []byte(`var x = 42;`), 0644); err != nil {
		t.Fatal(err)
	}

	methods := probeModuleExports(path)
	if len(methods) != 0 {
		t.Errorf("expected no methods for plain script, got %v", methods)
	}
}

func TestProbeModuleExports_NonHTTPKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obj.js")
	if err := os.WriteFile(path, []byte(`
module.exports = { name: "test", version: "1.0" };
`), 0644); err != nil {
		t.Fatal(err)
	}

	methods := probeModuleExports(path)
	if len(methods) != 0 {
		t.Errorf("expected no HTTP methods, got %v", methods)
	}
}

func TestProbeModuleExports_MixedKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.js")
	if err := os.WriteFile(path, []byte(`
module.exports = {
	GET:  function() {},
	name: "handler",
	PUT:  function() {},
	ANY:  function() {},
};
`), 0644); err != nil {
		t.Fatal(err)
	}

	methods := probeModuleExports(path)
	found := map[string]bool{}
	for _, m := range methods {
		found[m] = true
	}
	if !found["GET"] || !found["PUT"] || !found["ANY"] {
		t.Errorf("expected GET, PUT, ANY — got %v", methods)
	}
	if found["name"] {
		t.Error("non-HTTP key 'name' should not be included")
	}
}

func TestProbeModuleExports_InvalidJS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.js")
	if err := os.WriteFile(path, []byte(`@@@ not valid JS`), 0644); err != nil {
		t.Fatal(err)
	}

	// Ne doit pas paniquer, retourne nil
	methods := probeModuleExports(path)
	if methods != nil {
		t.Errorf("expected nil for invalid JS, got %v", methods)
	}
}

func TestProbeModuleExports_MissingFile(t *testing.T) {
	methods := probeModuleExports("/nonexistent/path/handler.js")
	if methods != nil {
		t.Errorf("expected nil for missing file, got %v", methods)
	}
}

// ==================== FILEPATHTROUTE (unit) ====================

func TestFilePathToRoute_Table(t *testing.T) {
	cfg := RouterConfig{TemplateExt: ".html", IndexFile: "index"}
	cfg.normalize()

	cases := []struct {
		in          string
		wantURL     string
		wantMethod  string
		wantDyn     bool
		wantCatch   bool
		wantPartial bool
	}{
		{"index.html", "/", "GET", false, false, false},
		{"about.html", "/about", "GET", false, false, false},
		{"blog/index.html", "/blog", "GET", false, false, false},
		{"blog/post.html", "/blog/post", "GET", false, false, false},
		{"blog/[slug].html", "/blog/:slug", "GET", true, false, false},
		{"api/[...all].js", "/api/*", "GET", true, true, false},
		{"users.POST.js", "/users", "POST", false, false, false},
		{"users/[id].DELETE.js", "/users/:id", "DELETE", true, false, false},
		{"(auth)/dashboard.html", "/dashboard", "GET", false, false, false},
		{"(auth)/settings/[tab].html", "/settings/:tab", "GET", true, false, false},
		{"page.partial.html", "/page", "GET", false, false, true},
		{"api/data.partial.js", "/api/data", "GET", false, false, true},
	}

	for _, tc := range cases {
		url, method, dyn, catch, part := filePathToRoute(tc.in, cfg)
		if url != tc.wantURL {
			t.Errorf("[%s] url: got %q, want %q", tc.in, url, tc.wantURL)
		}
		if method != tc.wantMethod {
			t.Errorf("[%s] method: got %q, want %q", tc.in, method, tc.wantMethod)
		}
		if dyn != tc.wantDyn {
			t.Errorf("[%s] isDynamic: got %v, want %v", tc.in, dyn, tc.wantDyn)
		}
		if catch != tc.wantCatch {
			t.Errorf("[%s] isCatchAll: got %v, want %v", tc.in, catch, tc.wantCatch)
		}
		if part != tc.wantPartial {
			t.Errorf("[%s] isPartial: got %v, want %v", tc.in, part, tc.wantPartial)
		}
	}
}

// ==================== FSROUTERDEBUG (unit) ====================

func TestFsRouterDebug_ContainsRoutes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", ``)
	writeFile(t, dir, "about.html", ``)
	writeFile(t, dir, "blog/[slug].html", ``)
	writeFile(t, dir, "api/users.js", ``)
	writeFile(t, dir, "_middleware.js", `next();`)
	writeFile(t, dir, "404.html", ``)

	out := FsRouterDebug(RouterConfig{
		Root:        dir,
		TemplateExt: ".html",
		IndexFile:   "index",
	})

	for _, want := range []string{"GET", "/", "/about", "/blog/:slug", "/api/users", "404"} {
		if !strings.Contains(out, want) {
			t.Errorf("FsRouterDebug missing %q\n---\n%s", want, out)
		}
	}
}

func TestFsRouterDebug_ShowsMiddleware(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_middleware.js", `next();`)
	writeFile(t, dir, "api/_middleware.js", `next();`)

	out := FsRouterDebug(RouterConfig{Root: dir, TemplateExt: ".html", IndexFile: "index"})

	if !strings.Contains(out, "Middlewares") {
		t.Errorf("expected 'Middlewares' section in debug output\n%s", out)
	}
}

// ==================== INTEGRATION : arborescence complète ====================

// TestFsRouter_Integration crée une arborescence représentative et vérifie
// que toutes les routes sont accessibles avec les bons codes.
func TestFsRouter_Integration(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "index.html", `Home`)
	writeFile(t, dir, "about.html", `About`)
	writeFile(t, dir, "(auth)/login.html", `Login`)
	writeFile(t, dir, "blog/index.html", `Blog`)
	writeFile(t, dir, "blog/[slug].html", `Post`)
	writeFile(t, dir, "api/users.js", `
module.exports = {
	GET:  function(ctx) { ctx.SendString("list"); },
	POST: function(ctx) { ctx.SendString("create"); },
};
`)
	writeFile(t, dir, "api/users/[id].js", `
module.exports = {
	GET:    function(ctx, p) { ctx.SendString("user:" + p.id); },
	DELETE: function(ctx, p) { ctx.SendString("delete:" + p.id); },
};
`)
	writeFile(t, dir, "api/[...catch].js", `context.SendString("catch");`)
	writeFile(t, dir, "_middleware.js", `context.Set("X-App","1"); next();`)
	writeFile(t, dir, "404.html", `Not Found`)

	app := newRouter(t, dir)

	cases := []struct {
		method string
		path   string
		code   int
		body   string
	}{
		{"GET", "/", 200, "Home"},
		{"GET", "/about", 200, "About"},
		{"GET", "/login", 200, "Login"}, // groupe (auth) ignoré
		{"GET", "/blog", 200, "Blog"},
		{"GET", "/blog/my-post", 200, ""}, // template dynamique
		{"GET", "/api/users", 200, "list"},
		{"POST", "/api/users", 200, "create"},
		{"GET", "/api/users/7", 200, "user:7"},
		{"DELETE", "/api/users/7", 200, "delete:7"},
		{"GET", "/api/foo/bar", 200, "catch"}, // catch-all
		{"GET", "/notfound", 404, "Not Found"},
	}

	for _, tc := range cases {
		code, body := do(t, app, tc.method, tc.path)
		if code != tc.code {
			t.Errorf("%s %s: expected %d, got %d (body: %q)", tc.method, tc.path, tc.code, code, body)
		}
		if tc.body != "" && !strings.Contains(body, tc.body) {
			t.Errorf("%s %s: expected %q in body, got %q", tc.method, tc.path, tc.body, body)
		}
	}

	// Vérifier que le middleware global est bien appliqué
	req := httptest.NewRequest("GET", "/about", nil)
	resp, _ := app.Test(req, fiber.TestConfig{Timeout: -1})
	resp.Body.Close()
	if resp.Header.Get("X-App") != "1" {
		t.Error("global middleware not applied in integration test")
	}
}

// ==================== ERROR HANDLERS ====================

func TestFsRouter_Error_404File(t *testing.T) {
	// 404.html dans un dossier intercepte les "not found" levés par le dispatcher
	dir := t.TempDir()
	writeFile(t, dir, "404.html", `Custom 404`)

	code, body := do(t, newRouter(t, dir), "GET", "/missing")

	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
	if !strings.Contains(body, "Custom 404") {
		t.Errorf("expected 'Custom 404', got %q", body)
	}
}

func TestFsRouter_Error_500File(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "500.html", `Server Error Page`)
	writeFile(t, dir, "boom.js", `throwError(500, "boom");`)

	code, body := do(t, newRouter(t, dir), "GET", "/boom")

	if code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", code)
	}
	if !strings.Contains(body, "Server Error Page") {
		t.Errorf("expected '500.html' content, got %q", body)
	}
}

func TestFsRouter_Error_Generic_ErrorFile(t *testing.T) {
	// _error.html est le fallback générique pour tout code non couvert
	dir := t.TempDir()
	writeFile(t, dir, "_error.html", `Generic Error`)
	writeFile(t, dir, "forbidden.js", `throwError(403, "forbidden");`)

	code, body := do(t, newRouter(t, dir), "GET", "/forbidden")

	if code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", code)
	}
	if !strings.Contains(body, "Generic Error") {
		t.Errorf("expected '_error.html' content, got %q", body)
	}
}

func TestFsRouter_Error_CodeBeforeWildcard(t *testing.T) {
	// {code}.html est prioritaire sur _error.html dans le même dossier
	dir := t.TempDir()
	writeFile(t, dir, "403.html", `Forbidden Specific`)
	writeFile(t, dir, "_error.html", `Generic Error`)
	writeFile(t, dir, "forbidden.js", `throwError(403, "x");`)
	writeFile(t, dir, "other.js", `throwError(422, "x");`)

	app := newRouter(t, dir)

	// 403 → 403.html prioritaire
	_, body403 := do(t, app, "GET", "/forbidden")
	if !strings.Contains(body403, "Forbidden Specific") {
		t.Errorf("403: expected '403.html' content, got %q", body403)
	}

	// 422 → pas de 422.html → _error.html fallback
	_, body422 := do(t, app, "GET", "/other")
	if !strings.Contains(body422, "Generic Error") {
		t.Errorf("422: expected '_error.html' fallback, got %q", body422)
	}
}

func TestFsRouter_Error_ParentFallback(t *testing.T) {
	// Si aucun handler dans le dossier courant, remonte au parent
	dir := t.TempDir()
	writeFile(t, dir, "500.html", `Root 500`) // handler à la racine
	writeFile(t, dir, "api/users.js", `throwError(500, "db");`)

	code, body := do(t, newRouter(t, dir), "GET", "/api/users")

	if code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", code)
	}
	if !strings.Contains(body, "Root 500") {
		t.Errorf("expected root 500 handler, got %q", body)
	}
}

func TestFsRouter_Error_CloserWins(t *testing.T) {
	// Le handler du dossier le plus proche de la requête gagne
	dir := t.TempDir()
	writeFile(t, dir, "500.html", `Root 500`)
	writeFile(t, dir, "api/500.html", `API 500`)
	writeFile(t, dir, "api/data.js", `throwError(500, "err");`)

	code, body := do(t, newRouter(t, dir), "GET", "/api/data")

	if code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", code)
	}
	if !strings.Contains(body, "API 500") {
		t.Errorf("expected api/500.html (closer), got %q", body)
	}
}

func TestFsRouter_Error_JS_Handler(t *testing.T) {
	// _error.js reçoit errorCode et errorMessage
	dir := t.TempDir()
	writeFile(t, dir, "_error.js", `
context.SendString("code:" + errorCode + " msg:" + errorMessage);
`)
	writeFile(t, dir, "fail.js", `throwError(418, "I am a teapot");`)

	code, body := do(t, newRouter(t, dir), "GET", "/fail")

	if code != 418 {
		t.Errorf("expected 418, got %d", code)
	}
	if !strings.Contains(body, "code:418") {
		t.Errorf("expected errorCode in body, got %q", body)
	}
	if !strings.Contains(body, "I am a teapot") {
		t.Errorf("expected errorMessage in body, got %q", body)
	}
}

func TestFsRouter_Error_JS_ReturnValue(t *testing.T) {
	dir := t.TempDir()
	// Top-level return works in goja scripts — use context.SendString to be explicit
	writeFile(t, dir, "500.js", `
context.SendString("error " + errorCode);
`)
	writeFile(t, dir, "boom.js", `throwError(500, "x");`)

	_, body := do(t, newRouter(t, dir), "GET", "/boom")
	if !strings.Contains(body, "error 500") {
		t.Errorf("expected 'error 500' from 500.js, got %q", body)
	}
}

func TestFsRouter_Error_Template_Variables(t *testing.T) {
	// Les variables errorCode/errorMessage sont accessibles dans le template
	// via context.Locals (injectés par handleErrorTemplate)
	dir := t.TempDir()
	writeFile(t, dir, "_error.html", `
<?js
  var code = context.Locals("errorCode") || 0;
  var msg  = context.Locals("errorMessage") || "";
?>
Error {{code}}: {{msg}}
`)
	writeFile(t, dir, "fail.js", `throwError(422, "Unprocessable");`)

	code, body := do(t, newRouter(t, dir), "GET", "/fail")
	if code != 422 {
		t.Errorf("expected 422, got %d", code)
	}
	if !strings.Contains(body, "422") {
		t.Errorf("expected code 422 in template output, got %q", body)
	}
	if !strings.Contains(body, "Unprocessable") {
		t.Errorf("expected message in template output, got %q", body)
	}
}

func TestFsRouter_Error_NoHandler_DefaultFiber(t *testing.T) {
	// Sans error handler, l'erreur Fiber est retournée normalement
	dir := t.TempDir()
	writeFile(t, dir, "fail.js", `throwError(503, "unavailable");`)

	code, _ := do(t, newRouter(t, dir), "GET", "/fail")
	if code != 503 {
		t.Errorf("expected 503, got %d", code)
	}
}

func TestFsRouter_Error_DeepNesting(t *testing.T) {
	// Remontée sur 3 niveaux
	dir := t.TempDir()
	writeFile(t, dir, "500.html", `Root 500`)
	writeFile(t, dir, "a/b/c/fail.js", `throwError(500, "deep");`)

	code, body := do(t, newRouter(t, dir), "GET", "/a/b/c/fail")

	if code != 500 {
		t.Errorf("expected 500, got %d", code)
	}
	if !strings.Contains(body, "Root 500") {
		t.Errorf("expected root handler after deep traversal, got %q", body)
	}
}

func TestFsRouter_Error_Wildcard_OverCode_AtSameLevel(t *testing.T) {
	// Dans le sous-dossier : _error.html présent mais pas 500.html
	// À la racine : 500.html présent
	// → le _error.html du sous-dossier doit gagner (plus proche)
	dir := t.TempDir()
	writeFile(t, dir, "500.html", `Root 500`)
	writeFile(t, dir, "api/_error.html", `API generic error`)
	writeFile(t, dir, "api/fail.js", `throwError(500, "x");`)

	_, body := do(t, newRouter(t, dir), "GET", "/api/fail")
	if !strings.Contains(body, "API generic error") {
		t.Errorf("api/_error.html should win over root/500.html, got %q", body)
	}
}

// ==================== ISHTTP ERROR CODE (unit) ====================

func TestIsHTTPErrorCode(t *testing.T) {
	valid := []string{"100", "200", "301", "404", "422", "500", "503", "599"}
	for _, s := range valid {
		if !isHTTPErrorCode(s) {
			t.Errorf("expected %q to be a valid HTTP code", s)
		}
	}
	invalid := []string{"", "99", "600", "abc", "4O4", "1000", "50", "_error", "index"}
	for _, s := range invalid {
		if isHTTPErrorCode(s) {
			t.Errorf("expected %q to NOT be a valid HTTP code", s)
		}
	}
}

// ==================== FINDEERRORHANDLER (unit) ====================

func TestFindErrorHandler_ExactCode(t *testing.T) {
	handlers := map[string]map[string]string{
		"/": {"500": "/pages/500.html", "_error": "/pages/_error.html"},
	}
	fp, kind := findErrorHandler(500, "/about", handlers)
	if fp != "/pages/500.html" {
		t.Errorf("expected 500.html, got %q", fp)
	}
	if kind != "code" {
		t.Errorf("expected kind=code, got %q", kind)
	}
}

func TestFindErrorHandler_Wildcard(t *testing.T) {
	handlers := map[string]map[string]string{
		"/": {"_error": "/pages/_error.html"},
	}
	fp, kind := findErrorHandler(422, "/form", handlers)
	if fp != "/pages/_error.html" {
		t.Errorf("expected _error.html, got %q", fp)
	}
	if kind != "wildcard" {
		t.Errorf("expected kind=wildcard, got %q", kind)
	}
}

func TestFindErrorHandler_CloserDirWins(t *testing.T) {
	handlers := map[string]map[string]string{
		"/":    {"500": "/pages/500.html"},
		"/api": {"500": "/pages/api/500.html"},
	}
	// Requête dans /api → api/500.html gagne
	fp, _ := findErrorHandler(500, "/api/users", handlers)
	if fp != "/pages/api/500.html" {
		t.Errorf("expected api/500.html, got %q", fp)
	}
	// Requête dans / → root/500.html
	fp2, _ := findErrorHandler(500, "/about", handlers)
	if fp2 != "/pages/500.html" {
		t.Errorf("expected root/500.html, got %q", fp2)
	}
}

func TestFindErrorHandler_NotFound(t *testing.T) {
	handlers := map[string]map[string]string{
		"/api": {"404": "/pages/api/404.html"},
	}
	// Code 500 → aucun handler → ("", "")
	fp, _ := findErrorHandler(500, "/api/users", handlers)
	if fp != "" {
		t.Errorf("expected empty, got %q", fp)
	}
}

func TestFindErrorHandler_SubdirFallsToRoot(t *testing.T) {
	handlers := map[string]map[string]string{
		"/": {"_error": "/pages/_error.html"},
		// /a/b/c n'a pas de handler
	}
	fp, _ := findErrorHandler(503, "/a/b/c/page", handlers)
	if fp != "/pages/_error.html" {
		t.Errorf("expected root _error.html after traversal, got %q", fp)
	}
}

// ==================== FSROUTERDEBUG avec error handlers ====================

func TestFsRouterDebug_ShowsErrorHandlers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "500.html", ``)
	writeFile(t, dir, "_error.html", ``)
	writeFile(t, dir, "api/404.html", ``)
	writeFile(t, dir, "api/422.js", ``)

	out := FsRouterDebug(RouterConfig{
		Root: dir, TemplateExt: ".html", IndexFile: "index",
	})

	for _, want := range []string{"Error handlers", "500", "_error", "422"} {
		if !strings.Contains(out, want) {
			t.Errorf("FsRouterDebug missing %q\n%s", want, out)
		}
	}
}

// ==================== LAYOUTS ====================

func TestFsRouter_Layout_Simple(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_layout.html", `Header {{content}} Footer`)
	writeFile(t, dir, "index.html", `Content`)

	code, body := do(t, newRouter(t, dir), "GET", "/")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	// "Header Content Footer"
	if !strings.Contains(body, "Header") || !strings.Contains(body, "Footer") || !strings.Contains(body, "Content") {
		t.Errorf("expected layout wrapping, got %q", body)
	}
}

func TestFsRouter_Layout_Nested(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_layout.html", `[Root {{content}}]`)
	writeFile(t, dir, "admin/_layout.html", `{Admin {{content}}}`)
	writeFile(t, dir, "admin/users.html", `Users`)

	_, body := do(t, newRouter(t, dir), "GET", "/admin/users")
	if !strings.Contains(body, "[Root {Admin Users}]") {
		t.Errorf("expected nested layout [Root {Admin Users}], got %q", body)
	}
}

func TestFsRouter_Layout_JS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_layout.js", `"[JS " + Locals.content + "]"`)
	writeFile(t, dir, "hello.html", `Hello`)

	_, body := do(t, newRouter(t, dir), "GET", "/hello")
	if !strings.Contains(body, "[JS Hello]") {
		t.Errorf("expected JS layout [JS Hello], got %q", body)
	}
}

func TestFsRouter_Layout_CapturedBody(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_layout.html", `Wrap {{content}}`)
	writeFile(t, dir, "send.js", `context.SendString("SentBody");`)

	_, body := do(t, newRouter(t, dir), "GET", "/send")
	if !strings.Contains(body, "Wrap SentBody") {
		t.Errorf("expected layout wrapping captured body, got %q", body)
	}
}

func TestFsRouter_Layout_Errors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_layout.html", `ErrWrap {{errorCode}}: {{content}}`)
	writeFile(t, dir, "500.html", `BOOM {{errorMessage}}`)
	writeFile(t, dir, "fail.js", `throwError(500, "oops");`)

	code, body := do(t, newRouter(t, dir), "GET", "/fail")
	if code != 500 {
		t.Errorf("expected 500, got %d", code)
	}
	if !strings.Contains(body, "ErrWrap 500: BOOM oops") {
		t.Errorf("expected error layout ErrWrap 500: BOOM oops, got %q", body)
	}
}

func TestFsRouter_Partial(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "_layout.html", `LAYOUT {{content}} END`)
	writeFile(t, dir, "page.html", `PAGE`)
	writeFile(t, dir, "partial.partial.html", `PARTIAL`)

	router := newRouter(t, dir)

	// 1. Verifier page normale
	_, body := do(t, router, "GET", "/page")
	if body != "LAYOUT PAGE END" {
		t.Errorf("expected wrapped page, got %q", body)
	}

	// 2. Verifier partial
	_, body = do(t, router, "GET", "/partial")
	if body != "PARTIAL" {
		t.Errorf("expected unwrapped partial, got %q", body)
	}
}

// ==================== HOT-RELOAD ====================

func TestFsRouter_HotReload_EmptyToIndex(t *testing.T) {
	dir := t.TempDir()

	app := fiber.New()
	h, err := FsRouter(RouterConfig{
		Root:        dir,
		TemplateExt: ".html",
		IndexFile:   "index",
		AppConfig:   &config.AppConfig{NoHtmx: true, HotReload: true, Silent: true},
	})
	if err != nil {
		t.Fatalf("FsRouter failed: %v", err)
	}
	app.Use(h)

	// 1. Répertoire vide → 404
	code, _ := do(t, app, "GET", "/")
	if code != http.StatusNotFound {
		t.Errorf("expected 404 for empty dir, got %d", code)
	}

	// 2. Créer index.html
	writeFile(t, dir, "index.html", `Hello Hot-Reload`)

	// 3. Attendre le debounce (150ms) + marge
	time.Sleep(500 * time.Millisecond)

	// 4. La route doit maintenant être disponible
	code, body := do(t, app, "GET", "/")
	if code != http.StatusOK {
		t.Errorf("expected 200 after hot-reload, got %d", code)
	}
	if !strings.Contains(body, "Hello Hot-Reload") {
		t.Errorf("expected 'Hello Hot-Reload', got %q", body)
	}
}
