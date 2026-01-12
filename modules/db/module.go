package db

import (
	"fmt"
	"http-server/modules"
	"net/url"
	"strings"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/driver/sqlserver"
)

/**
* Schémas maintenant supportés
| DB         | Scheme                       |
| ---------- | ---------------------------- |
| SQLite     | `sqlite:///file.db`          |
| PostgreSQL | `postgres://`                |
| MySQL      | `mysql://`                   |
| SQL Server | `sqlserver://` ou `mssql://` |
*/

func fromURL(dbURL string) (*gorm.DB, error) {
	cfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	}
	dbURL = strings.ToLower(strings.TrimSpace(dbURL))
	if dbURL == ":memory:" {
		return gorm.Open(sqlite.Open(":memory:"), cfg)
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, err
	}

	switch u.Scheme {

	case "sqlite", "sqlite3", "file":
		// "sqlite:///migration_enabled.db"
		path := strings.TrimPrefix(dbURL, "sqlite:///")
		return gorm.Open(sqlite.Open(path), cfg)

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

type Module struct{}

func init() {
	modules.RegisterModule(&Module{})

}

func (s *Module) Name() string {
	return "db"
}

func (s *Module) Doc() string {
	return "Database module"
}

func (s *Module) Loader(ctx fiber.Ctx, vm *goja.Runtime, module *goja.Object) {
	// Expose cookies
	collections := module.Get("exports").(*goja.Object)
	collections.Set("connect", func(call goja.FunctionCall) goja.Value {
		dbURL := call.Argument(0).String()
		db, err := fromURL(dbURL)
		if err != nil {
			vm.Interrupt(err)
			return goja.Undefined()
		}
		// Créer le gestionnaire de migrations
		gormDB := RegisterMongoose(vm, db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Error)}))
		return gormDB
	})

	module.Set("exports", collections)
}
