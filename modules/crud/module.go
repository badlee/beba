package crud

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"http-server/modules"
	"http-server/plugins/httpserver"
	"http-server/processor"
	"http-server/types"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	dbpkg "http-server/modules/db"
	"http-server/modules/sse"
)

func init() {
	// Wire bcrypt helpers used in auth.go and routes.go
	bcryptCompare = func(hashed, plain string) bool {
		return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)) == nil
	}
	bcryptHash = func(plain string) string {
		b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
		if err != nil {
			return plain
		}
		return string(b)
	}

	modules.RegisterModule(&CrudModule{})
}

// ─────────────────────────────────────────────────────────────────────────────
// CrudInstance — one running CRUD directive
// ─────────────────────────────────────────────────────────────────────────────

// CrudInstance is created by the binder directive and holds all runtime state.
type CrudInstance struct {
	name      string
	db        *gorm.DB
	secret    string
	providers map[string]*oauth2Config
	baseDir   string
	cfg       *CrudDirectiveConfig
}

var (
	crudInstances       = make(map[string]*CrudInstance)
	defaultCrudInstance *CrudInstance
)

func registerCrudInstance(name string, inst *CrudInstance, isDefault bool) {
	crudInstances[name] = inst
	if isDefault || defaultCrudInstance == nil {
		defaultCrudInstance = inst
	}
}

func GetCrudInstance(name ...string) *CrudInstance {
	if len(name) == 0 || name[0] == "" {
		return defaultCrudInstance
	}
	return crudInstances[name[0]]
}

func computeDiff(prev, next map[string]any) map[string]any {
	diff := make(map[string]any)
	for k, v := range next {
		if k == "updated_at" {
			continue
		}
		pv, ok := prev[k]
		if !ok || fmt.Sprint(pv) != fmt.Sprint(v) {
			diff[k] = v
		}
	}
	for k := range prev {
		if _, ok := next[k]; !ok {
			diff[k] = nil
		}
	}
	if len(diff) == 0 {
		return nil
	}
	return diff
}

func broadcastCRUD(action string, nsSlug string, schemaSlug string, docID string, data any, prevData ...any) {
	if sse.HubInstance == nil {
		return
	}
	m := map[string]any{
		"action":    action,
		"namespace": nsSlug,
		"schema":    schemaSlug,
		"id":        docID,
		"data":      data,
		"time":      time.Now(),
	}
	if len(prevData) > 0 && prevData[0] != nil {
		m["prev"] = prevData[0]
		if next, ok := data.(map[string]any); ok {
			if prev, ok := prevData[0].(map[string]any); ok {
				m["diff"] = computeDiff(prev, next)
			}
		}
	}

	payload, _ := json.Marshal(m)

	// 1. crud::{action}
	sse.HubInstance.Publish(&sse.Message{
		Event:   "crud",
		Data:    string(payload),
		Channel: "crud::" + action,
	})

	if nsSlug != "" {
		// 2. crud:{namespace}:{action}
		sse.HubInstance.Publish(&sse.Message{
			Event:   "crud",
			Data:    string(payload),
			Channel: fmt.Sprintf("crud:%s:%s", nsSlug, action),
		})

		if schemaSlug != "" {
			// 3. crud:{namespace}:{schema}:{action}
			sse.HubInstance.Publish(&sse.Message{
				Event:   "crud",
				Data:    string(payload),
				Channel: fmt.Sprintf("crud:%s:%s:%s", nsSlug, schemaSlug, action),
			})

			if docID != "" {
				// 4. crud:{namespace}:{schema}:{document}:{action}
				sse.HubInstance.Publish(&sse.Message{
					Event:   "crud",
					Data:    string(payload),
					Channel: fmt.Sprintf("crud:%s:%s:%s:%s", nsSlug, schemaSlug, docID, action),
				})
			}
		}
	}
}

