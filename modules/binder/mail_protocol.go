package binder

// mail_protocol.go — MAIL directive for sending emails from .bind files.
//
// ─────────────────────────────────────────────────────────────────────────────
// DSL
// ─────────────────────────────────────────────────────────────────────────────
//
//   MAIL 'smtp://host:587' [default]
//       NAME mailer
//       USER user password
//       USE  TLS                        // TLS | SSL | PLAIN
//
//       // FROM — static, file, or inline
//       FROM noreply@example.com
//       FROM "senders/from.js" [env=prod]
//       FROM BEGIN [env=prod]
//           // `email` is available (subject, from, to, cc, bcc, content, headers)
//           return email.to[0].endsWith("@vip.com")
//               ? "vip@example.com"
//               : "noreply@example.com"
//       END FROM
//
//       SET fromName "My App"
//
//       // PROCESSOR — pre/post middleware
//       PROCESSOR @PRE  "validators/spam.js" [args...]
//       PROCESSOR @POST BEGIN [args...]
//           // `email`   — {subject,from,to,cc,bcc,content,headers}  (pre: content="")
//           // `request` — {url,method,headers,body,query} for REST backends
//           // `args`    — route arguments
//           if (!email.to.length) reject("no recipients")
//           email.subject = "[APP] " + email.subject
//       END PROCESSOR
//
//       TEMPLATE welcome BEGIN
//           <h1>Hello {{name}}!</h1>
//       END TEMPLATE
//       TEMPLATE invoice "emails/invoice.html"
//   END MAIL
//
//   MAIL 'sendgrid://SG.xxxx'
//       NAME sg
//   END MAIL
//
//   MAIL 'mailgun://apikey@mg.domain.com'
//       NAME mg
//   END MAIL
//
//   MAIL 'postmark://serverToken'
//       NAME pm
//   END MAIL
//
//   MAIL 'rest://https://api.example.com/send'
//       NAME custom
//       // Static: HEADER|BODY|QUERY Key Value
//       HEADER Authorization "Bearer token"
//       BODY   source myapp
//       QUERY  version v2
//       // File: HEADER|BODY|QUERY "filepath" [args...]
//       HEADER "headers/auth.js" [env=prod]
//       // Inline: HEADER|BODY|QUERY BEGIN [args...] ... END HEADER|BODY|QUERY
//       HEADER BEGIN [ts=true]
//           append("X-Timestamp", Date.now().toString())
//       END HEADER
//       BODY BEGIN [region=eu]
//           append("region", args.region)
//       END BODY
//       QUERY "qs.js"
//       METHOD POST
//   END MAIL
//
// ─────────────────────────────────────────────────────────────────────────────
// JS usage
// ─────────────────────────────────────────────────────────────────────────────
//
//   const mail = require('mail');
//   const sg   = require('mail').get('sg');
//
//   await mail.send({
//       to: "user@example.com", subject: "Hi",
//       template: "welcome", data: { name: "Alice" },
//   });
//
//   mail.send({ to: "u@e.com", subject: "x", html: "<b>hi</b>" })
//       .then(() => print("sent"))
//       .catch(e  => print("error: " + e));
//
//   await mail.send({
//       to: "u@e.com",
//       subject: "Rapport",
//       html: "<p>Voir PJ</p>",
//       attachments: [
//           mail.attachment("reports/q3.pdf"),
//           mail.attachment("/abs/path/logo.png").name("logo.png").type("image/png"),
//       ]
//   })

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"http-server/modules"
	"http-server/processor"

	"github.com/dop251/goja"
)

// ─────────────────────────────────────────────────────────────────────────────
// MailMessage
// ─────────────────────────────────────────────────────────────────────────────

type MailMessage struct {
	To          []string
	Cc          []string
	Bcc         []string
	From        string
	FromName    string
	ReplyTo     string
	Subject     string
	Text        string
	HTML        string
	Template    string            // registered TEMPLATE name
	Data        map[string]any    // template variables
	Headers     map[string]string // per-message headers
	Attachments []MailAttachment
}

type MailAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ─────────────────────────────────────────────────────────────────────────────
// mailFileAttachment — lazy file-based attachment (resolved at send time)
// ─────────────────────────────────────────────────────────────────────────────

// mailFileAttachmentKey is stored in the JS object so sendFromJS can recognise it.
const mailFileAttachmentKey = "__mailFilePath"

// mailAttachmentFromJS converts one element of the JS `attachments` array into a
// MailAttachment. It supports two shapes:
//
//	a) Objects created by mail.attachment(path) / conn.attachment(path):
//	     { __mailFilePath: "/abs/or/rel/path", filename: "...", contentType: "..." }
//	   The file is read from disk here; an error is returned if it does not exist.
//
//	b) Plain objects with a `data` field (base64 string, ArrayBuffer, or []byte).
func mailAttachmentFromJS(raw map[string]interface{}, baseDir string) (MailAttachment, error) {
	// Case (a) — lazy file attachment
	if filePath, ok := raw[mailFileAttachmentKey].(string); ok && filePath != "" {
		if !filepath.IsAbs(filePath) {
			filePath = filepath.Join(baseDir, filePath)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return MailAttachment{}, fmt.Errorf("mail attachment: cannot read %q: %w", filePath, err)
		}
		ma := MailAttachment{
			Filename:    mapStr(raw, "filename"),
			ContentType: mapStr(raw, "contentType"),
			Data:        data,
		}
		if ma.Filename == "" {
			ma.Filename = filepath.Base(filePath)
		}
		return ma, nil
	}

	// Case (b) — raw data attachment
	ma := MailAttachment{
		Filename:    mapStr(raw, "filename"),
		ContentType: mapStr(raw, "contentType"),
	}
	switch d := raw["data"].(type) {
	case string:
		if decoded, err := base64.StdEncoding.DecodeString(d); err == nil {
			ma.Data = decoded
		} else {
			ma.Data = []byte(d)
		}
	case []byte:
		ma.Data = d
	case goja.ArrayBuffer:
		ma.Data = d.Bytes()
	}
	if ma.Data == nil {
		return MailAttachment{}, fmt.Errorf("mail attachment: missing or empty data")
	}
	return ma, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// MailSender
// ─────────────────────────────────────────────────────────────────────────────

type MailSender interface {
	Send(msg MailMessage) error
}

// ─────────────────────────────────────────────────────────────────────────────
// mailRoute — a stored RouteConfig with its base directory
// ─────────────────────────────────────────────────────────────────────────────

type mailRoute struct {
	route   *RouteConfig
	baseDir string
}

// runScript executes a mailRoute's JS code in vm.
// Returns (returnValue, error).
func (r *mailRoute) runScript(vm *goja.Runtime) (goja.Value, error) {
	var code string
	if r.route.Inline {
		code = r.route.Handler
	} else {
		fullPath := r.route.Handler
		if fullPath == "" {
			fullPath = r.route.Path
		}
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(r.baseDir, fullPath)
		}
		b, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("cannot read %q: %w", fullPath, err)
		}
		code = string(b)
	}
	// Wrap in a function to support top-level 'return'
	return vm.RunString("(function(){ " + code + " })()")
}

