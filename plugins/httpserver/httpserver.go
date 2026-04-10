// HTTP wrapper étendant zerolog.Logger avec stdout/stderr séparés
package httpserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/encryptcookie"
	"github.com/gofiber/fiber/v3/middleware/session"
	"github.com/oschwald/geoip2-golang"
	"github.com/rs/zerolog"
)

// ─────────────────────────────────────────────────────────────────────────────
// Global route registry
//
// RegisterRoute queues an HTTP route to be mounted on every HTTP server just
// before it starts accepting connections (via BeforeServeFunc in Listen).
// External directives (PAYMENT, MAIL, …) call this during their Start() so
// their webhook endpoints are available from the first request.
// ─────────────────────────────────────────────────────────────────────────────

// registeredRoute holds one queued route entry.
type registeredRoute struct {
	Method   string
	Path     string
	Handlers []any
}

type AppHandler func(app *HTTP) error

var (
	registeredRoutesMu sync.Mutex
	registeredRoutes   []registeredRoute
	appHandlers        []AppHandler
)

// RegisterRoute queues a route to be mounted on every HTTP server that is
// created (via Listen) after this call.
//
// method is an HTTP verb ("GET", "POST", …) or "*" for all methods.
// path is a fiber-compatible route path (e.g. "/payment/stripe/webhook").
// handlers are fiber.Handler functions executed in order.
//
// Example:
//
//	httpserver.RegisterRoute("POST", "/payment/stripe/webhook", stripeWebhookHandler)
func RegisterRoute(method, path string, handlers ...fiber.Handler) {
	h := make([]any, len(handlers))
	for i, handler := range handlers {
		h[i] = handler
	}
	registeredRoutesMu.Lock()
	registeredRoutes = append(registeredRoutes, registeredRoute{method, path, h})
	registeredRoutesMu.Unlock()
}

func RegisterOnApp(appHandler AppHandler) {
	registeredRoutesMu.Lock()
	appHandlers = append(appHandlers, appHandler)
	registeredRoutesMu.Unlock()
}

type HTTP struct {
	*fiber.App
	*Config
	GeoDB *geoip2.Reader
}

// Config holds optional overrides for the app factory.

