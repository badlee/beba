package binder

import (
	"fmt"
	"http-server/plugins/httpserver"
	"net"
	"strconv"
	"strings"
	"time"

	"http-server/modules/security"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var caser = cases.Title(language.English)

type SecurityDirective struct {
	NameStr string
	Addr    string
	Config  *httpserver.WAFConfig
}

func NewSecurityDirective(cfg *DirectiveConfig) (*SecurityDirective, error) {
	wafCfg := &httpserver.WAFConfig{
		Enabled: true,
		Hooks:   make(map[string]httpserver.WAFHook),
	}

	// name is in cfg.Address (due to how parser handles PROTO ADDRESS)
	name := cfg.Address
	if strings.EqualFold(name, "") {
		return nil, fmt.Errorf("SECURITY directive must have a name")
	}

	for _, r := range cfg.Routes {
		cmd := strings.ToUpper(r.Method)
		switch cmd {
		case "GEOIP_DB":
			// GEOIP_DB "path/to/GeoLite2-City.mmdb"
			dbPath := strings.Trim(r.Path, "\"'`")
			if err := security.InitGeoIP(dbPath); err != nil {
				// Non-fatal: log and continue
				return nil, fmt.Errorf("[security] GEOIP_DB init error: %v\n", err)
			}
		case "OWASP":
			wafCfg.Rules = append(wafCfg.Rules, fmt.Sprintf("Include %s", r.Path))
		case "ACTION":
			// ACTION [default]? action_name
			action := r.Path
			isDefault := false
			if r.Args.Has("default") {
				isDefault = true
			}
			// If it's a group, maybe the arg is in the path
			if action == "default" && len(r.Handler) > 0 {
				isDefault = true
				action = r.Handler
			}

			if isDefault {
				wafCfg.Rules = append(wafCfg.Rules, fmt.Sprintf("SecDefaultAction \"%s\"", action))
			} else {
				wafCfg.Rules = append(wafCfg.Rules, fmt.Sprintf("SecAction \"%s\"", action))
			}
		case "ENGINE":
			wafCfg.Engine = r.Path
		case "DEBUG":
			// DEBUG [level]? [filepath]
			if r.Path != "" && IsInt(r.Path) {
				wafCfg.DebugLogLevel, _ = strconv.Atoi(r.Path)
				wafCfg.DebugLogPath = r.Handler
			} else {
				wafCfg.DebugLogPath = r.Path
			}
		case "ON":
			event := strings.ToUpper(r.Path)
			hook := httpserver.WAFHook{
				Event:   event,
				Handler: r.Handler,
				Inline:  r.Inline,
				Args:    r.Args,
			}
			wafCfg.Hooks[event] = hook
		case "REQUEST":
			if r.IsGroup {
				for _, child := range r.Routes {
					switch strings.ToUpper(child.Method) {
					case "ACCESS":
						wafCfg.RequestBodyAccess = isTrue(child.Path)
					case "MEMORY":
						wafCfg.RequestBodyInMemory = parseSize(child.Path)
					case "JSONDEPTH":
						wafCfg.RequestBodyJsonDepth, _ = strconv.Atoi(child.Path)
					case "ACTION":
						wafCfg.RequestBodyLimitAction = child.Path
					case "LIMIT":
						wafCfg.RequestBodyLimit = parseSize(child.Path)
					case "ARGUMENTS":
						wafCfg.ArgumentsLimit, _ = strconv.Atoi(child.Path)
					}
				}
			}
		case "RESPONSE":
			if r.IsGroup {
				for _, child := range r.Routes {
					switch strings.ToUpper(child.Method) {
					case "ACCESS":
						wafCfg.ResponseBodyAccess = isTrue(child.Path)
					case "LIMIT":
						wafCfg.ResponseBodyLimit = parseSize(child.Path)
					case "ACTION":
						wafCfg.ResponseBodyLimitAction = child.Path
					case "MIME":
						wafCfg.ResponseBodyMimes = append(wafCfg.ResponseBodyMimes, child.Path)
					case "CLEAR_MIMES":
						wafCfg.ClearResponseBodyMimes = true
					}
				}
			}
		case "AUDIT":
			if r.IsGroup {
				for _, child := range r.Routes {
					switch strings.ToUpper(child.Method) {
					case "ENGINE":
						wafCfg.AuditEngine = child.Path
					case "FILE":
						wafCfg.AuditLogPath = child.Path
					case "MODE":
						modeType := strings.ToUpper(child.Path)
						modeVal := child.Handler
						if modeVal == "default" || modeVal == "" {
							modeVal = "0644"
						}
						m, _ := strconv.ParseInt(modeVal, 8, 32)
						if modeType == "DIR" {
							wafCfg.AuditLogDirMode = int(m)
						} else {
							wafCfg.AuditLogFileMode = int(m)
						}
					case "FORMAT":
						wafCfg.AuditLogFormat = child.Path
					case "PARTS":
						wafCfg.AuditLogParts = child.Path
					case "REVELANT":
						wafCfg.AuditLogRelevantStatus = child.Path
					case "DIR":
						wafCfg.AuditLogStorageDir = child.Path
					case "TYPE":
						wafCfg.AuditLogType = child.Path
					case "SIGNATURE":
						wafCfg.ComponentSignature = child.Path
					}
				}
			}
		case "MARKER":
			id := r.Path
			wafCfg.Rules = append(wafCfg.Rules, fmt.Sprintf("SecMarker %s", id))
			if r.IsGroup {
				for _, child := range r.Routes {
					if strings.ToUpper(child.Method) == "RULE" {
						wafCfg.Rules = append(wafCfg.Rules, fmt.Sprintf("SecRule %s %s", child.Path, child.Handler))
					}
				}
			}
		case "RULES":
			if r.IsGroup {
				for _, child := range r.Routes {
					sub := strings.ToUpper(child.Method)
					switch sub {
					case "RULE":
						wafCfg.Rules = append(wafCfg.Rules, fmt.Sprintf("SecRule %s %s", child.Path, child.Handler))
					case "ENGINE":
						wafCfg.Rules = append(wafCfg.Rules, fmt.Sprintf("SecRuleEngine %s", child.Path))
					case "REMOVE":
						// REMOVE [ID|MESSAGE|TAG] [Value]
						kind := caser.String(strings.ToLower(child.Path))
						if kind == "" {
							kind = "Id"
						}
						wafCfg.Rules = append(wafCfg.Rules, fmt.Sprintf("SecRuleRemoveBy%s %s", kind, child.Handler))
					case "UPDATE":
						// UPDATE [ACTION|TARGET|TAG] [Value]
						kind := caser.String(strings.ToLower(child.Path))
						if kind == "" {
							kind = "Action"
						}
						wafCfg.Rules = append(wafCfg.Rules, fmt.Sprintf("SecRuleUpdate%sBy%s %s", kind, "ID", child.Handler))
					}
				}
			}
		case "CONNECTION":
			if wafCfg.Connection == nil {
				wafCfg.Connection = &httpserver.ConnectionConfig{}
			}
			sub := strings.ToUpper(r.Path)
			switch sub {
			case "RATE":
				rateConf := &httpserver.RateLimitConfig{Mode: "ip"}
				// Look for options in Handler or Args
				h := r.Handler
				if r.Args.Has("limit") {
					rateConf.Limit = parseSize(r.Args.Get("limit", "0"))
				} else if h != "" {
					parts := strings.Fields(h)
					for _, p := range parts {
						if strings.Contains(p, "r/") {
							val := strings.Split(p, "r/")[0]
							rateConf.Limit = parseSize(val)
						} else if strings.HasSuffix(p, "s") || strings.HasSuffix(p, "m") || strings.HasSuffix(p, "h") {
							// window
							rateConf.Window, _ = time.ParseDuration(p)
						} else if strings.HasPrefix(p, "burst=") {
							rateConf.Burst, _ = strconv.Atoi(strings.TrimPrefix(p, "burst="))
						} else if strings.HasPrefix(p, "mode=") {
							rateConf.Mode = strings.TrimPrefix(p, "mode=")
						}
					}
				}

				if r.Args.Has("burst") {
					rateConf.Burst = r.Args.GetInt("burst", rateConf.Burst)
				}
				if r.Args.Has("mode") {
					rateConf.Mode = r.Args.Get("mode", rateConf.Mode)
				}
				if r.Args.Has("window") {
					rateConf.Window, _ = time.ParseDuration(r.Args.Get("window", "1s"))
				}
				if rateConf.Window == 0 {
					rateConf.Window = 1 * time.Second
				}
				wafCfg.Connection.RateLimit = rateConf

			case "ALLOW":
				wafCfg.Connection.AllowList = append(wafCfg.Connection.AllowList, r.Handler)
			case "DENY":
				wafCfg.Connection.DenyList = append(wafCfg.Connection.DenyList, r.Handler)
			case "IP":
				hook := httpserver.WAFHook{
					Event:   "IP",
					Handler: r.Handler,
					Inline:  r.Inline,
					Args:    r.Args,
				}
				wafCfg.Connection.IPHooks = append(wafCfg.Connection.IPHooks, hook)
			case "GEO":
				hook := httpserver.WAFHook{
					Event:   "GEO",
					Handler: r.Handler,
					Inline:  r.Inline,
					Args:    r.Args,
				}
				wafCfg.Connection.GEOHooks = append(wafCfg.Connection.GEOHooks, hook)
			}
		case "GEOJSON":
			if wafCfg.Connection == nil {
				wafCfg.Connection = &httpserver.ConnectionConfig{}
			}
			geo := httpserver.GeoJSONConfig{
				Name: r.Path,
			}
			if r.Inline {
				geo.Inline = r.Handler
			} else {
				geo.DataPath = r.Handler
			}
			wafCfg.Connection.GeoJSON = append(wafCfg.Connection.GeoJSON, geo)
		}
	}

	httpserver.RegisterWAF(name, wafCfg)
	if cfg.Args.Has("default") {
		httpserver.RegisterWAF("default", wafCfg)
	}

	if wafCfg.Connection != nil {
		security.GetEngine().LoadPolicy(name, wafCfg.Connection)
		if cfg.Args.Has("default") {
			security.GetEngine().LoadPolicy("default", wafCfg.Connection)
		}
	}

	return &SecurityDirective{
		NameStr: "SECURITY",
		Addr:    name,
		Config:  wafCfg,
	}, nil
}

func (s *SecurityDirective) Name() string    { return s.NameStr }
func (s *SecurityDirective) Address() string { return s.Addr }
func (s *SecurityDirective) Start() ([]net.Listener, error) {
	return nil, nil
}
func (s *SecurityDirective) Match(peek []byte) (bool, error) {
	return false, nil
}
func (d *SecurityDirective) Handle(conn net.Conn) error {
	return nil
}

func (d *SecurityDirective) HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error {
	return nil
}

func (d *SecurityDirective) Close() error {
	return nil
}

func IsInt(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func parseSize(s string) int64 {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0
	}
	unit := int64(1)
	if strings.HasSuffix(s, "kb") {
		unit = 1024
		s = s[:len(s)-2]
	} else if strings.HasSuffix(s, "mb") {
		unit = 1024 * 1024
		s = s[:len(s)-2]
	} else if strings.HasSuffix(s, "gb") {
		unit = 1024 * 1024 * 1024
		s = s[:len(s)-2]
	} else if strings.HasSuffix(s, "b") {
		s = s[:len(s)-1]
	}
	val, _ := strconv.ParseInt(s, 10, 64)
	return val * unit
}
