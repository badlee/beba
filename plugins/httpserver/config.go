package httpserver

import (
	"io"
	"net"
	"os"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
)

// Config defines the config for middleware.
type Config struct {
	// Accept allows intercepting physical connections before the HTTP protocol reads them.
	// Used for Network layer security filtering natively.
	Accept func(conn net.Conn) error

	// Application name
	//
	// Optional. Default: "http-server"
	AppName string
	// SessionStore defines the storage for session middleware.
	//
	// Optional. Default: nil (MemoryStorage)
	SessionStore fiber.Storage
	// Stdout writer
	//
	// Optional. Default: os.Stdout
	Stdout       io.Writer
	stdoutLogger zerolog.Logger
	// Stderr writer
	//
	// Optional. Default: os.Stderr
	Stderr       io.Writer
	stderrLogger zerolog.Logger

	// Next defines a function to skip this middleware when returned true.
	//
	// Optional. Default: nil
	Next func(c fiber.Ctx) bool

	// SkipField defines a function that returns true if a specific field should be skipped from logging.
	//
	// Optional. Default: nil
	SkipField func(field string, c fiber.Ctx) bool

	// GetResBody defines a function to get a custom response body.
	// e.g. when using compress middleware, the original response body may be unreadable.
	// You can use GetResBody to provide a readable body.
	//
	// Optional. Default: nil
	GetResBody func(c fiber.Ctx) []byte

	// GetLogger defines a function to get a custom zerolog logger.
	// e.g. when creating a new logger for each request.
	//
	// GetLogger will override Logger.
	//
	// Optional. Default: nil
	GetLogger func(c fiber.Ctx, level zerolog.Level) zerolog.Logger

	// Add the fields you want to log.
	//
	// Optional. Default: {"ip", "latency", "status", "method", "url", "error"}
	Fields []string

	// Defines a function that returns true if a header should not be logged.
	// Only relevant if `FieldReqHeaders` and/or `FieldResHeaders` are logged.
	//
	// Optional. Default: nil
	SkipHeader func(header string, c fiber.Ctx) bool

	// Wrap headers into a dictionary.
	// If false: {"method":"POST", "header-key":"header value"}
	// If true: {"method":"POST", "reqHeaders": {"header-key":"header value"}}
	//
	// Optional. Default: false
	WrapHeaders bool

	// Use snake case for fields: FieldResBody, FieldQueryParams, FieldBytesReceived, FieldBytesSent, FieldRequestID, FieldReqHeaders, FieldResHeaders.
	// If false: {"method":"POST", "resBody":"v", "queryParams":"v"}
	// If true: {"method":"POST", "res_body":"v", "query_params":"v"}
	//
	// Optional. Default: false
	FieldsSnakeCase bool

	// Custom response messages.
	// Response codes >= 500 will be logged with Messages[0].
	// Response codes >= 400 will be logged with Messages[1].
	// Other response codes will be logged with Messages[2].
	// You can specify fewer than 3 messages, but you must specify at least 1.
	// Specifying more than 3 messages is useless.
	//
	// Optional. Default: {"Server error", "Client error", "Success"}
	Messages []string

	// Custom response levels.
	// Response codes >= 500 will be logged with Levels[0].
	// Response codes >= 400 will be logged with Levels[1].
	// Other response codes will be logged with Levels[2].
	// You can specify fewer than 3 levels, but you must specify at least 1.
	// Specifying more than 3 levels is useless.
	//
	// Optional. Default: {zerolog.ErrorLevel, zerolog.WarnLevel, zerolog.InfoLevel}
	Levels []zerolog.Level

	// Custom response level.
	//
	// Optional. Default: zerolog.DebugLevel
	Level zerolog.Level

	// Read timeout.
	//
	// Optional. Default: 30s
	ReadTimeout time.Duration

	// Write timeout.
	//
	// Optional. Default: 0s (unlimited)
	WriteTimeout time.Duration

	// Idle timeout.
	//
	// Optional. Default: 120s
	IdleTimeout time.Duration

	// Body limit.
	//
	// Optional. Default: 4 * 1024 * 1024 (4MB)
	BodyLimit int

	// ErrorHandler defines a function that will be called when an error occurs.
	//
	// Optional. Default: nil
	ErrorHandler fiber.ErrorHandler

	// // Session store.
	// //
	// // Optional. Default: nil
	// SessionStore *session.Store

	// Secret config.
	//
	// Optional. Default: cryptographically secure random string or hash of AppName
	Secret string

	// WAF configuration.
	WAF *WAFConfig

	// GeoIP configuration.
	Geo *GeoConfig // Optional GeoIP DB from server
}

