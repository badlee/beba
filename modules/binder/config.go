package binder

import (
	"beba/plugins/config"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"beba/modules/auth"

	"golang.org/x/text/unicode/norm"
)

// Arguments is a flat key→value map used for parsed arguments, Env, and Configs.
type Arguments map[string]string

func (r Arguments) Get(key string, defaultValue ...string) string {
	if val, ok := r[key]; ok {
		return val
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

func (r Arguments) Set(key, value string) { r[key] = value }
func (r Arguments) Has(key string) bool   { _, ok := r[key]; return ok }
func (r Arguments) Delete(key string)     { delete(r, key) }

func (r Arguments) GetBool(key string, defaultValue ...bool) bool {
	if !r.Has(key) {
		if len(defaultValue) > 0 {
			return defaultValue[0]
		}
		return false
	}
	return isTrue(r[key])
}
func (r Arguments) SetBool(key string, v bool) { r[key] = strconv.FormatBool(v) }
func (r Arguments) HasBool(key string) bool    { return r.Has(key) && isBool(r[key]) }

func (r Arguments) GetInt(key string, defaultValue ...int) int {
	def := 0
	if len(defaultValue) > 0 {
		def = defaultValue[0]
	}
	if v, err := strconv.Atoi(r.Get(key, strconv.Itoa(def))); err == nil {
		return v
	}
	return def
}
func (r Arguments) SetInt(key string, v int) { r[key] = strconv.Itoa(v) }
func (r Arguments) HasInt(key string) bool {
	_, err := strconv.Atoi(r.Get(key))
	return r.Has(key) && err == nil
}

func (r Arguments) GetFloat(key string, defaultValue ...float64) float64 {
	def := 0.0
	if len(defaultValue) > 0 {
		def = defaultValue[0]
	}
	if v, err := strconv.ParseFloat(r.Get(key, strconv.FormatFloat(def, 'f', -1, 64)), 64); err == nil {
		return v
	}
	return def
}
func (r Arguments) SetFloat(key string, v float64) {
	r[key] = strconv.FormatFloat(v, 'f', -1, 64)
}

func (r Arguments) GetStringSlice(key string, defaultValue ...string) []string {
	v := r.Get(key, strings.Join(defaultValue, ","))
	var result []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			result = append(result, s)
		}
	}
	return result
}
func (r Arguments) SetStringSlice(key string, value []string) { r[key] = strings.Join(value, ",") }

