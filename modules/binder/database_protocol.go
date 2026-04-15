package binder

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"http-server/modules"
	"http-server/modules/crud"
	"http-server/modules/db"
	"http-server/processor"
)

const defaultName = db.DefaultConnName

// DatabaseDirective represents the DATABASE configuration block in a binder file.
// It unifies the previous CRUD and DATABASE protocols.
type DatabaseDirective struct {
	config    *DirectiveConfig
	databases []string
	crud      *CrudProtocol
	conn      *db.Connection
	Schemas   map[string]map[string]db.SchemaType // Parsed schemas for metadata/testing
}

// CrudProtocol is a wrapper around crud.CrudDirective to override its name.
type CrudProtocol struct {
	Inner *crud.CrudDirective
}

func (p *CrudProtocol) Name() string                    { return "DATABASE" }
func (p *CrudProtocol) Address() string                 { return p.Inner.Address() }
func (p *CrudProtocol) Match(peek []byte) (bool, error) { return p.Inner.Match(peek) }
func (p *CrudProtocol) Handle(conn net.Conn) error      { return p.Inner.Handle(conn) }
func (p *CrudProtocol) Close() error                    { return p.Inner.Close() }
func (p *CrudProtocol) Start() ([]net.Listener, error)  { return p.Inner.Start() }

func init() {
	modules.RegisterModule(&db.Module{})
}

func NewDatabaseDirective(c *DirectiveConfig) (Directive, error) {
	// Check if this is a CRUD-enabled DATABASE by looking for specific keywords
	isCrud := false
	for _, r := range c.Routes {
		cmd := strings.ToUpper(r.Method)
		switch cmd {
		case "NAMESPACE", "ROLE", "OAUTH2", "ADMIN", "SCHEMA":
			isCrud = true
		case "NAME", "SECRET":
			isCrud = true
		}
		if isCrud {
			break
		}
	}

	// Unified registration for both Classic and CRUD
	processor.RegisterGlobal("database", &db.Module{}, true)

	if isCrud {
		// Initialize as a CRUD protocol but register JS as "database"
		cfg, err := parseCrudDirectiveConfig(c)
		if err != nil {
			return nil, fmt.Errorf("DATABASE (CRUD) DSL parse error: %w", err)
		}
		// Force JS name to "database" if not already set by NAME directive
		if cfg.Name == "" {
			cfg.Name = "database"
		}

		inner, err := crud.NewCrudDirective(cfg)
		if err != nil {
			return nil, err
		}
		return &DatabaseDirective{config: c, crud: &CrudProtocol{Inner: inner}, Schemas: make(map[string]map[string]db.SchemaType)}, nil
	}

	// Classic DATABASE
	return &DatabaseDirective{config: c, Schemas: make(map[string]map[string]db.SchemaType)}, nil
}

func (d *DatabaseDirective) Name() string    { return "DATABASE" }
func (d *DatabaseDirective) Address() string { return d.config.Address }

func (d *DatabaseDirective) Match(peek []byte) (bool, error) {
	return false, nil // Data protocol doesn't match network connections
}

func (d *DatabaseDirective) Handle(conn net.Conn) error {
	// TCP handler for database proxying
	return nil
}

func (d *DatabaseDirective) HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error {
	return errors.New("Database directive does not support UDP")
}

