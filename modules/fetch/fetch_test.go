package fetch

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestFetchHTTP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		w.Header().Set("X-Custom", "test-val")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	vm := goja.New()
	mod := &Module{client: ts.Client()}
	mod.Loader(nil, vm, vm.NewObject()) // Load injects globally

	_, err := vm.RunString(`
		var result = null;
		var error = null;
		fetch("` + ts.URL + `/api", { method: "POST", body: "data" })
			.then(resp => {
				if (!resp.ok) throw new Error("not ok");
				if (resp.headers["X-Custom"] !== "test-val") throw new Error("bad header");
				return resp.json();
			})
			.then(data => {
				result = data.success;
			})
			.catch(err => {
				error = err.message;
			});
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}

	result := vm.Get("result")
	if result == nil || result.Export() != true {
		errVal := vm.Get("error")
		errMsg := "unknown"
		if errVal != nil && errVal.Export() != nil {
			errMsg = errVal.String()
		}
		t.Fatalf("JS Fetch failed or result is incorrect. Error: %v", errMsg)
	}
}

func TestFetchFile(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(tmpFile, []byte("local-content"), 0644)

	vm := goja.New()
	mod := &Module{client: &http.Client{Timeout: 5 * time.Second}}
	mod.Loader(nil, vm, vm.NewObject())

	_, err := vm.RunString(`
		var result = null;
		fetch("file://` + tmpFile + `")
			.then(resp => resp.text())
			.then(text => { result = text; })
			.catch(err => { result = err.message; });
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}

	result := vm.Get("result")
	if result == nil || result.String() != "local-content" {
		t.Fatalf("Failed to fetch local file. Got: %v", result)
	}
}

func TestFetchNoArgs(t *testing.T) {
	vm := goja.New()
	mod := &Module{client: &http.Client{Timeout: 5 * time.Second}}
	mod.Loader(nil, vm, vm.NewObject())

	_, err := vm.RunString(`
		var rejected = false;
		fetch()
			.then(() => {})
			.catch(err => { rejected = true; });
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}
	if v := vm.Get("rejected"); v == nil || v.Export() != true {
		t.Fatal("Expected fetch() with no args to reject")
	}
}

func TestFetchHTTPGet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET, got %s", r.Method)
		}
		w.Write([]byte("plain text"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	vm := goja.New()
	mod := &Module{client: ts.Client()}
	mod.Loader(nil, vm, vm.NewObject())

	_, err := vm.RunString(`
		var textResult = null;
		fetch("` + ts.URL + `/data")
			.then(resp => resp.text())
			.then(text => { textResult = text; });
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}
	if v := vm.Get("textResult"); v == nil || v.String() != "plain text" {
		t.Fatalf("Expected 'plain text', got %v", v)
	}
}

func TestFetchHTTPHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/echo-header", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Header.Get("X-Test-Header")))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	vm := goja.New()
	mod := &Module{client: ts.Client()}
	mod.Loader(nil, vm, vm.NewObject())

	_, err := vm.RunString(`
		var headerResult = null;
		fetch("` + ts.URL + `/echo-header", {
			headers: { "X-Test-Header": "hello-from-js" }
		})
			.then(resp => resp.text())
			.then(text => { headerResult = text; });
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}
	if v := vm.Get("headerResult"); v == nil || v.String() != "hello-from-js" {
		t.Fatalf("Expected header echo, got %v", v)
	}
}

func TestFetchHTTP404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	vm := goja.New()
	mod := &Module{client: ts.Client()}
	mod.Loader(nil, vm, vm.NewObject())

	_, err := vm.RunString(`
		var fetchOk = null;
		var fetchStatus = null;
		fetch("` + ts.URL + `/missing")
			.then(resp => {
				fetchOk = resp.ok;
				fetchStatus = resp.status;
			});
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}
	if v := vm.Get("fetchOk"); v == nil || v.Export() != false {
		t.Fatalf("Expected ok=false for 404, got %v", v)
	}
	if v := vm.Get("fetchStatus"); v == nil || v.ToInteger() != 404 {
		t.Fatalf("Expected status=404, got %v", v)
	}
}

func TestFetchJSONParseError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bad-json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not valid json {{{"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	vm := goja.New()
	mod := &Module{client: ts.Client()}
	mod.Loader(nil, vm, vm.NewObject())

	_, err := vm.RunString(`
		var jsonError = false;
		fetch("` + ts.URL + `/bad-json")
			.then(resp => resp.json())
			.then(data => {})
			.catch(err => { jsonError = true; });
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}
	if v := vm.Get("jsonError"); v == nil || v.Export() != true {
		t.Fatal("Expected json() to reject on invalid JSON")
	}
}

func TestFetchFileMissing(t *testing.T) {
	vm := goja.New()
	mod := &Module{client: &http.Client{Timeout: 5 * time.Second}}
	mod.Loader(nil, vm, vm.NewObject())

	_, err := vm.RunString(`
		var fileError = false;
		fetch("file:///nonexistent/path/to/nothing.txt")
			.then(resp => resp.text())
			.catch(err => { fileError = true; });
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}
	if v := vm.Get("fileError"); v == nil || v.Export() != true {
		t.Fatal("Expected fetch of missing file to reject")
	}
}

func TestFetchRelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "relative.txt")
	os.WriteFile(tmpFile, []byte("relative-data"), 0644)

	vm := goja.New()
	mod := &Module{client: &http.Client{Timeout: 5 * time.Second}}
	mod.Loader(nil, vm, vm.NewObject())

	// Fetch using a bare path (no scheme)
	_, err := vm.RunString(`
		var relResult = null;
		fetch("` + tmpFile + `")
			.then(resp => resp.text())
			.then(text => { relResult = text; })
			.catch(err => { relResult = "ERROR: " + err; });
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}
	if v := vm.Get("relResult"); v == nil || v.String() != "relative-data" {
		t.Fatalf("Expected 'relative-data', got %v", v)
	}
}

func TestFetchNetworkError(t *testing.T) {
	vm := goja.New()
	mod := &Module{client: &http.Client{Timeout: 1 * time.Second}}
	mod.Loader(nil, vm, vm.NewObject())

	_, err := vm.RunString(`
		var netErr = false;
		fetch("http://127.0.0.1:1")
			.then(resp => resp.text())
			.catch(err => { netErr = true; });
	`)
	if err != nil {
		t.Fatalf("JS failure: %v", err)
	}
	if v := vm.Get("netErr"); v == nil || v.Export() != true {
		t.Fatal("Expected network error for unreachable host")
	}
}

func TestModuleNameAndDoc(t *testing.T) {
	mod := &Module{}
	if mod.Name() != "fetch" {
		t.Errorf("Expected name 'fetch', got %q", mod.Name())
	}
	if mod.Doc() == "" {
		t.Error("Expected non-empty doc string")
	}
}
