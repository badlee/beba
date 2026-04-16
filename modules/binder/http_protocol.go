package binder

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"

	"http-server/modules/crud"
	"http-server/modules/sse"
	"http-server/plugins/httpserver"
	"http-server/plugins/js"
	"http-server/processor"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/etag"
	"github.com/gofiber/fiber/v3/middleware/healthcheck"
	"github.com/gofiber/fiber/v3/middleware/helmet"
	"github.com/gofiber/fiber/v3/middleware/idempotency"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/pprof"
	"github.com/gofiber/fiber/v3/middleware/proxy"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/gofiber/fiber/v3/middleware/responsetime"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/gofiber/fiber/v3/middleware/timeout"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/acme/autocert"
)

// ─────────────────────────────────────────────────────────────────────────────
// HTTPDirective
// ─────────────────────────────────────────────────────────────────────────────

type HTTPDirective struct {
	App         *httpserver.HTTP
	server      *fasthttp.Server
	tlsConfig   *tls.Config
	acmeManager *autocert.Manager
	address     string
	domain      string
	aliases     []string
	cfg         *DirectiveConfig // Added to support security policy lookup in Manager
}

func NewHTTPDirective(config *DirectiveConfig) *HTTPDirective {

	directive := &HTTPDirective{
		cfg: config,
	}
	// ── Settings ──────────────────────────────────────────────────────────────
	// config.Configs is already populated by the parser (SET/DEF/DEL + CONF files).
	// config.Env is already populated (os.Environ + ENV files + SET/REMOVE/DEFAULT).
	// No env loading needed here.
	settings := config.Configs // alias: used wherever Settings was referenced

	// ── Extract HTTP-specific routes from the unified Routes slice ────────────
	// Each of these was previously a dedicated field in DirectiveConfig.
	// Now every protocol-specific directive is a *RouteConfig in Routes.
	errors := config.GetRoutes("ERROR")
	workers := config.GetRoutes("WORKER")
	proxies := config.GetRoutes("PROXY")
	rewrites := config.GetRoutes("REWRITE")
	redirects := config.GetRoutes("REDIRECT")
	middlewares := config.GetRoutes("MIDDLEWARE")
	sslRoutes := config.GetRoutes("SSL")
	domainRoutes := config.GetRoutes("DOMAIN")
	aliasRoutes := config.GetRoutes("ALIASES")
	securityRoutes := config.GetRoutes("SECURITY")

	var defaultWAF *httpserver.WAFConfig
	if len(securityRoutes) > 0 {
		defaultWAF = httpserver.GetWAF(securityRoutes[0].Path)
		if defaultWAF != nil {
			defaultWAF.AppName = config.Name
		}
	}

	domain := ""
	if len(domainRoutes) > 0 {
		domain = domainRoutes[0].Path
	}
	var aliases []string
	for _, a := range aliasRoutes {
		aliases = append(aliases, a.Path)
	}

	// ── SSL ───────────────────────────────────────────────────────────────────
	var tlsConfig *tls.Config
	var acmeManager *autocert.Manager

	if len(sslRoutes) > 0 {
		ssl := sslRoutes[0]
		toks := strings.Fields(ssl.Handler) // re-use handler as the raw arg string if needed
		if ssl.Args.GetBool("auto") || (len(toks) > 0 && strings.ToUpper(toks[0]) == "AUTO") {
			domain := ssl.Args.Get("domain", ssl.Path)
			email := ssl.Args.Get("email", "")
			acmeManager = &autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(domain),
				Email:      email,
				Cache:      autocert.DirCache("cache-certs"),
			}
			tlsConfig = acmeManager.TLSConfig()
		} else {
			// SSL [key] [cert]  →  stored as Path=key, Handler=cert  (non-inline non-group)
			certPath := ssl.Args.Get("cert", ssl.Handler)
			keyPath := ssl.Args.Get("key", ssl.Path)
			if !filepath.IsAbs(certPath) {
				certPath = filepath.Join(config.BaseDir, certPath)
			}
			if !filepath.IsAbs(keyPath) {
				keyPath = filepath.Join(config.BaseDir, keyPath)
			}
			fmt.Printf("Loading SSL: Cert=%s Key=%s\n", certPath, keyPath)
			certBytes, err1 := os.ReadFile(certPath)
			keyBytes, err2 := os.ReadFile(keyPath)
			if err1 == nil && err2 == nil {
				cert, err := tls.X509KeyPair(certBytes, keyBytes)
				if err != nil {
					cert2, err2 := tls.X509KeyPair(keyBytes, certBytes)
					if err2 == nil {
						fmt.Printf("SSL Warning: Swapped key and cert worked! Auto-correcting.\n")
						tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert2}}
					} else {
						fmt.Printf("SSL Error: %v\n", err)
					}
				} else {
					tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
				}
			} else {
				fmt.Printf("SSL Read Error: cert=%v, key=%v\n", err1, err2)
			}
		}
	}

	// ── Error handler ─────────────────────────────────────────────────────────
	fiberCfg := fiber.Config{AppName: config.Name}

	if len(errors) > 0 {
		fiberCfg.ErrorHandler = buildErrorHandler(config, errors, settings)
	}

	app := httpserver.New(httpserver.Config{
		AppName:      config.Name,
		Secret:       config.AppConfig.SecretKey,
		ErrorHandler: fiberCfg.ErrorHandler,
	})

	// ── Workers ───────────────────────────────────────────────────────────────
	for _, w := range workers {
		go runWorker(w, config, settings)
	}

	// ── Proxies ───────────────────────────────────────────────────────────────
	for _, p := range proxies {
		registerProxy(app, p)
	}

	// ── Rewrites & Redirects ──────────────────────────────────────────────────
	if len(rewrites) > 0 || len(redirects) > 0 {
		registerRewritesRedirects(app, rewrites, redirects, config)
	}

	// ── Middlewares (global, sorted by priority arg) ──────────────────────────
	sort.SliceStable(middlewares, func(i, j int) bool {
		return middlewares[i].Args.GetInt("priority") < middlewares[j].Args.GetInt("priority")
	})
	for _, m := range middlewares {
		registerGlobalMiddleware(app, m, config)
	}

	// ── Routes ───────────────────────────────────────────────────────────────
	var registerRoutes func(router fiber.Router, routes []*RouteConfig)
	registerRoutes = func(router fiber.Router, routes []*RouteConfig) {
		for _, rP := range routes {
			r := *rP
			// Skip HTTP-infrastructure routes already handled above
			switch r.Method {
			case "ERROR", "WORKER", "PROXY", "REWRITE", "REDIRECT", "MIDDLEWARE", "SSL", "CRUD", "SECURITY":
				continue
			}
			if r.IsGroup && r.Method != "GROUP" {
				continue
			}

			path := r.Path
			method := r.Method
			rType := r.Type
			contentType := r.ContentType
			if !strings.Contains(contentType, "/") {
				contentType = ""
			}

			var handlerCode []byte
			var err error
			if r.Inline {
				handlerCode, err = r.Content(config)
				if err != nil {
					router.Add([]string{method}, path, func(c fiber.Ctx) error {
						return c.Status(500).SendString("Route Error: " + err.Error())
					})
					continue
				}
			} else {
				handlerCode = []byte(r.Handler)
			}

			// Named middleware handlers (@MW tokens)
			var handlers []any
			var timeoutMiddleware = func(fn fiber.Handler) fiber.Handler { return fn }

			useWAF := true
			var localWAF *httpserver.WAFConfig

			for _, mw := range r.Middlewares {
				name := strings.ToUpper(mw.Name)
				switch name {
				case "CONTENTTYPE":
					handlers = append(handlers, func(c fiber.Ctx) error {
						ct := c.Get("Content-Type")
						expected := mw.Args.Get("0", mw.Args.Get("type", "application/json"))
						if !strings.Contains(ct, expected) {
							httpserver.RecordSecurityBlock(config.Name, "protocol_violation")
							return c.Status(fiber.StatusUnsupportedMediaType).SendString("Unsupported Media Type: Expected " + expected)
						}
						return c.Next()
					})
				case "BOT":
					// @BOT[js_challenge=true threshold=50]
					botCfg := &httpserver.BotConfig{
						Enabled:         true,
						BlockCommonBots: true,
						JSChallenge:     isTrue(mw.Args.Get("js_challenge")),
						ChallengeSecret: config.AppConfig.SecretKey, // Reuse app secret
					}
					if mw.Args.Has("threshold") {
						if t, err := strconv.Atoi(mw.Args.Get("threshold")); err == nil {
							botCfg.ScoreThreshold = t
						}
					}
					if mw.Args.Has("path") {
						botCfg.ChallengePath = mw.Args.Get("path")
					}
					handlers = append(handlers, httpserver.BotMiddleware(botCfg))
				case "AUDIT":
					// @AUDIT[path="security.log" sign=true level="security"]
					auditCfg := &httpserver.AuditConfig{
						Enabled: true,
						Path:    mw.Args.Get("path", "audit.log"),
						Signed:  isTrue(mw.Args.Get("sign")),
						Level:   mw.Args.Get("level", "security"),
					}
					// Initialize for this specific directive if not using global
					httpserver.InitSecurityLogger(auditCfg, config.AppConfig.SecretKey)
					handlers = append(handlers, httpserver.AuditMiddleware(auditCfg))
				case "HELMET":
					cspMode := strings.ToLower(mw.Args.Get("csp"))

					// CSP Defaults from project guidelines
					htmxURL := config.AppConfig.HtmxURL
					if htmxURL == "" {
						htmxURL = "https://unpkg.com/htmx.org@2.0.0"
					}
					bootstrapJS := "https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js"
					bootstrapCSS := "https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css"
					bootstrapIcons := "https://cdn.jsdelivr.net/npm/bootstrap-icons@1.11.3/font/bootstrap-icons.min.css"
					googleFontsCSS := "fonts.googleapis.com"
					googleFontsFiles := "fonts.gstatic.com"
					googleOAuth := "accounts.google.com"

					switch cspMode {
					case "strict":
						handlers = append(handlers, func(c fiber.Ctx) error {
							// Generate secure random nonce
							b := make([]byte, 16)
							rand.Read(b)
							nonce := base64.StdEncoding.EncodeToString(b)
							c.Locals("cspNonce", nonce)

							policy := fmt.Sprintf("default-src 'none'; "+
								"script-src 'self' %s %s 'nonce-%s'; "+
								"style-src 'self' %s %s 'nonce-%s'; "+
								"img-src 'self' data: https:; "+
								"connect-src 'self' wss: ws:; "+
								"font-src 'self' %s %s; "+
								"frame-src %s; "+
								"frame-ancestors 'none'; "+
								"base-uri 'self'; "+
								"form-action 'self';",
								htmxURL, bootstrapJS, nonce,
								bootstrapCSS, googleFontsCSS, nonce,
								googleFontsFiles, bootstrapIcons,
								googleOAuth)
							c.Set("Content-Security-Policy", policy)
							return c.Next()
						})
					case "compatible":
						handlers = append(handlers, func(c fiber.Ctx) error {
							hashes := mw.Args.Get("scriptHashes")
							styleSrc := mw.Args.Get("styleSrc", "'self' 'unsafe-inline'")
							connectSrc := mw.Args.Get("connectSrc", "wss: ws:")
							fontSrc := mw.Args.Get("fontSrc", "'self' https://fonts.gstatic.com")

							hList := split(hashes, " ")
							scriptSrc := fmt.Sprintf("'self' %s %s", htmxURL, bootstrapJS)
							for _, h := range hList {
								scriptSrc += " '" + h + "'"
							}

							// Enrich style-src and font-src with default CDNs
							styleSrc += fmt.Sprintf(" %s %s", bootstrapCSS, googleFontsCSS)
							fontSrc += fmt.Sprintf(" %s %s", googleFontsFiles, bootstrapIcons)

							policy := fmt.Sprintf("default-src 'none'; "+
								"script-src %s; "+
								"style-src %s; "+
								"img-src 'self' data: https:; "+
								"connect-src %s; "+
								"font-src %s; "+
								"frame-src %s; "+
								"frame-ancestors 'none'; "+
								"base-uri 'self'; "+
								"form-action 'self';",
								scriptSrc, styleSrc, connectSrc, fontSrc, googleOAuth)
							c.Set("Content-Security-Policy", policy)
							return c.Next()
						})
					default:
						handlers = append(handlers, helmet.New(helmet.Config{
							XSSProtection:             mw.Args.Get("xss", "0"),
							ContentTypeNosniff:        mw.Args.Get("contentTypeNosniff", "nosniff"),
							XFrameOptions:             mw.Args.Get("frameOptions", "SAMEORIGIN"),
							ReferrerPolicy:            mw.Args.Get("referrerPolicy", "no-referrer"),
							CrossOriginEmbedderPolicy: mw.Args.Get("crossOriginEmbedderPolicy", "require-corp"),
							CrossOriginOpenerPolicy:   mw.Args.Get("crossOriginOpenerPolicy", "same-origin"),
							CrossOriginResourcePolicy: mw.Args.Get("crossOriginResourcePolicy", "same-origin"),
							OriginAgentCluster:        mw.Args.Get("originAgent", "?1"),
							XDNSPrefetchControl:       mw.Args.Get("dnsPrefetchControl", "off"),
							XDownloadOptions:          mw.Args.Get("downloadOptions", "noopen"),
							XPermittedCrossDomain:     mw.Args.Get("permittedCrossDomain", "none"),
						}))
					}
				case "ETAG":
					handlers = append(handlers, etag.New(etag.Config{
						Weak: isTrue(mw.Args.Get("weak", "false")),
					}))
				case "IDEMPOTENCY":
					exp, _ := time.ParseDuration(mw.Args.Get("expiration", "30s"))
					if exp == 0 {
						exp = 30 * time.Second
					}
					handlers = append(handlers, idempotency.New(idempotency.Config{
						KeyHeader:           mw.Args.Get("header", "X-Idempotency-Key"),
						Lifetime:            exp,
						KeepResponseHeaders: split(mw.Args.Get("responseHeaders")),
					}))
				case "LIMITER":
					maxReq := mw.Args.GetInt("max", 10)
					exp, _ := time.ParseDuration(mw.Args.Get("expiration", "30s"))
					if exp == 0 {
						exp = 30 * time.Second
					}
					handlers = append(handlers, limiter.New(limiter.Config{
						Max:               maxReq,
						Expiration:        exp,
						LimiterMiddleware: limiter.SlidingWindow{},
						LimitReached: func(c fiber.Ctx) error {
							httpserver.RecordSecurityBlock(config.Name, "rate_limit")
							return c.Status(fiber.StatusTooManyRequests).SendString("Too Many Requests")
						},
					}))
				case "REQUESTID":
					handlers = append(handlers, requestid.New(requestid.Config{
						Header: mw.Args.Get("header", "X-Request-ID"),
					}))
				case "REQUESTTIME":
					handlers = append(handlers, responsetime.New(responsetime.Config{
						Header: mw.Args.Get("header", "X-Response-Time"),
					}))
				case "TIMEOUT":
					exp, _ := time.ParseDuration(mw.Args.Get("expiration", "30s"))
					if exp == 0 {
						exp = 3 * time.Second
					}
					timeoutMiddleware = func(fn fiber.Handler) fiber.Handler {
						return timeout.New(fn, timeout.Config{Timeout: exp})
					}
				case "CSRF":
					handlers = append(handlers, func(c fiber.Ctx) error {
						method := c.Method()
						if method == "GET" || method == "HEAD" || method == "OPTIONS" || method == "TRACE" {
							return c.Next()
						}
						// Webhook exemptions
						path := c.Path()
						if strings.HasPrefix(path, "/payment/webhook") || strings.HasPrefix(path, "/mail/webhook") {
							return c.Next()
						}
						// Same-origin proof via custom headers
						if c.Get("X-Requested-With") != "" || c.Get("HX-Request") != "" || c.Get("X-CSRF-Token") != "" {
							return c.Next()
						}
						// Safe content types (preflight required)
						contentType := c.Get("Content-Type")
						if strings.Contains(contentType, "application/json") {
							return c.Next()
						}
						httpserver.RecordSecurityBlock(config.Name, "csrf")
						return c.Status(fiber.StatusForbidden).SendString("CSRF Verification Failed: Custom Header Required")
					})
				case "WAF":
					wafCfg := &httpserver.WAFConfig{
						Enabled:  true,
						Rules:    split(mw.Args.Get("rules")),
						AuditLog: mw.Args.Get("auditLog"),
					}
					handlers = append(handlers, httpserver.WAFMiddleware(wafCfg))
				case "IP":
					allow := split(mw.Args.Get("allow"))
					block := split(mw.Args.Get("block"))
					handlers = append(handlers, func(c fiber.Ctx) error {
						clientIP := net.ParseIP(c.IP())
						if len(block) > 0 {
							for _, b := range block {
								_, cidr, err := net.ParseCIDR(b)
								if err == nil && cidr.Contains(clientIP) {
									return c.Status(fiber.StatusForbidden).SendString("IP Blocked")
								}
								if b == c.IP() {
									return c.Status(fiber.StatusForbidden).SendString("IP Blocked")
								}
							}
						}
						if len(allow) > 0 {
							allowed := false
							for _, a := range allow {
								_, cidr, err := net.ParseCIDR(a)
								if (err == nil && cidr.Contains(clientIP)) || a == c.IP() {
									allowed = true
									break
								}
							}
							if !allowed {
								return c.Status(fiber.StatusForbidden).SendString("IP Not Allowed")
							}
						}
						return c.Next()
					})
				case "GEO":
					allow := split(mw.Args.Get("allow"))
					block := split(mw.Args.Get("block"))
					handlers = append(handlers, httpserver.GeoMiddleware(&httpserver.GeoConfig{
						Enabled:        true,
						AllowCountries: allow,
						BlockCountries: block,
					}, app.GeoDB))
				case "CORS":
					allowOriginsList := split(mw.Args.Get("origins"))
					allowMethods := split(mw.Args.Get("methods", "GET,POST,PUT,DELETE,OPTIONS"))
					allowHeaders := split(mw.Args.Get("headers", "Content-Type, Authorization"))

					allowCredentials := isTrue(mw.Args.Get("credentials", "true"))
					if slices.Contains(allowOriginsList, "*") && !mw.Args.Has("credentials") {
						allowCredentials = false
					}

					corsCfg := cors.Config{
						AllowMethods:     allowMethods,
						AllowHeaders:     allowHeaders,
						AllowCredentials: allowCredentials,
						ExposeHeaders:    split(mw.Args.Get("expose", "Content-Length")),
						MaxAge:           mw.Args.GetInt("maxAge", 86400),
					}
					if slices.Contains(allowOriginsList, "*") {
						corsCfg.AllowOrigins = []string{"*"}
					} else {
						corsCfg.AllowOriginsFunc = func(origin string) bool {
							for _, o := range allowOriginsList {
								if strings.ToLower(origin) == o {
									return true
								}
							}
							return false
						}
					}
					handlers = append(handlers, cors.New(corsCfg))
				case "ADMIN":
					redirect := mw.Args.Get("redirect")
					message := mw.Args.Get("message", "Admin Access Required")
					basic := mw.Args.Has("basic")
					handlers = append(handlers, func(c fiber.Ctx) error {
						return handleAdminAuth(c, config.Auth, redirect, message, config.Name, basic)
					})
				case "SECURITY":
					localWAF = httpserver.GetWAF(mw.Args.Get("0", mw.Args.Get("name")))
					if localWAF != nil {
						localWAF.AppName = config.Name
					}
				case "UNSECURE":
					useWAF = false
				case "PAYMENT":
					paymentName := mw.Args.Get("name", "")
					price := mw.Args.Get("price", "")
					desc := mw.Args.Get("desc", "")
					ref := mw.Args.Get("ref", "")
					scheme := mw.Args.Get("scheme", "")
					ttlStr := mw.Args.Get("ttl", "")
					handlers = append(handlers, paymentGateMiddleware(paymentGateConfig{
						Name:        paymentName,
						Price:       price,
						Description: desc,
						Ref:         ref,
						Scheme:      scheme,
						TTLOverride: ttlStr,
					}))
				}
			}

			if useWAF {
				wafToUse := localWAF
				if wafToUse == nil {
					wafToUse = defaultWAF
				}
				if wafToUse != nil {
					handlers = append(handlers, httpserver.WAFMiddleware(wafToUse))
				}
			}

			if method == "GROUP" && r.IsGroup {
				grp := router.Group(path, handlers...)
				registerRoutes(grp, r.Routes)
				continue
			}
			if method == "PPROF" {
				all := append(handlers, pprof.New(pprof.Config{Prefix: ""}))
				router.Get(path, all[0], all[1:]...)
				continue
			}
			if method == "HEALTH" {
				all := append(handlers, healthcheck.New())
				router.Get(path, all[0], all[1:]...)
				continue
			}
			if method == "SSE" {
				sseHandler := timeoutMiddleware(func(c fiber.Ctx) error {
					if len(handlerCode) > 0 {
						if r.Inline {
							vm := processor.New(config.BaseDir, c, config.AppConfig)
							_, err := vm.RunString(`const sse = require("sse");` + "\n" + string(handlerCode))
							if err != nil {
								return c.Status(500).SendString("SSE Init Error: " + err.Error())
							}
						} else {
							if _, err := processor.ProcessFile(string(handlerCode), c, config.AppConfig); err != nil {
								return c.Status(500).SendString("SSE Init Error: " + err.Error())
							}
						}
					}
					return sse.Handler(c)
				})
				all := append(handlers, sseHandler)
				router.Get(path, all[0], all[1:]...)
				continue
			}
			if method == "WS" {
				all := append(handlers, sse.WSUpgradeMiddleware, websocket.New(func(conn *websocket.Conn) {
					if len(handlerCode) > 0 {
						if r.Inline {
							vm := processor.New(config.BaseDir, nil, config.AppConfig)
							vm.Set("conn", conn)
							vm.RunString(string(handlerCode))
						} else {
							processor.ProcessFile(string(handlerCode), nil, config.AppConfig)
						}
					}
					sse.WSHandler(conn)
				}))
				router.Get(path, timeoutMiddleware(all[0].(fiber.Handler)), all[1:]...)
				continue
			}
			if method == "IO" {
				all := append(handlers, func(c fiber.Ctx) error {
					if websocket.IsWebSocketUpgrade(c) {
						c.Locals("sid", c.Cookies("sid"))
						return c.Next()
					}
					return fiber.ErrUpgradeRequired
				}, sse.SIOHandler())
				router.Get(path, timeoutMiddleware(all[0].(fiber.Handler)), all[1:]...)
				continue
			}
			if method == "MQTT" {
				var authFn func(string, string, string) (string, error)
				if r.Args.GetBool("auth", true) && len(config.Auth) > 0 {
					authFn = func(username, password, clientID string) (string, error) {
						if err := config.Auth.Auth(username, password); err != nil {
							return "", err
						}
						return clientID, nil
					}
				}
				mqttCfg := sse.MQTTConfig{
					Auth: authFn,
				}
				all := append(handlers, sse.MQTTUpgradeMiddleware, websocket.New(sse.MQTTHandler(mqttCfg)))
				router.Get(path, all[0], all[1:]...)
				continue
			}
			if method == "ROUTER" {
				args := append([]any{path}, handlers...)
				if !r.Inline && len(handlerCode) > 0 {
					dir := string(handlerCode)
					if !filepath.IsAbs(dir) {
						dir = filepath.Join(config.BaseDir, dir)
					}
					stat, err := os.Stat(dir)
					if err != nil {
						args = append(args, timeoutMiddleware(func(c fiber.Ctx) error {
							return c.Status(500).SendString("Router Error: " + err.Error())
						}))
					} else if !stat.IsDir() {
						args = append(args, timeoutMiddleware(func(c fiber.Ctx) error {
							return c.Status(500).SendString("Router Error: Not a directory")
						}))
					} else {
						cfg := httpserver.RouterConfig{
							Root:        dir,
							TemplateExt: ".html",
							IndexFile:   "index",
							AppConfig:   config.AppConfig,
						}
						if r.Args != nil {
							cfg.Settings = r.Args
						} else {
							cfg.Settings = make(map[string]string)
						}
						if r.Args.Has("templateExt") {
							cfg.TemplateExt = "." + strings.TrimPrefix(r.Args.Get("templateExt", "html"), ".")
							cfg.Settings["templateExt"] = cfg.TemplateExt
						}
						if r.Args.Has("indexFile") {
							cfg.IndexFile = r.Args.Get("indexFile", "index")
							cfg.Settings["indexFile"] = cfg.IndexFile
						}
						if r.Args.Has("serveFiles") {
							cfg.ServeFiles = isTrue(r.Args.Get("serveFiles"))
							cfg.Settings["serveFiles"] = strconv.FormatBool(cfg.ServeFiles)
						}
						if r.Args.Has("strictSlash") {
							cfg.StrictSlash = isTrue(r.Args.Get("strictSlash"))
							cfg.Settings["strictSlash"] = strconv.FormatBool(cfg.StrictSlash)
						}
						if r.Args.Has("exclude") {
							excludes := split(r.Args.Get("exclude"))
							cfg.Exclude = make([]*regexp.Regexp, 0)
							for _, e := range excludes {
								if ex, err := regexp.Compile(e); err == nil {
									cfg.Exclude = append(cfg.Exclude, ex)
								}
							}
							cfg.Settings["exclude"] = r.Args.Get("exclude")
						}
						h, err := httpserver.FsRouter(cfg)
						if err != nil {
							args = append(args, timeoutMiddleware(func(c fiber.Ctx) error {
								return c.Status(500).SendString("Router Error: " + err.Error())
							}))
						} else {
							args = append(args, timeoutMiddleware(h))
						}
					}
				} else {
					args = append(args, timeoutMiddleware(func(c fiber.Ctx) error {
						return c.Status(500).SendString("Router Error: Inline router not supported")
					}))
				}
				router.Use(args...)
				continue
			}
			if method == "STATIC" {
				if !r.Inline && len(handlerCode) > 0 {
					dir := string(handlerCode)
					if !filepath.IsAbs(dir) {
						dir = filepath.Join(config.BaseDir, dir)
					}
					args := append([]any{path}, handlers...)
					staticCfg := static.Config{
						IndexNames: split(r.Args.Get("indexName", "index.html")),
						Browse:     isTrue(r.Args.Get("browse")),
						Compress:   isTrue(r.Args.Get("compress")),
						ByteRange:  isTrue(r.Args.Get("byteRange")),
						Download:   isTrue(r.Args.Get("download")),
					}
					if r.Args.Has("cache") {
						if d, err := time.ParseDuration(r.Args.Get("cache")); err == nil {
							staticCfg.CacheDuration = d
							if staticCfg.CacheDuration <= 0 {
								staticCfg.CacheDuration = -1
							}
						}
					}
					if r.Args.Has("maxAge") {
						if d, err := time.ParseDuration(r.Args.Get("maxAge")); err == nil {
							staticCfg.MaxAge = int(d.Seconds())
						}
					}
					args = append(args, timeoutMiddleware(static.New(dir, staticCfg)))
					router.Use(args...)
					continue
				}
				if len(handlerCode) > 0 || r.Inline {
					method = "GET"
				} else {
					continue
				}
			}
			if method == "FILE" {
				all := append(handlers, func(c fiber.Ctx) error {
					return c.SendFile(string(handlerCode))
				})
				router.Get(path, timeoutMiddleware(all[0].(fiber.Handler)), all[1:]...)
				continue
			}

			finalHandler := timeoutMiddleware(func(c fiber.Ctx) error {
				if contentType != "" {
					c.Set("Content-Type", contentType)
				}
				if r.Inline {
					switch rType {
					case HandlerTemplate:
						res, tmplErr := processor.Process(handlerCode, config.BaseDir, c, config.AppConfig, settings)
						if tmplErr != nil {
							return c.Status(500).SendString("Template Error: " + tmplErr.Error())
						}
						if contentType == "" {
							c.Set("Content-Type", "text/html")
						}
						return c.Send(res)
					case HandlerJSON:
						return c.Type("json").Send(handlerCode)
					case HandlerHex, HandlerBase64, HandlerBase32, HandlerBinary, HandlerBytes, HandlerText:
						return c.Send(handlerCode)
					default:
						return runJSHandler(c, handlerCode, config, settings)
					}
				}
				// Non-inline
				handlerCode, err = r.Content(config)
				if err != nil {
					return c.Status(500).SendString("Route Handler Error: " + err.Error())
				}
				switch rType {
				case HandlerText, HandlerHex, HandlerBase32, HandlerBase64, HandlerBytes, HandlerBinary:
					return c.Send(handlerCode)
				case HandlerJSON:
					return c.Type("json").Send(handlerCode)
				case HandlerTemplate:
					res, err := processor.Process(handlerCode, config.BaseDir, c, config.AppConfig, settings)
					if err != nil {
						return c.Status(500).SendString("Template Error: " + err.Error())
					}
					if contentType == "" {
						c.Set("Content-Type", "text/html")
					}
					return c.Send(res)
				case HandlerFile:
					return runFileHandler(c, string(handlerCode), config, settings)
				case HandlerFS:
					return runFSHandler(c, string(handlerCode), config, settings)
				default:
					return c.SendString("Unknown Handler Type")
				}
			})

			all := append(handlers, finalHandler)
			router.Add([]string{method}, path, all[0], all[1:]...)
		}
	}

	var rootRoutes []*RouteConfig
	for i := range config.Routes {
		rootRoutes = append(rootRoutes, config.Routes[i])
	}
	registerRoutes(app.App, rootRoutes)

	// ── Metrics ──────────────────────────────────────────────────────────────
	// Expose /metrics for Prometheus
	app.App.Get("/metrics", adaptor.HTTPHandler(promhttp.Handler()))

	// ── CRUD mounts ──────────────────────────────────────────────────────────
	// CRUD [name] [/mount/path]
	// Attaches a named CrudInstance to this HTTP server at the given prefix.
	var crudRoutes []*RouteConfig = config.GetRoutes("CRUD")
	for _, r := range crudRoutes {
		crudName := r.Path                        // first token  = instance name
		crudMount := strings.TrimSpace(r.Handler) // second token = mount path
		if crudMount == "" {
			crudMount = "/" + crudName
		}
		inst := crud.GetCrudInstance(crudName)
		if inst == nil {
			log.Printf("HTTP: CRUD %q not found, skipping mount on %s", crudName, crudMount)
			continue
		}
		if err := crud.MountOn(app, inst, crudMount); err != nil {
			log.Printf("HTTP: CRUD %q mount error: %v", crudName, err)
		}
	}

	// ── PAYMENT mounts ──────────────────────────────────────────────────────
	// PAYMENT [name] [/mount/path]
	// Attaches a named PaymentConnection to this HTTP server at the given prefix.
	var paymentMountRoutes []*RouteConfig = config.GetRoutes("PAYMENT")
	mountedPaymentNames := make(map[string]bool)
	for _, r := range paymentMountRoutes {
		paymentName := r.Path                        // first token = payment connection name
		paymentMount := strings.TrimSpace(r.Handler) // second token = mount path
		if paymentMount == "" {
			paymentMount = "/" + paymentName
		}
		conn := GetPaymentConnection(paymentName)
		if conn == nil {
			log.Printf("HTTP: PAYMENT %q not found, skipping mount on %s", paymentName, paymentMount)
			continue
		}
		MountPaymentRoutes(app, conn, paymentMount)
		mountedPaymentNames[paymentName] = true
	}

	// ── Auto-mount payment routes ───────────────────────────────────────────
	// If @payment[name="X"] is used in routes but no PAYMENT X /prefix exists,
	// auto-mount on /_pay/hook/{name}
	var collectPaymentMiddlewareNames func(routes []*RouteConfig, names map[string]bool)
	collectPaymentMiddlewareNames = func(routes []*RouteConfig, names map[string]bool) {
		for _, r := range routes {
			for _, mw := range r.Middlewares {
				if strings.ToUpper(mw.Name) == "PAYMENT" {
					if n := mw.Args.Get("name", ""); n != "" {
						names[n] = true
					}
				}
			}
			if r.IsGroup && len(r.Routes) > 0 {
				collectPaymentMiddlewareNames(r.Routes, names)
			}
		}
	}
	usedPaymentNames := make(map[string]bool)
	collectPaymentMiddlewareNames(config.Routes, usedPaymentNames)
	for name := range usedPaymentNames {
		if !mountedPaymentNames[name] {
			conn := GetPaymentConnection(name)
			if conn != nil {
				prefix := "/_pay/hook/" + name
				MountPaymentRoutes(app, conn, prefix)
			}
		}
	}

	directive.App = app
	directive.tlsConfig = tlsConfig
	directive.acmeManager = acmeManager
	directive.address = config.Address
	directive.domain = domain
	directive.aliases = aliases

	return directive
}

