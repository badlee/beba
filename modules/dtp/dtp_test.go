package dtp

import (
	"http-server/processor"
	"strings"
	"testing"
)

func TestDTPModuleLoader(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()

	mod := &Module{}
	exports := vm.NewObject()
	moduleObj := vm.NewObject()
	moduleObj.Set("exports", exports)

	mod.Loader(nil, vm.Runtime, moduleObj)

	vm.Set("dtp", exports)

	// Since full DTP networking implies standing up limba/dtp mocks,
	// we will basic-test the Javascript initialization parameter failures to ensure bindings exist.

	script := `
		var errMessage = "";
		try {
			var client = dtp.newClient();
		} catch (e) {
			errMessage = e.message || e.toString();
		}
	`

	_, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("JS Execution failed: %v", err)
	}

	errMessage := vm.Get("errMessage")
	if errMessage == nil || !strings.Contains(errMessage.String(), "requires at least 3 arguments") {
		t.Fatalf("Expected initialization failure message, got: %v", errMessage)
	}
}

func TestDTPModuleOfflineConnect(t *testing.T) {
	vm := processor.NewEmpty()
	vm.AttachGlobals()

	mod := &Module{}
	exports := vm.NewObject()
	moduleObj := vm.NewObject()
	moduleObj.Set("exports", exports)

	mod.Loader(nil, vm.Runtime, moduleObj)

	vm.Set("dtp", exports)

	// Connect to dummy non-existent server to verify event hook routing
	script := `
		var client = dtp.newClient("127.0.0.1:0", "fake-device", "secret");
		var connectFired = false;
		var errorFired = false;
		
		client.on("connect", function() { connectFired = true; });
		client.on("error", function(err) { errorFired = true; });
		
		try {
			client.connect();
		} catch (e) {
			// Expecting failure
		}
	`

	_, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("JS Execution failed: %v", err)
	}

	errorFired := vm.Get("errorFired")
	if errorFired == nil || errorFired.Export() != true {
		t.Fatalf("Expected error hook to fire on bogus connection")
	}
}
