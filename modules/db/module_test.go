package db

import (
	"http-server/processor"
	"net/url"
	"strings"
	"testing"

	"github.com/dop251/goja"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	_ "modernc.org/sqlite"
)

// --------------------------------------------------------------------------
// FromURL
// --------------------------------------------------------------------------

func TestFromURL_MemoryDefault(t *testing.T) {
	db, err := FromURL(":memory:")
	if err != nil {
		t.Fatalf("FromURL :memory: failed: %v", err)
	}
	if db == nil {
		t.Fatal("Expected non-nil db")
	}
}

func TestFromURL_Empty(t *testing.T) {
	db, err := FromURL("")
	if err != nil {
		t.Fatalf("FromURL empty failed: %v", err)
	}
	if db == nil {
		t.Fatal("Expected non-nil db")
	}
}

func TestFromURL_SQLiteMemory(t *testing.T) {
	db, err := FromURL("sqlite::memory:")
	if err != nil {
		t.Fatalf("FromURL sqlite::memory: failed: %v", err)
	}
	if db == nil {
		t.Fatal("Expected non-nil db")
	}
}

func TestFromURL_FileScheme(t *testing.T) {
	db, err := FromURL("file::memory:")
	if err != nil {
		t.Fatalf("FromURL file::memory: failed: %v", err)
	}
	if db == nil {
		t.Fatal("Expected non-nil db")
	}
}

func TestFromURL_SQLitePath(t *testing.T) {
	dir := t.TempDir()
	db, err := FromURL("sqlite://" + dir + "/test.db")
	if err != nil {
		t.Fatalf("FromURL sqlite path failed: %v", err)
	}
	if db == nil {
		t.Fatal("Expected non-nil db")
	}
}

func TestFromURL_UnsupportedScheme(t *testing.T) {
	_, err := FromURL("ftp://localhost/db")
	if err == nil {
		t.Fatal("Expected error for unsupported scheme")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("Expected 'unsupported' in error, got %q", err.Error())
	}
}

// --------------------------------------------------------------------------
// mysqlDSNFromURL / sqlServerDSNFromURL
// --------------------------------------------------------------------------

func TestMysqlDSNFromURL(t *testing.T) {
	// We just test the helper builds a valid-looking DSN
	// Can't actually connect to MySQL in unit tests
	dsn := mysqlDSNFromURL(mustParseURL("mysql://user:pass@localhost:3306/mydb"))
	if !strings.Contains(dsn, "user:pass@tcp(localhost:3306)/mydb") {
		t.Errorf("Unexpected MySQL DSN: %q", dsn)
	}
	if !strings.Contains(dsn, "parseTime=true") {
		t.Errorf("Expected parseTime=true default in DSN: %q", dsn)
	}
	if !strings.Contains(dsn, "charset=utf8mb4") {
		t.Errorf("Expected charset=utf8mb4 default in DSN: %q", dsn)
	}
}

func TestSqlServerDSNFromURL(t *testing.T) {
	dsn := sqlServerDSNFromURL(mustParseURL("sqlserver://sa:StrongPass@localhost:1433?database=mydb"))
	if !strings.Contains(dsn, "sa:StrongPass@localhost:1433") {
		t.Errorf("Unexpected SQL Server DSN: %q", dsn)
	}
	if !strings.Contains(dsn, "database=mydb") {
		t.Errorf("Expected database=mydb in DSN: %q", dsn)
	}
	if !strings.Contains(dsn, "encrypt=disable") {
		t.Errorf("Expected encrypt=disable default in DSN: %q", dsn)
	}
}

// --------------------------------------------------------------------------
// Module interface
// --------------------------------------------------------------------------

func TestDBModuleNameAndDoc(t *testing.T) {
	mod := &Module{}
	if mod.Name() != "db" {
		t.Errorf("Expected name 'db', got %q", mod.Name())
	}
	if mod.Doc() == "" {
		t.Error("Expected non-empty doc")
	}
}

func TestDBModuleToJSObject(t *testing.T) {
	// Setup a default connection for the module to use
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	conn := NewConnection(db)
	defer conn.Close()

	mod := &Module{}
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	obj := mod.ToJSObject(vm.Runtime)
	if obj == nil || goja.IsUndefined(obj) {
		t.Fatal("Expected non-nil JS object")
	}
}

func TestDBModuleLoader_ConnectMemory(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	moduleObj := vm.NewObject()
	exports := vm.NewObject()
	moduleObj.Set("exports", exports)
	mod.Loader(nil, vm.Runtime, moduleObj)

	// connect should be a function on exports
	val := exports.Get("connect")
	if val == nil || goja.IsUndefined(val) {
		t.Fatal("Expected 'connect' function on exports")
	}
}

func TestDBModuleLoader_HasConnection(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	conn := NewConnection(db, "test-has")
	defer conn.Close()

	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	moduleObj := vm.NewObject()
	exports := vm.NewObject()
	moduleObj.Set("exports", exports)
	mod.Loader(nil, vm.Runtime, moduleObj)

	vm.Set("db", exports)
	val, err := vm.RunString(`db.hasConnection("test-has")`)
	if err != nil {
		t.Fatalf("hasConnection failed: %v", err)
	}
	if !val.ToBoolean() {
		t.Error("Expected hasConnection to return true")
	}
}

func TestDBModuleLoader_ConnectionNames(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	conn := NewConnection(db, "names-test")
	defer conn.Close()

	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	moduleObj := vm.NewObject()
	exports := vm.NewObject()
	moduleObj.Set("exports", exports)
	mod.Loader(nil, vm.Runtime, moduleObj)

	vm.Set("db", exports)
	val, err := vm.RunString(`db.connectionNames().length`)
	if err != nil {
		t.Fatalf("connectionNames failed: %v", err)
	}
	if val.ToInteger() < 1 {
		t.Error("Expected at least 1 connection name")
	}
}

func TestDBModule_Model_CRUD(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	conn := NewConnection(db)
	defer conn.Close()

	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	moduleObj := vm.NewObject()
	exports := vm.NewObject()
	moduleObj.Set("exports", exports)
	mod.Loader(nil, vm.Runtime, moduleObj)
	vm.Set("db", exports)

	_, err := vm.RunString(`
		const User = db.Model("users", {
			name: 'string',
			age: 'number'
		});

		// Create
		User.create({ name: "Alice", age: 30 });

		// Find
		const user = User.findOne({ name: "Alice" }).exec();
		if (user.age !== 30) throw new Error("Expected age 30, got " + user.age);
	`)
	if err != nil {
		t.Fatalf("JS Execution failed: %v", err)
	}
}

// --------------------------------------------------------------------------
// helpers
// --------------------------------------------------------------------------

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
