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
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/pflag"

	"beba/modules/binder"
	"beba/modules/db"
	"beba/modules/storage"
	appconfig "beba/plugins/config"
	"beba/processor"
)

var Version = "dev"

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
	Protocol string // "http", "https", "tcp", "udp", "dtp", "mqtt"
	Network  string // "tcp", "udp"
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

	if cfg.ShowVersion {
		fmt.Printf("beba version %s\n", Version)
		os.Exit(0)
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

	args := remainingArgs()

	// Répertoire racine
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	root, err = filepath.Abs(root)
	if err != nil {
		exit(fmt.Errorf("Failed to get absolute path: %v", err))
	}

	processor.SetVMDir(root)
	processor.SetVMConfig(cfg)

	// Détection mode child (spawné par le master vhost)
	isChild := os.Getenv("BEBA_VHOST_CHILD") == "1" || os.Getenv("BEBA_PROXY_CHILD") == "1"

	// Sous-commandes

	if !isChild && len(args) > 0 && args[0] == "test" {
		if len(args) < 2 {
			exit("test command requires a file path")
		}
		err := runTemplateTest(args[1], cfg, root)
		printCapturedLogs(cfg.Stdout, cfg.Stderr)
		if err != nil {
			exit(fmt.Errorf("Test Failed: %v", err))
		}
		exit()
	} else if len(args) > 0 && args[0] == "proxy" {
		// beba proxy --proto TCP --port 1234 --workers 'JSON'
		proxyFlags := pflag.NewFlagSet("proxy", pflag.ContinueOnError)
		proxyFlags.ParseErrorsWhitelist.UnknownFlags = true // Ignore global flags like --vhosts
		
		proto := ""
		port := 0
		workersJSON := ""
		proxyFlags.StringVar(&proto, "proto", "tcp", "Protocol (tcp, udp, http, https)")
		proxyFlags.IntVar(&port, "port", 0, "Port to listen on")
		proxyFlags.StringVar(&workersJSON, "workers", "", "JSON map of domain to socket path")
		
		// Parse from os.Args[2:] which contains flags after "proxy"
		proxyFlags.Parse(os.Args[2:])

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

	// chdir vers root en mode single ou child
	if isChild || !cfg.VHosts {
		if err := os.Chdir(root); err != nil {
			log.Printf("Warning: cannot chdir to %s: %v", root, err)
			if root, err = os.Getwd(); err != nil {
				exit(fmt.Errorf("Failed to get current directory: %v", err))
			}
		}
		// Calculate data directory relative to root
		// If cfg.DataDir was provided as relative path, it will now be relative to the new CWD
		db.SetDefaultDataDir(cfg.DataDir)
		storage.Initialize(cfg.DataDir)

		root = "."
		// .env déjà chargé par LoadConfig via LoadEnvFiles, mais on recharge
		// depuis le répertoire courant après chdir pour les enfants vhost
		if isChild {
			appconfig.LoadEnvFiles(cfg.EnvFiles)
		}

		// Ensure .vhost.bind exists or load it
		if len(cfg.BindFiles) == 0 {
			vhostFile, err := ensureVhostBind(cfg, ".", "")
			if err == nil {
				cfg.BindFiles = []string{vhostFile}
			}
		}
	} else if cfg.VHosts {
		// Master process in vhost mode - we don't want to initialize default DB/Storage here
		// as it would create a .data folder in the vhosts root directory.
		// However, if we need some global shared data, we could do it, but the user requested NOT to.
	}

	// S'assurer qu'il y a une base de données par défaut dans le répertoire racine ./
	// This only runs in single mode or child mode now, because of the check above (db.SetDefaultDataDir is not called in master)
	// Actually, EnsureDefaultDatabase can still be called, but we want it to be root-aware.
	if !cfg.VHosts || isChild {
		db.EnsureDefaultDatabase()
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
				if mergedCfg.AuthManagers == nil {
					mergedCfg.AuthManagers = make(map[string]*binder.AuthManagerConfig)
				}
				for k, v := range bcfg.AuthManagers {
					mergedCfg.AuthManagers[k] = v
				}
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
			go runControlSocket(cfg, func() *binder.Manager { return currentManager })
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
		vhostInfos, err := scanVhosts(root, cfg)
		if err != nil {
			exit(fmt.Errorf("Error scanning vhost directory %s: %v", root, err))
		}

		if !cfg.Silent {
			fmt.Printf("Master process starting, spawning workers for %d vhosts...\n", len(vhostInfos))
		}

		childProcesses := []*exec.Cmd{}
		socketFiles := []string{}

		type listenerKey struct {
			network string
			port    int
			socket  string
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
				p := strings.ToUpper(l.Protocol)
				if p != "HTTP" && p != "HTTPS" {
					continue // Only HTTP/HTTPS go through the proxy in vhost mode
				}
				lk := listenerKey{network: l.Network, port: l.Port, socket: l.Socket}
				if portGroups[lk] == nil {
					portGroups[lk] = make(map[string]string)
				}
				// Route main domain and aliases to this worker's control socket
				portGroups[lk][vi.Domain] = controlSock
				for _, alias := range vi.Aliases {
					portGroups[lk][alias] = controlSock
				}

				if l.Socket != "" {
					publicSocketFiles = append(publicSocketFiles, l.Socket)
				}
			}

			childArgs := []string{vi.Root, "--bind", vi.ConfigPath, "--socket", sockPath, "--control-socket", controlSock, "--silent"}
			childArgs = append(childArgs, configToChildArgs()...)

			cmd := exec.Command(os.Args[0], childArgs...)
			cmd.Env = append(os.Environ(), "BEBA_VHOST_CHILD=1")
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
			// Spawn a proxy process for each unique network/port
			workersJSON, _ := json.Marshal(domainMap)
			childArgs := []string{"proxy", "--proto", lKey.network, "--port", strconv.Itoa(lKey.port), "--workers", string(workersJSON)}
			if lKey.socket != "" {
				childArgs = append(childArgs, "--socket", lKey.socket)
			}
			childArgs = append(childArgs, configToChildArgs()...)

			cmd := exec.Command(os.Args[0], childArgs...)
			log.Printf("DEBUG: Spawning proxy: %s %v", os.Args[0], childArgs)
			cmd.Env = append(os.Environ(), "BEBA_PROXY_CHILD=1")
			cmd.Stdout = globalStdout
			cmd.Stderr = globalStderr
			if err := cmd.Start(); err != nil {
				log.Printf("Error: failed to start proxy for %s:%d: %v", lKey.network, lKey.port, err)
				continue
			}
			childProcesses = append(childProcesses, cmd)
			if !cfg.Silent {
				fmt.Printf("  -> Spawned proxy for %s on port %d\n", lKey.network, lKey.port)
			}
		}

		select {} // keep main alive
	}

	// -------------------- MODE CHILD / SINGLE --------------------
	// This mode is now unified with Binder mode.
	// If no bind files were provided or generated, we fall back to a minimal default.
	if len(cfg.BindFiles) == 0 {
		exit(fmt.Errorf("Failed to initialize server: no bind configuration found"))
	}

	// Control Socket for IPC Validation
	if cfg.ControlSocket != "" {
		go runControlSocket(cfg, func() *binder.Manager { return currentManager })
	}

	// Binder mode already handles the rest (scroll up to -------------------- MODE BINDER --------------------)
	// We just need to make sure we don't fall through to the old manual initialization.
	select {}
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

func scanVhosts(vhostDir string, cfg *appconfig.AppConfig) ([]VhostInfo, error) {
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

		vhostFile, err := ensureVhostBind(cfg, vhostRoot, vhostName)
		if err != nil {
			log.Printf("Warning: failed to ensure vhost config at %s: %v", vhostRoot, err)
			continue
		}

		bcfg, _, err := binder.ParseFile(vhostFile)
		if err != nil {
			log.Printf("Warning: failed to parse vhost config at %s: %v", vhostFile, err)
			continue
		}
		var listens []VhostListen
		var aliases []string
		var cert, key, email string
		skipVhost := false
		hasDomain := false

		for _, g := range bcfg.Groups {
			if strings.ToUpper(g.Directive) == "TCP" || strings.ToUpper(g.Directive) == "UDP" {
				log.Printf("Warning: VHost %s is skipped: %s directive is forbidden in vhost mode. Multiplexing is only allowed in single mode.", entry.Name(), g.Directive)
				skipVhost = true
				break
			}
			for _, item := range g.Items {
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
						hasDomain = true
					case "ALIAS":
						aliases = append(aliases, r.Path)
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
						if cfg.Port > 0 {
							port = cfg.Port
						} else {
							port = 80
						}
					case "https":
						port = 443
					}
				}

				network := "tcp"
				if proto == "udp" {
					network = "udp"
				}
				
				listens = append(listens, VhostListen{
					Protocol: proto,
					Network:  network,
					Port:     port,
					Socket:   item.Args.Get("socket"),
				})
			}
		}

		if skipVhost {
			continue
		}

		if !hasDomain {
			vhostName = "*"
		}

		if len(listens) == 0 {
			listens = append(listens, VhostListen{Protocol: "http", Network: "tcp", Port: cfg.Port})
		}

		vhosts = append(vhosts, VhostInfo{
			Domain: vhostName, Aliases: aliases, Root: vhostRoot, ConfigPath: vhostFile, Listens: listens,
			Cert: cert, Key: key, Email: email,
		})
	}

	// Conflict Validation: Enforce one protocol per port, and only HTTP can be multiplexed
	usedPorts := make(map[string]string) // "addr:port" -> "PROTOCOL"
	for _, vi := range vhosts {
		for _, l := range vi.Listens {
			addr := fmt.Sprintf("%s:%d", cfg.Address, l.Port)
			proto := strings.ToUpper(l.Protocol)
			if existing, ok := usedPorts[addr]; ok {
				if existing != proto {
					return nil, fmt.Errorf("VHost Conflict: Port %s is used by both %s and %s protocols. Multiplexing is only allowed in single mode.", addr, existing, proto)
				}
				if proto != "HTTP" && proto != "HTTPS" {
					return nil, fmt.Errorf("VHost Conflict: Port %s (protocol %s) is used by multiple vhosts. Only HTTP/HTTPS can be shared in vhost mode.", addr, proto)
				}
			}
			usedPorts[addr] = proto
		}
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
	return normalizeSocketPath(filepath.Join(os.TempDir(), fmt.Sprintf("beba-%d.sock", index)))
}

