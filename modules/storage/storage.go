package storage

import (
	"encoding/json"
	"fmt"
	"http-server/modules"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	_ "modernc.org/sqlite"
)

var (
	persistentDB *gorm.DB
	volatileDB   *gorm.DB
)

type StorageItem struct {
	Name      string    `gorm:"primaryKey"`
	SessionID string    `gorm:"primaryKey;column:session_id"`
	TTL       time.Time `gorm:"column:ttl"`
	Value     string    `gorm:"type:text"` // JSON encoded
}

func (StorageItem) TableName() string {
	return "storage_items"
}

const SessionTTL = "3h"

type Module struct{}

func (s *Module) Name() string {
	return "storage"
}

func (s *Module) Doc() string {
	return "Session and Cache module using SurrealDB"
}

// ToJSObject exposes the module as a SharedObject (processor.RegisterGlobal).
func (m *Module) ToJSObject(vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()
	m.Loader(nil, vm, obj)
	return obj
}
func (s *Module) Loader(c any, vm *goja.Runtime, moduleObject *goja.Object) {
	// CommonJS support: if exports exists, use it as the target
	module := moduleObject
	if exp := moduleObject.Get("exports"); exp != nil && !goja.IsUndefined(exp) {
		module = exp.ToObject(vm)
	}

	ctx, _ := c.(fiber.Ctx)
	var (
		jwtSecret          = []byte("secret")
		jwtSigningMethod   = jwt.SigningMethodHS256
		jwtCookieNames     = []string{"jwtToken", "jwt", "token"}
		jwtQueryNames      = []string{"jwttoken", "jwt-token", "jwt_token", "jwt", "token"}
		sessionCookieNames = []string{"sid"}
		sessionQueryNames  = []string{"sid"}
	)
	// Expose the API
	o := module
	o.Set("config", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		opts := call.Argument(0).ToObject(vm)
		if v := opts.Get("jwtSecret"); v != nil && !goja.IsUndefined(v) {
			jwtSecret = []byte(v.String())
		}
		if v := opts.Get("jwtSigningMethod"); v != nil && !goja.IsUndefined(v) {
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
		if v := opts.Get("jwtCookieNames"); v != nil && !goja.IsUndefined(v) {
			if arr, ok := v.Export().([]interface{}); ok {
				jwtCookieNames = make([]string, len(arr))
				for i, x := range arr {
					jwtCookieNames[i] = fmt.Sprint(x)
				}
			}
		}
		if v := opts.Get("jwtQueryNames"); v != nil && !goja.IsUndefined(v) {
			if arr, ok := v.Export().([]interface{}); ok {
				jwtQueryNames = make([]string, len(arr))
				for i, x := range arr {
					jwtQueryNames[i] = fmt.Sprint(x)
				}
			}
		}
		if v := opts.Get("sessionCookieNames"); v != nil && !goja.IsUndefined(v) {
			if arr, ok := v.Export().([]interface{}); ok {
				sessionCookieNames = make([]string, len(arr))
				for i, x := range arr {
					sessionCookieNames[i] = fmt.Sprint(x)
				}
			}
		}
		if v := opts.Get("sessionQueryNames"); v != nil && !goja.IsUndefined(v) {
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

}

func (s *Module) createStoreObject(vm *goja.Runtime, db *gorm.DB, tableName string, sessionID string) *goja.Object {
	obj := vm.NewObject()

	// Helper to get item
	getItem := func(key string) (*StorageItem, error) {
		if db == nil {
			return nil, fmt.Errorf("database not initialized")
		}
		var item StorageItem
		err := db.Where("session_id = ? AND name = ?", sessionID, key).First(&item).Error
		if err != nil {
			return nil, err
		}
		// Check expiry
		if time.Now().After(item.TTL) {
			db.Delete(&item) // Auto-cleanup on access
			return nil, gorm.ErrRecordNotFound
		}
		// Slide window
		ttlDuration, _ := time.ParseDuration(SessionTTL)
		item.TTL = time.Now().Add(ttlDuration)
		db.Save(&item) // Update TTL
		return &item, nil
	}

	// Helper to set item
	setItem := func(key string, val interface{}) error {
		if db == nil {
			return fmt.Errorf("database not initialized")
		}
		jsonVal, err := json.Marshal(val)
		if err != nil {
			return err
		}
		ttlDuration, _ := time.ParseDuration(SessionTTL)
		item := StorageItem{
			Name:      key,
			SessionID: sessionID,
			TTL:       time.Now().Add(ttlDuration),
			Value:     string(jsonVal),
		}
		return db.Save(&item).Error
	}

	// Helper to extract value (JSON decode)
	extract := func(item *StorageItem) interface{} {
		if item == nil {
			return nil
		}
		var val interface{}
		json.Unmarshal([]byte(item.Value), &val)
		return val
	}

	// NUM operations
	num := vm.NewObject()
	obj.Set("num", num)

	num.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		item, _ := getItem(key)
		return vm.ToValue(extract(item))
	})

	setNumOp := func(name string, op func(float64, float64) float64) {
		num.Set(name, func(call goja.FunctionCall) goja.Value {
			key := call.Argument(0).String()
			val := call.Argument(1).ToFloat()

			// Get current
			item, _ := getItem(key)
			current := 0.0
			if item != nil {
				v := extract(item)
				if f, ok := v.(float64); ok {
					current = f
				} else if i, ok := v.(int64); ok {
					current = float64(i)
				}
			}

			// Calc new
			newVal := op(current, val)
			setItem(key, newVal)
			return vm.ToValue(newVal)
		})
	}

	setNumOp("incr", func(a, b float64) float64 { return a + b })
	setNumOp("decr", func(a, b float64) float64 { return a - b })
	setNumOp("mul", func(a, b float64) float64 { return a * b })
	setNumOp("div", func(a, b float64) float64 { return a / b })
	setNumOp("mod", func(a, b float64) float64 { return float64(int(a) % int(b)) })
	setNumOp("divInt", func(a, b float64) float64 { return float64(int(a) / int(b)) })

	// Aliases
	num.Set("add", num.Get("incr"))
	num.Set("sub", num.Get("decr"))

	num.Set("defined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		item, _ := getItem(key)
		return vm.ToValue(item != nil)
	})

	num.Set("define", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).ToFloat()
		item, _ := getItem(key)
		if item == nil {
			setItem(key, val)
		}
		return goja.Undefined()
	})

	num.Set("undefine", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		if db != nil {
			db.Where("session_id = ? AND name = ?", sessionID, key).Delete(&StorageItem{})
		}
		return goja.Undefined()
	})

	num.Set("undefined", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		item, _ := getItem(key)
		return vm.ToValue(item == nil)
	})

	// LIST operations
	list := vm.NewObject()
	obj.Set("list", list)
	list.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		item, _ := getItem(key)
		return vm.ToValue(extract(item))
	})

	list.Set("push", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		item, _ := getItem(key)
		var arr []interface{}
		if item != nil {
			res := extract(item)
			if a, ok := res.([]interface{}); ok {
				arr = a
			}
		}
		arr = append(arr, val)
		setItem(key, arr)
		return goja.Undefined()
	})

	list.Set("pop", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		item, _ := getItem(key)
		var arr []interface{}
		if item != nil {
			res := extract(item)
			if a, ok := res.([]interface{}); ok {
				arr = a
			}
		}
		if len(arr) > 0 {
			arr = arr[:len(arr)-1]
		}
		setItem(key, arr)
		return goja.Undefined()
	})

	// Helpers for list ops
	list.Set("defined", num.Get("defined"))
	list.Set("define", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		item, _ := getItem(key)
		if item == nil {
			setItem(key, val)
		}
		return goja.Undefined()
	})
	list.Set("undefine", num.Get("undefine"))
	list.Set("undefined", num.Get("undefined"))

	// HASH operations
	hash := vm.NewObject()
	obj.Set("hash", hash)

	hash.Set("set", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		setItem(key, val)
		return goja.Undefined()
	})

	hash.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		item, _ := getItem(key)
		return vm.ToValue(extract(item))
	})

	hash.Set("has", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		item, _ := getItem(key)
		return vm.ToValue(item != nil)
	})

	hash.Set("keys", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		item, _ := getItem(key)
		var val interface{}
		if item != nil {
			val = extract(item)
		}

		if m, ok := val.(map[string]interface{}); ok {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			return vm.ToValue(keys)
		}
		return vm.ToValue([]string{})
	})

	hash.Set("defined", num.Get("defined"))
	hash.Set("define", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		item, _ := getItem(key)
		if item == nil {
			setItem(key, val)
		}
		return goja.Undefined()
	})
	hash.Set("undefine", num.Get("undefine"))
	hash.Set("undefined", num.Get("undefined"))

	str := vm.NewObject()
	obj.Set("str", str)
	str.Set("get", hash.Get("get"))
	str.Set("concat", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).String()
		item, _ := getItem(key)
		current := ""
		if item != nil {
			if s, ok := extract(item).(string); ok {
				current = s
			}
		}
		setItem(key, current+val)
		return goja.Undefined()
	})

	str.Set("defined", num.Get("defined"))
	str.Set("define", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).String()
		item, _ := getItem(key)
		if item == nil {
			setItem(key, val)
		}
		return goja.Undefined()
	})
	str.Set("undefine", num.Get("undefine"))
	str.Set("undefined", num.Get("undefined"))

	return obj
}

func extractVal(res interface{}) interface{} {
	return res
}

func (s *Module) cleanupLoop(db *gorm.DB, tableName string) {
	if db == nil {
		return
	}
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		db.Where("ttl < ?", time.Now()).Delete(&StorageItem{})
	}
}

func init() {
	// Initialize GORM / SQLite
	var err error
	dataDir := ".data"
	os.MkdirAll(dataDir, 0755)

	persistentPath := filepath.Join(dataDir, "sessions.db")
	persistentDB, err = gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: persistentPath}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		log.Printf("Failed to init persistent session DB: %v", err)
	} else {
		// WAL mode for concurrency
		persistentDB.Exec("PRAGMA journal_mode = WAL")
		persistentDB.AutoMigrate(&StorageItem{})
		go (&Module{}).cleanupLoop(persistentDB, "session")
	}

	volatileDB, err = gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		log.Printf("Failed to init volatile session DB: %v", err)
	} else {
		// WAL mode even for memory? memory default is fine.
		volatileDB.AutoMigrate(&StorageItem{})
		go (&Module{}).cleanupLoop(volatileDB, "volatile")
	}

	modules.RegisterModule(&Module{})
}
