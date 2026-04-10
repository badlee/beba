package httpserver

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

const (
	FieldReferer       = "referer"
	FieldProtocol      = "protocol"
	FieldPID           = "pid"
	FieldPort          = "port"
	FieldIP            = "ip"
	FieldIPs           = "ips"
	FieldHost          = "host"
	FieldPath          = "path"
	FieldURL           = "url"
	FieldUserAgent     = "ua"
	FieldLatency       = "latency"
	FieldStatus        = "status"
	FieldResBody       = "resBody"
	FieldQueryParams   = "queryParams"
	FieldBody          = "body"
	FieldBytesReceived = "bytesReceived"
	FieldBytesSent     = "bytesSent"
	FieldRoute         = "route"
	FieldMethod        = "method"
	FieldRequestID     = "requestId"
	FieldError         = "error"
	FieldReqHeaders    = "reqHeaders"
	FieldResHeaders    = "resHeaders"

	fieldResBody_       = "res_body"
	fieldQueryParams_   = "query_params"
	fieldBytesReceived_ = "bytes_received"
	fieldBytesSent_     = "bytes_sent"
	fieldRequestID_     = "request_id"
	fieldReqHeaders_    = "req_headers"
	fieldResHeaders_    = "res_headers"
)

// ConfigDefault is the default config
var ConfigDefault = Config{
	Next:     nil,
	Stdout:   os.Stdout,
	Stderr:   os.Stderr,
	Fields:   []string{FieldIP, FieldLatency, FieldStatus, FieldMethod, FieldURL, FieldError},
	Messages: []string{"Server error", "Client error", "Success"},
	Levels:   []zerolog.Level{zerolog.ErrorLevel, zerolog.WarnLevel, zerolog.InfoLevel},
}

// Helper function to set default values
func configDefault(config ...Config) Config {
	// Return default config if nothing provided
	if len(config) < 1 {
		return ConfigDefault
	}

	// Override default config
	cfg := config[0]

	// Set default values
	if cfg.Next == nil {
		cfg.Next = ConfigDefault.Next
	}

	if cfg.Stdout == nil {
		cfg.Stdout = ConfigDefault.Stdout
	}

	if cfg.Stderr == nil {
		cfg.Stderr = ConfigDefault.Stderr
	}

	if cfg.Fields == nil {
		cfg.Fields = ConfigDefault.Fields
	}

	if cfg.Messages == nil {
		cfg.Messages = ConfigDefault.Messages
	}

	if cfg.Levels == nil {
		cfg.Levels = ConfigDefault.Levels
	}

	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 30 * time.Second
	}

	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 120 * time.Second
	}

	if cfg.BodyLimit == 0 {
		cfg.BodyLimit = 4 * 1024 * 1024
	}

	return cfg
}
