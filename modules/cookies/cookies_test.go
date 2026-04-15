package cookies

import (
	"http-server/processor"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type MockCookieCtx struct {
	data map[string]string
}

func (m *MockCookieCtx) Cookies(key string, defaultValue ...string) string {
	if val, ok := m.data[key]; ok {
		return val
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

func (m *MockCookieCtx) Cookie(cookie *fiber.Cookie) *fiber.Cookie {
	m.data[cookie.Name] = cookie.Value
	return cookie
}

func (m *MockCookieCtx) ClearCookie(key string, path ...string) {
	delete(m.data, key)
}

func TestCookiesModule(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()

	mockCtx := &MockCookieCtx{data: make(map[string]string)}
	mod := &Module{}

	exports := vm.NewObject()
	moduleObj := vm.NewObject()
	moduleObj.Set("exports", exports)

	mod.Loader(mockCtx, vm.Runtime, moduleObj)

	vm.Set("cookies", exports)

	script := `
		cookies.set("user", "john_doe");
		if (cookies.get("user") !== "john_doe") throw new Error("Set/Get failed");
		if (!cookies.has("user")) throw new Error("Has failed before remove");
		
		cookies.remove("user");
		if (cookies.has("user")) throw new Error("Has failed after remove");
		if (cookies.get("user") !== "") throw new Error("Get failed after remove");
	`

	_, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("JS Execution failed: %v", err)
	}
}
