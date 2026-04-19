package binder

import (
	"beba/modules/crud"
	"beba/modules/db"
	"testing"
)

func TestParseDatabaseRelationships(t *testing.T) {
	content := `
DATABASE "sqlite://:memory:"
    SCHEMA User DEFINE
        FIELD name string
    END SCHEMA

    SCHEMA Profile DEFINE
        FIELD user_id User.id [has=one delete=cascade update=cascade]
        FIELD bio string
    END SCHEMA

    SCHEMA Order DEFINE
        FIELD customer_id User.id [has=many]
        FIELD amount number
    END SCHEMA

    SCHEMA Role DEFINE
        FIELD users User.id [has=many2many]
        FIELD name string
    END SCHEMA
END DATABASE
`

	config, _, err := ParseConfig(content)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	// Find the DATABASE group
	var dbItems []*DatabaseDirective
	for _, g := range config.Groups {
		if g.Directive == "DATABASE" {
			for _, item := range g.Items {
				if d, err := NewDatabaseDirective(item); err == nil {
					dbItems = append(dbItems, d.(*DatabaseDirective))
				}
			}
		}
	}

	if len(dbItems) != 1 {
		t.Fatalf("Expected 1 DATABASE directive, got %d", len(dbItems))
	}

	db := dbItems[0]
	// Start the directive to trigger parsing
	if _, err := db.Start(); err != nil {
		t.Fatalf("db.Start failed: %v", err)
	}
	defer db.Close()

	if len(db.Schemas) != 4 {
		t.Fatalf("Expected 4 parsed schemas, got %d", len(db.Schemas))
	}

	// Verify Profile relationships in model
	profile := db.Schemas["Profile"]
	userIdField := profile["user_id"]
	if userIdField.Ref != "User.id" {
		t.Errorf("Expected user_id.Ref 'User.id', got '%s'", userIdField.Ref)
	}
	if userIdField.Has != "one" {
		t.Errorf("Expected user_id.Has 'one', got '%s'", userIdField.Has)
	}
	if userIdField.OnDelete != "CASCADE" {
		t.Errorf("Expected user_id.OnDelete 'CASCADE', got '%s'", userIdField.OnDelete)
	}

	// Verify Role (many2many) in model
	role := db.Schemas["Role"]
	usersField := role["users"]
	if usersField.Has != "many2many" {
		t.Errorf("Expected role.users.Has 'many2many', got '%s'", usersField.Has)
	}

	// Verify Order (many) in model
	order := db.Schemas["Order"]
	customerIdField := order["customer_id"]
	if customerIdField.Has != "many" {
		t.Errorf("Expected order.customer_id.Has 'many', got '%s'", customerIdField.Has)
	}
}

func TestParseCrudDatabaseRelationships(t *testing.T) {
	content := `
DATABASE "sqlite://:memory:"
    SCHEMA Users DEFINE
        FIELD email string [unique]
    END SCHEMA

    SCHEMA Profiles DEFINE
        FIELD user_id Users.id [has=one delete=cascade]
        FIELD bio text
    END SCHEMA
END DATABASE
`
	config, _, err := ParseConfig(content)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	dbItem := config.Groups[0].Items[0]
	directive, err := NewDatabaseDirective(dbItem)
	if err != nil {
		t.Fatalf("NewDatabaseDirective failed: %v", err)
	}

	db := directive.(*DatabaseDirective)
	if db.crud == nil {
		t.Fatal("Expected CRUD setup, got nil")
	}

	// Verify the parsed CRUD schemas in config
	schemas := db.crud.Inner.Config().Schemas
	if len(schemas) != 2 {
		t.Fatalf("Expected 2 CRUD schemas, got %d", len(schemas))
	}

	var profilesSchema *crud.DSLCrudSchema
	for i := range schemas {
		if schemas[i].Name == "Profiles" {
			profilesSchema = &schemas[i]
		}
	}

	if profilesSchema == nil {
		t.Fatal("Profiles schema not found in CRUD config")
	}

	// Find user_id field
	var userIdField *crud.DSLField
	for i := range profilesSchema.Fields {
		if profilesSchema.Fields[i].Name == "user_id" {
			userIdField = &profilesSchema.Fields[i]
		}
	}

	if userIdField == nil {
		t.Fatal("user_id field not found in Profiles CRUD schema")
	}

	if userIdField.Ref != "Users.id" {
		t.Errorf("Expected DSLField.Ref 'Users.id', got '%s'", userIdField.Ref)
	}
	if userIdField.Has != "one" {
		t.Errorf("Expected DSLField.Has 'one', got '%s'", userIdField.Has)
	}
	if userIdField.OnDelete != "CASCADE" {
		t.Errorf("Expected DSLField.OnDelete 'CASCADE', got '%s'", userIdField.OnDelete)
	}
}

