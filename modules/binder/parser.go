package binder

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
	"sync"
)

// ─────────────────────────────────────────────────────────────────────────────
// Public API
// ─────────────────────────────────────────────────────────────────────────────

func ParseFile(path string) (*Config, []string, error) {
	allFiles := make(map[string]bool)
	lines, err := preprocessInclude(path, make(map[string]bool), allFiles)
	if err != nil {
		return nil, nil, err
	}
	return parseLines(lines, allFiles)
}

func ParseConfig(content string, cwd ...string) (*Config, []string, error) {
	allFiles := make(map[string]bool)
	lines, err := preprocessString(content, cwd, make(map[string]bool), allFiles)
	if err != nil {
		return nil, nil, err
	}
	return parseLines(lines, allFiles)
}

func ParseRouteFromString(content string, cwd ...string) (*RouteConfig, []string, error) {
	cfg, files, err := ParseConfig(content, cwd...)
	if err != nil {
		return nil, files, err
	}
	for _, g := range cfg.Groups {
		for _, item := range g.Items {
			if len(item.Routes) > 0 {
				return item.Routes[0], files, nil
			}
		}
	}
	return nil, files, fmt.Errorf("no route found")
}

var (
	knownProtocols   = make(map[string]bool)
	knownProtocolsMu sync.RWMutex
)

func init() {
	RegisterProtocolKeyword("HTTP")
	RegisterProtocolKeyword("HTTPS")
	RegisterProtocolKeyword("TCP")
	RegisterProtocolKeyword("UDP")
	RegisterProtocolKeyword("DTP")
	RegisterProtocolKeyword("SECURITY")
}

// RegisterProtocolKeyword adds a new top-level directive name that can be used
// for protocol-switching blocks (e.g. MQTT, DATABASE).
func RegisterProtocolKeyword(name string) {
	knownProtocolsMu.Lock()
	defer knownProtocolsMu.Unlock()
	knownProtocols[strings.ToUpper(name)] = true
}