// MountOn mounts the CrudInstance routes onto a *httpserver.HTTP at the given prefix.
// Called by http_protocol.go when it encounters a CRUD directive inside an HTTP block:
//
//	HTTP :8080
//	    CRUD crud /api
//	END HTTP
func MountOn(app *httpserver.HTTP, inst *CrudInstance, prefix string) error {
	return mountRoutes(app, prefix, inst.db, inst.providers, inst.secret, inst.baseDir, inst.cfg.Auth)
}

// ─────────────────────────────────────────────────────────────────────────────
// CrudDirective — implements binder.Directive
// ─────────────────────────────────────────────────────────────────────────────

// DirectiveConfig is a type alias to avoid importing the binder package.
// The binder package calls NewCrudDirective(cfg) where cfg is *binder.DirectiveConfig.
// We use interface{} and type-assert inside to keep this package self-contained.

type CrudDirectiveConfig struct {
	Address   string
	Name      string
	BaseDir   string
	Secret    string
	IsDefault bool
	Auth      types.Authentification
	AppSecret string // from AppConfig.SecretKey

	// Parsed DSL blocks
	Namespaces []DSLNamespace
	Roles      []DSLRole
	Schemas    []DSLCrudSchema
	OAuth2s    []DSLOAuth2
	AdminPages []AdminPage
	AdminLinks []AdminLink
}

type DSLNamespace struct {
	Slug          string
	Name          string
	Default       bool
	AuthProviders []string
	Hooks         HookSet
}

type DSLRole struct {
	Name          string
	NamespaceSlug string
	Permissions   []Permission
}

type DSLField struct {
	Name     string
	Type     string
	Required bool
	Default  interface{}
	Index    bool
	Unique   bool
	Ref      string
	Has      string
	OnDelete string
	OnUpdate string
}

type DSLCrudSchema struct {
	Name       string
	Slug       string
	Namespace  string
	Icon       string
	Color      string
	SoftDelete bool
	Fields     []DSLField
	Hooks      HookSet
}

type DSLOAuth2 struct {
	Name         string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Endpoint     string
	TokenURL     string
	UserinfoURL  string
	Scopes       []string
	FieldMap     map[string]string
}

// CrudDirective is the binder.Directive implementation.
type CrudDirective struct {
	cfg  *CrudDirectiveConfig
	inst *CrudInstance
	db   *gorm.DB
	conn *dbpkg.Connection
}

func NewCrudDirective(cfg *CrudDirectiveConfig) (*CrudDirective, error) {
	return &CrudDirective{cfg: cfg}, nil
}

func (d *CrudDirective) Config() *CrudDirectiveConfig { return d.cfg }

func (d *CrudDirective) Name() string                    { return "CRUD" }
func (d *CrudDirective) Address() string                 { return d.cfg.Address }
func (d *CrudDirective) Match(peek []byte) (bool, error) { return false, nil }
func (d *CrudDirective) Handle(conn net.Conn) error      { return nil }
func (d *CrudDirective) Close() error {
	if d.conn != nil {
		return d.conn.Close()
	}
	if d.db != nil {
		sqlDB, err := d.db.DB()
		if err == nil {
			return sqlDB.Close()
		}
	}
	return nil
}

