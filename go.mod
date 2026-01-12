module http-server

go 1.25.5

replace http-server/plugins/surrealdb-embedded => ./plugins/surrealdb-embedded

replace http-server/plugins/require => ./plugins/require

require (
	github.com/PuerkitoBio/goquery v1.11.0
	github.com/cbroglie/mustache v1.4.0
	github.com/dop251/goja v0.0.0-20260106131823-651366fbe6e3
	github.com/dop251/goja_nodejs v0.0.0-20251015164255-5e94316bedaf
	github.com/fatih/color v1.18.0
	github.com/gofiber/fiber/v3 v3.0.0-rc.3
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/google/uuid v1.6.0
	github.com/spf13/pflag v1.0.10
	gorm.io/driver/mysql v1.6.0
	gorm.io/driver/postgres v1.6.0
	gorm.io/driver/sqlite v1.6.0
	gorm.io/driver/sqlserver v1.6.3
	gorm.io/gorm v1.31.1
	http-server/plugins/require v0.0.0-00010101000000-000000000000
	http-server/plugins/surrealdb-embedded v0.0.0-00010101000000-000000000000
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/dlclark/regexp2 v1.11.4 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/go-sql-driver/mysql v1.8.1 // indirect
	github.com/gofiber/schema v1.6.0 // indirect
	github.com/gofiber/utils/v2 v2.0.0-rc.2 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/google/pprof v0.0.0-20240727154555-813a5fbdbec8 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.6.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/klauspost/compress v1.18.1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-sqlite3 v1.14.22 // indirect
	github.com/microsoft/go-mssqldb v1.8.2 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/tinylib/msgp v1.5.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.68.0 // indirect
	golang.org/x/crypto v0.44.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.33.0 // indirect
)
