package binder

import (
	"os"
	"testing"
)

func TestParseFile(t *testing.T) {
	content := `
HTTP 0.0.0.0:8080
    AUTH USER "admin" "password"
    GET @CORS @ADMIN[message=` + "`" + `Je suis un super message` + "`" + ` alert="cool \"message\"" name='m\'ba ntem'] / BEGIN
        print("Hello World");
    END GET
END HTTP

DTP 0.0.0.0:12345
    AUTH CSV "devices.csv"
    DATA "SENSOR_DATA" BEGIN
        print("Data received: " + payload);
    END DATA
    PING BEGIN
        print("Ping received");
    END PING
    ONLINE BEGIN
        print("Device online");
    END ONLINE
END DTP
`
	tmpfile, err := os.CreateTemp("", "test.bind")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	config, _, err := ParseFile(tmpfile.Name())
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(config.Groups) != 2 {
		t.Errorf("Expected 2 groups, got %d", len(config.Groups))
	}

	// Group 0: HTTP
	httpGroup := config.Groups[0]
	if httpGroup.Directive != "HTTP" {
		t.Errorf("Expected group 0 to be HTTP, got %s", httpGroup.Directive)
	}
	if len(httpGroup.Items) != 1 {
		t.Errorf("Expected 1 item in HTTP group, got %d", len(httpGroup.Items))
	}
	httpProto := httpGroup.Items[0]
	if len(httpProto.Auth) != 1 || httpProto.Auth[0].User != "admin" {
		t.Error("HTTP Auth parsing failed")
	}
	if len(httpProto.Routes) != 1 || httpProto.Routes[0].Method != "GET" || httpProto.Routes[0].Path != "/" {
		t.Error("HTTP Route parsing failed")
	}
	route := httpProto.Routes[0]
	if len(route.Middlewares) != 2 {
		t.Errorf("Expected 2 middlewares, got %d", len(route.Middlewares))
	} else {
		if route.Middlewares[0].Name != "CORS" {
			t.Errorf("Expected first middleware to be CORS, got %s", route.Middlewares[0].Name)
		}
		if route.Middlewares[1].Name != "ADMIN" {
			t.Errorf("Expected second middleware to be ADMIN, got %s", route.Middlewares[1].Name)
		} else {
			m := route.Middlewares[1]
			if m.Args["message"] != "Je suis un super message" {
				t.Errorf("Expected message to be 'Je suis un super message', got '%s'", m.Args["message"])
			}
			if m.Args["alert"] != `cool "message"` {
				t.Errorf("Expected alert to be 'cool \"message\"', got '%s'", m.Args["alert"])
			}
			if m.Args["name"] != "m'ba ntem" {
				t.Errorf("Expected name to be \"m'ba ntem\", got '%s'", m.Args["name"])
			}
		}
	}

	// Group 1: DTP
	dtpGroup := config.Groups[1]
	if dtpGroup.Directive != "DTP" {
		t.Errorf("Expected group 1 to be DTP, got %s", dtpGroup.Directive)
	}
	dtpProto := dtpGroup.Items[0]
	if len(dtpProto.Auth) != 1 || dtpProto.Auth[0].Type != AuthCSV {
		t.Error("DTP Auth parsing failed")
	}

	// DTP Routes
	foundData := false
	foundPing := false
	foundOnline := false
	for _, r := range dtpProto.Routes {
		switch r.Method {
		case "DATA":
			if r.Path == "SENSOR_DATA" {
				foundData = true
			}
		case "PING":
			foundPing = true
		case "ONLINE":
			foundOnline = true
		}
	}

	if !foundData {
		t.Error("DTP DATA route not found")
	}
	if !foundPing {
		t.Error("DTP PING route not found")
	}
	if !foundOnline {
		t.Error("DTP ONLINE route not found")
	}
}

func TestParseRouteMimeAndValue(t *testing.T) {
	tests := []struct {
		name                string
		content             string
		expectedPath        string
		expectedHandler     string
		expectedContentType string
	}{
		{
			name: "DIRECTIVE text",
			content: `
HTTP 0.0.0.0:8080
    DIRECTIVE text
END HTTP
`,
			expectedPath:        "text",
			expectedHandler:     "",
			expectedContentType: "",
		},
		{
			name: "DIRECTIVE text value",
			content: `
HTTP 0.0.0.0:8080
    DIRECTIVE text value
END HTTP
`,
			expectedPath:        "text",
			expectedHandler:     "value",
			expectedContentType: "",
		},
		{
			name: "DIRECTIVE text text/plain value",
			content: `
HTTP 0.0.0.0:8080
    DIRECTIVE text text/plain value
END HTTP
`,
			expectedPath:        "text",
			expectedHandler:     "value",
			expectedContentType: "text/plain",
		},
		{
			name: "DIRECTIVE text text/plain value with spaces",
			content: `
HTTP 0.0.0.0:8080
    DIRECTIVE text text/plain value with spaces
END HTTP
`,
			expectedPath:        "text",
			expectedHandler:     "value with spaces",
			expectedContentType: "text/plain",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			route, _, err := ParseRouteFromString(tc.content)
			if err != nil {
				t.Fatalf("ParseRouteFromString failed: %v", err)
			}
			if route.Path != tc.expectedPath {
				t.Errorf("Expected Path %q, got %q", tc.expectedPath, route.Path)
			}
			if route.Handler != tc.expectedHandler {
				t.Errorf("Expected Handler %q, got %q", tc.expectedHandler, route.Handler)
			}
			if route.ContentType != tc.expectedContentType {
				t.Errorf("Expected ContentType %q, got %q", tc.expectedContentType, route.ContentType)
			}
		})
	}
}
