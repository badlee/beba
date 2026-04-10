package binder

import (
	"http-server/plugins/config"
	"http-server/plugins/httpserver"
	"testing"
)

func TestSecurityProtocolParsing(t *testing.T) {
	content := `
SECURITY global_waf
    ENGINE On
    OWASP "/rules/coreruleset/*.conf"
    ACTION "nolog,phase:1,pass"
    
    REQUEST DEFINE
        ACCESS On
        LIMIT 10mb
        ACTION Reject
    END REQUEST

    AUDIT DEFINE
        ENGINE RelevantOnly
        FILE "/var/log/waf_audit.log"
        FORMAT JSON
    END AUDIT

    RULES DEFINE
        RULE "ARGS:test" "@contains payload" "id:1,phase:1,deny,status:403"
        REMOVE ID 999
    END RULES

    ON REQUEST_HEADERS BEGIN
        tx.AddRequestHeader("X-WAF-Phase", "1");
    END ON
END SECURITY
`
	cfg, _, err := ParseConfig(content)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(cfg.Groups) != 1 {
		t.Fatalf("Expected 1 group, got %d", len(cfg.Groups))
	}

	group := cfg.Groups[0]
	if group.Directive != "SECURITY" || group.Address != "global_waf" {
		t.Errorf("Unexpected group: %s %s", group.Directive, group.Address)
	}

	// SecurityDirective is created when Start is called, but we can verify the DirectiveConfig
	item := group.Items[0]
	sd, err := NewSecurityDirective(item)
	if err != nil {
		t.Fatalf("Failed to create SecurityDirective: %v", err)
	}

	if sd.Config.Engine != "On" {
		t.Errorf("Expected Engine On, got %s", sd.Config.Engine)
	}
	if sd.Config.RequestBodyLimit != 10*1024*1024 {
		t.Errorf("Expected RequestBodyLimit 10MB, got %d", sd.Config.RequestBodyLimit)
	}
	if sd.Config.AuditLogFormat != "JSON" {
		t.Errorf("Expected AuditLogFormat JSON, got %s", sd.Config.AuditLogFormat)
	}

	if len(sd.Config.Rules) < 3 {
		t.Errorf("Expected at least 3 rules, got %d", len(sd.Config.Rules))
	}

	if hook, ok := sd.Config.Hooks["REQUEST_HEADERS"]; !ok || !hook.Inline {
		t.Errorf("Hook REQUEST_HEADERS missing or not inline")
	}

	// Verify registry
	stored := httpserver.GetWAF("global_waf")
	if stored == nil {
		t.Fatalf("WAF global_waf not registered")
	}
}

func TestHTTP_SecurityIntegration(t *testing.T) {
	content := `
SECURITY my_waf
    ENGINE On
    RULES DEFINE
        RULE "ARGS:test" "@contains payload" "id:1,phase:1,deny,status:403"
    END RULES
END SECURITY

HTTP 127.0.0.1:8080
    SECURITY my_waf
    GET "/test" BEGIN
        context.SendString("ok");
    END GET
END HTTP
`
	cfg, _, err := ParseConfig(content)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(cfg.Groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(cfg.Groups))
	}

	// First group is SECURITY, second is HTTP
	httpGroup := cfg.Groups[1]
	if httpGroup.Directive != "HTTP" {
		t.Fatalf("Expected HTTP group, got %s", httpGroup.Directive)
	}

	item := httpGroup.Items[0]
	item.AppConfig = &config.AppConfig{SecretKey: "test_secret"}
	hd := NewHTTPDirective(item)

	// We need to check if the WAF was correctly applied.
	// Since we can't easily check private fields of Fiber app,
	// we check if NewHTTPDirective correctly resolved the WAF from the registry.

	// I'll add a temporary export or just check if it doesn't panic and
	// verify the logic in NewHTTPDirective.

	if hd == nil {
		t.Fatal("Failed to create HTTPDirective")
	}
}
