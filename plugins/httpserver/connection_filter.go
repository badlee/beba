package httpserver

import (
	"errors"
	"fmt"
	"http-server/processor"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	olc "github.com/google/open-location-code/go"
	"github.com/oschwald/geoip2-golang"
)

type tokenBucket struct {
	tokens int
	last   time.Time
}

type ConnectionSecurity struct {
	cfg   *ConnectionConfig
	geoDb *geoip2.Reader

	rateMu sync.Mutex
	rates  map[string]*tokenBucket

	allowCIDRs []*net.IPNet
	denyCIDRs  []*net.IPNet
	allowStr   []string
	denyStr    []string

	compiledHooks map[int]*goja.Program
}

func NewConnectionSecurity(cfg *ConnectionConfig, geoDb *geoip2.Reader) *ConnectionSecurity {
	if cfg == nil {
		return nil
	}
	cs := &ConnectionSecurity{
		cfg:           cfg,
		geoDb:         geoDb,
		rates:         make(map[string]*tokenBucket),
		compiledHooks: make(map[int]*goja.Program),
	}

	parseList := func(list []string, cidrList *[]*net.IPNet, strList *[]string) {
		for _, raw := range list {
			raw = strings.TrimSpace(raw)
			if strings.Contains(raw, "/") {
				_, ipnet, err := net.ParseCIDR(raw)
				if err == nil {
					*cidrList = append(*cidrList, ipnet)
					continue
				}
			}
			if ip := net.ParseIP(raw); ip != nil {
				_, ipnet, _ := net.ParseCIDR(raw + "/32")
				if ipnet != nil {
					*cidrList = append(*cidrList, ipnet)
				}
				continue
			}
			*strList = append(*strList, raw)
		}
	}

	parseList(cfg.AllowList, &cs.allowCIDRs, &cs.allowStr)
	parseList(cfg.DenyList, &cs.denyCIDRs, &cs.denyStr)

	// Precompile JS hooks (dummy hash or just linear indices)
	for i, hook := range cfg.IPHooks {
		if hook.Inline {
			if prg, err := goja.Compile("", hook.Handler, false); err == nil {
				cs.compiledHooks[i] = prg
			}
		}
	}
	for i, hook := range cfg.GEOHooks {
		if hook.Inline {
			if prg, err := goja.Compile("", hook.Handler, false); err == nil {
				cs.compiledHooks[1000+i] = prg // Offset for GEO
			}
		}
	}

	if cfg.RateLimit != nil && cfg.RateLimit.Limit > 0 {
		ticker := time.NewTicker(1 * time.Minute)
		go func() {
			for range ticker.C {
				cs.rateMu.Lock()
				now := time.Now()
				for k, v := range cs.rates {
					if now.Sub(v.last) > time.Minute {
						delete(cs.rates, k)
					}
				}
				cs.rateMu.Unlock()
			}
		}()
	}

	return cs
}

