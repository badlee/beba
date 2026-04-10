package security

import (
	"net"
	"os"
	"testing"
	"time"

	"http-server/plugins/httpserver"

	olc "github.com/google/open-location-code/go"
	"github.com/paulmach/orb"
)

// --------------------------------------------------------------------------
// TokenBucket / RateLimiter
// --------------------------------------------------------------------------

func TestTokenBucket_AllowAndDeny(t *testing.T) {
	// Bucket of 3 tokens, refill 3/s
	bucket := NewTokenBucket(3, 3, time.Second)

	// First 3 calls must succeed
	for i := 0; i < 3; i++ {
		if !bucket.Allow() {
			t.Fatalf("call %d: expected Allow=true", i+1)
		}
	}
	// 4th must be denied (bucket empty)
	if bucket.Allow() {
		t.Error("expected Allow=false after exhausting bucket")
	}
}

func TestRateLimiter_PerIP(t *testing.T) {
	rl := NewRateLimiter(2, 2, time.Second)

	if !rl.Allow("1.2.3.4") {
		t.Error("1st call 1.2.3.4 should allow")
	}
	if !rl.Allow("1.2.3.4") {
		t.Error("2nd call 1.2.3.4 should allow")
	}
	if rl.Allow("1.2.3.4") {
		t.Error("3rd call 1.2.3.4 should deny")
	}

	// Different IP has its own bucket
	if !rl.Allow("9.9.9.9") {
		t.Error("1st call 9.9.9.9 should allow")
	}
}

// --------------------------------------------------------------------------
// CIDR / IP Evaluation in Policy
// --------------------------------------------------------------------------

func makePolicyWithCIDR(allowCIDR, denyCIDR string) *Policy {
	p := &Policy{
		AllowIPs: make(map[string]bool),
		DenyIPs:  make(map[string]bool),
		Config:   &httpserver.ConnectionConfig{},
	}
	if denyCIDR != "" {
		_, ipnet, _ := net.ParseCIDR(denyCIDR)
		p.DenyCIDRs = []*net.IPNet{ipnet}
	}
	if allowCIDR != "" {
		_, ipnet, _ := net.ParseCIDR(allowCIDR)
		p.AllowCIDRs = []*net.IPNet{ipnet}
	}
	return p
}

func TestPolicy_DenyIP(t *testing.T) {
	p := &Policy{
		AllowIPs: make(map[string]bool),
		DenyIPs:  map[string]bool{"10.0.0.5": true},
		Config:   &httpserver.ConnectionConfig{},
	}
	ip := net.ParseIP("10.0.0.5")
	conn := &fakeConn{addr: "10.0.0.5:12345"}
	if p.Evaluate(conn, ip, "10.0.0.5", 0, 0) {
		t.Error("expected denied IP to be blocked")
	}
}

func TestPolicy_DenyCIDR(t *testing.T) {
	p := makePolicyWithCIDR("", "192.168.0.0/16")
	ip := net.ParseIP("192.168.1.100")
	conn := &fakeConn{addr: "192.168.1.100:9999"}
	if p.Evaluate(conn, ip, "192.168.1.100", 0, 0) {
		t.Error("expected CIDR-denied IP to be blocked")
	}
	// IP outside the CIDR should not be blocked
	ip2 := net.ParseIP("10.0.0.1")
	conn2 := &fakeConn{addr: "10.0.0.1:9999"}
	if !p.Evaluate(conn2, ip2, "10.0.0.1", 0, 0) {
		t.Error("expected non-CIDR IP to be allowed")
	}
}

func TestPolicy_AllowListOnly(t *testing.T) {
	p := makePolicyWithCIDR("172.16.0.0/12", "")
	// IP in allowlist CIDR → allowed
	ip := net.ParseIP("172.16.5.10")
	conn := &fakeConn{addr: "172.16.5.10:9999"}
	if !p.Evaluate(conn, ip, "172.16.5.10", 0, 0) {
		t.Error("expected IP in allowlist CIDR to be allowed")
	}
	// IP outside → blocked
	ip2 := net.ParseIP("8.8.8.8")
	conn2 := &fakeConn{addr: "8.8.8.8:9999"}
	if p.Evaluate(conn2, ip2, "8.8.8.8", 0, 0) {
		t.Error("expected IP outside allowlist CIDR to be denied")
	}
}