// ─────────────────────────────────────────────────────────────────────────────
// mailTemplate
// ─────────────────────────────────────────────────────────────────────────────

type mailTemplate struct {
	mailRoute
	// locked is true when the template was defined in the .bind config.
	// Locked templates cannot be modified or deleted from JS.
	locked bool
}

// ─────────────────────────────────────────────────────────────────────────────
// mailFromRoute — FROM directive (static email, file JS, or inline JS)
//
// • Static:  FROM email@example.com      → route.Path = email, Inline=false, Handler=""
// • File:    FROM "from.js" [args...]    → route.Inline=false, route.Handler=filepath
// • Inline:  FROM BEGIN ... END FROM    → route.Inline=true,  route.Handler=code
//
// When JS (file or inline): the script receives `email` (the current MailMessage
// as a read-only JS object) and `args`. It must return a string — the from address.
// ─────────────────────────────────────────────────────────────────────────────

type mailFromRoute struct{ mailRoute }

// resolve evaluates the FROM directive and returns the sender address.
func (f *mailFromRoute) resolve(msg *MailMessage) (string, error) {
	r := f.route

	// Static: Path holds the email address (if not file-like)
	if !r.Inline && r.Handler == "" && r.Path != "" && !IsFileLike(r.Path) {
		return r.Path, nil
	}

	// JS (file or inline) — use processor.New so all built-in modules are available
	vm := processor.New(f.baseDir, nil, nil)
	setEmailVar(vm.Runtime, msg, false) // pre-template: content may be empty

	argsObj := vm.NewObject()
	for k, v := range r.Args {
		argsObj.Set(k, v)
	}
	vm.Set("args", argsObj)

	val, err := f.runScript(vm.Runtime)
	if err != nil {
		return "", fmt.Errorf("mail FROM script error: %w", err)
	}
	if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
		return strings.TrimSpace(val.String()), nil
	}
	return "", nil
}

// ─────────────────────────────────────────────────────────────────────────────
// mailProcessorPhase / mailProcessor
// ─────────────────────────────────────────────────────────────────────────────

type mailProcessorPhase string

const (
	mailPre  mailProcessorPhase = "PRE"
	mailPost mailProcessorPhase = "POST"
)

// mailProcessor is one PROCESSOR directive.
// It mutates *MailMessage (and optionally *mailRequest for REST POST processors)
// via the JS variables `email` and `request`.
type mailProcessor struct {
	phase mailProcessorPhase
	mailRoute
}

