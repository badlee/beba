package db

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gorm.io/driver/clickhouse"
	"gorm.io/driver/gaussdb"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/driver/sqlserver"

	_ "modernc.org/sqlite"
)

/**
* Schémas maintenant supportés
| DB         | Scheme                            |
| ---------- | --------------------------------- |
| SQLite     | `sqlite:file.db` `file:`          |
| SQLite     | `sqlite::memory:` `file::memory:` |
| PostgreSQL | `postgres://`                     |
| MySQL      | `mysql://`                        |
| SQL Server | `sqlserver://` ou `mssql://`      |
| GaussDB    | `gaussdb://` `gauss://`           |
| ClickHouse | `clickhouse://`                   |
*/

func FromURL(dbURL string) (db *gorm.DB, err error) {
	cfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	}
	fmt.Printf("DATABASE: FromURL received %s\n", dbURL)
	dbURL = strings.ToLower(strings.TrimSpace(dbURL))
	if dbURL == "" || dbURL == ":memory:" || strings.HasPrefix(dbURL, "file::memory:") || strings.HasPrefix(dbURL, "sqlite::memory:") || strings.HasPrefix(dbURL, "sqlite://:memory:") {
		return gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), cfg)
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, err
	}
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		if err == nil {
			if sqlDB, e := db.DB(); e == nil {
				// SetMaxIdleConns sets the maximum number of connections in the idle connection pool.
				if m := u.Query().Get("maxIdleConns"); m != "" {
					if v, e := strconv.Atoi(m); e == nil {
						sqlDB.SetMaxIdleConns(v)
					}
				}

				// SetMaxOpenConns sets the maximum number of open connections to the database.
				if m := u.Query().Get("maxOpenConns"); m != "" {
					if v, e := strconv.Atoi(m); e == nil {
						sqlDB.SetMaxOpenConns(v)
					}
				}

				// SetConnMaxLifetime sets the maximum amount of time a connection may be reused.
				if m := u.Query().Get("connMaxLifetime"); m != "" {
					if v, e := strconv.Atoi(m); e == nil {
						sqlDB.SetConnMaxLifetime(time.Duration(v) * time.Second)
					}
				}
			} else {
				db = nil
			}
		}

		// si il y a une erreur, on ferme la connexion
		if err != nil && db != nil {
			if sqlDB, e := db.DB(); e == nil {
				sqlDB.Close()
			}
		}
	}()

	switch u.Scheme {
	case "gaussdb", "gauss":
		//gauss://user:password@localhost:9000/database?read_timeout=10&write_timeout=20"
		//gaussdb://user:password@localhost:9000/database?read_timeout=10&write_timeout=20"
		var p strings.Builder
		p.WriteString("host=")
		p.WriteString(u.Hostname())
		if user := u.User.Username(); user == "" {
			return nil, fmt.Errorf("gaussdb requires a username")
		}
		p.WriteString(" user=")
		p.WriteString(u.User.Username())
		if pass, pwdIsSet := u.User.Password(); pwdIsSet {
			p.WriteString(" password=")
			p.WriteString(pass)
		}
		p.WriteString(" dbname=")
		p.WriteString(strings.TrimPrefix(u.Path, "/"))
		p.WriteString(" port=")
		p.WriteString(u.Port())
		if v := u.Query().Get("sslmode"); v != "" {
			p.WriteString(" sslmode=")
			p.WriteString(v)
		}
		if v := u.Query().Get("timezone"); v != "" {
			p.WriteString(" TimeZone=")
			p.WriteString(v)
		}
		PreferSimpleProtocol := false
		if v := u.Query().Get("preferSimpleProtocol"); v != "" {
			PreferSimpleProtocol = strings.TrimSpace(strings.ToLower(v)) == "true"
		}
		return gorm.Open(gaussdb.New(gaussdb.Config{DSN: p.String(), PreferSimpleProtocol: PreferSimpleProtocol}), cfg)
	case "clickhouse":
		//clickhouse://user:password@localhost:9000/database?read_timeout=10&write_timeout=20"
		p := url.URL{
			Scheme: "tcp",
			Host:   u.Host,
		}

		if user := u.User.Username(); user == "" {
			return nil, fmt.Errorf("clickhouse requires a username")
		} else {
			p.Query().Add("user", user)
		}
		if pass, pwdIsSet := u.User.Password(); pwdIsSet {
			p.Query().Add("password", pass)
		}
		if u.Path != "" {
			p.Query().Add("database", strings.TrimPrefix(u.Path, "/"))
		} else {
			return nil, fmt.Errorf("clickhouse requires a database")
		}
		if v := u.Query().Get("read_timeout"); v != "" {
			p.Query().Add("read_timeout", v)
		}
		if v := u.Query().Get("write_timeout"); v != "" {
			p.Query().Add("write_timeout", v)
		}
		path := strings.TrimPrefix(dbURL, "clickhouse://")
		path = strings.TrimPrefix(path, "/")
		return gorm.Open(clickhouse.Open(path), cfg)
	case "sqlite", "sqlite3", "file":
		path := dbURL
		if strings.HasPrefix(dbURL, "sqlite://") {
			path = dbURL[9:]
		} else if strings.HasPrefix(dbURL, "sqlite:") {
			path = dbURL[7:]
		} else if strings.HasPrefix(dbURL, "file:") {
			path = dbURL[5:]
		}
		if path == "" {
			path = ":memory:"
		}
		return gorm.Open(sqlite.New(sqlite.Config{
			DriverName: "sqlite",
			DSN:        path,
		}), cfg)

	case "postgres", "postgresql":
		// "postgres://user:pass@localhost:5432/mydb?sslmode=disable"
		return gorm.Open(postgres.Open(dbURL), cfg)

	case "mysql":
		// "mysql://user:pass@localhost:3306/mydb?parseTime=true&charset=utf8mb4&loc=Local"
		dsn := mysqlDSNFromURL(u)
		return gorm.Open(mysql.Open(dsn), cfg)

	case "sqlserver", "mssql":
		// "sqlserver://sa:StrongPass@localhost:1433?database=mydb&encrypt=disable"
		dsn := sqlServerDSNFromURL(u)
		return gorm.Open(sqlserver.Open(dsn), cfg)

	default:
		return nil, fmt.Errorf("unsupported database scheme: %s", u.Scheme)
	}
}

