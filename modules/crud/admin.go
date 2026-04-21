package crud

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"beba/modules/sse"
	"beba/plugins/httpserver"
	"beba/processor"
	"beba/types"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

//go:embed admin_templates/*.html
var adminTemplates embed.FS

//go:embed admin_assets/*.css
var adminAssets embed.FS

// ─────────────────────────────────────────────────────────────────────────────
// Template rendering via processor.ProcessString
// ─────────────────────────────────────────────────────────────────────────────

// readEmbeddedTemplate reads a template file from the embedded FS.
func readEmbeddedTemplate(name string) (string, error) {
	data, err := adminTemplates.ReadFile("admin_templates/" + name)
	if err != nil {
		return "", fmt.Errorf("admin template %q not found: %w", name, err)
	}
	return string(data), nil
}

// renderAdmin renders a page template inside the layout using processor.ProcessString.
func renderAdmin(c fiber.Ctx, pageTemplate string, prefix string, apiPrefix string, activePath string, settings map[string]string) error {
	db := c.Locals("db").(*gorm.DB)

	// Read active namespace from cookie
	activeNS := c.Cookies("_admin_active_ns")
	var nsList []Namespace
	db.Find(&nsList)

	// If no active namespace is set, default to "global" or the first one
	if activeNS == "" {
		for _, ns := range nsList {
			if ns.Slug == "global" || ns.IsDefault {
				activeNS = ns.ID
				break
			}
		}
		if activeNS == "" && len(nsList) > 0 {
			activeNS = nsList[0].ID
		}
	}

	// Render the page content first
	pageHTML, err := processor.ProcessString(pageTemplate, "", c, nil, settings)
	if err != nil {
		return c.Status(500).SendString("Template error: " + err.Error())
	}

	// If it's an HTMX request for the main content, return only the page HTML
	if c.Get("HX-Request") == "true" {
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.SendString(pageHTML)
	}

	// Read and render the layout
	layoutTpl, err := readEmbeddedTemplate("layout.html")
	if err != nil {
		return c.Status(500).SendString(err.Error())
	}

	// Read CSS
	cssBytes, _ := adminAssets.ReadFile("admin_assets/admin.css")

	// Build menu JSON
	menu := buildMenu(prefix, activePath)
	menuJSON, _ := json.Marshal(menu)

	// Build namespace list JSON
	namespacesJSON, _ := json.Marshal(nsList)

	// Determine the SSE prefix
	ssePrefix := apiPrefix + "/namespaces/changes"

	// Build layout settings
	layoutSettings := map[string]string{
		"content":         pageHTML,
		"css":             string(cssBytes),
		"menu":            string(menuJSON),
		"namespaces":      string(namespacesJSON),
		"activeNamespace": activeNS,
		"pageTitle":       settings["pageTitle"],
		"ssePrefix":       ssePrefix,
		"adminPrefix":     prefix,
	}

	rendered, err := processor.ProcessString(layoutTpl, "", c, nil, layoutSettings)
	if err != nil {
		return c.Status(500).SendString("Layout error: " + err.Error())
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(rendered)
}

// ─────────────────────────────────────────────────────────────────────────────
// mountAdmin registers all admin routes under {prefix}/_admin
// ─────────────────────────────────────────────────────────────────────────────

func mountAdmin(app *httpserver.HTTP, prefix string, apiPrefix string, db *gorm.DB, secret string, authentication ...types.Authentification) {
	adminPrefix := prefix + "/_admin"

	// Admin requires a valid root token
	adminAuth := func(c fiber.Ctx) error {
		hdr := c.Get("Authorization")
		tok := strings.TrimPrefix(hdr, "Bearer ")
		// Also check for cookie-based auth
		if tok == "" {
			tok = c.Cookies("_admin_token")
		}
		if tok == "" {
			// Redirect to login page
			return c.Redirect().To(adminPrefix + "/login")
		}
		rc, err := resolveRequestCtx(db, tok, secret, authentication...)
		if err != nil || rc == nil {
			return c.Redirect().To(adminPrefix + "/login")
		}
		// Must be root (namespace_id is NULL)
		if !rc.IsRoot {
			return c.Status(403).SendString("Admin access requires root privileges")
		}
		c.Locals("rc", rc)
		c.Locals("db", db) // Store db in locals for helpers
		return c.Next()
	}

	ag := app.Group(adminPrefix)

	// ── Login page (no auth required) ────────────────────────────────────
	ag.Get("/login", func(c fiber.Ctx) error {
		loginTpl, err := readEmbeddedTemplate("login.html")
		if err != nil {
			return c.Status(500).SendString(err.Error())
		}
		cssBytes, _ := adminAssets.ReadFile("admin_assets/admin.css")
		settings := map[string]string{
			"adminPrefix": adminPrefix,
			"css":         string(cssBytes),
			"error":       c.Query("error"),
		}
		rendered, err := processor.ProcessString(loginTpl, "", c, nil, settings)
		if err != nil {
			return c.Status(500).SendString(err.Error())
		}
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.SendString(rendered)
	})

	ag.Post("/login", func(c fiber.Ctx) error {
		username := c.FormValue("username")
		password := c.FormValue("password")
		token, err := loginRoot(db, username, password, secret, authentication...)
		if err != nil {
			return c.Redirect().To(adminPrefix + "/login?error=" + url.QueryEscape(err.Error()))
		}
		c.Cookie(&fiber.Cookie{
			Name:     "_admin_token",
			Value:    token,
			Path:     adminPrefix,
			HTTPOnly: true,
			SameSite: "Lax",
		})
		return c.Redirect().To(adminPrefix)
	})

	ag.Get("/logout", func(c fiber.Ctx) error {
		c.Cookie(&fiber.Cookie{
			Name:     "_admin_token",
			Value:    "",
			Path:     adminPrefix,
			HTTPOnly: true,
			MaxAge:   -1,
		})
		return c.Redirect().To(adminPrefix + "/login")
	})

	ag.Get("/switch-ns/:id", func(c fiber.Ctx) error {
		c.Cookie(&fiber.Cookie{
			Name:     "_admin_active_ns",
			Value:    c.Params("id"),
			Path:     adminPrefix,
			HTTPOnly: true,
			SameSite: "Lax",
		})
		return c.Redirect().To(c.Get("Referer", adminPrefix))
	})

	// ── Protected admin routes ───────────────────────────────────────────
	admin := ag.Group("", adminAuth)

	// ── SSE endpoint for admin (reuses sse.Handler with pre-configured channels)
	admin.Get("/sse", func(c fiber.Ctx) error {
		c.Locals("channels", []string{
			"crud::create", "crud::update", "crud::delete",
			"crud::login", "crud::logout", "crud::reject",
		})
		return sse.Handler(c)
	})

	// ── Dashboard ────────────────────────────────────────────────────────
	admin.Get("/", func(c fiber.Ctx) error {
		activeNS := c.Cookies("_admin_active_ns")

		var schemaCount, nsCount, userCount, roleCount int64
		db.Model(&CrudSchema{}).Where("namespace_id = ?", activeNS).Count(&schemaCount)
		db.Model(&Namespace{}).Count(&nsCount)
		db.Model(&User{}).Where("namespace_id = ?", activeNS).Count(&userCount)
		db.Model(&Role{}).Where("namespace_id = ?", activeNS).Count(&roleCount)

		// Fetch schemas with doc counts
		var schemas []CrudSchema
		db.Where("namespace_id = ?", activeNS).Find(&schemas)
		type schemaInfo struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Slug     string `json:"slug"`
			Icon     string `json:"icon"`
			Color    string `json:"color"`
			DocCount int64  `json:"doc_count"`
		}
		var infos []schemaInfo
		for _, s := range schemas {
			var cnt int64
			db.Model(&CrudDocument{}).Where("schema_id = ? AND deleted_at IS NULL", s.ID).Count(&cnt)
			infos = append(infos, schemaInfo{ID: s.ID, Name: s.Name, Slug: s.Slug, Icon: s.Icon, Color: s.Color, DocCount: cnt})
		}
		schemasJSON, _ := json.Marshal(infos)

		tpl, _ := readEmbeddedTemplate("dashboard.html")
		return renderAdmin(c, tpl, adminPrefix, apiPrefix, "/", map[string]string{
			"pageTitle":      "Dashboard",
			"schemaCount":    fmt.Sprintf("%d", schemaCount),
			"namespaceCount": fmt.Sprintf("%d", nsCount),
			"userCount":      fmt.Sprintf("%d", userCount),
			"roleCount":      fmt.Sprintf("%d", roleCount),
			"schemas":        string(schemasJSON),
			"adminPrefix":    adminPrefix,
		})
	})

	// ── Collections list (Real page now) ─────────────────────────────────
	admin.Get("/collections", func(c fiber.Ctx) error {
		activeNS := c.Cookies("_admin_active_ns")
		var schemas []CrudSchema
		db.Where("namespace_id = ?", activeNS).Find(&schemas)
		schemasJSON, _ := json.Marshal(schemas)

		tpl, _ := readEmbeddedTemplate("schemas.html")
		return renderAdmin(c, tpl, adminPrefix, apiPrefix, "/collections", map[string]string{
			"pageTitle":   "Collections",
			"schemas":     string(schemasJSON),
			"adminPrefix": adminPrefix,
			"apiPrefix":   apiPrefix,
		})
	})

	// ── Collection detail (document list) ────────────────────────────────
	admin.Get("/collections/:slug", func(c fiber.Ctx) error {
		slug := c.Params("slug")
		var schema CrudSchema
		if err := db.Where("slug = ?", slug).First(&schema).Error; err != nil {
			return c.Status(404).SendString("Schema not found")
		}

		tableHTML, err := renderDocTable(c, db, &schema, adminPrefix, apiPrefix, "", "-created_at")
		if err != nil {
			return c.Status(500).SendString(err.Error())
		}

		tpl, _ := readEmbeddedTemplate("collection.html")
		return renderAdmin(c, tpl, adminPrefix, apiPrefix, "/collections", map[string]string{
			"pageTitle":    schema.Name,
			"schemaName":   schema.Name,
			"schemaSlug":   schema.Slug,
			"tableContent": tableHTML,
			"adminPrefix":  adminPrefix,
			"apiPrefix":    apiPrefix,
		})
	})

	// ── Table partial (for HTMX swaps) ───────────────────────────────────
	admin.Get("/collections/:slug/table", func(c fiber.Ctx) error {
		slug := c.Params("slug")
		var schema CrudSchema
		if err := db.Where("slug = ?", slug).First(&schema).Error; err != nil {
			return c.Status(404).SendString("Schema not found")
		}
		q := c.Query("q")
		sort := c.Query("sort", "-created_at")
		html, err := renderDocTable(c, db, &schema, adminPrefix, apiPrefix, q, sort)
		if err != nil {
			return c.Status(500).SendString(err.Error())
		}
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.SendString(html)
	})

	// ── Document detail ──────────────────────────────────────────────────
	admin.Get("/collections/:slug/:id", func(c fiber.Ctx) error {
		slug := c.Params("slug")
		docID := c.Params("id")
		var schema CrudSchema
		if err := db.Where("slug = ?", slug).First(&schema).Error; err != nil {
			return c.Status(404).SendString("Schema not found")
		}
		var doc CrudDocument
		if err := db.Where("id = ? AND schema_id = ?", docID, schema.ID).First(&doc).Error; err != nil {
			return c.Status(404).SendString("Document not found")
		}

		// Pretty-print JSON
		prettyData := prettyJSON(doc.Data)
		prettyMeta := prettyJSON(doc.Meta)

		tpl, _ := readEmbeddedTemplate("document.html")
		return renderAdmin(c, tpl, adminPrefix, apiPrefix, "/collections", map[string]string{
			"pageTitle":   "Document " + docID[:8],
			"docId":       docID,
			"schemaSlug":  slug,
			"docData":     prettyData,
			"docMeta":     prettyMeta,
			"docCreated":  doc.CreatedAt.Format("2006-01-02 15:04"),
			"docUpdated":  doc.UpdatedAt.Format("2006-01-02 15:04"),
			"adminPrefix": adminPrefix,
			"apiPrefix":   apiPrefix,
		})
	})

	// ── Namespaces ───────────────────────────────────────────────────────
	admin.Get("/namespaces", func(c fiber.Ctx) error {
		var nsList []Namespace
		db.Find(&nsList)
		nsJSON, _ := json.Marshal(nsList)
		tpl, _ := readEmbeddedTemplate("namespaces.html")
		return renderAdmin(c, tpl, adminPrefix, apiPrefix, "/namespaces", map[string]string{
			"pageTitle":   "Namespaces",
			"namespaces":  string(nsJSON),
			"adminPrefix": adminPrefix,
			"apiPrefix":   apiPrefix,
		})
	})

	// ── Users ────────────────────────────────────────────────────────────
	admin.Get("/users", func(c fiber.Ctx) error {
		var userList []User
		db.Find(&userList)
		usersJSON, _ := json.Marshal(userList)
		tpl, _ := readEmbeddedTemplate("users.html")
		return renderAdmin(c, tpl, adminPrefix, apiPrefix, "/users", map[string]string{
			"pageTitle":   "Users",
			"users":       string(usersJSON),
			"adminPrefix": adminPrefix,
			"apiPrefix":   apiPrefix,
		})
	})

	// ── Roles ────────────────────────────────────────────────────────────
	admin.Get("/roles", func(c fiber.Ctx) error {
		var roleList []Role
		db.Find(&roleList)
		rolesJSON, _ := json.Marshal(roleList)
		tpl, _ := readEmbeddedTemplate("roles.html")
		return renderAdmin(c, tpl, adminPrefix, apiPrefix, "/roles", map[string]string{
			"pageTitle":   "Roles",
			"roles":       string(rolesJSON),
			"adminPrefix": adminPrefix,
			"apiPrefix":   apiPrefix,
		})
	})

	// ── Custom pages from the registry ───────────────────────────────────
	for _, page := range sortedAdminPages() {
		p := page // capture loop var
		admin.Get(p.Path, func(c fiber.Ctx) error {
			tpl := p.Template
			if tpl == "" {
				return c.SendString("No template defined for " + p.Path)
			}
			return renderAdmin(c, tpl, adminPrefix, apiPrefix, p.Path, map[string]string{
				"pageTitle":   p.Title,
				"adminPrefix": adminPrefix,
				"apiPrefix":   apiPrefix,
			})
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// renderDocTable renders the table partial for a schema's documents.
func renderDocTable(c fiber.Ctx, db *gorm.DB, schema *CrudSchema, adminPrefix, apiPrefix, search, sortField string) (string, error) {
	query := db.Where("schema_id = ? AND deleted_at IS NULL", schema.ID)

	// Search
	if search != "" {
		query = query.Where("data LIKE ?", "%"+search+"%")
	}

	// Sort
	if sortField != "" {
		if strings.HasPrefix(sortField, "-") {
			query = query.Order(sortField[1:] + " DESC")
		} else {
			query = query.Order(sortField + " ASC")
		}
	}

	var docs []CrudDocument
	query.Limit(100).Find(&docs)

	// Serialize docs for template
	type docInfo struct {
		ID        string `json:"id"`
		Data      any    `json:"data"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	var infos []docInfo
	for _, d := range docs {
		var dataObj any
		json.Unmarshal([]byte(d.Data), &dataObj)
		infos = append(infos, docInfo{
			ID:        d.ID,
			Data:      dataObj,
			CreatedAt: d.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt: d.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	docsJSON, _ := json.Marshal(infos)

	tpl, err := readEmbeddedTemplate("table.html")
	if err != nil {
		return "", err
	}

	settings := map[string]string{
		"documents":   string(docsJSON),
		"schemaSlug":  schema.Slug,
		"adminPrefix": adminPrefix,
		"apiPrefix":   apiPrefix,
	}
	return processor.ProcessString(tpl, "", c, nil, settings)
}

// prettyJSON formats a JSON string for display.
func prettyJSON(raw string) string {
	if raw == "" {
		return "{}"
	}
	var obj interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return raw
	}
	pretty, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return raw
	}
	return string(pretty)
}

// loginRoot authenticates a root user and returns a JWT token.
func loginRoot(db *gorm.DB, username, password, secret string, authentications ...types.Authentification) (token string, err error) {
	var user User
	if len(authentications) > 0 {
		if err := authentications[0].Auth(username, password); err == nil {
			user = User{
				Username: username,
				Email:    username,
				IsActive: true,
			}
		} else {
			return "", fmt.Errorf("invalid credentials %s", err.Error())
		}
	} else {
		if err := db.Where("(username = ? OR email = ?) AND namespace_id IS NULL AND is_active = ?", username, username, true).First(&user).Error; err != nil {
			return "", fmt.Errorf("1invalid credentials %s", err.Error())
		}
		if !checkPwd(user.PasswordHash, password) {
			return "", fmt.Errorf("2invalid credentials")
		}
	}
	sess, err := createSession(db, &user, "")
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return signJWT(sess, &user, secret)
}