// run executes the processor script.
//
// Exposed JS variables:
//
//	email   — {subject, from, fromName, replyTo, to, cc, bcc, content, headers}
//	          'content' is HTML (empty for @PRE before template rendering)
//	request — {url, method, headers, body, query}  (non-nil only for REST @POST)
//	args    — route arguments
//	reject(reason) — aborts the pipeline
//	done()         — no-op explicit success
func (p *mailProcessor) run(msg *MailMessage, req *mailRequest) error {
	vm := processor.New(p.baseDir, nil, nil)

	// email variable
	isPre := p.phase == mailPre
	emailObj := setEmailVar(vm.Runtime, msg, isPre)

	// request variable (may be nil for SMTP/SG/etc.)
	if req != nil {
		reqObj := vm.NewObject()
		reqObj.Set("url", req.url)
		reqObj.Set("method", req.method)
		hObj := vm.NewObject()
		for k, v := range req.headers {
			hObj.Set(k, v)
		}
		reqObj.Set("headers", hObj)
		bObj := vm.NewObject()
		for k, v := range req.body {
			bObj.Set(k, v)
		}
		reqObj.Set("body", bObj)
		qObj := vm.NewObject()
		for k, v := range req.query {
			qObj.Set(k, v)
		}
		reqObj.Set("query", qObj)
		vm.Set("request", reqObj)
	} else {
		vm.Set("request", goja.Null())
	}

	// args
	argsObj := vm.NewObject()
	for k, v := range p.route.Args {
		argsObj.Set(k, v)
	}
	vm.Set("args", argsObj)

	// Control functions
	rejected := ""
	vm.Set("reject", func(reason string) { rejected = reason })
	vm.Set("done", func() {})

	if _, err := p.runScript(vm.Runtime); err != nil {
		return fmt.Errorf("mail PROCESSOR: %w", err)
	}
	if rejected != "" {
		return fmt.Errorf("mail PROCESSOR rejected: %s", rejected)
	}

	// Sync email mutations back to msg
	syncEmailVar(vm.Runtime, emailObj, msg)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// mailRequest — the outgoing REST request, mutable by POST processors
// ─────────────────────────────────────────────────────────────────────────────

type mailRequest struct {
	url     string
	method  string
	headers map[string]string
	body    map[string]any
	query   map[string]string
}

// ─────────────────────────────────────────────────────────────────────────────
// mailMapRoute — a HEADER / BODY / QUERY directive
//
// Three forms (parallel to the general DSL):
//   Static:  HEADER Key Value        → Path=Key, Handler=Value, Inline=false
//   File:    HEADER "file.js" [args] → Path="", Handler=filepath, Inline=false
//   Inline:  HEADER BEGIN ... END    → Inline=true, Handler=code
//
// JS env for file/inline: `append(key, value)` + `args`
// ─────────────────────────────────────────────────────────────────────────────

type mailMapRoute struct{ mailRoute }

// eval populates dst by executing the route (static, file, or inline).
func (m *mailMapRoute) eval(dst map[string]string) error {
	r := m.route

	// Static: HEADER Key Value  →  Path=Key, Handler=Value, !Inline
	// If Handler is empty and Path is file-like, it's a script.
	if !r.Inline && r.Path != "" && r.Handler != "" && !IsFileLike(r.Handler) {
		dst[r.Path] = r.Handler
		return nil
	}
	if !r.Inline && r.Path != "" && r.Handler == "" && !IsFileLike(r.Path) {
		dst[r.Path] = ""
		return nil
	}

	// File or inline JS — use processor.New so all built-in modules are available
	vm := processor.New(m.baseDir, nil, nil)
	argsObj := vm.NewObject()
	for k, v := range r.Args {
		argsObj.Set(k, v)
	}
	vm.Set("args", argsObj)
	vm.Set("append", func(key, value string) { dst[key] = value })

	if _, err := m.runScript(vm.Runtime); err != nil {
		return fmt.Errorf("mail map route script error: %w", err)
	}
	return nil
}

// IsFileLike returns true if s looks like a file path (has / or extension).

// ─────────────────────────────────────────────────────────────────────────────
// MailConnection
// ─────────────────────────────────────────────────────────────────────────────

type MailConnection struct {
	name       string
	sender     MailSender
	from       string // static default from (may be overridden by fromRoute)
	fromName   string
	fromRoute  *mailFromRoute // optional dynamic FROM directive
	templates  map[string]mailTemplate
	processors []mailProcessor
	baseDir    string
}

// SetTemplate registers or replaces a template on the connection.
// Returns an error if the template is locked (defined in .bind config).
// src is the raw template source (Mustache/JS string).
// filePath, if non-empty, means the template is file-based (src is ignored — loaded at render time).
func (c *MailConnection) SetTemplate(name, src, filePath string) error {
	if mt, exists := c.templates[name]; exists && mt.locked {
		return fmt.Errorf("mail: template %q is locked (defined in config) and cannot be modified", name)
	}
	var route *RouteConfig
	if filePath != "" {
		route = &RouteConfig{Path: name, Inline: false, Handler: filePath}
	} else {
		route = &RouteConfig{Path: name, Inline: true, Handler: src}
	}
	c.templates[name] = mailTemplate{
		mailRoute: mailRoute{route: route, baseDir: c.baseDir},
		locked:    false, // JS-created templates are never locked
	}
	return nil
}

// DeleteTemplate removes a template by name.
// Returns an error if the template is locked.
func (c *MailConnection) DeleteTemplate(name string) error {
	mt, exists := c.templates[name]
	if !exists {
		return fmt.Errorf("mail: template %q not found", name)
	}
	if mt.locked {
		return fmt.Errorf("mail: template %q is locked (defined in config) and cannot be deleted", name)
	}
	delete(c.templates, name)
	return nil
}

// HasTemplate reports whether a template with the given name is registered.
func (c *MailConnection) HasTemplate(name string) bool {
	_, ok := c.templates[name]
	return ok
}

// TemplateNames returns all registered template names.
func (c *MailConnection) TemplateNames() []string {
	names := make([]string, 0, len(c.templates))
	for n := range c.templates {
		names = append(names, n)
	}
	return names
}

// Send runs: resolve FROM → PRE processors → render template → send → POST processors.
func (c *MailConnection) Send(msg MailMessage) error {
	// Defaults
	if msg.From == "" {
		if c.fromRoute != nil {
			resolved, err := c.fromRoute.resolve(&msg)
			if err != nil {
				return fmt.Errorf("mail FROM: %w", err)
			}
			if resolved != "" {
				msg.From = resolved
			}
		}
		if msg.From == "" {
			msg.From = c.from
		}
	}
	if msg.FromName == "" {
		msg.FromName = c.fromName
	}

	// PRE processors (content is empty at this stage)
	for i := range c.processors {
		if c.processors[i].phase == mailPre {
			if err := c.processors[i].run(&msg, nil); err != nil {
				return err
			}
		}
	}

	// Template rendering via processor (Mustache + JS)
	if msg.Template != "" {
		mt, ok := c.templates[msg.Template]
		if !ok {
			return fmt.Errorf("mail: template %q not registered", msg.Template)
		}
		settings := make(map[string]string, len(msg.Data))
		for k, v := range msg.Data {
			settings[k] = fmt.Sprint(v)
		}
		var html string
		var err error
		if mt.route.Inline {
			html, err = processor.ProcessString(mt.route.Handler, mt.baseDir, nil, nil, settings)
		} else {
			fullPath := mt.route.Handler
			if !filepath.IsAbs(fullPath) {
				fullPath = filepath.Join(mt.baseDir, fullPath)
			}
			html, err = processor.ProcessFile(fullPath, nil, nil, settings)
		}
		if err != nil {
			return fmt.Errorf("mail: template %q render error: %w", msg.Template, err)
		}
		msg.HTML = html
	}

	// Actual send (REST sender receives a *mailRequest it may need to expose to POST processors)
	var req *mailRequest
	if rs, ok := c.sender.(*restSender); ok {
		r, err := rs.buildRequest(msg)
		if err != nil {
			return err
		}
		req = r
		// POST processors see the built request before it is fired
		for i := range c.processors {
			if c.processors[i].phase == mailPost {
				if err := c.processors[i].run(&msg, req); err != nil {
					log.Printf("mail: POST processor error (non-fatal): %v", err)
				}
			}
		}
		return rs.fireRequest(req)
	}

	if err := c.sender.Send(msg); err != nil {
		return err
	}

	// POST processors for non-REST senders
	for i := range c.processors {
		if c.processors[i].phase == mailPost {
			if err := c.processors[i].run(&msg, nil); err != nil {
				log.Printf("mail: POST processor error (non-fatal): %v", err)
			}
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// JS email variable helpers
// ─────────────────────────────────────────────────────────────────────────────

// setEmailVar creates the `email` JS object from a MailMessage and sets it on vm.
// When isPre=true the content fields (html/text) are exposed as "".
func setEmailVar(vm *goja.Runtime, msg *MailMessage, isPre bool) *goja.Object {
	obj := vm.NewObject()
	obj.Set("subject", msg.Subject)
	obj.Set("from", msg.From)
	obj.Set("fromName", msg.FromName)
	obj.Set("replyTo", msg.ReplyTo)
	obj.Set("to", msg.To)
	obj.Set("cc", msg.Cc)
	obj.Set("bcc", msg.Bcc)
	if isPre {
		obj.Set("content", "")
	} else {
		obj.Set("content", msg.HTML)
	}
	headersObj := vm.NewObject()
	for k, v := range msg.Headers {
		headersObj.Set(k, v)
	}
	obj.Set("headers", headersObj)
	vm.Set("email", obj)
	return obj
}

// syncEmailVar reads back mutations from the JS `email` object into msg.
func syncEmailVar(vm *goja.Runtime, obj *goja.Object, msg *MailMessage) {
	msg.Subject = jsStr(obj, "subject", msg.Subject)
	msg.From = jsStr(obj, "from", msg.From)
	msg.FromName = jsStr(obj, "fromName", msg.FromName)
	msg.ReplyTo = jsStr(obj, "replyTo", msg.ReplyTo)
	msg.HTML = jsStr(obj, "content", msg.HTML)
	msg.To = jsStrSlice(obj, "to", msg.To)
	msg.Cc = jsStrSlice(obj, "cc", msg.Cc)
	msg.Bcc = jsStrSlice(obj, "bcc", msg.Bcc)

	if hObj, ok := obj.Get("headers").(*goja.Object); ok {
		if msg.Headers == nil {
			msg.Headers = make(map[string]string)
		}
		for _, k := range hObj.Keys() {
			msg.Headers[k] = hObj.Get(k).String()
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Global connection registry
// ─────────────────────────────────────────────────────────────────────────────

var (
	mailConns       = make(map[string]*MailConnection)
	defaultMailConn *MailConnection
)

func registerMailConnection(name string, conn *MailConnection, isDefault bool) {
	mailConns[name] = conn
	if isDefault || defaultMailConn == nil || name == "" {
		defaultMailConn = conn
	}
}

// GetMailConnection returns the named connection, or the default one.
func GetMailConnection(name ...string) *MailConnection {
	if len(name) == 0 || name[0] == "" {
		return defaultMailConn
	}
	return mailConns[name[0]]
}

// ─────────────────────────────────────────────────────────────────────────────
// MailDirective — implements binder.Directive
// ─────────────────────────────────────────────────────────────────────────────

type MailDirective struct {
	config *DirectiveConfig
	conns  []*MailConnection
}

func NewMailDirective(c *DirectiveConfig) (*MailDirective, error) {
	processor.RegisterGlobal("mail", &MailModule{}, true) // set globlal when directive is defined
	return &MailDirective{config: c}, nil
}

func (d *MailDirective) Name() string                    { return "MAIL" }
func (d *MailDirective) Address() string                 { return d.config.Address }
func (d *MailDirective) Match(peek []byte) (bool, error) { return false, nil }
func (d *MailDirective) Handle(conn net.Conn) error {
	// Simple TCP handler for mail (SMTP-like)
	return nil
}

func (d *MailDirective) HandlePacket(data []byte, addr net.Addr, pc net.PacketConn) error {
	return errors.New("Mail protocol does not support UDP")
}

func (d *MailDirective) Close() error {
	for _, c := range d.conns {
		processor.UnregisterGlobal(c.name)
	}
	return nil
}

// Start parses the DSL config and registers the MailConnection globally.
func (d *MailDirective) Start() ([]net.Listener, error) {
	cfg := d.config
	rawURL := strings.Trim(cfg.Address, "\"'`")

	// ── NAME (required) ───────────────────────────────────────────────────────
	name := ""
	for _, r := range cfg.Routes {
		if strings.ToUpper(r.Method) == "NAME" {
			name = r.Path
			break
		}
	}
	if name == "" {
		return nil, fmt.Errorf("MAIL %s: missing required NAME directive", rawURL)
	}

	isDefault := cfg.Args.GetBool("default", GetMailConnection() == nil)

	// ── Build sender ──────────────────────────────────────────────────────────
	sender, smtpFrom, err := buildSender(rawURL, cfg)
	if err != nil {
		return nil, fmt.Errorf("MAIL %s: %w", name, err)
	}

	// ── FROM directive — three syntaxes ───────────────────────────────────────
	// Priority: FROM directive > SET from > URL-derived from (SMTP only)
	staticFrom := cfg.Configs.Get("from", smtpFrom) // SET from fallback
	var fromRoute *mailFromRoute

	for _, r := range cfg.Routes {
		if strings.ToUpper(r.Method) != "FROM" {
			continue
		}
		fr := &mailFromRoute{mailRoute{route: r, baseDir: cfg.BaseDir}}
		// Static email address: FROM email@example.com  → Path=email, Handler="", !Inline
		if !r.Inline && r.Handler == "" && r.Path != "" {
			staticFrom = r.Path
		} else {
			// File or inline — resolve dynamically per Send()
			fromRoute = fr
		}
		break
	}

	conn := &MailConnection{
		name:      name,
		sender:    sender,
		from:      staticFrom,
		fromName:  cfg.Configs.Get("fromName", cfg.Configs.Get("from_name", "")),
		fromRoute: fromRoute,
		templates: make(map[string]mailTemplate),
		baseDir:   cfg.BaseDir,
	}

	// ── TEMPLATE routes — locked: cannot be modified from JS ────────────────────
	for _, r := range cfg.GetRoutes("TEMPLATE") {
		conn.templates[r.Path] = mailTemplate{
			mailRoute: mailRoute{route: r, baseDir: cfg.BaseDir},
			locked:    true,
		}
	}

	// ── PROCESSOR routes ──────────────────────────────────────────────────────
	// PROCESSOR [@PRE|@POST]? [filepath] [args...]
	// PROCESSOR [@PRE|@POST]? BEGIN [args...] ... END PROCESSOR
	for _, r := range cfg.Routes {
		if strings.ToUpper(r.Method) != "PROCESSOR" {
			continue
		}
		phase := mailPre
		for _, mw := range r.Middlewares {
			switch strings.ToUpper(mw.Name) {
			case "POST":
				phase = mailPost
			case "PRE":
				phase = mailPre
			}
		}
		conn.processors = append(conn.processors, mailProcessor{
			phase:     phase,
			mailRoute: mailRoute{route: r, baseDir: cfg.BaseDir},
		})
	}
	// (Generic fix removed from here, moved to start of Start)
	// ── Register ──────────────────────────────────────────────────────────────
	registerMailConnection(name, conn, isDefault)
	d.conns = append(d.conns, conn)
	log.Printf("MAIL: connection %q started (%s)", name, rawURL)
	return nil, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// buildSender
// ─────────────────────────────────────────────────────────────────────────────

func buildSender(rawURL string, cfg *DirectiveConfig) (MailSender, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "smtp", "smtps":
		return buildSMTP(u, cfg)
	case "sendgrid":
		apiKey := u.Host
		if apiKey == "" {
			apiKey = u.User.Username()
		}
		return &sendgridSender{apiKey: apiKey}, "", nil
	case "mailgun":
		return &mailgunSender{apiKey: u.User.Username(), domain: u.Host}, "", nil
	case "postmark":
		token := u.Host
		if token == "" {
			token = u.User.Username()
		}
		return &postmarkSender{serverToken: token}, "", nil
	case "rest", "http", "https":
		target := rawURL
		if u.Scheme == "rest" {
			target = strings.TrimPrefix(rawURL, "rest://")
		}
		return buildREST(target, cfg), "", nil
	default:
		return nil, "", fmt.Errorf("unsupported MAIL scheme %q", u.Scheme)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SMTP builder
// ─────────────────────────────────────────────────────────────────────────────

func buildSMTP(u *url.URL, cfg *DirectiveConfig) (MailSender, string, error) {
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "smtps" {
			port = "465"
		} else {
			port = "587"
		}
	}
	// USER directive: USER username password
	username, password := u.User.Username(), ""
	if p, ok := u.User.Password(); ok {
		password = p
	}
	for _, r := range cfg.Routes {
		if strings.ToUpper(r.Method) == "USER" && r.Path != "" {
			username = r.Path
			password = r.Handler
			break
		}
	}
	// USE directive: USE TLS | USE SSL | USE PLAIN
	useTLS := u.Scheme == "smtps"
	skipVerify := false
	for _, r := range cfg.Routes {
		if strings.ToUpper(r.Method) == "USE" {
			switch strings.ToUpper(r.Path) {
			case "TLS", "SSL":
				useTLS = true
			case "PLAIN":
				useTLS = false
			}
			skipVerify = r.Args.GetBool("skipVerify", r.Args.GetBool("insecure"))
			break
		}
	}
	from := cfg.Configs.Get("from", username)
	return &smtpSender{
		addr: host + ":" + port, host: host,
		username: username, password: password,
		useTLS: useTLS, skipVerify: skipVerify,
	}, from, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// REST builder
// ─────────────────────────────────────────────────────────────────────────────

func buildREST(target string, cfg *DirectiveConfig) *restSender {
	s := &restSender{url: target, method: "POST"}
	for _, r := range cfg.Routes {
		mr := mailMapRoute{mailRoute{route: r, baseDir: cfg.BaseDir}}
		switch strings.ToUpper(r.Method) {
		case "HEADER":
			s.headerRoutes = append(s.headerRoutes, mr)
		case "BODY":
			s.bodyRoutes = append(s.bodyRoutes, mr)
		case "QUERY":
			s.queryRoutes = append(s.queryRoutes, mr)
		case "METHOD":
			if r.Path != "" {
				s.method = strings.ToUpper(r.Path)
			}
		}
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// SMTP backend
// ─────────────────────────────────────────────────────────────────────────────

type smtpSender struct {
	addr, host         string
	username, password string
	useTLS, skipVerify bool
}

func (s *smtpSender) Send(msg MailMessage) error {
	var auth smtp.Auth
	if s.username != "" {
		auth = smtp.PlainAuth("", s.username, s.password, s.host)
	}
	raw := buildMIME(msg)
	if s.useTLS {
		tlsCfg := &tls.Config{InsecureSkipVerify: s.skipVerify, ServerName: s.host}
		conn, err := tls.Dial("tcp", s.addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("smtp TLS dial: %w", err)
		}
		client, err := smtp.NewClient(conn, s.host)
		if err != nil {
			return fmt.Errorf("smtp new client: %w", err)
		}
		defer client.Close()
		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
		if err := client.Mail(msg.From); err != nil {
			return fmt.Errorf("smtp MAIL FROM: %w", err)
		}
		for _, to := range allRecipients(msg) {
			if err := client.Rcpt(to); err != nil {
				return fmt.Errorf("smtp RCPT TO %s: %w", to, err)
			}
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("smtp DATA: %w", err)
		}
		defer w.Close()
		_, err = w.Write(raw)
		return err
	}
	return smtp.SendMail(s.addr, auth, msg.From, allRecipients(msg), raw)
}

// ─────────────────────────────────────────────────────────────────────────────
// SendGrid backend
// ─────────────────────────────────────────────────────────────────────────────

type sendgridSender struct{ apiKey string }

func (s *sendgridSender) Send(msg MailMessage) error {
	type em struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}
	type content struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	type personalization struct {
		To  []em `json:"to"`
		Cc  []em `json:"cc,omitempty"`
		Bcc []em `json:"bcc,omitempty"`
	}
	type payload struct {
		Personalizations []personalization `json:"personalizations"`
		From             em                `json:"from"`
		ReplyTo          *em               `json:"reply_to,omitempty"`
		Subject          string            `json:"subject"`
		Content          []content         `json:"content"`
	}
	toEm := func(list []string) []em {
		out := make([]em, len(list))
		for i, e := range list {
			out[i] = em{Email: e}
		}
		return out
	}
	p := payload{
		Personalizations: []personalization{{To: toEm(msg.To), Cc: toEm(msg.Cc), Bcc: toEm(msg.Bcc)}},
		From:             em{Email: msg.From, Name: msg.FromName},
		Subject:          msg.Subject,
	}
	if msg.Text != "" {
		p.Content = append(p.Content, content{"text/plain", msg.Text})
	}
	if msg.HTML != "" {
		p.Content = append(p.Content, content{"text/html", msg.HTML})
	}
	if msg.ReplyTo != "" {
		p.ReplyTo = &em{Email: msg.ReplyTo}
	}
	return postJSON("https://api.sendgrid.com/v3/mail/send",
		map[string]string{"Authorization": "Bearer " + s.apiKey}, p)
}

// ─────────────────────────────────────────────────────────────────────────────
// Mailgun backend
// ─────────────────────────────────────────────────────────────────────────────

type mailgunSender struct{ apiKey, domain string }

func (s *mailgunSender) Send(msg MailMessage) error {
	form := url.Values{}
	form.Set("from", formatFrom(msg.From, msg.FromName))
	form.Set("to", strings.Join(msg.To, ","))
	if len(msg.Cc) > 0 {
		form.Set("cc", strings.Join(msg.Cc, ","))
	}
	if len(msg.Bcc) > 0 {
		form.Set("bcc", strings.Join(msg.Bcc, ","))
	}
	form.Set("subject", msg.Subject)
	if msg.Text != "" {
		form.Set("text", msg.Text)
	}
	if msg.HTML != "" {
		form.Set("html", msg.HTML)
	}
	req, err := http.NewRequest("POST",
		fmt.Sprintf("https://api.mailgun.net/v3/%s/messages", s.domain),
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth("api", s.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mailgun error %d: %s", resp.StatusCode, b)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Postmark backend
// ─────────────────────────────────────────────────────────────────────────────

type postmarkSender struct{ serverToken string }

func (s *postmarkSender) Send(msg MailMessage) error {
	type pm struct {
		From     string `json:"From"`
		To       string `json:"To"`
		Cc       string `json:"Cc,omitempty"`
		Bcc      string `json:"Bcc,omitempty"`
		ReplyTo  string `json:"ReplyTo,omitempty"`
		Subject  string `json:"Subject"`
		TextBody string `json:"TextBody,omitempty"`
		HtmlBody string `json:"HtmlBody,omitempty"`
	}
	return postJSON("https://api.postmarkapp.com/email",
		map[string]string{
			"X-Postmark-Server-Token": s.serverToken,
			"Accept":                  "application/json",
		},
		pm{
			From: formatFrom(msg.From, msg.FromName),
			To:   strings.Join(msg.To, ","), Cc: strings.Join(msg.Cc, ","),
			Bcc: strings.Join(msg.Bcc, ","), ReplyTo: msg.ReplyTo,
			Subject: msg.Subject, TextBody: msg.Text, HtmlBody: msg.HTML,
		})
}

// ─────────────────────────────────────────────────────────────────────────────
// REST backend
// ─────────────────────────────────────────────────────────────────────────────

type restSender struct {
	url          string
	method       string
	headerRoutes []mailMapRoute
	bodyRoutes   []mailMapRoute
	queryRoutes  []mailMapRoute
}

// Send is called for non-POST-processor paths (no request mutation needed).
func (s *restSender) Send(msg MailMessage) error {
	req, err := s.buildRequest(msg)
	if err != nil {
		return err
	}
	return s.fireRequest(req)
}

// buildRequest evaluates all map routes and assembles the mailRequest.
func (s *restSender) buildRequest(msg MailMessage) (*mailRequest, error) {
	headers := make(map[string]string)
	body := make(map[string]any)
	query := make(map[string]string)

	for i := range s.headerRoutes {
		tmp := make(map[string]string)
		if err := s.headerRoutes[i].eval(tmp); err != nil {
			return nil, fmt.Errorf("mail REST HEADER: %w", err)
		}
		for k, v := range tmp {
			headers[k] = v
		}
	}
	bodyStr := make(map[string]string)
	for i := range s.bodyRoutes {
		if err := s.bodyRoutes[i].eval(bodyStr); err != nil {
			return nil, fmt.Errorf("mail REST BODY: %w", err)
		}
	}
	for i := range s.queryRoutes {
		if err := s.queryRoutes[i].eval(query); err != nil {
			return nil, fmt.Errorf("mail REST QUERY: %w", err)
		}
	}

	// Envelope fields
	body["from"] = formatFrom(msg.From, msg.FromName)
	body["to"] = msg.To
	body["cc"] = msg.Cc
	body["bcc"] = msg.Bcc
	body["replyTo"] = msg.ReplyTo
	body["subject"] = msg.Subject
	body["text"] = msg.Text
	body["html"] = msg.HTML
	body["data"] = msg.Data
	// BODY directive overrides
	for k, v := range bodyStr {
		body[k] = v
	}

	// Build target with query params
	target := s.url
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + q.Encode()
	}

	return &mailRequest{
		url:     target,
		method:  s.method,
		headers: headers,
		body:    body,
		query:   query,
	}, nil
}

// fireRequest sends the assembled mailRequest.
func (s *restSender) fireRequest(req *mailRequest) error {
	return doHTTP(req.method, req.url, req.headers, req.body)
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP helpers
// ─────────────────────────────────────────────────────────────────────────────

func postJSON(apiURL string, headers map[string]string, payload any) error {
	return doHTTP("POST", apiURL, headers, payload)
}

func doHTTP(method, apiURL string, headers map[string]string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mail: marshal error: %w", err)
	}
	req, err := http.NewRequest(method, apiURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mail API error %d: %s", resp.StatusCode, body)
	}
	return nil
}

func formatFrom(email, name string) string {
	if name != "" {
		return fmt.Sprintf("%s <%s>", name, email)
	}
	return email
}

// ─────────────────────────────────────────────────────────────────────────────
// MIME builder (SMTP)
// ─────────────────────────────────────────────────────────────────────────────

// newBoundary generates a cryptographically random MIME boundary.
func newBoundary() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("----=_Boundary_%x", b)
}

// attachmentContentType returns a sensible Content-Type for an attachment.
// Falls back to "application/octet-stream" when the type cannot be detected.
func attachmentContentType(a MailAttachment) string {
	if a.ContentType != "" {
		return a.ContentType
	}
	if a.Filename != "" {
		if t := mime.TypeByExtension(filepath.Ext(a.Filename)); t != "" {
			return t
		}
	}
	return "application/octet-stream"
}

func buildMIME(msg MailMessage) []byte {
	var b strings.Builder

	// ── Headers ───────────────────────────────────────────────────────────────
	from := msg.From
	if msg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", msg.FromName, msg.From)
	}
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(msg.To, ", ") + "\r\n")
	if len(msg.Cc) > 0 {
		b.WriteString("Cc: " + strings.Join(msg.Cc, ", ") + "\r\n")
	}
	if msg.ReplyTo != "" {
		b.WriteString("Reply-To: " + msg.ReplyTo + "\r\n")
	}
	b.WriteString("Subject: " + msg.Subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	for k, v := range msg.Headers {
		b.WriteString(k + ": " + v + "\r\n")
	}

	// ── Determine structure ───────────────────────────────────────────────────
	// mixed    → body part(s) + attachments
	// alternative → text + html, no attachments
	// single part → plain text or html only, no attachments
	hasAttachments := len(msg.Attachments) > 0
	hasHTML := msg.HTML != ""
	hasText := msg.Text != ""
	hasBoth := hasHTML && hasText

	writeBodyPart := func() {
		switch {
		case hasBoth:
			altBnd := newBoundary()
			b.WriteString("Content-Type: multipart/alternative; boundary=\"" + altBnd + "\"\r\n\r\n")
			b.WriteString("--" + altBnd + "\r\n")
			b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n" + msg.Text + "\r\n")
			b.WriteString("--" + altBnd + "\r\n")
			b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n" + msg.HTML + "\r\n")
			b.WriteString("--" + altBnd + "--\r\n")
		case hasHTML:
			b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n" + msg.HTML)
		default:
			b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n" + msg.Text)
		}
	}

	if hasAttachments {
		// multipart/mixed wraps the body part + each attachment
		mixedBnd := newBoundary()
		b.WriteString("Content-Type: multipart/mixed; boundary=\"" + mixedBnd + "\"\r\n\r\n")

		// Body part
		b.WriteString("--" + mixedBnd + "\r\n")
		writeBodyPart()
		b.WriteString("\r\n")

		// Attachments
		for _, att := range msg.Attachments {
			ct := attachmentContentType(att)
			encoded := base64.StdEncoding.EncodeToString(att.Data)
			filename := att.Filename
			if filename == "" {
				filename = "attachment"
			}
			b.WriteString("--" + mixedBnd + "\r\n")
			b.WriteString("Content-Type: " + ct + "; name=\"" + filename + "\"\r\n")
			b.WriteString("Content-Transfer-Encoding: base64\r\n")
			b.WriteString("Content-Disposition: attachment; filename=\"" + filename + "\"\r\n\r\n")
			// Split base64 into 76-char lines per RFC 2045
			for i := 0; i < len(encoded); i += 76 {
				end := i + 76
				if end > len(encoded) {
					end = len(encoded)
				}
				b.WriteString(encoded[i:end] + "\r\n")
			}
			b.WriteString("\r\n")
		}
		b.WriteString("--" + mixedBnd + "--\r\n")
	} else {
		writeBodyPart()
	}

	return []byte(b.String())
}

func allRecipients(msg MailMessage) []string {
	all := make([]string, 0, len(msg.To)+len(msg.Cc)+len(msg.Bcc))
	return append(append(append(all, msg.To...), msg.Cc...), msg.Bcc...)
}

// ─────────────────────────────────────────────────────────────────────────────
// JS module — `require('mail')`
//
// API (mirrors db.Module):
//
//   mail.connect(url, name?, options?)  → connection proxy  (create + register)
//   mail.connection(name?)             → existing connection proxy (default if omitted)
//   mail.connectionNames               → []string  (accessor)
//   mail.hasConnection(name)           → bool
//   mail.hasDefault                    → bool  (accessor)
//   mail.default                       → default connection proxy  (accessor)
//   mail.send({...})                   → thenable  (delegates to default connection)
//   mail.template(name)               → raw template source (default connection)
//
// Per-connection proxy:
//   conn.send({...})                   → thenable
//   conn.template(name)               → raw template source
//   conn.name                         → string
// ─────────────────────────────────────────────────────────────────────────────

type MailModule struct{}

func init() { modules.RegisterModule(&MailModule{}) }

func (m *MailModule) Name() string { return "mail" }
func (m *MailModule) Doc() string  { return "Mail module (SMTP, SendGrid, Mailgun, Postmark, REST)" }

// ToJSObject exposes the module as a SharedObject (processor.RegisterGlobal).
func (m *MailModule) ToJSObject(vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()
	m.Loader(nil, vm, obj)
	return obj
}

func (m *MailModule) Loader(_ any, vm *goja.Runtime, moduleObject *goja.Object) {
	// CommonJS support: if exports exists, use it as the target
	module := moduleObject
	if exp := moduleObject.Get("exports"); exp != nil && !goja.IsUndefined(exp) {
		module = exp.ToObject(vm)
	}

	// ── connect(url, name?, options?) ────────────────────────────────────────
	// Creates and registers a new MailConnection from a DSL-like options object.
	// url    — mail URL  (smtp://…, sendgrid://…, mailgun://…, postmark://…, rest://…)
	// name   — connection name (default: defaultMailConnName)
	// options — plain JS object: { from, fromName, default }
	//
	// Example:
	//   const mg = mail.connect("mailgun://key@mg.example.com", "mg", { from: "no-reply@app.com" })
	module.Set("connect", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("mail.connect() requires a URL")))
			return goja.Undefined()
		}
		rawURL := call.Argument(0).String()

		name := ""
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			name = call.Arguments[1].String()
		}

		// Parse optional options object
		fromAddr, fromName := "", ""
		isDefault := GetMailConnection() == nil // first created becomes default
		if len(call.Arguments) > 2 {
			if opts, ok := call.Arguments[2].Export().(map[string]interface{}); ok {
				fromAddr = mapStr(opts, "from")
				fromName = mapStr(opts, "fromName")
				if v, ok := opts["default"].(bool); ok {
					isDefault = v
				}
			}
		}

		// Build a minimal DirectiveConfig so buildSender can parse the URL
		cfg := &DirectiveConfig{
			Address: rawURL,
			Args:    Arguments{},
			Configs: Arguments{},
			Routes:  []*RouteConfig{},
		}

		sender, smtpFrom, err := buildSender(rawURL, cfg)
		if err != nil {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("mail.connect: %w", err)))
			return goja.Undefined()
		}
		if fromAddr == "" {
			fromAddr = smtpFrom
		}

		conn := &MailConnection{
			name:      name,
			sender:    sender,
			from:      fromAddr,
			fromName:  fromName,
			templates: make(map[string]mailTemplate),
		}
		registerMailConnection(name, conn, isDefault)
		return mailConnProxy(vm, conn)
	})

	// ── connection(name?) ────────────────────────────────────────────────────
	// Returns an existing connection proxy. Interrupts if not found.
	module.Set("connection", func(call goja.FunctionCall) goja.Value {
		name := ""
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) {
			name = call.Arguments[0].String()
		}
		conn := GetMailConnection(name)
		if conn == nil {
			if name == "" {
				vm.Interrupt(vm.NewGoError(fmt.Errorf("mail: no default connection")))
			} else {
				vm.Interrupt(vm.NewGoError(fmt.Errorf("mail: connection %q not found", name)))
			}
			return goja.Undefined()
		}
		return mailConnProxy(vm, conn)
	})

	// ── connectionNames (accessor) ───────────────────────────────────────────
	module.DefineAccessorProperty("connectionNames",
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			names := make([]goja.Value, 0, len(mailConns))
			for n := range mailConns {
				names = append(names, vm.ToValue(n))
			}
			return vm.NewArray(names)
		}),
		goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE,
	)

	// ── hasConnection(name) ──────────────────────────────────────────────────
	module.Set("hasConnection", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("mail.hasConnection() requires a name")))
			return goja.Undefined()
		}
		_, ok := mailConns[call.Argument(0).String()]
		return vm.ToValue(ok)
	})

	// ── hasDefault (accessor) ────────────────────────────────────────────────
	module.DefineAccessorProperty("hasDefault",
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(defaultMailConn != nil)
		}),
		goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE,
	)

	// ── default (accessor) ───────────────────────────────────────────────────
	module.DefineAccessorProperty("default",
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			if defaultMailConn == nil {
				vm.Interrupt(vm.NewGoError(fmt.Errorf("mail: no default connection")))
				return goja.Undefined()
			}
			return mailConnProxy(vm, defaultMailConn)
		}),
		goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE,
	)

	// ── send / template — delegate to default connection ─────────────────────
	module.Set("send", func(call goja.FunctionCall) goja.Value {
		conn := GetMailConnection()
		if conn == nil {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("mail: no default connection")))
			return goja.Undefined()
		}
		return sendFromJS(vm, conn, call)
	})
	module.Set("template", func(name string) goja.Value {
		return templateSource(vm, GetMailConnection(), name)
	})

	// attachment(path) — module-level shortcut; uses working dir as baseDir
	module.Set("attachment", mailAttachmentProxy(vm, ""))
}