func TestPolicy_RateLimit(t *testing.T) {
	p := &Policy{
		AllowIPs:    make(map[string]bool),
		DenyIPs:     make(map[string]bool),
		Config:      &httpserver.ConnectionConfig{},
		RateLimiter: NewRateLimiter(2, 2, time.Second),
	}
	conn := &fakeConn{addr: "1.1.1.1:5000"}
	ip := net.ParseIP("1.1.1.1")

	if !p.Evaluate(conn, ip, "1.1.1.1", 0, 1) {
		t.Error("1st should be allowed")
	}
	if !p.Evaluate(conn, ip, "1.1.1.1", 0, 2) {
		t.Error("2nd should be allowed")
	}
	if p.Evaluate(conn, ip, "1.1.1.1", 0, 3) {
		t.Error("3rd should be rate-limited")
	}
}

// --------------------------------------------------------------------------
// OLC / Plus Codes
// --------------------------------------------------------------------------

func TestInPlusCode_Valid(t *testing.T) {
	// Use known coordinates: Eiffel Tower, Paris
	lat, lon := 48.8584, 2.2945

	// Encode to a full 10-digit code (precision ~13.9m x 13.9m)
	code := olc.Encode(lat, lon, 10)

	// The encoded code must contain the original point
	if !InPlusCode(code, lat, lon) {
		t.Errorf("InPlusCode(%q, %.4f, %.4f): expected true (point is inside its own code)", code, lat, lon)
	}

	// A clearly different point must NOT be inside the code
	if InPlusCode(code, 0.0, 0.0) {
		t.Errorf("InPlusCode(%q, 0, 0): expected false (Africa != Paris)", code)
	}
}

func TestPolygon_Containment(t *testing.T) {
	p := &Policy{
		GeoJSON:  make(map[string]orb.Geometry),
		AllowGeo: []string{"test_poly"},
	}

	// Simple square polygon around (1,1) -> (2,2)
	// GeoJSON: [[1,1], [2,1], [2,2], [1,2], [1,1]]
	poly := orb.Polygon{{{1, 1}, {2, 1}, {2, 2}, {1, 2}, {1, 1}}}
	p.GeoJSON["test_poly"] = poly

	// Point inside
	if !GeometryContains(p.GeoJSON["test_poly"], orb.Point{1.5, 1.5}) {
		t.Error("expected (1.5, 1.5) to be inside polygon")
	}

	// Point outside
	if GeometryContains(p.GeoJSON["test_poly"], orb.Point{0, 0}) {
		t.Error("expected (0, 0) to be outside polygon")
	}
}

func TestGeoJSON_Complexity(t *testing.T) {
	data, err := os.ReadFile("../../examples/complex_zone.geojson")
	if err != nil {
		t.Skip("complex_zone.geojson not found")
	}

	cfg := &httpserver.ConnectionConfig{
		GeoJSON: []httpserver.GeoJSONConfig{
			{Name: "complex", Inline: string(data)},
		},
	}

	e := GetEngine()
	e.LoadPolicy("test_complex", cfg)
	p := e.GetPolicy("test_complex")

	tests := []struct {
		pt      orb.Point
		inside  bool
		comment string
	}{
		{orb.Point{9.5, 0.5}, true, "Inside first square"},
		{orb.Point{10.1, 0.45}, true, "Inside second circle-like polygon"},
		{orb.Point{9.0, -1.5}, true, "Inside third long polygon"},
		{orb.Point{0, 0}, false, "Clearly outside"},
	}

	for _, tc := range tests {
		res := GeometryContains(p.GeoJSON["complex"], tc.pt)
		if res != tc.inside {
			t.Errorf("%s: expected %v, got %v", tc.comment, tc.inside, res)
		}
	}
}

