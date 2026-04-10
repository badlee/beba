package binder

import (
	"os"
	"testing"
)

func TestErrorContentTypeParsing(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectedCT  string
		expectedTyp HandlerType
	}{
		{
			name: "Standard JSON",
			content: `HTTP :80
    ERROR 500 JSON application/json
        {"error": "internal"}
    END ERROR
END HTTP`,
			expectedCT:  "application/json",
			expectedTyp: "JSON",
		},
		{
			name: "Custom MIME with dots",
			content: `HTTP :80
    ERROR 500 application/vnd.api+json
        {"error": "custom"}
    END ERROR
END HTTP`,
			expectedCT:  "application/vnd.api+json",
			expectedTyp: "", // Default JS
		},
		{
			name: "Custom MIME starting with image",
			content: `HTTP :80
    ERROR 500 image/svg+xml
        <svg></svg>
    END ERROR
END HTTP`,
			expectedCT:  "image/svg+xml",
			expectedTyp: "", // Default JS
		},
		{
			name: "MIME type that might be confused as path",
			content: `HTTP :80
    ERROR 500 model/vnd.collada+xml
        <collada></collada>
    END ERROR
END HTTP`,
			expectedCT:  "model/vnd.collada+xml",
			expectedTyp: "",
		},
		{
			name: "MIME type confused as path with handler type",
			content: `HTTP :80
    ERROR 500 TEXT model/vnd.collada+xml
        <collada></collada>
    END ERROR
END HTTP`,
			expectedCT:  "model/vnd.collada+xml",
			expectedTyp: "TEXT",
		},
		{
			name: "GET with complex MIME",
			content: `HTTP :80
    GET /test model/vnd.collada+xml BEGIN
        print("collada");
    END GET
END HTTP`,
			expectedCT:  "model/vnd.collada+xml",
			expectedTyp: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpfile, err := os.CreateTemp("", "repro.bind")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(tt.content)); err != nil {
				t.Fatal(err)
			}
			tmpfile.Close()

			config, _, err := ParseFile(tmpfile.Name())
			if err != nil {
				t.Fatalf("ParseFile failed: %v", err)
			}

			if len(config.Groups) == 0 || len(config.Groups[0].Items) == 0 {
				t.Fatal("No items parsed")
			}

			proto := config.Groups[0].Items[0]
			var errCfg RouteConfig

			if tt.name == "GET with complex MIME" {
				if len(proto.Routes) == 0 {
					t.Fatal("No routes parsed")
				}
				errCfg = *proto.Routes[0]
			} else {
				e := proto.GetRoutes("ERROR")
				if len(e) == 0 {
					t.Fatal("No errors parsed")
				}
				errCfg = RouteConfig{
					ContentType: e[0].ContentType,
					Type:        e[0].Type,
				}
			}

			if errCfg.ContentType != tt.expectedCT {
				t.Errorf("Expected Content-Type %q, got %q", tt.expectedCT, errCfg.ContentType)
			}
			if string(errCfg.Type) != string(tt.expectedTyp) {
				t.Errorf("Expected Type %q, got %q", tt.expectedTyp, errCfg.Type)
			}
		})
	}
}