// Close closes any active global hooks.
func (d *DatabaseDirective) Close() error {
	var errs []error
	if d.crud != nil {
		if err := d.crud.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if d.conn != nil {
		if err := d.conn.Close(); err != nil {
			errs = append(errs, err)
		}
		d.conn = nil
	}

	processor.UnregisterGlobal("database")

	if len(errs) > 0 {
		return fmt.Errorf("DATABASE: Errors during close: %v", errs)
	}
	return nil
}

// Start parses the database directives and registers the connection globally.
func (d *DatabaseDirective) Start() ([]net.Listener, error) {
	if d.crud != nil {
		l, err := d.crud.Start()
		if err != nil {
			return nil, err
		}
		// For unified mode, we still want to populate d.Schemas and d.conn
		// so that the classic DB API (used in tests and some JS) still works.
		d.conn = db.GetConnection(d.config.Args.Get("name", d.crud.Inner.Config().Name))
		if d.conn == nil && d.config.Args.Get("name") == "" {
			// Try "DEFAULT" if no explicit name was given
			d.conn = db.GetConnection(db.DefaultConnName)
		}

		for _, route := range d.config.Routes {
			if route.Method == "SCHEMA" {
				name := route.Path
				if route.Routes == nil && !route.IsGroup {
					r, _, _ := route.ParseHandlerAsRoutes()
					route.Routes = []*RouteConfig{r}
				}
				d.parseSchema(d.conn, name, route.Routes)
			}
		}
		return l, nil
	}

	dbURL := d.config.Address
	log.Printf("Starting DATABASE protocol on %s, %v", dbURL, d.config.Args)
	if dbURL == ":memory:" {
		dbURL = "sqlite://:memory:"
	}
	// Remove quotes if any
	dbURL = strings.Trim(dbURL, "\"'`")
	query := url.Values{}
	query.Set("default", strconv.FormatBool(d.config.Args.GetBool("default", db.GetConnection() == nil)))
	query.Set("name", d.config.Args.Get("name", defaultName))
	query.Set("url", dbURL)
	if query.Get("name") == defaultName && db.GetConnection() == nil {
		query.Set("default", "true")
	} else if query.Get("name") == defaultName {
		return nil, fmt.Errorf("DATABASE failed to connect: %v", "default database already defined")
	}

	gormDB, err := db.FromURL(dbURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE failed to connect: %v", err)
	}

	conn := db.NewConnection(gormDB, query.Get("name"))
	d.conn = conn
	if !query.Has("default") || isTrue(query.Get("default")) {
		db.RegisterDefaultConnection(conn)
	}
	for _, route := range d.config.Routes {
		if route.Method == "SCHEMA" {
			name := route.Path
			if route.Routes == nil && !route.IsGroup {
				r, _, err := route.ParseHandlerAsRoutes()
				if err != nil {
					log.Printf("Failed to parse schema routes: %v", err)
				}
				route.Routes = []*RouteConfig{r}
			}
			d.parseSchema(conn, name, route.Routes)
			log.Printf("Schema '%s' successfully compiled and saved to DB", name)
		}
	}

	// Bulk migration for all models at once (resolves relationship ordering panics)
	if err := conn.AutoMigrate(); err != nil {
		return nil, fmt.Errorf("DATABASE failed to migrate: %v", err)
	}

	// Inject global proxy for JS instances
	fmt.Printf("Injecting global proxy for JS instances: %s\n", query.Get("name"))
	return nil, nil
}

// parseSchema parses the raw text content of a SCHEMA block.
func (d *DatabaseDirective) parseSchema(conn *db.Connection, name string, routes []*RouteConfig) {
	if d.Schemas == nil {
		d.Schemas = make(map[string]map[string]db.SchemaType)
	}
	schemaMap := make(map[string]interface{})

	var deferred []deferredItem

	for _, route := range routes {
		if route == nil {
			continue
		}
		cmd := strings.ToUpper(route.Method)
		code, _ := processor.EnsureReturnStrict(strings.TrimSpace(route.Handler))
		// Parse [key=value,...] args
		args := ""
		init := ""
		if (cmd == "METHOD" || cmd == "STATIC") && len(route.Args) >= 0 {
			for k, v := range route.Args {
				if args != "" {
					args += ", "
				}
				if init != "" {
					init += "\n"
				}
				if isBool(v) && isTrue(v) {
					args += k
				} else {
					init += k + " = k == undefined ? " + v + " : " + k + "\n"
				}
			}
		}

		switch cmd {
		case "FIELD":
			fieldName := route.Path
			if route.Inline {
				// virtual field (computed from inline code)
				deferred = append(deferred, deferredItem{"VIRTUAL", fieldName, code, ""})
			} else {
				fType := fieldType(route)
				fieldMap := map[string]interface{}{
					"type": fType,
				}

				rawType := ""
				if route.Handler != "" && !route.Inline {
					rawType = strings.TrimSpace(route.Handler)
				}
				if route.ContentType != "" {
					rawType = route.ContentType
				}

				refVal := route.Args.Get("ref")
				if refVal == "" && strings.Contains(rawType, ".") {
					refVal = rawType
				}

				if refVal != "" {
					fieldMap["ref"] = refVal
					fieldMap["type"] = "string" // Default type for the FK column

					hasVal := route.Args.Get("has", "one")
					if !slices.Contains([]string{"one", "many", "many2many", "many_to_many", "one_to_many", "one_to_one"}, hasVal) {
						hasVal = "one"
					}
					if hasVal == "many_to_many" {
						hasVal = "many2many"
					} else if hasVal == "one_to_many" {
						hasVal = "many"
					} else if hasVal == "one_to_one" {
						hasVal = "one"
					}
					fieldMap["has"] = hasVal
					fieldMap["delete"] = strings.ToUpper(route.Args.Get("delete", "SET NULL"))
					fieldMap["update"] = strings.ToUpper(route.Args.Get("update", "CASCADE"))
				}

				// Parse other [key=value,...] args
				if len(route.Args) >= 0 {
					for k, v := range route.Args {
						if k == "type" || k == "has" || k == "ref" || k == "delete" || k == "update" {
							continue
						}
						if isBool(v) {
							fieldMap[k] = isTrue(v)
						} else {
							fieldMap[k] = v
						}
					}
				}

				schemaMap[fieldName] = fieldMap
			}
		case "VIRTUAL":
			deferred = append(deferred, deferredItem{"VIRTUAL", route.Path, code, ""})
		case "HOOK":
			action := "PRE"
			if m := route.Middleware("POST"); m != nil {
				action = "POST"
			}
			deferred = append(deferred, deferredItem{action, route.Path, code, ""})
		case "METHOD":
			deferred = append(deferred, deferredItem{"METHOD", route.Path, init + code, args})
		case "STATIC":
			deferred = append(deferred, deferredItem{"STATIC", route.Path, init + code, args})
		}
	}

	// Phase 1: Create the Model with field schema (this creates the base schema)
	// We use CreateModel to skip individual migrations that causes panics with relationships
	var m *db.Model
	if conn != nil {
		m = conn.CreateModel(name, schemaMap)
	}

	// Convert map to SchemaType for metadata storage
	parsedSchema := make(map[string]db.SchemaType)
	for k, v := range schemaMap {
		if vm, ok := v.(map[string]interface{}); ok {
			st := db.SchemaType{Type: "string"}
			if t, ok := vm["type"].(string); ok {
				st.Type = t
			}
			if r, ok := vm["ref"].(string); ok {
				st.Ref = r
			}
			if h, ok := vm["has"].(string); ok {
				st.Has = h
			}
			if d, ok := vm["delete"].(string); ok {
				st.OnDelete = d
			}
			if u, ok := vm["update"].(string); ok {
				st.OnUpdate = u
			}
			parsedSchema[k] = st
		}
	}
	d.Schemas[name] = parsedSchema

	if conn == nil || m == nil {
		return
	}

	// Phase 2: Add methods, virtuals, statics, middleware to the existing schema
	for _, item := range deferred {
		switch item.itemType {
		case "VIRTUAL":
			m.AddVirtual(item)
		case "PRE", "POST":
			m.AddMiddleware(item.itemType == "PRE", item)
		case "METHOD":
			m.AddMethod(item)
		case "STATIC":
			m.AddStatic(item)
		}
	}
}

// Deferred items to add after conn.Model() creates the base schema
type deferredItem struct {
	itemType string // "VIRTUAL", "PRE", "POST", "METHOD", "STATIC"
	name     string
	code     string
	args     string // for STATIC
}

// GetCode implements [db.Item].
func (d deferredItem) GetCode() string {
	async := ""
	if strings.Contains(d.code, "await ") {
		async = "async "
	}
	c, _ := processor.EnsureReturnStrict(d.code)
	return fmt.Sprintf("%sfunction(%s) { with(this){ %s; } }", async, d.args, c)
}

// GetItemType implements [db.Item].
func (d deferredItem) GetItemType() string {
	return d.itemType
}

// GetName implements [db.Item].
func (d deferredItem) GetName() string {
	return d.name
}

// ─────────────────────────────────────────────────────────────────────────────
// CRUD Parsing logic (migrated from crud_protocol.go)
// ─────────────────────────────────────────────────────────────────────────────

func parseCrudDirectiveConfig(config *DirectiveConfig) (*crud.CrudDirectiveConfig, error) {
	cfg := &crud.CrudDirectiveConfig{
		Address:   strings.Trim(config.Address, "\"'`"),
		BaseDir:   config.BaseDir,
		IsDefault: config.Args.GetBool("default", false),
		Auth:      config.Auth,
	}

	if config.AppConfig != nil {
		cfg.AppSecret = config.AppConfig.SecretKey
	}

	for _, r := range config.Routes {
		cmd := strings.ToUpper(r.Method)
		switch cmd {
		case "NAME":
			cfg.Name = r.Path
		case "SECRET":
			cfg.Secret = strings.Trim(r.Path, "\"'`")
		case "OAUTH2":
			if !r.IsGroup {
				log.Printf("DATABASE: OAUTH2 %s: expected DEFINE block, skipping", r.Path)
				continue
			}
			o, err := parseOAuth2Block(r)
			if err != nil {
				return nil, fmt.Errorf("OAUTH2 %s: %w", r.Path, err)
			}
			cfg.OAuth2s = append(cfg.OAuth2s, o)
		case "NAMESPACE":
			if !r.IsGroup {
				log.Printf("DATABASE: NAMESPACE %s: expected DEFINE block, skipping", r.Path)
				continue
			}
			ns := parseNamespaceBlock(r)
			cfg.Namespaces = append(cfg.Namespaces, ns)
		case "ROLE":
			if !r.IsGroup {
				log.Printf("DATABASE: ROLE %s: expected DEFINE block, skipping", r.Path)
				continue
			}
			role := parseRoleBlock(r)
			cfg.Roles = append(cfg.Roles, role)
		case "SCHEMA":
			if !r.IsGroup {
				continue
			}
			s := parseSchemaBlock(r)
			cfg.Schemas = append(cfg.Schemas, s)
		case "ADMIN":
			if !r.IsGroup {
				log.Printf("DATABASE: ADMIN: expected block, skipping")
				continue
			}
			parseAdminBlock(r, cfg)
		}
	}

	if cfg.Name == "" {
		cfg.Name = "database"
	}

	return cfg, nil
}

func parseOAuth2Block(r *RouteConfig) (crud.DSLOAuth2, error) {
	o := crud.DSLOAuth2{
		Name:     r.Path,
		FieldMap: make(map[string]string),
	}
	for _, child := range r.Routes {
		cmd := strings.ToUpper(child.Method)
		val := strings.Trim(child.Path, "\"'`")
		switch cmd {
		case "CLIENTID":
			o.ClientID = val
		case "CLIENTSECRET":
			o.ClientSecret = val
		case "REDIRECTURL":
			o.RedirectURL = val
		case "ENDPOINT":
			o.Endpoint = val
		case "TOKENURL":
			o.TokenURL = val
		case "USERINFOURL":
			o.UserinfoURL = val
		case "SCOPE":
			o.Scopes = append(o.Scopes, val)
		case "MAPFIELD":
			o.FieldMap[child.Path] = strings.Trim(child.Handler, "\"'`")
		}
	}
	if o.ClientID == "" || o.Endpoint == "" || o.TokenURL == "" || o.UserinfoURL == "" {
		return o, fmt.Errorf("missing OAUTH2 configuration fields")
	}
	return o, nil
}

func parseNamespaceBlock(r *RouteConfig) crud.DSLNamespace {
	ns := crud.DSLNamespace{
		Slug:    r.Path,
		Name:    titleCase(r.Path),
		Default: r.Args.GetBool("default"),
	}
	if v := r.Args.Get("auth"); v != "" {
		for _, p := range strings.Split(v, ",") {
			ns.AuthProviders = append(ns.AuthProviders, strings.TrimSpace(p))
		}
	}
	if len(ns.AuthProviders) == 0 {
		ns.AuthProviders = []string{"password"}
	}
	for _, child := range r.Routes {
		if strings.ToUpper(child.Method) == "HOOK" {
			applyHook(&ns.Hooks, child)
		}
	}
	return ns
}

func parseRoleBlock(r *RouteConfig) crud.DSLRole {
	role := crud.DSLRole{
		Name:          r.Path,
		NamespaceSlug: r.Args.Get("namespace", "global"),
	}
	for _, child := range r.Routes {
		if strings.ToUpper(child.Method) != "PERMISSION" {
			continue
		}
		resource := child.Path
		if resource == "" {
			resource = "*"
		}
		var actions []string
		if v := child.Args.Get("actions", "*"); v != "" {
			for _, a := range strings.Split(v, ",") {
				actions = append(actions, strings.TrimSpace(a))
			}
		}
		role.Permissions = append(role.Permissions, crud.Permission{
			Resource: resource,
			Actions:  actions,
		})
	}
	return role
}

func parseSchemaBlock(r *RouteConfig) crud.DSLCrudSchema {
	s := crud.DSLCrudSchema{
		Slug:       r.Path,
		Name:       titleCase(r.Path),
		Namespace:  r.Args.Get("namespace", "global"),
		Icon:       r.Args.Get("icon"),
		Color:      r.Args.Get("color"),
		SoftDelete: r.Args.GetBool("softDelete"),
	}
	for _, child := range r.Routes {
		cmd := strings.ToUpper(child.Method)
		switch cmd {
		case "FIELD":
			rawType := ""
			if child.Handler != "" && !child.Inline {
				rawType = strings.TrimSpace(child.Handler)
			}
			if child.ContentType != "" {
				rawType = child.ContentType
			}

			f := crud.DSLField{
				Name:     child.Path,
				Type:     fieldType(child),
				Required: child.Args.GetBool("required"),
				Index:    child.Args.GetBool("index"),
				Unique:   child.Args.GetBool("unique"),
			}

			if strings.Contains(rawType, ".") {
				// Detect Relationship [Schema].[Field]
				f.Ref = rawType
				f.Has = child.Args.Get("has", "one")
				if !slices.Contains([]string{"one", "many", "many2many", "many_to_many", "one_to_many", "one_to_one"}, f.Has) {
					f.Has = "one"
				}
				if f.Has == "many_to_many" {
					f.Has = "many2many"
				} else if f.Has == "one_to_many" {
					f.Has = "many"
				} else if f.Has == "one_to_one" {
					f.Has = "one"
				}
				f.OnDelete = strings.ToUpper(child.Args.Get("delete", "SET NULL"))
				f.OnUpdate = strings.ToUpper(child.Args.Get("update", "CASCADE"))
			}

			if v := child.Args.Get("default"); v != "" {
				f.Default = v
			}
			s.Fields = append(s.Fields, f)
		case "HOOK":
			applyHook(&s.Hooks, child)
		}
	}
	return s
}

func parseAdminBlock(r *RouteConfig, cfg *crud.CrudDirectiveConfig) {
	for _, child := range r.Routes {
		cmd := strings.ToUpper(child.Method)
		switch cmd {
		case "PAGE":
			p := crud.AdminPage{
				Path:  strings.Trim(child.Path, "\"'`"),
				Title: child.Args.Get("title", "Page"),
				Icon:  child.Args.Get("icon"),
				Order: child.Args.GetInt("order", 0),
			}
			if child.Inline {
				p.Template = child.Handler
			}
			cfg.AdminPages = append(cfg.AdminPages, p)
		case "LINK":
			l := crud.AdminLink{
				URL:   strings.Trim(child.Path, "\"'`"),
				Title: child.Args.Get("title", "Link"),
				Icon:  child.Args.Get("icon"),
				Order: child.Args.GetInt("order", 0),
			}
			cfg.AdminLinks = append(cfg.AdminLinks, l)
		}
	}
}

func applyHook(hs *crud.HookSet, r *RouteConfig) {
	action := r.Path
	code := r.Handler
	isFile := !r.Inline && code != ""
	switch strings.ToLower(action) {
	case "onlist":
		hs.OnList, hs.OnListFile = code, isFile
	case "onread":
		hs.OnRead, hs.OnReadFile = code, isFile
	case "oncreate":
		hs.OnCreate, hs.OnCreateFile = code, isFile
	case "onupdate":
		hs.OnUpdate, hs.OnUpdateFile = code, isFile
	case "ondelete":
		hs.OnDelete, hs.OnDeleteFile = code, isFile
	case "onlisttrash":
		hs.OnListTrash, hs.OnListTrashFile = code, isFile
	case "onreadtrash":
		hs.OnReadTrash, hs.OnReadTrashFile = code, isFile
	case "ondeletetrash":
		hs.OnDeleteTrash, hs.OnDeleteTrashFile = code, isFile
	}
}

func fieldType(r *RouteConfig) string {
	if r.ContentType != "" {
		return strings.ToLower(r.ContentType)
	}
	if r.Handler != "" && !r.Inline {
		t := strings.ToLower(strings.TrimSpace(r.Handler))
		switch t {
		case "string", "number", "boolean", "bool", "date", "datetime", "geo", "array", "object", "int", "float", "text":
			return t
		}
	}
	return "string"
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