func TestGeoJSON_AllTypes(t *testing.T) {
	e := GetEngine()

	// 1. MultiPoint
	cfgMP := &httpserver.ConnectionConfig{
		GeoJSON: []httpserver.GeoJSONConfig{{Name: "mp", Inline: `{"type":"MultiPoint","coordinates":[[1,1],[2,2]]}`}},
	}
	e.LoadPolicy("test_mp", cfgMP)
	pMP := e.GetPolicy("test_mp")
	if !GeometryContains(pMP.GeoJSON["mp"], orb.Point{1, 1}) {
		t.Error("expected MultiPoint to contain (1,1)")
	}

	// 2. GeometryCollection (Point + Polygon)
	cfgGC := &httpserver.ConnectionConfig{
		GeoJSON: []httpserver.GeoJSONConfig{{Name: "gc", Inline: `{"type":"GeometryCollection","geometries":[{"type":"Point","coordinates":[10,10]},{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}]}`}},
	}
	e.LoadPolicy("test_gc", cfgGC)
	pGC := e.GetPolicy("test_gc")
	if !GeometryContains(pGC.GeoJSON["gc"], orb.Point{10, 10}) {
		t.Error("expected GeometryCollection to contain point (10,10)")
	}
	if !GeometryContains(pGC.GeoJSON["gc"], orb.Point{0.5, 0.5}) {
		t.Error("expected GeometryCollection to contain point inside its polygon")
	}

	// 3. LineString (Point on line)
	// We'll see if our current logic handles it (likely not yet, needs planar.DistanceFromPoint check if we want it)
	// For now, geofencing is mostly Area-based.
}

func TestISOCode_Detection(t *testing.T) {
	tests := []struct {
		s       string
		wantISO bool
	}{
		{"GN", true},
		{"FR", true},
		{"CN", true},
		{"RU", true},
		{"889CM4V2+PQ", false},
		{"192.168.0.0/24", false},
		{"default", false},
		{"US", true},
	}
	for _, tt := range tests {
		got := isoCode(tt.s)
		if got != tt.wantISO {
			t.Errorf("isoCode(%q) = %v, want %v", tt.s, got, tt.wantISO)
		}
	}
}

// --------------------------------------------------------------------------
// JS IP Hook sandbox
// --------------------------------------------------------------------------

func TestRunIPHook_AllowByDefault(t *testing.T) {
	hook := httpserver.WAFHook{
		Inline:  true,
		Handler: `// no explicit allow/reject → allow by default`,
	}
	conn := &fakeConn{addr: "10.0.0.1:4444"}
	ok, err := RunIPHook(hook, conn, 5, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allow by default when no signal emitted")
	}
}

func TestRunIPHook_Allow(t *testing.T) {
	hook := httpserver.WAFHook{
		Inline:  true,
		Handler: `allow();`,
	}
	conn := &fakeConn{addr: "10.0.0.1:4444"}
	ok, err := RunIPHook(hook, conn, 5, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allow()")
	}
}

func TestRunIPHook_Reject(t *testing.T) {
	hook := httpserver.WAFHook{
		Inline:  true,
		Handler: `reject("blocked by test");`,
	}
	conn := &fakeConn{addr: "10.0.0.2:4444"}
	ok, err := RunIPHook(hook, conn, 200, 1000)
	if ok {
		t.Error("expected reject() to deny connection")
	}
	if err == nil {
		t.Error("expected ErrReject to be returned")
	}
	if re, ok2 := err.(*ErrReject); !ok2 || re.Reason != "blocked by test" {
		t.Errorf("unexpected reject error: %v", err)
	}
}

func TestRunIPHook_CONNObject(t *testing.T) {
	hook := httpserver.WAFHook{
		Inline:  true,
		Handler: `if (CONN.ip !== "10.0.0.5") reject("wrong IP"); else allow();`,
	}
	conn := &fakeConn{addr: "10.0.0.5:8080"}
	ok, err := RunIPHook(hook, conn, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allow when CONN.ip matches")
	}
}

func TestRunIPHook_Args(t *testing.T) {
	hook := httpserver.WAFHook{
		Inline:  true,
		Handler: `if (CONN.ip === args.whitelist) allow(); else reject("not whitelisted");`,
		Args:    map[string]string{"whitelist": "127.0.0.1"},
	}
	// allowed
	conn1 := &fakeConn{addr: "127.0.0.1:9999"}
	ok, _ := RunIPHook(hook, conn1, 0, 0)
	if !ok {
		t.Error("127.0.0.1 should be whitelisted")
	}
	// blocked
	conn2 := &fakeConn{addr: "5.5.5.5:9999"}
	ok, _ = RunIPHook(hook, conn2, 0, 0)
	if ok {
		t.Error("5.5.5.5 should not be whitelisted")
	}
}

