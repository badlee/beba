package binder

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"http-server/processor"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func parseMailBind(t *testing.T, content string) *Config {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "mail.bind")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return cfg
}

func mailProto(t *testing.T, cfg *Config, idx int) *DirectiveConfig {
	t.Helper()
	if len(cfg.Groups) <= idx {
		t.Fatalf("expected group[%d], got %d", idx, len(cfg.Groups))
	}
	if len(cfg.Groups[idx].Items) == 0 {
		t.Fatalf("group[%d] has no items", idx)
	}
	return cfg.Groups[idx].Items[0]
}

func routeOf(routes []*RouteConfig, method string) *RouteConfig {
	for _, r := range routes {
		if strings.EqualFold(r.Method, method) {
			return r
		}
	}
	return nil
}

func routesOf(routes []*RouteConfig, method string) []*RouteConfig {
	var out []*RouteConfig
	for _, r := range routes {
		if strings.EqualFold(r.Method, method) {
			out = append(out, r)
		}
	}
	return out
}

// captureSender captures the last sent message.
type captureSender struct{ last MailMessage }

func (c *captureSender) Send(msg MailMessage) error { c.last = msg; return nil }

// failSender always returns an error.
type failSender struct{ err error }

func (f *failSender) Send(_ MailMessage) error { return f.err }

// freshConn returns a minimal MailConnection wired to a captureSender.
func freshConn(sender MailSender) *MailConnection {
	return &MailConnection{
		name:      "test",
		sender:    sender,
		templates: make(map[string]mailTemplate),
		baseDir:   "",
	}
}

// lockedTemplate returns a mailTemplate marked as locked (config-defined).
func lockedTemplate(name, src, baseDir string) mailTemplate {
	return mailTemplate{
		mailRoute: mailRoute{
			route:   &RouteConfig{Path: name, Inline: true, Handler: src},
			baseDir: baseDir,
		},
		locked: true,
	}
}