func getControlSocketPath(index int) string {
	return normalizeSocketPath(filepath.Join(os.TempDir(), fmt.Sprintf("beba-%d-control.sock", index)))
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

	// If it's HTTP/HTTPS, try to match by host
	host := ""
	if (strings.ToLower(proto) == "http" || strings.ToLower(proto) == "tcp") && len(data) > 0 {
		if data[0] == 0x16 {
			host = extractSNI(data)
		} else {
			host = extractHost(data)
		}
	}

	if host != "" {
		for pattern, sockPath := range workers {
			if matchDomain(pattern, host) {
				if validateAndInject(sockPath, proto, port, data, conn) {
					return
				}
			}
		}
	}

	// Fallback or non-HTTP: Check each worker
	for _, sockPath := range workers {
		if validateAndInject(sockPath, proto, port, data, conn) {
			return
		}
	}
}

func matchDomain(pattern, host string) bool {
	if pattern == "*" || pattern == host {
		return true
	}
	// Regex matching /regex/flags
	if strings.HasPrefix(pattern, "/") && strings.LastIndex(pattern, "/") > 0 {
		lastIdx := strings.LastIndex(pattern, "/")
		regStr := pattern[1:lastIdx]
		flags := pattern[lastIdx+1:]
		
		if strings.Contains(flags, "i") {
			regStr = "(?i)" + regStr
		}
		
		re, err := regexp.Compile(regStr)
		if err == nil {
			return re.MatchString(host)
		}
	}
	// Wildcard matching *.example.com
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")
		if len(parts) == 2 {
			return strings.HasPrefix(host, parts[0]) && strings.HasSuffix(host, parts[1])
		}
	}
	return false
}

