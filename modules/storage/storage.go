package storage

import (
	"fmt"
	"http-server/modules"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"http-server/plugins/surrealdb-embedded"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	persistentDB *surrealdb.DB
	volatileDB   *surrealdb.DB
)

const SessionTTL = "3h"

type Module struct{}

func (s *Module) Name() string {
	return "storage"
}

func (s *Module) Doc() string {
	return "Session and Cache module using SurrealDB"
}

func (s *Module) Loader(ctx fiber.Ctx, vm *goja.Runtime, module *goja.Object) {
	var (
		jwtSecret          = []byte("secret")
		jwtSigningMethod   = jwt.SigningMethodHS256
		jwtCookieNames     = []string{"jwtToken", "jwt", "token"}
		jwtQueryNames      = []string{"jwttoken", "jwt-token", "jwt_token", "jwt", "token"}
		sessionCookieNames = []string{"sid"}
		sessionQueryNames  = []string{"sid"}
	)
	// Expose the API
	o := module.Get("exports").(*goja.Object)
	o.Set("config", func(call goja.FunctionCall) goja.Value {
		opts := call.Argument(0).ToObject(vm)
		if v := opts.Get("jwtSecret"); !goja.IsUndefined(v) {
			jwtSecret = []byte(v.String())
		}
		if v := opts.Get("jwtSigningMethod"); !goja.IsUndefined(v) {
			methodName := strings.ToUpper(v.String())
			switch methodName {
			case "HS256":
				jwtSigningMethod = jwt.SigningMethodHS256
			case "HS384":
				jwtSigningMethod = jwt.SigningMethodHS384
			case "HS512":
				jwtSigningMethod = jwt.SigningMethodHS512
			}
		}
		if v := opts.Get("jwtCookieNames"); !goja.IsUndefined(v) {
			if arr, ok := v.Export().([]interface{}); ok {
				jwtCookieNames = make([]string, len(arr))
				for i, x := range arr {
					jwtCookieNames[i] = fmt.Sprint(x)
				}
			}
		}
		if v := opts.Get("jwtQueryNames"); !goja.IsUndefined(v) {
			if arr, ok := v.Export().([]interface{}); ok {
				jwtQueryNames = make([]string, len(arr))
				for i, x := range arr {
					jwtQueryNames[i] = fmt.Sprint(x)
				}
			}
		}
		if v := opts.Get("sessionCookieNames"); !goja.IsUndefined(v) {
			if arr, ok := v.Export().([]interface{}); ok {
				sessionCookieNames = make([]string, len(arr))
				for i, x := range arr {
					sessionCookieNames[i] = fmt.Sprint(x)
				}
			}
		}
		if v := opts.Get("sessionQueryNames"); !goja.IsUndefined(v) {
			if arr, ok := v.Export().([]interface{}); ok {
				sessionQueryNames = make([]string, len(arr))
				for i, x := range arr {
					sessionQueryNames[i] = fmt.Sprint(x)
				}
			}
		}
		return goja.Undefined()
	})

	// session(id)
	o.Set("session", func(call goja.FunctionCall) goja.Value {
		id := ""
		if len(call.Arguments) > 0 {
			id = call.Arguments[0].String()
		} else if ctx != nil {
			// Auto-discovery for Session ID
			for _, name := range sessionCookieNames {
				if v := ctx.Cookies(name); v != "" {
					id = v
					break
				}
			}
			if id == "" {
				for _, name := range sessionQueryNames {
					if v := ctx.Query(name); v != "" {
						id = v
						break
					}
				}
			}
		}

		if id == "" {
			id = "@" // Fallback to anonymous
		}
		return s.createStoreObject(vm, persistentDB, "session", id)
	})

	// shared -> session("@")
	o.Set("shared", s.createStoreObject(vm, persistentDB, "session", "@"))

	// cache -> volatile
	o.Set("cache", s.createStoreObject(vm, volatileDB, "volatile", "#"))

	// JWT Session constructor: new JWTSession(cookieObjOrToken, [cookieName="jwtToken"])
	o.Set("JWTSession", func(call goja.ConstructorCall) *goja.Object {
		arg0 := call.Argument(0)
		var tokenStr string
		var cookieObj *goja.Object
		tokenMode := false

		if arg0.ExportType().Kind() == reflect.String {
			tokenStr = arg0.String()
			tokenMode = true
		} else if !goja.IsUndefined(arg0) && !goja.IsNull(arg0) {
			cookieObj = arg0.ToObject(vm)
		}

		cookieName := "jwtToken"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
			cookieName = call.Arguments[1].String()
		}

		// Auto-discovery for string mode if called with zero args
		if len(call.Arguments) == 0 && ctx != nil {
			// 1. Authorization header
			auth := ctx.Get("Authorization")
			if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
				tokenStr = strings.TrimSpace(auth[7:])
				tokenMode = true
			}
			// 2. Cookies (from configured list)
			if !tokenMode {
				for _, name := range jwtCookieNames {
					if v := ctx.Cookies(name); v != "" {
						tokenStr = v
						tokenMode = true
						cookieName = name
						break
					}
				}
			}
			// 3. Query params (from configured list)
			if !tokenMode {
				for _, name := range jwtQueryNames {
					if v := ctx.Query(name); v != "" {
						tokenStr = v
						tokenMode = true
						break
					}
				}
			}
		}

		secret := jwtSecret
		signingMethod := jwtSigningMethod
		this := call.This
		claims := make(jwt.MapClaims)

		// Helper to load and validate token
		loadToken := func(ts string) error {
			if ts == "" || ts == "undefined" {
				return nil
			}
			token, err := jwt.Parse(ts, func(token *jwt.Token) (interface{}, error) {
				return secret, nil
			})
			if err != nil {
				return err
			}
			if !token.Valid {
				return fmt.Errorf("invalid token")
			}
			if c, ok := token.Claims.(jwt.MapClaims); ok {
				claims = c
			}
			return nil
		}

		if tokenMode {
			if err := loadToken(tokenStr); err != nil {
				vm.Interrupt(fmt.Errorf("Invalid JWT Token: %v", err))
				return nil
			}
		} else if cookieObj != nil {
			// Try to load existing token from cookie
			getVal := cookieObj.Get("get")
			if getFunc, ok := goja.AssertFunction(getVal); ok {
				res, _ := getFunc(goja.Undefined(), vm.ToValue(cookieName))
				ts := res.String()
				loadToken(ts) // Ignore errors for cookies, just start fresh if invalid
			}
		}

		// Helper to get data map safely
		getDataMap := func() map[string]interface{} {
			if d, ok := claims["data"]; ok {
				if dm, ok := d.(map[string]interface{}); ok {
					return dm
				}
			}
			dm := make(map[string]interface{})
			claims["data"] = dm
			return dm
		}

		// Ensure JTI exists
		if _, ok := claims["jti"]; !ok {
			uid, _ := uuid.NewV7()
			claims["jti"] = strings.ReplaceAll(uid.String(), "-", "")
		}

		// Private Claims
		this.Set("set", func(call goja.FunctionCall) goja.Value {
			key := call.Argument(0).String()
			val := call.Argument(1).Export()
			getDataMap()[key] = val
			return goja.Undefined()
		})

		this.Set("get", func(call goja.FunctionCall) goja.Value {
			key := call.Argument(0).String()
			if val, ok := getDataMap()[key]; ok {
				return vm.ToValue(val)
			}
			return goja.Undefined()
		})

		this.Set("remove", func(call goja.FunctionCall) goja.Value {
			key := call.Argument(0).String()
			delete(getDataMap(), key)
			return goja.Undefined()
		})

		this.Set("clear", func(call goja.FunctionCall) goja.Value {
			claims["data"] = make(map[string]interface{})
			return goja.Undefined()
		})

		// Standard Claims
		this.Set("setExpire", func(call goja.FunctionCall) goja.Value {
			claims["exp"] = call.Argument(0).ToInteger()
			return goja.Undefined()
		})
		this.Set("expire", func(call goja.FunctionCall) goja.Value {
			if v, ok := claims["exp"]; ok {
				return vm.ToValue(v)
			}
			return goja.Undefined()
		})

		this.Set("setAudience", func(call goja.FunctionCall) goja.Value {
			claims["aud"] = call.Argument(0).String()
			return goja.Undefined()
		})
		this.Set("audience", func(call goja.FunctionCall) goja.Value {
			if v, ok := claims["aud"]; ok {
				return vm.ToValue(v)
			}
			return goja.Undefined()
		})

		this.Set("setIssuer", func(call goja.FunctionCall) goja.Value {
			claims["iss"] = call.Argument(0).String()
			return goja.Undefined()
		})
		this.Set("issuer", func(call goja.FunctionCall) goja.Value {
			if v, ok := claims["iss"]; ok {
				return vm.ToValue(v)
			}
			return goja.Undefined()
		})

		this.Set("setSubject", func(call goja.FunctionCall) goja.Value {
			claims["sub"] = call.Argument(0).String()
			return goja.Undefined()
		})
		this.Set("subject", func(call goja.FunctionCall) goja.Value {
			if v, ok := claims["sub"]; ok {
				return vm.ToValue(v)
			}
			return goja.Undefined()
		})

		this.Set("setNotBefore", func(call goja.FunctionCall) goja.Value {
			claims["nbf"] = call.Argument(0).ToInteger()
			return goja.Undefined()
		})
		this.Set("notBefore", func(call goja.FunctionCall) goja.Value {
			if v, ok := claims["nbf"]; ok {
				return vm.ToValue(v)
			}
			return goja.Undefined()
		})

		this.Set("setIssuedAt", func(call goja.FunctionCall) goja.Value {
			claims["iat"] = call.Argument(0).ToInteger()
			return goja.Undefined()
		})
		this.Set("issuedAt", func(call goja.FunctionCall) goja.Value {
			if v, ok := claims["iat"]; ok {
				return vm.ToValue(v)
			}
			return goja.Undefined()
		})

		this.Set("jti", func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(claims["jti"])
		})

		// Configuration
		this.Set("setSigningMethod", func(call goja.FunctionCall) goja.Value {
			methodName := strings.ToUpper(call.Argument(0).String())
			switch methodName {
			case "HS256":
				signingMethod = jwt.SigningMethodHS256
			case "HS384":
				signingMethod = jwt.SigningMethodHS384
			case "HS512":
				signingMethod = jwt.SigningMethodHS512
			}
			return goja.Undefined()
		})

		// Actions
		this.Set("getToken", func(call goja.FunctionCall) goja.Value {
			token := jwt.NewWithClaims(signingMethod, claims)
			t, _ := token.SignedString(secret)
			return vm.ToValue(t)
		})

		this.Set("save", func(call goja.FunctionCall) goja.Value {
			if tokenMode || cookieObj == nil {
				return goja.Undefined()
			}
			token := jwt.NewWithClaims(signingMethod, claims)
			t, _ := token.SignedString(secret)
			setVal := cookieObj.Get("set")
			if setFunc, ok := goja.AssertFunction(setVal); ok {
				setFunc(goja.Undefined(), vm.ToValue(cookieName), vm.ToValue(t))
			}
			return goja.Undefined()
		})

		this.Set("destroy", func(call goja.FunctionCall) goja.Value {
			if tokenMode || cookieObj == nil {
				return goja.Undefined()
			}
			removeVal := cookieObj.Get("remove")
			if removeFunc, ok := goja.AssertFunction(removeVal); ok {
				removeFunc(goja.Undefined(), vm.ToValue(cookieName))
			}
			return goja.Undefined()
		})

		return nil
	})

	// Session constructor: new Session(cookieObjOrSessionID, [cookieName="sid"])
	o.Set("Session", func(call goja.ConstructorCall) *goja.Object {
		cookieObj := call.Argument(0).ToObject(vm)
		cookieName := "sid"
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
			cookieName = call.Arguments[1].String()
		}

		var sessionID string
		// Try to get session ID from specified cookie
		sidVal := cookieObj.Get("get")
		if sidFunc, ok := goja.AssertFunction(sidVal); ok {
			res, _ := sidFunc(goja.Undefined(), vm.ToValue(cookieName))
			sessionID = res.String()
		}

		if sessionID == "" || sessionID == "undefined" {
			// Generate new ID using UUIDv7
			uid, err := uuid.NewV7()
			if err != nil {
				sessionID = uuid.NewString() // Fallback
			} else {
				sessionID = uid.String()
			}
			sessionID = strings.ReplaceAll(sessionID, "-", "")
			// Set it back
			setVal := cookieObj.Get("set")
			if setFunc, ok := goja.AssertFunction(setVal); ok {
				setFunc(goja.Undefined(), vm.ToValue(cookieName), vm.ToValue(sessionID))
			}
		}

		this := call.This
		store := s.createStoreObject(vm, persistentDB, "session", sessionID)

		// Map store properties to this
		for _, k := range store.Keys() {
			this.Set(k, store.Get(k))
		}
		this.Set("id", sessionID)

		return nil // constructors return this by default if returning nil
	})

	module.Set("exports", o) // Export Session constructor as default
}