// New crée une instance HTTP Fiber avec Zerolog séparé stdout/stderr et recover middleware
func New(cfg Config) *HTTP {
	cfg = configDefault(cfg)

	name := cfg.AppName
	if name == "" {
		name = "http-server"
		cfg.AppName = name
	}

	stdout := cfg.Stdout
	stderr := cfg.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	// Création des loggers séparés
	cfg.stdoutLogger = zerolog.New(zerolog.ConsoleWriter{Out: stdout, TimeFormat: "15:04:05"}).With().Str("APP", name).Timestamp().Logger().Level(cfg.Level)
	cfg.stderrLogger = zerolog.New(zerolog.ConsoleWriter{Out: stderr, TimeFormat: "15:04:05"}).With().Str("APP", name).Timestamp().Logger().Level(cfg.Level)
	// creation de sa sessionstore

	// cfg.SessionStore = session.NewStore(session.Config{
	// 	// Security
	// 	CookieSecure:   true,  // HTTPS only (required in production)
	// 	CookieHTTPOnly: true,  // No JavaScript access (prevents XSS)
	// 	CookieSameSite: "Lax", // CSRF protection

	// 	// Session Management
	// 	IdleTimeout:     30 * time.Minute, // Inactivity timeout
	// 	AbsoluteTimeout: 24 * time.Hour,   // Maximum session duration

	// 	// Cookie Settings
	// 	CookiePath: "/",
	// 	// CookieDomain:      "example.com",
	// 	CookieSessionOnly: false, // Persist across browser restarts

	// 	// Session ID
	// 	Extractor:    extractors.FromCookie("__Host-sid_csrf"),
	// 	KeyGenerator: utils.SecureToken,

	// 	// Error Handling
	// 	ErrorHandler: func(c fiber.Ctx, err error) {
	// 		log.Printf("Session error: %v", err)
	// 	},
	// })
	app := &HTTP{
		App: fiber.New(fiber.Config{
			AppName:      name,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
			BodyLimit:    cfg.BodyLimit,
			ErrorHandler: func(c fiber.Ctx, err error) error {
				if cfg.ErrorHandler != nil {
					return cfg.ErrorHandler(c, err)
				}
				code := fiber.StatusInternalServerError
				if e, ok := err.(*fiber.Error); ok {
					code = e.Code
				}
				if code == fiber.StatusNotFound {
					cfg.stdoutLogger.Warn().Str("path", c.Path()).Msg("route not found")
					return c.Status(fiber.StatusNotFound).SendString("Not Found")
				}
				cfg.stderrLogger.Error().Err(err).Str("path", c.Path()).Int("status", code).Msg("request error")
				return c.Status(code).SendString("Internal Server Error")
			},
		}),
		Config: &cfg,
	}

	// Recover middleware
	app.Use(func(c fiber.Ctx) (err error) {
		defer func() {
			if r := recover(); r != nil {
				var panicErr error
				switch x := r.(type) {
				case error:
					panicErr = x
				default:
					panicErr = fmt.Errorf("panic occurred: %v", x)
				}

				if err != nil {
					err = errors.Join(err, panicErr)
				} else {
					err = panicErr
				}

				app.stderrLogger.Error().Err(err).Msg("Recovered from panic")
				c.Status(fiber.StatusInternalServerError).SendString("Internal Server Error")
			}
		}()

		err = c.Next()
		return
	})
	// Cookies
	secret := cfg.Secret
	if secret == "" {
		// Generate a cryptographically secure random secret if none provided
		b := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, b); err == nil {
			secret = base64.StdEncoding.EncodeToString(b)
		} else {
			// Fallback if rand fails (should not happen)
			h := sha256.Sum256([]byte(cfg.AppName))
			secret = base64.StdEncoding.EncodeToString(h[:])
		}
	} else {
		// Hash the user-provided secret to ensure it's exactly 32 bytes when decoded
		h := sha256.Sum256([]byte(secret))
		secret = base64.StdEncoding.EncodeToString(h[:])
	}
	cfg.Secret = secret
	app.Use(encryptcookie.New(encryptcookie.Config{
		Key: secret,
	}))

	// session
	app.Use(session.New(session.Config{
		Storage:        cfg.SessionStore,
		CookieSecure:   true,
		CookieHTTPOnly: true,
		CookieSameSite: "Lax",
	}))

	// Middleware Zerolog Fiber
	app.Use(func(c fiber.Ctx) error {
		// Don't execute middleware if Next returns true
		if cfg.Next != nil && cfg.Next(c) {
			return c.Next()
		}

		start := time.Now()

		// Handle request, store err for logging
		chainErr := c.Next()
		if chainErr != nil {
			// Manually call error handler
			if err := c.App().ErrorHandler(c, chainErr); err != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}

		latency := time.Since(start)

		status := c.Response().StatusCode()

		index := 0
		switch {
		case status >= 500:
			// error index is zero
		case status >= 400:
			index = 1
		default:
			index = 2
		}

		levelIndex := index
		if levelIndex >= len(cfg.Levels) {
			levelIndex = len(cfg.Levels) - 1
		}
		level := cfg.Levels[levelIndex]

		// no log
		if level == zerolog.NoLevel || level == zerolog.Disabled {
			return nil
		}

		messageIndex := index
		if messageIndex >= len(cfg.Messages) {
			messageIndex = len(cfg.Messages) - 1
		}
		message := cfg.Messages[messageIndex]

		logger := cfg.logger(level, c, latency, chainErr)
		ctx := c

		switch level {
		case zerolog.TraceLevel:
			logger.Trace().Ctx(ctx).Msg(message)
		case zerolog.DebugLevel:
			logger.Debug().Ctx(ctx).Msg(message)
		case zerolog.InfoLevel:
			logger.Info().Ctx(ctx).Msg(message)
		case zerolog.WarnLevel:
			logger.Warn().Ctx(ctx).Msg(message)
		case zerolog.ErrorLevel:
			logger.Error().Ctx(ctx).Msg(message)
		case zerolog.FatalLevel:
			logger.Fatal().Ctx(ctx).Msg(message)
		case zerolog.PanicLevel:
			logger.Panic().Ctx(ctx).Msg(message)
		}

		return nil
	})

	// Initialize Security Logger (Audit Trail)
	if cfg.WAF != nil && cfg.WAF.Audit != nil {
		InitSecurityLogger(cfg.WAF.Audit, cfg.Secret)
	}

	// WAF middleware and Connection Filters
	if cfg.WAF != nil {
		if cfg.WAF.Enabled {
			// Bot Detection (Layer 4)
			if cfg.WAF.Bot != nil && cfg.WAF.Bot.Enabled {
				app.Use(BotMiddleware(cfg.WAF.Bot))

				// Register Challenge Route
				challengePath := cfg.WAF.Bot.ChallengePath
				if challengePath == "" {
					challengePath = "/_waf/challenge"
				}
				app.All(challengePath, BotChallengeHandler(cfg.WAF.Bot))
			}

			app.Use(WAFMiddleware(cfg.WAF))
		}
		if cfg.WAF.Connection != nil {
			// Provide connection-level filtering logic for physical Accept hooks
			// Note: GeoDB might be loaded below, so we'll link it by wrapping

			// Try to preload GeoDB if possible before Accept starts checking
			var geoDb *geoip2.Reader
			if cfg.Geo != nil && cfg.Geo.Enabled && cfg.Geo.DBPath != "" {
				geoDb, _ = geoip2.Open(cfg.Geo.DBPath)
			}

			cs := NewConnectionSecurity(cfg.WAF.Connection, geoDb)

			// Chain any existing Accept hook with our new ConnectionSecurity evaluator
			origAccept := cfg.Accept
			cfg.Accept = func(conn net.Conn) error {
				if err := cs.Accept(conn); err != nil {
					return err
				}
				if origAccept != nil {
					return origAccept(conn)
				}
				return nil
			}
		}
	}

	// GeoIP middleware
	if cfg.Geo != nil && cfg.Geo.Enabled && cfg.Geo.DBPath != "" {
		db, err := geoip2.Open(cfg.Geo.DBPath)
		if err != nil {
			app.stderrLogger.Error().Err(err).Msg("GeoIP Init Error")
		} else {
			app.GeoDB = db
			app.Use(GeoMiddleware(cfg.Geo, db))
		}
	}

	return app
}