func IsKnownProtocol(name string) bool {
	knownProtocolsMu.RLock()
	defer knownProtocolsMu.RUnlock()
	return knownProtocols[strings.ToUpper(name)]
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal types
// ─────────────────────────────────────────────────────────────────────────────

type parsedLine struct {
	content string
	file    string
	lineNum int
}

// parseContext is the mutable state threaded through the main loop.
type parseContext struct {
	config       *Config
	currentGroup *GroupConfig
	currentProto *DirectiveConfig
	parentProto  *DirectiveConfig

	// Pending ENV state — resolved lazily when the proto is finalised or
	// when an explicit SET/REMOVE/DEFAULT is applied.
	envPrefix string // current ENV PREFIX value (default: "")

	// Inline code collection
	inlineCode   strings.Builder
	inlineTarget inlineTarget

	// Stack for DEFINE groups
	groupStack []*RouteConfig

	currentAuth   *AuthManagerConfig
	currentOAuth2 *OAuth2ClientConfig
}

type inlineTarget struct {
	route      *RouteConfig
	authConfig *AuthConfig
	reg        *ProtocolRegistration
}

func (t *inlineTarget) active() bool {
	return t.route != nil || t.authConfig != nil || t.reg != nil
}
func (t *inlineTarget) clear() { *t = inlineTarget{} }

// ─────────────────────────────────────────────────────────────────────────────
// Main loop
// ─────────────────────────────────────────────────────────────────────────────

func parseLines(lines []parsedLine, allFiles map[string]bool) (*Config, []string, error) {
	ctx := &parseContext{config: &Config{
		AuthManagers: make(map[string]*AuthManagerConfig),
	}}

	for _, pl := range lines {
		line := strings.TrimSpace(pl.content)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		toks := tokenizeLine(line)
		if len(toks) == 0 {
			continue
		}
		cmd := strings.ToUpper(toks[0])

		if cmd == "END" {
			if err := ctx.handleEnd(toks, pl); err != nil {
				return nil, nil, err
			}
			continue
		}

		// Accumulate inline code while inside a BEGIN…END block.
		// Applies even inside a DEFINE group stack: e.g. VIRTUAL BEGIN inside SCHEMA DEFINE.
		if ctx.inlineTarget.active() {
			ctx.inlineCode.WriteString(pl.content + "\n")
			continue
		}

		if ctx.currentGroup == nil {
			if err := ctx.handleTopLevel(toks, pl); err != nil {
				return nil, nil, err
			}
		} else {
			if err := ctx.handleInsideGroup(toks, pl); err != nil {
				return nil, nil, err
			}
		}
	}

	files := make([]string, 0, len(allFiles))
	for f := range allFiles {
		files = append(files, f)
	}
	return ctx.config, files, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// END
// ─────────────────────────────────────────────────────────────────────────────

func (ctx *parseContext) handleEnd(toks []string, pl parsedLine) error {
	if len(toks) < 2 {
		return fmt.Errorf("%s:%d: END requires a keyword", pl.file, pl.lineNum)
	}
	endKw := strings.ToUpper(toks[1])
	code := strings.TrimSpace(ctx.inlineCode.String())

	// 1. Close inline block
	if ctx.inlineTarget.active() {
		switch {
		case ctx.inlineTarget.route != nil:
			ctx.inlineTarget.route.Handler = code
		case ctx.inlineTarget.authConfig != nil:
			ctx.inlineTarget.authConfig.Handler = code
		case ctx.inlineTarget.reg != nil:
			ctx.inlineTarget.reg.Code = code
		}
		ctx.inlineCode.Reset()
		ctx.inlineTarget.clear()
		return nil
	}

	// 2. Close DEFINE group
	if len(ctx.groupStack) > 0 {
		top := ctx.groupStack[len(ctx.groupStack)-1]
		if strings.ToUpper(top.Method) == endKw {
			ctx.groupStack = ctx.groupStack[:len(ctx.groupStack)-1]
			if len(ctx.groupStack) > 0 {
				parent := ctx.groupStack[len(ctx.groupStack)-1]
				parent.Routes = append(parent.Routes, top)
			} else {
				ctx.currentProto.Routes = append(ctx.currentProto.Routes, top)
			}
			ctx.inlineCode.Reset()
			return nil
		}
	}

	// 3. Close current proto — only when it has a DIFFERENT name from the group.
	// When proto.Name == group.Directive, END closes the whole group (case 4), not
	// just an inner proto. Without this guard, e.g. "END DATABASE" for a DATABASE
	// group would hit case 3 and silently discard the group.
	if ctx.currentProto != nil && strings.EqualFold(endKw, ctx.currentProto.Name) &&
		(ctx.currentGroup == nil || !strings.EqualFold(ctx.currentProto.Name, ctx.currentGroup.Directive)) {
		if ctx.currentGroup != nil {
			ctx.currentGroup.Items = append(ctx.currentGroup.Items, ctx.currentProto)
		}
		ctx.currentProto = nil
		ctx.envPrefix = ""
		return nil
	}

	// 4. Close current group
	if ctx.currentGroup != nil && endKw == strings.ToUpper(ctx.currentGroup.Directive) {
		if ctx.currentProto != nil {
			isContainer := ctx.currentProto.Name == ctx.currentGroup.Directive
			if !isContainer || len(ctx.currentProto.Routes) > 0 || len(ctx.currentGroup.Items) == 0 {
				ctx.currentGroup.Items = append(ctx.currentGroup.Items, ctx.currentProto)
			}
			ctx.currentProto = nil
		}
		ctx.config.Groups = append(ctx.config.Groups, *ctx.currentGroup)
		ctx.currentGroup = nil
		ctx.parentProto = nil
		ctx.envPrefix = ""
	}

	// 5. Close AUTH manager
	if ctx.currentAuth != nil && endKw == "AUTH" {
		ctx.config.AuthManagers[ctx.currentAuth.Name] = ctx.currentAuth
		ctx.currentAuth = nil
		return nil
	}

	// 6. Close OAUTH2 client
	if ctx.currentOAuth2 != nil && (endKw == "OAUTH2" || endKw == "STRATEGY") {
		if ctx.currentAuth != nil {
			ctx.currentAuth.Clients = append(ctx.currentAuth.Clients, ctx.currentOAuth2)
		}
		ctx.currentOAuth2 = nil
		return nil
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Top-level
// ─────────────────────────────────────────────────────────────────────────────

func (ctx *parseContext) handleTopLevel(toks []string, pl parsedLine) error {
	cmd := strings.ToUpper(toks[0])
	if cmd == "REGISTER" {
		ctx.handleRegister(toks, pl)
		return nil
	}
	if cmd == "AUTH" && len(toks) >= 3 && strings.ToUpper(toks[2]) == "DEFINE" {
		ctx.currentAuth = &AuthManagerConfig{
			Name:    toks[1],
			BaseDir: filepath.Dir(pl.file),
		}
		return nil
	}
	if len(toks) < 2 {
		return fmt.Errorf("%s:%d: invalid group (expected: PROTO ADDRESS)", pl.file, pl.lineNum)
	}

	ctx.currentGroup = &GroupConfig{Directive: cmd, Address: toks[1]}

	protoName := cmd
	protoArgs := Arguments{}
	if len(toks) > 2 {
		next := toks[2]
		if strings.HasPrefix(next, "[") || strings.Contains(next, "=") {
			protoArgs = parseArgs(strings.Join(toks[2:], " "))
		} else {
			protoName = strings.ToUpper(next)
			if len(toks) > 3 {
				protoArgs = parseArgs(strings.Join(toks[3:], " "))
			}
		}
	}
	ctx.currentProto = newProto(protoName, toks[1], protoArgs, pl.file)
	// Seed Env with os.Environ() at proto creation — ENV directives will refine it.
	loadOSEnv(ctx.currentProto.Env, "")
	return nil
}

func (ctx *parseContext) handleRegister(toks []string, pl parsedLine) {
	if len(toks) < 3 {
		return
	}
	kind := strings.ToUpper(toks[1])

	name := ""
	rest := toks[2:]
	if kind != "PRELOAD" && len(toks) > 2 {
		name = toks[2]
		rest = toks[3:]
	}

	isInline, code := false, []byte{}
	var args Arguments
	for _, tok := range rest {
		switch strings.ToUpper(tok) {
		case "BEGIN":
			isInline = true
		default:
			if strings.HasPrefix(tok, "[") && strings.HasSuffix(tok, "]") {
				args = parseArgs(tok)
			} else if len(code) == 0 {
				code = []byte(tok)
				b, err := os.ReadFile(tok)
				if err != nil {
					log.Printf("failed to read JS protocol file %s: %v", tok, err)
					os.Exit(1)
				}
				code = b
			}
		}
	}

	reg := ProtocolRegistration{Kind: kind, Name: name, Code: string(code), Args: args, Inline: isInline}
	ctx.config.Registrations = append(ctx.config.Registrations, reg)
	if isInline {
		ctx.inlineTarget = inlineTarget{reg: &ctx.config.Registrations[len(ctx.config.Registrations)-1]}
		ctx.inlineCode.Reset()
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Inside a group
// ─────────────────────────────────────────────────────────────────────────────

func (ctx *parseContext) handleInsideGroup(toks []string, pl parsedLine) error {
	cmd := strings.ToUpper(toks[0])

	if ctx.isKnownProtocol(cmd) {
		ctx.switchProtocol(cmd, toks, pl)
		return nil
	}

	switch cmd {
	case "ENV":
		return ctx.parseENV(toks, pl.file)
	case "CONF":
		return ctx.parseCONF(toks, pl.file)
	case "SET":
		return ctx.parseSET(toks)
	case "DEF":
		return ctx.parseDEF(toks)
	case "DEL":
		return ctx.parseDEL(toks)
	case "AUTH":
		if ctx.currentAuth != nil {
			return ctx.handleInsideAuth(toks, pl)
		}
		return ctx.parseAUTH(toks)
	}

	if ctx.currentAuth != nil {
		return ctx.handleInsideAuth(toks, pl)
	}

	return ctx.parseRoute(toks, pl)
}

func (ctx *parseContext) handleInsideAuth(toks []string, pl parsedLine) error {
	cmd := strings.ToUpper(toks[0])

	if ctx.currentOAuth2 != nil {
		return ctx.handleInsideOAuth2(toks, pl)
	}

	switch cmd {
	case "DATABASE":
		if len(toks) > 1 {
			ctx.currentAuth.Database = strings.Trim(toks[1], "\"'`")
		}
	case "SECRET":
		if len(toks) > 1 {
			ctx.currentAuth.Secret = strings.Trim(toks[1], "\"'`")
		}
	case "USERS":
		if len(toks) >= 3 {
			ac := &AuthConfig{
				Type:     AuthFile,
				Format:   strings.ToUpper(toks[1]),
				Filepath: strings.Trim(toks[2], "\"'`"),
			}
			ctx.currentAuth.Strategies.Append(ac)
		}
	case "USER":
		if len(toks) >= 3 {
			ac := &AuthConfig{
				Type:     AuthUser,
				User:     toks[1],
				Password: strings.Trim(toks[2], "\"'`"),
			}
			ctx.currentAuth.Strategies.Append(ac)
		}
	case "AUTH":
		// This can be AUTH BEGIN or AUTH filepath or AUTH CSV
		if len(toks) < 2 {
			return nil
		}
		sub := strings.ToUpper(toks[1])
		if sub == "CSV" {
			if len(toks) >= 3 {
				ctx.currentAuth.Strategies.Append(&AuthConfig{
					Type:     AuthCSV,
					Filepath: strings.Trim(toks[2], "\"'`"),
				})
			}
		} else if sub == "BEGIN" {
			ac := &AuthConfig{Type: AuthScript, Inline: true}
			if len(toks) > 2 {
				ac.Configs = parseArgs(strings.Join(toks[2:], " "))
			}
			ctx.currentAuth.Strategies.Append(ac)
			ctx.inlineTarget = inlineTarget{authConfig: ac}
			ctx.inlineCode.Reset()
		} else {
			// AUTH filepath [args]
			ac := &AuthConfig{Type: AuthScript, Inline: false, Handler: strings.Trim(toks[1], "\"'`")}
			if len(toks) > 2 {
				ac.Configs = parseArgs(strings.Join(toks[2:], " "))
			}
			ctx.currentAuth.Strategies.Append(ac)
		}
	case "STRATEGY", "OAUTH2":
		if len(toks) >= 3 && strings.ToUpper(toks[2]) == "DEFINE" {
			ctx.currentOAuth2 = &OAuth2ClientConfig{
				ID: toks[1],
			}
		}
	case "SERVER":
		if len(toks) >= 2 && strings.ToUpper(toks[1]) == "DEFINE" {
			// Handle SERVER DEFINE
		}
	}
	return nil
}

func (ctx *parseContext) handleInsideOAuth2(toks []string, pl parsedLine) error {
	cmd := strings.ToUpper(toks[0])
	switch cmd {
	case "CLIENTID":
		if len(toks) > 1 {
			ctx.currentOAuth2.ID = strings.Trim(toks[1], "\"'`")
		}
	case "CLIENTSECRET":
		if len(toks) > 1 {
			ctx.currentOAuth2.Secret = strings.Trim(toks[1], "\"'`")
		}
	case "REDIRECT", "REDIRECTURL":
		if len(toks) > 1 {
			ctx.currentOAuth2.RedirectURIs = append(ctx.currentOAuth2.RedirectURIs, strings.Trim(toks[1], "\"'`"))
		}
	case "SCOPE", "SCOPES":
		if len(toks) > 1 {
			ctx.currentOAuth2.Scopes = append(ctx.currentOAuth2.Scopes, strings.Trim(toks[1], "\"'`"))
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Protocol switching
// ─────────────────────────────────────────────────────────────────────────────

func (ctx *parseContext) switchProtocol(name string, toks []string, pl parsedLine) {
	if ctx.currentProto != nil {
		if ctx.currentProto.Name == ctx.currentGroup.Directive {
			ctx.parentProto = ctx.currentProto
		} else {
			ctx.currentGroup.Items = append(ctx.currentGroup.Items, ctx.currentProto)
		}
	}
	protoArgs := Arguments{}
	if len(toks) > 1 {
		protoArgs = parseArgs(strings.Join(toks[1:], " "))
	}
	p := newProto(name, ctx.currentGroup.Address, protoArgs, pl.file)
	// Inherit Env + Configs from parent container if present.
	if ctx.parentProto != nil {
		for k, v := range ctx.parentProto.Env {
			p.Env[k] = v
		}
		for k, v := range ctx.parentProto.Configs {
			p.Configs[k] = v
		}
		p.Auth = ctx.parentProto.Auth
	} else {
		// Fresh proto: seed with os.Environ
		loadOSEnv(p.Env, ctx.envPrefix)
	}
	ctx.currentProto = p
	ctx.envPrefix = ""
}

func (ctx *parseContext) isKnownProtocol(name string) bool {
	if IsKnownProtocol(name) {
		return true
	}
	// Fallback to locally registered JS protocols
	for _, reg := range ctx.config.Registrations {
		if reg.Kind == "PROTOCOL" && strings.ToUpper(reg.Name) == name {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// ENV — load and merge into proto.Env immediately
//
//   ENV PREFIX prefix          set prefix filter for os.Environ re-load
//   ENV "filepath"             load .env file, merge into Env
//   ENV SET key value          set a key
//   ENV REMOVE key             delete a key
//   ENV DEFAULT key value      set only if absent
// ─────────────────────────────────────────────────────────────────────────────

func (ctx *parseContext) parseENV(toks []string, sourceFile string) error {
	if ctx.currentProto == nil || len(toks) < 2 {
		return nil
	}
	sub := strings.ToUpper(toks[1])
	switch sub {
	case "PREFIX":
		// Re-seed Env from os.Environ with the new prefix filter.
		if len(toks) >= 3 {
			ctx.envPrefix = toks[2]
			loadOSEnv(ctx.currentProto.Env, ctx.envPrefix)
		}
	case "SET":
		if len(toks) >= 4 {
			ctx.currentProto.Env[toks[2]] = strings.Trim(strings.Join(toks[3:], " "), "\"'`")
		}
	case "REMOVE":
		if len(toks) >= 3 {
			delete(ctx.currentProto.Env, toks[2])
		}
	case "DEFAULT":
		if len(toks) >= 4 {
			if _, exists := ctx.currentProto.Env[toks[2]]; !exists {
				ctx.currentProto.Env[toks[2]] = strings.Trim(strings.Join(toks[3:], " "), "\"'`")
			}
		}
	default:
		// ENV "filepath" — load and merge immediately.
		envPath := resolvePath(toks[1], sourceFile)
		if err := loadEnvFile(envPath, ctx.currentProto.Env, ctx.envPrefix); err != nil {
			return fmt.Errorf("ENV: cannot load %q: %w", envPath, err)
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CONF — parse config file and merge into proto.Configs immediately
//
//   CONF TYPE filepath
//   CONF filepath       (type inferred from extension)
// ─────────────────────────────────────────────────────────────────────────────

func (ctx *parseContext) parseCONF(toks []string, sourceFile string) error {
	if ctx.currentProto == nil || len(toks) < 2 {
		return nil
	}
	var confType, confPath string
	if len(toks) >= 3 {
		confType = strings.ToUpper(toks[1])
		confPath = toks[2]
	} else {
		confPath = toks[1]
		confType = extToConfType(confPath)
	}
	fullPath := resolvePath(confPath, sourceFile)
	if err := loadConfFile(fullPath, confType, ctx.currentProto.Configs); err != nil {
		return fmt.Errorf("CONF: cannot load %q: %w", fullPath, err)
	}
	return nil
}

// SET key value  →  Configs[key] = value  (overwrite)
func (ctx *parseContext) parseSET(toks []string) error {
	if ctx.currentProto == nil || len(toks) < 3 {
		return nil
	}
	ctx.currentProto.Configs[toks[1]] = strings.Trim(strings.Join(toks[2:], " "), "\"'`")
	return nil
}

// DEF key value  →  Configs[key] = value  (only if absent)
func (ctx *parseContext) parseDEF(toks []string) error {
	if ctx.currentProto == nil || len(toks) < 3 {
		return nil
	}
	if _, exists := ctx.currentProto.Configs[toks[1]]; !exists {
		ctx.currentProto.Configs[toks[1]] = strings.Trim(strings.Join(toks[2:], " "), "\"'`")
	}
	return nil
}

// DEL key  →  delete Configs[key]
func (ctx *parseContext) parseDEL(toks []string) error {
	if ctx.currentProto == nil || len(toks) < 2 {
		return nil
	}
	delete(ctx.currentProto.Configs, toks[1])
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// AUTH
// ─────────────────────────────────────────────────────────────────────────────

func (ctx *parseContext) parseAUTH(toks []string) error {
	if ctx.currentProto == nil || len(toks) < 2 {
		return nil
	}
	sub := strings.ToUpper(toks[1])
	switch sub {
	case "JSON", "YAML", "TOML", "ENV":
		if len(toks) >= 3 {
			ctx.currentProto.Auth.Append(&AuthConfig{
				Type: AuthFile, Format: sub, Filepath: toks[2], BaseDir: ctx.currentProto.BaseDir,
			})
		}
	case "CSV":
		if len(toks) >= 3 {
			ctx.currentProto.Auth.Append(&AuthConfig{
				Type: AuthCSV, Filepath: toks[2], BaseDir: ctx.currentProto.BaseDir,
			})
		}
	case "USER":
		if len(toks) >= 4 {
			ctx.currentProto.Auth.Append(&AuthConfig{
				Type: AuthUser, User: toks[2], Password: toks[3], BaseDir: ctx.currentProto.BaseDir,
			})
		}
	case "HANDLER":
		if len(toks) >= 3 {
			args := Arguments{}
			if len(toks) > 3 {
				args = parseArgs(strings.Join(toks[3:], " "))
			}
			ctx.currentProto.Auth.Append(&AuthConfig{
				Type: AuthScript, Handler: toks[2], Inline: false, Configs: args, BaseDir: ctx.currentProto.BaseDir,
			})
		}
	case "BEGIN":
		args := Arguments{}
		if len(toks) > 2 {
			args = parseArgs(strings.Join(toks[2:], " "))
		}
		ac := &AuthConfig{Type: AuthScript, Inline: true, Configs: args, BaseDir: ctx.currentProto.BaseDir}
		ctx.currentProto.Auth.Append(ac)
		ctx.inlineTarget = inlineTarget{authConfig: ac}
		ctx.inlineCode.Reset()
	default:
		// AUTH [args]  or standalone AUTH (implies inline script or block)
		args := Arguments{}
		if strings.HasPrefix(toks[1], "[") {
			args = parseArgs(toks[1])
		} else if len(toks) > 1 {
			// Might be a handler path or just args
			if strings.Contains(toks[1], "=") {
				args = parseArgs(strings.Join(toks[1:], " "))
			} else {
				// AUTH handler.js [args]
				handler := toks[1]
				if len(toks) > 2 {
					args = parseArgs(strings.Join(toks[2:], " "))
				}
				ctx.currentProto.Auth.Append(&AuthConfig{
					Type: AuthScript, Inline: false, Handler: handler, Configs: args, BaseDir: ctx.currentProto.BaseDir,
				})
				return nil
			}
		}
		ac := &AuthConfig{Type: AuthScript, Inline: true, Configs: args, BaseDir: ctx.currentProto.BaseDir}
		ctx.currentProto.Auth.Append(ac)
		ctx.inlineTarget = inlineTarget{authConfig: ac}
		ctx.inlineCode.Reset()
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Route parser
//
//   BEGIN  → inline   (A)
//   DEFINE → group    (C)
//   else   → non-inline (B) — filepath optional
// ─────────────────────────────────────────────────────────────────────────────

func (ctx *parseContext) parseRoute(toks []string, pl parsedLine) error {
	if ctx.currentProto == nil || len(toks) < 1 {
		return nil
	}
	cmd := strings.ToUpper(toks[0])

	// Pass 1: detect markers, strip them, lift trailing [args]
	isInline, isGroup := false, false
	var args Arguments
	var raw []string

	for _, tok := range toks[1:] {
		switch strings.ToUpper(tok) {
		case "BEGIN":
			isInline = true
		case "DEFINE":
			isGroup = true
		default:
			raw = append(raw, tok)
		}
	}
	if len(raw) > 0 {
		last := raw[len(raw)-1]
		if strings.HasPrefix(last, "[") && strings.HasSuffix(last, "]") {
			args = parseArgs(last)
			raw = raw[:len(raw)-1]
		}
	}

	// Pass 2: Separate middlewares from other tokens (middlewares start with @)
	var middlewares []MiddlewareUse
	var filtered []string
	for _, tok := range raw {
		if strings.HasPrefix(tok, "@") {
			mu := MiddlewareUse{Args: make(Arguments)}
			bi := strings.Index(tok, "[")
			ei := strings.LastIndex(tok, "]")
			if bi == -1 {
				bi = strings.Index(tok, "(")
				ei = strings.LastIndex(tok, ")")
			}
			if bi != -1 && ei != -1 && ei > bi {
				mu.Name = strings.ToUpper(tok[1:bi])
				mu.Args = parseArgs(tok[bi : ei+1])
			} else {
				mu.Name = strings.ToUpper(tok[1:])
			}
			middlewares = append(middlewares, mu)
		} else {
			filtered = append(filtered, tok)
		}
	}

	// Refinement: MIDDLEWARE directive syntax rules
	if cmd == "MIDDLEWARE" {
		if len(middlewares) > 0 && isInline {
			return fmt.Errorf("%s:%d: MIDDLEWARE with named middlewares (@...) cannot have BEGIN/END blocks", pl.file, pl.lineNum)
		}
		// Enforce arguments position: must be after file or BEGIN
		// If toks[1] is an argument block, it's only allowed if there are no more tokens
		// (which means it's a standalone MIDDLEWARE [args] line, likely used with @MW tags handled later)
		// But if it's followed by BEGIN or a file, it's an error.
		if len(toks) > 1 && strings.HasPrefix(toks[1], "[") && len(toks) > 2 {
			return fmt.Errorf("%s:%d: MIDDLEWARE arguments must come after the file path or BEGIN keyword", pl.file, pl.lineNum)
		}
	}

	// Pass 3: [path]? [TYPE]? [ContentType]? [filepath]?
	i := 0
	path := ""
	if i < len(filtered) {
		path = filtered[i]
		i++
	}

	handlerType := HandlerType("")
	if i < len(filtered) && knownHandlerTypes[strings.ToUpper(filtered[i])] {
		handlerType = HandlerType(strings.ToUpper(filtered[i]))
		i++
	}

	contentType := ""
	if i < len(filtered) && IsMimeType(filtered[i]) {
		contentType = filtered[i]
		i++
	}

	// filepath: only in non-inline, non-group; optional
	// Handler: all remaining tokens
	handler := ""
	if !isInline && !isGroup && i < len(filtered) {
		handler = strings.Join(filtered[i:], " ")
	}

	// Refinement for single argument directives
	// If only one token is provided and it looks like a file but not a URL path,
	// move it to Handler only if it's a "payload" directive (BODY, FROM, WEBHOOK).
	// For others like WORKER or URLs (containing ://), it's safer to keep in Path.
	if handler == "" && path != "" && !isGroup && !isInline {
		isPayloadCmd := (cmd == "BODY" || cmd == "FROM" || cmd == "WEBHOOK" || cmd == "PROCESSOR")
		if isPayloadCmd && IsFileLike(path) && !strings.Contains(path, "://") && !IsMimeType(path) {
			handler = path
			path = ""
		}
	}

	if !isInline && !isGroup && handler == "" {
		if cmd == "ON" || cmd == "ERROR" || ctx.inlineTarget.authConfig != nil || (cmd == "WORKER" && path == "") {
			isInline = true
		}
	}

	route := &RouteConfig{
		Method:      cmd,
		Path:        path,
		Type:        handlerType,
		Handler:     handler,
		ContentType: contentType,
		Inline:      isGroup || isInline,
		IsGroup:     isGroup,
		Middlewares: middlewares,
		Args:        args,
	}

	if isGroup {
		ctx.groupStack = append(ctx.groupStack, route)
		ctx.inlineCode.Reset()
		return nil
	}

	if len(ctx.groupStack) > 0 {
		top := ctx.groupStack[len(ctx.groupStack)-1]
		top.Routes = append(top.Routes, route)
	} else {
		ctx.currentProto.Routes = append(ctx.currentProto.Routes, route)
	}

	if isInline {
		ctx.inlineTarget = inlineTarget{route: route}
		ctx.inlineCode.Reset()
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Env loading helpers
// ─────────────────────────────────────────────────────────────────────────────

// loadOSEnv seeds dst from os.Environ(), optionally filtered by prefix.
// If prefix is non-empty, only vars whose key starts with prefix are loaded,
// and the prefix is stripped from the key.
func loadOSEnv(dst Arguments, prefix string) {
	for _, e := range os.Environ() {
		idx := strings.IndexByte(e, '=')
		if idx < 0 {
			continue
		}
		k, v := e[:idx], e[idx+1:]
		if prefix == "" {
			dst[k] = v
		} else if strings.HasPrefix(k, prefix) {
			dst[strings.TrimPrefix(k, prefix)] = v
		}
	}
}

// loadEnvFile parses a .env file and merges the values into dst.
// If prefix is non-empty, only keys starting with prefix are loaded (prefix stripped).
func loadEnvFile(path string, dst Arguments, prefix string) error {
	m, err := godotenv.Read(path)
	if err != nil {
		return err
	}
	for k, v := range m {
		if prefix == "" {
			dst[k] = v
		} else if strings.HasPrefix(k, prefix) {
			dst[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Config loading helpers
// ─────────────────────────────────────────────────────────────────────────────

// loadConfFile parses a config file (JSON/YAML/TOML/ENV) and merges the
// top-level key→string pairs into dst.  Non-string values are skipped.
func loadConfFile(path, confType string, dst Arguments) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]interface{}
	switch confType {
	case "JSON":
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
	case "YAML":
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return err
		}
	case "TOML":
		if err := toml.Unmarshal(data, &raw); err != nil {
			return err
		}
	case "ENV":
		m, err := godotenv.Read(path)
		if err != nil {
			return err
		}
		for k, v := range m {
			dst[k] = v
		}
		return nil
	default:
		return fmt.Errorf("unknown config type %q", confType)
	}
	flattenInto(raw, "", dst)
	return nil
}

// flattenInto recursively flattens nested maps into dot-separated keys.
// e.g. {"db": {"host": "localhost"}} → "db.host" = "localhost"
func flattenInto(m map[string]interface{}, prefix string, dst Arguments) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]interface{}:
			flattenInto(val, key, dst)
		case string:
			dst[key] = val
		default:
			if val != nil {
				dst[key] = fmt.Sprintf("%v", val)
			}
		}
	}
}

func extToConfType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "JSON"
	case ".yaml", ".yml":
		return "YAML"
	case ".toml":
		return "TOML"
	case ".env":
		return "ENV"
	}
	return ""
}

func resolvePath(p, sourceFile string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(sourceFile), p)
}

// ─────────────────────────────────────────────────────────────────────────────
// Misc helpers
// ─────────────────────────────────────────────────────────────────────────────

var knownHandlerTypes = map[string]bool{
	"TEMPLATE": true, "HANDLER": true, "FILE": true,
	"BINARY": true, "BASE32": true, "BASE64": true, "HEX": true,
	"TEXT": true, "JSON": true, "YAML": true, "TOML": true, "ENV": true,
}


func newProto(name, address string, args Arguments, file string) *DirectiveConfig {
	return &DirectiveConfig{
		Name:    name,
		Address: address,
		Args:    args,
		BaseDir: filepath.Dir(file),
		Env:     make(Arguments),
		Configs: make(Arguments),
		Auth:    AuthConfigs(make([]*AuthConfig, 0)),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Include preprocessing
// ─────────────────────────────────────────────────────────────────────────────

func preprocessInclude(path string, visiting map[string]bool, allFiles map[string]bool) ([]parsedLine, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if visiting[absPath] {
		return nil, fmt.Errorf("fatal: circular include detected: %s", absPath)
	}
	stat, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("fatal: include file not found: %s", absPath)
	}
	if stat.IsDir() {
		return nil, fmt.Errorf("fatal: include path is a directory: %s", absPath)
	}
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	visiting[absPath] = true
	allFiles[absPath] = true
	defer func() { delete(visiting, absPath) }()

	var lines []parsedLine
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		text := scanner.Text()
		if trimmed := strings.TrimSpace(text); strings.HasPrefix(trimmed, "INCLUDE") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				incPath := strings.Trim(parts[1], "\"'`")
				if !filepath.IsAbs(incPath) {
					incPath = filepath.Join(filepath.Dir(absPath), incPath)
				}
				incLines, err := preprocessInclude(incPath, visiting, allFiles)
				if err != nil {
					return nil, err
				}
				lines = append(lines, incLines...)
				continue
			}
		}
		lines = append(lines, parsedLine{content: text, file: absPath, lineNum: lineNum})
	}
	return lines, scanner.Err()
}

func preprocessString(content string, cwd []string, visiting map[string]bool, allFiles map[string]bool) ([]parsedLine, error) {
	fakeFile := "inline"
	if len(cwd) > 0 {
		fakeFile = filepath.Join(cwd[0], "inline")
	}
	var lines []parsedLine
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		text := scanner.Text()
		if trimmed := strings.TrimSpace(text); strings.HasPrefix(trimmed, "INCLUDE") && len(cwd) > 0 {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				incPath := strings.Trim(parts[1], "\"'`")
				if !filepath.IsAbs(incPath) {
					incPath = filepath.Join(cwd[0], incPath)
				}
				incLines, err := preprocessInclude(incPath, visiting, allFiles)
				if err != nil {
					return nil, err
				}
				lines = append(lines, incLines...)
				continue
			}
		}
		lines = append(lines, parsedLine{content: text, file: fakeFile, lineNum: lineNum})
	}
	return lines, scanner.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// tokenizeLine
// ─────────────────────────────────────────────────────────────────────────────

func tokenizeLine(line string) []string {
	var tokens []string
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		inQuote := byte(0)
		depth := 0
		for i < len(line) {
			c := line[i]
			if c == '\\' && i+1 < len(line) {
				i += 2
				continue
			}
			if inQuote != 0 {
				if c == inQuote {
					inQuote = 0
				}
			} else {
				switch c {
				case '"', '\'', '`':
					inQuote = c
				case '[', '(':
					depth++
				case ']', ')':
					depth--
				case ' ', '\t':
					if depth <= 0 {
						goto endTok
					}
				}
			}
			i++
		}
	endTok:
		tok := line[start:i]
		if len(tok) >= 2 && (tok[0] == '"' || tok[0] == '\'' || tok[0] == '`') && tok[0] == tok[len(tok)-1] {
			tok = tok[1 : len(tok)-1]
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

// ─────────────────────────────────────────────────────────────────────────────
// parseArgs
// ─────────────────────────────────────────────────────────────────────────────

func parseArgs(s string) Arguments {
	s = strings.TrimSpace(s)
	if (strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) ||
		(strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")) {
		s = s[1 : len(s)-1]
	}
	args := make(Arguments)
	var parts []string
	for _, cp := range strings.Split(s, ",") {
		if cp = strings.TrimSpace(cp); cp != "" {
			parts = append(parts, tokenizeLine(cp)...)
		}
	}
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		key := stripQuotes(kv[0])
		if len(kv) == 2 {
			val := stripQuotes(kv[1])
			val = strings.ReplaceAll(val, `\"`, `"`)
			val = strings.ReplaceAll(val, `\'`, `'`)
			val = strings.ReplaceAll(val, "\\`", "`")
			args[key] = val
		} else {
			args[key] = "true"
		}
	}
	return args
}

func stripQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'' || s[0] == '`') && s[0] == s[len(s)-1] {
		return s[1 : len(s)-1]
	}
	return s
}
