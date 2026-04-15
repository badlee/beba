package binder

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"http-server/processor"

	"github.com/dop251/goja"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// parsePaymentBind writes content to a temp .bind file and parses it.
func parsePaymentBind(t *testing.T, content string) *Config {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "payment.bind")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return cfg
}

// paymentProto returns the DirectiveConfig for group[idx].
func paymentProto(t *testing.T, cfg *Config, idx int) *DirectiveConfig {
	t.Helper()
	if len(cfg.Groups) <= idx {
		t.Fatalf("expected group[%d], got %d groups", idx, len(cfg.Groups))
	}
	if len(cfg.Groups[idx].Items) == 0 {
		t.Fatalf("group[%d] has no items", idx)
	}
	return cfg.Groups[idx].Items[0]
}

// mockProvider is a test double that records calls and returns canned results.
type mockProvider struct {
	chargeResult   PaymentResult
	chargeErr      error
	verifyResult   PaymentResult
	verifyErr      error
	refundResult   PaymentResult
	refundErr      error
	checkoutResult PaymentResult
	checkoutErr    error
	pushResult     PaymentResult
	pushErr        error

	lastChargeReq   PaymentRequest
	lastVerifyID    string
	lastRefundReq   PaymentRequest
	lastCheckoutReq PaymentRequest
	lastPushReq     PaymentRequest
}

func (m *mockProvider) Charge(req PaymentRequest) (PaymentResult, error) {
	m.lastChargeReq = req
	return m.chargeResult, m.chargeErr
}
func (m *mockProvider) Verify(id string) (PaymentResult, error) {
	m.lastVerifyID = id
	return m.verifyResult, m.verifyErr
}
func (m *mockProvider) Refund(req PaymentRequest) (PaymentResult, error) {
	m.lastRefundReq = req
	return m.refundResult, m.refundErr
}
func (m *mockProvider) Checkout(req PaymentRequest) (PaymentResult, error) {
	m.lastCheckoutReq = req
	return m.checkoutResult, m.checkoutErr
}
func (m *mockProvider) Push(req PaymentRequest) (PaymentResult, error) {
	m.lastPushReq = req
	return m.pushResult, m.pushErr
}

// freshPaymentConn builds a minimal PaymentConnection wired to a mockProvider.
func freshPaymentConn(name string, mock *mockProvider) *PaymentConnection {
	return &PaymentConnection{
		name:     name,
		provider: mock,
		currency: "XOF",
		mode:     "sandbox",
		metadata: make(map[string]string),
	}
}

// mockHTTPServer starts a test HTTP server that returns the given JSON body
// and status code for every request. Returns the server and its URL.
func mockHTTPServer(t *testing.T, status int, body map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ─────────────────────────────────────────────────────────────────────────────
// DSL parsing
// ─────────────────────────────────────────────────────────────────────────────

func TestPayment_NAME_Required(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'stripe://sk_test_xxx'
END PAYMENT
`)
	d := &PaymentDirective{config: paymentProto(t, cfg, 0)}
	if _, err := d.Start(); err == nil || !strings.Contains(err.Error(), "NAME") {
		t.Fatalf("expected NAME error, got: %v", err)
	}
}

func TestPayment_DSL_NativeProvider(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'stripe://sk_test_key' [default]
    NAME stripe
    MODE sandbox
    CURRENCY EUR
    COUNTRY FR
    CALLBACK "https://myapp.com/cb"
    REDIRECT success "/merci"
    REDIRECT cancel  "/annule"
    REDIRECT failure "/echec"
    SET company "TestApp"
END PAYMENT
`)
	proto := paymentProto(t, cfg, 0)

	if !proto.Args.GetBool("default") {
		t.Error("expected default=true")
	}

	nameR := routeScalarFromRoutes(proto.Routes, "NAME")
	if nameR != "stripe" {
		t.Errorf("expected NAME stripe, got %q", nameR)
	}
	if routeScalarFromRoutes(proto.Routes, "MODE") != "sandbox" {
		t.Errorf("expected MODE sandbox")
	}
	if routeScalarFromRoutes(proto.Routes, "CURRENCY") != "EUR" {
		t.Errorf("expected CURRENCY EUR")
	}
	if routeScalarFromRoutes(proto.Routes, "COUNTRY") != "FR" {
		t.Errorf("expected COUNTRY FR")
	}
	if routeScalarFromRoutes(proto.Routes, "CALLBACK") != "https://myapp.com/cb" {
		t.Errorf("expected CALLBACK https://myapp.com/cb")
	}
	if proto.Configs.Get("company") != "TestApp" {
		t.Errorf("expected SET company=TestApp, got %s", proto.Configs.Get("company"))
	}

	// REDIRECT directives
	redirects := map[string]string{}
	for _, r := range proto.Routes {
		if strings.ToUpper(r.Method) == "REDIRECT" {
			redirects[r.Path] = r.Handler
		}
	}
	if redirects["success"] != "/merci" {
		t.Errorf("expected REDIRECT success=/merci, got %q", redirects["success"])
	}
	if redirects["cancel"] != "/annule" {
		t.Errorf("expected REDIRECT cancel=/annule")
	}
	if redirects["failure"] != "/echec" {
		t.Errorf("expected REDIRECT failure=/echec")
	}
}