// ─────────────────────────────────────────────────────────────────────────────
// Directive interface
// ─────────────────────────────────────────────────────────────────────────────

func (p *HTTPDirective) Name() string    { return "HTTP" }
func (p *HTTPDirective) Address() string { return p.address }
func (p *HTTPDirective) Start() ([]net.Listener, error) {
	ln, err := net.Listen("tcp", p.address)
	if err != nil {
		return nil, err
	}
	return []net.Listener{ln}, nil
}

func (p *HTTPDirective) Match(peek []byte) (bool, error) {
	// For specialized HTTP/HTTPS, if we have a domain, we MUST match it
	if p.domain == "" && len(p.aliases) == 0 {
		return p.matchBasic(peek)
	}

	host := ""
	if len(peek) > 0 && peek[0] == 0x16 {
		// TLS Handshake - extract SNI
		host = extractSNI(peek)
	} else {
		// Plain HTTP - extract Host header
		host = extractHost(peek)
	}

	if host == "" {
		// Fallback to basic match if no host found but protocol looks correct
		return p.matchBasic(peek)
	}

	// Match domain or aliases
	if strings.EqualFold(host, p.domain) {
		return true, nil
	}
	for _, a := range p.aliases {
		if strings.EqualFold(host, a) {
			return true, nil
		}
	}

	return false, nil
}