// mailConnProxy wraps a *MailConnection as a JS object.
//
// Exposed properties:
//
//	conn.name                           — string identifier
//	conn.send({...})                    — thenable
//	conn.template(name)                → raw template source (undefined if not found)
//	conn.setTemplate(name, src)        → set inline template  (error if locked)
//	conn.setFileTemplate(name, path)   → set file-based template  (error if locked)
//	conn.deleteTemplate(name)          → delete template  (error if locked)
//	conn.hasTemplate(name)             → bool
//	conn.templateNames                 → []string  (accessor)
func mailConnProxy(vm *goja.Runtime, conn *MailConnection) goja.Value {
	obj := vm.NewObject()
	obj.Set("name", conn.name)

	obj.Set("send", func(call goja.FunctionCall) goja.Value {
		return sendFromJS(vm, conn, call)
	})

	// template(name) — raw source of a registered template
	obj.Set("template", func(name string) goja.Value {
		return templateSource(vm, conn, name)
	})

	// setTemplate(name, src) — register or replace an inline template
	obj.Set("setTemplate", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("setTemplate(name, src) requires 2 arguments")))
			return goja.Undefined()
		}
		name := call.Argument(0).String()
		src := call.Argument(1).String()
		if err := conn.SetTemplate(name, src, ""); err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return obj // chainable
	})

	// setFileTemplate(name, path) — register or replace a file-based template
	obj.Set("setFileTemplate", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("setFileTemplate(name, path) requires 2 arguments")))
			return goja.Undefined()
		}
		name := call.Argument(0).String()
		path := call.Argument(1).String()
		if err := conn.SetTemplate(name, "", path); err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return obj // chainable
	})

	// deleteTemplate(name) — delete a non-locked template
	obj.Set("deleteTemplate", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("deleteTemplate(name) requires a name")))
			return goja.Undefined()
		}
		if err := conn.DeleteTemplate(call.Argument(0).String()); err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return goja.Undefined()
	})

	// hasTemplate(name) — bool
	obj.Set("hasTemplate", func(name string) goja.Value {
		return vm.ToValue(conn.HasTemplate(name))
	})

	// templateNames — accessor → string[]
	obj.DefineAccessorProperty("templateNames",
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			names := conn.TemplateNames()
			vals := make([]goja.Value, len(names))
			for i, n := range names {
				vals[i] = vm.ToValue(n)
			}
			return vm.NewArray(vals)
		}),
		goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE,
	)

	// attachment(path) — creates a lazy file attachment descriptor.
	// The file is NOT read here; it is resolved at send() time.
	// The returned object can be placed directly in the `attachments` array.
	//
	//   conn.send({
	//       to: "u@e.com", subject: "x",
	//       attachments: [ conn.attachment("reports/q3.pdf") ]
	//   })
	//
	//   // Override filename and content-type if desired:
	//   conn.attachment("data.csv")
	//       .name("report.csv")    // rename in the email
	//       .type("text/csv")      // override MIME type
	obj.Set("attachment", mailAttachmentProxy(vm, conn.baseDir))

	return obj
}