func (d *CrudDirective) Start() ([]net.Listener, error) {
	cfg := d.cfg

	// Clear previous admin configurations (for hot-reload)
	ClearAdminRegistry()

	// Register DSL Admin custom pages and links
	for _, p := range cfg.AdminPages {
		RegisterAdminPage(p)
	}
	for _, l := range cfg.AdminLinks {
		RegisterAdminLink(l)
	}

	// ── Connect to DB ─────────────────────────────────────────────────────
	rawURL := strings.Trim(cfg.Address, "\"'`")
	gormDB, err := dbpkg.FromURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("CRUD %s: db connect: %w", cfg.Name, err)
	}
	d.db = gormDB

	if err := Migrate(gormDB); err != nil {
		return nil, fmt.Errorf("CRUD %s: migrate: %w", cfg.Name, err)
	}
	if err := Seed(gormDB); err != nil {
		return nil, fmt.Errorf("CRUD %s: seed: %w", cfg.Name, err)
	}

	secret := cfg.Secret
	if secret == "" {
		secret = cfg.AppSecret
	}
	if secret == "" {
		secret = "changeme"
	}

	inst := &CrudInstance{
		name:      cfg.Name,
		db:        gormDB,
		secret:    secret,
		providers: make(map[string]*oauth2Config),
		baseDir:   cfg.BaseDir,
		cfg:       cfg,
	}

	// ── Apply DSL: OAuth2 providers ───────────────────────────────────────
	for _, o := range cfg.OAuth2s {
		prov := &OAuth2Provider{
			ID:           newID(),
			Name:         o.Name,
			ClientID:     o.ClientID,
			ClientSecret: o.ClientSecret,
			RedirectURL:  o.RedirectURL,
			Endpoint:     o.Endpoint,
			TokenURL:     o.TokenURL,
			UserinfoURL:  o.UserinfoURL,
			Scopes:       strings.Join(o.Scopes, ","),
			CreatedAt:    time.Now(),
		}
		if o.FieldMap != nil {
			b, _ := json.Marshal(o.FieldMap)
			prov.FieldMap = string(b)
		}
		gormDB.Where("name = ?", o.Name).Assign(*prov).FirstOrCreate(prov)

		inst.providers[o.Name] = &oauth2Config{
			Name:         o.Name,
			ClientID:     o.ClientID,
			ClientSecret: o.ClientSecret,
			RedirectURL:  o.RedirectURL,
			Endpoint:     o.Endpoint,
			TokenURL:     o.TokenURL,
			UserinfoURL:  o.UserinfoURL,
			Scopes:       o.Scopes,
			FieldMap:     o.FieldMap,
		}
	}

	// ── Apply DSL: Namespaces ─────────────────────────────────────────────
	for _, dn := range cfg.Namespaces {
		hooksJSON, _ := json.Marshal(dn.Hooks)
		ns := Namespace{
			ID:            newID(),
			Name:          dn.Name,
			Slug:          dn.Slug,
			AuthProviders: strings.Join(dn.AuthProviders, ","),
			Hooks:         string(hooksJSON),
			IsDefault:     dn.Default,
			JWTSecret:     "",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		gormDB.Where("slug = ?", dn.Slug).Assign(ns).FirstOrCreate(&ns)
	}

	// ── Apply DSL: Roles ──────────────────────────────────────────────────
	for _, dr := range cfg.Roles {
		var ns Namespace
		if err := gormDB.Where("slug = ?", dr.NamespaceSlug).First(&ns).Error; err != nil {
			log.Printf("CRUD %s: role %s: namespace %q not found", cfg.Name, dr.Name, dr.NamespaceSlug)
			continue
		}
		permsJSON, _ := json.Marshal(dr.Permissions)
		role := Role{
			ID:          newID(),
			Name:        dr.Name,
			NamespaceID: ns.ID,
			Permissions: string(permsJSON),
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		gormDB.Where("name = ? AND namespace_id = ?", dr.Name, ns.ID).
			Assign(role).FirstOrCreate(&role)
	}

	// ── Apply DSL: Schemas ────────────────────────────────────────────────
	for _, ds := range cfg.Schemas {
		var ns Namespace
		nsSlug := ds.Namespace
		if nsSlug == "" {
			nsSlug = "global"
		}
		if err := gormDB.Where("slug = ?", nsSlug).First(&ns).Error; err != nil {
			log.Printf("CRUD %s: schema %s: namespace %q not found", cfg.Name, ds.Slug, nsSlug)
			continue
		}
		fieldsJSON, _ := json.Marshal(ds.Fields)
		hooksJSON, _ := json.Marshal(ds.Hooks)
		cs := CrudSchema{
			ID:          newID(),
			Name:        ds.Name,
			Slug:        ds.Slug,
			Icon:        ds.Icon,
			Color:       ds.Color,
			NamespaceID: ns.ID,
			SoftDelete:  ds.SoftDelete,
			Fields:      string(fieldsJSON),
			Hooks:       string(hooksJSON),
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		gormDB.Where("slug = ? AND namespace_id = ?", ds.Slug, ns.ID).
			Assign(cs).FirstOrCreate(&cs)
	}

	// Routes are NOT auto-mounted here.
	// Use  CRUD [name] [/mount/path]  inside an HTTP block to attach this
	// instance to a specific fiber server at the desired prefix.
	registerCrudInstance(cfg.Name, inst, cfg.IsDefault)

	// Regiser connection globally so other protocols (like MQTT STORAGE) can find it
	conn := dbpkg.NewConnection(gormDB, cfg.Name)
	d.conn = conn
	if cfg.IsDefault {
		dbpkg.RegisterDefaultConnection(conn)
	}

	processor.RegisterGlobal(cfg.Name, &CrudModule{}, true)

	log.Printf("CRUD: instance %q started (%s)", cfg.Name, rawURL)
	return nil, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CrudModule — JS require('crud')
// ─────────────────────────────────────────────────────────────────────────────

type CrudModule struct{}

func (m *CrudModule) Name() string { return "database" }
func (m *CrudModule) Doc() string  { return "CRUD module — schemas, documents, auth" }

func (m *CrudModule) ToJSObject(vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()
	m.Loader(nil, vm, obj)
	return obj
}

func (m *CrudModule) Loader(_ any, vm *goja.Runtime, moduleObject *goja.Object) {
	// CommonJS support: if exports exists, use it as the target
	module := moduleObject
	if exp := moduleObject.Get("exports"); exp != nil && !goja.IsUndefined(exp) {
		module = exp.ToObject(vm)
	}

	inst := defaultCrudInstance

	// ── login / logout ────────────────────────────────────────────────────

	module.Set("login", makLoginFn(vm, inst, ""))

	module.Set("logout", func(call goja.FunctionCall) goja.Value {
		tok := ""
		if len(call.Arguments) > 0 {
			tok = call.Arguments[0].String()
		}
		if tok == "" || inst == nil {
			return vm.ToValue(false)
		}
		claims, err := parseJWT(tok, inst.secret)
		if err != nil {
			return vm.ToValue(false)
		}
		revokeSession(inst.db, claims.ID)
		return vm.ToValue(true)
	})

	// ── ns(slug).login(...) ───────────────────────────────────────────────
	module.Set("ns", func(slug string) goja.Value {
		obj := vm.NewObject()
		obj.Set("login", makLoginFn(vm, inst, slug))
		obj.Set("collection", makeCollectionFn(vm, inst, slug))
		return obj
	})

	// ── collection(schema, ns?) ───────────────────────────────────────────
	module.Set("collection", makeCollectionFn(vm, inst, ""))

	// ── schemas ───────────────────────────────────────────────────────────
	schemasObj := vm.NewObject()

	schemasObj.Set("list", func(call goja.FunctionCall) goja.Value {
		if inst == nil {
			return vm.ToValue([]interface{}{})
		}
		nsFilter := ""
		if len(call.Arguments) > 0 {
			if m, ok := call.Arguments[0].Export().(map[string]interface{}); ok {
				if v, ok := m["namespace"].(string); ok {
					nsFilter = v
				}
			}
		}
		q := inst.db.Model(&CrudSchema{})
		if nsFilter != "" {
			q = q.Where("namespace_id = ?", nsFilter)
		}
		var list []CrudSchema
		q.Find(&list)
		return vm.ToValue(list)
	})

	schemasObj.Set("get", func(id string) goja.Value {
		if inst == nil {
			return goja.Null()
		}
		var s CrudSchema
		if err := inst.db.Where("id = ? OR slug = ?", id, id).First(&s).Error; err != nil {
			return goja.Null()
		}
		return vm.ToValue(s)
	})

	schemasObj.Set("create", func(call goja.FunctionCall) goja.Value {
		if inst == nil || len(call.Arguments) == 0 {
			return goja.Null()
		}
		m, _ := call.Arguments[0].Export().(map[string]interface{})
		s := CrudSchema{
			ID:        newID(),
			Name:      mapStrVal(m, "name"),
			Slug:      mapStrVal(m, "slug"),
			Icon:      mapStrVal(m, "icon"),
			Color:     mapStrVal(m, "color"),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if fields, ok := m["fields"]; ok {
			b, _ := json.Marshal(fields)
			s.Fields = string(b)
		}
		if hooks, ok := m["hooks"]; ok {
			b, _ := json.Marshal(hooks)
			s.Hooks = string(b)
		}
		inst.db.Create(&s)
		return vm.ToValue(s)
	})

	schemasObj.Set("update", func(id string, patch goja.Value) goja.Value {
		if inst == nil {
			return goja.Null()
		}
		var s CrudSchema
		if err := inst.db.Where("id = ? OR slug = ?", id, id).First(&s).Error; err != nil {
			return goja.Null()
		}
		if m, ok := patch.Export().(map[string]interface{}); ok {
			if v := mapStrVal(m, "name"); v != "" {
				s.Name = v
			}
			if v := mapStrVal(m, "icon"); v != "" {
				s.Icon = v
			}
			if v := mapStrVal(m, "color"); v != "" {
				s.Color = v
			}
		}
		s.UpdatedAt = time.Now()
		inst.db.Save(&s)
		return vm.ToValue(s)
	})

	schemasObj.Set("delete", func(id string) goja.Value {
		if inst == nil {
			return vm.ToValue(false)
		}
		inst.db.Delete(&CrudSchema{}, "id = ? OR slug = ?", id, id)
		return vm.ToValue(true)
	})

	schemasObj.Set("move", func(schemaID, nsID string) goja.Value {
		if inst == nil {
			return vm.ToValue(false)
		}
		var ns Namespace
		if err := inst.db.Where("id = ? OR slug = ?", nsID, nsID).First(&ns).Error; err != nil {
			return vm.ToValue(false)
		}
		inst.db.Model(&CrudSchema{}).Where("id = ? OR slug = ?", schemaID, schemaID).
			Update("namespace_id", ns.ID)
		return vm.ToValue(true)
	})

	module.Set("schemas", schemasObj)

	// ── namespaces ────────────────────────────────────────────────────────
	namespacesObj := vm.NewObject()
	namespacesObj.Set("list", func() goja.Value {
		if inst == nil {
			return vm.ToValue([]interface{}{})
		}
		var list []Namespace
		inst.db.Find(&list)
		return vm.ToValue(list)
	})
	namespacesObj.Set("create", func(call goja.FunctionCall) goja.Value {
		if inst == nil || len(call.Arguments) == 0 {
			return goja.Null()
		}
		m, _ := call.Arguments[0].Export().(map[string]interface{})
		ns := Namespace{
			ID:        newID(),
			Name:      mapStrVal(m, "name"),
			Slug:      mapStrVal(m, "slug"),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		inst.db.Create(&ns)
		return vm.ToValue(ns)
	})
	module.Set("namespaces", namespacesObj)

	// ── users ─────────────────────────────────────────────────────────────
	usersObj := vm.NewObject()
	usersObj.Set("list", func(call goja.FunctionCall) goja.Value {
		if inst == nil {
			return vm.ToValue([]interface{}{})
		}
		var list []User
		inst.db.Find(&list)
		out := make([]fiber.Map, len(list))
		for i := range list {
			out[i] = publicUser(&list[i])
		}
		return vm.ToValue(out)
	})
	usersObj.Set("create", func(call goja.FunctionCall) goja.Value {
		if inst == nil || len(call.Arguments) == 0 {
			return goja.Null()
		}
		m, _ := call.Arguments[0].Export().(map[string]interface{})
		u := User{
			ID:           newID(),
			Username:     mapStrVal(m, "username"),
			Email:        mapStrVal(m, "email"),
			PasswordHash: bcryptHash(mapStrVal(m, "password")),
			IsActive:     true,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		inst.db.Create(&u)
		return vm.ToValue(publicUser(&u))
	})
	module.Set("users", usersObj)

	// ── roles ─────────────────────────────────────────────────────────────
	rolesObj := vm.NewObject()
	rolesObj.Set("list", func() goja.Value {
		if inst == nil {
			return vm.ToValue([]interface{}{})
		}
		var list []Role
		inst.db.Find(&list)
		return vm.ToValue(list)
	})
	module.Set("roles", rolesObj)

	// ── hasDefault / default accessors ────────────────────────────────────
	module.DefineAccessorProperty("hasDefault",
		vm.ToValue(func(goja.FunctionCall) goja.Value {
			return vm.ToValue(defaultCrudInstance != nil)
		}), goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE)

	module.DefineAccessorProperty("default",
		vm.ToValue(func(goja.FunctionCall) goja.Value {
			if defaultCrudInstance == nil {
				vm.Interrupt(vm.NewGoError(fmt.Errorf("crud: no default instance")))
				return goja.Undefined()
			}
			return vm.ToValue(defaultCrudInstance.name)
		}), goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE)
}

// ─────────────────────────────────────────────────────────────────────────────
// JS helpers
// ─────────────────────────────────────────────────────────────────────────────

// makLoginFn returns the login function for a specific namespace (or default).
func makLoginFn(vm *goja.Runtime, inst *CrudInstance, nsSlug string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if inst == nil {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("crud: no instance")))
			return goja.Undefined()
		}
		if len(call.Arguments) < 2 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("login(identity, options) requires 2 arguments")))
			return goja.Undefined()
		}
		identity := call.Arguments[0].String()
		opts, _ := call.Arguments[1].Export().(map[string]interface{})

		// Resolve namespace
		slug := nsSlug
		if slug == "" {
			// check opts.namespace
			if v, ok := opts["namespace"].(string); ok {
				slug = v
			}
		}
		var ns Namespace
		if slug == "" {
			inst.db.Where("is_default = ?", true).First(&ns)
		} else {
			inst.db.Where("slug = ? OR id = ?", slug, slug).First(&ns)
		}
		if ns.ID == "" {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("crud login: namespace not found")))
			return goja.Undefined()
		}

		secret := effectiveSecret(&ns, inst.secret)

		// Check provider
		provider, _ := opts["provider"].(string)
		if provider == "" {
			provider = "password"
		}

		if !strings.Contains(ns.AuthProviders, provider) {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("crud login: provider %q not enabled for namespace %q", provider, ns.Slug)))
			return goja.Undefined()
		}

		var (
			token string
			user  *User
			err   error
		)

		if provider == "password" {
			password, _ := opts["password"].(string)
			token, user, err = loginPassword(inst.db, ns.ID, identity, password, secret)
		} else {
			// OAuth2 sub-based login (frontend has already verified the token)
			sub, _ := opts["sub"].(string)
			email := identity
			name, _ := opts["name"].(string)
			token, user, err = finalizeOAuth(inst.db, &ns, provider, sub, email, name, secret)
		}

		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}

		result := vm.NewObject()
		result.Set("token", token)
		result.Set("user", publicUser(user))
		return result
	}
}