func TestRunIPHook_RateCheck(t *testing.T) {
	hook := httpserver.WAFHook{
		Inline:  true,
		Handler: `if (CONN.current_rate > 100) reject("too fast"); else allow();`,
	}
	conn := &fakeConn{addr: "2.2.2.2:1234"}
	// under rate → allow
	ok, _ := RunIPHook(hook, conn, 50, 0)
	if !ok {
		t.Error("rate=50 should be allowed")
	}
	// over rate → reject
	ok, _ = RunIPHook(hook, conn, 150, 0)
	if ok {
		t.Error("rate=150 should be rejected")
	}
}

// --------------------------------------------------------------------------
// GEO hook
// --------------------------------------------------------------------------

func TestRunGeoHook_CountryAllow(t *testing.T) {
	hook := httpserver.WAFHook{
		Inline:  true,
		Handler: `if (GEO.country === "GN" || GEO.country === "GA") allow(); else reject("country blocked");`,
	}
	conn := &fakeConn{addr: "1.1.1.1:80"}

	// Allowed country
	gn := &GeoContext{Country: "GN"}
	ok, _ := RunGeoHook(hook, conn, gn)
	if !ok {
		t.Error("GN should be allowed")
	}

	// Blocked country
	cn := &GeoContext{Country: "CN"}
	ok, _ = RunGeoHook(hook, conn, cn)
	if ok {
		t.Error("CN should be rejected")
	}
}

func TestRunGeoHook_NilGeo(t *testing.T) {
	hook := httpserver.WAFHook{
		Inline:  true,
		Handler: `allow();`,
	}
	conn := &fakeConn{addr: "1.1.1.1:80"}
	ok, err := RunGeoHook(hook, conn, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allow even with nil GeoContext")
	}
}

// --------------------------------------------------------------------------
// Engine — Policy loading & AllowConnection
// --------------------------------------------------------------------------

func TestEngine_LoadAndEvaluate_DenyIP(t *testing.T) {
	e := &Engine{
		rateLimiters: make(map[string]*RateLimiter),
		policies:     make(map[string]*Policy),
	}
	cfg := &httpserver.ConnectionConfig{
		DenyList: []string{"203.0.113.5"},
	}
	e.LoadPolicy("default", cfg)

	conn := &fakeConn{addr: "203.0.113.5:5000"}
	if e.AllowConnection(conn, "default") {
		t.Error("explicitly denied IP should be blocked at engine level")
	}

	conn2 := &fakeConn{addr: "1.2.3.4:5000"}
	if !e.AllowConnection(conn2, "default") {
		t.Error("non-denied IP should be allowed")
	}
}

func TestEngine_LoadAndEvaluate_Allow(t *testing.T) {
	e := &Engine{
		rateLimiters: make(map[string]*RateLimiter),
		policies:     make(map[string]*Policy),
	}
	cfg := &httpserver.ConnectionConfig{
		AllowList: []string{"10.0.0.0/8"},
	}
	e.LoadPolicy("default", cfg)

	connOK := &fakeConn{addr: "10.5.5.5:1234"}
	if !e.AllowConnection(connOK, "default") {
		t.Error("10.5.5.5 is in allowed /8, should pass")
	}

	connBlocked := &fakeConn{addr: "8.8.8.8:1234"}
	if e.AllowConnection(connBlocked, "default") {
		t.Error("8.8.8.8 is NOT in allowed /8, should be blocked")
	}
}

// --------------------------------------------------------------------------
// fakeConn — minimal net.Conn for tests
// --------------------------------------------------------------------------

type fakeConn struct {
	net.Conn
	addr string
}

type fakeAddr struct{ s string }

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return a.s }

func (c *fakeConn) RemoteAddr() net.Addr { return fakeAddr{c.addr} }
func (c *fakeConn) Close() error         { return nil }