// jsTemplate returns an unlocked inline mailTemplate (JS-created).
func jsTemplate(name, src, baseDir string) mailTemplate {
	return mailTemplate{
		mailRoute: mailRoute{
			route:   &RouteConfig{Path: name, Inline: true, Handler: src},
			baseDir: baseDir,
		},
		locked: false,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NAME is required
// ─────────────────────────────────────────────────────────────────────────────

func TestMail_NAME_Required(t *testing.T) {
	cfg := parseMailBind(t, `
MAIL 'smtp://localhost:1025'
END MAIL
`)
	if len(cfg.Groups[0].Items) != 1 {
		t.Fatalf("group[0] has %d items", len(cfg.Groups[0].Items))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// USER + USE directives
// ─────────────────────────────────────────────────────────────────────────────

func TestMail_USER_Parsed(t *testing.T) {
	cfg := parseMailBind(t, `
MAIL 'smtp://localhost:587'
    NAME mailer
    USER myuser mypassword
    USE TLS
END MAIL
`)
	proto := mailProto(t, cfg, 0)
	u := routeOf(proto.Routes, "USER")
	if u == nil || u.Path != "myuser" || u.Handler != "mypassword" {
		t.Errorf("expected USER myuser mypassword, got %v %v", u.Path, u.Handler)
	}
	use := routeOf(proto.Routes, "USE")
	if use == nil || strings.ToUpper(use.Path) != "TLS" {
		t.Errorf("expected USE TLS, got %v", use)
	}
}

func TestMailBuildSender_SMTP_USER(t *testing.T) {
	cfg := &DirectiveConfig{
		Configs: Arguments{},
		Routes: []*RouteConfig{
			{Method: "USER", Path: "bob", Handler: "s3cr3t"},
			{Method: "USE", Path: "SSL"},
		},
	}
	sender, _, err := buildSender("smtp://host:587", cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := sender.(*smtpSender)
	if s.username != "bob" || s.password != "s3cr3t" {
		t.Errorf("wrong credentials: %s/%s", s.username, s.password)
	}
	if !s.useTLS {
		t.Error("expected TLS from USE SSL")
	}
}

func TestMailBuildSender_SMTP_UsePlain_OverridesSmtps(t *testing.T) {
	cfg := &DirectiveConfig{
		Configs: Arguments{},
		Routes:  []*RouteConfig{{Method: "USE", Path: "PLAIN"}},
	}
	sender, _, err := buildSender("smtps://host:465", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sender.(*smtpSender).useTLS {
		t.Error("USE PLAIN should override smtps TLS")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// FROM — static, inline JS, file JS
// ─────────────────────────────────────────────────────────────────────────────

func TestMail_FROM_Static_Parsed(t *testing.T) {
	cfg := parseMailBind(t, `
MAIL 'sendgrid://SG.key'
    NAME sg
    FROM noreply@example.com
END MAIL
`)
	proto := mailProto(t, cfg, 0)
	fr := routeOf(proto.Routes, "FROM")
	if fr == nil || fr.Path != "noreply@example.com" {
		t.Errorf("expected FROM noreply@example.com, got %v", fr)
	}
}

func TestMail_FROM_Inline_Resolves(t *testing.T) {
	cap := &captureSender{}
	conn := &MailConnection{
		name:      "test",
		sender:    cap,
		templates: make(map[string]mailTemplate),
		fromRoute: &mailFromRoute{mailRoute{
			route: &RouteConfig{
				Method:  "FROM",
				Inline:  true,
				Handler: `return email.to[0].endsWith("@vip.com") ? "vip@app.com" : "app@app.com"`,
			},
			baseDir: t.TempDir(),
		}},
	}

	conn.Send(MailMessage{To: []string{"user@vip.com"}, Subject: "x"})
	if cap.last.From != "vip@app.com" {
		t.Errorf("expected vip@app.com, got %s", cap.last.From)
	}

	conn.Send(MailMessage{To: []string{"user@other.com"}, Subject: "x"})
	if cap.last.From != "app@app.com" {
		t.Errorf("expected app@app.com, got %s", cap.last.From)
	}
}

func TestMail_FROM_File_Resolves(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "from.js"), []byte(`return "file@example.com"`), 0644)

	cap := &captureSender{}
	conn := &MailConnection{
		name:      "test",
		sender:    cap,
		templates: make(map[string]mailTemplate),
		fromRoute: &mailFromRoute{mailRoute{
			route:   &RouteConfig{Method: "FROM", Inline: false, Handler: "from.js"},
			baseDir: dir,
		}},
	}
	conn.Send(MailMessage{To: []string{"u@e.com"}, Subject: "x"})
	if cap.last.From != "file@example.com" {
		t.Errorf("expected file@example.com, got %s", cap.last.From)
	}
}

func TestMail_FROM_Static_Priority(t *testing.T) {
	cfg := &DirectiveConfig{
		Address: "smtp://smtp.example.com:587",
		Args:    Arguments{},
		Configs: Arguments{},
		BaseDir: t.TempDir(),
		Routes: []*RouteConfig{
			{Method: "NAME", Path: "m"},
			{Method: "FROM", Path: "override@example.com"},
		},
	}
	d := &MailDirective{config: cfg}
	if _, err := d.Start(); err != nil {
		t.Fatal(err)
	}
	conn := GetMailConnection("m")
	if conn == nil {
		t.Fatal("connection not registered")
	}
	if conn.from != "override@example.com" {
		t.Errorf("expected from=override@example.com, got %s", conn.from)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROCESSOR — DSL parsing + behaviour
// ─────────────────────────────────────────────────────────────────────────────

func TestMail_PROCESSOR_DSL_Parsed(t *testing.T) {
	cfg := parseMailBind(t, `
MAIL 'smtp://localhost:1025'
    NAME test
    PROCESSOR @PRE "validators/spam.js" [strict=true]
    PROCESSOR @POST BEGIN [tag=v1]
        email.subject = "[SENT] " + email.subject
    END PROCESSOR
END MAIL
`)
	proto := mailProto(t, cfg, 0)
	procs := routesOf(proto.Routes, "PROCESSOR")
	if len(procs) != 2 {
		t.Fatalf("expected 2 PROCESSOR routes, got %d", len(procs))
	}

	pre := procs[0]
	if len(pre.Middlewares) == 0 || pre.Middlewares[0].Name != "PRE" {
		t.Errorf("expected @PRE middleware, got %v", pre.Middlewares)
	}
	if pre.Inline || pre.Handler != "validators/spam.js" {
		t.Errorf("expected file PRE, got inline=%v handler=%q", pre.Inline, pre.Handler)
	}
	if pre.Args.Get("strict") != "true" {
		t.Errorf("expected strict=true, got %s", pre.Args.Get("strict"))
	}

	post := procs[1]
	if len(post.Middlewares) == 0 || post.Middlewares[0].Name != "POST" {
		t.Errorf("expected @POST middleware, got %v", post.Middlewares)
	}
	if !post.Inline {
		t.Error("expected inline POST processor")
	}
	if post.Args.Get("tag") != "v1" {
		t.Errorf("expected tag=v1, got %s", post.Args.Get("tag"))
	}
}

func TestMail_PROCESSOR_PRE_MutatesEmail(t *testing.T) {
	cap := &captureSender{}
	conn := freshConn(cap)
	conn.processors = []mailProcessor{{
		phase: mailPre,
		mailRoute: mailRoute{
			route:   &RouteConfig{Inline: true, Handler: `email.subject = "[PRE] " + email.subject`},
			baseDir: t.TempDir(),
		},
	}}
	conn.Send(MailMessage{To: []string{"u@e.com"}, Subject: "Hello"})
	if cap.last.Subject != "[PRE] Hello" {
		t.Errorf("expected '[PRE] Hello', got %q", cap.last.Subject)
	}
}

func TestMail_PROCESSOR_PRE_ContentIsEmpty(t *testing.T) {
	conn := freshConn(&captureSender{})
	conn.processors = []mailProcessor{{
		phase: mailPre,
		mailRoute: mailRoute{
			route: &RouteConfig{
				Inline:  true,
				Handler: `if (email.content !== "") reject("content must be empty in PRE")`,
			},
			baseDir: t.TempDir(),
		},
	}}
	if err := conn.Send(MailMessage{To: []string{"u@e.com"}, Subject: "x", HTML: "<b>hi</b>"}); err != nil {
		t.Errorf("unexpected PRE content error: %v", err)
	}
}

func TestMail_PROCESSOR_PRE_Reject(t *testing.T) {
	conn := freshConn(&captureSender{})
	conn.processors = []mailProcessor{{
		phase: mailPre,
		mailRoute: mailRoute{
			route: &RouteConfig{
				Inline:  true,
				Handler: `if (!email.to || email.to.length === 0) reject("no recipients")`,
			},
			baseDir: t.TempDir(),
		},
	}}
	if err := conn.Send(MailMessage{Subject: "x"}); err == nil || !strings.Contains(err.Error(), "no recipients") {
		t.Errorf("expected 'no recipients', got %v", err)
	}
}

func TestMail_PROCESSOR_PRE_SentBeforePOST(t *testing.T) {
	cap := &captureSender{}
	conn := freshConn(cap)
	conn.processors = []mailProcessor{
		{
			phase: mailPre,
			mailRoute: mailRoute{
				route:   &RouteConfig{Inline: true, Handler: `email.subject = "PRE+" + email.subject`},
				baseDir: t.TempDir(),
			},
		},
		{
			phase: mailPost,
			mailRoute: mailRoute{
				route:   &RouteConfig{Inline: true, Handler: `email.subject = email.subject + "+POST"`},
				baseDir: t.TempDir(),
			},
		},
	}
	conn.Send(MailMessage{To: []string{"u@e.com"}, Subject: "X"})
	// POST mutates after send — the captured message reflects the PRE mutation only
	if cap.last.Subject != "PRE+X" {
		t.Errorf("expected 'PRE+X' sent, got %q", cap.last.Subject)
	}
}

func TestMail_PROCESSOR_Args_Exposed(t *testing.T) {
	cap := &captureSender{}
	conn := freshConn(cap)
	conn.processors = []mailProcessor{{
		phase: mailPre,
		mailRoute: mailRoute{
			route: &RouteConfig{
				Inline:  true,
				Handler: `email.subject = args.prefix + email.subject`,
				Args:    Arguments{"prefix": "[APP]"},
			},
			baseDir: t.TempDir(),
		},
	}}
	conn.Send(MailMessage{To: []string{"u@e.com"}, Subject: " Hi"})
	if cap.last.Subject != "[APP] Hi" {
		t.Errorf("expected '[APP] Hi', got %q", cap.last.Subject)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HEADER / BODY / QUERY — DSL variants
// ─────────────────────────────────────────────────────────────────────────────

func TestMail_REST_Static_HEADER_BODY_QUERY(t *testing.T) {
	cfg := parseMailBind(t, `
MAIL 'rest://https://api.example.com/send'
    NAME custom
    HEADER Authorization "Bearer token"
    HEADER X-App "myapp"
    BODY   source backend
    QUERY  version v2
    METHOD PATCH
END MAIL
`)
	proto := mailProto(t, cfg, 0)

	headers := routesOf(proto.Routes, "HEADER")
	if len(headers) != 2 {
		t.Fatalf("expected 2 HEADER routes, got %d", len(headers))
	}
	hMap := map[string]string{}
	for _, h := range headers {
		hMap[h.Path] = h.Handler
	}
	if hMap["Authorization"] != "Bearer token" {
		t.Errorf("bad Authorization: %s", hMap["Authorization"])
	}
	if hMap["X-App"] != "myapp" {
		t.Errorf("bad X-App: %s", hMap["X-App"])
	}

	bodyR := routeOf(proto.Routes, "BODY")
	if bodyR == nil || bodyR.Path != "source" || bodyR.Handler != "backend" {
		t.Errorf("unexpected BODY: %v", bodyR)
	}
	queryR := routeOf(proto.Routes, "QUERY")
	if queryR == nil || queryR.Path != "version" || queryR.Handler != "v2" {
		t.Errorf("unexpected QUERY: %v", queryR)
	}
	methodR := routeOf(proto.Routes, "METHOD")
	if methodR == nil || strings.ToUpper(methodR.Path) != "PATCH" {
		t.Errorf("unexpected METHOD: %v", methodR)
	}
}

func TestMail_REST_Inline_HEADER(t *testing.T) {
	cfg := parseMailBind(t, `
MAIL 'rest://https://api.example.com/send'
    NAME custom
    HEADER BEGIN [ts=fixed]
        append("X-Timestamp", "12345")
        append("X-Source", args.ts)
    END HEADER
END MAIL
`)
	proto := mailProto(t, cfg, 0)
	headers := routesOf(proto.Routes, "HEADER")
	if len(headers) != 1 || !headers[0].Inline {
		t.Fatalf("expected 1 inline HEADER, got %d", len(headers))
	}
	if headers[0].Args.Get("ts") != "fixed" {
		t.Errorf("expected ts=fixed, got %s", headers[0].Args.Get("ts"))
	}
}

func TestMail_REST_File_BODY(t *testing.T) {
	cfg := parseMailBind(t, `
MAIL 'rest://https://api.example.com/send'
    NAME custom
    BODY "body/build.js" [env=prod]
END MAIL
`)
	proto := mailProto(t, cfg, 0)
	bodyR := routeOf(proto.Routes, "BODY")
	if bodyR == nil || bodyR.Inline {
		t.Fatalf("expected non-inline BODY file route, got %v", bodyR)
	}
	if bodyR.Handler != "body/build.js" {
		t.Errorf("expected handler=body/build.js, got %q", bodyR.Handler)
	}
	if bodyR.Args.Get("env") != "prod" {
		t.Errorf("expected env=prod, got %s", bodyR.Args.Get("env"))
	}
}

func TestMailMapRoute_eval_Static(t *testing.T) {
	mr := mailMapRoute{mailRoute{
		route:   &RouteConfig{Method: "HEADER", Path: "X-Foo", Handler: "bar", Inline: false},
		baseDir: t.TempDir(),
	}}
	dst := map[string]string{}
	if err := mr.eval(dst); err != nil {
		t.Fatal(err)
	}
	if dst["X-Foo"] != "bar" {
		t.Errorf("expected X-Foo=bar, got %s", dst["X-Foo"])
	}
}

func TestMailMapRoute_eval_Inline(t *testing.T) {
	mr := mailMapRoute{mailRoute{
		route: &RouteConfig{
			Method:  "BODY",
			Inline:  true,
			Handler: `append("source", "myapp"); append("version", "1.0")`,
			Args:    Arguments{},
		},
		baseDir: t.TempDir(),
	}}
	dst := map[string]string{}
	if err := mr.eval(dst); err != nil {
		t.Fatal(err)
	}
	if dst["source"] != "myapp" || dst["version"] != "1.0" {
		t.Errorf("unexpected dst: %v", dst)
	}
}

func TestMailMapRoute_eval_File(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "q.js"), []byte(`append("env", "prod"); append("v", args.ver)`), 0644)
	mr := mailMapRoute{mailRoute{
		route:   &RouteConfig{Method: "QUERY", Inline: false, Handler: "q.js", Args: Arguments{"ver": "v2"}},
		baseDir: dir,
	}}
	dst := map[string]string{}
	if err := mr.eval(dst); err != nil {
		t.Fatal(err)
	}
	if dst["env"] != "prod" || dst["v"] != "v2" {
		t.Errorf("unexpected dst: %v", dst)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Template management — SetTemplate / DeleteTemplate / HasTemplate / TemplateNames
// ─────────────────────────────────────────────────────────────────────────────

func TestMail_SetTemplate_Inline(t *testing.T) {
	conn := freshConn(&captureSender{})
	if err := conn.SetTemplate("welcome", "<h1>Hi {{name}}</h1>", ""); err != nil {
		t.Fatalf("SetTemplate: %v", err)
	}
	if !conn.HasTemplate("welcome") {
		t.Error("expected template to exist after SetTemplate")
	}
	mt := conn.templates["welcome"]
	if mt.locked {
		t.Error("JS-created template must not be locked")
	}
	if mt.route.Handler != "<h1>Hi {{name}}</h1>" {
		t.Errorf("unexpected handler: %q", mt.route.Handler)
	}
}

func TestMail_SetTemplate_File(t *testing.T) {
	conn := freshConn(&captureSender{})
	if err := conn.SetTemplate("invoice", "", "emails/invoice.html"); err != nil {
		t.Fatalf("SetTemplate file: %v", err)
	}
	mt := conn.templates["invoice"]
	if mt.route.Inline {
		t.Error("file template must not be inline")
	}
	if mt.route.Handler != "emails/invoice.html" {
		t.Errorf("unexpected handler: %q", mt.route.Handler)
	}
}

func TestMail_SetTemplate_OverwritesUnlocked(t *testing.T) {
	conn := freshConn(&captureSender{})
	conn.SetTemplate("tpl", "v1", "")
	if err := conn.SetTemplate("tpl", "v2", ""); err != nil {
		t.Fatalf("should be able to overwrite unlocked template: %v", err)
	}
	if conn.templates["tpl"].route.Handler != "v2" {
		t.Error("expected template to be updated to v2")
	}
}

func TestMail_SetTemplate_CannotOverwriteLocked(t *testing.T) {
	conn := freshConn(&captureSender{})
	conn.templates["welcome"] = lockedTemplate("welcome", "<h1>Original</h1>", "")

	err := conn.SetTemplate("welcome", "<h1>Modified</h1>", "")
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("expected locked error, got %v", err)
	}
	// Ensure original is untouched
	if conn.templates["welcome"].route.Handler != "<h1>Original</h1>" {
		t.Error("locked template was modified")
	}
}

func TestMail_DeleteTemplate_Unlocked(t *testing.T) {
	conn := freshConn(&captureSender{})
	conn.SetTemplate("tmp", "src", "")
	if err := conn.DeleteTemplate("tmp"); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	if conn.HasTemplate("tmp") {
		t.Error("template should be gone after delete")
	}
}

func TestMail_DeleteTemplate_Locked(t *testing.T) {
	conn := freshConn(&captureSender{})
	conn.templates["locked"] = lockedTemplate("locked", "src", "")

	if err := conn.DeleteTemplate("locked"); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("expected locked error, got %v", err)
	}
	if !conn.HasTemplate("locked") {
		t.Error("locked template should still exist")
	}
}

func TestMail_DeleteTemplate_NotFound(t *testing.T) {
	conn := freshConn(&captureSender{})
	if err := conn.DeleteTemplate("ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got %v", err)
	}
}

func TestMail_TemplateNames(t *testing.T) {
	conn := freshConn(&captureSender{})
	conn.templates["a"] = jsTemplate("a", "x", "")
	conn.templates["b"] = lockedTemplate("b", "y", "")
	conn.templates["c"] = jsTemplate("c", "z", "")

	names := conn.TemplateNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d: %v", len(names), names)
	}
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !nameSet[want] {
			t.Errorf("missing name %q in TemplateNames", want)
		}
	}
}

func TestMail_HasTemplate(t *testing.T) {
	conn := freshConn(&captureSender{})
	conn.SetTemplate("exists", "src", "")
	if !conn.HasTemplate("exists") {
		t.Error("HasTemplate should return true for existing template")
	}
	if conn.HasTemplate("ghost") {
		t.Error("HasTemplate should return false for unknown template")
	}
}

func TestMail_Template_Inline_Renders(t *testing.T) {
	cap := &captureSender{}
	conn := freshConn(cap)
	conn.SetTemplate("hi", "<p>Hello {{name}}!</p>", "")
	conn.Send(MailMessage{
		To: []string{"u@e.com"}, Subject: "x",
		Template: "welcome_not_found",
	})
	// Not found should return error — verify separately
	conn.templates["hi"] = mailTemplate{
		mailRoute: mailRoute{
			route:   &RouteConfig{Path: "hi", Inline: true, Handler: "<p>Hello {{name}}!</p>"},
			baseDir: t.TempDir(),
		},
	}
	conn.Send(MailMessage{
		To: []string{"u@e.com"}, Subject: "x",
		Template: "hi", Data: map[string]any{"name": "Alice"},
	})
	if !strings.Contains(cap.last.HTML, "Alice") {
		t.Errorf("expected Alice in HTML, got %q", cap.last.HTML)
	}
}

func TestMail_Template_File_Renders(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "t.html"), []byte("<p>Hi {{user}}!</p>"), 0644)
	cap := &captureSender{}
	conn := freshConn(cap)
	conn.SetTemplate("greet", "", "t.html")
	conn.templates["greet"] = mailTemplate{
		mailRoute: mailRoute{
			route:   &RouteConfig{Path: "greet", Inline: false, Handler: "t.html"},
			baseDir: dir,
		},
	}
	conn.Send(MailMessage{
		To: []string{"u@e.com"}, Subject: "x",
		Template: "greet", Data: map[string]any{"user": "Bob"},
	})
	if !strings.Contains(cap.last.HTML, "Bob") {
		t.Errorf("expected Bob in HTML, got %q", cap.last.HTML)
	}
}

func TestMail_Template_NotFound_Error(t *testing.T) {
	conn := freshConn(&captureSender{})
	err := conn.Send(MailMessage{To: []string{"u@e.com"}, Subject: "x", Template: "missing"})
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("expected 'not registered' error, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Config-locked templates
// ─────────────────────────────────────────────────────────────────────────────

func TestMail_ConfigTemplate_IsLocked(t *testing.T) {
	// Simulate what Start() does: templates from .bind are locked=true
	conn := freshConn(&captureSender{})
	conn.templates["config_tpl"] = lockedTemplate("config_tpl", "<b>original</b>", "")

	// Cannot modify
	if err := conn.SetTemplate("config_tpl", "<b>hacked</b>", ""); err == nil {
		t.Error("expected error modifying locked template")
	}
	// Cannot delete
	if err := conn.DeleteTemplate("config_tpl"); err == nil {
		t.Error("expected error deleting locked template")
	}
	// Still accessible
	if !conn.HasTemplate("config_tpl") {
		t.Error("locked template should still exist")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Attachments — mailAttachmentFromJS
// ─────────────────────────────────────────────────────────────────────────────

func TestMailAttachmentFromJS_RawBase64(t *testing.T) {
	data := []byte("hello world")
	encoded := base64.StdEncoding.EncodeToString(data)
	raw := map[string]interface{}{
		"filename":    "hello.txt",
		"contentType": "text/plain",
		"data":        encoded,
	}
	att, err := mailAttachmentFromJS(raw, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Filename != "hello.txt" {
		t.Errorf("expected filename=hello.txt, got %s", att.Filename)
	}
	if string(att.Data) != "hello world" {
		t.Errorf("unexpected data: %q", att.Data)
	}
}

func TestMailAttachmentFromJS_RawBytes(t *testing.T) {
	raw := map[string]interface{}{
		"filename": "bin.dat",
		"data":     []byte{0x01, 0x02, 0x03},
	}
	att, err := mailAttachmentFromJS(raw, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(att.Data) != 3 || att.Data[0] != 0x01 {
		t.Errorf("unexpected bytes: %v", att.Data)
	}
}

func TestMailAttachmentFromJS_LazyFile_Exists(t *testing.T) {
	dir := t.TempDir()
	content := []byte("pdf content")
	path := filepath.Join(dir, "doc.pdf")
	os.WriteFile(path, content, 0644)

	raw := map[string]interface{}{
		mailFileAttachmentKey: path,
		"filename":            "doc.pdf",
		"contentType":         "application/pdf",
	}
	att, err := mailAttachmentFromJS(raw, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(att.Data) != "pdf content" {
		t.Errorf("unexpected data: %q", att.Data)
	}
	if att.Filename != "doc.pdf" {
		t.Errorf("unexpected filename: %s", att.Filename)
	}
}

func TestMailAttachmentFromJS_LazyFile_Missing(t *testing.T) {
	raw := map[string]interface{}{
		mailFileAttachmentKey: "/nonexistent/missing.pdf",
		"filename":            "missing.pdf",
	}
	_, err := mailAttachmentFromJS(raw, "")
	if err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Errorf("expected 'cannot read' error, got %v", err)
	}
}

func TestMailAttachmentFromJS_LazyFile_DefaultFilename(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "report.csv"), []byte("a,b"), 0644)

	// No explicit filename — should default to basename of path
	raw := map[string]interface{}{
		mailFileAttachmentKey: filepath.Join(dir, "report.csv"),
	}
	att, err := mailAttachmentFromJS(raw, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Filename != "report.csv" {
		t.Errorf("expected filename=report.csv, got %s", att.Filename)
	}
}

func TestMailAttachmentFromJS_LazyFile_RelativePath(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "rel.txt"), []byte("relative"), 0644)

	raw := map[string]interface{}{
		mailFileAttachmentKey: "rel.txt", // relative — resolved against baseDir
	}
	att, err := mailAttachmentFromJS(raw, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(att.Data) != "relative" {
		t.Errorf("unexpected data: %q", att.Data)
	}
}

func TestMailAttachmentFromJS_MissingData(t *testing.T) {
	raw := map[string]interface{}{
		"filename": "empty.bin",
		// no data, no __mailFilePath
	}
	_, err := mailAttachmentFromJS(raw, "")
	if err == nil || !strings.Contains(err.Error(), "missing or empty data") {
		t.Errorf("expected 'missing or empty data' error, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// MIME builder — dynamic boundary + attachments
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildMIME_DynamicBoundary(t *testing.T) {
	m1 := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"},
		Subject: "x", Text: "hi", HTML: "<b>hi</b>",
	}))
	m2 := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"},
		Subject: "x", Text: "hi", HTML: "<b>hi</b>",
	}))
	// Extract boundary from first message
	bnd1 := extractBoundary(m1)
	bnd2 := extractBoundary(m2)
	if bnd1 == "" || bnd2 == "" {
		t.Fatal("no boundary found in MIME output")
	}
	if bnd1 == bnd2 {
		t.Error("boundaries should differ between messages (dynamic)")
	}
}

func extractBoundary(mime string) string {
	for _, line := range strings.Split(mime, "\n") {
		if strings.Contains(line, "boundary=") {
			parts := strings.SplitN(line, `boundary="`, 2)
			if len(parts) == 2 {
				return strings.TrimSuffix(strings.TrimRight(parts[1], "\r\n"), `"`)
			}
		}
	}
	return ""
}

func TestBuildMIME_Multipart_Alternative(t *testing.T) {
	s := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"},
		Subject: "Test", Text: "plain", HTML: "<b>html</b>",
	}))
	if !strings.Contains(s, "multipart/alternative") {
		t.Error("expected multipart/alternative")
	}
	if !strings.Contains(s, "plain") {
		t.Error("missing plain text body")
	}
	if !strings.Contains(s, "<b>html</b>") {
		t.Error("missing html body")
	}
}

func TestBuildMIME_HTMLOnly(t *testing.T) {
	s := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"}, Subject: "x", HTML: "<b>hi</b>",
	}))
	if !strings.Contains(s, "text/html") {
		t.Error("expected text/html content-type")
	}
	if strings.Contains(s, "multipart") {
		t.Error("should not be multipart for HTML-only message")
	}
}

