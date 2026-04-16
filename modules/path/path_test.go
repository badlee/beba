package path

import (
	"http-server/processor"
	"runtime"
	"testing"
)

func TestPathJoin(t *testing.T) {
	vm := processor.NewEmpty()

	_, err := vm.RunString(`
		var path = require('path')
		var result = path.join("/usr", "local", "bin");
		if (result !== "/usr/local/bin") throw new Error("join failed: " + result);
	`)
	if err != nil {
		t.Fatalf("path.join test failed: %v", err)
	}
}

func TestPathBasename(t *testing.T) {
	vm := processor.NewEmpty()

	_, err := vm.RunString(`
		var path = require('path')
		if (path.basename("/foo/bar/baz.html") !== "baz.html") throw new Error("basename failed");
		if (path.basename("/foo/bar/baz.html", ".html") !== "baz") throw new Error("basename with ext failed");
		if (path.basename("") !== ".") throw new Error("basename empty failed");
	`)
	if err != nil {
		t.Fatalf("path.basename test failed: %v", err)
	}
}

func TestPathDirname(t *testing.T) {
	vm := processor.NewEmpty()

	_, err := vm.RunString(`
		var path = require('path')
		if (path.dirname("/foo/bar/baz.html") !== "/foo/bar") throw new Error("dirname failed: " + path.dirname("/foo/bar/baz.html"));
	`)
	if err != nil {
		t.Fatalf("path.dirname test failed: %v", err)
	}
}

func TestPathExtname(t *testing.T) {
	vm := processor.NewEmpty()

	_, err := vm.RunString(`
		var path = require('path')
		if (path.extname("index.html") !== ".html") throw new Error("extname failed");
		if (path.extname("index.") !== ".") throw new Error("extname dot failed");
		if (path.extname("index") !== "") throw new Error("extname no ext failed");
		if (path.extname("file.tar.gz") !== ".gz") throw new Error("extname multi dot failed");
	`)
	if err != nil {
		t.Fatalf("path.extname test failed: %v", err)
	}
}

func TestPathIsAbsolute(t *testing.T) {
	vm := processor.NewEmpty()

	if runtime.GOOS != "windows" {
		_, err := vm.RunString(`
			var path = require('path')
			if (!path.isAbsolute("/foo/bar")) throw new Error("isAbsolute abs failed");
			if (path.isAbsolute("foo/bar")) throw new Error("isAbsolute rel failed");
		`)
		if err != nil {
			t.Fatalf("path.isAbsolute test failed: %v", err)
		}
	}
}

func TestPathNormalize(t *testing.T) {
	vm := processor.NewEmpty()

	_, err := vm.RunString(`
		var path = require('path')
		var n = path.normalize("/foo/bar//baz/../qux");
		if (n !== "/foo/bar/qux") throw new Error("normalize failed: " + n);
	`)
	if err != nil {
		t.Fatalf("path.normalize test failed: %v", err)
	}
}

func TestPathRelative(t *testing.T) {
	vm := processor.NewEmpty()

	_, err := vm.RunString(`
		var path = require('path')
		var r = path.relative("/data/orandea/test/aaa", "/data/orandea/impl/bbb");
		if (r !== "../../impl/bbb") throw new Error("relative failed: " + r);
	`)
	if err != nil {
		t.Fatalf("path.relative test failed: %v", err)
	}
}

func TestPathParse(t *testing.T) {
	vm := processor.NewEmpty()

	_, err := vm.RunString(`
		var path = require('path')
		var p = path.parse("/home/user/dir/file.txt");
		if (p.base !== "file.txt") throw new Error("parse base: " + p.base);
		if (p.ext !== ".txt") throw new Error("parse ext: " + p.ext);
		if (p.name !== "file") throw new Error("parse name: " + p.name);
		if (p.dir !== "/home/user/dir") throw new Error("parse dir: " + p.dir);
	`)
	if err != nil {
		t.Fatalf("path.parse test failed: %v", err)
	}
}

func TestPathResolve(t *testing.T) {
	vm := processor.NewEmpty()

	_, err := vm.RunString(`
		var path = require('path')
		var r = path.resolve("/foo", "bar");
		if (!path.isAbsolute(r)) throw new Error("resolve should return absolute: " + r);
	`)
	if err != nil {
		t.Fatalf("path.resolve test failed: %v", err)
	}
}

func TestPathSep(t *testing.T) {
	vm := processor.NewEmpty()

	_, err := vm.RunString(`
		var path = require('path')
		if (typeof path.sep !== "string" || path.sep.length !== 1) throw new Error("sep invalid: " + path.sep);
	`)
	if err != nil {
		t.Fatalf("path.sep test failed: %v", err)
	}
}

func TestModuleNameAndDoc(t *testing.T) {
	mod := &Module{}
	if mod.Name() != "path" {
		t.Errorf("Expected name 'path', got %q", mod.Name())
	}
	if mod.Doc() == "" {
		t.Error("Expected non-empty doc string")
	}
}

func TestPathEdgeCases(t *testing.T) {
	vm := processor.NewEmpty()

	// No-args edge cases
	_, err := vm.RunString(`
		var path = require('path')

		if (path.basename() !== "") throw new Error("basename no args");
		if (path.dirname() !== ".") throw new Error("dirname no args");
		if (path.extname() !== "") throw new Error("extname no args");
		if (path.isAbsolute() !== false) throw new Error("isAbsolute no args");
		if (path.normalize() !== ".") throw new Error("normalize no args");
		if (path.relative() !== "") throw new Error("relative no args");
	`)
	if err != nil {
		t.Fatalf("Edge case test failed: %v", err)
	}
}
