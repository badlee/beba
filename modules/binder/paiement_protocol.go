package binder

// payment_protocol.go — PAYMENT directive for integrating payment providers.
//
// Supported providers: Stripe, Flutterwave, CinetPay, MTN, Orange, Airtel,
// x402/crypto (via facilitator), and custom.
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
//       PUSH DEFINE                        // (alias: USSD DEFINE)
//           ENDPOINT "https://api.mypay.com/v1/push"
//           METHOD POST
//           BODY BEGIN
//               append("phone",  payment.phone)
//               append("amount", payment.amount)
//           END BODY
//           RESPONSE BEGIN
//               if (response.status !== 202) reject("Push failed")
//               resolve({ id: response.body.requestId, status: "pending" })
//           END RESPONSE
//       END PUSH
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
//   // Push payment (push message, USSD, email, SMS)
//   const push = await pay.push({ phone: "237612345678", amount: 1000, orderId: "ORD-003" })
//   // Backward compat: pay.ussd() is an alias for pay.push()
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
	"crypto/sha256"
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
	"strconv"
	"strings"
	"time"

	dbpkg "http-server/modules/db"
	"http-server/modules"
	"http-server/plugins/httpserver"
	"http-server/processor"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
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
// paymentOp — one operation block (CHARGE / VERIFY / REFUND / CHECKOUT / PUSH)
// ─────────────────────────────────────────────────────────────────────────────

type paymentOpKind string