func (p *HTTPDirective) matchBasic(peek []byte) (bool, error) {
	methods := []string{"GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS", "PATCH"}
	s := string(peek)
	for _, m := range methods {
		if strings.HasPrefix(s, m+" ") {
			return true, nil
		}
	}
	if len(peek) > 0 && peek[0] == 0x16 {
		return p.tlsConfig != nil || p.acmeManager != nil, nil
	}
	return false, nil
}

func extractHost(peek []byte) string {
	s := string(peek)
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), "host:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) > 1 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func extractSNI(peek []byte) string {
	// Standard TLS ClientHello SNI extraction (simplified)
	if len(peek) < 43 {
		return ""
	}
	// Skip record header (5) + handshake header (4) + version (2) + random (32)
	pos := 43
	if len(peek) <= pos {
		return ""
	}
	sessionIDLen := int(peek[pos])
	pos += 1 + sessionIDLen
	if len(peek) <= pos+2 {
		return ""
	}
	cipherSuiteLen := int(peek[pos])<<8 | int(peek[pos+1])
	pos += 2 + cipherSuiteLen
	if len(peek) <= pos {
		return ""
	}
	compressionLen := int(peek[pos])
	pos += 1 + compressionLen
	if len(peek) <= pos+2 {
		return ""
	}
	extensionsLen := int(peek[pos])<<8 | int(peek[pos+1])
	pos += 2
	end := pos + extensionsLen
	if end > len(peek) {
		end = len(peek)
	}
	for pos+4 <= end {
		extType := int(peek[pos])<<8 | int(peek[pos+1])
		extLen := int(peek[pos+2])<<8 | int(peek[pos+3])
		pos += 4
		if extType == 0 { // Server Name Extension
			if pos+2 > end {
				return ""
			}
			_ = int(peek[pos])<<8 | int(peek[pos+1]) // listLen (unused)
			pos += 2
			if pos+3 > end {
				return ""
			}
			nameType := peek[pos]
			nameLen := int(peek[pos+1])<<8 | int(peek[pos+2])
			pos += 3
			if nameType == 0 && pos+nameLen <= end {
				return string(peek[pos : pos+nameLen])
			}
		}
		pos += extLen
	}
	return ""
}