type ConnectionConfig struct {
	RateLimit *RateLimitConfig
	AllowList []string // CIDRs, IPs, ISO codes, Plus codes, text filepaths, polygon names
	DenyList  []string // CIDRs, IPs, ISO codes, Plus codes, text filepaths, polygon names
	IPHooks   []WAFHook
	GEOHooks  []WAFHook
	GeoJSON   []GeoJSONConfig
}

type GeoJSONConfig struct {
	Name     string
	DataPath string
	Inline   string
}

type RateLimitConfig struct {
	Limit  int64
	Window time.Duration
	Burst  int
	Mode   string
}

type WAFConfig struct {
	Enabled   bool
	Rules     []string
	AuditLog  string
	Severity  int // Block if severity >= X
	BlockPage string
	AppName   string // Added for metrics

	// Directives from DSL
	Engine                 string // ON, OFF, DetectionOnly
	RequestBodyAccess      bool
	RequestBodyLimit       int64
	RequestBodyInMemory    int64
	RequestBodyLimitAction string // Reject, ProcessPartial
	RequestBodyJsonDepth   int
	ArgumentsLimit         int

	Bot *BotConfig

	Connection *ConnectionConfig

	ResponseBodyAccess      bool
	ResponseBodyLimit       int64
	ResponseBodyLimitAction string // Reject, ProcessPartial
	ResponseBodyMimes       []string
	ClearResponseBodyMimes  bool

	// Audit Config
	AuditEngine            string // ON, OFF, RelevantOnly
	AuditLogPath           string
	AuditLogDirMode        int
	AuditLogFileMode       int
	AuditLogFormat         string // JSON, Native, etc.
	AuditLogParts          string // e.g. "ABCDEF"
	AuditLogRelevantStatus string // regex
	AuditLogStorageDir     string
	AuditLogType           string // Serial, Concurrent, etc.
	ComponentSignature     string

	// Debug
	DebugLogPath  string
	DebugLogLevel int

	Audit *AuditConfig

	// Hooks (event name -> code/path)
	Hooks map[string]WAFHook
}

type AuditConfig struct {
	Enabled bool
	Path    string
	Signed  bool   // Enable cryptographically chained logs (HMAC)
	Level   string // "security" for blocks only, "all" for everything
}

type BotConfig struct {
	Enabled         bool
	BlockCommonBots bool
	JSChallenge     bool   // Enable JS challenge for suspicious requests
	ChallengeSecret string // HMAC secret for signing verification tokens
	ChallengePath   string // Default: "/_waf/challenge"
	ScoreThreshold  int    // Score at which to trigger a challenge
}

type WAFHook struct {
	Event   string
	Handler string // Code or filepath
	Inline  bool
	Args    map[string]string
}

type GeoConfig struct {
	Enabled        bool
	DBPath         string
	AllowCountries []string
	BlockCountries []string
}

func (c *Config) loggerCtx(fc fiber.Ctx, level zerolog.Level) zerolog.Context {
	if c.GetLogger != nil {
		return c.GetLogger(fc, level).With()
	}
	if level == zerolog.DebugLevel || level == zerolog.InfoLevel || level == zerolog.TraceLevel {
		return c.stdoutLogger.With()
	}
	return c.stderrLogger.With()
}