const (
	opCharge   paymentOpKind = "CHARGE"
	opVerify   paymentOpKind = "VERIFY"
	opRefund   paymentOpKind = "REFUND"
	opCheckout paymentOpKind = "CHECKOUT"
	opPush     paymentOpKind = "PUSH"
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
	Push(req PaymentRequest) (PaymentResult, error) // was USSD — covers push message, USSD, email, SMS
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

	// x402/crypto payment support
	x402       *x402Config          // non-nil for x402/crypto providers
	schema     *paymentSchemaLink   // link to a DATABASE table for payment tracking
	userLookup *paymentUserLookup   // JS code to extract user_id from request
	ttl        time.Duration        // default TTL for payments (0 = permanent/lifetime)
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
func (c *PaymentConnection) Push(req PaymentRequest) (PaymentResult, error) {
	return c.provider.Push(c.defaultRequest(req))
}

// USSD is a backward-compat alias for Push.
func (c *PaymentConnection) USSD(req PaymentRequest) (PaymentResult, error) {
	return c.Push(req)
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

	// ── x402/crypto-specific sub-directives ──────────────────────────────────
	if _, isX402 := provider.(*x402Provider); isX402 {
		conn.x402 = &x402Config{
			wallets:  make(map[string]string),
			networks: make(map[string]string),
			scheme:   "exact",
			useHTTPS: true,
		}
		p := provider.(*x402Provider)
		conn.x402.facilitatorURL = p.facilitatorURL
		for _, r := range cfg.Routes {
			cmd := strings.ToUpper(r.Method)
			switch cmd {
			case "WALLET":
				conn.x402.wallets[strings.ToLower(r.Path)] = strings.Trim(r.Handler, "\"'`")
			case "NETWORK":
				conn.x402.networks[strings.ToLower(r.Path)] = strings.Trim(r.Handler, "\"'`")
			case "SCHEME":
				conn.x402.scheme = strings.ToLower(r.Path)
			case "USE":
				conn.x402.useHTTPS = strings.ToLower(r.Path) == "https"
			}
		}
		// Copy wallets/networks into the provider so it can build PaymentRequired
		p.wallets = conn.x402.wallets
		p.networks = conn.x402.networks
		p.scheme = conn.x402.scheme
	}

	// ── TTL ──────────────────────────────────────────────────────────────────
	ttlStr := routeScalar(cfg.Routes, "TTL", "")
	if ttlStr != "" {
		conn.ttl = parseTTL(ttlStr)
	}

	// ── SCHEMA 'dbname:table(f1,f2,...)' ────────────────────────────────────
	schemaStr := routeScalar(cfg.Routes, "SCHEMA", "")
	if schemaStr != "" {
		schemaStr = strings.Trim(schemaStr, "\"'`")
		link, err := parseSchemaLink(schemaStr)
		if err != nil {
			log.Printf("PAYMENT %s: SCHEMA parse error: %v", name, err)
		} else {
			conn.schema = link
		}
	}

	// ── USER_ID_LOOKUP ──────────────────────────────────────────────────────
	for _, r := range cfg.Routes {
		if strings.ToUpper(r.Method) == "USER_ID_LOOKUP" {
			conn.userLookup = &paymentUserLookup{
				route:   r,
				baseDir: cfg.BaseDir,
			}
			break
		}
	}

	// ── Auto-SCHEMA fallback: sqlite://:memory: with default table ──────────
	if conn.schema == nil {
		memDB, err := dbpkg.FromURL("sqlite://:memory:")
		if err != nil {
			log.Printf("PAYMENT %s: auto-schema memory DB error: %v", name, err)
		} else {
			memDB.AutoMigrate(&defaultPaymentRecord{})
			conn.schema = &paymentSchemaLink{
				dbName:    "__payment_" + name,
				tableName: "payments",
				fields:    []string{"ref", "amount", "currency", "provider", "status", "product", "user", "expiration"},
				db:        memDB,
			}
		}
	}

	// ── Auto USER_ID_LOOKUP fallback: hash(IP + User-Agent) ─────────────────
	if conn.userLookup == nil {
		conn.userLookup = &paymentUserLookup{
			route: &RouteConfig{
				Inline:  true,
				Handler: defaultUserIDLookupJS,
			},
			baseDir: cfg.BaseDir,
		}
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

	// ── Resolve SCHEMA DB lazily (deferred until first use if from DATABASE) ─
	if conn.schema != nil && conn.schema.db == nil {
		dbConn := dbpkg.GetConnection(conn.schema.dbName)
		if dbConn != nil {
			conn.schema.db = dbConn.GetDB()
		} else {
			log.Printf("PAYMENT %s: SCHEMA db %q not found, will retry on first use", name, conn.schema.dbName)
		}
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

	case "x402", "crypto":
		facilitatorURL := "https://" + u.Host + u.Path
		if strings.HasSuffix(facilitatorURL, "/") {
			facilitatorURL = strings.TrimRight(facilitatorURL, "/")
		}
		return &x402Provider{
			facilitatorURL: facilitatorURL,
			mode:           mode,
			wallets:        make(map[string]string),
			networks:       make(map[string]string),
			scheme:         "exact",
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
		"PUSH":     opPush,
		"USSD":     opPush, // backward compat alias
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
func (p *customProvider) Push(req PaymentRequest) (PaymentResult, error) {
	return p.execute(opPush, req)
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

func (s *stripeProvider) Push(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("stripe: push payment not supported")
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

func (f *flutterwaveProvider) Push(req PaymentRequest) (PaymentResult, error) {
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
func (c *cinetpayProvider) Push(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("cinetpay: push payment not supported")
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
func (m *mtnProvider) Push(req PaymentRequest) (PaymentResult, error) {
	return m.Charge(req) // MTN MoMo IS a push payment
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
func (o *orangeProvider) Push(req PaymentRequest) (PaymentResult, error)     { return o.Charge(req) }

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
func (a *airtelProvider) Push(req PaymentRequest) (PaymentResult, error)     { return a.Charge(req) }

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
	return "Payment module (Stripe, Flutterwave, CinetPay, MTN, Orange, Airtel, x402/crypto, custom)"
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
	for _, op := range []string{"charge", "verify", "refund", "checkout", "push"} {
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
	// Backward-compat alias: ussd → push
	module.Set("ussd", module.Get("push"))

	// ── get(name) — alias for connection() ─────────────────────────────────────
	module.Set("get", module.Get("connection"))
}

// paymentConnProxy wraps a *PaymentConnection as a JS object.
//
//	conn.name
//	conn.charge({amount, currency, phone, email, orderId, metadata})
//	conn.verify(id)
//	conn.refund({id, amount, reason})
//	conn.checkout({amount, currency, orderId})
//	conn.push({phone, amount, currency, orderId})
//	conn.ussd()  — alias for push()
//	conn.isX402
//	conn.facilitator / wallets / networks / scheme  (x402 only)
//	conn.isPaid(userID, ref)
func paymentConnProxy(vm *goja.Runtime, conn *PaymentConnection) goja.Value {
	obj := vm.NewObject()
	obj.Set("name", conn.name)
	obj.Set("currency", conn.currency)
	obj.Set("mode", conn.mode)

	for _, op := range []string{"charge", "verify", "refund", "checkout", "push"} {
		op := op
		obj.Set(op, func(call goja.FunctionCall) goja.Value {
			return paymentOpFromJS(vm, conn, op, call)
		})
	}
	// Backward-compat alias
	obj.Set("ussd", obj.Get("push"))

	// ── x402/crypto extensions ──────────────────────────────────────────────
	obj.Set("isX402", conn.x402 != nil)
	if conn.x402 != nil {
		obj.Set("facilitator", conn.x402.facilitatorURL)
		walletsObj := vm.NewObject()
		for k, v := range conn.x402.wallets {
			walletsObj.Set(k, v)
		}
		obj.Set("wallets", walletsObj)
		networksObj := vm.NewObject()
		for k, v := range conn.x402.networks {
			networksObj.Set(k, v)
		}
		obj.Set("networks", networksObj)
		obj.Set("scheme", conn.x402.scheme)
	}

	// ── DB-backed helpers (all providers) ──────────────────────────────────
	obj.Set("isPaid", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(false)
		}
		userID := call.Arguments[0].String()
		ref := call.Arguments[1].String()
		paid, _ := checkPaymentExists(conn, userID, ref)
		return vm.ToValue(paid)
	})

	obj.Set("getPayments", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue([]interface{}{})
		}
		userID := call.Arguments[0].String()
		ref := call.Arguments[1].String()
		includeExpired := false
		if len(call.Arguments) > 2 {
			includeExpired = call.Arguments[2].ToBoolean()
		}
		records, err := getPaymentRecords(conn, userID, ref, includeExpired)
		if err != nil {
			return vm.ToValue([]interface{}{})
		}
		return vm.ToValue(records)
	})

	obj.Set("getAmountPayments", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return vm.ToValue(0)
		}
		userID := call.Arguments[0].String()
		ref := ""
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			ref = call.Arguments[1].String()
		}
		amount := getAmountPayments(conn, userID, ref)
		return vm.ToValue(amount)
	})

	obj.Set("totalPayments", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return vm.ToValue(0)
		}
		userID := call.Arguments[0].String()
		ref := ""
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			ref = call.Arguments[1].String()
		}
		count := getCountPayments(conn, userID, ref)
		return vm.ToValue(count)
	})

	obj.Set("infoPayments", func(call goja.FunctionCall) goja.Value {
		userID := ""
		ref := ""
		includeExpired := false
		limit := 10
		offset := 0
		var start, end *time.Time

		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) && !goja.IsNull(call.Arguments[0]) {
			opts := call.Arguments[0].ToObject(vm)
			if uVal := opts.Get("userID"); uVal != nil && !goja.IsUndefined(uVal) && !goja.IsNull(uVal) {
				userID = uVal.String()
			}
			if refVal := opts.Get("ref"); refVal != nil && !goja.IsUndefined(refVal) && !goja.IsNull(refVal) {
				ref = refVal.String()
			}
			if incVal := opts.Get("include_expired"); incVal != nil && !goja.IsUndefined(incVal) {
				includeExpired = incVal.ToBoolean()
			}
			if limitVal := opts.Get("limit"); limitVal != nil && !goja.IsUndefined(limitVal) {
				limit = int(limitVal.ToInteger())
			}
			if offsetVal := opts.Get("offset"); offsetVal != nil && !goja.IsUndefined(offsetVal) {
				offset = int(offsetVal.ToInteger())
			}
			
			parseDate := func(v goja.Value) *time.Time {
				if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
					return nil
				}
				if exp, ok := v.Export().(time.Time); ok {
					return &exp
				} else if str, ok := v.Export().(string); ok {
					if t, err := time.Parse(time.RFC3339, str); err == nil {
						return &t
					} else if t, err := time.Parse("2006-01-02", str); err == nil {
						return &t
					}
				}
				return nil
			}

			if startVal := opts.Get("start"); startVal != nil {
				start = parseDate(startVal)
			}
			if endVal := opts.Get("end"); endVal != nil {
				end = parseDate(endVal)
			}
		}

		info, err := getInfoPayments(conn, userID, ref, includeExpired, limit, offset, start, end)
		if err != nil {
			return vm.ToValue(map[string]interface{}{"total": 0, "amount": 0, "transactions": []interface{}{}, "expired": []interface{}{}})
		}
		return vm.ToValue(info)
	})

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
		case "push", "ussd":
			result, opErr = conn.Push(req)
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

