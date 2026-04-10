package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/proxy"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/spf13/pflag"

	"http-server/modules/binder"
	"http-server/modules/sse"
	appconfig "http-server/plugins/config"
	"http-server/plugins/httpserver"
)

// TODO: add fs modules
// TODO: add fetch modules : http://, https://, ftp://, ftps://, sftp://, file:// (limited to the current directory)
// TODO: add net modules : TCP, UDP socket client
// TODO: add path modules : filepath manipulation
// TODO: add process modules : like nodejs controll current process
// TODO: add unicode modules
// TODO: add SSE, WebSocket modules : Client for SSE and WebSocket streams
// TODO: add zlib modules : compression and decompression
// TODO: add crypto modules : encryption and decryption
// TODO: add pdf modules : pdf generation

// -------------------- TYPES VHOST --------------------

type VhostListen struct {
	Protocol string // "http", "https", "tcp", "udp"
	Port     int
	Socket   string
}

type VhostInfo struct {
	Domain     string
	Aliases    []string
	Root       string
	ConfigPath string
	Listens    []VhostListen
	Cert       string
	Key        string
	Email      string
}

type udpSession struct {
	conn     net.Conn
	lastSeen time.Time
	worker   string
	lpath    string
}

// -------------------- MAIN --------------------

func main() {
	var watcher *appconfig.Watcher
	var currentManager *binder.Manager
	// Chargement de la configuration (flags > env > fichier > défauts)
	cfg, err := appconfig.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Fichiers de log — tracés pour flush/close propre à l'arrêt
	var logFiles []*os.File

	var globalStdout io.Writer = os.Stdout
	if cfg.Stdout != "" {
		if f, ferr := os.OpenFile(cfg.Stdout, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666); ferr != nil {
			log.Printf("Warning: cannot open stdout log %s: %v", cfg.Stdout, ferr)
		} else {
			globalStdout = io.MultiWriter(os.Stdout, f)
			logFiles = append(logFiles, f)
		}
	}

	var globalStderr io.Writer = os.Stderr
	if cfg.Stderr != "" {
		if f, ferr := os.OpenFile(cfg.Stderr, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666); ferr != nil {
			log.Printf("Warning: cannot open stderr log %s: %v", cfg.Stderr, ferr)
		} else {
			globalStderr = io.MultiWriter(os.Stderr, f)
			logFiles = append(logFiles, f)
		}
	}

	// exit : flush des logs puis os.Exit
	exit := func(errs ...any) {
		exitCode := 0
		for _, e := range errs {
			if e != nil {
				switch v := e.(type) {
				case int:
					exitCode = v
				default:
					fmt.Fprintf(globalStderr, "Error: %v\n", v)
					if exitCode == 0 {
						exitCode = 1
					}
				}
			}
		}
		for _, f := range logFiles {
			f.Sync()
			f.Close()
		}
		if watcher != nil {
			watcher.Close()
		}
		os.Exit(exitCode)
	}

	// Détection mode child (spawné par le master vhost)
	isChild := os.Getenv("HTTP_SERVER_VHOST_CHILD") == "1"

	// Sous-commandes
	args := remainingArgs()
	if !isChild && len(args) > 0 && args[0] == "test" {
		if len(args) < 2 {
			exit("test command requires a file path")
		}
		err := runTemplateTest(args[1], cfg)
		printCapturedLogs(cfg.Stdout, cfg.Stderr)
		if err != nil {
			exit(fmt.Errorf("Test Failed: %v", err))
		}
		exit()
	} else if !isChild && len(args) > 0 && args[0] == "proxy" {
		// http-server proxy --proto TCP --port 1234 --workers 'JSON'
		proto := ""
		port := 0
		workersJSON := ""
		pflag.StringVar(&proto, "proto", "tcp", "Protocol (tcp, udp, http, https)")
		pflag.IntVar(&port, "port", 0, "Port to listen on")
		pflag.StringVar(&workersJSON, "workers", "", "JSON map of domain to socket path")
		pflag.Parse()

		if port == 0 || workersJSON == "" {
			exit("proxy command requires --port and --workers")
		}
		var dm map[string]string
		if err := json.Unmarshal([]byte(workersJSON), &dm); err != nil {
			exit(fmt.Errorf("failed to parse workers JSON: %v", err))
		}
		err := runProxy(proto, port, dm, cfg)
		if err != nil {
			exit(err)
		}
		exit()
	} else if !isChild && cfg.HotReload {
		var err error
		watcher, err = appconfig.NewWatcher(appconfig.LoadConfig)
		if err != nil {
			exit(fmt.Errorf("Failed to load config: %v", err))
		}

		// Callback appelé après chaque rechargement réussi.
		// changes liste uniquement les champs dont la valeur a changé.
		watcher.OnChange(func(next *appconfig.AppConfig, changes []appconfig.FieldChange) {
			for _, c := range changes {
				switch c.Type {
				case appconfig.ChangeStatic:
					fmt.Fprintf(globalStderr, "WARN: %s — redémarrage requis", c.Warning)
				case appconfig.ChangeHotReload:
					fmt.Fprintf(globalStdout, "INFO: %s — rechargement dynamique", c.Field)
				default:
					fmt.Fprintf(globalStdout, "INFO: %s — rechargement", c.Field)
				}
			}
		})
	}

	// Répertoire racine
	root := "."
	if len(args) > 0 {
		root = args[0]
	}

	// chdir vers root en mode single ou child
	if isChild || !cfg.VHosts {
		if err := os.Chdir(root); err != nil {
			log.Printf("Warning: cannot chdir to %s: %v", root, err)
			if root, err = os.Getwd(); err != nil {
				exit(fmt.Errorf("Failed to get current directory: %v", err))
			}
		}
		root = "."
		// .env déjà chargé par LoadConfig via LoadEnvFiles, mais on recharge
		// depuis le répertoire courant après chdir pour les enfants vhost
		if isChild {
			appconfig.LoadEnvFiles(cfg.EnvFiles)
		}
	}

	// -------------------- MODE BINDER --------------------
	if len(cfg.BindFiles) > 0 {
		fmt.Printf("Starting in Binder mode using configs: %v\n", cfg.BindFiles)

		var watcher *fsnotify.Watcher

		reload := func(reload bool) ([]string, error) {
			var mergedCfg binder.Config
			var allFiles []string

			for _, file := range cfg.BindFiles {
				bcfg, files, err := binder.ParseFile(file)
				if err != nil {
					return nil, fmt.Errorf("error parsing %s: %v", file, err)
				}
				mergedCfg.Registrations = append(mergedCfg.Registrations, bcfg.Registrations...)
				mergedCfg.Groups = append(mergedCfg.Groups, bcfg.Groups...)
				allFiles = append(allFiles, files...)
			}

			if reload && currentManager != nil {
				go func() {
					if err := currentManager.Restart(); err != nil {
						fmt.Fprintf(globalStderr, "Binder manager error: %v\n", err)
						os.Exit(1)
					}
				}()
				return allFiles, nil
			}
			m := binder.NewManager(cfg)
			currentManager = m
			go func() {
				if err := m.Start(&mergedCfg); err != nil {
					fmt.Fprintf(globalStderr, "Binder manager error: %v\n", err)
				}
			}()

			return allFiles, nil
		}
		if cfg.ControlSocket != "" {
			go runControlSocket(cfg, currentManager)
		}

		files, err := reload(false)
		if err != nil {
			exit(fmt.Errorf("Initial Binder error: %v", err))
		}

		if cfg.HotReload {
			var err error
			watcher, err = fsnotify.NewWatcher()
			if err != nil {
				exit(fmt.Errorf("Failed to start watcher: %v", err))
			}
			defer watcher.Close()

			updateWatchList := func(paths []string) {
				for _, p := range paths {
					_ = watcher.Add(p)
				}
			}

			updateWatchList(files)

			fmt.Println("Hot-reload enabled for Binder")
			defer reload(false)
			for {
				select {
				case event, ok := <-watcher.Events:
					if !ok {
						return
					}
					if event.Op&fsnotify.Write == fsnotify.Write {
						fmt.Printf("INFO: Change detected in %s, reloading Binder...\n", event.Name)
						newFiles, err := reload(true)
						if err != nil {
							fmt.Fprintf(globalStderr, "RELOAD ERROR: %v\n", err)
						} else {
							updateWatchList(newFiles)
							fmt.Println("INFO: Binder configuration reloaded successfully")
						}
					}
				case err, ok := <-watcher.Errors:
					if !ok {
						return
					}
					fmt.Fprintf(globalStderr, "Watcher error: %v\n", err)
				}
			}
		} else {
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			<-sigChan
			if !cfg.Silent {
				fmt.Println("\nBinder shutting down...")
			}
		}
		
		if currentManager != nil {
			currentManager.Stop()
		}
		if cfg.ControlSocket != "" {
			os.Remove(cfg.ControlSocket)
		}
		exit()
		return
	}

	// -------------------- MODE MASTER (vhost) --------------------
	if !isChild && cfg.VHosts {
		vhostInfos, err := scanVhosts(root, cfg.Port)
		if err != nil {
			exit(fmt.Errorf("Error scanning vhost directory %s: %v", root, err))
		}

		if !cfg.Silent {
			fmt.Printf("Master process starting, spawning workers for %d vhosts...\n", len(vhostInfos))
		}

		childProcesses := []*exec.Cmd{}
		socketFiles := []string{}

		type listenerKey struct {
			protocol string
			port     int
			socket   string
		}
		portGroups := make(map[listenerKey]map[string]string)
		publicSocketFiles := []string{}

		for i, vi := range vhostInfos {
			sockPath := getInternalSocketPath(i)
			socketFiles = append(socketFiles, sockPath)
			controlSock := getControlSocketPath(i)
			socketFiles = append(socketFiles, controlSock)

			// Record every listener this vhost wants to join
			for _, l := range vi.Listens {
				lk := listenerKey{protocol: l.Protocol, port: l.Port, socket: l.Socket}
				if portGroups[lk] == nil {
					portGroups[lk] = make(map[string]string)
				}
				// Route main domain and aliases to this worker's socket
				portGroups[lk][vi.Domain] = sockPath
				for _, alias := range vi.Aliases {
					portGroups[lk][alias] = sockPath
				}

				if l.Socket != "" {
					publicSocketFiles = append(publicSocketFiles, l.Socket)
				}
			}

			childArgs := []string{vi.Root, "--bind", vi.ConfigPath, "--socket", sockPath, "--control-socket", controlSock, "--silent"}
			childArgs = append(childArgs, configToChildArgs()...)

			cmd := exec.Command(os.Args[0], childArgs...)
			cmd.Env = append(os.Environ(), "HTTP_SERVER_VHOST_CHILD=1")
			cmd.Stdout = globalStdout
			cmd.Stderr = globalStderr
			if err := cmd.Start(); err != nil {
				exit(fmt.Errorf("Failed to start worker for %s: %v", vi.Domain, err))
			}
			childProcesses = append(childProcesses, cmd)
			if !cfg.Silent {
				fmt.Printf("  -> Spawned worker [%s] on UDS %s\n", vi.Domain, sockPath)
			}
		}

		// Cleanup SIGTERM
		go func() {
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			<-sigChan
			if !cfg.Silent {
				fmt.Println("\nMaster shutting down, killing children...")
			}
			for _, cmd := range childProcesses {
				_ = cmd.Process.Kill()
			}
			for _, sock := range socketFiles {
				_ = os.Remove(sock)
			}
			for _, sock := range publicSocketFiles {
				_ = os.Remove(sock)
			}
			exit()
		}()

		for lKey, domainMap := range portGroups {
			// Spawn a proxy process for each unique protocol/port
			workersJSON, _ := json.Marshal(domainMap)
			childArgs := []string{"proxy", "--proto", lKey.protocol, "--port", strconv.Itoa(lKey.port), "--workers", string(workersJSON)}
			if lKey.socket != "" {
				childArgs = append(childArgs, "--socket", lKey.socket)
			}
			childArgs = append(childArgs, configToChildArgs()...)

			cmd := exec.Command(os.Args[0], childArgs...)
			cmd.Stdout = globalStdout
			cmd.Stderr = globalStderr
			if err := cmd.Start(); err != nil {
				log.Printf("Error: failed to start proxy for %s:%d: %v", lKey.protocol, lKey.port, err)
				continue
			}
			childProcesses = append(childProcesses, cmd)
			if !cfg.Silent {
				fmt.Printf("  -> Spawned proxy for %s on port %d\n", lKey.protocol, lKey.port)
			}
		}

		select {} // keep main alive
	}

	// -------------------- MODE CHILD / SINGLE --------------------

	// Control Socket for IPC Validation (already handled in MODE BINDER if --bind used, but needed for custom non-binder childs if any)
	if cfg.ControlSocket != "" && len(cfg.BindFiles) == 0 {
		go runControlSocket(cfg, currentManager)
	}

	app := httpserver.New(httpserver.Config{
		AppName:      "http-server",
		Stdout:       globalStdout,
		Stderr:       globalStderr,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
		Secret:       cfg.SecretKey,
	})

	// SSE + WebSocket
	app.Get("/events", sse.Handler)
	app.Get("/event/:id", sse.Handler)
	app.Get("/event/:id/:channel", sse.Handler)
	app.Get("/sse", sse.Handler)
	realtime := app.Group("/_")
	realtime.Get("/sse", sse.Handler)
	realtime.Get("/sse/:id", sse.Handler)
	realtime.Get("/sse/:id/:channel", sse.Handler)
	realtime.Use("/ws", sse.WSUpgradeMiddleware)
	realtime.Get("/ws", websocket.New(sse.WSHandler))
	realtime.Get("/ws/:id", websocket.New(sse.WSHandler))
	realtime.Get("/ws/:id/:channel", websocket.New(sse.WSHandler))
	realtime.Use("/sio", func(c fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("sid", c.Cookies("sid")) // optionnel
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	realtime.Get("/sio", sse.SIOHandler())
	realtime.Get("/sio/:id", sse.SIOHandler())
	mqttCfg := sse.MQTTConfig{
		Auth: sse.MQTTAllowAllAuth(),
		OnPublish: func(id, topic string, payload []byte, qos byte) bool {
			log.Printf("mqtt publish: %s → %s", id, topic)
			return true
		},
	}

	realtime.Use("/mqtt", sse.MQTTUpgradeMiddleware)
	realtime.Get("/mqtt", websocket.New(sse.MQTTHandler(mqttCfg)))
	realtime.Get("/mqtt/:id", websocket.New(sse.MQTTHandler(mqttCfg)))

	// Logger d'accès
	if !cfg.Silent {
		if err := os.MkdirAll(".logs", 0755); err != nil {
			log.Printf("Failed to create .logs directory: %v", err)
		}
		logFile, err := os.OpenFile(filepath.Join(".logs", "access.log"), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Printf("error opening log file: %v", err)
			app.Use(logger.New())
		} else {
			app.Use(logger.New(logger.Config{Stream: logFile}))
			logFiles = append(logFiles, logFile)
		}
	}

	// CORS
	if cfg.CORS {
		app.Use(cors.New())
	}

	// Compression
	if cfg.Gzip || cfg.Brotli || cfg.Deflate {
		app.Use(compress.New(compress.Config{
			Level: compress.LevelBestSpeed,
			Next: func(c fiber.Ctx) bool {
				// SSE et WS ne doivent pas être compressés
				p := c.Path()
				if strings.HasPrefix(p, "/events") ||
					strings.HasPrefix(p, "/event/") ||
					strings.HasPrefix(p, "/ws") {
					return true
				}
				enc := c.Get("Accept-Encoding")
				var allowed []string
				if cfg.Brotli && strings.Contains(enc, "br") {
					allowed = append(allowed, "br")
				}
				if cfg.Gzip && strings.Contains(enc, "gzip") {
					allowed = append(allowed, "gzip")
				}
				if cfg.Deflate && strings.Contains(enc, "deflate") {
					allowed = append(allowed, "deflate")
				}
				if len(allowed) == 0 {
					return true
				}
				c.Request().Header.Set("Accept-Encoding", strings.Join(allowed, ", "))
				return false
			},
		}))
	}

	// Robots
	if cfg.Robots {
		app.Get("/robots.txt", func(c fiber.Ctx) error {
			content, err := os.ReadFile(cfg.RobotsFile)
			if err != nil {
				return c.SendString("User-agent: *\nDisallow: /")
			}
			return c.Send(content)
		})
	}

	// FsRouter — routeur file-based principal (layouts, middlewares, error handlers, etc.)
	if !cfg.NoTemplate {
		routerCfg := httpserver.RouterConfig{
			Root:        root,
			TemplateExt: cfg.TemplateExt,
			IndexFile:   strings.TrimSuffix(cfg.IndexFile, filepath.Ext(cfg.IndexFile)),
			AppConfig:   cfg,
		}
		if !cfg.Silent {
			fmt.Print(httpserver.FsRouterDebug(routerCfg))
		}
		app.Use(httpserver.FsRouter(routerCfg))
	} else {
		// Fallback : serveur de fichiers statiques basique (sans FsRouter)
		staticConfig := static.Config{
			Browse:     cfg.DirListing,
			IndexNames: []string{cfg.IndexFile},
			MaxAge:     cfg.CacheTime,
		}
		if !cfg.AutoIndex {
			staticConfig.IndexNames = []string{}
		}
		app.Use("/", static.New(root, staticConfig))
	}

	// Proxy fallback
	if cfg.ProxyURL != "" {
		app.Use(func(c fiber.Ctx) error {
			return proxy.Do(c, cfg.ProxyURL+c.Path())
		})
	}

	// -------------------- DÉMARRAGE --------------------

	if !cfg.Silent {
		schema := "http"
		if cfg.HTTPS {
			schema = "https"
		}
		fmt.Printf("Starting up http-server, serving %s through %s\n\n", root, schema)
		fmt.Println("http-server settings:")
		fmt.Printf("\tCORS: %s\n", boolToStr(cfg.CORS, "enabled", "disabled"))
		fmt.Printf("\tCache: %d seconds\n", cfg.CacheTime)
		fmt.Printf("\tRead Timeout: %s\n", cfg.ReadTimeout)
		fmt.Printf("\tWrite Timeout: %s\n", cfg.WriteTimeout)
		fmt.Printf("\tIdle Timeout: %s\n", cfg.IdleTimeout)
		fmt.Printf("\tDirectory Listings: %s\n", boolToStr(cfg.DirListing, "visible", "not visible"))
		fmt.Printf("\tAutoIndex: %s\n", boolToStr(cfg.AutoIndex, "visible", "not visible"))
		fmt.Printf("\tGZIP: %v  Brotli: %v  Deflate: %v\n", cfg.Gzip, cfg.Brotli, cfg.Deflate)
		fmt.Printf("\tTemplate: %s  HTMX: %s\n",
			boolToStr(!cfg.NoTemplate, "enabled", "disabled"),
			boolToStr(!cfg.NoHtmx, "enabled", "disabled"))
		fmt.Printf("\tRobots: %v\n", cfg.Robots)
		fmt.Printf("\tProxy: %s\n", cfg.ProxyURL)
		fmt.Printf("\tHTTPS: %v  Cert: %s\n", cfg.HTTPS, cfg.Cert)
		fmt.Printf("\tAddress: %s  Port: %d\n", cfg.Address, cfg.Port)
		if cfg.Socket != "" {
			fmt.Printf("\tSocket: %s\n", cfg.Socket)
		}
		for _, ip := range getAvailableIPs(cfg.Address) {
			fmt.Printf("  %s://%s:%d\n", schema, ip, cfg.Port)
		}
		fmt.Println("Hit CTRL-C to stop the server")
	}

	listenAddr := fmt.Sprintf("%s:%d", cfg.Address, cfg.Port)

	if cfg.Socket != "" {
		if !cfg.Silent {
			id := "Unix socket"
			if getSocketNetwork(cfg.Socket) == "npipe" {
				id = "Windows Named Pipe"
			}
			fmt.Printf("Listening on %s: %s\n", id, cfg.Socket)
		}
		ln, err := listenSocket(getSocketNetwork(cfg.Socket), cfg.Socket)
		if err != nil {
			exit(fmt.Errorf("Failed to listen on %s: %v", cfg.Socket, err))
		}
		exit(app.Listener(ln))
	} else if cfg.HTTPS {
		exit(app.Listen(listenAddr, fiber.ListenConfig{
			CertFile:    cfg.Cert,
			CertKeyFile: cfg.Key,
		}))
	} else {
		exit(app.Listen(listenAddr))
	}
}

// -------------------- HELPERS --------------------

// remainingArgs retourne les arguments positionnels (non-flags) depuis os.Args.
// pflag est déjà parsé via LoadConfig → ParseFlags ; on lit directement os.Args
// pour récupérer les positionnels sans re-parser les flags.
//
// Pour distinguer les flags booléens (pas de valeur séparée) des flags à valeur,
// on délègue à appconfig.IsBoolFlag qui inspecte les tags `flag` de AppConfig
// par reflection — aucune liste manuelle à maintenir.
func remainingArgs() []string {
	var args []string
	skipNext := false
	for _, a := range os.Args[1:] {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") && !isBoolArg(a) {
				skipNext = true
			}
			continue
		}
		args = append(args, a)
	}
	return args
}

