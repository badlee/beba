package httpserver

import (
	"fmt"
	"http-server/processor"
	"strconv"
	"strings"
	"sync"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/types"
	"github.com/gofiber/fiber/v3"
)

var (
	wafRegistry = make(map[string]*WAFConfig)
	wafMu       sync.RWMutex
)

// RegisterWAF adds a WAF configuration to the global registry.
func RegisterWAF(name string, cfg *WAFConfig) {
	wafMu.Lock()
	defer wafMu.Unlock()
	wafRegistry[name] = cfg
}

// GetWAF retrieves a WAF configuration from the global registry.
func GetWAF(name string) *WAFConfig {
	wafMu.RLock()
	defer wafMu.RUnlock()
	return wafRegistry[name]
}

// WAFMiddleware creates a Fiber middleware that uses Coraza WAF.
func WAFMiddleware(cfg *WAFConfig) fiber.Handler {
	if cfg == nil || !cfg.Enabled {
		return func(c fiber.Ctx) error { return c.Next() }
	}

	wafCfg := coraza.NewWAFConfig()

	// 1. Core Rules & Engine
	for _, rule := range cfg.Rules {
		if strings.HasPrefix(rule, "Include ") || strings.HasPrefix(rule, "SecRule") || strings.HasPrefix(rule, "SecAction") || strings.HasPrefix(rule, "SecMarker") {
			wafCfg = wafCfg.WithDirectives(rule)
		} else {
			wafCfg = wafCfg.WithDirectives(fmt.Sprintf("Include %s", rule))
		}
	}

	if cfg.Engine != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecRuleEngine %s", cfg.Engine))
	}

	// 2. Request Limits
	if cfg.RequestBodyAccess {
		wafCfg = wafCfg.WithDirectives("SecRequestBodyAccess On")
	} else {
		wafCfg = wafCfg.WithDirectives("SecRequestBodyAccess Off")
	}

	if cfg.RequestBodyLimit > 0 {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecRequestBodyLimit %d", cfg.RequestBodyLimit))
	}
	if cfg.RequestBodyInMemory > 0 {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecRequestBodyInMemoryLimit %d", cfg.RequestBodyInMemory))
	}
	if cfg.RequestBodyLimitAction != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecRequestBodyLimitAction %s", cfg.RequestBodyLimitAction))
	}
	if cfg.RequestBodyJsonDepth > 0 {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecRequestBodyJsonDepthLimit %d", cfg.RequestBodyJsonDepth))
	}
	if cfg.ArgumentsLimit > 0 {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecArgumentsLimit %d", cfg.ArgumentsLimit))
	}

	// 3. Response Limits
	if cfg.ResponseBodyAccess {
		wafCfg = wafCfg.WithDirectives("SecResponseBodyAccess On")
	} else {
		wafCfg = wafCfg.WithDirectives("SecResponseBodyAccess Off")
	}
	if cfg.ResponseBodyLimit > 0 {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecResponseBodyLimit %d", cfg.ResponseBodyLimit))
	}
	if cfg.ResponseBodyLimitAction != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecResponseBodyLimitAction %s", cfg.ResponseBodyLimitAction))
	}
	if cfg.ClearResponseBodyMimes {
		wafCfg = wafCfg.WithDirectives("SecResponseBodyMimeTypesClear")
	}
	if len(cfg.ResponseBodyMimes) > 0 {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecResponseBodyMimeType %s", strings.Join(cfg.ResponseBodyMimes, " ")))
	}

	// 4. Audit Logging
	if cfg.AuditEngine != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecAuditEngine %s", cfg.AuditEngine))
	}
	if cfg.AuditLogPath != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecAuditLog %s", cfg.AuditLogPath))
	}
	if cfg.AuditLogDirMode > 0 {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecAuditLogDirMode %04o", cfg.AuditLogDirMode))
	}
	if cfg.AuditLogFileMode > 0 {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecAuditLogFileMode %04o", cfg.AuditLogFileMode))
	}
	if cfg.AuditLogFormat != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecAuditLogFormat %s", cfg.AuditLogFormat))
	}
	if cfg.AuditLogParts != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecAuditLogParts %s", cfg.AuditLogParts))
	}
	if cfg.AuditLogRelevantStatus != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecAuditLogRelevantStatus %s", cfg.AuditLogRelevantStatus))
	}
	if cfg.AuditLogStorageDir != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecAuditLogStorageDir %s", cfg.AuditLogStorageDir))
	}
	if cfg.AuditLogType != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecAuditLogType %s", cfg.AuditLogType))
	}
	if cfg.ComponentSignature != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecComponentSignature %s", cfg.ComponentSignature))
	}

	// 5. Debug
	if cfg.DebugLogPath != "" {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecDebugLog %s", cfg.DebugLogPath))
	}
	if cfg.DebugLogLevel > 0 {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("SecDebugLogLevel %d", cfg.DebugLogLevel))
	}

	waf, err := coraza.NewWAF(wafCfg)
	if err != nil {
		fmt.Printf("WAF Init Error: %v\n", err)
		return func(c fiber.Ctx) error { return c.Next() }
	}

	runHook := func(c fiber.Ctx, event string, tx types.Transaction) {
		if hook, ok := cfg.Hooks[event]; ok {
			vm := processor.New("", c, nil) // BaseDir empty for now
			vm.Set("tx", tx)
			vm.Set("event", event)
			for k, v := range hook.Args {
				vm.Set(k, v)
			}
			if hook.Inline {
				_, err := vm.RunString(hook.Handler)
				if err != nil {
					fmt.Printf("WAF Hook Error [%s]: %v\n", event, err)
				}
			} else {
				_, err := processor.ProcessFile(hook.Handler, c, nil)
				if err != nil {
					fmt.Printf("WAF Hook Error [%s]: %v\n", event, err)
				}
			}
		}
	}

	runHook(nil, "INIT", nil)

	return func(c fiber.Ctx) error {
		tx := waf.NewTransaction()
		defer tx.Close()

		// 1. Process Connection
		port, _ := strconv.Atoi(c.Port())
		tx.ProcessConnection(c.IP(), port, "127.0.0.1", 80)
		runHook(c, "CONNECTION", tx)

		// 2. Process URI, Method, Protocol
		tx.ProcessURI(c.OriginalURL(), c.Method(), c.Protocol())
		runHook(c, "URI", tx)

		// 3. Process Request Headers
		for k, v := range c.GetReqHeaders() {
			for _, val := range v {
				tx.AddRequestHeader(k, val)
			}
		}
		tx.ProcessRequestHeaders()
		runHook(c, "REQUEST_HEADERS", tx)

		if it := tx.Interruption(); it != nil {
			runHook(c, "INTERRUPTED", tx)
			RecordSecurityBlock(cfg.AppName, "waf")
			for _, rule := range tx.MatchedRules() {
				RecordWAFHit(cfg.AppName, strconv.Itoa(rule.Rule().ID()))
			}
			return handleInterruption(c, it, cfg)
		}

		// 4. Process Request Body
		body := c.Body()
		if len(body) > 0 {
			_, _, err := tx.WriteRequestBody(body)
			if err != nil {
				runHook(c, "ERROR", tx)
				return c.Status(fiber.StatusInternalServerError).SendString("WAF Error")
			}
		}
		_, err = tx.ProcessRequestBody()
		if err != nil {
			runHook(c, "ERROR", tx)
			return c.Status(fiber.StatusInternalServerError).SendString("WAF Error")
		}
		runHook(c, "REQUEST_BODY", tx)

		if it := tx.Interruption(); it != nil {
			runHook(c, "INTERRUPTED", tx)
			RecordSecurityBlock(cfg.AppName, "waf")
			for _, rule := range tx.MatchedRules() {
				RecordWAFHit(cfg.AppName, strconv.Itoa(rule.Rule().ID()))
			}
			return handleInterruption(c, it, cfg)
		}

		// 5. Call next handler
		err = c.Next()
		if err != nil {
			return err
		}

		// 6. Process Response Headers
		for k, v := range c.GetRespHeaders() {
			for _, val := range v {
				tx.AddResponseHeader(k, val)
			}
		}
		tx.ProcessResponseHeaders(c.Response().StatusCode(), c.Protocol())
		runHook(c, "RESPONSE_HEADERS", tx)

		// 7. Process Response Body
		resBody := c.Response().Body()
		if len(resBody) > 0 {
			_, _, err := tx.WriteResponseBody(resBody)
			if err != nil {
				runHook(c, "ERROR", tx)
				return nil
			}
		}
		_, err = tx.ProcessResponseBody()
		if err != nil {
			runHook(c, "ERROR", tx)
			return nil
		}
		runHook(c, "RESPONSE_BODY", tx)

		if it := tx.Interruption(); it != nil {
			runHook(c, "INTERRUPTED", tx)
			RecordSecurityBlock(cfg.AppName, "waf")
			for _, rule := range tx.MatchedRules() {
				RecordWAFHit(cfg.AppName, strconv.Itoa(rule.Rule().ID()))
			}
			return handleInterruption(c, it, cfg)
		}

		tx.ProcessLogging()
		runHook(c, "LOGGING", tx)

		return nil
	}
}

func handleInterruption(c fiber.Ctx, it *types.Interruption, cfg *WAFConfig) error {
	code := it.Status
	if code == 0 {
		code = fiber.StatusForbidden
	}

	if cfg.BlockPage != "" {
		return c.Status(code).SendFile(cfg.BlockPage)
	}

	// Audit Log (Layer 5)
	if GlobalSecLogger != nil {
		GlobalSecLogger.RecordEvent(c, "waf_block", map[string]any{
			"action": it.Action,
			"msg":    fmt.Sprintf("Blocked by WAF (Action: %s)", it.Action),
		})
	}

	return c.Status(code).SendString(fmt.Sprintf("Blocked by WAF (Action: %s)", it.Action))
}
