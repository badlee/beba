package storage

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/dop251/goja"

	_ "modernc.org/sqlite"
)

func TestJWTSession(t *testing.T) {
	vm := goja.New()

	// Mock cookie object
	cookieData := make(map[string]string)
	cookies := vm.NewObject()
	cookies.Set("get", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		if val, ok := cookieData[name]; ok {
			return vm.ToValue(val)
		}
		return goja.Undefined()
	})
	cookies.Set("set", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		val := call.Argument(1).String()
		cookieData[name] = val
		return goja.Undefined()
	})
	cookies.Set("remove", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		delete(cookieData, name)
		return goja.Undefined()
	})

	// Register module manually for test
	mod := &Module{}
	exports := vm.NewObject()
	module := vm.NewObject()
	module.Set("exports", exports)
	mod.Loader(nil, vm, module)

	vm.Set("cookies", cookies)
	vm.Set("cookieData", cookieData)
	vm.Set("JWTSession", exports.Get("JWTSession"))

	// Test case 1: Create and save
	_, err := vm.RunString(`
		var sess = new JWTSession(cookies);
		sess.set("foo", "bar");
		sess.setExpire(4102444800); // Year 2100
		sess.save();
	`)
	if err != nil {
		t.Fatalf("JS Execution failed: %v", err)
	}

	token := cookieData["jwtToken"]
	if token == "" {
		t.Fatal("JWT token not saved to cookie")
	}

	// Test case 2: Load and verify
	_, err = vm.RunString(`
		var sess2 = new JWTSession(cookies);
		if (sess2.get("foo") !== "bar") throw new Error("Data not loaded: " + sess2.get("foo"));
		if (sess2.expire() !== 4102444800) throw new Error("Expire not loaded: " + sess2.expire());
	`)
	if err != nil {
		t.Fatalf("JS Verification failed: %v", err)
	}

	// Test case 3: Remove and clear
	_, err = vm.RunString(`
		sess = new JWTSession(cookies);
		sess.remove("foo");
		if (sess.get("foo") !== undefined) throw new Error("Remove failed");
		sess.set("a", 1);
		sess.clear();
		if (sess.get("a") !== undefined) throw new Error("Clear failed");
	`)
	if err != nil {
		t.Fatalf("JS Actions failed: %v", err)
	}

	// Test case 4: Direct token initialization
	_, err = vm.RunString(`
		const token = new JWTSession(cookies).getToken();
		const sess3 = new JWTSession(token);
		if (sess3.jti() === undefined) throw new Error("JTI not loaded from token");
	`)
	if err != nil {
		t.Fatalf("JS Token Init failed: %v", err)
	}

	// Test case 5: Validation failure (Interruption)
	_, err = vm.RunString(`new JWTSession("invalid.token.string");`)
	if err == nil {
		t.Fatal("Validation should have triggered interruption")
	}
	if !strings.Contains(err.Error(), "Invalid JWT Token") {
		t.Fatalf("Unexpected error: %v", err)
	}
	vm.ClearInterrupt()

	// Test case 6: setSigningMethod
	_, err = vm.RunString(`
		const sHS512 = new JWTSession(cookies);
		sHS512.setSigningMethod("HS512");
		const t512 = sHS512.getToken();
		// We can't easily verify the header here without a JWT lib in JS, 
		// but we can verify it parses back.
		const sVerify = new JWTSession(t512);
	`)
	if err != nil {
		t.Fatalf("JS SigningMethod failed: %v", err)
	}

	// Test case 7: save/destroy are no-ops in token mode
	_, err = vm.RunString(`
		const sToken = new JWTSession(new JWTSession(cookies).getToken());
		cookieData["jwtToken"] = "original";
		sToken.save();
		if (cookieData["jwtToken"] !== "original") throw new Error("Save should be no-op");
		sToken.destroy();
		if (cookieData["jwtToken"] !== "original") throw new Error("Destroy should be no-op");
	`)
	if err != nil {
		t.Fatalf("JS No-op test failed: %v", err)
	}
}

func TestStorageUnset(t *testing.T) {
	// Initialize memory DB for test
	db, err := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	db.AutoMigrate(&StorageItem{})

	vm := goja.New()
	mod := &Module{}

	// Hijack persistentDB for the test.
	oldPersistent := persistentDB
	persistentDB = db
	defer func() { persistentDB = oldPersistent }()

	exports := vm.NewObject()
	module := vm.NewObject()
	module.Set("exports", exports)
	mod.Loader(nil, vm, module)

	vm.Set("sess", exports.Get("session"))

	// Test undefine/undefined
	script := `
		const s = sess("sess1");
		s.hash.set("val", 10);
		if (s.hash.undefined("val")) throw new Error("Should be defined (val)");
		s.hash.undefine("val");
		if (!s.hash.undefined("val")) throw new Error("Should be undefined (val)");
		
		s.hash.set("obj", {a:1});
		if (s.hash.undefined("obj")) throw new Error("Should be defined (obj)");
		s.hash.undefine("obj");
		if (!s.hash.undefined("obj")) throw new Error("Hash undefine failed");
	`
	_, err = vm.RunString(script)
	if err != nil {
		t.Fatalf("Storage test failed: %v", err)
	}
}

func TestStorageAdvanced(t *testing.T) {
	vm := goja.New()
	mod := &Module{}
	exports := vm.NewObject()
	module := vm.NewObject()
	module.Set("exports", exports)

	mod.Loader(nil, vm, module) // Passing nil context for config test

	vm.Set("exports", exports)
	script := `
		const { config } = exports;
		config({
			jwtSecret: "new-secret",
			jwtCookieNames: ["my-jwt"]
		});
		// Verify that it accepts empty objects and returns undefined
		if (config({}) !== undefined) throw new Error("config should return undefined");
	`
	_, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("Advanced test failed: %v", err)
	}
}