func extractHost(peek []byte) string {
	s := string(peek)
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), "host:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) > 1 {
				h := strings.TrimSpace(parts[1])
				// strip port if present
				if strings.Contains(h, ":") {
					h, _, _ = net.SplitHostPort(h)
				}
				return h
			}
		}
	}
	return ""
}

func extractSNI(peek []byte) string {
	if len(peek) < 43 {
		return ""
	}
	pos := 43
	if len(peek) <= pos {
		return ""
	}
	sessionIDLen := int(peek[pos])
	pos += 1 + sessionIDLen
	if len(peek) <= pos+2 {
		return ""
	}
	cipherSuiteLen := int(peek[pos])<<8 | int(peek[pos+1])
	pos += 2 + cipherSuiteLen
	if len(peek) <= pos {
		return ""
	}
	compressionMethodLen := int(peek[pos])
	pos += 1 + compressionMethodLen
	if len(peek) <= pos+2 {
		return ""
	}
	extensionsLen := int(peek[pos])<<8 | int(peek[pos+1])
	pos += 2
	end := pos + extensionsLen
	if len(peek) < end {
		return ""
	}
	for pos+4 <= end {
		extType := int(peek[pos])<<8 | int(peek[pos+1])
		extLen := int(peek[pos+2])<<8 | int(peek[pos+3])
		pos += 4
		if extType == 0x00 { // SNI
			if pos+2 > end {
				return ""
			}
			sniListLen := int(peek[pos])<<8 | int(peek[pos+1])
			pos += 2
			if pos+sniListLen > end {
				return ""
			}
			if pos+3 > end {
				return ""
			}
			// server name type (1) + name len (2)
			nameLen := int(peek[pos+1])<<8 | int(peek[pos+2])
			pos += 3
			if pos+nameLen > end {
				return ""
			}
			return string(peek[pos : pos+nameLen])
		}
		pos += extLen
	}
	return ""
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



	// 1. Send validation request
	type Req struct {
		Proto string `json:"proto"`
		Port  string `json:"port"`
		Data  []byte `json:"data"`
	}
	if err := json.NewEncoder(c).Encode(Req{
		Proto: proto,
		Port:  strconv.Itoa(port),
		Data:  data,
	}); err != nil {
		return false
	}

	// 2. Wait for OK
	type Resp struct {
		OK bool `json:"ok"`
	}
	var resp Resp
	c.SetReadDeadline(time.Now().Add(1 * time.Second))
	if err := json.NewDecoder(c).Decode(&resp); err != nil || !resp.OK {
		return false
	}

	// 3. If TCP/HTTP/HTTPS, pass the FD
	if strings.ToLower(proto) == "tcp" && rawConn != nil {
		var f *os.File
		if fconn, ok := rawConn.(interface{ File() (*os.File, error) }); ok {
			f, _ = fconn.File()
		}
		if f == nil {
			return false
		}
		defer f.Close()
		return sendFD(c, f) == nil
	}

	return true
}
func runControlSocket(cfg *appconfig.AppConfig, getManager func() *binder.Manager) {
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
				m := getManager()
				if m != nil {
					ok = m.Validate(req.Proto, req.Port, req.Data)
				}
				log.Printf("ControlSocket: Validated proto=%s port=%s ok=%v", req.Proto, req.Port, ok)
				json.NewEncoder(c).Encode(map[string]bool{"ok": ok})

				// If it's a TCP vhost and we validated it, wait for the FD
				if ok && strings.ToLower(req.Proto) == "tcp" {
					f, err := receiveFD(c)
					if err != nil {
						log.Printf("ControlSocket: receiveFD failed: %v", err)
						return
					}
					defer f.Close()
					conn, err := net.FileConn(f)
					if err != nil {
						log.Printf("ControlSocket: FileConn failed: %v", err)
						return
					}

					log.Printf("ControlSocket: FD passed successfully, calling HandleWithPeek")
					m.HandleWithPeek(conn, req.Data)
				}
			} else {
				log.Printf("ControlSocket: Decode failed: %v", err)
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

func ensureVhostBind(cfg *appconfig.AppConfig, root string, domain string) (string, error) {
	bindPath := filepath.Join(root, ".vhost.bind")
	hclPath := filepath.Join(root, ".vhost")

	if _, err := os.Stat(bindPath); err == nil {
		return bindPath, nil
	}
	if _, err := os.Stat(hclPath); err == nil {
		return hclPath, nil
	}

	// Auto-generate a minimal .vhost.bind for this directory
	address := cfg.Address
	if address == "" {
		address = "0.0.0.0"
	}
	content := fmt.Sprintf("HTTP %s\n  PORT %d\n", address, cfg.Port)
	if domain != "" && domain != "0.0.0.0" {
		content += fmt.Sprintf("  DOMAIN %s\n", domain)
	}
	content += "  ROUTER .\nEND HTTP\n"
	if err := os.WriteFile(bindPath, []byte(content), 0644); err != nil {
		return "", err
	}
	return bindPath, nil
}