func (p *HTTPDirective) Close() error {
	if p.server != nil {
		return p.server.Shutdown()
	}
	return nil
}

func (p *HTTPDirective) Handle(conn net.Conn) error {
	if p.App != nil && p.App.Config != nil && p.App.Config.Accept != nil {
		if err := p.App.Config.Accept(conn); err != nil {
			conn.Close()
			return err
		}
	}

	if p.server == nil {
		p.server = &fasthttp.Server{Handler: p.App.Handler()}
	}
	go p.server.ServeConn(conn)
	return nil
}

func (p *HTTPDirective) HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error {
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Error handler builder
// ─────────────────────────────────────────────────────────────────────────────

func buildErrorHandler(config *DirectiveConfig, errors []*RouteConfig, settings Arguments) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		code := fiber.StatusInternalServerError
		if e, ok := err.(*fiber.Error); ok {
			code = e.Code
		}
		codeStr := strconv.Itoa(code)

		var errRoute *RouteConfig
		for _, e := range errors {
			if e.Path == codeStr {
				errRoute = e
				break
			}
		}
		if errRoute == nil {
			for _, e := range errors {
				if e.Path == "" {
					errRoute = e
					break
				}
			}
		}
		if errRoute == nil {
			return fiber.DefaultErrorHandler(c, err)
		}

		if errRoute.ContentType != "" {
			c.Type(errRoute.ContentType)
		}

		if errRoute.Inline {
			switch errRoute.Type {
			case HandlerHex:
				if b, e := hex.DecodeString(errRoute.Handler); e == nil {
					return c.Status(code).Send(b)
				}
				return c.Status(500).SendString("Invalid HEX in error handler")
			case HandlerBase64:
				if b, e := base64.StdEncoding.DecodeString(errRoute.Handler); e == nil {
					return c.Status(code).Send(b)
				}
				return c.Status(500).SendString("Invalid BASE64 in error handler")
			case HandlerBase32:
				if b, e := base32.StdEncoding.DecodeString(errRoute.Handler); e == nil {
					return c.Status(code).Send(b)
				}
				return c.Status(500).SendString("Invalid BASE32 in error handler")
			case HandlerBinary, HandlerBytes:
				return c.Status(code).Send([]byte(errRoute.Handler))
			case HandlerText:
				return c.Status(code).SendString(errRoute.Handler)
			case HandlerJSON:
				return c.Status(code).Type("json").SendString(errRoute.Handler)
			case HandlerTemplate:
				res, vmErr := processor.ProcessString(errRoute.Handler, config.BaseDir, c, config.AppConfig, settings)
				if vmErr != nil {
					return c.Status(code).SendString("Error Handler Template Error: " + vmErr.Error())
				}
				if errRoute.ContentType == "" {
					c.Set("Content-Type", "text/html")
				}
				return c.Status(code).SendString(res)
			default:
				return runErrorJSHandler(c, code, []byte(errRoute.Handler), err, config, settings)
			}
		}

		// File-based
		fullPath := errRoute.Handler
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(config.BaseDir, fullPath)
		}
		switch errRoute.Type {
		case HandlerTemplate:
			res, errCompile := processor.ProcessFile(fullPath, c, config.AppConfig)
			if errCompile != nil {
				return c.Status(500).SendString("Template Error: " + errCompile.Error())
			}
			if errRoute.ContentType == "" {
				c.Set("Content-Type", "text/html")
			}
			return c.Status(code).SendString(res)
		case HandlerFile:
			b, readErr := os.ReadFile(fullPath)
			if readErr != nil {
				return c.Status(500).SendString("File Error: " + readErr.Error())
			}
			return runErrorJSHandler(c, code, b, err, config, settings)
		default:
			return c.Status(code).SendFile(fullPath)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Worker runner
// ─────────────────────────────────────────────────────────────────────────────

func runWorker(w *RouteConfig, config *DirectiveConfig, settings Arguments) {
	var code []byte
	var err error
	dir := config.BaseDir

	if w.Inline {
		code = []byte(w.Handler)
	} else {
		fullPath := w.Handler
		if fullPath == "" {
			fullPath = w.Path
		}
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(config.BaseDir, fullPath)
		}
		dir = filepath.Dir(fullPath)
		code, err = os.ReadFile(fullPath)
		if err != nil {
			fmt.Printf("Worker Error (read): %v\n", err)
			return
		}
	}

	vm := processor.New(dir, nil, config.AppConfig)

	cfgObj := vm.NewObject()
	for k, v := range w.Args {
		cfgObj.Set(k, v)
	}
	vm.Set("config", cfgObj)

	if len(settings) > 0 {
		settingsObj := vm.NewObject()
		for k, v := range settings {
			settingsObj.Set(k, v)
		}
		vm.Set("settings", settingsObj)
	}

	if _, err = vm.RunString(string(code)); err != nil {
		fmt.Printf("Worker Error (run): %v\n", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Proxy registration
// ─────────────────────────────────────────────────────────────────────────────

func registerProxy(app *httpserver.HTTP, p *RouteConfig) {
	// Non-inline PROXY: Handler = RemoteURL, Path = route path, Args["type"] = WS/HTTP
	remoteURL := p.Handler
	routePath := p.Path
	proxyType := strings.ToUpper(p.Args.Get("type"))

	var wsTarget, httpTarget string
	if strings.HasPrefix(remoteURL, "http") {
		httpTarget = remoteURL
		wsTarget = strings.Replace(remoteURL, "http", "ws", 1)
	} else if strings.HasPrefix(remoteURL, "ws") {
		wsTarget = remoteURL
		httpTarget = strings.Replace(remoteURL, "ws", "http", 1)
	} else {
		httpTarget = "http://" + remoteURL
		wsTarget = "ws://" + remoteURL
	}

	switch proxyType {
	case "WS":
		app.Use(routePath, httpserver.WSProxy(wsTarget))
	case "HTTP":
		app.Use(routePath, proxy.Balancer(proxy.Config{
			Servers:   []string{httpTarget},
			TLSConfig: &tls.Config{InsecureSkipVerify: true},
		}))
	default:
		wsHandler := httpserver.WSProxy(wsTarget)
		httpHandler := proxy.Balancer(proxy.Config{
			Servers:   []string{httpTarget},
			TLSConfig: &tls.Config{InsecureSkipVerify: true},
		})
		app.Use(routePath, func(c fiber.Ctx) error {
			if websocket.IsWebSocketUpgrade(c) {
				return wsHandler(c)
			}
			return httpHandler(c)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Rewrite & Redirect registration
// ─────────────────────────────────────────────────────────────────────────────

func registerRewritesRedirects(app *httpserver.HTTP, rewrites, redirects []*RouteConfig, config *DirectiveConfig) {
	type rule struct {
		re        *regexp.Regexp
		sub       string
		code      int
		condition string
	}
	var rules []rule

	// REWRITE: Path=pattern, Handler=substitution, Args["condition"]=JS expr
	for _, rw := range rewrites {
		if re, err := regexp.Compile(rw.Path); err == nil {
			rules = append(rules, rule{
				re:        re,
				sub:       rw.Handler,
				code:      0,
				condition: strings.TrimSpace(rw.Args.Get("condition")),
			})
			fmt.Printf("Registered REWRITE: %s -> %s\n", rw.Path, rw.Handler)
		} else {
			fmt.Printf("REWRITE: invalid regexp %q: %v\n", rw.Path, err)
		}
	}

	// REDIRECT: Path=pattern, Handler=substitution, Args["code"]=HTTP code
	for _, rd := range redirects {
		code := rd.Args.GetInt("code", 302)
		if re, err := regexp.Compile(rd.Path); err == nil {
			rules = append(rules, rule{
				re:        re,
				sub:       rd.Handler,
				code:      code,
				condition: strings.TrimSpace(rd.Args.Get("condition")),
			})
			fmt.Printf("Registered REDIRECT: %s -> %s (%d)\n", rd.Path, rd.Handler, code)
		} else {
			fmt.Printf("REDIRECT: invalid regexp %q: %v\n", rd.Path, err)
		}
	}

	evalCondition := func(cond string, c fiber.Ctx) bool {
		if cond == "" {
			return true
		}
		vm := processor.New(config.BaseDir, c, config.AppConfig)
		vm.Register("ctx", c)
		v, err := vm.RunString(fmt.Sprintf("(function(){ with(ctx){ return Boolean(%s); } })()", cond))
		if err != nil {
			fmt.Printf("REWRITE/REDIRECT condition error [%q]: %v\n", cond, err)
			return true
		}
		if b, ok := v.Export().(bool); ok {
			return b
		}
		return v.ToBoolean()
	}

	app.Use(func(c fiber.Ctx) error {
		p := c.Path()
		for _, r := range rules {
			if r.re.MatchString(p) && evalCondition(r.condition, c) {
				newPath := r.re.ReplaceAllString(p, r.sub)
				if r.code == 0 {
					c.Path(newPath)
					p = newPath
				} else {
					return c.Status(r.code).Redirect().To(newPath)
				}
			}
		}
		return c.Next()
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Global middleware registration
// ─────────────────────────────────────────────────────────────────────────────

func registerGlobalMiddleware(app *httpserver.HTTP, m *RouteConfig, config *DirectiveConfig) {
	// Path stored in route Path, method/priority in Args
	routePath := m.Path
	isInline := m.Inline

	handlerFn := func(c fiber.Ctx) error {
		if isInline {
			if _, err := processor.New(config.BaseDir, c, config.AppConfig).RunString(m.Handler); err != nil {
				return c.Status(500).SendString("Middleware Error: " + err.Error())
			}
			return c.Next()
		}
		if _, err := processor.ProcessFile(m.Handler, c, config.AppConfig); err != nil {
			return c.Status(500).SendString("Middleware Error: " + err.Error())
		}
		return c.Next()
	}

	methodFilter := strings.ToUpper(m.Args.Get("method"))
	if methodFilter == "PRE" || methodFilter == "AFTER" || methodFilter == "" {
		if routePath != "" {
			app.Use(routePath, handlerFn)
		} else {
			app.Use(handlerFn)
		}
	} else {
		app.Add([]string{methodFilter}, routePath, handlerFn)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// JS handler helpers
// ─────────────────────────────────────────────────────────────────────────────

func injectSettings(vm *goja.Runtime, settings Arguments) {
	if len(settings) == 0 {
		return
	}
	obj := vm.NewObject()
	for k, v := range settings {
		obj.Set(k, v)
	}
	vm.Set("settings", obj)
}

func runJSHandler(c fiber.Ctx, code []byte, config *DirectiveConfig, settings Arguments) error {
	vm := processor.New(config.BaseDir, c, config.AppConfig)
	injectSettings(vm.Runtime, settings)

	done := make(chan goja.Value, 1)
	errChan := make(chan error, 1)
	vm.Set("__resolve", func(call goja.FunctionCall) goja.Value {
		done <- call.Argument(0)
		return goja.Undefined()
	})
	vm.Set("__reject", func(call goja.FunctionCall) goja.Value {
		errChan <- fmt.Errorf("%v", call.Argument(0))
		return goja.Undefined()
	})

	script := fmt.Sprintf("(async () => { %s })().then(__resolve).catch(__reject)", code)
	if _, err := vm.RunString(script); err != nil {
		return fiberErrorFromJS(c, err, 500)
	}

	var res goja.Value
	select {
	case res = <-done:
	case err := <-errChan:
		return c.Status(500).SendString("JS Async Error: " + err.Error())
	case <-time.After(30 * time.Second):
		return c.Status(504).SendString("JS Timeout")
	}

	return sendJSResult(c, vm.Runtime, res, "")
}

func runErrorJSHandler(c fiber.Ctx, code int, handlerCode []byte, origErr error, config *DirectiveConfig, settings Arguments) error {
	vm := processor.New(config.BaseDir, c, config.AppConfig)
	vm.Set("error", origErr.Error())
	injectSettings(vm.Runtime, settings)

	res, vmErr := vm.RunString(string(handlerCode))
	if vmErr != nil {
		return fiberErrorFromJS(c, vmErr, code)
	}
	val := res.Export()
	if val == nil {
		if len(c.Response().Body()) > 0 {
			return nil
		}
		if out := jsOutput(vm.Runtime); out != "" {
			return c.Status(code).SendString(out)
		}
		return nil
	}
	return c.Status(code).SendString(fmt.Sprint(val))
}

func runFileHandler(c fiber.Ctx, fullPath string, config *DirectiveConfig, settings Arguments) error {
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(config.BaseDir, fullPath)
	}
	// Security: prevent path traversal
	rel, err := filepath.Rel(config.BaseDir, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return c.Status(fiber.StatusForbidden).SendString("Forbidden: Path traversal detected")
	}
	if strings.HasSuffix(fullPath, ".js") {
		b, err := os.ReadFile(fullPath)
		if err != nil {
			return c.Status(500).SendString("File Error: " + err.Error())
		}
		vm := processor.New(filepath.Dir(fullPath), c, config.AppConfig)
		injectSettings(vm.Runtime, settings)
		res, err := vm.RunString(string(b))
		if err != nil {
			return fiberErrorFromJS(c, err, 500)
		}
		return sendJSResult(c, vm.Runtime, res, "")
	}
	if strings.HasSuffix(fullPath, ".html") || strings.HasSuffix(fullPath, ".htm") {
		b, err := os.ReadFile(fullPath)
		if err != nil {
			return c.Status(500).SendString("File Error: " + err.Error())
		}
		res, err := processor.Process(b, config.BaseDir, c, config.AppConfig, settings)
		if err != nil {
			return c.Status(500).SendString("Processing Error: " + err.Error())
		}
		c.Set("Content-Type", "text/html")
		return c.Send(res)
	}
	return c.SendFile(fullPath)
}

func runFSHandler(c fiber.Ctx, fullPath string, config *DirectiveConfig, settings Arguments) error {
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(config.BaseDir, fullPath)
	}
	// Security: prevent path traversal
	rel, err := filepath.Rel(config.BaseDir, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return c.Status(fiber.StatusForbidden).SendString("Forbidden: Path traversal detected")
	}
	if strings.HasSuffix(fullPath, ".js") {
		b, err := os.ReadFile(fullPath)
		if err != nil {
			return c.Status(500).SendString("File Error: " + err.Error())
		}
		vm := processor.New(filepath.Dir(fullPath), c, config.AppConfig)
		injectSettings(vm.Runtime, settings)
		res, err := vm.RunString(string(b))
		if err != nil {
			return fiberErrorFromJS(c, err, 500)
		}
		return sendJSResult(c, vm.Runtime, res, "")
	}
	if strings.HasSuffix(fullPath, config.AppConfig.TemplateExt) {
		b, err := os.ReadFile(fullPath)
		if err != nil {
			return c.Status(500).SendString("File Error: " + err.Error())
		}
		res, err := processor.Process(b, config.BaseDir, c, config.AppConfig, settings)
		if err != nil {
			return c.Status(500).SendString("Processing Error: " + err.Error())
		}
		c.Set("Content-Type", "text/html")
		return c.Send(res)
	}
	return c.SendFile(fullPath)
}

func sendJSResult(c fiber.Ctx, vm *goja.Runtime, res goja.Value, contentType string) error {
	if goja.IsUndefined(res) || goja.IsNull(res) {
		if len(c.Response().Body()) > 0 {
			return nil
		}
		if out := jsOutput(vm); out != "" {
			if contentType == "" {
				c.Set("Content-Type", "text/html")
			}
			return c.SendString(out)
		}
		return nil
	}
	if goja.IsString(res) {
		if contentType == "" {
			c.Set("Content-Type", "text/html")
		}
		return c.SendString(res.String())
	}
	if jsonData, err := js.ToJSON(vm, res); err == nil {
		if contentType == "" {
			c.Set("Content-Type", "application/json")
		}
		return c.SendString(jsonData)
	}
	if contentType == "" {
		c.Set("Content-Type", "text/html")
	}
	return c.SendString(fmt.Sprintf("%v", res.Export()))
}

func jsOutput(vm *goja.Runtime) string {
	outVal := vm.Get("__output")
	if outVal == nil {
		return ""
	}
	if fn, ok := goja.AssertFunction(outVal); ok {
		res, _ := fn(goja.Undefined())
		return res.String()
	}
	return ""
}

func fiberErrorFromJS(c fiber.Ctx, err error, defaultCode int) error {
	msg := err.Error()
	if strings.Contains(msg, "__FIBER_ERROR__") {
		parts := strings.Split(msg, "__FIBER_ERROR__")
		if len(parts) == 2 {
			codeStr := strings.Fields(parts[1])[0]
			if code, parseErr := strconv.Atoi(codeStr); parseErr == nil {
				return fiber.NewError(code, parts[0])
			}
		}
	}
	return c.Status(defaultCode).SendString("JS Error: " + msg)
}

// ─────────────────────────────────────────────────────────────────────────────
// Admin auth helper
// ─────────────────────────────────────────────────────────────────────────────

func handleAdminAuth(c fiber.Ctx, auth AuthConfigs, redirect, message, appName string, basic bool) error {
	authHeader := c.Get("Authorization")
	if basic {
		if authHeader == "" {
			c.Set("WWW-Authenticate", `Basic realm="Restricted"`)
			return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized")
		}
		if !strings.HasPrefix(authHeader, "Basic ") {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid Authentication Type")
		}
		payload, err := base64.StdEncoding.DecodeString(authHeader[6:])
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid Authentication Payload")
		}
		pair := strings.SplitN(string(payload), ":", 2)
		if len(pair) != 2 {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid Authentication Format")
		}
		if err := auth.Auth(pair[0], pair[1]); err != nil {
			c.Set("WWW-Authenticate", `Basic realm="`+err.Error()+`"`)
			return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized")
		}
		return c.Next()
	}
	if authHeader == "" {
		if redirect != "" {
			return c.Redirect().To(redirect)
		}
		httpserver.RecordSecurityBlock(appName, "admin_auth")
		return c.Status(403).SendString(message)
	}
	username, password := "", ""
	payload, err := base64.StdEncoding.DecodeString(authHeader[6:])
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid Authentication Payload - Silent Fallback Refused")
	}
	if pair := strings.SplitN(string(payload), ":", 2); len(pair) == 2 {
		username, password = pair[0], pair[1]
	} else {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid Authentication Format")
	}
	if err := auth.Auth(username, password, authHeader); err != nil {
		if redirect != "" {
			return c.Redirect().To(redirect)
		}
		return c.Status(403).SendString(err.Error())
	}
	return c.Next()
}

// split splits a comma-separated string into a trimmed slice.
func split(s string, separator ...string) []string {
	sep := ","
	if len(separator) > 0 {
		sep = separator[0]
	}
	var result []string
	for _, part := range strings.Split(s, sep) {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
