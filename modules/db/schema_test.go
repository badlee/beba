package db

import (
	"http-server/processor"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	_ "modernc.org/sqlite"
)

func TestExecOne(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	schema := &Schema{
		Paths: map[string]SchemaType{
			"name": {Type: "string"},
			"age":  {Type: "number"},
		},
	}
	model := &Model{
		Name:   "users",
		Schema: schema,
		db:     db,
	}

	db.Exec("CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT, age INTEGER)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('1', 'Alice', 25)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('2', 'Bob', 30)")

	q := NewQuery(model, nil)
	q.Filter(map[string]interface{}{"name": "Alice"})
	doc, err := q.ExecOne()
	if err != nil {
		t.Fatalf("ExecOne failed: %v", err)
	}
	if doc.Data["name"] != "Alice" {
		t.Errorf("Expected Alice, got %v", doc.Data["name"])
	}
	if doc.ID != "1" {
		t.Errorf("Expected ID '1', got %q", doc.ID)
	}
}

func TestExecOneNotFound(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	schema := &Schema{
		Paths: map[string]SchemaType{
			"name": {Type: "string"},
		},
	}
	model := &Model{
		Name:   "users",
		Schema: schema,
		db:     db,
	}
	db.Exec("CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT)")

	q := NewQuery(model, nil)
	q.Filter(map[string]interface{}{"name": "Missing"})
	_, err := q.ExecOne()
	if err == nil {
		t.Fatal("Expected error for no rows found")
	}
}

func TestQueryAdditionalOperators(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	schema := &Schema{
		Paths: map[string]SchemaType{
			"name": {Type: "string"},
			"age":  {Type: "number"},
		},
	}
	model := &Model{
		Name:   "users",
		Schema: schema,
		db:     db,
	}

	db.Exec("CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT, age INTEGER)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('1', 'Alice', 25)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('2', 'Bob', 30)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('3', 'Charlie', 15)")

	tests := []struct {
		name     string
		filter   map[string]interface{}
		expected int
	}{
		{"$eq operator", map[string]interface{}{"name": map[string]interface{}{"$eq": "Bob"}}, 1},
		{"$gte operator", map[string]interface{}{"age": map[string]interface{}{"$gte": 25}}, 2},
		{"$lte operator", map[string]interface{}{"age": map[string]interface{}{"$lte": 25}}, 2},
		{"$ne operator", map[string]interface{}{"name": map[string]interface{}{"$ne": "Alice"}}, 2},
		{"$nin operator", map[string]interface{}{"name": map[string]interface{}{"$nin": []interface{}{"Alice", "Bob"}}}, 1},
		{"$not operator", map[string]interface{}{
			"$not": map[string]interface{}{"name": "Alice"},
		}, 2},
		{"$where operator", map[string]interface{}{
			"$where": "age > 20",
		}, 2},
		{"$comment ignored", map[string]interface{}{
			"$comment": "this is a test query",
			"name":     "Alice",
		}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQuery(model, nil)
			q.Filter(tt.filter)
			var count int64
			q.db.Count(&count)
			if int(count) != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, int(count))
			}
		})
	}
}

func TestQuerySelectOmit(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	schema := &Schema{
		Paths: map[string]SchemaType{
			"name": {Type: "string"},
			"age":  {Type: "number"},
		},
	}
	model := &Model{
		Name:   "users",
		Schema: schema,
		db:     db,
	}

	db.Exec("CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT, age INTEGER, created_at DATETIME, updated_at DATETIME)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('1', 'Alice', 25)")

	// Test positive select (only name)
	q := NewQuery(model, nil)
	docs, err := q.Select("name").Exec()
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("Expected 1 doc, got %d", len(docs))
	}
	if docs[0].Data["name"] != "Alice" {
		t.Errorf("Expected name 'Alice', got %v", docs[0].Data["name"])
	}
	// age should be filtered out by select
	if _, ok := docs[0].Data["age"]; ok {
		t.Error("age should have been excluded by positive select")
	}
}

func TestJSQueryFindOne(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	vm := processor.NewEmpty()
	vm.AttachGlobals()

	schema := &Schema{
		Paths: map[string]SchemaType{
			"name": {Type: "string"},
			"age":  {Type: "number"},
		},
	}
	model := &Model{
		Name:   "users",
		Schema: schema,
		db:     db,
	}

	db.Exec("CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT, age INTEGER)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('1', 'Alice', 25)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('2', 'Bob', 30)")

	// Test returnFirstOnly mode
	jsQuery := NewQuery(model, vm.Runtime).ToJSObject(true)
	vm.Set("query", jsQuery)

	_, err := vm.RunString(`
		const result = query.exec();
		if (result === null || result === undefined) throw new Error("Expected single doc");
		if (result.name !== "Alice") throw new Error("Wrong name: " + result.name);
	`)
	if err != nil {
		t.Fatalf("JS FindOne test failed: %v", err)
	}
}

func TestSortAscending(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	schema := &Schema{
		Paths: map[string]SchemaType{
			"name": {Type: "string"},
			"age":  {Type: "number"},
		},
	}
	model := &Model{
		Name:   "users",
		Schema: schema,
		db:     db,
	}

	db.Exec("CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT, age INTEGER)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('1', 'Charlie', 15)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('2', 'Alice', 25)")
	db.Exec("INSERT INTO users (id, name, age) VALUES ('3', 'Bob', 30)")

	q := NewQuery(model, nil)
	docs, err := q.Sort("age").Exec()
	if err != nil {
		t.Fatalf("Sort ascending failed: %v", err)
	}
	if docs[0].Data["name"] != "Charlie" {
		t.Errorf("Expected Charlie first (youngest), got %v", docs[0].Data["name"])
	}
	if docs[2].Data["name"] != "Bob" {
		t.Errorf("Expected Bob last (oldest), got %v", docs[2].Data["name"])
	}
}