// mailAttachmentProxy returns the attachment(path) function bound to baseDir.
// Exposed as both conn.attachment() and mail.attachment() (module-level).
func mailAttachmentProxy(vm *goja.Runtime, baseDir string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Arguments[0]) {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("mail.attachment() requires a file path")))
			return goja.Undefined()
		}
		filePath := call.Argument(0).String()
		if filePath == "" {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("mail.attachment() path must not be empty")))
			return goja.Undefined()
		}

		// Resolve path eagerly enough to give a useful error at definition time
		// if the path is clearly wrong, but the actual ReadFile happens at send().
		// We do NOT stat here — errors surface at send time so that dynamic paths
		// (built at runtime) still work.
		attObj := vm.NewObject()
		attObj.Set(mailFileAttachmentKey, filePath)

		// Plain string fields — read by mailAttachmentFromJS via map export
		attObj.Set("filename", filepath.Base(filePath))
		attObj.Set("contentType", "")

		// name(v) — chainable setter to override the filename shown in the email
		attObj.Set("name", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return attObj.Get("filename")
			}
			attObj.Set("filename", call.Argument(0).String())
			return attObj
		})

		// type(v) — chainable setter to override the MIME content-type
		attObj.Set("type", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return attObj.Get("contentType")
			}
			attObj.Set("contentType", call.Argument(0).String())
			return attObj
		})

		return attObj
	}
}

