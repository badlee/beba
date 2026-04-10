package binder

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"http-server/plugins/config"
	"http-server/processor"
	"http-server/types"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/dop251/goja"
	"github.com/joho/godotenv"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
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
	Name      string            // Directive identifier ("HTTP", "DTP", …)
	Address   string            // Inherited from the parent GroupConfig
	Args      Arguments         // Arguments on the opening line
	Env       Arguments         // ENV values (keys __file_N hold file paths; __prefix holds prefix)
	Configs   Arguments         // CONF + SET/DEF/DEL values
	Routes    []*RouteConfig    // All custom directives declared inside this block
	AppConfig *config.AppConfig // Injected at runtime by the caller
	BaseDir   string            // Absolute dir of the source .bind file
	Auth      AuthConfigs       // AUTH directives
}

func (r DirectiveConfig) GetRoutes(route string) []*RouteConfig {
	var out []*RouteConfig
	for _, rc := range r.Routes {
		if rc.Method == route {
			out = append(out, rc)
		}
	}
	return out
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
	Routes      []*RouteConfig  // child routes (IsGroup only)
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

// ─────────────────────────────────────────────────────────────────────────────
// Auth
// ─────────────────────────────────────────────────────────────────────────────

type AuthType string

const (
	AuthFile   AuthType = "FILE"
	AuthCSV    AuthType = "CSV"
	AuthUser   AuthType = "USER"
	AuthScript AuthType = "SCRIPT"
)

type AuthResult struct {
	Username string
	Secret   string
	UseProto bool
}

func (a *AuthResult) User() string {
	return a.Username
}

func (a *AuthResult) Pwd() string {
	return a.Secret
}
func (a *AuthResult) Proto() bool {
	return a.UseProto
}

type AuthConfig struct {
	Type     AuthType
	Format   string    // JSON, YAML, TOML, ENV (when Type==AuthFile)
	Filepath string    // for FILE and CSV
	User     string    // for AuthUser
	Password string    // for AuthUser
	Handler  string    // JS code or filepath (for AuthScript)
	Inline   bool      // true when handler is inline JS
	Configs  Arguments // passed to script as `config`
	config   *DirectiveConfig
}

type AuthConfigs []*AuthConfig

func (a *AuthConfigs) Append(c *AuthConfig) { *a = append(*a, c) }

func (a AuthConfigs) Auth(username, password string, token ...string) error {
	for _, ac := range a {
		if err := ac.Auth(username, password, token...); err == nil {
			return nil
		}
	}
	return errors.New("unauthorized")
}

func (a AuthConfigs) UserInfo(username string) (types.UserInfo, error) {
	for _, ac := range a {
		if r, err := ac.UserInfo(username); err == nil {
			return r, nil
		}
	}
	return nil, errors.New("user not found")
}

func (auth *AuthConfig) CheckPwd(hashed, plain string) bool {
	if hashed == "" {
		return plain == ""
	}
	if strings.HasPrefix(hashed, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)) == nil
	}
	if strings.HasPrefix(hashed, "{") {
		end := strings.IndexByte(hashed, '}')
		if end > 1 {
			alg, val := strings.ToUpper(hashed[1:end]), hashed[end+1:]
			var candidates [][]byte
			if b, err := hex.DecodeString(val); err == nil {
				candidates = append(candidates, b)
			}
			if b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(val)); err == nil {
				candidates = append(candidates, b)
			}
			if b, err := base64.StdEncoding.DecodeString(val); err == nil {
				candidates = append(candidates, b)
			}
			if len(candidates) == 0 {
				candidates = append(candidates, []byte(val))
			}
			var dig []byte
			switch alg {
			case "SHA512":
				h := sha512.Sum512([]byte(plain))
				dig = h[:]
			case "SHA256":
				h := sha256.Sum256([]byte(plain))
				dig = h[:]
			case "SHA1":
				h := sha1.Sum([]byte(plain))
				dig = h[:]
			case "MD5":
				h := md5.Sum([]byte(plain))
				dig = h[:]
			default:
				return false
			}
			for _, c := range candidates {
				if bytes.Equal(c, dig) {
					return true
				}
			}
			return false
		}
	}
	return hashed == plain
}