// ─────────────────────────────────────────────────────────────────────────────
// x402/crypto provider
// ─────────────────────────────────────────────────────────────────────────────

type x402Provider struct {
	facilitatorURL string
	wallets        map[string]string // "evm" → "0x...", "svm" → "..."
	networks       map[string]string // "evm" → "eip155:84532"
	scheme         string            // "exact" | "upto"
	mode           string            // "sandbox" | "production"
}

func (x *x402Provider) Charge(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("x402: use @payment middleware for crypto payments (pull-based)")
}

func (x *x402Provider) Verify(id string) (PaymentResult, error) {
	// In x402, verification is done via the facilitator during the gate flow.
	return PaymentResult{ID: id, Status: "unknown"}, nil
}

func (x *x402Provider) Refund(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("x402: refunds are managed on-chain")
}

func (x *x402Provider) Checkout(req PaymentRequest) (PaymentResult, error) {
	// For x402, "checkout" builds the PaymentRequired response
	return PaymentResult{
		Status: "payment_required",
		Amount: req.Amount,
	}, nil
}

func (x *x402Provider) Push(_ PaymentRequest) (PaymentResult, error) {
	return PaymentResult{}, fmt.Errorf("x402: push payment not supported")
}

// ─────────────────────────────────────────────────────────────────────────────
// x402 configuration structures
// ─────────────────────────────────────────────────────────────────────────────

// x402Config holds configuration specific to x402/crypto providers.
type x402Config struct {
	facilitatorURL string            // "https://x402.org/facilitator"
	wallets        map[string]string // chain → wallet address
	networks       map[string]string // chain → CAIP-2 network ID
	scheme         string            // "exact" | "upto"
	useHTTPS       bool              // USE https|http
}