func sqlServerDSNFromURL(u *url.URL) string {
	user := u.User.Username()
	pass, _ := u.User.Password()
	host := u.Host

	query := u.Query()
	db := query.Get("database")
	query.Del("database")

	// Valeurs par défaut utiles
	if query.Get("encrypt") == "" {
		query.Set("encrypt", "disable")
	}

	return fmt.Sprintf(
		"sqlserver://%s:%s@%s?database=%s&%s",
		user,
		pass,
		host,
		db,
		query.Encode(),
	)
}

func mysqlDSNFromURL(u *url.URL) string {
	user := u.User.Username()
	pass, _ := u.User.Password()
	host := u.Host
	db := strings.TrimPrefix(u.Path, "/")

	params := u.Query()
	if params.Get("parseTime") == "" {
		params.Set("parseTime", "true")
	}
	if params.Get("charset") == "" {
		params.Set("charset", "utf8mb4")
	}
	if params.Get("loc") == "" {
		params.Set("loc", "Local")
	}

	return fmt.Sprintf(
		"%s:%s@tcp(%s)/%s?%s",
		user,
		pass,
		host,
		db,
		params.Encode(),
	)
}

type Module struct {
}

func (s *Module) Name() string {
	return "db"
}

func (s *Module) Doc() string {
	return "Database module"
}

// ToJSObject - Exposer le model via processor.RegisterGlobal/processor.Register
func (s *Module) ToJSObject(vm *goja.Runtime) goja.Value {
	obj := vm.NewObject()
	s.Loader(nil, vm, obj)
	return obj
}

// Loader - Charger le  via require
func (s *Module) Loader(_ any, vm *goja.Runtime, moduleObject *goja.Object) {
	// CommonJS support: if exports exists, use it as the target
	module := moduleObject
	if exp := moduleObject.Get("exports"); exp != nil && !goja.IsUndefined(exp) {
		module = exp.ToObject(vm)
	}
	// On injecte les méthodes qui délèguent à globalConn si présent
	model := func(name string, schema goja.Value) goja.Value {
		if HasDefaultConnection() {
			globalConn := GetDefaultConnection()
			model := globalConn.Model(name, schema)
			return globalConn.createModelProxy(vm, model)
		}
		vm.Interrupt(vm.NewGoError(fmt.Errorf("no default connection found")))
		return goja.Undefined()
	}
	module.Set("Model", model)
	module.Set("model", model)

	module.Set("Schema", func(paths map[string]interface{}) goja.Value {
		if !HasDefaultConnection() {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("no default connection found")))
			return goja.Undefined()
		}
		conn := GetDefaultConnection()
		schema := conn.createSchemaFromMap(paths)
		schema.vm = vm
		return createSchemaProxy(vm, schema)
	})

	module.Set("connect", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("connect requires a database URL")))
			return goja.Undefined()
		}
		name := DefaultConnName
		if len(call.Arguments) > 1 {
			name = call.Argument(1).String()
		}
		dbURL := call.Argument(0).String()
		db, err := FromURL(dbURL)
		if err != nil {
			vm.Interrupt(vm.NewGoError(err))
			return goja.Undefined()
		}
		return RegisterMongoose(vm, db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Error)}), name)
	})

	module.Set("connection", func(call goja.FunctionCall) goja.Value {
		name := DefaultConnName
		if len(call.Arguments) > 0 {
			name = call.Argument(0).String()
		}
		conn := GetConnection(name)
		if conn == nil {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("connection %s not found", name)))
			return goja.Undefined()
		}
		return conn.ToJSObject(vm)
	})
	module.Set("connectionNames", func(call goja.FunctionCall) goja.Value {
		arr := make([]goja.Value, 0)
		for name := range AllConnections {
			arr = append(arr, vm.ToValue(name))
		}
		return vm.NewArray(arr)
	})
	module.Set("hasConnection", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("hasConnection requires a connection name")))
			return goja.Undefined()
		}
		name := call.Argument(0).String()
		conn := HasConnection(name)
		return vm.ToValue(conn)
	})
	module.DefineAccessorProperty("hasDefault", vm.ToValue(func(call goja.FunctionCall) goja.Value {
		conn := HasDefaultConnection()
		return vm.ToValue(conn)
	}), goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE)

	module.DefineAccessorProperty("default", vm.ToValue(func(call goja.FunctionCall) goja.Value {
		conn := GetDefaultConnection()
		if conn == nil {
			vm.Interrupt(vm.NewGoError(fmt.Errorf("no default connection found")))
			return goja.Undefined()
		}
		return conn.ToJSObject(vm)
	}), goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE)
}