func TestPayment_DSL_Webhook_Inline(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'stripe://sk_test_key'
    NAME stripe
    WEBHOOK @PRE /pay/webhook BEGIN [secret=whsec_xxx]
        if (!verify(request.body, request.headers["stripe-signature"], args.secret)) reject("bad sig")
    END WEBHOOK
    WEBHOOK @POST /pay/webhook BEGIN
        if (payment.status === "succeeded") {}
    END WEBHOOK
END PAYMENT
`)
	proto := paymentProto(t, cfg, 0)
	webhooks := routesOfMethod(proto.Routes, "WEBHOOK")
	if len(webhooks) != 2 {
		t.Fatalf("expected 2 WEBHOOK routes, got %d", len(webhooks))
	}

	pre := webhooks[0]
	if len(pre.Middlewares) == 0 || pre.Middlewares[0].Name != "PRE" {
		t.Errorf("expected @PRE on first webhook, got %v", pre.Middlewares)
	}
	if !pre.Inline {
		t.Error("expected inline PRE webhook")
	}
	if pre.Args.Get("secret") != "whsec_xxx" {
		t.Errorf("expected secret=whsec_xxx, got %s", pre.Args.Get("secret"))
	}

	post := webhooks[1]
	if len(post.Middlewares) == 0 || post.Middlewares[0].Name != "POST" {
		t.Errorf("expected @POST on second webhook, got %v", post.Middlewares)
	}
	if !post.Inline {
		t.Error("expected inline POST webhook")
	}
}

func TestPayment_DSL_Webhook_File(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'stripe://sk_test_key'
    NAME stripe
    WEBHOOK @PRE  /pay/webhook "hooks/pre.js"  [secret=xxx]
    WEBHOOK @POST /pay/webhook "hooks/post.js"
END PAYMENT
`)
	proto := paymentProto(t, cfg, 0)
	webhooks := routesOfMethod(proto.Routes, "WEBHOOK")
	if len(webhooks) != 2 {
		t.Fatalf("expected 2 WEBHOOK routes, got %d", len(webhooks))
	}
	if webhooks[0].Inline {
		t.Error("pre webhook should be file-based")
	}
	if webhooks[0].Handler != "hooks/pre.js" {
		t.Errorf("expected hooks/pre.js, got %q", webhooks[0].Handler)
	}
	if webhooks[1].Handler != "hooks/post.js" {
		t.Errorf("expected hooks/post.js, got %q", webhooks[1].Handler)
	}
}

func TestPayment_DSL_Custom_AllOps(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'custom'
    NAME mypay
    CURRENCY XOF
    CHARGE DEFINE
        ENDPOINT "https://api.mypay.com/v1/charges"
        METHOD POST
        HEADER Authorization "Bearer key"
        BODY BEGIN
            append("amount", payment.amount)
        END BODY
        RESPONSE BEGIN
            resolve({ id: response.body.txId, status: "pending" })
        END RESPONSE
    END CHARGE
    VERIFY DEFINE
        ENDPOINT "https://api.mypay.com/v1/charges/{id}"
        METHOD GET
        RESPONSE BEGIN
            resolve({ id: response.body.id, status: response.body.state })
        END RESPONSE
    END VERIFY
    REFUND DEFINE
        ENDPOINT "https://api.mypay.com/v1/refunds"
        METHOD POST
        RESPONSE BEGIN
            resolve({ id: response.body.refundId, status: "refunded" })
        END RESPONSE
    END REFUND
    CHECKOUT DEFINE
        ENDPOINT "https://api.mypay.com/v1/checkout"
        METHOD POST
        RESPONSE BEGIN
            resolve({ redirectUrl: response.body.url, id: response.body.session })
        END RESPONSE
    END CHECKOUT
    USSD DEFINE
        ENDPOINT "https://api.mypay.com/v1/ussd"
        METHOD POST
        RESPONSE BEGIN
            if (response.status !== 202) reject("USSD failed")
            resolve({ id: response.body.ref, status: "pending" })
        END RESPONSE
    END USSD
END PAYMENT
`)
	proto := paymentProto(t, cfg, 0)

	// All 5 ops should be parsed as IsGroup routes
	ops := map[string]bool{}
	for _, r := range proto.Routes {
		if r.IsGroup {
			ops[strings.ToUpper(r.Method)] = true
		}
	}
	for _, want := range []string{"CHARGE", "VERIFY", "REFUND", "CHECKOUT", "USSD"} {
		if !ops[want] {
			t.Errorf("missing op %s", want)
		}
	}
}

func TestPayment_DSL_Custom_HEADER_Variants(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'custom'
    NAME mypay
    CHARGE DEFINE
        ENDPOINT "https://api.example.com/charges"
        HEADER Authorization "Bearer statickey"
        HEADER BEGIN [ts=true]
            append("X-Timestamp", "now")
        END HEADER
        RESPONSE BEGIN
            resolve({ id: "x", status: "pending" })
        END RESPONSE
    END CHARGE
END PAYMENT
`)
	proto := paymentProto(t, cfg, 0)
	var chargeRoutes []*RouteConfig
	for _, r := range proto.Routes {
		if strings.ToUpper(r.Method) == "CHARGE" && r.IsGroup {
			chargeRoutes = r.Routes
			break
		}
	}
	if chargeRoutes == nil {
		t.Fatal("CHARGE group not found")
	}
	headers := routesOfMethod(chargeRoutes, "HEADER")
	if len(headers) != 2 {
		t.Fatalf("expected 2 HEADER routes, got %d", len(headers))
	}
	// First: static
	if headers[0].Inline || headers[0].Path != "Authorization" {
		t.Errorf("expected static HEADER Authorization, got inline=%v path=%q", headers[0].Inline, headers[0].Path)
	}
	// Second: inline
	if !headers[1].Inline {
		t.Error("expected inline HEADER")
	}
	if headers[1].Args.Get("ts") != "true" {
		t.Errorf("expected ts=true, got %s", headers[1].Args.Get("ts"))
	}
}