func (auth *AuthConfig) Auth(username, password string, token ...string) error {
	switch auth.Type {
	case AuthUser:
		if auth.User == username && auth.CheckPwd(auth.Password, password) {
			return nil
		}
	case AuthCSV:
		if f, err := auth.openFile(); err == nil {
			defer f.Close()
			r := csv.NewReader(f)
			r.Comma = ';'
			for {
				rec, err := r.Read()
				if err == io.EOF {
					break
				}
				if err == nil && len(rec) >= 2 &&
					strings.TrimSpace(rec[0]) == username &&
					auth.CheckPwd(strings.TrimSpace(rec[1]), password) {
					return nil
				}
			}
		}
	case AuthFile:
		if users := auth.loadUserFile(); users != nil {
			if pwd, ok := users[username]; ok && auth.CheckPwd(pwd, password) {
				return nil
			}
		}
	case AuthScript:
		return auth.runScript(username, password, nil)
	}
	return errors.New("unauthorized")
}

func (auth *AuthConfig) UserInfo(username string) (*AuthResult, error) {
	switch auth.Type {
	case AuthUser:
		if auth.User == username {
			return &AuthResult{Username: username, Secret: auth.Password, UseProto: useProto(auth)}, nil
		}
	case AuthCSV:
		if f, err := auth.openFile(); err == nil {
			defer f.Close()
			r := csv.NewReader(f)
			r.Comma = ';'
			for {
				rec, err := r.Read()
				if err == io.EOF {
					break
				}
				if err == nil && len(rec) >= 2 && strings.TrimSpace(rec[0]) == username {
					return &AuthResult{Username: username, Secret: strings.TrimSpace(rec[1]), UseProto: useProto(auth)}, nil
				}
			}
		}
	case AuthFile:
		if users := auth.loadUserFile(); users != nil {
			if secret, ok := users[username]; ok {
				return &AuthResult{Username: username, Secret: secret, UseProto: useProto(auth)}, nil
			}
		}
	case AuthScript:
		var result *AuthResult
		err := auth.runScript(username, "", &result)
		return result, err
	}
	return nil, errors.New("user not found")
}

func (auth *AuthConfig) openFile() (*os.File, error) {
	p := auth.Filepath
	if auth.config != nil && !filepath.IsAbs(p) {
		p = filepath.Join(auth.config.BaseDir, p)
	}
	return os.Open(p)
}

func (auth *AuthConfig) loadUserFile() map[string]string {
	f, err := auth.openFile()
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	users := make(map[string]string)
	switch auth.Format {
	case "JSON":
		json.Unmarshal(data, &users)
	case "YAML":
		yaml.Unmarshal(data, &users)
	case "TOML":
		toml.Unmarshal(data, &users)
	case "ENV":
		if e, err := godotenv.Unmarshal(string(data)); err == nil {
			users = e
		}
	}
	return users
}

func (auth *AuthConfig) runScript(username, password string, resultOut **AuthResult) error {
	vm := processor.New(auth.config.BaseDir, nil, auth.config.AppConfig)
	vm.Set("username", username)
	vm.Set("user", username)
	vm.Set("password", password)

	var storedPwd AtomicString
	vm.Set("checkPwd", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(storedPwd.Load() != "")
		}
		p := call.Arguments[0].ToString().String()
		storedPwd.Store(p)
		return vm.ToValue(auth.CheckPwd(p, password))
	})
	vm.Set("setPwd", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			storedPwd.Store(call.Arguments[0].ToString().String())
		}
		return goja.Undefined()
	})
	cfgObj := vm.NewObject()
	for k, v := range auth.Configs {
		cfgObj.Set(k, v)
	}
	vm.Set("config", cfgObj)

	passed := make(chan bool, 1)
	var closed atomic.Bool
	var rejectMsg string
	vm.Set("allow", func() {
		if closed.CompareAndSwap(false, true) {
			passed <- true
		}
	})
	vm.Set("reject", func(msg ...string) {
		if closed.CompareAndSwap(false, true) {
			if len(msg) > 0 {
				rejectMsg = msg[0]
			}
			passed <- false
		}
	})

	code := auth.Handler
	if !auth.Inline {
		b, err := os.ReadFile(auth.Handler)
		if err != nil {
			return err
		}
		code = string(b)
	}
	vm.RunString(code)

	if <-passed {
		if resultOut != nil {
			*resultOut = &AuthResult{Username: username, Secret: storedPwd.Load(), UseProto: useProto(auth)}
		}
		return nil
	}
	if rejectMsg != "" {
		return errors.New(rejectMsg)
	}
	return errors.New("unauthorized")
}

// ─────────────────────────────────────────────────────────────────────────────
// Utilities
// ─────────────────────────────────────────────────────────────────────────────

func useProto(auth *AuthConfig) bool {
	return auth.Configs != nil && isTrue(auth.Configs["PROTO"])
}

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
