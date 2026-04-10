package db

import (
	"fmt"
	"testing"

	"github.com/dop251/goja"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	_ "modernc.org/sqlite"
)

func TestDocumentPropertySync(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	vm := goja.New()

	schema := &Schema{
		Paths: map[string]SchemaType{
			"name": {Type: "string"},
			"age":  {Type: "number"},
		},
	}
	model := &Model{
		Name:   "User",
		Schema: schema,
		db:     db,
	}
	doc := &Document{
		Data: map[string]interface{}{
			"name": "Alice",
			"age":  25,
		},
		Model: model,
		ID:    "1",
		isNew: false,
	}

	jsDoc := doc.ToJSObject(vm)
	vm.Set("user", jsDoc)

	// Test 1: Property assignment updates Go data
	_, err := vm.RunString(`user.age = 31;`)
	if err != nil {
		t.Fatalf("JS Execution failed: %v", err)
	}

	age := doc.Data["age"]
	// Check if it's 31 regardless of int64/float64
	if fmt.Sprintf("%v", age) != "31" {
		t.Errorf("Go data not updated for 'age'. Found: %v (%T)", age, age)
	}

	// Test 2: set() method still works and is equivalent
	_, err = vm.RunString(`user.set("name", "Bob");`)
	if err != nil {
		t.Fatalf("JS Execution failed: %v", err)
	}

	if doc.Data["name"] != "Bob" {
		t.Errorf("Go data not updated for 'name' via set(). Found: %v", doc.Data["name"])
	}

	// Test 3: Property access reads from Go data
	doc.Data["age"] = 40
	val, _ := vm.RunString(`user.age`)
	if fmt.Sprintf("%v", val.Export()) != "40" {
		t.Errorf("JS property read did not get updated Go data. Found: %v (%T)", val.Export(), val.Export())
	}

	// Test 4: Static save delegation
	conn := &Connection{db: db, vm: vm}
	vm.Set("User", conn.createModelProxy(vm, model))
	_, err = vm.RunString(`User.save(user);`)
	if err != nil {
		t.Fatalf("Static save failed: %v", err)
	}

	// Test 5: Static remove delegation
	_, err = vm.RunString(`User.remove(user);`)
	if err != nil {
		t.Fatalf("Static remove failed: %v", err)
	}
}