// makeCollectionFn returns the collection(schema, ns?) function.
func makeCollectionFn(vm *goja.Runtime, inst *CrudInstance, nsSlugHint string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if inst == nil {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("crud: no instance")))
			return goja.Undefined()
		}
		if len(call.Arguments) == 0 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("collection(schemaSlug) requires an argument")))
			return goja.Undefined()
		}
		schemaSlug := call.Arguments[0].String()
		nsSlug := nsSlugHint
		if len(call.Arguments) > 1 {
			nsSlug = call.Arguments[1].String()
		}

		q := inst.db.Where("slug = ? OR id = ?", schemaSlug, schemaSlug)
		if nsSlug != "" {
			var ns Namespace
			inst.db.Where("slug = ? OR id = ?", nsSlug, nsSlug).First(&ns)
			if ns.ID != "" {
				q = q.Where("namespace_id = ?", ns.ID)
			}
		}
		var schema CrudSchema
		if err := q.First(&schema).Error; err != nil {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("collection: schema %q not found", schemaSlug)))
			return goja.Undefined()
		}

		col, err := newCollection(inst.db, &schema, nil, inst.baseDir)
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}

		return collectionProxy(vm, col)
	}
}

// collectionProxy exposes a Collection as a JS object.
func collectionProxy(vm *goja.Runtime, col *Collection) goja.Value {
	obj := vm.NewObject()

	obj.Set("list", func(call goja.FunctionCall) goja.Value {
		opts := ListOptions{Limit: 50}
		if len(call.Arguments) > 0 {
			if m, ok := call.Arguments[0].Export().(map[string]interface{}); ok {
				if v, ok := m["filter"].(map[string]interface{}); ok {
					opts.Filter = v
				}
				if v, ok := m["limit"].(int64); ok {
					opts.Limit = int(v)
				}
				if v, ok := m["offset"].(int64); ok {
					opts.Offset = int(v)
				}
				if v, ok := m["sort"].(string); ok {
					opts.Sort = v
				}
			}
		}
		docs, err := col.List(opts)
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(docs)
	})

	obj.Set("findOne", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		id := call.Arguments[0].String()
		doc, err := col.FindOne(id)
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(doc)
	})

	obj.Set("find", func(call goja.FunctionCall) goja.Value {
		filter := map[string]any{}
		if len(call.Arguments) > 0 {
			if m, ok := call.Arguments[0].Export().(map[string]interface{}); ok {
				filter = m
			}
		}
		docs, err := col.Find(filter, ListOptions{Limit: 50})
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(docs)
	})

	obj.Set("create", func(call goja.FunctionCall) goja.Value {
		data := map[string]any{}
		meta := map[string]any{}
		if len(call.Arguments) > 0 {
			if m, ok := call.Arguments[0].Export().(map[string]interface{}); ok {
				data = m
			}
		}
		if len(call.Arguments) > 1 {
			if m, ok := call.Arguments[1].Export().(map[string]interface{}); ok {
				meta = m
			}
		}
		doc, err := col.Create(data, meta)
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(doc)
	})

	obj.Set("update", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("update(id, patch) requires 2 arguments")))
			return goja.Undefined()
		}
		id := call.Arguments[0].String()
		patch, _ := call.Arguments[1].Export().(map[string]interface{})
		doc, err := col.Update(id, patch, nil)
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(doc)
	})

	obj.Set("delete", func(id string) goja.Value {
		if err := col.Delete(id); err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(true)
	})

	obj.Set("restore", func(id string) goja.Value {
		if err := col.Restore(id); err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(true)
	})

	obj.Set("near", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue([]interface{}{})
		}
		m, _ := call.Arguments[0].Export().(map[string]interface{})
		docs, err := col.Near(NearOptions{
			Lat:         toFloat(m["lat"]),
			Lng:         toFloat(m["lng"]),
			MaxDistance: toFloat(m["maxDistance"]),
		})
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(docs)
	})

	obj.Set("within", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue([]interface{}{})
		}
		poly, _ := call.Arguments[0].Export().(map[string]interface{})
		docs, err := col.Within(WithinOptions{Polygon: poly})
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(docs)
	})

	// trash sub-object
	trashObj := vm.NewObject()
	trashObj.Set("list", func(call goja.FunctionCall) goja.Value {
		filter := map[string]any{}
		if len(call.Arguments) > 0 {
			filter, _ = call.Arguments[0].Export().(map[string]interface{})
		}
		docs, err := col.TrashList(filter)
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(docs)
	})
	trashObj.Set("findOne", func(id string) goja.Value {
		doc, err := col.TrashFindOne(id)
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(doc)
	})
	trashObj.Set("delete", func(id string) goja.Value {
		if err := col.TrashDelete(id); err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return vm.ToValue(true)
	})
	obj.Set("trash", trashObj)

	return obj
}

// ─────────────────────────────────────────────────────────────────────────────
// Tiny helpers
// ─────────────────────────────────────────────────────────────────────────────

func mapStrVal(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprint(v)
	}
	return ""
}