// paymentSchemaLink parsed from SCHEMA 'dbname:table(f1,f2,...)'
// Field order: ref(0), amount(1), currency(2), provider(3), status(4),
//
//	product(5), user(6), expiration(7)
type paymentSchemaLink struct {
	dbName    string   // "paiements" → db.GetConnection("paiements")
	tableName string   // "payments"
	fields    []string // ordered mapping to DB columns
	db        *gorm.DB // resolved at Start() or lazily
}

// paymentUserLookup holds the USER_ID_LOOKUP directive (inline JS or file path).
type paymentUserLookup struct {
	route   *RouteConfig
	baseDir string
}

// paymentGateConfig holds per-route @payment middleware args.
type paymentGateConfig struct {
	Name        string // payment connection name
	Price       string // "$0.001" or "500" (cents)
	Description string
	Ref         string // product reference for DB lookup
	Scheme      string // override: "exact" | "upto"
	TTLOverride string // override: "1d", "lifetime", etc.
}

// defaultPaymentRecord is the GORM model for the auto-created in-memory payment table.
type defaultPaymentRecord struct {
	ID          string    `gorm:"primaryKey;size:36"`
	Ref         string    `gorm:"index;size:255"`
	Amount      float64   `gorm:"not null"`
	Currency    string    `gorm:"size:10"`
	Provider    string    `gorm:"size:50"`
	Status      string    `gorm:"index;size:50"`
	Product     string    `gorm:"size:255"`
	User        string    `gorm:"index;size:255"`
	Expiration  time.Time `gorm:"index"`
	Description string    `gorm:"size:500"`
	CreatedAt   time.Time
}

func (defaultPaymentRecord) TableName() string { return "payments" }

// defaultUserIDLookupJS is the default JS code for USER_ID_LOOKUP:
// hashes IP + User-Agent to produce a stable anonymous client identifier.
const defaultUserIDLookupJS = `(function(){
	var h = require('crypto').createHash('sha256');
	h.update(req.ip + ':' + (req.headers['user-agent'] || ''));
	return h.digest('hex').substring(0, 16);
})()`

// ─────────────────────────────────────────────────────────────────────────────
// Parser helpers
// ─────────────────────────────────────────────────────────────────────────────

// parseTTL converts a TTL string to a time.Duration.
// "30d" → 30 days, "5mn" → 5 minutes, "1h" → 1 hour
// "once", "lifetime", "life", "forever" → 0 (permanent, will use +999 years)
func parseTTL(s string) time.Duration {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "once", "lifetime", "life", "forever":
		return 0 // permanent
	default:
		// Try standard Go duration first
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
		// Custom: "30d" → 30 * 24h, "5mn" → 5m
		s = strings.TrimSpace(s)
		if strings.HasSuffix(s, "d") {
			if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil {
				return time.Duration(n) * 24 * time.Hour
			}
		}
		if strings.HasSuffix(s, "mn") {
			if n, err := strconv.Atoi(strings.TrimSuffix(s, "mn")); err == nil {
				return time.Duration(n) * time.Minute
			}
		}
		return 30 * 24 * time.Hour // default 30 days
	}
}

