package binder

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"http-server/modules/security"

	"github.com/paulmach/orb"
)

func TestSecuritySuite(t *testing.T) {
	testDir := "testdata/security"
	files, err := os.ReadDir(testDir)
	if err != nil {
		t.Skipf("No testdata found: %v", err)
	}

	for _, f := range files {
		if filepath.Ext(f.Name()) != ".bind" {
			continue
		}

		t.Run(f.Name(), func(t *testing.T) {
			path := filepath.Join(testDir, f.Name())
			cfg, _, err := ParseFile(path)
			if err != nil {
				t.Fatalf("Parse error %s: %v", f.Name(), err)
			}

			// Extract the security block if it exists
			var policy *security.Policy
			// Try to find a SECURITY group first
			for _, g := range cfg.Groups {
				if g.Directive == "SECURITY" && len(g.Items) > 0 {
					// The first item in a SECURITY group is the policy itself
					dc := g.Items[0]
					_, err := NewSecurityDirective(dc)
					if err != nil {
						t.Fatalf("Failed to init SECURITY directive: %v", err)
					}
					policy = security.GetEngine().GetPolicy(dc.Address)
					break
				}
			}

			if policy == nil && f.Name() != "default_baseline.bind" {
				t.Fatalf("Policy not loaded into engine for %s", f.Name())
			}

			// Functional checks based on filename
			switch f.Name() {
			case "firewall_basic.bind":
				checkFirewallBasic(t, policy)
			case "rate_limit_advanced.bind":
				checkRateLimitAdvanced(t, policy)
			case "geo_advanced.bind":
				checkGeoAdvanced(t, policy)
			case "js_hooks.bind":
				checkJSHooks(t, policy)
			case "geojson_enforcement.bind":
				checkGeoJSONEnforcement(t, policy)
			case "complex_geojson.bind":
				checkComplexGeoJSON(t, policy)
			case "default_baseline.bind":
				checkDefaultInheritance(t, policy)
			case "default_override.bind":
				checkDefaultOverride(t, policy)
			}
		})
	}
}

func checkFirewallBasic(t *testing.T, p *security.Policy) {
	// Deny 1.2.3.4
	if p.Evaluate(nil, net.ParseIP("1.2.3.4"), "1.2.3.4", 0, 1) {
		t.Error("1.2.3.4 should be denied")
	}
	// Allow 127.0.0.1
	if !p.Evaluate(nil, net.ParseIP("127.0.0.1"), "127.0.0.1", 0, 1) {
		t.Error("127.0.0.1 should be allowed")
	}
	// Allow 10.1.2.3
	if !p.Evaluate(nil, net.ParseIP("10.1.2.3"), "10.1.2.3", 0, 1) {
		t.Error("10.1.2.3 should be allowed by 10.0.0.0/8")
	}
}

func checkRateLimitAdvanced(t *testing.T, p *security.Policy) {
	if p.Config.RateLimit == nil {
		t.Fatal("RateLimit not found")
	}
	rl := p.Config.RateLimit
	if rl.Limit != 100 || rl.Burst != 50 {
		t.Errorf("Unexpected rate limit settings: %+v", rl)
	}
}

func checkGeoAdvanced(t *testing.T, p *security.Policy) {
	// Verify lists
	foundCN := false
	for _, g := range p.Config.DenyList {
		if g == "CN" {
			foundCN = true
		}
	}
	if !foundCN {
		t.Error("CN not in deny list")
	}

	foundOLC := false
	for _, g := range p.Config.AllowList {
		if g == "889CM4V2+PQ" {
			foundOLC = true
		}
	}
	if !foundOLC {
		t.Error("OLC not in allow list")
	}
}

func checkJSHooks(t *testing.T, p *security.Policy) {
	if len(p.Config.IPHooks) == 0 {
		t.Error("IP hook missing")
	}
	if len(p.Config.GEOHooks) == 0 {
		t.Error("GEO hook missing")
	}
}

func checkGeoJSONEnforcement(t *testing.T, p *security.Policy) {
	if len(p.Config.GeoJSON) != 2 {
		t.Fatalf("Expected 2 GeoJSON configs, got %d", len(p.Config.GeoJSON))
	}
	// Zone A is inline, Zone B is file
	if p.Config.GeoJSON[0].Name != "zone_a" {
		t.Errorf("Expected zone_a, got %s", p.Config.GeoJSON[0].Name)
	}
}

func checkComplexGeoJSON(t *testing.T, p *security.Policy) {
	if len(p.Config.GeoJSON) != 1 {
		t.Fatalf("Expected 1 GeoJSON config, got %d", len(p.Config.GeoJSON))
	}
	// For simple testing in the suite, we can call GeometryContains directly on the loaded policy
	geom := p.GeoJSON["complex_area"]
	if !security.GeometryContains(geom, orb.Point{9.5, 0.5}) {
		t.Error("Expected (9.5, 0.5) to be inside complex_area square")
	}
	if !security.GeometryContains(geom, orb.Point{10.1, 0.45}) {
		t.Error("Expected (10.1, 0.45) to be inside complex_area circle")
	}
}

func checkDefaultInheritance(t *testing.T, p *security.Policy) {
	// The binder will have loaded the built-in default if no SECURITY block was parsed.
	// But in TestSecuritySuite, each file creates a NEW policy based on its content.
	engine := security.GetEngine()
	def := engine.GetPolicy("default")
	if def == nil {
		t.Fatal("Built-in default policy missing")
	}
	if def.Config.RateLimit.Limit != 100 {
		t.Errorf("Expected baseline limit 100, got %d", def.Config.RateLimit.Limit)
	}
}

func checkDefaultOverride(t *testing.T, p *security.Policy) {
	// In default_override.bind, there IS a SECURITY block named 'my_strict_default' with 'default' arg.
	// So both 'my_strict_default' and 'default' should point to the 2r/s policy.
	engine := security.GetEngine()
	def := engine.GetPolicy("default")
	if def == nil {
		t.Fatal("Default policy missing after override")
	}
	if def.Config.RateLimit.Limit != 2 {
		t.Errorf("Expected overridden limit 2, got %d", def.Config.RateLimit.Limit)
	}

	// Also check by its actual name
	strict := engine.GetPolicy("my_strict_default")
	if strict == nil {
		t.Fatal("Policy 'my_strict_default' missing")
	}
}

// mockConn used for testing
type mockConnSuite struct {
	net.Conn
	addr net.Addr
}

func (m *mockConnSuite) RemoteAddr() net.Addr { return m.addr }
func (m *mockConnSuite) Close() error         { return nil }
