package binder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGroups(t *testing.T) {
	// Create a temporary bind file
	dir := t.TempDir()
	path := filepath.Join(dir, "test_group.bind")
	content := `
DATABASE 'postgres://localhost/db' 
    SCHEMA user DEFINE [strict args=12]
        FIELD name string [required]
        FIELD age number
        VIRTUAL fullName BEGIN
            done(this.firstName);
        END VIRTUAL
    END SCHEMA

    SCHEMA post DEFINE
        FIELD title string
        METHOD publish BEGIN [name]
            done("published");
        END METHOD
    END SCHEMA
END DATABASE
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	config, _, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if len(config.Groups) != 1 {
		t.Fatalf("Expected 1 group (DATABASE), got %d", len(config.Groups))
	}

	dbGroup := config.Groups[0]
	if dbGroup.Directive != "DATABASE" {
		t.Fatalf("Expected group directive DATABASE, got %s", dbGroup.Directive)
	}

	// Implicit protocol inside the DATABASE group should exist
	if len(dbGroup.Items) != 1 {
		t.Fatalf("Expected 1 implicit item inside DATABASE, got %d", len(dbGroup.Items))
	}

	proto := dbGroup.Items[0]
	if proto.Name != "DATABASE" { // the implicit protocol takes the group's name
		t.Fatalf("Expected proto name DATABASE, got %s", proto.Name)
	}

	if len(proto.Routes) != 2 {
		t.Fatalf("Expected 2 sub-routes (SCHEMA user, SCHEMA post), got %d", len(proto.Routes))
	}

	// Check first SCHEMA
	schemaUser := proto.Routes[0]
	if !schemaUser.IsGroup {
		t.Fatal("Expected SCHEMA user to be a group")
	}
	if schemaUser.Method != "SCHEMA" || schemaUser.Path != "user" {
		t.Fatalf("Expected SCHEMA user, got %s %s", schemaUser.Method, schemaUser.Path)
	}
	if !schemaUser.Args.Has("strict") || !isBool(schemaUser.Args.Get("strict")) || schemaUser.Args.Get("strict") != "true" {
		t.Fatalf("Expected SCHEMA strict, got %s", schemaUser.Args.Get("strict"))
	}
	if len(schemaUser.Routes) != 3 {
		t.Fatalf("Expected 3 routes inside SCHEMA user (FIELD, FIELD, VIRTUAL), got %d", len(schemaUser.Routes))
	}

	// Check inside SCHEMA user
	if schemaUser.Routes[0].Method != "FIELD" || schemaUser.Routes[0].Path != "name" || schemaUser.Routes[0].Handler != "string" || !schemaUser.Routes[0].Args.Has("required") {
		t.Fatalf("Expected FIELD name string [required], got %s %s %s. IsGroup=%v Inline=%v", schemaUser.Routes[0].Method, schemaUser.Routes[0].Path, schemaUser.Routes[0].Handler, schemaUser.Routes[0].IsGroup, schemaUser.Routes[0].Inline)
	}

	if schemaUser.Routes[2].Method != "VIRTUAL" || schemaUser.Routes[2].Path != "fullName" {
		t.Fatalf("Expected VIRTUAL fullName, got %s %s", schemaUser.Routes[2].Method, schemaUser.Routes[2].Path)
	}
	if schemaUser.Routes[2].Handler != "done(this.firstName);" {
		t.Fatalf("Expected VIRTUAL handler code 'done(this.firstName);', got '%s'", schemaUser.Routes[2].Handler)
	}

	// Check second SCHEMA
	schemaPost := proto.Routes[1]
	if !schemaPost.IsGroup {
		t.Fatal("Expected SCHEMA post to be a group")
	}
	if schemaPost.Method != "SCHEMA" || schemaPost.Path != "post" {
		t.Fatalf("Expected SCHEMA post, got %s %s", schemaPost.Method, schemaPost.Path)
	}
	if len(schemaPost.Routes) != 2 {
		t.Fatalf("Expected 2 routes inside SCHEMA post (FIELD, METHOD), got %d", len(schemaPost.Routes))
	}

	t.Log("Successfully parsed nested groups!")
}