func templateSource(vm *goja.Runtime, conn *MailConnection, name string) goja.Value {
	if conn == nil {
		return goja.Undefined()
	}
	mt, ok := conn.templates[name]
	if !ok {
		return goja.Undefined()
	}
	return vm.ToValue(mt.route.Handler)
}

func sendFromJS(vm *goja.Runtime, conn *MailConnection, call goja.FunctionCall) goja.Value {
	if conn == nil {
		panic(vm.ToValue("mail: no connection available"))
	}
	if len(call.Arguments) == 0 {
		panic(vm.ToValue("mail.send() requires a message object"))
	}
	msgMap, ok := call.Arguments[0].Export().(map[string]interface{})
	if !ok {
		panic(vm.ToValue("mail.send() argument must be an object"))
	}
	msg := MailMessage{
		Subject:  mapStr(msgMap, "subject"),
		From:     mapStr(msgMap, "from"),
		FromName: mapStr(msgMap, "fromName"),
		ReplyTo:  mapStr(msgMap, "replyTo"),
		Text:     mapStr(msgMap, "text"),
		HTML:     mapStr(msgMap, "html"),
		Template: mapStr(msgMap, "template"),
		To:       toStringSlice(msgMap["to"]),
		Cc:       toStringSlice(msgMap["cc"]),
		Bcc:      toStringSlice(msgMap["bcc"]),
	}
	if d, ok := msgMap["data"].(map[string]interface{}); ok {
		msg.Data = d
	}
	if h, ok := msgMap["headers"].(map[string]interface{}); ok {
		msg.Headers = make(map[string]string, len(h))
		for k, v := range h {
			msg.Headers[k] = fmt.Sprint(v)
		}
	}

	// attachments — supports both mail.attachment(path) lazy objects and raw {data} objects.
	// File resolution happens here, at send time.
	if atts, ok := msgMap["attachments"].([]interface{}); ok {
		for _, raw := range atts {
			attMap, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			ma, err := mailAttachmentFromJS(attMap, conn.baseDir)
			if err != nil {
				// Return the error via the thenable instead of panicking
				return mailSendResult(vm, err)
			}
			msg.Attachments = append(msg.Attachments, ma)
		}
	}

	sendErr := conn.Send(msg)
	return mailSendResult(vm, sendErr)
}