func (c *Config) logger(level zerolog.Level, fc fiber.Ctx, latency time.Duration, err error) zerolog.Logger {
	zc := c.loggerCtx(fc, level)

	for _, field := range c.Fields {
		if c.SkipField != nil && c.SkipField(field, fc) {
			continue
		}
		switch field {
		case FieldReferer:
			zc = zc.Str(field, fc.Get(fiber.HeaderReferer))
		case FieldProtocol:
			zc = zc.Str(field, fc.Protocol())
		case FieldPID:
			zc = zc.Int(field, os.Getpid())
		case FieldPort:
			zc = zc.Str(field, fc.Port())
		case FieldIP:
			zc = zc.Str(field, fc.IP())
		case FieldIPs:
			zc = zc.Str(field, fc.Get(fiber.HeaderXForwardedFor))
		case FieldHost:
			zc = zc.Str(field, fc.Hostname())
		case FieldPath:
			zc = zc.Str(field, fc.Path())
		case FieldURL:
			zc = zc.Str(field, fc.OriginalURL())
		case FieldUserAgent:
			zc = zc.Str(field, fc.Get(fiber.HeaderUserAgent))
		case FieldLatency:
			zc = zc.Str(field, latency.String())
		case FieldStatus:
			zc = zc.Int(field, fc.Response().StatusCode())
		case FieldBody:
			zc = zc.Str(field, string(fc.Body()))
		case FieldResBody:
			if c.FieldsSnakeCase {
				field = fieldResBody_
			}
			resBody := fc.Response().Body()
			if c.GetResBody != nil {
				if customResBody := c.GetResBody(fc); customResBody != nil {
					resBody = customResBody
				}
			}
			zc = zc.Str(field, string(resBody))
		case FieldQueryParams:
			if c.FieldsSnakeCase {
				field = fieldQueryParams_
			}
			zc = zc.Stringer(field, fc.Request().URI().QueryArgs())
		case FieldBytesReceived:
			if c.FieldsSnakeCase {
				field = fieldBytesReceived_
			}
			zc = zc.Int(field, len(fc.Request().Body()))
		case FieldBytesSent:
			if c.FieldsSnakeCase {
				field = fieldBytesSent_
			}
			zc = zc.Int(field, len(fc.Response().Body()))
		case FieldRoute:
			zc = zc.Str(field, fc.Route().Path)
		case FieldMethod:
			zc = zc.Str(field, fc.Method())
		case FieldRequestID:
			if c.FieldsSnakeCase {
				field = fieldRequestID_
			}
			zc = zc.Str(field, fc.GetRespHeader(fiber.HeaderXRequestID))
		case FieldError:
			if err != nil {
				zc = zc.Err(err)
			}
		case FieldReqHeaders:
			if c.FieldsSnakeCase {
				field = fieldReqHeaders_
			}
			if c.WrapHeaders {
				dict := zerolog.Dict()
				for header, values := range fc.GetReqHeaders() {
					if len(values) == 0 {
						continue
					}

					if c.SkipHeader != nil && c.SkipHeader(header, fc) {
						continue
					}

					if len(values) == 1 {
						dict.Str(header, values[0])
						continue
					}

					dict.Strs(header, values)
				}
				zc = zc.Dict(field, dict)
			} else {
				for header, values := range fc.GetReqHeaders() {
					if len(values) == 0 {
						continue
					}

					if c.SkipHeader != nil && c.SkipHeader(header, fc) {
						continue
					}

					if len(values) == 1 {
						zc = zc.Str(header, values[0])
						continue
					}

					zc = zc.Strs(header, values)
				}
			}
		case FieldResHeaders:
			if c.FieldsSnakeCase {
				field = fieldResHeaders_
			}
			if c.WrapHeaders {
				dict := zerolog.Dict()
				for header, values := range fc.GetRespHeaders() {
					if len(values) == 0 {
						continue
					}

					if c.SkipHeader != nil && c.SkipHeader(header, fc) {
						continue
					}

					if len(values) == 1 {
						dict.Str(header, values[0])
						continue
					}

					dict.Strs(header, values)
				}
				zc = zc.Dict(field, dict)
			} else {
				for header, values := range fc.GetRespHeaders() {
					if len(values) == 0 {
						continue
					}

					if c.SkipHeader != nil && c.SkipHeader(header, fc) {
						continue
					}

					if len(values) == 1 {
						zc = zc.Str(header, values[0])
						continue
					}

					zc = zc.Strs(header, values)
				}
			}
		}
	}

	return zc.Logger()
}
