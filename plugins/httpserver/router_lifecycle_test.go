package httpserver

import (
	"http-server/plugins/config"
	"os"
	"path/filepath"
	"testing"

	_ "http-server/modules/fs" // allow `require('fs')` in js script
)

func TestFsRouter_Lifecycle_Start(t *testing.T) {
	dir := t.TempDir()

	// Fichier _start.js qui crée un fichier témoin
	witness := filepath.Join(dir, "witness.txt")
	script := "require('fs').writeFileSync('" + filepath.ToSlash(witness) + "', 'started');"
	writeFile(t, dir, "_start.js", script)

	appConfig := &config.AppConfig{NoHtmx: true, Silent: false}
	httpSrv := New(Config{AppName: "test"})

	cfg := RouterConfig{
		Root:      dir,
		AppConfig: appConfig,
		App:       httpSrv,
	}

	_, err := FsRouter(cfg)
	if err != nil {
		t.Fatalf("FsRouter failed: %v", err)
	}

	// Simuler le démarrage (qui exécute les starupFn)
	// Dans httpserver.go, Listen appelle BeforeServeFunc qui exécute les starupFn.
	// On peut les appeler manuellement pour le test.
	for _, fns := range httpSrv.starupFn {
		for _, f := range fns {
			if err := f(); err != nil {
				t.Errorf("startup fn failed: %v", err)
			}
		}
	}

	// Vérifier si le témoin existe
	if _, err := os.Stat(witness); os.IsNotExist(err) {
		t.Error("_start.js was not executed or failed to create witness")
	}
}

func TestFsRouter_Lifecycle_Close(t *testing.T) {
	dir := t.TempDir()

	// Fichier _close.js qui crée un fichier témoin
	witness := filepath.Join(dir, "closed.txt")
	script := `require("fs").writeFileSync("` + filepath.ToSlash(witness) + `", "closed");`
	writeFile(t, dir, "_close.js", script)

	appConfig := &config.AppConfig{NoHtmx: true}
	httpSrv := New(Config{AppName: "test"})

	cfg := RouterConfig{
		Root:      dir,
		AppConfig: appConfig,
		App:       httpSrv,
	}

	_, err := FsRouter(cfg)
	if err != nil {
		t.Fatalf("FsRouter failed: %v", err)
	}

	// Simuler la fermeture
	for _, fns := range httpSrv.shutdownFn {
		for _, f := range fns {
			if err := f(); err != nil {
				t.Errorf("shutdown fn failed: %v", err)
			}
		}
	}

	// Vérifier si le témoin existe
	if _, err := os.Stat(witness); os.IsNotExist(err) {
		t.Error("_close.js was not executed or failed to create witness")
	}
}

func TestFsRouter_Cron_Registration(t *testing.T) {
	dir := t.TempDir()

	// Fichier _test.cron.js
	writeFile(t, dir, "_test.cron.js", "# CRON * * * * *\nconsole.log('cron');")

	appConfig := &config.AppConfig{NoHtmx: true}
	httpSrv := New(Config{AppName: "test"})

	cfg := RouterConfig{
		Root:      dir,
		AppConfig: appConfig,
		App:       httpSrv,
	}

	_, err := FsRouter(cfg)
	if err != nil {
		t.Fatalf("FsRouter failed: %v", err)
	}

	// Vérifier qu'un job de fermeture pour gocron a été enregistré
	if _, ok := httpSrv.shutdownFn["gocron"]; !ok {
		t.Error("gocron shutdown function was not registered")
	}
}
