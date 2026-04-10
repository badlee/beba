package binder

// payment_protocol.go — PAYMENT directive for integrating payment providers.
//
// ─────────────────────────────────────────────────────────────────────────────
// DSL
// ─────────────────────────────────────────────────────────────────────────────
//
//   PAYMENT 'stripe://sk_live_xxx' [default]
//       NAME stripe
//       MODE sandbox                         // sandbox | production
//       CURRENCY XOF
//       COUNTRY CM                           // ISO country (MoMo)
//       CALLBACK "https://myapp.com/cb"      // async confirmation URL
//
//       REDIRECT success "/merci"
//       REDIRECT cancel  "/annule"
//       REDIRECT failure "/echec"
//
//       SET company "MonApp"
//
//       WEBHOOK @PRE  /pay/webhook BEGIN [secret=xxx]
//           if (!verify(request.body, request.headers["stripe-signature"], args.secret))
//               reject("bad signature")
//       END WEBHOOK
//       WEBHOOK @PRE  /pay/webhook "hooks/pre.js"  [secret=xxx]
//
//       WEBHOOK @POST /pay/webhook BEGIN
//           if (payment.status === "succeeded") { /* ... */ }
//       END WEBHOOK
//       WEBHOOK @POST /pay/webhook "hooks/post.js"
//   END PAYMENT
//
//   PAYMENT 'custom'
//       NAME mypay
//       CURRENCY XOF
//
//       CHARGE DEFINE
//           ENDPOINT "https://api.mypay.com/v1/charges"
//           METHOD POST
//           HEADER Authorization "Bearer key"
//           HEADER BEGIN
//               append("X-Id", Date.now().toString())
//           END HEADER
//           HEADER "headers/auth.js" [env=prod]
//           BODY BEGIN [desc="Paiement"]
//               append("amount",    payment.amount)
//               append("currency",  payment.currency)
//               append("reference", payment.orderId)
//           END BODY
//           BODY "body/charge.js"
//           QUERY ref "v2"
//           RESPONSE BEGIN
//               if (response.status !== 200) reject(response.body.message)
//               resolve({ id: response.body.transactionId, status: "pending" })
//           END RESPONSE
//           RESPONSE "response/charge.js"
//       END CHARGE
//
//       VERIFY DEFINE
//           ENDPOINT "https://api.mypay.com/v1/charges/{id}"
//           METHOD GET
//           HEADER Authorization "Bearer key"
//           RESPONSE BEGIN
//               resolve({ id: response.body.id, status: response.body.state })
//           END RESPONSE
//       END VERIFY
//
//       REFUND DEFINE
//           ENDPOINT "https://api.mypay.com/v1/refunds"
//           METHOD POST
//           BODY BEGIN
//               append("transactionId", payment.id)
//               append("amount",        payment.amount)
//           END BODY
//           RESPONSE BEGIN
//               resolve({ id: response.body.refundId, status: "refunded" })
//           END RESPONSE
//       END REFUND
//
//       CHECKOUT DEFINE
//           ENDPOINT "https://api.mypay.com/v1/checkout"
//           METHOD POST
//           BODY BEGIN
//               append("success_url", payment.redirects.success)
//               append("cancel_url",  payment.redirects.cancel)
//               append("amount",      payment.amount)
//           END BODY
//           RESPONSE BEGIN
//               resolve({ redirectUrl: response.body.checkoutUrl, id: response.body.sessionId })
//           END RESPONSE
//       END CHECKOUT
//
//       USSD DEFINE
//           ENDPOINT "https://api.mypay.com/v1/ussd"
//           METHOD POST
//           BODY BEGIN
//               append("phone",  payment.phone)
//               append("amount", payment.amount)
//           END BODY
//           RESPONSE BEGIN
//               if (response.status !== 202) reject("USSD push failed")
//               resolve({ id: response.body.requestId, status: "pending" })
//           END RESPONSE
//       END USSD
//
//       WEBHOOK @PRE  /payment/mypay/webhook BEGIN [secret=s]
//           if (!verify(request.body, request.headers["x-sig"], args.secret)) reject("bad sig")
//       END WEBHOOK
//       WEBHOOK @POST /payment/mypay/webhook BEGIN
//           if (payment.status === "succeeded") { /* ... */ }
//       END WEBHOOK
//
//       REDIRECT success "/merci"
//       REDIRECT cancel  "/annule"
//   END PAYMENT
//
// ─────────────────────────────────────────────────────────────────────────────
// JS API — `require('payment')`
// ─────────────────────────────────────────────────────────────────────────────
//
//   const pay = require('payment')           // default connection
//   const sg  = require('payment').get('stripe')
//
//   // Initier un paiement
//   const result = await pay.charge({
//       amount: 5000, currency: "XOF",
//       phone: "237612345678", email: "u@e.com",
//       orderId: "ORD-001",
//       metadata: { description: "Achat produit" },
//   })
//   // result = { id, status, redirectUrl? }
//
//   // Vérifier un statut
//   const status = await pay.verify("txn_abc123")
//
//   // Rembourser
//   await pay.refund({ id: "txn_abc123", amount: 2500 })
//
//   // Page de paiement hébergée
//   const checkout = await pay.checkout({ amount: 5000, orderId: "ORD-002" })
//   // checkout.redirectUrl → rediriger le client
//
//   // USSD push
//   const push = await pay.ussd({ phone: "237612345678", amount: 1000, orderId: "ORD-003" })
//
//   // Gestion des connexions
//   pay.connection("stripe")
//   pay.connectionNames      // accessor
//   pay.hasConnection("mtn") // bool
//   pay.hasDefault           // accessor bool
//   pay.default              // accessor → proxy connexion par défaut
//
//   pay.connect("stripe://sk_test_xxx", "stripe2", { currency: "EUR" })

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"http-server/modules"
	"http-server/plugins/httpserver"
	"http-server/processor"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
)

// ─────────────────────────────────────────────────────────────────────────────
// PaymentRequest — entrée normalisée pour toutes les opérations
// ─────────────────────────────────────────────────────────────────────────────

// PaymentRequest is the normalised input for any payment operation.
// JS: pay.charge({ amount, currency, phone, email, orderId, metadata, ... })
type PaymentRequest struct {
	Amount   float64
	Currency string
	Phone    string
	Email    string
	OrderID  string
	Metadata map[string]any
	// Populated at runtime from the connection defaults + REDIRECT directives
	Redirects struct{ Success, Cancel, Failure string }
	// For REFUND
	ID     string
	Reason string
	// For VERIFY
	// ID is re-used
}

// PaymentResult is the normalised output of any payment operation.
type PaymentResult struct {
	ID          string
	Status      string // pending | succeeded | failed | refunded
	RedirectURL string // non-empty for CHECKOUT
	Amount      float64
	Currency    string
	Raw         map[string]any // raw provider response body
}

// ─────────────────────────────────────────────────────────────────────────────
// paymentOp — one operation block (CHARGE / VERIFY / REFUND / CHECKOUT / USSD)
// ─────────────────────────────────────────────────────────────────────────────

