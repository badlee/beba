package db

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	_ "modernc.org/sqlite"
)

func TestNewConnection(t *testing.T) {
	db, err := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}

	conn := NewConnection(db, "test-conn")
	defer conn.Close()

	if conn.Name() != "TEST-CONN" {
		t.Errorf("Expected name 'TEST-CONN', got %q", conn.Name())
	}
	if conn.GetDB() == nil {
		t.Error("Expected non-nil DB")
	}
	if conn.GetModels() == nil {
		t.Error("Expected non-nil models map")
	}
}

func TestGetConnection(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})

	conn := NewConnection(db, "lookup-test")
	defer conn.Close()

	found := GetConnection("lookup-test")
	if found == nil {
		t.Fatal("Expected to find registered connection")
	}
	if found.Name() != "LOOKUP-TEST" {
		t.Errorf("Expected 'LOOKUP-TEST', got %q", found.Name())
	}
}

func TestHasConnection(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})

	_ = NewConnection(db, "exists-test")

	if !HasConnection("exists-test") {
		t.Error("Expected HasConnection to return true")
	}
	if HasConnection("nonexistent") {
		t.Error("Expected HasConnection to return false for missing connection")
	}
}

func TestRegisterConnection(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})

	conn := NewConnection(db, "original")
	defer conn.Close()

	RegisterConnection(conn, "renamed")
	if conn.Name() != "RENAMED" {
		t.Errorf("Expected name to be updated to 'RENAMED', got %q", conn.Name())
	}

	found := GetConnection("renamed")
	if found == nil {
		t.Fatal("Expected connection under new name")
	}
}

func TestDefaultConnection(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})

	conn := NewConnection(db) // default name
	defer conn.Close()

	RegisterDefaultConnection(conn)

	if !HasDefaultConnection() {
		t.Error("Expected default connection to exist")
	}

	def := GetDefaultConnection()
	if def == nil {
		t.Fatal("Expected non-nil default connection")
	}

	name := GetDefaultlDatabaseName()
	if name != DefaultConnName {
		t.Errorf("Expected %q, got %q", DefaultConnName, name)
	}
}

func TestConnectionClose(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})

	conn := NewConnection(db, "close-test")
	err := conn.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// After close, DB and name should be nil/empty
	if conn.GetDB() != nil {
		t.Error("Expected nil DB after close")
	}
	if conn.Name() != "" {
		t.Errorf("Expected empty name after close, got %q", conn.Name())
	}
}

func TestSchemaItemInterface(t *testing.T) {
	item := NewSchemaItem("METHOD", "greet", "return 'hello'", "name")

	if item.GetName() != "greet" {
		t.Errorf("Expected name 'greet', got %q", item.GetName())
	}
	if item.GetItemType() != "METHOD" {
		t.Errorf("Expected type 'METHOD', got %q", item.GetItemType())
	}
	code := item.GetCode()
	if code == "" {
		t.Error("Expected non-empty code")
	}
}

func TestModelAddMethod(t *testing.T) {
	schema := &Schema{
		Paths:      map[string]SchemaType{},
		Methods:    map[string]string{},
		Statics:    map[string]string{},
		Virtuals:   map[string]VirtualDef{},
		Middleware: map[string][]MiddlewareDef{},
	}
	model := &Model{Name: "test", Schema: schema}

	item := NewSchemaItem("METHOD", "greet", "return 'hi'")
	model.AddMethod(item)
	if _, ok := schema.Methods["greet"]; !ok {
		t.Error("Expected method 'greet' registered")
	}
}

func TestModelAddVirtual(t *testing.T) {
	schema := &Schema{
		Paths:      map[string]SchemaType{},
		Methods:    map[string]string{},
		Statics:    map[string]string{},
		Virtuals:   map[string]VirtualDef{},
		Middleware: map[string][]MiddlewareDef{},
	}
	model := &Model{Name: "test", Schema: schema}

	item := NewSchemaItem("VIRTUAL", "fullName", "return this.first + ' ' + this.last")
	model.AddVirtual(item)
	if v, ok := schema.Virtuals["fullName"]; !ok {
		t.Error("Expected virtual 'fullName' registered")
	} else if v.Get == "" {
		t.Error("Expected non-empty getter code")
	}
}

func TestModelAddStatic(t *testing.T) {
	schema := &Schema{
		Paths:      map[string]SchemaType{},
		Methods:    map[string]string{},
		Statics:    map[string]string{},
		Virtuals:   map[string]VirtualDef{},
		Middleware: map[string][]MiddlewareDef{},
	}
	model := &Model{Name: "test", Schema: schema}

	item := NewSchemaItem("STATIC", "findByEmail", "return this.findOne({email: email})", "email")
	model.AddStatic(item)
	if _, ok := schema.Statics["findByEmail"]; !ok {
		t.Error("Expected static 'findByEmail' registered")
	}
}

func TestModelAddMiddleware(t *testing.T) {
	schema := &Schema{
		Paths:      map[string]SchemaType{},
		Methods:    map[string]string{},
		Statics:    map[string]string{},
		Virtuals:   map[string]VirtualDef{},
		Middleware: map[string][]MiddlewareDef{},
	}
	model := &Model{Name: "test", Schema: schema}

	item := NewSchemaItem("PRE", "save", "console.log('pre-save')")
	model.AddMiddleware(true, item)
	if mw, ok := schema.Middleware["save"]; !ok {
		t.Error("Expected middleware 'save' registered")
	} else if len(mw) != 1 || mw[0].Type != "pre" {
		t.Errorf("Expected 1 pre middleware, got %v", mw)
	}

	item2 := NewSchemaItem("POST", "save", "console.log('post-save')")
	model.AddMiddleware(false, item2)
	if mw := schema.Middleware["save"]; len(mw) != 2 {
		t.Errorf("Expected 2 middlewares, got %d", len(mw))
	} else if mw[1].Type != "post" {
		t.Errorf("Expected second middleware to be 'post', got %q", mw[1].Type)
	}
}
