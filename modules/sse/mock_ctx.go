package sse

import (
	"github.com/gofiber/fiber/v3"
)

// fiberMockCtx implémente fiber.Ctx pour tester attach() + send().
// Seules Locals() et Cookies() sont fonctionnelles — les autres paniquent
// si appelées, ce qui détecte immédiatement tout usage inattendu.
type fiberMockCtx struct {
	fiber.Ctx
	locals  map[string]any
	cookies map[string]string
}

func newFiberMock(cookies map[string]string) *fiberMockCtx {
	return &fiberMockCtx{
		locals:  make(map[string]any),
		cookies: cookies,
	}
}

// Les deux méthodes réellement utilisées par Loader :

func (m *fiberMockCtx) Locals(key any, value ...any) any {
	k, ok := key.(string)
	if !ok {
		return nil
	}
	if len(value) > 0 {
		m.locals[k] = value[0]
	}
	return m.locals[k]
}

func (m *fiberMockCtx) Cookies(key string, defaultValue ...string) string {
	if v, ok := m.cookies[key]; ok {
		return v
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

func (m *fiberMockCtx) Params(key string, defaultValue ...string) string {
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

func (m *fiberMockCtx) Query(key string, defaultValue ...string) string {
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