// Redéfinition des méthodes Info/Debug/Error pour HTTP
func (app *HTTP) Trace() *zerolog.Event {
	return app.stdoutLogger.Trace()
}

func (app *HTTP) Info() *zerolog.Event {
	return app.stdoutLogger.Info()
}

func (app *HTTP) Debug() *zerolog.Event {
	return app.stdoutLogger.Debug()
}

func (app *HTTP) Warn() *zerolog.Event {
	return app.stderrLogger.Warn()
}

func (app *HTTP) Error() *zerolog.Event {
	return app.stderrLogger.Error()
}

func (app *HTTP) Fatal() *zerolog.Event {
	return app.stderrLogger.Fatal()
}

func (app *HTTP) Panic() *zerolog.Event {
	return app.stderrLogger.Panic()
}

func (app *HTTP) Listen(addr string, config ...fiber.ListenConfig) error {
	c := fiber.ListenConfig{DisableStartupMessage: true}
	if len(config) > 0 {
		c = config[0]
	}
	origBefore := c.BeforeServeFunc
	c.BeforeServeFunc = func(a *fiber.App) error {
		// Mount all queued routes before accepting the first connection.
		registeredRoutesMu.Lock()
		pending := make([]registeredRoute, len(registeredRoutes))
		pendingHandlers := make([]AppHandler, len(appHandlers))
		copy(pending, registeredRoutes)
		copy(pendingHandlers, appHandlers)
		registeredRoutes = registeredRoutes[:0]
		appHandlers = appHandlers[:0]
		registeredRoutesMu.Unlock()
		for _, handler := range pendingHandlers {
			if err := handler(app); err != nil {
				return err
			}
		}
		for _, route := range pending {
			if route.Method == "*" {
				a.All(route.Path, route.Handlers[0], route.Handlers[1:]...)
				continue
			}
			a.Add([]string{route.Method}, route.Path, route.Handlers[0], route.Handlers[1:]...)
		}
		app.Info().Str("addr", addr).Msg("Server started")
		if origBefore != nil {
			return origBefore(a)
		}
		return nil
	}
	return app.App.Listen(addr, c)
}