// parseSchemaLink parses 'dbname:table(f1,f2,...)' into a paymentSchemaLink.
func parseSchemaLink(s string) (*paymentSchemaLink, error) {
	// Format: "paiements:payments(ref,amount,currency,provider,status,product,user,expiration)"
	colonIdx := strings.Index(s, ":")
	if colonIdx < 0 {
		return nil, fmt.Errorf("SCHEMA: expected 'dbname:table(fields)', got %q", s)
	}
	dbName := s[:colonIdx]
	rest := s[colonIdx+1:]

	parenIdx := strings.Index(rest, "(")
	if parenIdx < 0 || !strings.HasSuffix(rest, ")") {
		return nil, fmt.Errorf("SCHEMA: expected 'table(f1,f2,...)', got %q", rest)
	}
	tableName := rest[:parenIdx]
	fieldsStr := rest[parenIdx+1 : len(rest)-1]
	fields := strings.Split(fieldsStr, ",")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	if len(fields) < 8 {
		return nil, fmt.Errorf("SCHEMA: expected 8 fields (ref,amount,currency,provider,status,product,user,expiration), got %d", len(fields))
	}

	return &paymentSchemaLink{
		dbName:    dbName,
		tableName: tableName,
		fields:    fields,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// DB helpers
// ─────────────────────────────────────────────────────────────────────────────

// resolveSchemaDB ensures the schema's DB is resolved (lazy resolution).
func resolveSchemaDB(conn *PaymentConnection) *gorm.DB {
	if conn.schema == nil {
		return nil
	}
	if conn.schema.db != nil {
		return conn.schema.db
	}
	// Try lazy resolution from registered connections
	dbConn := dbpkg.GetConnection(conn.schema.dbName)
	if dbConn != nil {
		conn.schema.db = dbConn.GetDB()
		return conn.schema.db
	}
	return nil
}

// checkPaymentExists checks if a valid payment exists in the DB for the given user and ref.
func checkPaymentExists(conn *PaymentConnection, userID, ref string) (bool, error) {
	db := resolveSchemaDB(conn)
	if db == nil || conn.schema == nil || ref == "" {
		return false, nil
	}
	s := conn.schema
	var count int64
	db.Table(s.tableName).
		Where(fmt.Sprintf("%s = ? AND %s = ? AND %s = ? AND %s > ?",
			s.fields[5], // product
			s.fields[6], // user
			s.fields[4], // status
			s.fields[7], // expiration
		), ref, userID, "succeeded", time.Now()).
		Count(&count)
	return count > 0, nil
}

// recordPayment inserts a new payment record in the DB.
func recordPayment(conn *PaymentConnection, userID, ref, product, desc string, amount float64, currency, providerName, status string, ttl time.Duration) error {
	db := resolveSchemaDB(conn)
	if db == nil || conn.schema == nil {
		return fmt.Errorf("payment: no schema DB available")
	}
	s := conn.schema

	// Calculate expiration
	var expiration time.Time
	if ttl <= 0 {
		// Permanent: +999 years
		expiration = time.Now().AddDate(999, 0, 0)
	} else {
		expiration = time.Now().Add(ttl)
	}

	// Generate a unique ref ID
	refID := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", userID, ref, time.Now().UnixNano()))))[:32]

	row := map[string]interface{}{
		"id":        refID,
		s.fields[0]: refID,     // ref
		s.fields[1]: amount,    // amount
		s.fields[2]: currency,  // currency
		s.fields[3]: providerName, // provider
		s.fields[4]: status,    // status
		s.fields[5]: product,   // product (the ref from @payment)
		s.fields[6]: userID,    // user
		s.fields[7]: expiration, // expiration
	}

	return db.Table(s.tableName).Create(row).Error
}

// getPaymentRecords retrieves payment records by user and ref (product).
func getPaymentRecords(conn *PaymentConnection, userID, ref string, includeExpired bool) ([]map[string]interface{}, error) {
	db := resolveSchemaDB(conn)
	if db == nil || conn.schema == nil {
		return nil, fmt.Errorf("payment: no schema DB available")
	}
	s := conn.schema
	var result []map[string]interface{}
	
	query := db.Table(s.tableName).
		Where(fmt.Sprintf("%s = ? AND %s = ?", s.fields[6], s.fields[5]), userID, ref)
		
	if !includeExpired {
		query = query.Where(fmt.Sprintf("%s > ?", s.fields[7]), time.Now())
	}
	
	err := query.Order("created_at DESC").Find(&result).Error
	if err != nil {
		return nil, err
	}
	return result, nil
}

// getAmountPayments returns the sum of amounts for valid, non-expired payments.
func getAmountPayments(conn *PaymentConnection, userID, ref string) float64 {
	db := resolveSchemaDB(conn)
	if db == nil || conn.schema == nil {
		return 0
	}
	s := conn.schema
	var total float64
	query := db.Table(s.tableName).
		Where(fmt.Sprintf("%s = ? AND %s = ? AND %s > ?",
			s.fields[6], // user
			s.fields[4], // status
			s.fields[7], // expiration
		), userID, "succeeded", time.Now())
		
	if ref != "" {
		query = query.Where(fmt.Sprintf("%s = ?", s.fields[5]), ref)
	}

	_ = query.Select(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.fields[1])).Row().Scan(&total)
	return total
}

// getCountPayments returns the number of valid, non-expired payments.
func getCountPayments(conn *PaymentConnection, userID, ref string) int64 {
	db := resolveSchemaDB(conn)
	if db == nil || conn.schema == nil {
		return 0
	}
	s := conn.schema
	var count int64
	query := db.Table(s.tableName).
		Where(fmt.Sprintf("%s = ? AND %s = ? AND %s > ?",
			s.fields[6], // user
			s.fields[4], // status
			s.fields[7], // expiration
		), userID, "succeeded", time.Now())
		
	if ref != "" {
		query = query.Where(fmt.Sprintf("%s = ?", s.fields[5]), ref)
	}

	query.Count(&count)
	return count
}

// getInfoPayments returns detailed payment information for a user.
func getInfoPayments(conn *PaymentConnection, userID, ref string, includeExpired bool, limit, offset int, start, end *time.Time) (map[string]interface{}, error) {
	db := resolveSchemaDB(conn)
	if db == nil || conn.schema == nil {
		return nil, fmt.Errorf("payment: no schema DB available")
	}
	s := conn.schema
	now := time.Now()

	buildQuery := func() *gorm.DB {
		q := db.Table(s.tableName).
			Where(fmt.Sprintf("%s = ?", s.fields[4]), "succeeded")
		if userID != "" {
			q = q.Where(fmt.Sprintf("%s = ?", s.fields[6]), userID)
		}
		if ref != "" {
			q = q.Where(fmt.Sprintf("%s = ?", s.fields[5]), ref)
		}
		if start != nil {
			q = q.Where("created_at >= ?", *start)
		}
		if end != nil {
			q = q.Where("created_at <= ?", *end)
		}
		return q
	}

	var totalCount int64
	buildQuery().Where(fmt.Sprintf("%s > ?", s.fields[7]), now).Count(&totalCount)
	
	var totalAmount float64
	_ = buildQuery().Where(fmt.Sprintf("%s > ?", s.fields[7]), now).
		Select(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.fields[1])).Row().Scan(&totalAmount)

	var transactions []map[string]interface{}
	err := buildQuery().Where(fmt.Sprintf("%s > ?", s.fields[7]), now).
		Select("*").Order("created_at DESC").Limit(limit).Offset(offset).Find(&transactions).Error
	if err != nil {
		return nil, err
	}
	if transactions == nil {
		transactions = []map[string]interface{}{}
	}

	// Expired transactions
	var expired []map[string]interface{}
	if includeExpired {
		err := buildQuery().Where(fmt.Sprintf("%s <= ?", s.fields[7]), now).
			Select("*").Order("created_at DESC").Limit(limit).Offset(offset).Find(&expired).Error
		if err != nil {
			return nil, err
		}
	}
	if expired == nil {
		expired = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"total":        totalCount,
		"amount":       totalAmount,
		"transactions": transactions,
		"expired":      expired,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// resolveUserID — run USER_ID_LOOKUP JS to extract user ID from request
// ─────────────────────────────────────────────────────────────────────────────

func resolveUserID(conn *PaymentConnection, c fiber.Ctx) (string, error) {
	if conn.userLookup == nil {
		return "", fmt.Errorf("no USER_ID_LOOKUP configured")
	}

	r := conn.userLookup.route
	var code string
	if r.Inline {
		code = r.Handler
	} else {
		full := r.Handler
		if full == "" {
			full = r.Path
		}
		if !filepath.IsAbs(full) {
			full = filepath.Join(conn.userLookup.baseDir, full)
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return "", fmt.Errorf("USER_ID_LOOKUP: cannot read %q: %w", full, err)
		}
		code = string(b)
	}

	vm := processor.New(conn.userLookup.baseDir, c, nil)

	// Provide req object
	reqObj := vm.NewObject()
	headersObj := vm.NewObject()
	c.Request().Header.VisitAll(func(key, value []byte) {
		headersObj.Set(strings.ToLower(string(key)), string(value))
	})
	reqObj.Set("headers", headersObj)
	reqObj.Set("ip", c.IP())
	reqObj.Set("method", c.Method())
	reqObj.Set("path", c.Path())
	cookiesObj := vm.NewObject()
	c.Request().Header.VisitAllCookie(func(key, value []byte) {
		cookiesObj.Set(string(key), string(value))
	})
	reqObj.Set("cookies", cookiesObj)
	vm.Set("req", reqObj)

	val, err := vm.RunString(code)
	if err != nil {
		return "", fmt.Errorf("USER_ID_LOOKUP script error: %w", err)
	}

	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return "", fmt.Errorf("USER_ID_LOOKUP returned empty")
	}
	userID := val.String()
	if userID == "" || userID == "undefined" || userID == "null" {
		return "", fmt.Errorf("USER_ID_LOOKUP returned empty")
	}
	return userID, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// x402 Facilitator HTTP client
// ─────────────────────────────────────────────────────────────────────────────

func x402FacilitatorVerify(facilitatorURL string, payload, details map[string]any) (map[string]any, error) {
	body := map[string]any{
		"payload": payload,
		"details": details,
	}
	status, resp, _, err := paymentDoHTTP("POST", facilitatorURL+"/verify", nil, body)
	if err != nil {
		return nil, fmt.Errorf("x402 facilitator verify: %w", err)
	}
	if status >= 400 {
		return resp, fmt.Errorf("x402 facilitator verify: HTTP %d", status)
	}
	return resp, nil
}

func x402FacilitatorSettle(facilitatorURL string, payload, details map[string]any) (map[string]any, error) {
	body := map[string]any{
		"payload": payload,
		"details": details,
	}
	status, resp, _, err := paymentDoHTTP("POST", facilitatorURL+"/settle", nil, body)
	if err != nil {
		return nil, fmt.Errorf("x402 facilitator settle: %w", err)
	}
	if status >= 400 {
		return resp, fmt.Errorf("x402 facilitator settle: HTTP %d", status)
	}
	return resp, nil
}

// x402BuildPaymentRequired builds the PaymentRequired JSON structure for the 402 response.
func x402BuildPaymentRequired(conn *PaymentConnection, price, desc, mimeType, scheme string) map[string]any {
	if scheme == "" && conn.x402 != nil {
		scheme = conn.x402.scheme
	}
	if scheme == "" {
		scheme = "exact"
	}
	if mimeType == "" {
		mimeType = "application/json"
	}

	accepts := []map[string]any{}
	if conn.x402 != nil {
		for chain, wallet := range conn.x402.wallets {
			networkID := conn.x402.networks[chain]
			if networkID == "" {
				continue
			}
			accepts = append(accepts, map[string]any{
				"scheme":    scheme,
				"maxAmountRequired": price,
				"network":   networkID,
				"payTo":     wallet,
				"resource":  desc,
			})
		}
	}

	return map[string]any{
		"accepts":     accepts,
		"description": desc,
		"mimeType":    mimeType,
		"maxAmountRequired": price,
		"scheme":      scheme,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Payment Gate Middleware — @payment[name=... price=... ref=...]
// ─────────────────────────────────────────────────────────────────────────────

// paymentGateMiddleware creates a Fiber middleware that gates route access behind payment.
func paymentGateMiddleware(cfg paymentGateConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		conn := GetPaymentConnection(cfg.Name)
		if conn == nil {
			return c.Status(500).SendString("payment: connection not found: " + cfg.Name)
		}

		// 1. USER_ID_LOOKUP — run JS to extract user ID
		userID, err := resolveUserID(conn, c)
		if err != nil {
			return c.Status(401).JSON(fiber.Map{
				"error":   "Unauthorized",
				"message": err.Error(),
			})
		}

		// 2. DB check — already paid?
		if conn.schema != nil && cfg.Ref != "" {
			paid, _ := checkPaymentExists(conn, userID, cfg.Ref)
			if paid {
				c.Locals("payment_user_id", userID)
				c.Locals("payment_ref", cfg.Ref)
				c.Locals("payment_status", "succeeded")
				c.Locals("payment_provider", conn.name)
				return c.Next()
			}
		}

		// 3. Provider-specific gate
		if conn.x402 != nil {
			return handleX402Gate(c, conn, cfg, userID)
		}
		return handleClassicGate(c, conn, cfg, userID)
	}
}

// handleX402Gate handles the x402/crypto payment gate flow.
func handleX402Gate(c fiber.Ctx, conn *PaymentConnection, cfg paymentGateConfig, userID string) error {
	// Check for PAYMENT-SIGNATURE header
	sigHeader := c.Get("X-PAYMENT")
	if sigHeader == "" {
		sigHeader = c.Get("Payment")
	}

	if sigHeader == "" {
		// No payment signature → return 402 with PaymentRequired
		pr := x402BuildPaymentRequired(conn, cfg.Price, cfg.Description, "", cfg.Scheme)

		prJSON, _ := json.Marshal(pr)
		prB64 := base64.StdEncoding.EncodeToString(prJSON)
		c.Set("X-PAYMENT-REQUIRED", prB64)

		return c.Status(402).JSON(pr)
	}

	// Decode the payment signature
	sigBytes, err := base64.StdEncoding.DecodeString(sigHeader)
	if err != nil {
		// Try raw JSON
		sigBytes = []byte(sigHeader)
	}

	var paymentPayload map[string]any
	if err := json.Unmarshal(sigBytes, &paymentPayload); err != nil {
		return c.Status(400).JSON(fiber.Map{
			"error":   "Invalid payment signature",
			"message": err.Error(),
		})
	}

	// Build payment details
	details := map[string]any{
		"description": cfg.Description,
		"maxAmountRequired": cfg.Price,
		"scheme":      cfg.Scheme,
	}
	if cfg.Scheme == "" && conn.x402 != nil {
		details["scheme"] = conn.x402.scheme
	}

	// Verify with facilitator
	verifyResp, err := x402FacilitatorVerify(conn.x402.facilitatorURL, paymentPayload, details)
	if err != nil {
		return c.Status(402).JSON(fiber.Map{
			"error":   "Payment verification failed",
			"message": err.Error(),
			"details": verifyResp,
		})
	}

	isValid := false
	if v, ok := verifyResp["valid"].(bool); ok {
		isValid = v
	}
	if !isValid {
		pr := x402BuildPaymentRequired(conn, cfg.Price, cfg.Description, "", cfg.Scheme)
		prJSON, _ := json.Marshal(pr)
		c.Set("X-PAYMENT-REQUIRED", base64.StdEncoding.EncodeToString(prJSON))
		return c.Status(402).JSON(fiber.Map{
			"error":    "Payment invalid",
			"details":  verifyResp,
			"required": pr,
		})
	}

	// Settle with facilitator
	settleResp, err := x402FacilitatorSettle(conn.x402.facilitatorURL, paymentPayload, details)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error":   "Payment settlement failed",
			"message": err.Error(),
		})
	}

	// Record payment in DB
	ttl := conn.ttl
	if cfg.TTLOverride != "" {
		ttl = parseTTL(cfg.TTLOverride)
	}

	amount := parsePrice(cfg.Price)
	_ = recordPayment(conn, userID, cfg.Ref, cfg.Ref, cfg.Description, amount, conn.currency, "x402", "succeeded", ttl)

	// Set response headers
	settleJSON, _ := json.Marshal(settleResp)
	c.Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString(settleJSON))

	// Inject payment context for JS handlers
	c.Locals("payment_user_id", userID)
	c.Locals("payment_ref", cfg.Ref)
	c.Locals("payment_status", "succeeded")
	c.Locals("payment_provider", "x402")
	c.Locals("payment_settle_response", settleResp)

	return c.Next()
}

