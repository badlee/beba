package security

import (
	"http-server/plugins/httpserver"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
	"sync/atomic"

	olcLib "github.com/google/open-location-code/go"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/planar"
)

// Engine is the central point for evaluating raw sockets against security rules.
// It will parse and cache CIDRs, rate limiters, etc. from WAF configurations.
type Engine struct {
	mu           sync.RWMutex
	rateLimiters map[string]*RateLimiter
	// Map of WAF rule names to parsed policies
	policies map[string]*Policy

	// Pre-connection stats for hook exposure
	totalConns atomic.Int64
}

type Policy struct {
	Name        string
	Config      *httpserver.ConnectionConfig
	RateLimiter *RateLimiter
	AllowCIDRs  []*net.IPNet
	AllowIPs    map[string]bool
	AllowGeo    []string // ISO codes, OLC codes, polygon names
	GeoJSON     map[string]orb.Geometry
	DenyCIDRs   []*net.IPNet
	DenyIPs     map[string]bool
	DenyGeo     []string // ISO codes, OLC codes, polygon names
}

// isoCode returns true if the provided string looks like an ISO 3166-1 alpha-2 country code (2 uppercase letters).
func isoCode(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

// isPlusCode returns true if the string looks like an OLC Plus Code.
func isPlusCode(s string) bool {
	return strings.Contains(s, "+")
}

// Global engine instance
var globalEngine *Engine
var once sync.Once

func GetEngine() *Engine {
	once.Do(func() {
		globalEngine = &Engine{
			rateLimiters: make(map[string]*RateLimiter),
			policies:     make(map[string]*Policy),
		}
		globalEngine.initBuiltinDefault()
	})
	return globalEngine
}

func (e *Engine) initBuiltinDefault() {
	// Baseline: 100 requests per second, burst 10
	cfg := &httpserver.ConnectionConfig{
		RateLimit: &httpserver.RateLimitConfig{
			Limit:  100,
			Window: time.Second,
			Burst:  10,
			Mode:   "ip",
		},
	}
	e.loadPolicyLocked("default", cfg)
}

// Reset clears all registered policies and rate limiters.
func (e *Engine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policies = make(map[string]*Policy)
	e.rateLimiters = make(map[string]*RateLimiter)
	e.initBuiltinDefault()
}

func (e *Engine) LoadPolicy(name string, cfg *httpserver.ConnectionConfig) {
	if cfg == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.loadPolicyLocked(name, cfg)
}

func (e *Engine) loadPolicyLocked(name string, cfg *httpserver.ConnectionConfig) {
	if cfg == nil {
		return
	}

	p := &Policy{
		Name:     name,
		Config:   cfg,
		AllowIPs: make(map[string]bool),
		DenyIPs:  make(map[string]bool),
		GeoJSON:  make(map[string]orb.Geometry),
	}

	if cfg.RateLimit != nil && cfg.RateLimit.Limit > 0 {
		window := cfg.RateLimit.Window
		if window <= 0 {
			window = time.Second
		}
		p.RateLimiter = NewRateLimiter(
			int64(cfg.RateLimit.Burst)+cfg.RateLimit.Limit,
			cfg.RateLimit.Limit,
			window,
		)
	}

	// Parse GeoJSON
	for _, pc := range cfg.GeoJSON {
		var data []byte
		var err error
		if pc.Inline != "" {
			data = []byte(pc.Inline)
		} else if pc.DataPath != "" {
			data, err = os.ReadFile(pc.DataPath)
		}
		if err == nil && len(data) > 0 {
			var geom orb.Geometry
			if fc, err := geojson.UnmarshalFeatureCollection(data); err == nil && len(fc.Features) > 0 {
				var collection orb.Collection
				for _, f := range fc.Features {
					if f.Geometry != nil {
						collection = append(collection, f.Geometry)
					}
				}
				geom = collection
			} else if f, err := geojson.UnmarshalFeature(data); err == nil {
				geom = f.Geometry
			} else if g, err := geojson.UnmarshalGeometry(data); err == nil {
				geom = g.Geometry()
			}
			if geom != nil {
				p.GeoJSON[pc.Name] = geom
			}
		}
	}

	for _, item := range cfg.AllowList {
		item = strings.Trim(item, "\"'`")
		if strings.Contains(item, "/") {
			_, ipnet, err := net.ParseCIDR(item)
			if err == nil {
				p.AllowCIDRs = append(p.AllowCIDRs, ipnet)
				continue
			}
		}
		if ip := net.ParseIP(item); ip != nil {
			p.AllowIPs[ip.String()] = true
			continue
		}
		// Otherwise: ISO country, OLC plus code, or polygon name → store for geo evaluation
		p.AllowGeo = append(p.AllowGeo, item)
	}

	for _, item := range cfg.DenyList {
		item = strings.Trim(item, "\"'`")
		if strings.Contains(item, "/") {
			_, ipnet, err := net.ParseCIDR(item)
			if err == nil {
				p.DenyCIDRs = append(p.DenyCIDRs, ipnet)
				continue
			}
		}
		if ip := net.ParseIP(item); ip != nil {
			p.DenyIPs[ip.String()] = true
			continue
		}
		p.DenyGeo = append(p.DenyGeo, item)
	}

	e.policies[name] = p
}

func (e *Engine) GetPolicy(name string) *Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.policies[name]
}

func (e *Engine) AllowConnection(conn net.Conn, policyName string) bool {
	if conn == nil {
		return false
	}
	remoteAddr := conn.RemoteAddr().String()
	ipStr, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ipStr = remoteAddr // Fallback
	}
	log.Printf("SECURITY: Checking connection from %s using policy %q", ipStr, policyName)
	return e.checkPolicy(conn, policyName, ipStr)
}