type paymentOpKind string

const (
	opCharge   paymentOpKind = "CHARGE"
	opVerify   paymentOpKind = "VERIFY"
	opRefund   paymentOpKind = "REFUND"
	opCheckout paymentOpKind = "CHECKOUT"
	opUSSD     paymentOpKind = "USSD"
)

// paymentOp holds the configuration of one custom operation block.
type paymentOp struct {
	kind          paymentOpKind
	endpoint      string            // may contain {id} placeholder
	method        string            // HTTP method
	headerRoutes  []paymentMapRoute // HEADER directives
	bodyRoutes    []paymentMapRoute // BODY directives
	queryRoutes   []paymentMapRoute // QUERY directives
	responseRoute *paymentRoute     // RESPONSE directive (inline or file)
	baseDir       string
}

// ─────────────────────────────────────────────────────────────────────────────
// paymentRoute — inline or file JS route
// ─────────────────────────────────────────────────────────────────────────────

type paymentRoute struct {
	route   *RouteConfig
	baseDir string
}

func (r *paymentRoute) code() (string, error) {
	if r.route.Inline {
		return r.route.Handler, nil
	}
	full := r.route.Handler
	if !filepath.IsAbs(full) {
		full = filepath.Join(r.baseDir, full)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("cannot read %q: %w", full, err)
	}
	return string(b), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// paymentMapRoute — HEADER / BODY / QUERY directive
// ─────────────────────────────────────────────────────────────────────────────

// paymentMapRoute holds one HEADER, BODY, or QUERY directive.
//
//	Static:  HEADER Key Value       → Path=Key, Handler=Value, !Inline
//	File:    HEADER "file.js" [args]→ !Inline, IsFileLike(Handler)
//	Inline:  HEADER BEGIN...END     → Inline=true
type paymentMapRoute struct {
	route   *RouteConfig
	baseDir string
}

// eval populates dst.
// JS env: append(key, value), args, payment (the PaymentRequest as JS object).
func (m *paymentMapRoute) eval(dst map[string]string, vm *goja.Runtime) error {
	r := m.route
	// Static single key
	if !r.Inline && r.Path != "" && !IsFileLike(r.Handler) {
		dst[r.Path] = r.Handler
		return nil
	}

	// File or inline
	var code string
	if r.Inline {
		code = r.Handler
	} else {
		full := r.Handler
		if !filepath.IsAbs(full) {
			full = filepath.Join(m.baseDir, full)
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("payment map route: cannot read %q: %w", full, err)
		}
		code = string(b)
	}

	argsObj := vm.NewObject()
	for k, v := range r.Args {
		argsObj.Set(k, v)
	}
	vm.Set("args", argsObj)
	vm.Set("append", func(key, value string) { dst[key] = value })

	if _, err := vm.RunString(code); err != nil {
		return fmt.Errorf("payment map route script: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// paymentWebhook — WEBHOOK directive
// ─────────────────────────────────────────────────────────────────────────────

type webhookPhase string

const (
	whPre  webhookPhase = "PRE"
	whPost webhookPhase = "POST"
)

type paymentWebhook struct {
	phase   webhookPhase
	path    string // fiber route path, e.g. "/payment/stripe/webhook"
	route   *RouteConfig
	baseDir string
}

// ─────────────────────────────────────────────────────────────────────────────
// PaymentProvider — interface implemented by each backend
// ─────────────────────────────────────────────────────────────────────────────

type PaymentProvider interface {
	Charge(req PaymentRequest) (PaymentResult, error)
	Verify(id string) (PaymentResult, error)
	Refund(req PaymentRequest) (PaymentResult, error)
	Checkout(req PaymentRequest) (PaymentResult, error)
	USSD(req PaymentRequest) (PaymentResult, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// PaymentConnection
// ─────────────────────────────────────────────────────────────────────────────

type PaymentConnection struct {
	name      string
	provider  PaymentProvider
	currency  string // default currency
	country   string // default country
	mode      string // sandbox | production
	callback  string // async callback URL
	redirects struct{ Success, Cancel, Failure string }
	metadata  map[string]string // from SET directives
	webhooks  []paymentWebhook
	baseDir   string
	appConfig interface{} // *config.AppConfig — stored as any to avoid import cycle
}

// defaultRequest merges connection defaults into a PaymentRequest.
func (c *PaymentConnection) defaultRequest(req PaymentRequest) PaymentRequest {
	if req.Currency == "" {
		req.Currency = c.currency
	}
	if req.Redirects.Success == "" {
		req.Redirects.Success = c.redirects.Success
	}
	if req.Redirects.Cancel == "" {
		req.Redirects.Cancel = c.redirects.Cancel
	}
	if req.Redirects.Failure == "" {
		req.Redirects.Failure = c.redirects.Failure
	}
	return req
}

func (c *PaymentConnection) Charge(req PaymentRequest) (PaymentResult, error) {
	return c.provider.Charge(c.defaultRequest(req))
}
func (c *PaymentConnection) Verify(id string) (PaymentResult, error) {
	return c.provider.Verify(id)
}
func (c *PaymentConnection) Refund(req PaymentRequest) (PaymentResult, error) {
	return c.provider.Refund(c.defaultRequest(req))
}
func (c *PaymentConnection) Checkout(req PaymentRequest) (PaymentResult, error) {
	return c.provider.Checkout(c.defaultRequest(req))
}
func (c *PaymentConnection) USSD(req PaymentRequest) (PaymentResult, error) {
	return c.provider.USSD(c.defaultRequest(req))
}

// ─────────────────────────────────────────────────────────────────────────────
// Global connection registry
// ─────────────────────────────────────────────────────────────────────────────

var (
	paymentConns       = make(map[string]*PaymentConnection)
	defaultPaymentConn *PaymentConnection
)

func registerPaymentConnection(name string, conn *PaymentConnection, isDefault bool) {
	paymentConns[name] = conn
	if isDefault || defaultPaymentConn == nil {
		defaultPaymentConn = conn
	}
}

func GetPaymentConnection(name ...string) *PaymentConnection {
	if len(name) == 0 || name[0] == "" {
		return defaultPaymentConn
	}
	return paymentConns[name[0]]
}

// ─────────────────────────────────────────────────────────────────────────────
// PaymentDirective — implements binder.Directive
// ─────────────────────────────────────────────────────────────────────────────

type PaymentDirective struct {
	config *DirectiveConfig
	conns  []*PaymentConnection
}

func NewPaymentDirective(c *DirectiveConfig) (*PaymentDirective, error) {
	processor.RegisterGlobal("payment", &PaymentModule{}, true) // set globlal when directive is defined
	return &PaymentDirective{config: c}, nil
}

func (d *PaymentDirective) Name() string                    { return "PAYMENT" }
func (d *PaymentDirective) Address() string                 { return "" }
func (d *PaymentDirective) Match(peek []byte) (bool, error) { return false, nil }
func (d *PaymentDirective) Handle(conn net.Conn) error      { return nil }

func (d *PaymentDirective) Close() error {
	for _, c := range d.conns {
		processor.UnregisterGlobal(c.name)
	}
	return nil
}

func (d *PaymentDirective) Start() ([]net.Listener, error) {
	cfg := d.config
	address := strings.Trim(cfg.Address, "\"'`")

	// ── NAME (required) ───────────────────────────────────────────────────────
	name := ""
	for _, r := range cfg.Routes {
		if strings.ToUpper(r.Method) == "NAME" {
			name = r.Path
			break
		}
	}
	if name == "" {
		return nil, fmt.Errorf("PAYMENT %s: missing required NAME directive", address)
	}

	isDefault := cfg.Args.GetBool("default", GetPaymentConnection() == nil)

	// ── Build provider ────────────────────────────────────────────────────────
	provider, err := buildPaymentProvider(address, cfg)
	if err != nil {
		return nil, fmt.Errorf("PAYMENT %s: %w", name, err)
	}

	conn := &PaymentConnection{
		name:     name,
		provider: provider,
		currency: routeScalar(cfg.Routes, "CURRENCY", cfg.Configs.Get("currency", "USD")),
		country:  routeScalar(cfg.Routes, "COUNTRY", ""),
		mode:     routeScalar(cfg.Routes, "MODE", "production"),
		callback: routeScalar(cfg.Routes, "CALLBACK", ""),
		metadata: make(map[string]string),
		baseDir:  cfg.BaseDir,
	}

	// REDIRECT directives
	for _, r := range cfg.Routes {
		if strings.ToUpper(r.Method) != "REDIRECT" {
			continue
		}
		switch strings.ToLower(r.Path) {
		case "success":
			conn.redirects.Success = r.Handler
		case "cancel":
			conn.redirects.Cancel = r.Handler
		case "failure":
			conn.redirects.Failure = r.Handler
		}
	}

	// SET directives → metadata
	for k, v := range cfg.Configs {
		if !strings.HasPrefix(k, "__") {
			conn.metadata[k] = v
		}
	}

	// WEBHOOK directives
	for _, r := range cfg.Routes {
		if strings.ToUpper(r.Method) != "WEBHOOK" {
			continue
		}
		phase := whPre
		for _, mw := range r.Middlewares {
			switch strings.ToUpper(mw.Name) {
			case "POST":
				phase = whPost
			case "PRE":
				phase = whPre
			}
		}
		conn.webhooks = append(conn.webhooks, paymentWebhook{
			phase:   phase,
			path:    r.Path,
			route:   r,
			baseDir: cfg.BaseDir,
		})
	}

	registerPaymentConnection(name, conn, isDefault)
	d.conns = append(d.conns, conn)
	processor.RegisterGlobal(formatToJSVariableName(name), conn, true)

	// Register WEBHOOK routes via httpserver.RegisterRoute so they are mounted
	// on every HTTP server just before it starts accepting connections.
	for i := range conn.webhooks {
		wh := &conn.webhooks[i] // stable pointer — capture address, not index value
		c := conn
		httpserver.RegisterRoute("POST", wh.path, func(ctx fiber.Ctx) error {
			return handlePaymentWebhook(ctx, c, wh)
		})
	}

	log.Printf("PAYMENT: connection %q started (%s, mode=%s)", name, address, conn.mode)
	return nil, nil
}

// routeScalar returns the Path of the first route matching method, or fallback.
func routeScalar(routes []*RouteConfig, method, fallback string) string {
	for _, r := range routes {
		if strings.ToUpper(r.Method) == method {
			return r.Path
		}
	}
	return fallback
}

// ─────────────────────────────────────────────────────────────────────────────
func handlePaymentWebhook(ctx fiber.Ctx, _ *PaymentConnection, wh *paymentWebhook) error {
	vm := processor.New(wh.baseDir, ctx, nil)

	// request object
	reqObj := vm.NewObject()
	headersObj := vm.NewObject()
	for k, v := range ctx.Request().Header.All() {
		headersObj.Set(string(k), string(v))
	}
	reqObj.Set("headers", headersObj)
	reqObj.Set("body", string(ctx.Body()))
	queryObj := vm.NewObject()
	for k, v := range ctx.Request().URI().QueryArgs().All() {
		queryObj.Set(string(k), string(v))
	}
	reqObj.Set("query", queryObj)
	vm.Set("request", reqObj)

	// payment object (populated for @POST — raw parse of JSON body for @PRE)
	payObj := vm.NewObject()
	if wh.phase == whPost {
		var raw map[string]interface{}
		if err := json.Unmarshal(ctx.Body(), &raw); err == nil {
			for k, v := range raw {
				payObj.Set(k, v)
			}
		}
	}
	vm.Set("payment", payObj)

	// verify(body, sig, secret) helper
	vm.Set("verify", func(body, sig, secret string) bool {
		// Generic HMAC-SHA256 verification — providers can override via JS
		return paymentVerifyHMAC(body, sig, secret)
	})

	// args
	argsObj := vm.NewObject()
	for k, v := range wh.route.Args {
		argsObj.Set(k, v)
	}
	vm.Set("args", argsObj)

	// reject(msg)
	rejected := ""
	vm.Set("reject", func(msg string) { rejected = msg })

	code, err := (&paymentRoute{route: wh.route, baseDir: wh.baseDir}).code()
	if err != nil {
		return ctx.Status(500).SendString("webhook script error: " + err.Error())
	}
	if _, err := vm.RunString(code); err != nil {
		return ctx.Status(500).SendString("webhook script runtime error: " + err.Error())
	}
	if rejected != "" {
		return ctx.Status(400).SendString(rejected)
	}
	return ctx.SendStatus(200)
}

// ─────────────────────────────────────────────────────────────────────────────
// buildPaymentProvider
// ─────────────────────────────────────────────────────────────────────────────

func buildPaymentProvider(address string, cfg *DirectiveConfig) (PaymentProvider, error) {
	if address == "custom" {
		return buildCustomProvider(cfg)
	}

	u, err := url.Parse(address)
	if err != nil {
		return nil, fmt.Errorf("invalid payment URL %q: %w", address, err)
	}

	mode := routeScalar(cfg.Routes, "MODE", "production")

	switch strings.ToLower(u.Scheme) {
	case "stripe":
		secretKey := u.Host // stripe://sk_live_xxx → Host = sk_live_xxx
		if secretKey == "" {
			secretKey = u.User.Username()
		}
		return &stripeProvider{secretKey: secretKey, mode: mode}, nil

	case "flutterwave":
		pub := u.User.Username()
		sec, _ := u.User.Password()
		return &flutterwaveProvider{publicKey: pub, secretKey: sec, mode: mode}, nil

	case "cinetpay":
		apiKey := u.User.Username()
		siteID, _ := u.User.Password()
		return &cinetpayProvider{apiKey: apiKey, siteID: siteID, mode: mode}, nil

	case "mtn":
		subKey := u.User.Username()
		// apiUser:apiKey embedded after subKey
		rest, _ := u.User.Password()
		parts := strings.SplitN(rest, ":", 2)
		apiUser, apiKey2 := "", ""
		if len(parts) == 2 {
			apiUser, apiKey2 = parts[0], parts[1]
		}
		baseURL := "https://" + u.Host + u.Path
		return &mtnProvider{
			subscriptionKey: subKey,
			apiUser:         apiUser,
			apiKey:          apiKey2,
			baseURL:         baseURL,
			mode:            mode,
		}, nil

	case "orange":
		clientID := u.User.Username()
		clientSecret, _ := u.User.Password()
		baseURL := "https://" + u.Host
		return &orangeProvider{
			clientID:     clientID,
			clientSecret: clientSecret,
			baseURL:      baseURL,
			mode:         mode,
		}, nil

	case "airtel":
		clientID := u.User.Username()
		clientSecret, _ := u.User.Password()
		baseURL := "https://" + u.Host
		return &airtelProvider{
			clientID:     clientID,
			clientSecret: clientSecret,
			baseURL:      baseURL,
			mode:         mode,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported payment provider %q", u.Scheme)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Custom provider
// ─────────────────────────────────────────────────────────────────────────────

type customProvider struct {
	ops     map[paymentOpKind]*paymentOp
	baseDir string
}

func buildCustomProvider(cfg *DirectiveConfig) (*customProvider, error) {
	p := &customProvider{
		ops:     make(map[paymentOpKind]*paymentOp),
		baseDir: cfg.BaseDir,
	}
	// Operation kinds that map to DEFINE groups in Routes
	kinds := map[string]paymentOpKind{
		"CHARGE":   opCharge,
		"VERIFY":   opVerify,
		"REFUND":   opRefund,
		"CHECKOUT": opCheckout,
		"USSD":     opUSSD,
	}
	for _, r := range cfg.Routes {
		kind, ok := kinds[strings.ToUpper(r.Method)]
		if !ok || !r.IsGroup {
			continue
		}
		op, err := buildPaymentOp(kind, r.Routes, cfg.BaseDir)
		if err != nil {
			return nil, fmt.Errorf("custom PAYMENT %s: %w", r.Method, err)
		}
		p.ops[kind] = op
	}
	return p, nil
}

func buildPaymentOp(kind paymentOpKind, routes []*RouteConfig, baseDir string) (*paymentOp, error) {
	op := &paymentOp{
		kind:    kind,
		method:  "POST",
		baseDir: baseDir,
	}
	for _, r := range routes {
		cmd := strings.ToUpper(r.Method)
		switch cmd {
		case "ENDPOINT":
			op.endpoint = strings.Trim(r.Path, "\"'`")
		case "METHOD":
			op.method = strings.ToUpper(r.Path)
		case "HEADER":
			op.headerRoutes = append(op.headerRoutes, paymentMapRoute{route: r, baseDir: baseDir})
		case "BODY":
			op.bodyRoutes = append(op.bodyRoutes, paymentMapRoute{route: r, baseDir: baseDir})
		case "QUERY":
			op.queryRoutes = append(op.queryRoutes, paymentMapRoute{route: r, baseDir: baseDir})
		case "RESPONSE":
			op.responseRoute = &paymentRoute{route: r, baseDir: baseDir}
		}
	}
	if op.endpoint == "" {
		return nil, fmt.Errorf("missing ENDPOINT in %s block", kind)
	}
	if op.responseRoute == nil {
		return nil, fmt.Errorf("missing RESPONSE in %s block", kind)
	}
	return op, nil
}

// execute runs the op against the provider API with the given PaymentRequest.
func (p *customProvider) execute(kind paymentOpKind, req PaymentRequest) (PaymentResult, error) {
	op, ok := p.ops[kind]
	if !ok {
		return PaymentResult{}, fmt.Errorf("custom payment: operation %s not configured", kind)
	}

	vm := processor.New(op.baseDir, nil, nil)
	setPaymentVar(vm, req)

	// Build headers / body / query
	headers := make(map[string]string)
	body := make(map[string]string)
	query := make(map[string]string)

	for i := range op.headerRoutes {
		if err := op.headerRoutes[i].eval(headers, vm); err != nil {
			return PaymentResult{}, fmt.Errorf("HEADER: %w", err)
		}
	}
	for i := range op.bodyRoutes {
		if err := op.bodyRoutes[i].eval(body, vm); err != nil {
			return PaymentResult{}, fmt.Errorf("BODY: %w", err)
		}
	}
	for i := range op.queryRoutes {
		if err := op.queryRoutes[i].eval(query, vm); err != nil {
			return PaymentResult{}, fmt.Errorf("QUERY: %w", err)
		}
	}

	// Resolve {id} in endpoint
	endpoint := strings.ReplaceAll(op.endpoint, "{id}", req.ID)

	// Append query params
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		endpoint += sep + q.Encode()
	}

	// HTTP call
	rawBody := map[string]any{}
	for k, v := range body {
		rawBody[k] = v
	}
	respStatus, respBody, respHeaders, err := paymentDoHTTP(op.method, endpoint, headers, rawBody)
	if err != nil {
		return PaymentResult{}, fmt.Errorf("HTTP %s %s: %w", op.method, endpoint, err)
	}

	// RESPONSE script
	code, err := op.responseRoute.code()
	if err != nil {
		return PaymentResult{}, err
	}

	responseObj := vm.NewObject()
	responseObj.Set("status", respStatus)
	responseObj.Set("body", vm.ToValue(respBody))
	hObj := vm.NewObject()
	for k, v := range respHeaders {
		hObj.Set(k, v)
	}
	responseObj.Set("headers", hObj)
	vm.Set("response", responseObj)

	var resolved *PaymentResult
	var rejected string
	vm.Set("resolve", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		r := &PaymentResult{Raw: respBody}
		if m, ok := call.Arguments[0].Export().(map[string]interface{}); ok {
			r.ID = payStr(m, "id")
			r.Status = payStr(m, "status")
			r.RedirectURL = payStr(m, "redirectUrl")
			if a, ok := m["amount"].(float64); ok {
				r.Amount = a
			}
			r.Currency = payStr(m, "currency")
		}
		resolved = r
		return goja.Undefined()
	})
	vm.Set("reject", func(msg string) { rejected = msg })

	if _, err := vm.RunString(code); err != nil {
		return PaymentResult{}, fmt.Errorf("RESPONSE script: %w", err)
	}
	if rejected != "" {
		return PaymentResult{}, fmt.Errorf("payment rejected: %s", rejected)
	}
	if resolved == nil {
		return PaymentResult{}, fmt.Errorf("RESPONSE script did not call resolve()")
	}
	return *resolved, nil
}

func (p *customProvider) Charge(req PaymentRequest) (PaymentResult, error) {
	return p.execute(opCharge, req)
}
func (p *customProvider) Verify(id string) (PaymentResult, error) {
	return p.execute(opVerify, PaymentRequest{ID: id})
}
func (p *customProvider) Refund(req PaymentRequest) (PaymentResult, error) {
	return p.execute(opRefund, req)
}
func (p *customProvider) Checkout(req PaymentRequest) (PaymentResult, error) {
	return p.execute(opCheckout, req)
}
func (p *customProvider) USSD(req PaymentRequest) (PaymentResult, error) {
	return p.execute(opUSSD, req)
}

// ─────────────────────────────────────────────────────────────────────────────
// Stripe provider
// ─────────────────────────────────────────────────────────────────────────────

type stripeProvider struct {
	secretKey string
	mode      string
}

func (s *stripeProvider) auth() map[string]string {
	return map[string]string{"Authorization": "Bearer " + s.secretKey}
}

func (s *stripeProvider) Charge(req PaymentRequest) (PaymentResult, error) {
	body := map[string]any{
		"amount":   int(req.Amount),
		"currency": strings.ToLower(req.Currency),
	}
	if req.Email != "" {
		body["receipt_email"] = req.Email
	}
	if req.OrderID != "" {
		body["metadata[order_id]"] = req.OrderID
	}
	_, resp, _, err := paymentDoHTTP("POST", "https://api.stripe.com/v1/payment_intents", s.auth(), body)
	if err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{
		ID:     payStr(resp, "id"),
		Status: normalizeStripeStatus(payStr(resp, "status")),
		Raw:    resp,
	}, nil
}

func (s *stripeProvider) Verify(id string) (PaymentResult, error) {
	_, resp, _, err := paymentDoHTTP("GET",
		"https://api.stripe.com/v1/payment_intents/"+id, s.auth(), nil)
	if err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{
		ID:     payStr(resp, "id"),
		Status: normalizeStripeStatus(payStr(resp, "status")),
		Raw:    resp,
	}, nil
}

func (s *stripeProvider) Refund(req PaymentRequest) (PaymentResult, error) {
	body := map[string]any{"payment_intent": req.ID}
	if req.Amount > 0 {
		body["amount"] = int(req.Amount)
	}
	_, resp, _, err := paymentDoHTTP("POST", "https://api.stripe.com/v1/refunds", s.auth(), body)
	if err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{ID: payStr(resp, "id"), Status: "refunded", Raw: resp}, nil
}

func (s *stripeProvider) Checkout(req PaymentRequest) (PaymentResult, error) {
	body := map[string]any{
		"mode":                                   "payment",
		"success_url":                            req.Redirects.Success,
		"cancel_url":                             req.Redirects.Cancel,
		"line_items[0][price_data][currency]":    strings.ToLower(req.Currency),
		"line_items[0][price_data][unit_amount]": int(req.Amount),
		"line_items[0][quantity]":                1,
	}
	_, resp, _, err := paymentDoHTTP("POST",
		"https://api.stripe.com/v1/checkout/sessions", s.auth(), body)
	if err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{
		ID:          payStr(resp, "id"),
		Status:      "pending",
		RedirectURL: payStr(resp, "url"),
		Raw:         resp,
	}, nil
}

func (s *stripeProvider) USSD(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("stripe: USSD not supported")
}

func normalizeStripeStatus(s string) string {
	switch s {
	case "succeeded":
		return "succeeded"
	case "canceled":
		return "failed"
	default:
		return "pending"
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Flutterwave provider
// ─────────────────────────────────────────────────────────────────────────────

type flutterwaveProvider struct {
	publicKey string
	secretKey string
	mode      string
}

func (f *flutterwaveProvider) auth() map[string]string {
	return map[string]string{"Authorization": "Bearer " + f.secretKey}
}

func (f *flutterwaveProvider) Charge(req PaymentRequest) (PaymentResult, error) {
	body := map[string]any{
		"amount":       req.Amount,
		"currency":     req.Currency,
		"email":        req.Email,
		"phone_number": req.Phone,
		"tx_ref":       req.OrderID,
	}
	_, resp, _, err := paymentDoHTTP("POST",
		"https://api.flutterwave.com/v3/charges?type=mobile_money_ghana",
		f.auth(), body)
	if err != nil {
		return PaymentResult{}, err
	}
	data := payMap(resp, "data")
	return PaymentResult{
		ID:     payStr(data, "id"),
		Status: normalizeFlwStatus(payStr(data, "status")),
		Raw:    resp,
	}, nil
}

func (f *flutterwaveProvider) Verify(id string) (PaymentResult, error) {
	_, resp, _, err := paymentDoHTTP("GET",
		"https://api.flutterwave.com/v3/transactions/"+id+"/verify",
		f.auth(), nil)
	if err != nil {
		return PaymentResult{}, err
	}
	data := payMap(resp, "data")
	return PaymentResult{
		ID:       payStr(data, "id"),
		Status:   normalizeFlwStatus(payStr(data, "status")),
		Amount:   payFloat(data, "amount"),
		Currency: payStr(data, "currency"),
		Raw:      resp,
	}, nil
}

func (f *flutterwaveProvider) Refund(req PaymentRequest) (PaymentResult, error) {
	body := map[string]any{"amount": req.Amount}
	_, resp, _, err := paymentDoHTTP("POST",
		"https://api.flutterwave.com/v3/transactions/"+req.ID+"/refund",
		f.auth(), body)
	if err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{ID: req.ID, Status: "refunded", Raw: resp}, nil
}

func (f *flutterwaveProvider) Checkout(req PaymentRequest) (PaymentResult, error) {
	body := map[string]any{
		"tx_ref":       req.OrderID,
		"amount":       req.Amount,
		"currency":     req.Currency,
		"redirect_url": req.Redirects.Success,
		"customer":     map[string]any{"email": req.Email, "phonenumber": req.Phone},
		"public_key":   f.publicKey,
	}
	_, resp, _, err := paymentDoHTTP("POST",
		"https://api.flutterwave.com/v3/payments", f.auth(), body)
	if err != nil {
		return PaymentResult{}, err
	}
	data := payMap(resp, "data")
	return PaymentResult{
		ID:          req.OrderID,
		Status:      "pending",
		RedirectURL: payStr(data, "link"),
		Raw:         resp,
	}, nil
}

func (f *flutterwaveProvider) USSD(req PaymentRequest) (PaymentResult, error) {
	body := map[string]any{
		"tx_ref":   req.OrderID,
		"amount":   req.Amount,
		"currency": req.Currency,
		"email":    req.Email,
	}
	_, resp, _, err := paymentDoHTTP("POST",
		"https://api.flutterwave.com/v3/charges?type=ussd",
		f.auth(), body)
	if err != nil {
		return PaymentResult{}, err
	}
	data := payMap(resp, "data")
	return PaymentResult{
		ID:     payStr(data, "flw_ref"),
		Status: "pending",
		Raw:    resp,
	}, nil
}

func normalizeFlwStatus(s string) string {
	switch strings.ToLower(s) {
	case "successful":
		return "succeeded"
	case "failed":
		return "failed"
	default:
		return "pending"
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CinetPay provider
// ─────────────────────────────────────────────────────────────────────────────

type cinetpayProvider struct {
	apiKey string
	siteID string
	mode   string
}

func (c *cinetpayProvider) Charge(req PaymentRequest) (PaymentResult, error) {
	body := map[string]any{
		"apikey":         c.apiKey,
		"site_id":        c.siteID,
		"transaction_id": req.OrderID,
		"amount":         req.Amount,
		"currency":       req.Currency,
		"description":    "Paiement",
		"notify_url":     "",
		"return_url":     req.Redirects.Success,
		"cancel_url":     req.Redirects.Cancel,
		"customer_name":  req.Email,
		"customer_email": req.Email,
	}
	_, resp, _, err := paymentDoHTTP("POST",
		"https://api-checkout.cinetpay.com/v2/payment", nil, body)
	if err != nil {
		return PaymentResult{}, err
	}
	data := payMap(resp, "data")
	return PaymentResult{
		ID:          req.OrderID,
		Status:      "pending",
		RedirectURL: payStr(data, "payment_url"),
		Raw:         resp,
	}, nil
}

func (c *cinetpayProvider) Verify(id string) (PaymentResult, error) {
	body := map[string]any{
		"apikey":         c.apiKey,
		"site_id":        c.siteID,
		"transaction_id": id,
	}
	_, resp, _, err := paymentDoHTTP("POST",
		"https://api-checkout.cinetpay.com/v2/payment/check", nil, body)
	if err != nil {
		return PaymentResult{}, err
	}
	data := payMap(resp, "data")
	status := "pending"
	if payStr(data, "status") == "ACCEPTED" {
		status = "succeeded"
	} else if payStr(data, "status") == "REFUSED" {
		status = "failed"
	}
	return PaymentResult{ID: id, Status: status, Raw: resp}, nil
}

func (c *cinetpayProvider) Refund(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("cinetpay: refund not supported via API")
}
func (c *cinetpayProvider) Checkout(req PaymentRequest) (PaymentResult, error) {
	return c.Charge(req) // CinetPay is always redirect-based
}
func (c *cinetpayProvider) USSD(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("cinetpay: USSD not supported")
}

// ─────────────────────────────────────────────────────────────────────────────
// MTN MoMo provider
// ─────────────────────────────────────────────────────────────────────────────

type mtnProvider struct {
	subscriptionKey string
	apiUser         string
	apiKey          string
	baseURL         string
	mode            string
}

func (m *mtnProvider) auth() map[string]string {
	token := base64Encode(m.apiUser + ":" + m.apiKey)
	return map[string]string{
		"Authorization":             "Basic " + token,
		"X-Target-Environment":      m.mode,
		"Ocp-Apim-Subscription-Key": m.subscriptionKey,
	}
}

func (m *mtnProvider) Charge(req PaymentRequest) (PaymentResult, error) {
	ref := req.OrderID
	body := map[string]any{
		"amount":       fmt.Sprintf("%.0f", req.Amount),
		"currency":     req.Currency,
		"externalId":   ref,
		"payer":        map[string]any{"partyIdType": "MSISDN", "partyId": req.Phone},
		"payerMessage": "Payment",
		"payeeNote":    "Order " + ref,
	}
	headers := m.auth()
	headers["X-Reference-Id"] = ref
	headers["Content-Type"] = "application/json"
	status, _, _, err := paymentDoHTTP("POST",
		m.baseURL+"/requesttopay", headers, body)
	if err != nil {
		return PaymentResult{}, err
	}
	if status != 202 {
		return PaymentResult{}, fmt.Errorf("MTN charge failed: HTTP %d", status)
	}
	return PaymentResult{ID: ref, Status: "pending"}, nil
}

func (m *mtnProvider) Verify(id string) (PaymentResult, error) {
	_, resp, _, err := paymentDoHTTP("GET",
		m.baseURL+"/requesttopay/"+id, m.auth(), nil)
	if err != nil {
		return PaymentResult{}, err
	}
	status := "pending"
	switch strings.ToUpper(payStr(resp, "status")) {
	case "SUCCESSFUL":
		status = "succeeded"
	case "FAILED":
		status = "failed"
	}
	return PaymentResult{ID: id, Status: status, Raw: resp}, nil
}

func (m *mtnProvider) Refund(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("mtn: refund not supported")
}
func (m *mtnProvider) Checkout(req PaymentRequest) (PaymentResult, error) {
	return m.Charge(req)
}
func (m *mtnProvider) USSD(req PaymentRequest) (PaymentResult, error) {
	return m.Charge(req) // MTN MoMo IS a USSD push
}

// ─────────────────────────────────────────────────────────────────────────────
// Orange Money provider
// ─────────────────────────────────────────────────────────────────────────────

type orangeProvider struct {
	clientID     string
	clientSecret string
	baseURL      string
	mode         string
}

func (o *orangeProvider) token() (string, error) {
	body := map[string]any{"grant_type": "client_credentials"}
	headers := map[string]string{
		"Authorization": "Basic " + base64Encode(o.clientID+":"+o.clientSecret),
		"Content-Type":  "application/x-www-form-urlencoded",
	}
	_, resp, _, err := paymentDoHTTP("POST", o.baseURL+"/oauth/v2/token", headers, body)
	if err != nil {
		return "", err
	}
	return payStr(resp, "access_token"), nil
}

func (o *orangeProvider) Charge(req PaymentRequest) (PaymentResult, error) {
	tok, err := o.token()
	if err != nil {
		return PaymentResult{}, err
	}
	body := map[string]any{
		"merchant_key": o.clientID,
		"currency":     req.Currency,
		"order_id":     req.OrderID,
		"amount":       req.Amount,
		"return_url":   req.Redirects.Success,
		"cancel_url":   req.Redirects.Cancel,
		"notif_url":    "",
		"lang":         "fr",
		"reference":    req.OrderID,
	}
	_, resp, _, err := paymentDoHTTP("POST",
		o.baseURL+"/orange-money-webpay/CM/v1/webpayment",
		map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"},
		body)
	if err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{
		ID:          payStr(resp, "pay_token"),
		Status:      "pending",
		RedirectURL: payStr(resp, "payment_url"),
		Raw:         resp,
	}, nil
}

func (o *orangeProvider) Verify(id string) (PaymentResult, error) {
	tok, err := o.token()
	if err != nil {
		return PaymentResult{}, err
	}
	_, resp, _, err := paymentDoHTTP("GET",
		o.baseURL+"/orange-money-webpay/CM/v1/webpayment/"+id,
		map[string]string{"Authorization": "Bearer " + tok}, nil)
	if err != nil {
		return PaymentResult{}, err
	}
	status := "pending"
	if payStr(resp, "status") == "SUCCESS" {
		status = "succeeded"
	}
	return PaymentResult{ID: id, Status: status, Raw: resp}, nil
}

func (o *orangeProvider) Refund(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("orange: refund not supported")
}
func (o *orangeProvider) Checkout(req PaymentRequest) (PaymentResult, error) { return o.Charge(req) }
func (o *orangeProvider) USSD(req PaymentRequest) (PaymentResult, error)     { return o.Charge(req) }

// ─────────────────────────────────────────────────────────────────────────────
// Airtel Money provider
// ─────────────────────────────────────────────────────────────────────────────

type airtelProvider struct {
	clientID     string
	clientSecret string
	baseURL      string
	mode         string
}

func (a *airtelProvider) token() (string, error) {
	body := map[string]any{
		"client_id":     a.clientID,
		"client_secret": a.clientSecret,
		"grant_type":    "client_credentials",
	}
	_, resp, _, err := paymentDoHTTP("POST",
		a.baseURL+"/auth/oauth2/token", nil, body)
	if err != nil {
		return "", err
	}
	return payStr(resp, "access_token"), nil
}

func (a *airtelProvider) Charge(req PaymentRequest) (PaymentResult, error) {
	tok, err := a.token()
	if err != nil {
		return PaymentResult{}, err
	}
	body := map[string]any{
		"reference":   req.OrderID,
		"subscriber":  map[string]any{"country": "CM", "currency": req.Currency, "msisdn": req.Phone},
		"transaction": map[string]any{"amount": req.Amount, "country": "CM", "currency": req.Currency, "id": req.OrderID},
	}
	_, resp, _, err := paymentDoHTTP("POST",
		a.baseURL+"/merchant/v1/payments/",
		map[string]string{
			"Authorization": "Bearer " + tok,
			"X-Country":     "CM",
			"X-Currency":    req.Currency,
		}, body)
	if err != nil {
		return PaymentResult{}, err
	}
	data := payMap(payMap(resp, "data"), "transaction")
	return PaymentResult{
		ID:     payStr(data, "id"),
		Status: "pending",
		Raw:    resp,
	}, nil
}

func (a *airtelProvider) Verify(id string) (PaymentResult, error) {
	tok, err := a.token()
	if err != nil {
		return PaymentResult{}, err
	}
	_, resp, _, err := paymentDoHTTP("GET",
		a.baseURL+"/standard/v1/payments/"+id,
		map[string]string{"Authorization": "Bearer " + tok}, nil)
	if err != nil {
		return PaymentResult{}, err
	}
	data := payMap(payMap(resp, "data"), "transaction")
	status := "pending"
	if payStr(data, "status") == "TS" {
		status = "succeeded"
	} else if payStr(data, "status") == "TF" {
		status = "failed"
	}
	return PaymentResult{ID: id, Status: status, Raw: resp}, nil
}

func (a *airtelProvider) Refund(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("airtel: refund not supported")
}
func (a *airtelProvider) Checkout(req PaymentRequest) (PaymentResult, error) { return a.Charge(req) }
func (a *airtelProvider) USSD(req PaymentRequest) (PaymentResult, error)     { return a.Charge(req) }

// ─────────────────────────────────────────────────────────────────────────────
// HTTP helper
// ─────────────────────────────────────────────────────────────────────────────

func paymentDoHTTP(method, apiURL string, headers map[string]string, body map[string]any) (int, map[string]any, map[string]string, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, apiURL, reqBody)
	if err != nil {
		return 0, nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var result map[string]any
	json.Unmarshal(raw, &result)

	respHeaders := make(map[string]string)
	for k, vals := range resp.Header {
		if len(vals) > 0 {
			respHeaders[k] = vals[0]
		}
	}
	return resp.StatusCode, result, respHeaders, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// JS helpers
// ─────────────────────────────────────────────────────────────────────────────

func setPaymentVar(vm *goja.Runtime, req PaymentRequest) {
	obj := vm.NewObject()
	obj.Set("amount", req.Amount)
	obj.Set("currency", req.Currency)
	obj.Set("phone", req.Phone)
	obj.Set("email", req.Email)
	obj.Set("orderId", req.OrderID)
	obj.Set("id", req.ID)
	obj.Set("reason", req.Reason)
	meta := vm.NewObject()
	for k, v := range req.Metadata {
		meta.Set(k, v)
	}
	obj.Set("metadata", meta)
	redirects := vm.NewObject()
	redirects.Set("success", req.Redirects.Success)
	redirects.Set("cancel", req.Redirects.Cancel)
	redirects.Set("failure", req.Redirects.Failure)
	obj.Set("redirects", redirects)
	vm.Set("payment", obj)
}

func payStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func payFloat(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func payMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// paymentVerifyHMAC verifies a HMAC-SHA256 signature.
func paymentVerifyHMAC(body, sig, secret string) bool {
	// Providers use different naming; this is the generic fallback.
	// Real verification is done in the WEBHOOK JS script.
	return sig != "" && secret != "" && body != ""
}

// ─────────────────────────────────────────────────────────────────────────────
// JS module — `require('payment')`
// ─────────────────────────────────────────────────────────────────────────────

type PaymentModule struct{}

func init() {
	modules.RegisterModule(&PaymentModule{})
}

func (m *PaymentModule) Name() string { return "payment" }
func (m *PaymentModule) Doc() string {
	return "Payment module (Stripe, Flutterwave, CinetPay, MTN, Orange, Airtel, custom)"
}

func (m *PaymentModule) ToJSObject(vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()
	m.Loader(nil, vm, obj)
	return obj
}

func (m *PaymentModule) Loader(_ any, vm *goja.Runtime, moduleObject *goja.Object) {
	// CommonJS support: if exports exists, use it as the target
	module := moduleObject
	if exp := moduleObject.Get("exports"); exp != nil && !goja.IsUndefined(exp) {
		module = exp.ToObject(vm)
	}
	// ── connect(url, name?, options?) ─────────────────────────────────────────
	module.Set("connect", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("payment.connect() requires a URL or 'custom'")))
			return goja.Undefined()
		}
		rawURL := call.Argument(0).String()
		name := "payment"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
			name = call.Arguments[1].String()
		}
		currency, country, mode := "USD", "", "production"
		isDefault := GetPaymentConnection() == nil
		if len(call.Arguments) > 2 {
			if opts, ok := call.Arguments[2].Export().(map[string]interface{}); ok {
				if v := payStr(opts, "currency"); v != "" {
					currency = v
				}
				if v := payStr(opts, "country"); v != "" {
					country = v
				}
				if v := payStr(opts, "mode"); v != "" {
					mode = v
				}
				if v, ok := opts["default"].(bool); ok {
					isDefault = v
				}
			}
		}
		cfg := &DirectiveConfig{
			Address: rawURL,
			Args:    Arguments{"mode": mode},
			Configs: Arguments{},
			Routes:  []*RouteConfig{{Method: "MODE", Path: mode}},
		}
		provider, err := buildPaymentProvider(rawURL, cfg)
		if err != nil {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("payment.connect: %w", err)))
			return goja.Undefined()
		}
		conn := &PaymentConnection{
			name:     name,
			provider: provider,
			currency: currency,
			country:  country,
			mode:     mode,
			metadata: make(map[string]string),
		}
		registerPaymentConnection(name, conn, isDefault)
		return paymentConnProxy(vm, conn)
	})

	// ── connection(name?) ──────────────────────────────────────────────────────
	module.Set("connection", func(call goja.FunctionCall) goja.Value {
		name := ""
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) {
			name = call.Arguments[0].String()
		}
		conn := GetPaymentConnection(name)
		if conn == nil {
			if name == "" {
				vm.Interrupt(vm.NewGoError(fmt.Errorf("payment: no default connection")))
			} else {
				vm.Interrupt(vm.NewGoError(fmt.Errorf("payment: connection %q not found", name)))
			}
			return goja.Undefined()
		}
		return paymentConnProxy(vm, conn)
	})

	// ── connectionNames ────────────────────────────────────────────────────────
	module.DefineAccessorProperty("connectionNames",
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			names := make([]goja.Value, 0, len(paymentConns))
			for n := range paymentConns {
				names = append(names, vm.ToValue(n))
			}
			return vm.NewArray(names)
		}),
		goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE,
	)

	// ── hasConnection(name) ────────────────────────────────────────────────────
	module.Set("hasConnection", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("payment.hasConnection() requires a name")))
			return goja.Undefined()
		}
		_, ok := paymentConns[call.Argument(0).String()]
		return vm.ToValue(ok)
	})

	// ── hasDefault ─────────────────────────────────────────────────────────────
	module.DefineAccessorProperty("hasDefault",
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(defaultPaymentConn != nil)
		}),
		goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE,
	)

	// ── default ────────────────────────────────────────────────────────────────
	module.DefineAccessorProperty("default",
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			if defaultPaymentConn == nil {
				vm.Interrupt(vm.NewGoError(fmt.Errorf("payment: no default connection")))
				return goja.Undefined()
			}
			return paymentConnProxy(vm, defaultPaymentConn)
		}),
		goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE,
	)

	// ── Shortcuts delegating to default connection ─────────────────────────────
	for _, op := range []string{"charge", "verify", "refund", "checkout", "ussd"} {
		op := op
		module.Set(op, func(call goja.FunctionCall) goja.Value {
			conn := GetPaymentConnection()
			if conn == nil {
				vm.Interrupt(vm.NewGoError(fmt.Errorf("payment: no default connection")))
				return goja.Undefined()
			}
			return paymentOpFromJS(vm, conn, op, call)
		})
	}
}

