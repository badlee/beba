package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/proxy"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/spf13/pflag"

	"http-server/modules/sse"
	"http-server/processor"
)

func main() {
	// Flags
	port := pflag.IntP("port", "p", 8080, "Port to use")
	addr := pflag.StringP("address", "a", "0.0.0.0", "Address to use")
	showDir := pflag.BoolP("show-dir", "d", true, "Show directory listings")
	autoIndex := pflag.BoolP("auto-index", "i", true, "Display autoIndex")
	gzip := pflag.BoolP("gzip", "g", false, "Enable gzip")
	brotli := pflag.BoolP("brotli", "b", false, "Enable brotli")
	silent := pflag.BoolP("silent", "s", false, "Suppress log messages")
	enableCors := pflag.Bool("cors", false, "Enable CORS")
	cacheTime := pflag.IntP("cache", "c", 3600, "Cache time (seconds)")
	proxyUrl := pflag.StringP("proxy", "P", "", "Fallback proxy if not found")
	ssl := pflag.BoolP("ssl", "S", false, "Enable https")
	cert := pflag.StringP("cert", "C", "cert.pem", "Path to ssl cert file")
	key := pflag.StringP("key", "K", "key.pem", "Path to ssl key file")
	robots := pflag.BoolP("robots", "r", false, "Respond to /robots.txt")
	ext := pflag.StringP("ext", "e", "", "Default file extension if none supplied")
	timeout := pflag.IntP("timeout", "t", 120, "Connection timeout (seconds)")
	noTemplate := pflag.Bool("no-template", false, "Disable template engine")
	find := pflag.String("find", "", "CSS selector to find elements in test mode")
	match := pflag.String("match", "", "Validation expression (regex or JS) in test mode")
	testStdout := pflag.String("stdout", "stdout.log", "File to redirect stdout to in test mode")
	testStderr := pflag.String("stderr", "stderr.log", "File to redirect stderr to in test mode")
	help := pflag.BoolP("help", "h", false, "Print this list and exit")

	pflag.Parse()

	if *help {
		fmt.Printf("Usage: http-server [path] [options]\n")
		fmt.Printf("       http-server test [file] [options]\n\nOptions:\n")
		pflag.PrintDefaults()
		return
	}

	// Handle subcommand
	args := pflag.Args()
	if len(args) > 0 && args[0] == "test" {
		if len(args) < 2 {
			fmt.Println("Error: test command requires a file path")
			os.Exit(1)
		}
		testFile := args[1]
		err := runTemplateTest(testFile, *find, *match, *testStdout, *testStderr)
		printCapturedLogs(*testStdout, *testStderr)
		if err != nil {
			fmt.Printf("\nTest Failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle positional arg for root
	root := "."
	if len(args) > 0 {
		root = args[0]
	}

	app := fiber.New(fiber.Config{
		// DisableStartupMessage not found in v3 rc3, assuming removed or default.
	})

	sseManager := sse.NewManager()
	app.Get("/events", sseManager.Handler)

	// Middleware
	if !*silent {
		app.Use(logger.New())
	}

	if *enableCors {
		app.Use(cors.New())
	}

	if *gzip || *brotli {
		level := compress.LevelBestSpeed
		app.Use(compress.New(compress.Config{
			Level: level,
		}))
	}

	// Robots
	if *robots {
		app.Get("/robots.txt", func(c fiber.Ctx) error {
			return c.SendString("User-agent: *\nDisallow: /")
		})
	}

	// Static
	staticConfig := static.Config{
		Browse:     *showDir,
		IndexNames: []string{"index.html"},
		MaxAge:     *cacheTime,
	}
	if !*autoIndex {
		staticConfig.IndexNames = []string{}
	}

	// Template Engine Middleware
	if !*noTemplate {
		app.Use(func(c fiber.Ctx) error {
			path := c.Path()
			fullPath := filepath.Join(root, path)

			// Helper to check and process file
			tryProcess := func(fPath string) error {
				// Check if exists and is file
				stat, err := os.Stat(fPath)
				if err != nil || stat.IsDir() {
					return fiber.ErrNotFound
				}

				// Check extension
				ext := strings.ToLower(filepath.Ext(fPath))
				if ext == ".html" || ext == ".htm" {
					res, err := processor.ProcessFile(fPath, c, sseManager)
					if err != nil {
						// Fallback to raw file or error?
						// If template error, probably show it
						return c.Status(500).SendString(fmt.Sprintf("Template Error:\n%v", err))
					}
					c.Set("Content-Type", "text/html")
					return c.SendString(res)
				}
				return fiber.ErrNotFound
			}

			// 0. Detect HTMX
			isHTMX := c.Get("HX-Request") != ""
			if isHTMX {
				// We could do something specific for HTMX here if needed
			}

			// 1. Try exact path
			if err := tryProcess(fullPath); err == nil {
				return nil
			}

			// 2. Try index.html if is directory
			stat, err := os.Stat(fullPath)
			if err == nil && stat.IsDir() {
				if *autoIndex {
					idxPath := filepath.Join(fullPath, "index.html")
					if err := tryProcess(idxPath); err == nil {
						return nil
					}
				}
			}

			return c.Next()
		})
	}

	app.Use("/", static.New(root, staticConfig))

	// Proxy fallback
	if *proxyUrl != "" {
		app.Use(func(c fiber.Ctx) error {
			target := *proxyUrl + c.Path()
			return proxy.Do(c, target)
		})
	}

	// Start logic
	listenAddr := fmt.Sprintf("%s:%d", *addr, *port)
	schema := "http"
	if *ssl {
		schema = "https"
	}

	if !*silent {
		fmt.Printf("Starting up http-server, serving %s through %s\n\n", root, schema)
		fmt.Println("http-server settings:")
		fmt.Println("COOP: disabled") // Not implemented feature
		fmt.Printf("CORS: %s\n", boolToString(*enableCors, "enabled", "disabled"))
		fmt.Printf("Cache: %d seconds\n", *cacheTime)
		fmt.Printf("Connection Timeout: %d seconds\n", *timeout)
		fmt.Printf("Directory Listings: %s\n", boolToString(*showDir, "visible", "not visible"))
		fmt.Printf("AutoIndex: %s\n", boolToString(*autoIndex, "visible", "not visible"))
		fmt.Printf("Serve GZIP Files: %v\n", *gzip)
		fmt.Printf("Serve Brotli Files: %v\n", *brotli)

		extStr := *ext
		if extStr == "" {
			extStr = "none"
		}
		fmt.Printf("Default File Extension: %s\n", extStr)

		fmt.Println("\nAvailable on:")

		ips := getAvailableIPs(*addr)
		for _, ip := range ips {
			fmt.Printf("  %s://%s:%d\n", schema, ip, *port)
		}
		fmt.Println("Hit CTRL-C to stop the server")
	}

	var err error
	if *ssl {
		err = app.Listen(listenAddr, fiber.ListenConfig{
			CertFile:    *cert,
			CertKeyFile: *key,
		})
	} else {
		err = app.Listen(listenAddr)
	}

	if err != nil {
		log.Fatal(err)
	}
}

func boolToString(b bool, trueStr, falseStr string) string {
	if b {
		return trueStr
	}
	return falseStr
}

func getAvailableIPs(bindAddr string) []string {
	var ips []string

	// If binding to specific IP, just show that
	if bindAddr != "0.0.0.0" && bindAddr != "::" {
		return []string{bindAddr}
	}

	// Get all interface addresses
	ifaces, err := net.Interfaces()
	if err != nil {
		return []string{"127.0.0.1"}
	}

	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			switch v := a.(type) {
			case *net.IPNet:
				if v.IP.To4() != nil {
					ips = append(ips, v.IP.String())
				}
			}
		}
	}

	// Sort IPs to ensure consistent order (optional but nice)
	sort.Slice(ips, func(i, j int) bool {
		return ips[i] < ips[j]
	})

	return ips
}