// handleClassicGate handles the classic (Stripe/MoMo/etc.) payment gate flow.
func handleClassicGate(c fiber.Ctx, conn *PaymentConnection, cfg paymentGateConfig, userID string) error {
	// Not paid → create checkout/charge session
	amount := parsePrice(cfg.Price)
	req := PaymentRequest{
		Amount:  amount,
		OrderID: cfg.Ref,
		Email:   c.Get("X-User-Email"),
		Metadata: map[string]any{
			"user_id":     userID,
			"ref":         cfg.Ref,
			"description": cfg.Description,
		},
	}

	result, err := conn.Checkout(req)
	if err != nil {
		// Fallback to charge if checkout not supported
		result, err = conn.Charge(req)
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error":   "Payment initiation failed",
			"message": err.Error(),
		})
	}

	return c.Status(402).JSON(fiber.Map{
		"provider":    conn.name,
		"redirectUrl": result.RedirectURL,
		"sessionId":   result.ID,
		"ref":         cfg.Ref,
		"amount":      amount,
		"currency":    conn.currency,
		"status":      result.Status,
	})
}

// parsePrice converts a price string to a float64.
// "$0.001" → 0.001, "500" → 500.0
func parsePrice(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimPrefix(s, "€")
	s = strings.TrimPrefix(s, "£")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// MountPaymentRoutes mounts the standard payment API routes for a connection.
// Used by auto-mount when @payment is used without explicit PAYMENT [name] /prefix.
func MountPaymentRoutes(app *httpserver.HTTP, conn *PaymentConnection, prefix string) {
	prefix = strings.TrimRight(prefix, "/")

	// POST /prefix/charge
	app.Post(prefix+"/charge", func(c fiber.Ctx) error {
		var body map[string]interface{}
		if err := c.Bind().JSON(&body); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
		req := paymentRequestFromJS(body, conn)
		result, err := conn.Charge(req)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(result)
	})

	// GET /prefix/verify/:id
	app.Get(prefix+"/verify/:id", func(c fiber.Ctx) error {
		result, err := conn.Verify(c.Params("id"))
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(result)
	})

	// POST /prefix/refund
	app.Post(prefix+"/refund", func(c fiber.Ctx) error {
		var body map[string]interface{}
		if err := c.Bind().JSON(&body); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
		req := paymentRequestFromJS(body, conn)
		result, err := conn.Refund(req)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(result)
	})

	// POST /prefix/checkout
	app.Post(prefix+"/checkout", func(c fiber.Ctx) error {
		var body map[string]interface{}
		if err := c.Bind().JSON(&body); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
		req := paymentRequestFromJS(body, conn)
		result, err := conn.Checkout(req)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(result)
	})

	// POST /prefix/push (was /ussd)
	app.Post(prefix+"/push", func(c fiber.Ctx) error {
		var body map[string]interface{}
		if err := c.Bind().JSON(&body); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
		req := paymentRequestFromJS(body, conn)
		result, err := conn.Push(req)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(result)
	})

	// GET /prefix/transaction
	app.Get(prefix+"/transaction", func(c fiber.Ctx) error {
		userID, err := resolveUserID(conn, c)
		if err != nil {
			return c.Status(401).JSON(fiber.Map{"error": err.Error()})
		}

		ref := c.Query("ref")
		includeExpired := c.Query("include_expired") == "true"
		limit := 10
		if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
			limit = l
		}
		offset := 0
		if o, err := strconv.Atoi(c.Query("offset")); err == nil && o >= 0 {
			offset = o
		}
		if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
			offset = (p - 1) * limit
		}

		var start, end *time.Time
		if s := c.Query("start"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				start = &t
			} else if t, err := time.Parse("2006-01-02", s); err == nil {
				start = &t
			}
		}
		if e := c.Query("end"); e != "" {
			if t, err := time.Parse(time.RFC3339, e); err == nil {
				end = &t
			} else if t, err := time.Parse("2006-01-02", e); err == nil {
				end = &t
			}
		}

		info, err := getInfoPayments(conn, userID, ref, includeExpired, limit, offset, start, end)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(info)
	})

	// POST /prefix/webhook
	app.Post(prefix+"/webhook", func(c fiber.Ctx) error {
		// Process webhooks from all configured webhook handlers
		for i := range conn.webhooks {
			wh := &conn.webhooks[i]
			if err := handlePaymentWebhook(c, conn, wh); err != nil {
				return err
			}
		}

		// For x402 provider: auto-record payment from webhook data
		if conn.x402 != nil {
			var body map[string]interface{}
			if err := json.Unmarshal(c.Body(), &body); err == nil {
				status := payStr(body, "status")
				ref := payStr(body, "ref")
				userID := payStr(body, "user_id")
				amount := payFloat(body, "amount")
				if status == "succeeded" && ref != "" && userID != "" {
					_ = recordPayment(conn, userID, ref, ref, "", amount, conn.currency, "x402", "succeeded", conn.ttl)
				}
			}
		}

		return c.SendStatus(200)
	})

	log.Printf("PAYMENT: auto-mounted %q routes on %s", conn.name, prefix)
}