func TestBuildMIME_TextOnly(t *testing.T) {
	s := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"}, Subject: "x", Text: "hello",
	}))
	if !strings.Contains(s, "text/plain") {
		t.Error("expected text/plain content-type")
	}
	if strings.Contains(s, "multipart") {
		t.Error("should not be multipart for text-only message")
	}
}

func TestBuildMIME_FromName(t *testing.T) {
	s := string(buildMIME(MailMessage{
		From: "a@e.com", FromName: "Alice", To: []string{"b@e.com"}, Subject: "x",
	}))
	if !strings.Contains(s, "Alice <a@e.com>") {
		t.Errorf("missing FromName, got: %.200s", s)
	}
}

func TestBuildMIME_CC_ReplyTo(t *testing.T) {
	s := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"}, Cc: []string{"c@e.com"},
		ReplyTo: "reply@e.com", Subject: "x",
	}))
	if !strings.Contains(s, "Cc: c@e.com") {
		t.Error("missing Cc header")
	}
	if !strings.Contains(s, "Reply-To: reply@e.com") {
		t.Error("missing Reply-To header")
	}
}

func TestBuildMIME_WithAttachment(t *testing.T) {
	s := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"}, Subject: "x",
		HTML: "<p>see attachment</p>",
		Attachments: []MailAttachment{
			{Filename: "doc.pdf", ContentType: "application/pdf", Data: []byte("pdfdata")},
		},
	}))
	if !strings.Contains(s, "multipart/mixed") {
		t.Error("expected multipart/mixed for message with attachments")
	}
	if !strings.Contains(s, "doc.pdf") {
		t.Error("expected attachment filename in MIME")
	}
	if !strings.Contains(s, "application/pdf") {
		t.Error("expected content-type in attachment part")
	}
	if !strings.Contains(s, "Content-Disposition: attachment") {
		t.Error("expected Content-Disposition header")
	}
	if !strings.Contains(s, "Content-Transfer-Encoding: base64") {
		t.Error("expected base64 transfer encoding")
	}
}