func TestPayment_DSL_Custom_MissingEndpoint(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'custom'
    NAME mypay
    CHARGE DEFINE
        METHOD POST
        RESPONSE BEGIN
            resolve({ id: "x", status: "ok" })
        END RESPONSE
    END CHARGE
END PAYMENT
`)
	d := &PaymentDirective{config: paymentProto(t, cfg, 0)}
	_, err := d.Start()
	if err == nil || !strings.Contains(err.Error(), "ENDPOINT") {
		t.Errorf("expected missing ENDPOINT error, got: %v", err)
	}
}

func TestPayment_DSL_Custom_MissingResponse(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'custom'
    NAME mypay
    CHARGE DEFINE
        ENDPOINT "https://api.example.com/charges"
        METHOD POST
    END CHARGE
END PAYMENT
`)
	d := &PaymentDirective{config: paymentProto(t, cfg, 0)}
	_, err := d.Start()
	if err == nil || !strings.Contains(err.Error(), "RESPONSE") {
		t.Errorf("expected missing RESPONSE error, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// buildPaymentProvider
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildPaymentProvider_Stripe(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}, Routes: []*RouteConfig{{Method: "MODE", Path: "sandbox"}}}
	p, err := buildPaymentProvider("stripe://sk_test_abc123", cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := p.(*stripeProvider)
	if !ok {
		t.Fatalf("expected *stripeProvider, got %T", p)
	}
	if s.secretKey != "sk_test_abc123" {
		t.Errorf("expected secretKey=sk_test_abc123, got %s", s.secretKey)
	}
	if s.mode != "sandbox" {
		t.Errorf("expected mode=sandbox, got %s", s.mode)
	}
}

func TestBuildPaymentProvider_Flutterwave(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	p, err := buildPaymentProvider("flutterwave://FLWPUBK_TEST-pub:FLWSECK_TEST-sec@api.flutterwave.com", cfg)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := p.(*flutterwaveProvider)
	if !ok {
		t.Fatalf("expected *flutterwaveProvider, got %T", p)
	}
	if f.publicKey != "FLWPUBK_TEST-pub" {
		t.Errorf("unexpected publicKey: %s", f.publicKey)
	}
	if f.secretKey != "FLWSECK_TEST-sec" {
		t.Errorf("unexpected secretKey: %s", f.secretKey)
	}
}

func TestBuildPaymentProvider_CinetPay(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	p, err := buildPaymentProvider("cinetpay://myapikey:mysiteid@api.cinetpay.com", cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := p.(*cinetpayProvider)
	if !ok {
		t.Fatalf("expected *cinetpayProvider, got %T", p)
	}
	if c.apiKey != "myapikey" || c.siteID != "mysiteid" {
		t.Errorf("unexpected cinetpay credentials: %s/%s", c.apiKey, c.siteID)
	}
}

func TestBuildPaymentProvider_MTN(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	p, err := buildPaymentProvider("mtn://subkey:apiuser:apikey@collection.mtn.com/v1_0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := p.(*mtnProvider)
	if !ok {
		t.Fatalf("expected *mtnProvider, got %T", p)
	}
	if m.subscriptionKey != "subkey" {
		t.Errorf("unexpected subscriptionKey: %s", m.subscriptionKey)
	}
	if m.apiUser != "apiuser" || m.apiKey != "apikey" {
		t.Errorf("unexpected apiUser/apiKey: %s/%s", m.apiUser, m.apiKey)
	}
}

func TestBuildPaymentProvider_Orange(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	p, err := buildPaymentProvider("orange://clientid:clientsecret@api.orange.com", cfg)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := p.(*orangeProvider)
	if !ok {
		t.Fatalf("expected *orangeProvider, got %T", p)
	}
	if o.clientID != "clientid" || o.clientSecret != "clientsecret" {
		t.Errorf("unexpected orange credentials: %s/%s", o.clientID, o.clientSecret)
	}
}

func TestBuildPaymentProvider_Airtel(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	p, err := buildPaymentProvider("airtel://clientid:clientsecret@openapi.airtel.africa", cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := p.(*airtelProvider)
	if !ok {
		t.Fatalf("expected *airtelProvider, got %T", p)
	}
	if a.clientID != "clientid" {
		t.Errorf("unexpected airtel clientID: %s", a.clientID)
	}
}

func TestBuildPaymentProvider_Custom(t *testing.T) {
	cfg := &DirectiveConfig{
		Configs: Arguments{},
		BaseDir: t.TempDir(),
		Routes: []*RouteConfig{
			{
				Method: "CHARGE", IsGroup: true,
				Routes: []*RouteConfig{
					{Method: "ENDPOINT", Path: "https://api.example.com/charges"},
					{Method: "RESPONSE", Inline: true, Handler: `resolve({ id: "x", status: "pending" })`},
				},
			},
		},
	}
	p, err := buildPaymentProvider("custom", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*customProvider); !ok {
		t.Fatalf("expected *customProvider, got %T", p)
	}
}

func TestBuildPaymentProvider_UnknownScheme(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	if _, err := buildPaymentProvider("ftp://nope", cfg); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PaymentConnection — defaultRequest
// ─────────────────────────────────────────────────────────────────────────────

func TestPaymentConnection_DefaultRequest_Currency(t *testing.T) {
	conn := freshPaymentConn("test", &mockProvider{})
	conn.currency = "XAF"

	req := conn.defaultRequest(PaymentRequest{Amount: 1000})
	if req.Currency != "XAF" {
		t.Errorf("expected currency=XAF, got %s", req.Currency)
	}

	// Explicit currency in request should not be overridden
	req2 := conn.defaultRequest(PaymentRequest{Amount: 1000, Currency: "EUR"})
	if req2.Currency != "EUR" {
		t.Errorf("explicit currency should not be overridden, got %s", req2.Currency)
	}
}

func TestPaymentConnection_DefaultRequest_Redirects(t *testing.T) {
	conn := freshPaymentConn("test", &mockProvider{})
	conn.redirects.Success = "/ok"
	conn.redirects.Cancel = "/cancel"
	conn.redirects.Failure = "/fail"

	req := conn.defaultRequest(PaymentRequest{})
	if req.Redirects.Success != "/ok" {
		t.Errorf("expected redirect success=/ok, got %s", req.Redirects.Success)
	}

	// Explicit redirects should not be overridden
	req2 := conn.defaultRequest(PaymentRequest{
		Redirects: struct{ Success, Cancel, Failure string }{Success: "/custom"},
	})
	if req2.Redirects.Success != "/custom" {
		t.Errorf("explicit redirect should not be overridden")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PaymentConnection — operations dispatch to provider
// ─────────────────────────────────────────────────────────────────────────────

func TestPaymentConnection_Charge(t *testing.T) {
	mock := &mockProvider{
		chargeResult: PaymentResult{ID: "pi_abc", Status: "pending"},
	}
	conn := freshPaymentConn("test", mock)
	result, err := conn.Charge(PaymentRequest{Amount: 5000, Currency: "XOF"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "pi_abc" || result.Status != "pending" {
		t.Errorf("unexpected result: %+v", result)
	}
	if mock.lastChargeReq.Amount != 5000 {
		t.Errorf("expected amount=5000, got %v", mock.lastChargeReq.Amount)
	}
}

func TestPaymentConnection_Verify(t *testing.T) {
	mock := &mockProvider{
		verifyResult: PaymentResult{ID: "pi_abc", Status: "succeeded"},
	}
	conn := freshPaymentConn("test", mock)
	result, err := conn.Verify("pi_abc")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" {
		t.Errorf("expected status=succeeded, got %s", result.Status)
	}
	if mock.lastVerifyID != "pi_abc" {
		t.Errorf("expected verifyID=pi_abc, got %s", mock.lastVerifyID)
	}
}

func TestPaymentConnection_Refund(t *testing.T) {
	mock := &mockProvider{
		refundResult: PaymentResult{ID: "re_abc", Status: "refunded"},
	}
	conn := freshPaymentConn("test", mock)
	result, err := conn.Refund(PaymentRequest{ID: "pi_abc", Amount: 2500})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "refunded" {
		t.Errorf("expected status=refunded, got %s", result.Status)
	}
}

func TestPaymentConnection_Checkout(t *testing.T) {
	mock := &mockProvider{
		checkoutResult: PaymentResult{ID: "cs_abc", Status: "pending", RedirectURL: "https://checkout.stripe.com/abc"},
	}
	conn := freshPaymentConn("test", mock)
	result, err := conn.Checkout(PaymentRequest{Amount: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if result.RedirectURL == "" {
		t.Error("expected non-empty RedirectURL")
	}
}

func TestPaymentConnection_Push(t *testing.T) {
	mock := &mockProvider{
		pushResult: PaymentResult{ID: "req_abc", Status: "pending"},
	}
	conn := freshPaymentConn("test", mock)
	result, err := conn.Push(PaymentRequest{Phone: "237612345678", Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "pending" {
		t.Errorf("expected status=pending, got %s", result.Status)
	}
	if mock.lastPushReq.Phone != "237612345678" {
		t.Errorf("expected phone=237612345678, got %s", mock.lastPushReq.Phone)
	}
}

// TestPaymentConnection_USSD_BackwardCompat verifies that USSD is an alias for Push.
func TestPaymentConnection_USSD_BackwardCompat(t *testing.T) {
	mock := &mockProvider{
		pushResult: PaymentResult{ID: "req_bc", Status: "pending"},
	}
	conn := freshPaymentConn("test", mock)
	result, err := conn.USSD(PaymentRequest{Phone: "237600000000", Amount: 500})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "req_bc" {
		t.Errorf("expected id=req_bc via USSD alias, got %s", result.ID)
	}
}

func TestPaymentConnection_ProviderError(t *testing.T) {
	mock := &mockProvider{
		chargeErr: fmt.Errorf("network error"),
	}
	conn := freshPaymentConn("test", mock)
	_, err := conn.Charge(PaymentRequest{Amount: 100})
	if err == nil || !strings.Contains(err.Error(), "network error") {
		t.Errorf("expected provider error, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// routeScalar
// ─────────────────────────────────────────────────────────────────────────────

func TestRouteScalar(t *testing.T) {
	routes := []*RouteConfig{
		{Method: "CURRENCY", Path: "XOF"},
		{Method: "MODE", Path: "sandbox"},
	}
	if routeScalar(routes, "CURRENCY", "USD") != "XOF" {
		t.Error("expected CURRENCY=XOF")
	}
	if routeScalar(routes, "MODE", "production") != "sandbox" {
		t.Error("expected MODE=sandbox")
	}
	if routeScalar(routes, "COUNTRY", "CM") != "CM" {
		t.Error("expected fallback CM")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// paymentMapRoute.eval
// ─────────────────────────────────────────────────────────────────────────────

func TestPaymentMapRoute_Static(t *testing.T) {
	mr := paymentMapRoute{
		route:   &RouteConfig{Method: "HEADER", Path: "Authorization", Handler: "Bearer key", Inline: false},
		baseDir: t.TempDir(),
	}
	dst := map[string]string{}
	vm := newTestVM(t)
	setPaymentVar(vm, PaymentRequest{Amount: 100, Currency: "XOF"})
	if err := mr.eval(dst, vm); err != nil {
		t.Fatal(err)
	}
	if dst["Authorization"] != "Bearer key" {
		t.Errorf("expected Authorization=Bearer key, got %s", dst["Authorization"])
	}
}

func TestPaymentMapRoute_Inline(t *testing.T) {
	mr := paymentMapRoute{
		route: &RouteConfig{
			Method:  "BODY",
			Inline:  true,
			Handler: `append("amount", payment.amount.toString()); append("ref", payment.orderId)`,
			Args:    Arguments{},
		},
		baseDir: t.TempDir(),
	}
	dst := map[string]string{}
	vm := newTestVM(t)
	setPaymentVar(vm, PaymentRequest{Amount: 5000, OrderID: "ORD-001"})
	if err := mr.eval(dst, vm); err != nil {
		t.Fatal(err)
	}
	if dst["amount"] != "5000" {
		t.Errorf("expected amount=5000, got %s", dst["amount"])
	}
	if dst["ref"] != "ORD-001" {
		t.Errorf("expected ref=ORD-001, got %s", dst["ref"])
	}
}

func TestPaymentMapRoute_File(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "body.js"), []byte(
		`append("source", args.src); append("phone", payment.phone)`,
	), 0644)

	mr := paymentMapRoute{
		route:   &RouteConfig{Method: "BODY", Inline: false, Handler: "body.js", Args: Arguments{"src": "myapp"}},
		baseDir: dir,
	}
	dst := map[string]string{}
	vm := newTestVM(t)
	setPaymentVar(vm, PaymentRequest{Phone: "237612345678"})
	if err := mr.eval(dst, vm); err != nil {
		t.Fatal(err)
	}
	if dst["source"] != "myapp" || dst["phone"] != "237612345678" {
		t.Errorf("unexpected dst: %v", dst)
	}
}

func TestPaymentMapRoute_FileMissing(t *testing.T) {
	mr := paymentMapRoute{
		route:   &RouteConfig{Method: "BODY", Inline: false, Handler: "missing.js"},
		baseDir: t.TempDir(),
	}
	vm := newTestVM(t)
	setPaymentVar(vm, PaymentRequest{})
	if err := mr.eval(map[string]string{}, vm); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// customProvider.execute — with mock HTTP server
// ─────────────────────────────────────────────────────────────────────────────

func TestCustomProvider_Charge(t *testing.T) {
	srv := mockHTTPServer(t, 200, map[string]any{
		"txId": "custom_abc", "status": "PENDING",
	})

	op := &paymentOp{
		kind:     opCharge,
		endpoint: srv.URL + "/charges",
		method:   "POST",
		bodyRoutes: []paymentMapRoute{{
			route:   &RouteConfig{Method: "BODY", Inline: true, Handler: `append("amount", payment.amount.toString())`},
			baseDir: t.TempDir(),
		}},
		responseRoute: &paymentRoute{
			route: &RouteConfig{
				Method:  "RESPONSE",
				Inline:  true,
				Handler: `resolve({ id: response.body.txId, status: "pending" })`,
			},
			baseDir: t.TempDir(),
		},
		baseDir: t.TempDir(),
	}

	p := &customProvider{
		ops:     map[paymentOpKind]*paymentOp{opCharge: op},
		baseDir: t.TempDir(),
	}

	result, err := p.Charge(PaymentRequest{Amount: 5000, Currency: "XOF"})
	if err != nil {
		t.Fatalf("Charge failed: %v", err)
	}
	if result.ID != "custom_abc" {
		t.Errorf("expected id=custom_abc, got %s", result.ID)
	}
	if result.Status != "pending" {
		t.Errorf("expected status=pending, got %s", result.Status)
	}
}

func TestCustomProvider_Verify_IDPlaceholder(t *testing.T) {
	capturedPath := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "txn_xyz", "state": "SUCCESSFUL"})
	}))
	defer srv.Close()

	op := &paymentOp{
		kind:     opVerify,
		endpoint: srv.URL + "/charges/{id}",
		method:   "GET",
		responseRoute: &paymentRoute{
			route: &RouteConfig{
				Method:  "RESPONSE",
				Inline:  true,
				Handler: `resolve({ id: response.body.id, status: response.body.state === "SUCCESSFUL" ? "succeeded" : "pending" })`,
			},
			baseDir: t.TempDir(),
		},
		baseDir: t.TempDir(),
	}

	p := &customProvider{
		ops:     map[paymentOpKind]*paymentOp{opVerify: op},
		baseDir: t.TempDir(),
	}

	result, err := p.Verify("txn_xyz")
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if !strings.HasSuffix(capturedPath, "/txn_xyz") {
		t.Errorf("expected {id} replaced in URL, path=%s", capturedPath)
	}
	if result.Status != "succeeded" {
		t.Errorf("expected status=succeeded, got %s", result.Status)
	}
}

func TestCustomProvider_Response_Reject(t *testing.T) {
	srv := mockHTTPServer(t, 400, map[string]any{"message": "invalid amount"})

	op := &paymentOp{
		kind:     opCharge,
		endpoint: srv.URL + "/charges",
		method:   "POST",
		responseRoute: &paymentRoute{
			route: &RouteConfig{
				Method:  "RESPONSE",
				Inline:  true,
				Handler: `if (response.status !== 200) reject(response.body.message)`,
			},
			baseDir: t.TempDir(),
		},
		baseDir: t.TempDir(),
	}

	p := &customProvider{
		ops:     map[paymentOpKind]*paymentOp{opCharge: op},
		baseDir: t.TempDir(),
	}

	_, err := p.Charge(PaymentRequest{Amount: -1})
	if err == nil || !strings.Contains(err.Error(), "invalid amount") {
		t.Errorf("expected rejection with 'invalid amount', got: %v", err)
	}
}

func TestCustomProvider_Response_NoResolve(t *testing.T) {
	srv := mockHTTPServer(t, 200, map[string]any{"txId": "x"})

	op := &paymentOp{
		kind:     opCharge,
		endpoint: srv.URL + "/charges",
		method:   "POST",
		responseRoute: &paymentRoute{
			route: &RouteConfig{
				Method:  "RESPONSE",
				Inline:  true,
				Handler: `// intentionally does not call resolve()`,
			},
			baseDir: t.TempDir(),
		},
		baseDir: t.TempDir(),
	}

	p := &customProvider{
		ops:     map[paymentOpKind]*paymentOp{opCharge: op},
		baseDir: t.TempDir(),
	}

	_, err := p.Charge(PaymentRequest{})
	if err == nil || !strings.Contains(err.Error(), "resolve()") {
		t.Errorf("expected missing resolve() error, got: %v", err)
	}
}

func TestCustomProvider_OpNotConfigured(t *testing.T) {
	p := &customProvider{ops: make(map[paymentOpKind]*paymentOp)}
	_, err := p.Charge(PaymentRequest{})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected not-configured error, got: %v", err)
	}
}

func TestCustomProvider_Query_AppendedToURL(t *testing.T) {
	capturedQuery := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "x"})
	}))
	defer srv.Close()

	op := &paymentOp{
		kind:     opCharge,
		endpoint: srv.URL + "/charges",
		method:   "POST",
		queryRoutes: []paymentMapRoute{{
			route:   &RouteConfig{Method: "QUERY", Path: "version", Handler: "v2", Inline: false},
			baseDir: t.TempDir(),
		}},
		responseRoute: &paymentRoute{
			route: &RouteConfig{
				Method:  "RESPONSE",
				Inline:  true,
				Handler: `resolve({ id: response.body.id, status: "pending" })`,
			},
			baseDir: t.TempDir(),
		},
		baseDir: t.TempDir(),
	}

	p := &customProvider{
		ops:     map[paymentOpKind]*paymentOp{opCharge: op},
		baseDir: t.TempDir(),
	}

	p.Charge(PaymentRequest{})
	if !strings.Contains(capturedQuery, "version=v2") {
		t.Errorf("expected version=v2 in query, got %q", capturedQuery)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// normalizeStripeStatus
// ─────────────────────────────────────────────────────────────────────────────

func TestNormalizeStripeStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"succeeded", "succeeded"},
		{"canceled", "failed"},
		{"requires_payment_method", "pending"},
		{"processing", "pending"},
		{"", "pending"},
	}
	for _, tc := range cases {
		got := normalizeStripeStatus(tc.in)
		if got != tc.want {
			t.Errorf("normalizeStripeStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// normalizeFlwStatus
// ─────────────────────────────────────────────────────────────────────────────

func TestNormalizeFlwStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"successful", "succeeded"},
		{"SUCCESSFUL", "succeeded"},
		{"failed", "failed"},
		{"FAILED", "failed"},
		{"pending", "pending"},
		{"NEW", "pending"},
	}
	for _, tc := range cases {
		got := normalizeFlwStatus(tc.in)
		if got != tc.want {
			t.Errorf("normalizeFlwStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// payStr / payFloat / payMap
// ─────────────────────────────────────────────────────────────────────────────

func TestPayHelpers(t *testing.T) {
	m := map[string]any{
		"id":     "abc",
		"amount": float64(5000),
		"nested": map[string]any{"key": "val"},
	}

	if payStr(m, "id") != "abc" {
		t.Error("payStr id")
	}
	if payStr(m, "missing") != "" {
		t.Error("payStr missing should be empty")
	}
	if payStr(nil, "id") != "" {
		t.Error("payStr nil map")
	}
	if payFloat(m, "amount") != 5000 {
		t.Error("payFloat amount")
	}
	if payFloat(m, "missing") != 0 {
		t.Error("payFloat missing should be 0")
	}
	if payMap(m, "nested") == nil {
		t.Error("payMap nested")
	}
	if payMap(m, "id") != nil {
		t.Error("payMap non-map key should be nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// paymentResultThenable
// ─────────────────────────────────────────────────────────────────────────────

func TestPaymentResultThenable_Success(t *testing.T) {
	vm := newTestVM(t)
	result := PaymentResult{ID: "pi_abc", Status: "succeeded", Amount: 5000, Currency: "XOF"}
	obj := paymentResultThenable(vm, result, nil)

	// Verify via RunString
	vm.Set("result", obj)
	v, err := vm.RunString(`result.ok`)
	if err != nil {
		t.Fatal(err)
	}
	if !v.ToBoolean() {
		t.Error("expected result.ok=true")
	}

	// Verify result fields
	v2, err := vm.RunString(`result.result.id`)
	if err != nil {
		t.Fatal(err)
	}
	if v2.String() != "pi_abc" {
		t.Errorf("expected result.result.id=pi_abc, got %s", v2.String())
	}
}

func TestPaymentResultThenable_Error(t *testing.T) {
	vm := newTestVM(t)
	opErr := fmt.Errorf("provider down")
	obj := paymentResultThenable(vm, PaymentResult{}, opErr)

	vm.Set("result", obj)
	v, err := vm.RunString(`result.ok`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToBoolean() {
		t.Error("expected ok=false on error")
	}

	v2, err := vm.RunString(`result.error()`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v2.String(), "provider down") {
		t.Errorf("expected error message, got %s", v2.String())
	}
}

func TestPaymentResultThenable_ThenOnSuccess(t *testing.T) {
	vm := newTestVM(t)
	result := PaymentResult{ID: "pi_abc", Status: "succeeded"}
	obj := paymentResultThenable(vm, result, nil)
	vm.Set("result", obj)

	got, err := vm.RunString(`
		var captured = null;
		result.then(function(r) { captured = r.id }, function(e) {});
		captured
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "pi_abc" {
		t.Errorf("expected pi_abc in then callback, got %s", got.String())
	}
}

func TestPaymentResultThenable_CatchOnError(t *testing.T) {
	vm := newTestVM(t)
	obj := paymentResultThenable(vm, PaymentResult{}, fmt.Errorf("bad gateway"))
	vm.Set("result", obj)

	got, err := vm.RunString(`
		var captured = null;
		result.catch(function(e) { captured = e });
		captured
	`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.String(), "bad gateway") {
		t.Errorf("expected 'bad gateway' in catch, got %s", got.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// paymentRequestFromJS
// ─────────────────────────────────────────────────────────────────────────────

func TestPaymentRequestFromJS(t *testing.T) {
	conn := freshPaymentConn("test", &mockProvider{})
	conn.currency = "XOF"
	conn.redirects.Success = "/ok"
	conn.redirects.Cancel = "/cancel"

	m := map[string]interface{}{
		"amount":   float64(5000),
		"currency": "EUR",
		"phone":    "237612345678",
		"email":    "u@e.com",
		"orderId":  "ORD-001",
		"metadata": map[string]interface{}{"desc": "Product"},
	}

	req := paymentRequestFromJS(m, conn)

	if req.Amount != 5000 {
		t.Errorf("expected amount=5000, got %v", req.Amount)
	}
	if req.Currency != "EUR" {
		t.Errorf("expected currency=EUR (explicit), got %s", req.Currency)
	}
	if req.Phone != "237612345678" {
		t.Errorf("expected phone=237612345678, got %s", req.Phone)
	}
	if req.Redirects.Success != "/ok" {
		t.Errorf("expected redirect success=/ok, got %s", req.Redirects.Success)
	}
	if req.Metadata["desc"] != "Product" {
		t.Errorf("expected metadata.desc=Product")
	}
}

func TestPaymentRequestFromJS_DefaultCurrency(t *testing.T) {
	conn := freshPaymentConn("test", &mockProvider{})
	conn.currency = "XAF"

	req := paymentRequestFromJS(map[string]interface{}{"amount": float64(1000)}, conn)
	if req.Currency != "XAF" {
		t.Errorf("expected default currency XAF, got %s", req.Currency)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Global connection registry
// ─────────────────────────────────────────────────────────────────────────────

func TestGetPaymentConnection(t *testing.T) {
	// Reset registry for this test
	oldConns := paymentConns
	oldDefault := defaultPaymentConn
	defer func() {
		paymentConns = oldConns
		defaultPaymentConn = oldDefault
	}()

	paymentConns = make(map[string]*PaymentConnection)
	defaultPaymentConn = nil

	conn1 := freshPaymentConn("stripe", &mockProvider{})
	conn2 := freshPaymentConn("mtn", &mockProvider{})

	registerPaymentConnection("stripe", conn1, true)
	registerPaymentConnection("mtn", conn2, false)

	if GetPaymentConnection() != conn1 {
		t.Error("default connection should be stripe")
	}
	if GetPaymentConnection("mtn") != conn2 {
		t.Error("named connection should be mtn")
	}
	if GetPaymentConnection("ghost") != nil {
		t.Error("unknown connection should be nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Multiple providers coexistence
// ─────────────────────────────────────────────────────────────────────────────

func TestPayment_MultipleProviders_DSL(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'stripe://sk_test_key' [default]
    NAME stripe
END PAYMENT

PAYMENT 'custom'
    NAME mypay
    CHARGE DEFINE
        ENDPOINT "https://api.example.com/charges"
        RESPONSE BEGIN
            resolve({ id: "x", status: "pending" })
        END RESPONSE
    END CHARGE
END PAYMENT
`)
	if len(cfg.Groups) != 2 {
		t.Fatalf("expected 2 PAYMENT groups, got %d", len(cfg.Groups))
	}
	n0 := routeScalarFromRoutes(cfg.Groups[0].Items[0].Routes, "NAME")
	n1 := routeScalarFromRoutes(cfg.Groups[1].Items[0].Routes, "NAME")
	if n0 != "stripe" {
		t.Errorf("expected NAME=stripe, got %s", n0)
	}
	if n1 != "mypay" {
		t.Errorf("expected NAME=mypay, got %s", n1)
	}
}

func TestPayment_CoexistWithHTTP(t *testing.T) {
	cfg := parsePaymentBind(t, `
PAYMENT 'stripe://sk_test_key'
    NAME stripe
END PAYMENT

HTTP :8080
    GET /ping BEGIN
        "pong"
    END GET
END HTTP
`)
	if len(cfg.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(cfg.Groups))
	}
	if cfg.Groups[0].Directive != "PAYMENT" {
		t.Errorf("expected first group PAYMENT, got %s", cfg.Groups[0].Directive)
	}
	if cfg.Groups[1].Directive != "HTTP" {
		t.Errorf("expected second group HTTP, got %s", cfg.Groups[1].Directive)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// setPaymentVar
// ─────────────────────────────────────────────────────────────────────────────

func TestSetPaymentVar(t *testing.T) {
	vm := newTestVM(t)
	setPaymentVar(vm, PaymentRequest{
		Amount:   5000,
		Currency: "XOF",
		Phone:    "237612345678",
		Email:    "u@e.com",
		OrderID:  "ORD-001",
		ID:       "pi_abc",
		Reason:   "customer_request",
		Metadata: map[string]any{"desc": "Product"},
		Redirects: struct{ Success, Cancel, Failure string }{
			Success: "/ok", Cancel: "/cancel", Failure: "/fail",
		},
	})

	cases := []struct{ expr, want string }{
		{`payment.amount`, "5000"},
		{`payment.currency`, "XOF"},
		{`payment.phone`, "237612345678"},
		{`payment.email`, "u@e.com"},
		{`payment.orderId`, "ORD-001"},
		{`payment.id`, "pi_abc"},
		{`payment.reason`, "customer_request"},
		{`payment.metadata.desc`, "Product"},
		{`payment.redirects.success`, "/ok"},
		{`payment.redirects.cancel`, "/cancel"},
		{`payment.redirects.failure`, "/fail"},
	}

	for _, tc := range cases {
		v, err := vm.RunString(tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if v.String() != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, v.String(), tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// routeScalarFromRoutes is a test-local helper (routeScalar is already in the package).
func routeScalarFromRoutes(routes []*RouteConfig, method string) string {
	return routeScalar(routes, method, "")
}

// routesOfMethod returns all RouteConfigs matching method (case-insensitive).
func routesOfMethod(routes []*RouteConfig, method string) []*RouteConfig {
	var out []*RouteConfig
	for _, r := range routes {
		if strings.EqualFold(r.Method, method) {
			out = append(out, r)
		}
	}
	return out
}

// newTestVM creates a processor VM for unit tests.
func newTestVM(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := processor.New(t.TempDir(), nil, nil)
	return vm.Runtime
}