func (e *Engine) AllowPacket(addr net.Addr, policyName string) bool {
	if addr == nil {
		return false
	}
	remoteAddr := addr.String()
	ipStr, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		ipStr = remoteAddr // Fallback
	}
	log.Printf("SECURITY: Checking packet from %s using policy %q", ipStr, policyName)
	return e.checkPolicy(nil, policyName, ipStr)
}

func (e *Engine) checkPolicy(conn net.Conn, policyName string, ipStr string) bool {
	if ipStr == "" {
		return false
	}

	total := e.totalConns.Add(1)

	ip := net.ParseIP(ipStr)

	e.mu.RLock()
	// Priority: Specific policy -> default policy
	p := e.policies[policyName]
	if p == nil && policyName != "default" {
		p = e.policies["default"]
	}
	e.mu.RUnlock()

	if p != nil {
		var currentRate int64
		if p.RateLimiter != nil {
			// We peek: if bucket empty → deny immediately, else count as used
			if !p.RateLimiter.Allow(ipStr) {
				return false
			}
			currentRate = p.Config.RateLimit.Limit
		}

		if !p.Evaluate(conn, ip, ipStr, currentRate, total) {
			return false
		}
	}

	return true
}

func (p *Policy) Evaluate(conn net.Conn, ip net.IP, ipStr string, currentRate, total int64) bool {
	// 1. Check Denylist (IP/CIDR)
	if ip != nil {
		if p.DenyIPs[ipStr] {
			return false
		}
		for _, cidr := range p.DenyCIDRs {
			if cidr.Contains(ip) {
				return false
			}
		}
	}

	geoCtx := GetGeoContext(conn)

	// 2. Add extra Geo logic here if needed (e.g. city level)
	if geoCtx != nil && geoCtx.Latitude != 0 && geoCtx.Longitude != 0 {
		// potential for more granular checks
	}

	// 3. Deny Geo (ISO / OLC / polygon name)
	if geoCtx != nil && len(p.DenyGeo) > 0 {
		pt := orb.Point{geoCtx.Longitude, geoCtx.Latitude}
		for _, geo := range p.DenyGeo {
			if isoCode(geo) && geoCtx.Country == geo {
				return false
			}
			if isPlusCode(geo) {
				if err := olcLib.CheckFull(geo); err == nil {
					if InPlusCode(geo, geoCtx.Latitude, geoCtx.Longitude) {
						return false
					}
				}
			}
			if geom, ok := p.GeoJSON[geo]; ok {
				if GeometryContains(geom, pt) {
					return false
				}
			}
		}
	}

	// 4. Check Allowlist (if non-empty, must pass at least one)
	hasAllowRules := len(p.AllowIPs) > 0 || len(p.AllowCIDRs) > 0 || len(p.AllowGeo) > 0
	if hasAllowRules {
		allowed := false
		if ip != nil {
			if p.AllowIPs[ipStr] {
				allowed = true
			}
			if !allowed {
				for _, cidr := range p.AllowCIDRs {
					if cidr.Contains(ip) {
						allowed = true
						break
					}
				}
			}
		}
		if !allowed && geoCtx != nil {
			pt := orb.Point{geoCtx.Longitude, geoCtx.Latitude}
			for _, geo := range p.AllowGeo {
				if isoCode(geo) && geoCtx.Country == geo {
					allowed = true
					break
				}
				if isPlusCode(geo) {
					if err := olcLib.CheckFull(geo); err == nil {
						if InPlusCode(geo, geoCtx.Latitude, geoCtx.Longitude) {
							allowed = true
							break
						}
					}
				}
				if geom, ok := p.GeoJSON[geo]; ok {
					if GeometryContains(geom, pt) {
						allowed = true
						break
					}
				}
			}
		}
		if !allowed {
			return false
		}
	}

	// 5. Run IP hooks (CONNECTION IP BEGIN ... END CONNECTION)
	for _, hook := range p.Config.IPHooks {
		ok, err := RunIPHook(hook, conn, currentRate, total)
		if err != nil {
			log.Printf("[security] IP hook rejected: %v", err)
		}
		if !ok {
			return false
		}
	}

	// 6. Run GEO hooks (CONNECTION GEO BEGIN ... END CONNECTION)
	for _, hook := range p.Config.GEOHooks {
		ok, err := RunGeoHook(hook, conn, geoCtx)
		if err != nil {
			log.Printf("[security] GEO hook rejected: %v", err)
		}
		if !ok {
			return false
		}
	}

	// 7. Rate Limit (checked last so hooks can still run before throttling)
	if p.RateLimiter != nil {
		if !p.RateLimiter.Allow(ipStr) {
			return false
		}
	}

	return true
}

func GeometryContains(g orb.Geometry, pt orb.Point) bool {
	switch geom := g.(type) {
	case orb.Polygon:
		return planar.PolygonContains(geom, pt)
	case orb.MultiPolygon:
		return planar.MultiPolygonContains(geom, pt)
	case orb.Collection:
		for _, sub := range geom {
			if GeometryContains(sub, pt) {
				return true
			}
		}
	case orb.Ring:
		return planar.RingContains(geom, pt)
	case orb.Point:
		return geom == pt
	case orb.MultiPoint:
		for _, mp := range geom {
			if mp == pt {
				return true
			}
		}
	case orb.Bound:
		return geom.Contains(pt)
	}
	return false
}