func TestParseSchemaBlockRelationships(t *testing.T) {
	// Mock a RouteConfig for a SCHEMA block
	r := &RouteConfig{
		Method: "SCHEMA",
		Path:   "posts",
		Routes: []*RouteConfig{
			{
				Method:  "FIELD",
				Path:    "author_id",
				Handler: "User.id",
				Args: map[string]string{
					"has":    "one",
					"delete": "cascade",
				},
			},
		},
	}

	s := parseSchemaBlock(r)

	if s.Slug != "posts" {
		t.Errorf("Expected slug 'posts', got '%s'", s.Slug)
	}
	if len(s.Fields) != 1 {
		t.Fatalf("Expected 1 field, got %d", len(s.Fields))
	}

	f := s.Fields[0]
	if f.Name != "author_id" {
		t.Errorf("Expected field name 'author_id', got '%s'", f.Name)
	}
	if f.Ref != "User.id" {
		t.Errorf("Expected ref 'User.id', got '%s'", f.Ref)
	}
	if f.OnDelete != "CASCADE" {
		t.Errorf("Expected delete 'CASCADE', got '%s'", f.OnDelete)
	}
}

func TestParseAliasRelationships(t *testing.T) {
	content := `
DATABASE "sqlite://:memory:"
    SCHEMA User DEFINE
        FIELD roles Role.id [has=many_to_many]
        FIELD profile Profile.id [has=one_to_one]
        FIELD posts Post.id [has=one_to_many]
    END SCHEMA
END DATABASE
`
	config, _, err := ParseConfig(content)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	dbItem := config.Groups[0].Items[0]
	directive, _ := NewDatabaseDirective(dbItem)
	db := directive.(*DatabaseDirective)
	
	// Start to trigger model parsing
	if _, err := db.Start(); err != nil {
		t.Fatalf("db.Start failed: %v", err)
	}
	defer db.Close()

	userSchema := db.Schemas["User"]
	
	if userSchema["roles"].Has != "many2many" {
		t.Errorf("Expected roles.Has mapped to 'many2many', got '%s'", userSchema["roles"].Has)
	}
	if userSchema["profile"].Has != "one" {
		t.Errorf("Expected profile.Has mapped to 'one', got '%s'", userSchema["profile"].Has)
	}
	if userSchema["posts"].Has != "many" {
		t.Errorf("Expected posts.Has mapped to 'many', got '%s'", userSchema["posts"].Has)
	}
	
	// Also check Uppercase constraints default
	if userSchema["roles"].OnDelete != "SET NULL" {
		t.Errorf("Expected default OnDelete 'SET NULL', got '%s'", userSchema["roles"].OnDelete)
	}
}

func TestDatabaseDefaultKeyword(t *testing.T) {
	// Manually set a default connection in memory to avoid touching the disk
	import_db := true
	_ = import_db

	content := `
DATABASE ":default:"
    SCHEMA Item DEFINE
        FIELD name string
    END SCHEMA
END DATABASE

DATABASE "sqlite://:default:"
    SCHEMA Person DEFINE
        FIELD age number
    END SCHEMA
END DATABASE
`
	config, _, err := ParseConfig(content)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	var dbItems []*DatabaseDirective
	for _, g := range config.Groups {
		if g.Directive == "DATABASE" {
			for _, item := range g.Items {
				if d, err := NewDatabaseDirective(item); err == nil {
					dbItems = append(dbItems, d.(*DatabaseDirective))
				}
			}
		}
	}

	if len(dbItems) != 2 {
		t.Fatalf("Expected 2 DATABASE directives, got %d", len(dbItems))
	}

	for _, dbDir := range dbItems {
		if _, err := dbDir.Start(); err != nil {
			t.Fatalf("dbDir.Start failed: %v", err)
		}
		defer dbDir.Close()
	}

	// Verify the items were added correctly. They should be applying to the default connection.
	if _, ok := dbItems[0].Schemas["Item"]; !ok {
		t.Errorf("Expected Item schema to be defined in the first directive")
	}

	if _, ok := dbItems[1].Schemas["Person"]; !ok {
		t.Errorf("Expected Person schema to be defined in the second directive")
	}
}

func TestDatabaseDefaultOverride(t *testing.T) {
	// 1. Setup an initial default connection
	initialDB, _ := db.FromURL("sqlite://:memory:")
	initialConn := db.NewConnection(initialDB, "DEFAULT")
	db.RegisterDefaultConnection(initialConn)

	if db.GetDefaultConnection() != initialConn {
		t.Fatal("Initial default connection registration failed")
	}

	// 2. Define a new DATABASE directive that says default=true
	content := `
DATABASE "sqlite://:memory:" default=true
    NAME override_db
    SCHEMA NewSchema DEFINE
        FIELD name string
    END SCHEMA
END DATABASE
`
	config, _, err := ParseConfig(content)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	item := config.Groups[0].Items[0]
	directive, err := NewDatabaseDirective(item)
	if err != nil {
		t.Fatalf("NewDatabaseDirective failed: %v", err)
	}

	d := directive.(*DatabaseDirective)
	if _, err := d.Start(); err != nil {
		t.Fatalf("Directive Start failed: %v", err)
	}
	defer d.Close()

	// 3. Verify the default connection has been replaced
	currentDefault := db.GetDefaultConnection()
	if currentDefault == initialConn {
		t.Error("Expected default connection to be overridden, but it is still the initial one")
	}

	if currentDefault.Name() != "DEFAULT" {
		t.Errorf("Expected new default connection name to be 'DEFAULT', got %q", currentDefault.Name())
	}
	
	// Verify it still has the NAME argument as an alias
	if db.GetConnection("OVERRIDE_DB") != currentDefault {
		t.Error("Expected connection to also be registered under its provided name 'OVERRIDE_DB'")
	}
}