// isBoolArg normalise l'argument (strip les tirets) et délègue à IsBoolFlag.
func isBoolArg(arg string) bool {
	return appconfig.IsBoolFlag(strings.TrimLeft(arg, "-"))
}

// configToChildArgs propage les flags explicitement fournis par l'utilisateur
// aux processus enfants vhost, en excluant les flags propres au master.
//
// On s'appuie sur pflag.Visit (flags effectivement changés) + appconfig.FlagNames
// pour ne propager que ce qui existe dans AppConfig — zéro liste manuelle.
func configToChildArgs() []string {
	excluded := map[string]bool{
		"vhosts": true, "vhost": true, "port": true, "address": true,
		"silent": true, "socket": true, "bind": true, "bind-file": true,
	}
	var args []string
	// pflag.Visit parcourt uniquement les flags dont la valeur a été changée
	// par l'utilisateur (Changed == true) — pas les défauts.
	pflag.Visit(func(f *pflag.Flag) {
		if excluded[f.Name] {
			return
		}
		args = append(args, "--"+f.Name, f.Value.String())
	})
	return args
}

func boolToStr(b bool, trueStr, falseStr string) string {
	if b {
		return trueStr
	}
	return falseStr
}

// -------------------- VHOST --------------------

func scanVhosts(vhostDir string, defaultPort int) ([]VhostInfo, error) {
	var vhosts []VhostInfo
	entries, err := os.ReadDir(vhostDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		vhostRoot := filepath.Join(vhostDir, entry.Name())
		vhostName := entry.Name()

		bindFile := filepath.Join(vhostRoot, ".vhost.bind")
		hclFile := filepath.Join(vhostRoot, ".vhost")

		vhostFile := bindFile
		if _, err := os.Stat(bindFile); os.IsNotExist(err) {
			if _, err := os.Stat(hclFile); err == nil {
				vhostFile = hclFile
			} else {
				// Auto-generate a minimal .vhost.bind for this directory
				content := fmt.Sprintf("HTTP %s\n  PORT %d\n  GET / FILE .\nEND HTTP\n", vhostName, defaultPort)
				if err := os.WriteFile(bindFile, []byte(content), 0644); err != nil {
					log.Printf("Warning: failed to auto-generate %s: %v", bindFile, err)
				}
				vhostFile = bindFile
			}
		}

		bcfg, _, err := binder.ParseFile(vhostFile)
		if err != nil {
			log.Printf("Warning: failed to parse vhost config at %s: %v", vhostFile, err)
			continue
		}
		var listens []VhostListen
		var aliases []string
		var cert, key, email string

		for _, g := range bcfg.Groups {
			for _, item := range g.Items {
				if item.Address != "" && vhostName == entry.Name() {
					vhostName = item.Address
				}

				// Protocol-specific extraction
				proto := strings.ToLower(item.Name)
				var port int
				if p := item.Args.GetInt("port"); p > 0 {
					port = p
				}
				if port == 0 {
					if strings.Contains(item.Address, ":") {
						_, pstr, _ := net.SplitHostPort(item.Address)
						port, _ = strconv.Atoi(pstr)
					}
				}

				for _, r := range item.Routes {
					switch r.Method {
					case "DOMAIN":
						vhostName = r.Path
					case "ALIASES":
						parts := strings.Split(r.Path, ",")
						for _, p := range parts {
							if trimmed := strings.TrimSpace(p); trimmed != "" {
								aliases = append(aliases, trimmed)
							}
						}
					case "SSL":
						// SSL cert.pem key.pem -> Path=cert.pem, Handler=key.pem
						if r.Path != "" {
							cert = r.Path
						}
						if r.Handler != "" {
							key = r.Handler
						}
					case "PORT":
						if p, err := strconv.Atoi(r.Path); err == nil {
							port = p
						}
					case "EMAIL":
						email = r.Path
					}
				}

				if port == 0 {
					switch proto {
					case "http":
						if defaultPort > 0 {
							port = defaultPort
						} else {
							port = 80
						}
					case "https":
						port = 443
					}
				}

				listens = append(listens, VhostListen{
					Protocol: proto,
					Port:     port,
					Socket:   item.Args.Get("socket"),
				})
			}
		}

		if len(listens) == 0 {
			listens = append(listens, VhostListen{Protocol: "http", Port: defaultPort})
		}

		vhosts = append(vhosts, VhostInfo{
			Domain: vhostName, Aliases: aliases, Root: vhostRoot, ConfigPath: vhostFile, Listens: listens,
			Cert: cert, Key: key, Email: email,
		})
	}
	return vhosts, nil
}