// mailSendResult builds the thenable result object returned by send().
func mailSendResult(vm *goja.Runtime, sendErr error) goja.Value {
	result := vm.NewObject()
	result.Set("ok", sendErr == nil)
	result.Set("error", func() goja.Value {
		if sendErr != nil {
			return vm.ToValue(sendErr.Error())
		}
		return goja.Null()
	})
	result.Set("then", func(onFulfilled, onRejected goja.Value) goja.Value {
		if sendErr != nil {
			if fn, ok := goja.AssertFunction(onRejected); ok {
				fn(goja.Undefined(), vm.ToValue(sendErr.Error()))
			}
			return goja.Undefined()
		}
		if fn, ok := goja.AssertFunction(onFulfilled); ok {
			fn(goja.Undefined(), vm.ToValue(true))
		}
		return vm.ToValue(true)
	})
	result.Set("catch", func(onRejected goja.Value) goja.Value {
		if sendErr != nil {
			if fn, ok := goja.AssertFunction(onRejected); ok {
				fn(goja.Undefined(), vm.ToValue(sendErr.Error()))
			}
		}
		return result
	})
	return result
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal JS / reflect helpers
// ─────────────────────────────────────────────────────────────────────────────

func jsStr(obj *goja.Object, key, fallback string) string {
	v := obj.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return fallback
	}
	s := v.String()
	if s == "" {
		return fallback
	}
	return s
}

func jsStrSlice(obj *goja.Object, key string, fallback []string) []string {
	v := obj.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return fallback
	}
	return toStringSlice(v.Export())
}

func mapStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func toStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, elem := range t {
			if s := fmt.Sprint(elem); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	}
	return []string{fmt.Sprint(v)}
}