func (s *Module) createStoreObject(vm *goja.Runtime, db *surrealdb.DB, tableName string, sessionID string) *goja.Object {
	obj := vm.NewObject()

	// Helper to run query with TTL update
	run := func(query string, params map[string]interface{}) (interface{}, error) {
		res, err := db.Query(strings.TrimSpace(query), params)
		if err != nil {
			return nil, err
		}

		if sessionID != "@" && sessionID != "#" {
			// Update expiration on every access (sliding window)
			ttlQ := fmt.Sprintf("UPDATE %s:%s SET expires_at = time::now() + %s", tableName, sessionID, SessionTTL)
			db.Query(ttlQ, nil)
		}

		if len(res) > 0 {
			return res[0], nil
		}
		return nil, nil
	}

	// NUM operations: /, +, -, *, %, ~/
	num := vm.NewObject()
	obj.Set("num", num)

	genericGet := func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	}

	num.Set("get", genericGet)

	setNumOp := func(name string, op string) {
		num.Set(name, func(call goja.FunctionCall) goja.Value {
			key := call.Argument(0).String()
			val := call.Argument(1).ToFloat()

			q := fmt.Sprintf("UPDATE %s:%s SET %s %s= $val", tableName, sessionID, key, op)
			if op == "/" || op == "*" || op == "%" {
				q = fmt.Sprintf("UPDATE %s:%s SET %s = (%s ?? 0) %s $val", tableName, sessionID, key, key, op)
			}
			if op == "~/" { // Floor div
				q = fmt.Sprintf("UPDATE %s:%s SET %s = math::floor((%s ?? 0) / $val)", tableName, sessionID, key, key)
			}

			res, err := run(q, map[string]interface{}{"val": val})
			if err != nil {
				panic(vm.ToValue(fmt.Sprintf("SessionError: %v", err)))
			}
			// Return updated value
			if m, ok := res.(map[string]interface{}); ok {
				return vm.ToValue(m[key])
			}
			return goja.Undefined()
		})
	}

	setNumOp("add", "+")
	num.Set("incr", num.Get("add"))
	setNumOp("sub", "-")
	num.Set("decr", num.Get("sub"))
	setNumOp("mul", "*")
	setNumOp("div", "/")
	setNumOp("mod", "%")
	setNumOp("divInt", "~/")

	num.Set("defined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s != NONE as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	num.Set("define", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).ToFloat()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = %s ?? $val", tableName, sessionID, key, key)
		run(q, map[string]interface{}{"val": val})
		return goja.Undefined()
	})

	num.Set("undefine", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = NONE", tableName, sessionID, key)
		run(q, nil)
		return goja.Undefined()
	})

	num.Set("undefined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s == NONE as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	// LIST operations: +(push), -(pop), ^+(shift), ^-(unshift), MIN, MAX, COUNT, SUM, AVG
	list := vm.NewObject()
	obj.Set("list", list)

	list.Set("get", genericGet)

	list.Set("push", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		q := fmt.Sprintf("UPDATE %s:%s SET %s += [$val]", tableName, sessionID, key)
		run(q, map[string]interface{}{"val": val})
		return goja.Undefined()
	})

	list.Set("pop", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = %s[..-1]", tableName, sessionID, key, key)
		run(q, nil)
		return goja.Undefined()
	})

	list.Set("shift", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = %s[1..]", tableName, sessionID, key, key)
		run(q, nil)
		return goja.Undefined()
	})

	list.Set("unshift", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = [$val] + (%s ?? [])", tableName, sessionID, key, key)
		run(q, map[string]interface{}{"val": val})
		return goja.Undefined()
	})

	list.Set("min", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT math::min(%s) as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	for _, op := range []string{"max", "count", "sum", "avg"} {
		opName := op
		list.Set(opName, func(call goja.FunctionCall) goja.Value {
			key := call.Argument(0).String()
			q := fmt.Sprintf("SELECT math::%s(%s) as val FROM %s:%s", opName, key, tableName, sessionID)
			res, _ := run(q, nil)
			return vm.ToValue(extractVal(res))
		})
	}

	list.Set("defined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s != NONE as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	list.Set("define", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = %s ?? $val", tableName, sessionID, key, key)
		run(q, map[string]interface{}{"val": val})
		return goja.Undefined()
	})

	list.Set("undefine", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = NONE", tableName, sessionID, key)
		run(q, nil)
		return goja.Undefined()
	})

	list.Set("undefined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s == NONE as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	// STRING operations: SUB, CONCAT, SPLIT, AT, CODE_AT
	str := vm.NewObject()
	obj.Set("str", str)

	str.Set("get", genericGet)

	str.Set("concat", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).String()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = (%s ?? '') + $val", tableName, sessionID, key, key)
		run(q, map[string]interface{}{"val": val})
		return goja.Undefined()
	})

	str.Set("sub", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		start := call.Argument(1).ToInteger()
		end := call.Argument(2).ToInteger()
		q := fmt.Sprintf("SELECT string::slice(%s, $start, $end) as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, map[string]interface{}{"start": start, "end": end})
		return vm.ToValue(extractVal(res))
	})

	str.Set("split", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		sep := call.Argument(1).String()
		q := fmt.Sprintf("SELECT string::split(%s, $sep) as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, map[string]interface{}{"sep": sep})
		return vm.ToValue(extractVal(res))
	})

	str.Set("at", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		pos := call.Argument(1).ToInteger()
		q := fmt.Sprintf("SELECT string::slice(%s, $pos, $pos + 1) as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, map[string]interface{}{"pos": pos})
		return vm.ToValue(extractVal(res))
	})

	str.Set("defined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s != NONE as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	str.Set("define", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).String()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = %s ?? $val", tableName, sessionID, key, key)
		run(q, map[string]interface{}{"val": val})
		return goja.Undefined()
	})

	str.Set("undefine", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = NONE", tableName, sessionID, key)
		run(q, nil)
		return goja.Undefined()
	})

	str.Set("undefined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s == NONE as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	// HASH operations: GET, SET, HAS, KEYS, VALUES, ENTRIES
	hash := vm.NewObject()
	obj.Set("hash", hash)

	hash.Set("set", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = $val", tableName, sessionID, key)
		run(q, map[string]interface{}{"val": val})
		return goja.Undefined()
	})

	hash.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	hash.Set("has", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s != NONE as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	hash.Set("keys", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		// Get the object and extract keys in Go
		q := fmt.Sprintf("SELECT %s as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		val := extractVal(res)
		if m, ok := val.(map[string]interface{}); ok {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			return vm.ToValue(keys)
		}
		return vm.ToValue([]string{})
	})

	hash.Set("all", func(call goja.FunctionCall) goja.Value {
		q := fmt.Sprintf("SELECT * FROM %s:%s", tableName, sessionID)
		res, _ := run(q, nil)
		if m, ok := res.(map[string]interface{}); ok {
			if items, ok := m["result"].([]interface{}); ok && len(items) > 0 {
				return vm.ToValue(items[0])
			}
			return vm.ToValue(res)
		}
		return goja.Undefined()
	})

	hash.Set("defined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s != NONE as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})

	hash.Set("define", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = %s ?? $val", tableName, sessionID, key, key)
		run(q, map[string]interface{}{"val": val})
		return goja.Undefined()
	})

	hash.Set("undefine", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("UPDATE %s:%s SET %s = NONE", tableName, sessionID, key)
		run(q, nil)
		return goja.Undefined()
	})

	hash.Set("undefined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		q := fmt.Sprintf("SELECT %s == NONE as val FROM %s:%s", key, tableName, sessionID)
		res, _ := run(q, nil)
		return vm.ToValue(extractVal(res))
	})
	run(fmt.Sprintf("CREATE %s:%s SET create_at = time::now()", tableName, sessionID), nil)

	return obj
}