// -------------------- RÉSEAU --------------------

func normalizeSocketPath(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	if strings.HasPrefix(path, `\\.\pipe\`) {
		return path
	}
	norm := strings.ReplaceAll(path, `/`, `_`)
	norm = strings.ReplaceAll(norm, `\`, `_`)
	norm = strings.ReplaceAll(norm, `:`, `_`)
	return `\\.\pipe\` + norm
}

func getSocketNetwork(path string) string {
	if runtime.GOOS == "windows" && strings.HasPrefix(path, `\\.\pipe\`) {
		return "npipe"
	}
	return "unix"
}

func getInternalSocketPath(index int) string {
	return normalizeSocketPath(filepath.Join(os.TempDir(), fmt.Sprintf("http-server-%d.sock", index)))
}

func getControlSocketPath(index int) string {
	return normalizeSocketPath(filepath.Join(os.TempDir(), fmt.Sprintf("http-server-%d-control.sock", index)))
}

func getAvailableIPs(bindAddr string) []string {
	if bindAddr != "0.0.0.0" && bindAddr != "::" {
		return []string{bindAddr}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return []string{"127.0.0.1"}
	}
	var ips []string
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if v, ok := a.(*net.IPNet); ok {
				if v.IP.To4() != nil {
					ips = append(ips, v.IP.String())
				} else if v.IP.To16() != nil {
					ips = append(ips, v.IP.String())
				}
			}
		}
	}
	sort.Strings(ips)
	return ips
}

// -------------------- PROXY --------------------

func runProxy(proto string, port int, workers map[string]string, cfg *appconfig.AppConfig) error {
	addr := fmt.Sprintf("%s:%d", cfg.Address, port)
	proto = strings.ToLower(proto)

	if proto == "udp" {
		return runUDPProxy(port, workers, cfg)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	if !cfg.Silent {
		fmt.Printf("%s Proxy listening on %s\n", strings.ToUpper(proto), addr)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go handleTCPProxy(proto, port, conn, workers)
	}
}

func handleTCPProxy(proto string, port int, conn net.Conn, workers map[string]string) {
	defer conn.Close()
	// Read first bytes for validation
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}
	data := buf[:n]

	// Check each worker
	for _, sockPath := range workers {
		if validateAndInject(sockPath, proto, port, data, conn) {
			return
		}
	}
}

func validateAndInject(sockPath, proto string, port int, data []byte, rawConn net.Conn) bool {
	network := "unix"
	if strings.ToLower(proto) == "udp" {
		network = "unixgram"
	}

	var c net.Conn
	var err error
	if network == "unixgram" {
		lpath := filepath.Join(os.TempDir(), fmt.Sprintf("mc-%d.sock", time.Now().UnixNano()))
		laddr, _ := net.ResolveUnixAddr("unixgram", lpath)
		raddr, _ := net.ResolveUnixAddr("unixgram", sockPath)
		c, err = net.DialUnix("unixgram", laddr, raddr)
		if err == nil {
			defer os.Remove(lpath)
		}
	} else {
		c, err = net.Dial(network, sockPath)
	}

	if err != nil {
		return false
	}
	defer c.Close()

	if strings.ToLower(proto) == "http" || strings.ToLower(proto) == "https" {
		if uconn, ok := c.(*net.UnixConn); ok && rawConn != nil {
			if fconn, ok := rawConn.(interface{ File() (*os.File, error) }); ok {
				f, _ := fconn.File()
				if f != nil {
					defer f.Close()
					rights := syscall.UnixRights(int(f.Fd()))
					uconn.WriteMsgUnix([]byte{0}, rights, nil)
					return true
				}
			}
		}
		return false
	}

	// For TCP/UDP, send a validation request
	type Req struct {
		Proto string `json:"proto"`
		Port  string `json:"port"`
		Data  []byte `json:"data"`
	}
	json.NewEncoder(c).Encode(Req{
		Proto: proto,
		Port:  strconv.Itoa(port),
		Data:  data,
	})

	type Resp struct {
		OK bool `json:"ok"`
	}
	var resp Resp
	c.SetReadDeadline(time.Now().Add(1 * time.Second))
	if err := json.NewDecoder(c).Decode(&resp); err != nil || !resp.OK {
		return false
	}

	// If TCP, pass the FD
	if strings.ToLower(proto) == "tcp" && rawConn != nil {
		unixConn, ok := c.(*net.UnixConn)
		if !ok {
			return false
		}

		var f *os.File
		// Get raw FD from the connection
		switch tc := rawConn.(type) {
		case *net.TCPConn:
			f, _ = tc.File()
		case *net.UnixConn:
			f, _ = tc.File()
		default:
			return false
		}
		if f == nil {
			return false
		}
		defer f.Close()

		rights := syscall.UnixRights(int(f.Fd()))
		_, _, err = unixConn.WriteMsgUnix([]byte{0}, rights, nil)
		return err == nil
	}

	return true
}

func runControlSocket(cfg *appconfig.AppConfig, m *binder.Manager) {
	_ = os.Remove(cfg.ControlSocket)
	ln, err := net.Listen("unix", cfg.ControlSocket)
	if err != nil {
		return
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			var req struct {
				Proto string `json:"proto"`
				Port  string `json:"port"`
				Data  []byte `json:"data"`
			}
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				ok := false
				if m != nil {
					ok = m.Validate(req.Proto, req.Port, req.Data)
				}
				json.NewEncoder(c).Encode(map[string]bool{"ok": ok})

				// If it's a TCP vhost and we validated it, wait for the FD
				if ok && strings.ToLower(req.Proto) == "tcp" {
					unixConn, ok := c.(*net.UnixConn)
					if !ok {
						return
					}
					// Read FD
					buf := make([]byte, 1) // dummy byte
					oob := make([]byte, 32)
					n, oobn, _, _, err := unixConn.ReadMsgUnix(buf, oob)
					if err != nil || n == 0 {
						return
					}
					msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
					if err != nil || len(msgs) == 0 {
						return
					}
					fds, err := syscall.ParseUnixRights(&msgs[0])
					if err != nil || len(fds) == 0 {
						return
					}

					fd := fds[0]
					f := os.NewFile(uintptr(fd), "proxied-socket")
					defer f.Close()
					conn, err := net.FileConn(f)
					if err != nil {
						return
					}

					m.HandleWithPeek(conn, req.Data)
				}
			}
		}(conn)
	}
}

func runUDPProxy(port int, workers map[string]string, cfg *appconfig.AppConfig) error {
	addr := fmt.Sprintf("%s:%d", cfg.Address, port)
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	defer pc.Close()

	if !cfg.Silent {
		fmt.Printf("UDP Proxy listening on %s\n", addr)
	}

	sessions := make(map[string]*udpSession)
	var mu sync.RWMutex

	// Cleanup idle sessions
	go func() {
		for {
			time.Sleep(30 * time.Second)
			mu.Lock()
			for addr, s := range sessions {
				if time.Since(s.lastSeen) > 5*time.Minute {
					s.conn.Close()
					if s.lpath != "" {
						os.Remove(s.lpath)
					}
					delete(sessions, addr)
				}
			}
			mu.Unlock()
		}
	}()

	buf := make([]byte, 64*1024)
	for {
		n, remoteAddr, err := pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		clientKey := remoteAddr.String()

		mu.RLock()
		s, exists := sessions[clientKey]
		mu.RUnlock()

		if exists {
			s.lastSeen = time.Now()
			s.conn.Write(data)
			continue
		}

		// New session - validate
		for _, sockPath := range workers {
			if validateAndInject(sockPath, "udp", port, data, nil) {
				var dest *net.UnixConn
				var err error

				lpath := filepath.Join(os.TempDir(), fmt.Sprintf("mu-%d.sock", time.Now().UnixNano()))
				laddr, _ := net.ResolveUnixAddr("unixgram", lpath)
				raddr, _ := net.ResolveUnixAddr("unixgram", sockPath)
				dest, err = net.DialUnix("unixgram", laddr, raddr)
				if err != nil {
					continue
				}

				sess := &udpSession{
					conn:     dest,
					lastSeen: time.Now(),
					worker:   sockPath,
					lpath:    lpath,
				}

				mu.Lock()
				sessions[clientKey] = sess
				mu.Unlock()

				// Read responses and forward back to client
				go func(remote net.Addr, sConn net.Conn) {
					respBuf := make([]byte, 64*1024)
					for {
						rn, err := sConn.Read(respBuf)
						if err != nil {
							return
						}
						pc.WriteTo(respBuf[:rn], remote)
					}
				}(remoteAddr, dest)

				dest.Write(data)
				break
			}
		}
	}
}
