package crud

import (
	"beba/modules/sse"
	"beba/plugins/httpserver"
	"beba/types"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// mountRoutes registers all CRUD routes onto a *fiber.App at the given prefix.
// Called directly from http_protocol.go when processing a CRUD directive inside
// an HTTP block, and also usable in tests without an httpserver.
func mountRoutes(
	app *httpserver.HTTP,
	prefix string,
	db *gorm.DB,
	providers map[string]*oauth2Config,
	secret string,
	baseDir string,
	authentication types.Authentification,
) error {

	// ── Auth middleware ───────────────────────────────────────────────────────
	// Reads Bearer token, resolves *requestCtx, stores in c.Locals("rc").
	// Unauthenticated requests pass through (nil rc) — hooks enforce access.
	authMW := func(c fiber.Ctx) error {
		hdr := c.Get("Authorization")
		tok := strings.TrimPrefix(hdr, "Bearer ")
		if tok != "" {
			rc, err := resolveRequestCtx(db, tok, secret, authentication)
			if err != nil {
				return c.Status(401).JSON(fiber.Map{"error": err.Error()})
			}
			c.Locals("rc", rc)
		}
		return c.Next()
	}

	rc := func(c fiber.Ctx) *requestCtx {
		v := c.Locals("rc")
		if v == nil {
			return nil
		}
		return v.(*requestCtx)
	}

	// All CRUD routes share the auth middleware
	g := app.Group(prefix, authMW)

	// ── Auth ──────────────────────────────────────────────────────────────────
	auth := g.Group("/auth")

	// POST /crud/auth/login
	auth.Post("/login", func(c fiber.Ctx) error {
		var body struct {
			Identity  string `json:"identity"`
			Password  string `json:"password"`
			Namespace string `json:"namespace"`
		}
		if err := c.Bind().JSON(&body); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid body"})
		}
		var ns Namespace
		if body.Namespace == "" || body.Namespace == "default" {
			if err := db.Where("is_default = ?", true).First(&ns).Error; err != nil {
				return c.Status(500).JSON(fiber.Map{"error": "no default namespace"})
			}
		} else {
			if err := db.Where("slug = ? OR id = ?", body.Namespace, body.Namespace).
				First(&ns).Error; err != nil {
				return c.Status(404).JSON(fiber.Map{"error": "namespace not found"})
			}
		}
		if !strings.Contains(ns.AuthProviders, "password") {
			return c.Status(403).JSON(fiber.Map{"error": "password auth not enabled for this namespace"})
		}
		tok, user, err := loginPassword(db, ns.ID, body.Identity, body.Password, effectiveSecret(&ns, secret))
		if err != nil {
			broadcastCRUD("reject", ns.Slug, "", "", err.Error())
			return c.Status(401).JSON(fiber.Map{"error": err.Error()})
		}
		res := fiber.Map{"token": tok, "user": publicUser(user)}
		broadcastCRUD("login", ns.Slug, "", user.ID, res)
		return c.JSON(res)
	})

	// POST /crud/auth/logout
	auth.Post("/logout", func(c fiber.Ctx) error {
		hdr := c.Get("Authorization")
		tok := strings.TrimPrefix(hdr, "Bearer ")
		if tok == "" {
			var body struct {
				Token string `json:"token"`
			}
			c.Bind().JSON(&body)
			tok = body.Token
		}
		if tok == "" {
			return c.Status(400).JSON(fiber.Map{"error": "no token"})
		}
		claims, err := parseJWT(tok, secret)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid token"})
		}
		revokeSession(db, claims.ID)
		var ns Namespace
		db.Select("slug").First(&ns, "id = ?", claims.NamespaceID)
		broadcastCRUD("logout", ns.Slug, "", claims.UserID, nil)
		return c.JSON(fiber.Map{"ok": true})
	})

	// ── OAuth2 — per namespace + default namespace shortcut ───────────────────
	var nsList []Namespace
	db.Find(&nsList)
	for i := range nsList {
		ns := &nsList[i]
		nsSecret := effectiveSecret(ns, secret)
		nsGroup := g.Group("/" + ns.Slug + "/auth")

		for pName, pCfg := range providers {
			p := pCfg      // stable copy
			pname := pName // stable copy

			// GET /crud/{ns}/auth/{provider}  → redirect to provider
			nsGroup.Get("/"+pname, func(c fiber.Ctx) error {
				state := newID()
				db.Create(&OAuthState{State: state, Provider: pname, NamespaceID: ns.ID})
				return c.Redirect().To(p.authURL(state))
			})

			// GET /crud/{ns}/auth/{provider}/callback
			nsGroup.Get("/"+pname+"/callback", func(c fiber.Ctx) error {
				return oauthCallback(c, db, ns, pname, p, nsSecret)
			})

			// Shortcut on /crud/auth/{provider} for the default namespace
			if ns.IsDefault {
				auth.Get("/"+pname, func(c fiber.Ctx) error {
					state := newID()
					db.Create(&OAuthState{State: state, Provider: pname, NamespaceID: ns.ID})
					return c.Redirect().To(p.authURL(state))
				})
				auth.Get("/"+pname+"/callback", func(c fiber.Ctx) error {
					return oauthCallback(c, db, ns, pname, p, nsSecret)
				})
			}
		}
	}

	// ── Namespaces ────────────────────────────────────────────────────────────

	nsGroup := g.Group("/namespaces")
	nsGroup.Get("", func(c fiber.Ctx) error {
		var list []Namespace
		db.Find(&list)
		return c.JSON(list)
	})
	nsGroup.Get("/", func(c fiber.Ctx) error {
		var list []Namespace
		db.Find(&list)
		return c.JSON(list)
	})
	nsGroup.Get("/changes", func(c fiber.Ctx) error {
		r := rc(c)
		if r == nil || !r.IsRoot {
			return c.Status(403).JSON(fiber.Map{"error": "root only"})
		}
		c.Locals("channels", "crud::create,crud::update,crud::delete,crud::login,crud::logout")
		return sse.Handler(c)
	})
	nsGroup.Post("/", func(c fiber.Ctx) error {
		var n Namespace
		c.Bind().JSON(&n)
		n.ID = newID()
		if err := db.Create(&n).Error; err != nil {
			broadcastCRUD("reject", "", "", "", err.Error())
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		broadcastCRUD("create", n.Slug, "", n.ID, n)
		return c.Status(201).JSON(n)
	})
	nsGroup.Get("/:id/changes", func(c fiber.Ctx) error {
		r := rc(c)
		var target Namespace
		if err := db.First(&target, "id = ? OR slug = ?", c.Params("id"), c.Params("id")).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		if r != nil && !r.IsRoot && (r.Namespace == nil || r.Namespace.ID != target.ID) {
			return c.Status(403).JSON(fiber.Map{"error": "access denied"})
		}
		c.Locals("channels", fmt.Sprintf("crud:%s:create,crud:%s:update,crud:%s:delete,crud:%s:login,crud:%s:logout,crud:%s:reject", target.Slug, target.Slug, target.Slug, target.Slug, target.Slug, target.Slug))
		return sse.Handler(c)
	})
	nsGroup.Put("/:id", func(c fiber.Ctx) error {
		var n Namespace
		if err := db.First(&n, "id = ? OR slug = ?", c.Params("id"), c.Params("id")).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		c.Bind().JSON(&n)
		db.Save(&n)
		broadcastCRUD("update", n.Slug, "", n.ID, n)
		return c.JSON(n)
	})
	nsGroup.Delete("/:id", func(c fiber.Ctx) error {
		db.Delete(&Namespace{}, "id = ? OR slug = ?", c.Params("id"), c.Params("id"))
		broadcastCRUD("delete", c.Params("id"), "", "", nil)
		return c.JSON(fiber.Map{"ok": true})
	})

	// ── Roles ─────────────────────────────────────────────────────────────────
	roles := g.Group("/roles")
	roles.Get("", func(c fiber.Ctx) error {
		nsFilter := c.Query("namespace")
		q := db.Model(&Role{})
		if nsFilter != "" {
			q = q.Where("namespace_id = ?", nsFilter)
		}
		var list []Role
		q.Find(&list)
		return c.JSON(list)
	})
	roles.Get("/", func(c fiber.Ctx) error {
		nsFilter := c.Query("namespace")
		q := db.Model(&Role{})
		if nsFilter != "" {
			q = q.Where("namespace_id = ?", nsFilter)
		}
		var list []Role
		q.Find(&list)
		return c.JSON(list)
	})
	roles.Post("/", func(c fiber.Ctx) error {
		var role Role
		c.Bind().JSON(&role)
		role.ID = newID()
		if err := db.Create(&role).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		broadcastCRUD("create", "", "roles", role.ID, role)
		return c.Status(201).JSON(role)
	})
	roles.Put("/:id", func(c fiber.Ctx) error {
		var role Role
		if err := db.First(&role, "id = ?", c.Params("id")).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		c.Bind().JSON(&role)
		db.Save(&role)
		broadcastCRUD("update", "", "roles", role.ID, role)
		return c.JSON(role)
	})
	roles.Delete("/:id", func(c fiber.Ctx) error {
		db.Delete(&Role{}, "id = ?", c.Params("id"))
		broadcastCRUD("delete", "", "roles", c.Params("id"), nil)
		return c.JSON(fiber.Map{"ok": true})
	})

	// ── Users ─────────────────────────────────────────────────────────────────
	users := g.Group("/users")
	users.Get("", func(c fiber.Ctx) error {
		nsFilter := c.Query("namespace")
		q := db.Model(&User{})
		if nsFilter != "" {
			q = q.Where("namespace_id = ?", nsFilter)
		}
		var list []User
		q.Find(&list)
		out := make([]fiber.Map, len(list))
		for i := range list {
			out[i] = publicUser(&list[i])
		}
		return c.JSON(out)
	})
	users.Get("/", func(c fiber.Ctx) error {
		nsFilter := c.Query("namespace")
		q := db.Model(&User{})
		if nsFilter != "" {
			q = q.Where("namespace_id = ?", nsFilter)
		}
		var list []User
		q.Find(&list)
		out := make([]fiber.Map, len(list))
		for i := range list {
			out[i] = publicUser(&list[i])
		}
		return c.JSON(out)
	})
	users.Post("/", func(c fiber.Ctx) error {
		var user User
		c.Bind().JSON(&user)
		user.ID = newID()
		if user.PasswordHash != "" && !strings.HasPrefix(user.PasswordHash, "$2") {
			user.PasswordHash = bcryptHash(user.PasswordHash)
		}
		if err := db.Create(&user).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		broadcastCRUD("create", "", "users", user.ID, publicUser(&user))
		return c.Status(201).JSON(publicUser(&user))
	})
	users.Put("/:id", func(c fiber.Ctx) error {
		var user User
		if err := db.First(&user, "id = ?", c.Params("id")).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		c.Bind().JSON(&user)
		db.Save(&user)
		broadcastCRUD("update", "", "users", user.ID, publicUser(&user))
		return c.JSON(publicUser(&user))
	})
	users.Delete("/:id", func(c fiber.Ctx) error {
		db.Delete(&User{}, "id = ?", c.Params("id"))
		broadcastCRUD("delete", "", "users", c.Params("id"), nil)
		return c.JSON(fiber.Map{"ok": true})
	})

	// ── Schemas ───────────────────────────────────────────────────────────────
	schemas := g.Group("/_schema")
	schemas.Get("", func(c fiber.Ctx) error {
		q := db.Model(&CrudSchema{})
		if r := rc(c); r != nil && !r.IsRoot {
			q = q.Where("namespace_id = ?", r.Namespace.ID)
		}
		var list []CrudSchema
		q.Find(&list)
		return c.JSON(list)
	})
	schemas.Get("/", func(c fiber.Ctx) error {
		var list []CrudSchema
		db.Find(&list)
		return c.JSON(list)
	})

	schemaUpsert := func(c fiber.Ctx) error {
		var s CrudSchema
		c.Bind().JSON(&s)
		if s.Slug == "" {
			s.Slug = strings.ToLower(s.Name)
		}
		// Validate slug regex: /^[a-z][a-z0-9_]*/
		matched := regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(s.Slug)
		if !matched {
			return c.Status(400).JSON(fiber.Map{"error": "invalid schema name/slug (must match /^[a-z][a-z0-9_]*/)"})
		}

		var existing CrudSchema
		err := db.Where("slug = ?", s.Slug).First(&existing).Error
		if err == nil {
			// Update
			s.ID = existing.ID
			db.Save(&s)
			broadcastCRUD("update", "", "schemas", s.ID, s)
		} else {
			// Create
			s.ID = newID()
			if err := db.Create(&s).Error; err != nil {
				return c.Status(500).JSON(fiber.Map{"error": err.Error()})
			}
			broadcastCRUD("create", "", "schemas", s.ID, s)
		}
		return c.JSON(s)
	}

	schemas.Post("/", schemaUpsert)
	schemas.Put("/", schemaUpsert)

	schemas.Delete("/:id", func(c fiber.Ctx) error {
		db.Delete(&CrudSchema{}, "id = ? OR slug = ?", c.Params("id"), c.Params("id"))
		broadcastCRUD("delete", "", "schemas", c.Params("id"), nil)
		return c.JSON(fiber.Map{"ok": true})
	})

	schemas.Get("/:id", func(c fiber.Ctx) error {
		var s CrudSchema
		if err := db.First(&s, "id = ? OR slug = ?", c.Params("id"), c.Params("id")).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		return c.JSON(s)
	})

	// Field management via JSON in Fields column
	schemas.Get("/:id/_fields", func(c fiber.Ctx) error {
		var s CrudSchema
		if err := db.First(&s, "id = ? OR slug = ?", c.Params("id"), c.Params("id")).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		var fields []FieldDef
		json.Unmarshal([]byte(s.Fields), &fields)
		return c.JSON(fields)
	})

	schemas.Get("/:id/:field", func(c fiber.Ctx) error {
		var s CrudSchema
		if err := db.First(&s, "id = ? OR slug = ?", c.Params("id"), c.Params("id")).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		var fields []FieldDef
		json.Unmarshal([]byte(s.Fields), &fields)
		for _, f := range fields {
			if f.Name == c.Params("field") {
				return c.JSON(f)
			}
		}
		return c.Status(404).JSON(fiber.Map{"error": "field not found"})
	})

	fieldUpsert := func(c fiber.Ctx) error {
		var s CrudSchema
		if err := db.First(&s, "id = ? OR slug = ?", c.Params("id"), c.Params("id")).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		fieldName := c.Params("field")
		if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(fieldName) {
			return c.Status(400).JSON(fiber.Map{"error": "invalid field name"})
		}

		var fields []FieldDef
		json.Unmarshal([]byte(s.Fields), &fields)

		var newField FieldDef
		c.Bind().JSON(&newField)
		newField.Name = fieldName

		found := false
		for i, f := range fields {
			if f.Name == fieldName {
				fields[i] = newField
				found = true
				break
			}
		}
		if !found {
			fields = append(fields, newField)
		}

		fieldsJSON, _ := json.Marshal(fields)
		db.Model(&s).Update("fields", string(fieldsJSON))
		return c.JSON(newField)
	}

	schemas.Post("/:id/:field", fieldUpsert)
	schemas.Put("/:id/:field", fieldUpsert)
	schemas.Patch("/:id/:field", fieldUpsert)

	schemas.Delete("/:id/:field", func(c fiber.Ctx) error {
		var s CrudSchema
		if err := db.First(&s, "id = ? OR slug = ?", c.Params("id"), c.Params("id")).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "not found"})
		}
		var fields []FieldDef
		json.Unmarshal([]byte(s.Fields), &fields)

		newFields := fields[:0]
		for _, f := range fields {
			if f.Name != c.Params("field") {
				newFields = append(newFields, f)
			}
		}
		fieldsJSON, _ := json.Marshal(newFields)
		db.Model(&s).Update("fields", string(fieldsJSON))
		return c.JSON(fiber.Map{"ok": true})
	})
	// POST /schemas/:id/move → { "namespace": "slug-or-id" }
	schemas.Post("/:id/move", func(c fiber.Ctx) error {
		var body struct {
			Namespace string `json:"namespace"`
		}
		c.Bind().JSON(&body)
		var n Namespace
		if err := db.Where("id = ? OR slug = ?", body.Namespace, body.Namespace).First(&n).Error; err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "namespace not found"})
		}
		db.Model(&CrudSchema{}).Where("id = ? OR slug = ?", c.Params("id"), c.Params("id")).
			Update("namespace_id", n.ID)
		broadcastCRUD("move", n.Slug, "schemas", c.Params("id"), n.ID)
		return c.JSON(fiber.Map{"ok": true})
	})

	// ── Documents — per schema ────────────────────────────────────────────────
	// NOTE: /:schema MUST be registered after /schemas, /roles, /users, /auth
	// to avoid shadowing those fixed-segment routes.

	resolveSchema := func(c fiber.Ctx, r *requestCtx) (*CrudSchema, error) {
		slug := c.Params("schema")
		q := db.Where("slug = ? OR id = ?", slug, slug)
		if r != nil && !r.IsRoot {
			q = q.Where("namespace_id = ?", r.Namespace.ID)
		}
		var s CrudSchema
		if err := q.First(&s).Error; err != nil {
			return nil, fiber.NewError(404, "schema not found")
		}
		return &s, nil
	}

	docs := g.Group("/:schema")

	// GET    /:schema                — list
	docs.Get("/", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		if r != nil {
			if err := r.canAccess(s.Slug, "list"); err != nil {
				return c.Status(403).JSON(fiber.Map{"error": err.Error()})
			}
		}

		first := c.Query("first") == "true"
		last := c.Query("last") == "true"
		// Default: first=true if none specified
		if !first && !last {
			first = true
		}

		col, err := newCollection(db, s, r, baseDir)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}

		limit := int(toInt(c.Query("limit"), 50))
		offset := int(toInt(c.Query("offset"), 0))
		sort := c.Query("sort")

		// Handle first/last logic
		if first || last {
			limit = 1
			if last {
				sort = "-created_at"
			} else {
				sort = "created_at"
			}
		}

		docs, err := col.List(ListOptions{
			Limit:  limit,
			Offset: offset,
			Sort:   sort,
		})
		if err != nil {
			return apiError(c, err)
		}

		if (first || last) && len(docs) > 0 {
			return c.JSON(docs[0])
		}
		return c.JSON(docs)
	})
	docs.Get("/changes", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		if r != nil {
			if err := r.canAccess(s.Slug, "list"); err != nil {
				return c.Status(403).JSON(fiber.Map{"error": err.Error()})
			}
		}
		var targetNs Namespace
		db.First(&targetNs, "id = ?", s.NamespaceID)
		c.Locals("channels", fmt.Sprintf("crud:%s:%s:create,crud:%s:%s:update,crud:%s:%s:delete,crud:%s:%s:restore,crud:%s:%s:reject", targetNs.Slug, s.Slug, targetNs.Slug, s.Slug, targetNs.Slug, s.Slug, targetNs.Slug, s.Slug, targetNs.Slug, s.Slug))
		return sse.Handler(c)
	})

	// GET    /:schema/trash          — list trashed
	docs.Get("/trash", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		col, _ := newCollection(db, s, r, baseDir)
		list, err := col.TrashList(nil)
		if err != nil {
			return apiError(c, err)
		}
		return c.JSON(list)
	})
	docs.Get("/trash/changes", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		var targetNs Namespace
		db.First(&targetNs, "id = ?", s.NamespaceID)
		c.Locals("channels", fmt.Sprintf("crud:%s:%s:listTrash,crud:%s:%s:readTrash,crud:%s:%s:deleteTrash", targetNs.Slug, s.Slug, targetNs.Slug, s.Slug, targetNs.Slug, s.Slug))
		return sse.Handler(c)
	})

	// GET    /:schema/near           — geo near (query params: lat, lng, maxDistance)
	docs.Get("/near", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		col, _ := newCollection(db, s, r, baseDir)
		list, err := col.Near(NearOptions{
			Lat:         toFloat(c.Query("lat")),
			Lng:         toFloat(c.Query("lng")),
			MaxDistance: toFloat(c.Query("maxDistance"), 1000),
		})
		if err != nil {
			return apiError(c, err)
		}
		return c.JSON(list)
	})

	// GET    /:schema/:id            — findOne
	docs.Get("/:id", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		if r != nil {
			if err := r.canAccess(s.Slug, "read"); err != nil {
				return c.Status(403).JSON(fiber.Map{"error": err.Error()})
			}
		}
		col, _ := newCollection(db, s, r, baseDir)
		doc, err := col.FindOne(c.Params("id"))
		if err != nil {
			return apiError(c, err)
		}
		return c.JSON(doc)
	})
	docs.Get("/:id/changes", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		if r != nil {
			if err := r.canAccess(s.Slug, "read"); err != nil {
				return c.Status(403).JSON(fiber.Map{"error": err.Error()})
			}
		}
		var targetNs Namespace
		db.First(&targetNs, "id = ?", s.NamespaceID)
		id := c.Params("id")
		c.Locals("channels", fmt.Sprintf("crud:%s:%s:%s:update,crud:%s:%s:%s:delete,crud:%s:%s:%s:read,crud:%s:%s:%s:reject", targetNs.Slug, s.Slug, id, targetNs.Slug, s.Slug, id, targetNs.Slug, s.Slug, id, targetNs.Slug, s.Slug, id))
		return sse.Handler(c)
	})

	// POST/PUT   /:schema                — create
	createDoc := func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		if r != nil {
			if err := r.canAccess(s.Slug, "create"); err != nil {
				return c.Status(403).JSON(fiber.Map{"error": err.Error()})
			}
		}
		var body struct {
			Data map[string]any `json:"data"`
			Meta map[string]any `json:"meta"`
		}
		c.Bind().JSON(&body)
		col, _ := newCollection(db, s, r, baseDir)
		doc, err := col.Create(body.Data, body.Meta)
		if err != nil {
			return apiError(c, err)
		}
		return c.Status(201).JSON(doc)
	}
	docs.Post("/", createDoc)
	docs.Put("/", createDoc)

	// POST   /:schema/query          — find with complex filter
	docs.Post("/query", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		var body struct {
			Filter map[string]any `json:"filter"`
			Sort   string         `json:"sort"`
			Limit  int            `json:"limit"`
			Offset int            `json:"offset"`
		}
		c.Bind().JSON(&body)
		col, _ := newCollection(db, s, r, baseDir)
		list, err := col.Find(body.Filter, ListOptions{Sort: body.Sort, Limit: body.Limit, Offset: body.Offset})
		if err != nil {
			return apiError(c, err)
		}
		return c.JSON(list)
	})

	// POST   /:schema/within         — geo within polygon
	docs.Post("/within", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		var poly map[string]any
		c.Bind().JSON(&poly)
		col, _ := newCollection(db, s, r, baseDir)
		list, err := col.Within(WithinOptions{Polygon: poly})
		if err != nil {
			return apiError(c, err)
		}
		return c.JSON(list)
	})

	// PATCH/POST/PUT    /:schema/:id            — update
	updateDoc := func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		if r != nil {
			if err := r.canAccess(s.Slug, "update"); err != nil {
				return c.Status(403).JSON(fiber.Map{"error": err.Error()})
			}
		}
		var body struct {
			Data map[string]any `json:"data"`
			Meta map[string]any `json:"meta"`
		}
		c.Bind().JSON(&body)
		col, _ := newCollection(db, s, r, baseDir)
		doc, err := col.Update(c.Params("id"), body.Data, body.Meta)
		if err != nil {
			return apiError(c, err)
		}
		return c.JSON(doc)
	}
	docs.Patch("/:id", updateDoc)
	docs.Post("/:id", updateDoc)
	docs.Put("/:id", updateDoc)

	// DELETE /:schema/:id            — soft delete
	docs.Delete("/:id", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		if r != nil {
			if err := r.canAccess(s.Slug, "delete"); err != nil {
				return c.Status(403).JSON(fiber.Map{"error": err.Error()})
			}
		}
		col, _ := newCollection(db, s, r, baseDir)
		if err := col.Delete(c.Params("id")); err != nil {
			return apiError(c, err)
		}
		return c.JSON(fiber.Map{"ok": true})
	})

	// DELETE /:schema/trash/:id      — permanent delete
	docs.Delete("/trash/:id", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		col, _ := newCollection(db, s, r, baseDir)
		if err := col.TrashDelete(c.Params("id")); err != nil {
			return apiError(c, err)
		}
		return c.JSON(fiber.Map{"ok": true})
	})

	// POST   /:schema/trash/:id/restore
	docs.Post("/trash/:id/restore", func(c fiber.Ctx) error {
		r := rc(c)
		s, err := resolveSchema(c, r)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		col, _ := newCollection(db, s, r, baseDir)
		if err := col.Restore(c.Params("id")); err != nil {
			return apiError(c, err)
		}
		return c.JSON(fiber.Map{"ok": true})
	})

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// OAuth2 callback — shared between namespace-scoped and default routes
// ─────────────────────────────────────────────────────────────────────────────

func oauthCallback(
	c fiber.Ctx,
	db *gorm.DB,
	ns *Namespace,
	providerName string,
	p *oauth2Config,
	nsSecret string,
) error {
	code := c.Query("code")
	state := c.Query("state")

	var oas OAuthState
	if err := db.Where("state = ? AND provider = ?", state, providerName).
		First(&oas).Error; err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid state"})
	}
	db.Delete(&oas)

	accessToken, err := p.exchangeCode(c.Context(), code)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	sub, email, name, err := p.userinfo(c.Context(), accessToken)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	token, user, err := finalizeOAuth(db, ns, providerName, sub, email, name, nsSecret)
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"token": token, "user": publicUser(user)})
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func apiError(c fiber.Ctx, err error) error {
	if isRejection(err) {
		return c.Status(403).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(500).JSON(fiber.Map{"error": err.Error()})
}

func effectiveSecret(ns *Namespace, fallback string) string {
	if ns != nil && ns.JWTSecret != "" {
		return ns.JWTSecret
	}
	return fallback
}

// bcryptHash is wired at init() in module.go.
var bcryptHash func(plain string) string

var _ = json.Marshal