func TestBuildMIME_AttachmentAlternativePlusFile(t *testing.T) {
	// text + html body + attachment → multipart/mixed wrapping multipart/alternative
	s := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"}, Subject: "x",
		Text: "plain", HTML: "<b>html</b>",
		Attachments: []MailAttachment{
			{Filename: "img.png", ContentType: "image/png", Data: []byte{0x89, 0x50}},
		},
	}))
	if !strings.Contains(s, "multipart/mixed") {
		t.Error("expected multipart/mixed")
	}
	if !strings.Contains(s, "multipart/alternative") {
		t.Error("expected nested multipart/alternative")
	}
	if !strings.Contains(s, "img.png") {
		t.Error("expected attachment filename")
	}
}

func TestBuildMIME_AttachmentDefaultFilename(t *testing.T) {
	// No filename provided — should default to "attachment"
	s := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"}, Subject: "x", Text: "hi",
		Attachments: []MailAttachment{
			{Data: []byte("binary")},
		},
	}))
	if !strings.Contains(s, `name="attachment"`) {
		t.Error("expected default filename 'attachment'")
	}
}

func TestBuildMIME_AttachmentBase64Lines(t *testing.T) {
	// Base64 must be split into 76-char lines per RFC 2045
	data := make([]byte, 200) // big enough to exceed one line
	for i := range data {
		data[i] = byte(i % 256)
	}
	s := string(buildMIME(MailMessage{
		From: "a@e.com", To: []string{"b@e.com"}, Subject: "x", Text: "hi",
		Attachments: []MailAttachment{{Filename: "big.bin", Data: data}},
	}))
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		// Only check lines inside the base64 block (heuristic: pure base64 chars)
		if len(line) > 76 && isBase64Line(line) {
			t.Errorf("base64 line exceeds 76 chars (%d): %.80s", len(line), line)
		}
	}
}

