package fs

import (
	"http-server/processor"
	"path/filepath"
	"testing"
)

func TestFSSyncMethods(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	tmpDir := t.TempDir()
	vm.Set("TEST_DIR", filepath.ToSlash(tmpDir))

	script := `
		var file = TEST_DIR + "/test.txt";
		fs.writeFileSync(file, "hello");
		if (fs.readFileSync(file) !== "hello") throw new Error("Read failed");
		
		fs.appendFileSync(file, " world");
		if (fs.readFileSync(file) !== "hello world") throw new Error("Append failed");
		
		if (!fs.existsSync(file)) throw new Error("Exists failed");
		
		var stat = fs.statSync(file);
		if (!stat.isFile() || stat.size !== 11) throw new Error("Stat failed: size=" + stat.size);
		
		fs.mkdirSync(TEST_DIR + "/sub", { recursive: true });
		var dirStat = fs.statSync(TEST_DIR + "/sub");
		if (!dirStat.isDirectory()) throw new Error("Mkdir/Stat dir failed");
		
		var files = fs.readdirSync(TEST_DIR);
		if (files.length !== 2) throw new Error("Readdir failed, expected 2 got " + files.length);
		
		fs.unlinkSync(file);
		if (fs.existsSync(file)) throw new Error("Unlink failed");
	`
	_, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("FS Sync test failed: %v", err)
	}
}

func TestFSAsyncMethods(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	tmpDir := t.TempDir()
	vm.Set("TEST_DIR", filepath.ToSlash(tmpDir))

	script := `
		var result = null;
		var file = TEST_DIR + "/async.txt";
		
		fs.writeFile(file, "123")
			.then(() => fs.readFile(file))
			.then((data) => {
				if (data !== "123") throw new Error("Async read/write mismatch");
				return fs.unlink(file);
			})
			.then(() => {
				result = "SUCCESS";
			})
			.catch(err => {
				result = err.message || err.toString();
			});
	`
	_, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("FS Async test failed: %v", err)
	}

	result := vm.Get("result")
	if result == nil || result.String() != "SUCCESS" {
		t.Fatalf("Async pipeline failed. Result: %v", result)
	}
}

func TestFSReadFileNotFound(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	// Sync read of non-existent file should panic (caught by JS try/catch)
	_, err := vm.RunString(`
		var caught = false;
		try {
			fs.readFileSync("/nonexistent/path/file.txt");
		} catch (e) {
			caught = true;
		}
		if (!caught) throw new Error("Expected error");
	`)
	if err != nil {
		t.Fatalf("FS ReadFile error test failed: %v", err)
	}
}

func TestFSWriteFileError(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	_, err := vm.RunString(`
		var caught = false;
		try {
			fs.writeFileSync("/nonexistent/dir/file.txt", "data");
		} catch (e) {
			caught = true;
		}
		if (!caught) throw new Error("Expected write error");
	`)
	if err != nil {
		t.Fatalf("FS WriteFile error test failed: %v", err)
	}
}

func TestFSAppendFileCreatesNew(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	tmpDir := t.TempDir()
	vm.Set("TEST_DIR", filepath.ToSlash(tmpDir))

	_, err := vm.RunString(`
		var f = TEST_DIR + "/appended.txt";
		fs.appendFileSync(f, "first");
		fs.appendFileSync(f, "second");
		if (fs.readFileSync(f) !== "firstsecond") throw new Error("Append create failed");
	`)
	if err != nil {
		t.Fatalf("FS AppendFile create test failed: %v", err)
	}
}

func TestFSStatError(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	_, err := vm.RunString(`
		var caught = false;
		try {
			fs.statSync("/nonexistent/path");
		} catch (e) {
			caught = true;
		}
		if (!caught) throw new Error("Expected stat error");
	`)
	if err != nil {
		t.Fatalf("FS Stat error test failed: %v", err)
	}
}

func TestFSReaddirError(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	_, err := vm.RunString(`
		var caught = false;
		try {
			fs.readdirSync("/nonexistent/dir");
		} catch (e) {
			caught = true;
		}
		if (!caught) throw new Error("Expected readdir error");
	`)
	if err != nil {
		t.Fatalf("FS Readdir error test failed: %v", err)
	}
}

func TestFSMkdirError(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	_, err := vm.RunString(`
		var caught = false;
		try {
			fs.mkdirSync("/nonexistent/deep/path");
		} catch (e) {
			caught = true;
		}
		if (!caught) throw new Error("Expected mkdir error");
	`)
	if err != nil {
		t.Fatalf("FS Mkdir error test failed: %v", err)
	}
}

func TestFSMkdirRecursive(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	tmpDir := t.TempDir()
	vm.Set("TEST_DIR", filepath.ToSlash(tmpDir))

	_, err := vm.RunString(`
		fs.mkdirSync(TEST_DIR + "/a/b/c", { recursive: true });
		if (!fs.existsSync(TEST_DIR + "/a/b/c")) throw new Error("Recursive mkdir failed");
	`)
	if err != nil {
		t.Fatalf("FS Mkdir recursive test failed: %v", err)
	}
}

func TestFSUnlinkError(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	_, err := vm.RunString(`
		var caught = false;
		try {
			fs.unlinkSync("/nonexistent/file.txt");
		} catch (e) {
			caught = true;
		}
		if (!caught) throw new Error("Expected unlink error");
	`)
	if err != nil {
		t.Fatalf("FS Unlink error test failed: %v", err)
	}
}

func TestFSExistsFalse(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	_, err := vm.RunString(`
		if (fs.existsSync("/this/does/not/exist")) throw new Error("Should return false");
	`)
	if err != nil {
		t.Fatalf("FS existsSync false test failed: %v", err)
	}
}

func TestFSAsyncReject(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	_, err := vm.RunString(`
		var rejected = false;
		fs.readFile("/nonexistent/path.txt")
			.then(() => {})
			.catch(err => { rejected = true; });
	`)
	if err != nil {
		t.Fatalf("FS Async reject test failed: %v", err)
	}

	if v := vm.Get("rejected"); v == nil || v.Export() != true {
		t.Fatal("Expected async readFile to reject for missing file")
	}
}

func TestFSStatProperties(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()
	mod := &Module{}
	mod.Loader(nil, vm.Runtime, vm.NewObject())

	tmpDir := t.TempDir()
	vm.Set("TEST_DIR", filepath.ToSlash(tmpDir))

	_, err := vm.RunString(`
		var f = TEST_DIR + "/stat-test.txt";
		fs.writeFileSync(f, "abcde");
		var st = fs.statSync(f);
		
		if (st.size !== 5) throw new Error("Wrong size: " + st.size);
		if (typeof st.mtimeMs !== "number" || st.mtimeMs <= 0) throw new Error("Bad mtimeMs: " + st.mtimeMs);
		if (!st.isFile()) throw new Error("Should be file");
		if (st.isDirectory()) throw new Error("Should not be directory");
		
		var ds = fs.statSync(TEST_DIR);
		if (ds.isFile()) throw new Error("Dir should not be file");
		if (!ds.isDirectory()) throw new Error("Dir should be directory");
	`)
	if err != nil {
		t.Fatalf("FS Stat properties test failed: %v", err)
	}
}

func TestModuleNameAndDoc(t *testing.T) {
	mod := &Module{}
	if mod.Name() != "fs" {
		t.Errorf("Expected name 'fs', got %q", mod.Name())
	}
	if mod.Doc() == "" {
		t.Error("Expected non-empty doc string")
	}
}