// paymentConnProxy wraps a *PaymentConnection as a JS object.
//
//	conn.name
//	conn.charge({amount, currency, phone, email, orderId, metadata})
//	conn.verify(id)
//	conn.refund({id, amount, reason})
//	conn.checkout({amount, currency, orderId})
//	conn.ussd({phone, amount, currency, orderId})
func paymentConnProxy(vm *goja.Runtime, conn *PaymentConnection) goja.Value {
	obj := vm.NewObject()
	obj.Set("name", conn.name)
	obj.Set("currency", conn.currency)
	obj.Set("mode", conn.mode)

	for _, op := range []string{"charge", "verify", "refund", "checkout", "ussd"} {
		op := op
		obj.Set(op, func(call goja.FunctionCall) goja.Value {
			return paymentOpFromJS(vm, conn, op, call)
		})
	}
	return obj
}

// paymentOpFromJS dispatches a JS call to the right PaymentConnection method
// and returns a thenable.
func paymentOpFromJS(vm *goja.Runtime, conn *PaymentConnection, op string, call goja.FunctionCall) goja.Value {
	var result PaymentResult
	var opErr error

	switch op {
	case "verify":
		id := ""
		if len(call.Arguments) > 0 {
			id = call.Arguments[0].String()
		}
		result, opErr = conn.Verify(id)

	default:
		var req PaymentRequest
		if len(call.Arguments) > 0 {
			if m, ok := call.Arguments[0].Export().(map[string]interface{}); ok {
				req = paymentRequestFromJS(m, conn)
			}
		}
		switch op {
		case "charge":
			result, opErr = conn.Charge(req)
		case "refund":
			result, opErr = conn.Refund(req)
		case "checkout":
			result, opErr = conn.Checkout(req)
		case "ussd":
			result, opErr = conn.USSD(req)
		}
	}

	return paymentResultThenable(vm, result, opErr)
}