func extractVal(res interface{}) interface{} {
	if m, ok := res.(map[string]interface{}); ok {
		if val, ok := m["val"]; ok {
			return val
		}
		if result, ok := m["result"]; ok {
			if arr, ok := result.([]interface{}); ok && len(arr) > 0 {
				if first, ok := arr[0].(map[string]interface{}); ok {
					return first["val"]
				}
			}
		}
	}
	return nil
}

func (s *Module) cleanupLoop(db *surrealdb.DB, tableName string) {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		q := fmt.Sprintf("DELETE %s WHERE expires_at < time::now()", tableName)
		db.Query(q, nil)
	}
}

func init() {
	// Initialize SurrealDB
	var err error
	dataDir := "./data"
	os.MkdirAll(dataDir, 0755)

	persistentPath := filepath.Join(dataDir, "sessions.db")
	persistentDB, err = surrealdb.NewSurrealKV(persistentPath)
	if err != nil {
		log.Printf("Failed to init persistent session DB: %v", err)
	} else {
		persistentDB.Use("http-server", "persistent")
		go (&Module{}).cleanupLoop(persistentDB, "session")
	}

	volatileDB, err = surrealdb.NewMemory()
	if err != nil {
		log.Printf("Failed to init volatile session DB: %v", err)
	} else {
		volatileDB.Use("http-server", "volatile")
		go (&Module{}).cleanupLoop(volatileDB, "volatile")
	}

	modules.RegisterModule(&Module{})
}