func (r Arguments) GetDuration(key string, defaultValue ...time.Duration) time.Duration {
	def := time.Duration(0)
	if len(defaultValue) > 0 {
		def = defaultValue[0]
	}
	if v, err := time.ParseDuration(r.Get(key, def.String())); err == nil {
		return v
	}
	return def
}
func (r Arguments) SetDuration(key string, v time.Duration) { r[key] = v.String() }
func (r Arguments) HasDuration(key string) bool {
	_, err := time.ParseDuration(r.Get(key))
	return r.Has(key) && err == nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Config
// ─────────────────────────────────────────────────────────────────────────────

type Config struct {
	Registrations []ProtocolRegistration
	Groups        []GroupConfig
	AuthManagers  map[string]*AuthManagerConfig
}

// ProtocolRegistration stores a REGISTER PROTOCOL|MIDDLEWARE|MODULE|PRELOAD declaration.
type ProtocolRegistration struct {
	Kind         string    // "PROTOCOL", "MIDDLEWARE", "MODULE", "PRELOAD"
	Name         string    // identifier
	Inline       bool      // code is inline
	Code         string    // filepath (non-inline)
	Args         Arguments // optional arguments
	pendingRoute *RouteConfig
}

// GroupConfig is one [PROTOCOL ADDRESS … END PROTOCOL] block.
type GroupConfig struct {
	Directive string             // "HTTP", "TCP", "DATABASE", …
	Address   string             // IP:port, socket, URL, …
	Items     []*DirectiveConfig // one per nested protocol handler
}

// ─────────────────────────────────────────────────────────────────────────────
// DirectiveConfig — protocol-agnostic, simplified
// ─────────────────────────────────────────────────────────────────────────────

// DirectiveConfig holds only what the parser knows universally.
// Protocol-specific directives (SSL, proxy, rewrite, errors, workers, events,
// middleware chains …) appear as RouteConfig entries in Routes and are
// interpreted by each protocol implementation.
type DirectiveConfig struct {
	Name          string            // Directive identifier ("HTTP", "DTP", …)
	Address       string            // Inherited from the parent GroupConfig
	Args          Arguments         // Arguments on the opening line
	Env           Arguments         // ENV values (keys __file_N hold file paths; __prefix holds prefix)
	Configs       Arguments         // CONF + SET/DEF/DEL values
	Routes        Routes            // All custom directives declared inside this block
	AppConfig     *config.AppConfig // Injected at runtime by the caller
	BaseDir       string            // Absolute dir of the source .bind file
	Auth          Managers          // AUTH managers
	disabledCache map[string]bool
	mu            sync.RWMutex
}

func (r *DirectiveConfig) GetRoutes(route string) Routes {
	var out Routes
	for _, rc := range r.Routes {
		if rc.Method == route {
			out = append(out, rc)
		}
	}
	return out
}

func (r *DirectiveConfig) Disabled(typ, feature string, extra ...string) bool {
	r.initDisabledCache()
	r.mu.RLock()
	defer r.mu.RUnlock()

	typ = strings.ToUpper(typ)
	if !r.disabledCache[typ+":"+strings.ToUpper(feature)] {
		return false
	}
	for _, ef := range extra {
		if !r.disabledCache[typ+":"+strings.ToUpper(ef)] {
			return false
		}
	}
	return true
}

func (r *DirectiveConfig) AnyDisabled(typ, feature string, extra ...string) bool {
	r.initDisabledCache()
	r.mu.RLock()
	defer r.mu.RUnlock()

	typ = strings.ToUpper(typ)
	if r.disabledCache[typ+":"+strings.ToUpper(feature)] {
		return true
	}
	for _, ef := range extra {
		if r.disabledCache[typ+":"+strings.ToUpper(ef)] {
			return true
		}
	}
	return false
}

func (r *DirectiveConfig) Enabled(typ, feature string, extra ...string) bool {
	return !r.AnyDisabled(typ, feature, extra...)
}

func (r *DirectiveConfig) AnyEnabled(typ, feature string, extra ...string) bool {
	return !r.Disabled(typ, feature, extra...)
}

func (r *DirectiveConfig) initDisabledCache() {
	r.mu.RLock()
	if r.disabledCache != nil {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.disabledCache == nil {
		r.disabledCache = make(map[string]bool)
		routes := r.GetRoutes("DISABLE")
		for _, rc := range routes {
			t := strings.ToUpper(strings.TrimSpace(rc.Path))
			f := strings.ToUpper(strings.TrimSpace(rc.Handler))
			r.disabledCache[t+":"+f] = true
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RouteConfig
// ─────────────────────────────────────────────────────────────────────────────

type HandlerType string

const (
	HandlerFile     HandlerType = "HANDLER"
	HandlerText     HandlerType = "TEXT"
	HandlerJSON     HandlerType = "JSON"
	HandlerYAML     HandlerType = "YAML"
	HandlerTOML     HandlerType = "TOML"
	HandlerENV      HandlerType = "ENV"
	HandlerTemplate HandlerType = "TEMPLATE"
	HandlerFS       HandlerType = "FILE"
	HandlerBinary   HandlerType = "BINARY"
	HandlerHex      HandlerType = "HEX"
	HandlerBase32   HandlerType = "BASE32"
	HandlerBase64   HandlerType = "BASE64"
	HandlerJS       HandlerType = "JS"
	HandlerBytes    HandlerType = "BYTES"
)

// MiddlewareUse is one @Name[args] token on a route line.
type MiddlewareUse struct {
	Name string
	Args Arguments
}

// RouteConfig represents one custom directive — inline, non-inline, or group.
//
//	Inline:     Inline==true,  IsGroup==false  — Handler holds body code (collected from BEGIN…END)
//	Non-inline: Inline==false, IsGroup==false  — Handler holds filepath (may be "")
//	Group:      IsGroup==true                  — Routes holds child directives (collected from DEFINE…END)
type RouteConfig struct {
	Method      string          // e.g. "GET", "SCHEMA", "FIELD" — always uppercase
	Path        string          // path, name, or identifier (optional)
	Handler     string          // inline code OR filepath; empty when IsGroup
	Type        HandlerType     // encoding / interpretation hint
	ContentType string          // MIME type hint
	Inline      bool            // true for inline (BEGIN) and group (DEFINE)
	IsGroup     bool            // true for DEFINE groups
	Middlewares []MiddlewareUse // @MW tokens on this line
	Args        Arguments       // trailing [key=val …] arguments
	Routes      Routes          // child routes (IsGroup only)
}

// ParseHandlerAsRoutes re-parses Handler as a route list.
func (r *RouteConfig) ParseHandlerAsRoutes(cwd ...string) (*RouteConfig, []string, error) {
	return ParseRouteFromString(r.Handler, cwd...)
}

// ParseHandlerAsConfig re-parses Handler as a full Config.
func (r *RouteConfig) ParseHandlerAsConfig(cwd ...string) (*Config, []string, error) {
	return ParseConfig(r.Handler, cwd...)
}

// Middleware returns the first MiddlewareUse matching key (case-insensitive).
func (r *RouteConfig) Middleware(key string) *MiddlewareUse {
	for i := range r.Middlewares {
		if strings.EqualFold(r.Middlewares[i].Name, key) {
			return &r.Middlewares[i]
		}
	}
	return nil
}

// Content returns the handler body as bytes, applying HandlerType decoding.
func (r *RouteConfig) Content(cfg *DirectiveConfig) ([]byte, error) {
	h := r.Handler
	if r.Inline {
		h = strings.TrimSpace(h)
		switch r.Type {
		case HandlerHex:
			return hex.DecodeString(h)
		case HandlerBase64:
			return base64.StdEncoding.DecodeString(h)
		case HandlerBase32:
			return base32.StdEncoding.DecodeString(h)
		case HandlerBinary, HandlerBytes:
			if b, err := hex.DecodeString(h); err == nil {
				return b, nil
			}
			if b, err := base64.StdEncoding.DecodeString(h); err == nil {
				return b, nil
			}
			return []byte(h), nil
		default:
			return []byte(h), nil
		}
	}
	switch r.Type {
	case HandlerFS, HandlerFile:
		full := h
		if !filepath.IsAbs(full) {
			full = filepath.Join(cfg.BaseDir, full)
		}
		return os.ReadFile(full)
	default:
		return []byte(h), nil
	}
}

type Routes []*RouteConfig

func (r Routes) Get(key string) *RouteConfig {
	for _, rc := range r {
		if rc.Path == key {
			return rc
		}
	}
	return nil
}

func (r Routes) GetGroups() Routes {
	var groups Routes
	for _, rc := range r {
		if rc.IsGroup {
			groups = append(groups, rc)
		}
	}
	return groups
}

// ─────────────────────────────────────────────────────────────────────────────
// Auth
// ─────────────────────────────────────────────────────────────────────────────

type AuthResult = auth.AuthResult
type AuthConfig = auth.AuthConfig
type AuthConfigs = auth.AuthConfigs
type Managers = auth.Managers
type AuthManagerConfig = auth.AuthManagerConfig
type OAuth2ClientConfig = auth.OAuth2ClientConfig
type OAuth2ServerConfig = auth.OAuth2ServerConfig

type AuthType = auth.AuthType

const (
	AuthFile   = auth.AuthFile
	AuthCSV    = auth.AuthCSV
	AuthUser   = auth.AuthUser
	AuthScript = auth.AuthScript
)

// ─────────────────────────────────────────────────────────────────────────────
// Utilities

func isBool(s string) bool  { return isTrue(s) || isFalse(s) }
func isTrue(s string) bool  { s = strings.ToLower(s); return s == "true" || s == "yes" || s == "on" }
func isFalse(s string) bool { s = strings.ToLower(s); return s == "false" || s == "no" || s == "off" }

func IsMimeType(s string) bool {
	if !strings.Contains(s, "/") {
		return false
	}
	parts := strings.SplitN(s, "/", 2)
	switch strings.ToLower(parts[0]) {
	case "application", "audio", "font", "example", "image", "message", "model", "multipart", "text", "video":
		_, _, err := mime.ParseMediaType(s)
		return err == nil
	default:
		return strings.HasPrefix(parts[0], "x-") // Allow vendor types
	}
}

func formatToJSVariableName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	t := norm.NFD.String(s)
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, t)
	s = regexp.MustCompile(`[^a-zA-Z0-9_$]`).ReplaceAllString(s, "_")
	s = regexp.MustCompile(`_+`).ReplaceAllString(s, "_")
	if first := s[0]; !(first == '_' || first == '$' || unicode.IsLetter(rune(first))) {
		s = "_" + s
	}
	return s
}

// IsFileLike returns true if s looks like a file path (has / or extension).
func IsFileLike(s string) bool {
	if s == "" {
		return false
	}
	// A file usually contains a slash or a dot (extension) but NOT an @ (email)
	return strings.ContainsAny(s, "/\\") ||
		(strings.Contains(s, ".") && !strings.Contains(s, "@"))
}

type AtomicString struct{ v atomic.Value }

func (a *AtomicString) Store(s string) { a.v.Store(s) }
func (a *AtomicString) Load() string {
	if x := a.v.Load(); x != nil {
		return x.(string)
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// Ensure fmt is used (for ENV countPrefixed helper in parser)
// ─────────────────────────────────────────────────────────────────────────────
var _ = fmt.Sprintf