func (cs *ConnectionSecurity) Accept(conn net.Conn) error {
	host, port, _ := net.SplitHostPort(conn.RemoteAddr().String())
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}

	// 1. Rate Limiting Check
	if rl := cs.cfg.RateLimit; rl != nil && rl.Limit > 0 {
		key := host
		cs.rateMu.Lock()
		b, exists := cs.rates[key]
		now := time.Now()
		if !exists {
			b = &tokenBucket{tokens: int(rl.Limit), last: now}
			cs.rates[key] = b
		} else {
			// refuel
			elapsed := now.Sub(b.last)
			if elapsed > rl.Window {
				b.tokens = int(rl.Limit)
				b.last = now
			}
		}
		b.tokens--
		allowed := true
		if b.tokens < -rl.Burst {
			allowed = false
		}
		cs.rateMu.Unlock()
		if !allowed {
			return errors.New("rate limit exceeded")
		}
	}

	// Fetch GEO Data lazily if requested
	var countryIso string
	var asnNum uint
	var lat, lon float64
	geoLoaded := false

	loadGeo := func() {
		if !geoLoaded && cs.geoDb != nil {
			rec, err := cs.geoDb.City(ip)
			if err == nil {
				countryIso = rec.Country.IsoCode
				lat = rec.Location.Latitude
				lon = rec.Location.Longitude
				// ASN is typically in a separate DB, but we do best effort or leave 0 for now.
			}
			geoLoaded = true
		}
	}

	// Match Lists
	checkStringList := func(strList []string) bool {
		for _, s := range strList {
			s = strings.ToUpper(s)
			if !geoLoaded && len(s) == 2 {
				loadGeo() // maybe ISO
			}
			if len(s) == 2 && countryIso == s {
				return true
			}
			// check plus code
			if strings.Contains(s, "+") {
				loadGeo()
				if geoLoaded && lat != 0 && lon != 0 {
					area, err := olc.Decode(s)
					if err == nil {
						if lat >= area.LatLo && lat <= area.LatHi && lon >= area.LngLo && lon <= area.LngHi {
							return true
						}
					}
				}
			}
		}
		return false
	}

	// 2. Deny List
	for _, mask := range cs.denyCIDRs {
		if mask.Contains(ip) {
			return errors.New("connection denied by IP blocklist")
		}
	}
	if checkStringList(cs.denyStr) {
		return errors.New("connection denied by GEO/OLC blocklist")
	}

	// 3. Allow List (if populated and not matched, implies deny)
	if len(cs.allowCIDRs) > 0 || len(cs.allowStr) > 0 {
		allowed := false
		for _, mask := range cs.allowCIDRs {
			if mask.Contains(ip) {
				allowed = true
				break
			}
		}
		if !allowed && checkStringList(cs.allowStr) {
			allowed = true
		}
		if !allowed {
			return errors.New("connection not in allowlist")
		}
	}

	// 4. JS Hooks
	runHooks := func(hooks []WAFHook, offset int, env map[string]interface{}) error {
		for i, hook := range hooks {
			prg := cs.compiledHooks[offset+i]
			if prg == nil {
				continue
			}
			var vm *processor.Processor
			if !hook.Inline {
				file, err := os.Stat(hook.Handler)
				if err != nil {
					return fmt.Errorf("security hook: %s is not exists", hook.Handler)
				}
				if file.IsDir() {
					return fmt.Errorf("security hook: %s is a directory", hook.Handler)
				}
				vm = processor.New(filepath.Dir(hook.Handler), nil)
			} else {
				vm = processor.NewVM()
			}
			vm.AttachGlobals()

			rejected := false
			rejectMsg := "rejected by hook"
			vm.Set("allow", func() {})
			vm.Set("reject", func(msg string) {
				rejected = true
				if msg != "" {
					rejectMsg = msg
				}
			})
			for k, v := range env {
				vm.Set(k, v)
			}
			for k, v := range hook.Args {
				vm.Set(k, v) // Global args bound
			}

			_, err := vm.RunProgram(prg)
			if err != nil {
				return fmt.Errorf("hook error: %v", err)
			}
			if rejected {
				return errors.New(rejectMsg)
			}
		}
		return nil
	}

	if len(cs.cfg.IPHooks) > 0 {
		cs.rateMu.Lock()
		b, _ := cs.rates[host]
		rate := 0
		if b != nil {
			rate = int(cs.cfg.RateLimit.Limit) - b.tokens
		}
		cs.rateMu.Unlock()
		env := map[string]interface{}{
			"CONN": map[string]interface{}{
				"ip":           host,
				"port":         port,
				"current_rate": rate,
			},
		}
		if err := runHooks(cs.cfg.IPHooks, 0, env); err != nil {
			return err
		}
	}

	if len(cs.cfg.GEOHooks) > 0 {
		loadGeo()
		env := map[string]interface{}{
			"GEO": map[string]interface{}{
				"country": countryIso,
				"asn":     asnNum,
				"lat":     lat,
				"lon":     lon,
			},
		}
		if err := runHooks(cs.cfg.GEOHooks, 1000, env); err != nil {
			return err
		}
	}

	return nil
}
