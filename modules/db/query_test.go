package db

import (
	"testing"

	"github.com/dop251/goja"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	_ "modernc.org/sqlite"
)

func TestQueryOperators(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})

	schema := &Schema{
		Paths: map[string]SchemaType{
			"name": {Type: "string"},
			"age":  {Type: "number"},
			"tags": {Type: "string"}, // comma-sep for test
		},
	}
	model := &Model{
		Name:   "users",
		Schema: schema,
		db:     db,
	}

	// Prepare table
	db.Exec("CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT, age INTEGER, tags TEXT)")
	db.Exec("INSERT INTO users (id, name, age, tags) VALUES ('1', 'Alice', 25, 'dev,admin')")
	db.Exec("INSERT INTO users (id, name, age, tags) VALUES ('2', 'Bob', 30, 'dev')")
	db.Exec("INSERT INTO users (id, name, age, tags) VALUES ('3', 'Charlie', 15, 'user')")

	tests := []struct {
		name     string
		filter   map[string]interface{}
		expected int
	}{
		{"Simple equality", map[string]interface{}{"name": "Alice"}, 1},
		{"$gt operator", map[string]interface{}{"age": map[string]interface{}{"$gt": 20}}, 2},
		{"$lt operator", map[string]interface{}{"age": map[string]interface{}{"$lt": 20}}, 1},
		{"$or operator", map[string]interface{}{
			"$or": []interface{}{
				map[string]interface{}{"name": "Alice"},
				map[string]interface{}{"name": "Bob"},
			},
		}, 2},
		{"$and operator", map[string]interface{}{
			"$and": []interface{}{
				map[string]interface{}{"age": map[string]interface{}{"$gt": 20}},
				map[string]interface{}{"name": "Bob"},
			},
		}, 1},
		{"$mod operator", map[string]interface{}{"age": map[string]interface{}{"$mod": []interface{}{10, 5}}}, 2}, // 25 % 10 == 5, 15 % 10 == 5
		{"$in operator", map[string]interface{}{"name": map[string]interface{}{"$in": []interface{}{"Alice", "Charlie"}}}, 2},
		{"$exists operator (true)", map[string]interface{}{"name": map[string]interface{}{"$exists": true}}, 3},
		{"$exists operator (false)", map[string]interface{}{"name": map[string]interface{}{"$exists": false}}, 0},
		{"$nor operator", map[string]interface{}{
			"$nor": []interface{}{
				map[string]interface{}{"name": "Alice"},
				map[string]interface{}{"name": "Bob"},
			},
		}, 1}, // Only Charlie
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQuery(model, nil)
			q.Filter(tt.filter)

			// Count results
			var count int64
			err := q.db.Count(&count).Error
			if err != nil {
				t.Fatalf("Query failed: %v", err)
			}

			if int(count) != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, int(count))
			}
		})
	}
}

func TestGeospatialOperators(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	schema := &Schema{
		Paths: map[string]SchemaType{
			"name":     {Type: "string"},
			"location": {Type: "array"},
		},
	}
	model := &Model{
		Name:   "places",
		Schema: schema,
		db:     db,
	}

	db.Exec("CREATE TABLE places (id TEXT PRIMARY KEY, name TEXT, location TEXT)")
	db.Exec("INSERT INTO places (id, name, location) VALUES ('1', 'Paris', '[2.35, 48.85]')")
	db.Exec("INSERT INTO places (id, name, location) VALUES ('2', 'London', '[-0.12, 51.50]')")
	db.Exec("INSERT INTO places (id, name, location) VALUES ('3', 'New York', '[-74.00, 40.71]')")

	tests := []struct {
		name     string
		filter   map[string]interface{}
		expected int
	}{
		{"$geoWithin $box (Europe)", map[string]interface{}{
			"location": map[string]interface{}{
				"$geoWithin": map[string]interface{}{
					"$box": []interface{}{
						[]interface{}{-5.0, 40.0},
						[]interface{}{10.0, 60.0},
					},
				},
			},
		}, 2}, // Paris, London
		{"$geoWithin $centerSphere (Near Paris)", map[string]interface{}{
			"location": map[string]interface{}{
				"$geoWithin": map[string]interface{}{
					"$centerSphere": []interface{}{
						[]interface{}{2.0, 48.0},
						2.0,
					},
				},
			},
		}, 1}, // Paris
		{"$nearSphere (Near London)", map[string]interface{}{
			"location": map[string]interface{}{
				"$nearSphere": map[string]interface{}{
					"$geometry": map[string]interface{}{
						"type":        "Point",
						"coordinates": []interface{}{0.0, 51.0},
					},
					"$maxDistance": 1.0,
				},
			},
		}, 1}, // London
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

func TestQueryChaining(t *testing.T) {
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

	// Test Sort, Limit, Skip
	q := NewQuery(model, nil)
	docs, err := q.Sort("-age").Limit(2).Skip(1).Exec()
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(docs) != 2 {
		t.Errorf("Expected 2 docs, got %d", len(docs))
	}
	// Sorted by -age: Bob(30), Alice(25), Charlie(15)
	// Skip 1, Limit 2 -> Alice, Charlie
	if docs[0].Data["name"] != "Alice" || docs[1].Data["name"] != "Charlie" {
		t.Errorf("Unexpected order or data: %v, %v", docs[0].Data["name"], docs[1].Data["name"])
	}

	// Test Select
	q2 := NewQuery(model, nil)
	docs2, _ := q2.Select("name").Exec()
	if _, ok := docs2[0].Data["age"]; ok {
		t.Errorf("Age should have been omitted")
	}
}

func TestJSQuery(t *testing.T) {
	db, _ := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	vm := goja.New()

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

	jsQuery := NewQuery(model, vm).ToJSObject()
	vm.Set("query", jsQuery)

	// Test Chaining and Exec in JS
	script := `
		const results = query.limit(1).exec();
		if (results.length !== 1) throw new Error("Expected 1 result");
		if (results[0].name !== "Alice") throw new Error("Wrong data");
	`
	_, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("JS Exec test failed: %v", err)
	}

	// Test Thenable (Promise-like)
	script2 := `
		let called = false;
		query.then(docs => {
			if (docs.length === 1) called = true;
		});
		if (!called) throw new Error("Then callback not called");
	`
	_, err = vm.RunString(script2)
	if err != nil {
		t.Fatalf("JS Then test failed: %v", err)
	}
}