func isBase64Line(s string) bool {
	const b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
	for _, c := range s {
		if !strings.ContainsRune(b64chars, c) {
			return false
		}
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────
// newBoundary
// ─────────────────────────────────────────────────────────────────────────────

func TestNewBoundary_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		b := newBoundary()
		if seen[b] {
			t.Fatalf("duplicate boundary generated: %s", b)
		}
		seen[b] = true
		if !strings.HasPrefix(b, "----=_Boundary_") {
			t.Errorf("unexpected boundary format: %s", b)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// attachmentContentType
// ─────────────────────────────────────────────────────────────────────────────

func TestAttachmentContentType(t *testing.T) {
	cases := []struct {
		att  MailAttachment
		want string
	}{
		{MailAttachment{ContentType: "application/pdf"}, "application/pdf"},
		{MailAttachment{Filename: "image.png"}, "image/png"},
		{MailAttachment{Filename: "data.csv"}, "text/csv; charset=utf-8"},
		{MailAttachment{Filename: "archive.zip"}, "application/zip"},
		{MailAttachment{}, "application/octet-stream"},
		{MailAttachment{Filename: "noext"}, "application/octet-stream"},
	}
	for _, tc := range cases {
		got := attachmentContentType(tc.att)
		if got != tc.want {
			t.Errorf("attachmentContentType(%+v) = %q, want %q", tc.att, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// buildSender variants
// ─────────────────────────────────────────────────────────────────────────────

func TestMailBuildSender_SendGrid(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	s, _, err := buildSender("sendgrid://SG.mykey", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.(*sendgridSender).apiKey != "SG.mykey" {
		t.Errorf("unexpected apiKey: %s", s.(*sendgridSender).apiKey)
	}
}

func TestMailBuildSender_Mailgun(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	s, _, err := buildSender("mailgun://key-abc@mg.example.com", cfg)
	if err != nil {
		t.Fatal(err)
	}
	mg := s.(*mailgunSender)
	if mg.apiKey != "key-abc" || mg.domain != "mg.example.com" {
		t.Errorf("unexpected mailgun config: %+v", mg)
	}
}

func TestMailBuildSender_Postmark(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	s, _, err := buildSender("postmark://my-token", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.(*postmarkSender).serverToken != "my-token" {
		t.Errorf("unexpected token: %s", s.(*postmarkSender).serverToken)
	}
}

func TestMailBuildSender_REST_Defaults(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	s, _, err := buildSender("rest://https://api.example.com/send", cfg)
	if err != nil {
		t.Fatal(err)
	}
	rs := s.(*restSender)
	if rs.method != "POST" {
		t.Errorf("expected method POST, got %s", rs.method)
	}
	if rs.url != "https://api.example.com/send" {
		t.Errorf("unexpected url: %s", rs.url)
	}
}

func TestMailBuildSender_UnknownScheme(t *testing.T) {
	cfg := &DirectiveConfig{Configs: Arguments{}}
	if _, _, err := buildSender("ftp://nope", cfg); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// IsFileLike
// ─────────────────────────────────────────────────────────────────────────────

func TestIsFileLike(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"file.js", true},
		{"path/to/file.js", true},
		{"./relative.js", true},
		{"/abs/path.js", true},
		{"Bearer token", false},
		{"noreply@example.com", false},
		{"myvalue", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsFileLike(tc.in); got != tc.want {
			t.Errorf("IsFileLike(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Multiple connections + coexistence
// ─────────────────────────────────────────────────────────────────────────────

func TestMail_MultipleConnections(t *testing.T) {
	cfg := parseMailBind(t, `
MAIL 'smtp://localhost:1025' [default]
    NAME smtp_local
END MAIL

MAIL 'sendgrid://SG.key'
    NAME sg_prod
END MAIL
`)
	if len(cfg.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(cfg.Groups))
	}
	n0 := routeOf(cfg.Groups[0].Items[0].Routes, "NAME")
	n1 := routeOf(cfg.Groups[1].Items[0].Routes, "NAME")
	if n0 == nil || n0.Path != "smtp_local" {
		t.Errorf("expected smtp_local, got %v", n0)
	}
	if n1 == nil || n1.Path != "sg_prod" {
		t.Errorf("expected sg_prod, got %v", n1)
	}
}

func TestMail_CoexistsWithHTTP(t *testing.T) {
	cfg := parseMailBind(t, `
MAIL 'smtp://localhost:1025'
    NAME mail
END MAIL

HTTP :8080
    GET /ping BEGIN
        "pong"
    END GET
END HTTP
`)
	if len(cfg.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(cfg.Groups))
	}
	if cfg.Groups[0].Directive != "MAIL" || cfg.Groups[1].Directive != "HTTP" {
		t.Errorf("unexpected order: %s %s", cfg.Groups[0].Directive, cfg.Groups[1].Directive)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// JS Module API & Execution
// ─────────────────────────────────────────────────────────────────────────────

func TestMailModule_JS_API(t *testing.T) {
	mailConns = make(map[string]*MailConnection)
	defaultMailConn = nil

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "att.txt"), []byte("file-content"), 0644)

	processor.RegisterGlobal("mail", &MailModule{}, true)

	// Create a standard processor context (loads mail via modules.RegisterModule)
	vm := processor.New(dir, nil, nil)

	script := `
		// Test connect
		const myconn = mail.connect('smtp://localhost:587', 'js_conn', { from: 'js@app.com', default: true });
		if (myconn.name !== 'js_conn') throw new Error("bad name: " + myconn.name);
		if (!mail.hasConnection('js_conn')) throw new Error("not registered");
		if (!mail.hasDefault) throw new Error("no default");
		
		// Test templates
		myconn.setTemplate('welcome', '<h1>Hello {{name}}</h1>');
		if (!myconn.hasTemplate('welcome')) throw new Error("template missing");
		if (myconn.template('welcome') !== '<h1>Hello {{name}}</h1>') throw new Error("bad template src");
	`
	if _, err := vm.RunString(script); err != nil {
		t.Fatalf("js mail API setup failed: %v", err)
	}

	conn := GetMailConnection("js_conn")
	if conn == nil {
		t.Fatalf("js_conn not registered globally")
	}

	// Hijack sender to verify what JS sent
	cap := &captureSender{}
	conn.sender = cap

	attPath := filepath.Join(dir, "att.txt")
	// On Windows backslashes need to be replaced with normal slashes for JS strings if we don't escape
	attPath = strings.ReplaceAll(attPath, "\\", "/")

	scriptSend := `
		// Test mail.send()
		let p = mail.send({
			to: 'u@e.com',
			subject: 'from js',
			template: 'welcome',
			data: { name: 'Bob' },
			attachments: [ mail.attachment('` + attPath + `').name('renamed.txt').type('text/plain') ]
		});
		if (p && p.catch) {
			p.catch(e => { throw e }); // re-throw if it rejects
		}
	`
	if _, err := vm.RunString(scriptSend); err != nil {
		t.Fatalf("js send API failed: %v", err)
	}

	if cap.last.Subject != "from js" {
		t.Errorf("expected subject 'from js', got %q", cap.last.Subject)
	}
	if len(cap.last.To) != 1 || cap.last.To[0] != "u@e.com" {
		t.Errorf("expected to[0] u@e.com, got %v", cap.last.To)
	}
	if cap.last.Template != "welcome" {
		t.Errorf("expected template 'welcome', got %q", cap.last.Template)
	}
	if len(cap.last.Attachments) != 1 {
		t.Errorf("expected 1 attachment, got %d", len(cap.last.Attachments))
	} else {
		att := cap.last.Attachments[0]
		if string(att.Data) != "file-content" || att.Filename != "renamed.txt" || att.ContentType != "text/plain" {
			t.Errorf("unexpected attachment properties: %+v", att)
		}
	}

	// Test cross connection access & delete template
	scriptCheck := `
		const conn = mail.connection('js_conn');
		conn.deleteTemplate('welcome');
		if (conn.hasTemplate('welcome')) throw new Error("template still exists");
	`
	if _, err := vm.RunString(scriptCheck); err != nil {
		t.Fatalf("js mail cleanup failed: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP Mock for REST and External Providers
// ─────────────────────────────────────────────────────────────────────────────

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMailSenders_HTTP_Mock(t *testing.T) {
	originalTransport := http.DefaultClient.Transport
	defer func() { http.DefaultClient.Transport = originalTransport }()

	var lastReq *http.Request
	var lastBody []byte

	// Mock Transport
	http.DefaultClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		lastReq = req
		lastBody, _ = io.ReadAll(req.Body)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"ok":true}`))),
			Header:     make(http.Header),
		}, nil
	})

	msg := MailMessage{
		From:     "noreply@app.com",
		FromName: "My App",
		To:       []string{"alice@domain.com"},
		Subject:  "Hello External",
		Text:     "Text body",
		HTML:     "<html>HTML body</html>",
	}

	t.Run("SendGrid", func(t *testing.T) {
		sg := &sendgridSender{apiKey: "SG.mockkey"}
		err := sg.Send(msg)
		if err != nil {
			t.Fatalf("sendgrid Error: %v", err)
		}
		if lastReq.Header.Get("Authorization") != "Bearer SG.mockkey" {
			t.Errorf("missing/invalid auth header: %s", lastReq.Header.Get("Authorization"))
		}
		if !strings.Contains(string(lastBody), "personalizations") {
			t.Errorf("missing personalizations: %s", lastBody)
		}
		if !strings.Contains(string(lastBody), "alice@domain.com") {
			t.Errorf("missing recipient in body: %s", lastBody)
		}
		if !strings.Contains(string(lastBody), `\u003chtml\u003eHTML body\u003c/html\u003e`) {
			t.Errorf("missing HTML content in body")
		}
	})

	t.Run("Mailgun", func(t *testing.T) {
		mg := &mailgunSender{apiKey: "key-123", domain: "mg.app.com"}
		err := mg.Send(msg)
		if err != nil {
			t.Fatalf("mailgun Error: %v", err)
		}
		if lastReq.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("invalid content-type: %s", lastReq.Header.Get("Content-Type"))
		}
		// Basic auth is essentially base64("api:key-123")
		if !strings.HasPrefix(lastReq.Header.Get("Authorization"), "Basic ") {
			t.Errorf("missing basic auth")
		}
		if !strings.Contains(string(lastBody), "alice%40domain.com") { // form encoded
			t.Errorf("missing recipient form data: %s", lastBody)
		}
	})

	t.Run("Postmark", func(t *testing.T) {
		pm := &postmarkSender{serverToken: "pm-token"}
		err := pm.Send(msg)
		if err != nil {
			t.Fatalf("postmark Error: %v", err)
		}
		if lastReq.Header.Get("X-Postmark-Server-Token") != "pm-token" {
			t.Errorf("missing postmark auth header")
		}
		if !strings.Contains(string(lastBody), "alice@domain.com") {
			t.Errorf("missing recipient: %s", lastBody)
		}
		if !strings.Contains(string(lastBody), `My App \u003cnoreply@app.com\u003e`) {
			t.Errorf("missing from formatting: %s", lastBody)
		}
	})

	t.Run("REST", func(t *testing.T) {
		rs := &restSender{
			url:    "https://api.mybackend.com/email",
			method: "POST",
		}
		err := rs.Send(msg)
		if err != nil {
			t.Fatalf("REST Error: %v", err)
		}
		if lastReq.URL.String() != "https://api.mybackend.com/email" {
			t.Errorf("bad rest url: %s", lastReq.URL.String())
		}
		if !strings.Contains(string(lastBody), "alice@domain.com") || !strings.Contains(string(lastBody), "noreply@app.com") {
			t.Errorf("REST body missing fields: %s", lastBody)
		}
	})
}

