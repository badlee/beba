package crud

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/gofiber/fiber/v3"

	"http-server/processor"
)

func TestJSModule_Integration(t *testing.T) {
	db := setupTestDB(t)

	// Register module instance
	inst := &CrudInstance{
		name:      "default",
		db:        db,
		secret:    "js_secret",
		providers: nil,
		baseDir:   "",
	}
	registerCrudInstance("default", inst, true)

	// Create a Fiber app to get a valid context safely
	app := fiber.New()
	app.Get("/test", func(c fiber.Ctx) error {
		vm := processor.New("", c, nil)

		// Create a promise wrapper to handle async JS
		doneChan := make(chan bool, 1)
		errChan := make(chan any, 1)

		vm.Set("__resolve", func(call goja.FunctionCall) goja.Value {
			doneChan <- true
			return goja.Undefined()
		})
		vm.Set("__reject", func(call goja.FunctionCall) goja.Value {
			errChan <- call.Argument(0).Export()
			return goja.Undefined()
		})

		script := `
		(async () => {
			try {
				const cr = require('database');
				if (!cr) throw new Error("require('database') returned null/undefined");
				
				// 1. Create Schema
				const s = await cr.schemas.create({
					name: 'Books',
					slug: 'books',
					soft_delete: false
				});
				if (!s || s.Slug !== 'books') throw new Error("schema not created properly: " + JSON.stringify(s));

				// 2. Collection Operations
				const books = cr.collection('books');
				
				const doc1 = await books.create({ title: "Dune", pages: 400 });
				const doc2 = await books.create({ title: "Foundation", pages: 800 });
				if (doc1.title !== "Dune") throw new Error("bad title");

				const found = await books.findOne(doc1.id);
				if (found.pages !== 400) throw new Error("pages mismatch");

				const updated = await books.update(doc2.id, { pages: { $inc: 100 } });
				if (updated.pages !== 900) throw new Error("inc failed: " + updated.pages);

				const list = await books.find({ title: "Dune" });
				if (list.length !== 1) throw new Error("exact find failed");

				await books.delete(doc2.id); 
				const listAfter = await books.find({});
				if (listAfter.length !== 1) throw new Error("delete failed");

				__resolve();
			} catch (e) {
				__reject(e.toString());
			}
		})()
		`

		if _, err := vm.RunString(script); err != nil {
			t.Fatalf("script run error: %v", err)
		}

		select {
		case <-doneChan:
			// Success
		case errVal := <-errChan:
			t.Fatalf("js promise rejection: %v", errVal)
		case <-time.After(5 * time.Second):
			t.Fatalf("js timeout")
		}

		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	app.Test(req)
}