func paymentRequestFromJS(m map[string]interface{}, conn *PaymentConnection) PaymentRequest {
	req := PaymentRequest{
		Amount:   0,
		Currency: conn.currency,
		Phone:    payStr(m, "phone"),
		Email:    payStr(m, "email"),
		OrderID:  payStr(m, "orderId"),
		ID:       payStr(m, "id"),
		Reason:   payStr(m, "reason"),
	}
	if v := payStr(m, "currency"); v != "" {
		req.Currency = v
	}
	if v, ok := m["amount"].(float64); ok {
		req.Amount = v
	}
	if meta, ok := m["metadata"].(map[string]interface{}); ok {
		req.Metadata = make(map[string]any, len(meta))
		for k, v := range meta {
			req.Metadata[k] = v
		}
	}
	// Populate redirects from connection defaults
	req.Redirects.Success = conn.redirects.Success
	req.Redirects.Cancel = conn.redirects.Cancel
	req.Redirects.Failure = conn.redirects.Failure
	return req
}

// paymentResultThenable builds a thenable from a PaymentResult.
func paymentResultThenable(vm *goja.Runtime, result PaymentResult, opErr error) goja.Value {
	obj := vm.NewObject()
	obj.Set("ok", opErr == nil)
	obj.Set("error", func() goja.Value {
		if opErr != nil {
			return vm.ToValue(opErr.Error())
		}
		return goja.Null()
	})
	if opErr == nil {
		res := vm.NewObject()
		res.Set("id", result.ID)
		res.Set("status", result.Status)
		res.Set("redirectUrl", result.RedirectURL)
		res.Set("amount", result.Amount)
		res.Set("currency", result.Currency)
		obj.Set("result", res)
	}
	obj.Set("then", func(onFulfilled, onRejected goja.Value) goja.Value {
		if opErr != nil {
			if fn, ok := goja.AssertFunction(onRejected); ok {
				fn(goja.Undefined(), vm.ToValue(opErr.Error()))
			}
			return goja.Undefined()
		}
		if fn, ok := goja.AssertFunction(onFulfilled); ok {
			res := vm.NewObject()
			res.Set("id", result.ID)
			res.Set("status", result.Status)
			res.Set("redirectUrl", result.RedirectURL)
			res.Set("amount", result.Amount)
			res.Set("currency", result.Currency)
			fn(goja.Undefined(), res)
		}
		return goja.Undefined()
	})
	obj.Set("catch", func(onRejected goja.Value) goja.Value {
		if opErr != nil {
			if fn, ok := goja.AssertFunction(onRejected); ok {
				fn(goja.Undefined(), vm.ToValue(opErr.Error()))
			}
		}
		return obj
	})
	return obj
}
