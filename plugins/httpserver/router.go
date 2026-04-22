package httpserver

// fsrouter.go — Routeur file-based inspiré de Next.js pour Fiber v3.
//
// # Convention de nommage des fichiers
//
//   pages/
//   ├── index.html              → GET  /
//   ├── about.html              → GET  /about
//   ├── blog/
//   │   ├── index.html          → GET  /blog
//   │   └── [slug].html         → GET  /blog/:slug
//   ├── api/
//   │   ├── users.js            → GET  /api/users         (handler JS)
//   │   ├── users/
//   │   │   └── [id].js         → GET  /api/users/:id
//   │   └── [...catchall].js    → GET  /api/*             (catch-all)
//   ├── _middleware.js          → middleware appliqué à tout le sous-arbre
//   ├── (auth)/                 → groupe de layout — préfixe de chemin ignoré
//   │   └── dashboard.html      → GET  /dashboard
//   ├── 404.html                → handler 404 personnalisé (alias de _error.html pour code=404)
//   ├── 500.html                → handler d'erreur pour le code HTTP 500
//   ├── _error.html             → handler d'erreur générique (tous codes non couverts)
//   └── api/
//       ├── 422.js              → handler d'erreur 422 pour le sous-arbre /api
//       └── _error.js           → handler d'erreur générique pour /api
//
// # Fichiers spéciaux
//
//   index.<ext>       → route racine du répertoire
//   [param].<ext>     → paramètre dynamique        → :param
//   [...param].<ext>  → catch-all                  → *
//   _middleware.js    → middleware JS appliqué en cascade
//   _layout.<ext>     → layout injecté (future)
//   {code}.<ext>      → handler d'erreur pour ce code HTTP  ex: 404.html, 500.js
//   _error.<ext>      → handler d'erreur générique (tous codes non couverts par un fichier dédié)
//   *.*               → tout autre fichier est servi statiquement (c.SendFile)
//                       sauf si son chemin relatif matche un pattern Exclude
//
// # Fichiers statiques et exclusions
//
//   Par défaut, les fichiers dont le nom commence par '.' sont exclus (regexp `^\.`).
//   La regexp est testée sur chaque segment du chemin (nom de fichier + répertoires).
//   Exemple : exclure les dotfiles ET node_modules :
//     Exclude: []*regexp.Regexp{
//         regexp.MustCompile(`^\.`),
//         regexp.MustCompile(`^node_modules$`),
//     }
//   Pour désactiver les fichiers statiques sans exclusions :
//     ServeFiles: false, Exclude: []*regexp.Regexp{}  // slice vide non-nil
//
// Règle de résolution des error handlers :
//   1. Chercher {code}.<ext> dans le dossier courant
//   2. Sinon chercher _error.<ext> dans le dossier courant
//   3. Remonter au dossier parent et recommencer
//   4. Jusqu'à la racine — si rien n'est trouvé : comportement Fiber par défaut
//
// Variables disponibles dans les error handlers JS/template :
//   errorCode    (int)    — code HTTP de l'erreur
//   errorMessage (string) — message d'erreur
//   (group)/          → groupe — le nom du répertoire est ignoré dans l'URL
//
// # Méthodes HTTP
//
//   Par défaut, une route répond à GET.
//   Pour plusieurs méthodes, créer des fichiers frères :
//     users.GET.js / users.POST.js / users.DELETE.js
//   Ou un fichier avec export objet (JS) :
//     module.exports = { GET: fn, POST: fn }
//     → une routeEntry par méthode, toutes sur le même urlPattern
//
// # Usage
//
//   app := httpserver.New(httpserver.Config{...})
//   h, err := httpserver.FsRouter(httpserver.RouterConfig{
//       Root:        "./pages",
//       TemplateExt: ".html",
//       IndexFile:   "index",
//       AppConfig:   &myAppConfig,
//   })
//	 if err != nil {
//	 	 panic(err)
//	 }
//	 app.Use(h)

import (
	"bufio"
	"fmt"
	fsIO "io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"beba/plugins/config"
	"beba/processor"

	"github.com/dop251/goja"
	"github.com/fsnotify/fsnotify"
	"github.com/go-co-op/gocron/v2"
	"github.com/gofiber/fiber/v3"
)

// ==================== CONFIG ====================

// RouterConfig configure le FsRouter.
type RouterConfig struct {
	// Root est le répertoire racine des pages. Défaut : "./pages"
	Root string

	// TemplateExt est l'extension des fichiers de template. Défaut : ".html"
	TemplateExt string

	// IndexFile est le nom du fichier index (sans extension). Défaut : "index"
	IndexFile string

	// AppConfig est la configuration de l'application passée au processor.
	// Si nil, une config vide est utilisée.
	AppConfig *config.AppConfig

	// Settings est injecté comme globals `settings` dans le processor JS.
	// Optionnel.
	Settings map[string]string

	// NotFound est un handler personnalisé pour les 404.
	// Si nil, fiber.ErrNotFound est retourné.
	NotFound fiber.Handler

	// ErrorHandler est appelé si le processor retourne une erreur.
	// Si nil, une réponse 500 est retournée.
	ErrorHandler func(c fiber.Ctx, err error) error

	// StrictSlash : si true, /blog et /blog/ sont différents. Défaut : false (trailing slash ignoré)
	StrictSlash bool

	// Exclude est une liste de regexp testées sur chaque segment du chemin relatif
	// (nom de fichier et chaque répertoire) ainsi que sur le chemin complet.
	// Un fichier est ignoré si AU MOINS UN pattern matche AU MOINS UN segment.
	//
	// Défaut : []*regexp.Regexp{regexp.MustCompile(`^\.`)}
	// → exclut tout fichier ou répertoire dont le nom commence par '.' :
	//   .env, .git/, .DS_Store, .htaccess…
	//
	// Exemples :
	//   // Exclure un répertoire entier
	//   regexp.MustCompile(`^node_modules$`)
	//   // Exclure les fichiers *.test.js
	//   regexp.MustCompile(`\.test\.js$`)
	//   // Exclure par chemin complet
	//   regexp.MustCompile(`^/private/`)
	//
	// Mettre à nil pour désactiver toutes les exclusions.
	// S'applique aux fichiers statiques, templates ET handlers JS.
	Exclude []*regexp.Regexp

	// ServeFiles active la livraison des fichiers statiques (non-.js, non-template).
	// Quand true (défaut), tout fichier éligible est servi via c.SendFile :
	// .css, .js (client), .png, .pdf, favicon.ico, .wasm…
	// Quand false, seuls les templates et les handlers JS sont enregistrés.
	//
	// Défaut : true
	// Pour désactiver : RouterConfig{ServeFiles: false, Exclude: []*regexp.Regexp{…}}
	// (Exclude doit être non-nil pour que ServeFiles=false soit respecté par normalize)
	ServeFiles bool

	// App est l'instance HTTP parente. Requis pour enregistrer les handlers
	// de fermeture (_close.js) via app.RegisterOnShutdown.
	App *HTTP

	// CacheTTL est la durée de vie des fichiers en cache mémoire.
	// Si != 0, overrides AppConfig.CacheTTL.
	// Si <= 0 (explicitement), le cache est permanent (pas de goroutine de cleanup).
	// Si 0 (zero-value), utilise AppConfig.CacheTTL.
	// Défaut : 0 (hérite de AppConfig.CacheTTL, qui vaut 5m par défaut)
	CacheTTL time.Duration
}

func (r *RouterConfig) normalize() {

	if r.Root == "" {
		r.Root = "./pages"
	}
	if r.TemplateExt == "" {
		r.TemplateExt = ".html"
	}
	if r.IndexFile == "" {
		r.IndexFile = "index"
	}
	if r.AppConfig == nil {
		r.AppConfig = &config.AppConfig{}
	}
	if !strings.HasPrefix(r.TemplateExt, ".") {
		r.TemplateExt = "." + r.TemplateExt
	}
	// Exclusion par défaut : segments commençant par '.' (dotfiles, .git, .env…)
	// Regexp `^\.` testée sur chaque segment individuel du chemin.
	if r.Exclude == nil {
		r.Exclude = []*regexp.Regexp{
			regexp.MustCompile(`^\.`),
		}
		// ServeFiles par défaut : true (Exclude était nil = config fraîche)
		// L'utilisateur qui veut désactiver les fichiers statiques doit passer
		// ServeFiles:false ET Exclude:[]*regexp.Regexp{} (slice vide, non-nil).
		if !r.ServeFiles {
			r.ServeFiles = true
		}
	}
	// Si Exclude est non-nil (config explicite), ServeFiles est respecté tel quel.
}

// isExcluded retourne true si relPath doit être ignoré par au moins un pattern.
//
// Chaque pattern est testé sur :
//  1. Chaque segment individuel du chemin (nom de fichier + chaque répertoire)
//  2. Le chemin complet préfixé par '/' (séparateurs Unix)
//
// Cela permet :
//   - `^\.`         → exclut tout segment commençant par '.' (.env, .git, .DS_Store)
//   - `^node_modules$` → exclut le répertoire node_modules
//   - `\.test\.js$` → exclut les fichiers *.test.js (testé sur le nom de fichier)
//   - `^/private/`  → exclut par chemin absolu depuis la racine
func isExcluded(relPath string, patterns []*regexp.Regexp) bool {
	if len(patterns) == 0 {
		return false
	}

	// Chemin complet normalisé
	fullPath := "/" + filepath.ToSlash(relPath)

	// Segments individuels
	segments := strings.Split(filepath.ToSlash(relPath), "/")

	for _, re := range patterns {
		// Test sur le chemin complet
		if re.MatchString(fullPath) {
			return true
		}
		// Test sur chaque segment
		for _, seg := range segments {
			if seg != "" && re.MatchString(seg) {
				return true
			}
		}
	}
	return false
}

// ==================== ROUTE ENTRY ====================

// routeEntry représente une route résolue lors du scan.
type routeEntry struct {
	method      string   // "GET", "POST", "ANY", etc. Défaut : "GET"
	urlPattern  string   // Pattern Fiber, ex: "/blog/:slug"
	filePath    string   // Chemin absolu du fichier source
	isTemplate  bool     // true si c'est un fichier template (TemplateExt)
	isJS        bool     // true si c'est un fichier .js exécuté entièrement
	isExport    bool     // true si la route vient d'un module.exports = { METHOD: fn }
	isPartial   bool     // true si c'est un partial (ne pas wrapper dans le layout)
	exportKey   string   // clé dans module.exports (ex: "GET"), vide si isExport=false
	isStatic    bool     // true si le fichier est servi statiquement via c.SendFile
	isDynamic   bool     // true si la route contient au moins un :param ou *
	isCatchAll  bool     // true si la route se termine par *
	middlewares []string // chemins des _middleware.js applicables (racine → profond)
	is404       bool     // true si c'est un handler 404
	layouts     []string // chemins des _layout.<ext> applicables (profond → racine)
}

// knownHTTPMethods est l'ensemble des méthodes HTTP reconnues comme clés d'export.
var knownHTTPMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "HEAD": true, "OPTIONS": true, "ANY": true,
}

// ==================== FILE CACHE ====================

// cacheEntry stocke le contenu d'un fichier avec son timestamp de dernier accès.
type cacheEntry struct {
	content    []byte
	lastAccess int64 // UnixNano, mis à jour via atomic
}

// fileCache est un cache de fichiers avec expiration automatique.
// Les fichiers sont chargés à la première requête (lazy-load) et
// libérés après une période d'inactivité (TTL).
//
// TTL > 0 : les entrées expirent après TTL d'inactivité ; un goroutine
// de nettoyage tourne en arrière-plan.
// TTL <= 0 : cache permanent, pas de goroutine de nettoyage.
type fileCache struct {
	mu   sync.RWMutex
	data map[string]*cacheEntry
	ttl  time.Duration
	done chan struct{}
}

const defaultCacheTTL = 5 * time.Minute
const cacheCleanupInterval = 1 * time.Minute

// newFileCache crée un cache avec TTL.
// Si ttl <= 0, le cache est permanent (pas de goroutine de cleanup).
func newFileCache(ttl time.Duration) *fileCache {
	fc := &fileCache{
		data: make(map[string]*cacheEntry),
		ttl:  ttl,
		done: make(chan struct{}),
	}
	if ttl > 0 { // TTL <= 0 → cache permanent, pas de goroutine de nettoyage
		go fc.cleanup()
	}
	return fc
}

// ReadFile lit un fichier depuis le cache ou le disque.
// Mise en cache à la première lecture, rafraîchit le lastAccess à chaque accès.
func (fc *fileCache) ReadFile(path string) ([]byte, error) {
	fc.mu.RLock()
	if entry, ok := fc.data[path]; ok {
		atomic.StoreInt64(&entry.lastAccess, time.Now().UnixNano())
		fc.mu.RUnlock()
		return entry.content, nil
	}
	fc.mu.RUnlock()

	// Cache miss — lire depuis le disque
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fc.mu.Lock()
	// Double-check sous write lock (un autre goroutine a pu le charger)
	if entry, ok := fc.data[path]; ok {
		atomic.StoreInt64(&entry.lastAccess, time.Now().UnixNano())
		fc.mu.Unlock()
		return entry.content, nil
	}
	fc.data[path] = &cacheEntry{
		content:    content,
		lastAccess: time.Now().UnixNano(),
	}
	fc.mu.Unlock()

	return content, nil
}

// Invalidate supprime un fichier du cache (appelé par le watcher).
func (fc *fileCache) Invalidate(path string) {
	fc.mu.Lock()
	delete(fc.data, path)
	fc.mu.Unlock()
}

// cleanup supprime périodiquement les entrées expirées.
func (fc *fileCache) cleanup() {
	ticker := time.NewTicker(cacheCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-fc.done:
			return
		case <-ticker.C:
			now := time.Now().UnixNano()
			ttlNano := fc.ttl.Nanoseconds()
			fc.mu.Lock()
			for path, entry := range fc.data {
				if now-atomic.LoadInt64(&entry.lastAccess) > ttlNano {
					delete(fc.data, path)
				}
			}
			fc.mu.Unlock()
		}
	}
}

// Close arrête le goroutine de nettoyage.
func (fc *fileCache) Close() {
	select {
	case <-fc.done:
	default:
		close(fc.done)
	}
}

// ==================== ROUTER STATE ====================

// routerState encapsule l'état du routeur FsRouter de manière thread-safe.
// Le handler principal lit sous RLock, le watcher écrit sous Lock lors d'un rescan.
type routerState struct {
	mu               sync.RWMutex
	routes           []routeEntry
	middlewareMap    map[string]string
	notFoundHandlers map[string]string
	errorHandlers    map[string]map[string]string
	layoutMap        map[string][]string
	cache            *fileCache
}

// rescan re-scanne le répertoire et met à jour l'état de manière atomique.
func (s *routerState) rescan(cfg RouterConfig) {
	layoutMap := make(map[string][]string)
	routes, mwMap, nfHandlers, errHandlers, _, _, _ := scanDirectory(
		cfg.Root, cfg.Root, layoutMap, cfg,
	)
	sort.SliceStable(routes, func(i, j int) bool {
		return routePriority(routes[i]) > routePriority(routes[j])
	})

	s.mu.Lock()
	s.routes = routes
	s.middlewareMap = mwMap
	s.notFoundHandlers = nfHandlers
	s.errorHandlers = errHandlers
	s.layoutMap = layoutMap
	s.mu.Unlock()
}

// snapshot retourne une copie locale de l'état courant (sous RLock).
func (s *routerState) snapshot() ([]routeEntry, map[string]string, map[string]string, map[string]map[string]string, map[string][]string) {
	s.mu.RLock()
	routes := s.routes
	mw := s.middlewareMap
	nf := s.notFoundHandlers
	eh := s.errorHandlers
	lm := s.layoutMap
	s.mu.RUnlock()
	return routes, mw, nf, eh, lm
}

// ==================== ROUTE WATCHER ====================

// startRouteWatcher démarre un watcher fsnotify sur le répertoire root
// et déclenche un rescan lors des créations/suppressions de fichiers de route.
// Les modifications de contenu invalident uniquement le cache (pas de rescan).
func startRouteWatcher(root string, state *routerState, cfg RouterConfig) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Ajouter récursivement tous les sous-répertoires
	_ = filepath.WalkDir(root, func(path string, d fsIO.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(root, path)
		if relPath != "." && isExcluded(relPath, cfg.Exclude) {
			return filepath.SkipDir
		}
		_ = watcher.Add(path)
		return nil
	})

	go func() {
		var debounce *time.Timer
		const debounceDuration = 150 * time.Millisecond
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				ext := filepath.Ext(event.Name)
				isRouteFile := ext == cfg.TemplateExt || ext == ".js"

				// Invalider le cache pour les fichiers modifiés/supprimés
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Remove) {
					state.cache.Invalidate(event.Name)
				}

				// Ajouter les nouveaux répertoires au watcher
				if event.Has(fsnotify.Create) {
					if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
						_ = watcher.Add(event.Name)
					}
				}

				// Re-scanner sur création/suppression/renommage de fichiers de route
				// (une simple modification de contenu invalide uniquement le cache)
				if isRouteFile && (event.Has(fsnotify.Create) ||
					event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) {
					if debounce != nil {
						debounce.Stop()
					}
					debounce = time.AfterFunc(debounceDuration, func() {
						if !cfg.AppConfig.Silent {
							fmt.Printf("FsRouter: rescan (%s %s)\n",
								event.Op, filepath.Base(event.Name))
						}
						state.rescan(cfg)
					})
				}

			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	return watcher, nil
}

// ==================== SCANNER ====================

// FsRouter scanne le répertoire Root et enregistre les routes Fiber.
// Retourne un fiber.Handler qui sert de point d'entrée.
// Les routes sont enregistrées directement sur l'app Fiber parente via c.App().
//
// Note : FsRouter doit être appelé AVANT les autres Use/Get/Post sur l'app.
func FsRouter(cfgs ...RouterConfig) (fiber.Handler, error) {
	cfg := RouterConfig{}
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
	cfg.normalize()

	// Vérifier que le répertoire existe
	if _, err := os.Stat(cfg.Root); err != nil {
		panic(fmt.Sprintf("fsrouter: root directory %q not found: %v", cfg.Root, err))
	}

	layoutMap := make(map[string][]string)
	routes, middlewareMap, notFoundHandlers, errorHandlers, startFiles, closeFiles, cronFiles := scanDirectory(cfg.Root, cfg.Root, layoutMap, cfg)
	cronSched, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("FsRouter: failed to create cron scheduler: %v", err)
	}
	// Trier les routes : statiques avant dynamiques, catch-all en dernier
	sort.SliceStable(routes, func(i, j int) bool {
		return routePriority(routes[i]) > routePriority(routes[j])
	})

	// Préparer les fichiers _start.js
	for _, f := range startFiles {
		cfg.App.RegisterOnStartup(f, func() error {
			if !cfg.AppConfig.Silent {
				fmt.Println("FsRouter: running startup script ", f)
			}
			vm := processor.New(filepath.Dir(f), nil, cfg.AppConfig)
			_, err := vm.ExecuteFile(f)
			if err != nil && !cfg.AppConfig.Silent {
				return fmt.Errorf("FsRouter: startup script error (%s): %v\n", f, err)
			}
			return nil
		})
	}

	// Préparer les fichiers _close.js (shutdown)
	if cfg.App != nil && len(closeFiles) > 0 {
		for _, f := range closeFiles {
			cfg.App.RegisterOnShutdown(f, func() error {
				if !cfg.AppConfig.Silent {
					fmt.Println("FsRouter: running shutdown script", f)
				}
				baseDir := filepath.Dir(f)
				_, err := processor.New(baseDir, nil, cfg.AppConfig).ExecuteFile(f)
				if err != nil && !cfg.AppConfig.Silent {
					return err
				}
				return nil
			})
		}
	}

	// Gérer les tâches cron (_*.cron.js)
	if cfg.AppConfig.HasSchedule {
		for _, cronFile := range cronFiles {
			filePath := cronFile // Capture pour la closure
			expr := parseCronHeader(filePath)
			if expr == "" {
				// Pas de header CRON -> émettre un message d'erreur au démarrage
				return nil, fmt.Errorf("FsRouter: cron script %s has no CRON header", filePath)
			} else {
				// Planifier la tâche cron
				if !cfg.AppConfig.Silent {
					fmt.Printf("FsRouter: scheduling cron task %s (%s)\n", filePath, expr)
				}
				_, err := cronSched.NewJob(
					gocron.CronJob(expr, false), // false = format standard 5 champs
					gocron.NewTask(func(p string) {
						if !cfg.AppConfig.Silent {
							fmt.Printf("FsRouter [Cron]: executing %s\n", p)
						}
						_, err := processor.New(filepath.Dir(p), nil, cfg.AppConfig).ExecuteFile(p)
						if err != nil && !cfg.AppConfig.Silent {
							fmt.Fprintf(os.Stderr, "FsRouter [Cron]: execution error (%s): %v\n", p, err)
						}
					}, filePath),
				)
				if err != nil {
					return nil, fmt.Errorf("FsRouter: invalid cron expression in %s: %v", filePath, err)
				}
			}
		}
	}
	cronSched.Start()
	if cfg.App != nil {
		cfg.App.RegisterOnShutdown("gocron", func() error {
			if !cfg.AppConfig.Silent {
				fmt.Println("FsRouter: shutting down cron scheduler...")
			}
			return cronSched.Shutdown()
		})
	}

	// ==================== CACHE + STATE + WATCHER ====================

	// Résoudre le TTL du cache
	// TTL <= 0 → cache permanent (pas de cleanup goroutine)
	ttl := defaultCacheTTL
	if cfg.AppConfig != nil && cfg.AppConfig.CacheTTL != 0 {
		ttl = cfg.AppConfig.CacheTTL // flag --cache-ttl
	}
	if cfg.CacheTTL != 0 {
		ttl = cfg.CacheTTL // override par directive ROUTER
	}
	// En mode production (hot-reload désactivé), forcer cache permanent
	if cfg.AppConfig != nil && !cfg.AppConfig.HotReload {
		ttl = 0
	}

	cache := newFileCache(ttl)
	state := &routerState{
		routes:           routes,
		middlewareMap:     middlewareMap,
		notFoundHandlers: notFoundHandlers,
		errorHandlers:    errorHandlers,
		layoutMap:        layoutMap,
		cache:            cache,
	}

	// Démarrer le watcher si hot-reload activé
	if cfg.AppConfig != nil && cfg.AppConfig.HotReload {
		routeWatcher, watchErr := startRouteWatcher(cfg.Root, state, cfg)
		if watchErr != nil {
			if !cfg.AppConfig.Silent {
				fmt.Printf("FsRouter: watcher error: %v (continuing without hot-reload)\n", watchErr)
			}
		} else if routeWatcher != nil && cfg.App != nil {
			cfg.App.RegisterOnShutdown("fsrouter-watcher", func() error {
				cache.Close()
				return routeWatcher.Close()
			})
		}
	}

	// Le handler retourné est un middleware Fiber qui redirige vers les routes scannées.
	// On l'utilise comme dispatcher — il est enregistré via app.Use() dans main.
	return func(c fiber.Ctx) error {
		// Injecter le cache dans Locals pour ProcessFile et les fonctions internes
		c.Locals("_fsrouter_cache", state.cache)

		path := c.Path()
		method := c.Method()

		// Normalisation trailing slash
		if !cfg.StrictSlash && len(path) > 1 && strings.HasSuffix(path, "/") {
			path = strings.TrimSuffix(path, "/")
		}

		// Prendre un snapshot de l'état courant (sous RLock)
		routes, middlewareMap, notFoundHandlers, errorHandlers, layoutMap := state.snapshot()

		// Chercher la route correspondante
		var pathMatched bool
		for i := range routes {
			r := &routes[i]

			matched, methodMatched := matchRoute(r, method, path, c)
			if !matched {
				continue
			}
			pathMatched = true

			if !methodMatched {
				continue // Chemin existant, mais mauvaise méthode
			}

			// Appliquer les middlewares en cascade (du plus haut au plus profond)
			mwHandlers := buildMiddlewareChain(r.middlewares, middlewareMap, cfg, state.cache)

			// Handler final
			finalHandler := buildRouteHandler(r, cfg, state.cache)

			// Exécuter la chaîne ; les erreurs sont interceptées par handleHTTPError
			err := runChain(c, append(mwHandlers, finalHandler))
			if err != nil {
				return handleHTTPError(c, err, path, errorHandlers, layoutMap, cfg, state.cache)
			}
			return nil
		}

		if pathMatched {
			// Le chemin a été trouvé, mais pas la bonne méthode HTTP => 405
			return handleHTTPError(c, fiber.ErrMethodNotAllowed, path, errorHandlers, layoutMap, cfg, state.cache)
		}

		// 404 — chercher un handler 404 dans la hiérarchie
		if h := find404Handler(path, notFoundHandlers, layoutMap, cfg, state.cache); h != nil {
			return h(c)
		}
		if cfg.NotFound != nil {
			return cfg.NotFound(c)
		}
		// Pas de route trouvée → erreur 404 via le système d'error handlers
		return handleHTTPError(c, fiber.ErrNotFound, path, errorHandlers, layoutMap, cfg, state.cache)
	}, nil
}

// ==================== SCAN ====================

// scanDirectory parcourt récursivement le répertoire et collecte les routes,
// les middlewares, les handlers 404 et les error handlers.
//
// errorHandlers : map[urlDir]map[codeOrWildcard]filePath
//
//	codeOrWildcard est soit un code HTTP ("404","500",...) soit "_error" (wildcard)
func scanDirectory(
	baseDir, dir string,
	layoutMap map[string][]string,
	cfg RouterConfig,
) (routes []routeEntry, middlewareMap map[string]string, notFoundHandlers map[string]string, errorHandlers map[string]map[string]string, startFiles []string, closeFiles []string, cronFiles []string) {
	middlewareMap = make(map[string]string)
	notFoundHandlers = make(map[string]string)
	errorHandlers = make(map[string]map[string]string)

	err := filepath.WalkDir(dir, func(path string, d fsIO.DirEntry, err error) error {
		if err != nil {
			return nil // ignorer les erreurs d'accès
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)

		// Ignorer les fichiers cachés (commençant par .) sauf pour les fichiers autorisés explicitement
		if strings.HasPrefix(name, ".") {
			return nil
		}

		// Fichiers spéciaux : _start, _close, _*.cron.js
		if name == "_start.js" {
			startFiles = append(startFiles, path)
			return nil
		}
		if name == "_close.js" {
			closeFiles = append(closeFiles, path)
			return nil
		}
		if strings.HasPrefix(name, "_") && strings.HasSuffix(name, ".cron.js") {
			cronFiles = append(cronFiles, path)
			return nil
		}

		// Middleware spécial
		if base == "_middleware" && ext == ".js" {
			relDir, _ := filepath.Rel(baseDir, filepath.Dir(path))
			urlDir := fsPathToURL(relDir)
			middlewareMap[urlDir] = path
			return nil
		}

		// Layout spécial
		if base == "_layout" && (ext == cfg.TemplateExt || ext == ".js") {
			relDir, _ := filepath.Rel(baseDir, filepath.Dir(path))
			urlDir := fsPathToURL(relDir)
			layoutMap[urlDir] = append(layoutMap[urlDir], path)
			return nil
		}

		// Error handlers : {code}.{ext} et _error.{ext}
		// Formats reconnus : "404", "500", "422", ... ou "_error"
		// "404" est aussi ajouté à notFoundHandlers pour compatibilité avec find404Handler.
		if (isHTTPErrorCode(base) || base == "_error") && (ext == cfg.TemplateExt || ext == ".js") {
			relDir, _ := filepath.Rel(baseDir, filepath.Dir(path))
			urlDir := fsPathToURL(relDir)
			if errorHandlers[urlDir] == nil {
				errorHandlers[urlDir] = make(map[string]string)
			}
			errorHandlers[urlDir][base] = path
			// Alias : "404" est aussi un notFoundHandler pour rester compatible
			if base == "404" {
				notFoundHandlers[urlDir] = path
			}
			return nil
		}

		// Fichiers statiques : tout ce qui n'est pas template ni .js
		// → servi directement via c.SendFile si ServeFiles=true et non exclu
		if ext != cfg.TemplateExt && ext != ".js" {
			if !cfg.ServeFiles {
				return nil // fichiers statiques désactivés
			}
			relPath, _ := filepath.Rel(baseDir, path)
			if isExcluded(relPath, cfg.Exclude) {
				return nil // exclu par pattern
			}
			// Les fichiers statiques conservent leur extension dans l'URL
			urlPattern := staticFileURL(relPath)
			if urlPattern == "" {
				return nil
			}
			mws := collectMiddlewares(baseDir, filepath.Dir(path), middlewareMap)
			routes = append(routes, routeEntry{
				method:     "GET",
				urlPattern: urlPattern,
				filePath:   path,
				isStatic:   true,
				// Les fichiers statiques sont toujours des URLs exactes (pas de :param ni *)
				isDynamic:   false,
				isCatchAll:  false,
				middlewares: mws,
			})
			return nil
		}

		// Appliquer Exclude aussi sur les templates et les handlers JS
		// (protège contre la livraison de fichiers privés .js/_middleware etc.)
		{
			relPath, _ := filepath.Rel(baseDir, path)
			if isExcluded(relPath, cfg.Exclude) {
				return nil
			}
		}

		// Construire l'URL de base depuis le chemin relatif
		relPath, _ := filepath.Rel(baseDir, path)
		urlPattern, method, isDynamic, isCatchAll, isPartial := filePathToRoute(relPath, cfg)
		if urlPattern == "" {
			return nil
		}

		// Middlewares applicables (sera recalculé au second passage)
		mws := collectMiddlewares(baseDir, filepath.Dir(path), middlewareMap)

		// ---- Détection module.exports = { METHOD: fn, ... } ----
		// Un fichier .js peut exporter un objet dont les clés sont des méthodes HTTP.
		// On l'inspecte via un VM léger AVANT de créer les routeEntry définitives.
		if ext == ".js" && method == "GET" {
			// On inspecte uniquement si la méthode n'est pas déjà explicite dans le nom
			// (users.POST.js → method="POST" → déjà un handler simple, pas un export)
			if exportedMethods := probeModuleExports(path); len(exportedMethods) > 0 {
				for _, m := range exportedMethods {
					routes = append(routes, routeEntry{
						method:      m,
						urlPattern:  urlPattern,
						filePath:    path,
						isJS:        false,
						isExport:    true,
						isPartial:   isPartial,
						exportKey:   m,
						isDynamic:   isDynamic,
						isCatchAll:  isCatchAll,
						middlewares: mws,
					})
				}
				return nil
			}
		}

		entry := routeEntry{
			method:      method,
			urlPattern:  urlPattern,
			filePath:    path,
			isTemplate:  ext == cfg.TemplateExt,
			isJS:        ext == ".js",
			isDynamic:   isDynamic,
			isCatchAll:  isCatchAll,
			isPartial:   isPartial,
			middlewares: mws,
		}
		routes = append(routes, entry)
		return nil
	})

	_ = err

	// Second passage pour injecter les middlewares dans les routes
	// (middlewareMap est maintenant complet)
	for i := range routes {
		routes[i].middlewares = collectMiddlewares(
			baseDir,
			filepath.Dir(routes[i].filePath),
			middlewareMap,
		)
		routes[i].layouts = collectLayouts(
			baseDir,
			filepath.Dir(routes[i].filePath),
			layoutMap,
		)
	}

	return routes, middlewareMap, notFoundHandlers, errorHandlers, startFiles, closeFiles, cronFiles
}

// staticFileURL construit l'URL d'un fichier statique en conservant l'extension.
//
// Contrairement à filePathToRoute (qui supprime l'extension pour les templates/JS),
// les fichiers statiques sont accessibles à leur URL exacte avec extension.
//
// Exemples :
//
//	"style.css"          → "/style.css"
//	"images/logo.png"    → "/images/logo.png"
//	"(public)/app.wasm"  → "/app.wasm"   (groupes de layout ignorés)
//	"fonts/.hidden.ttf"  → ""            (segments commençant par _ ou . déjà filtrés en amont)
func staticFileURL(relPath string) string {
	relPath = filepath.ToSlash(relPath)
	segments := strings.Split(relPath, "/")
	var urlSegs []string
	for _, seg := range segments {
		// Ignorer les groupes de layout (auth), (public)…
		if strings.HasPrefix(seg, "(") && strings.HasSuffix(seg, ")") {
			continue
		}
		if seg == "" {
			continue
		}
		urlSegs = append(urlSegs, seg)
	}
	if len(urlSegs) == 0 {
		return ""
	}
	return "/" + strings.Join(urlSegs, "/")
}

// filePathToRoute convertit un chemin relatif de fichier en pattern d'URL Fiber.
//
//	pages/index.html             → ("/"           , "GET", false, false, false)
//	pages/about.html             → ("/about"       , "GET", false, false, false)
//	pages/blog/[slug].html       → ("/blog/:slug"  , "GET", true,  false, false)
//	pages/api/[...all].js        → ("/api/*"       , "GET", true,  true , false)
//	pages/users.POST.js          → ("/users"        , "POST",false, false, false)
//	pages/page.partial.html      → ("/page"         , "GET", false, false, true)
//	pages/(auth)/dashboard.html  → ("/dashboard"   , "GET", false, false, false)
func filePathToRoute(relPath string, cfg RouterConfig) (urlPattern, method string, isDynamic, isCatchAll, isPartial bool) {
	method = "GET"

	// Normaliser les séparateurs
	relPath = filepath.ToSlash(relPath)

	// Supprimer l'extension
	ext := filepath.Ext(relPath)
	withoutExt := strings.TrimSuffix(relPath, ext)

	// Détecter si c'est un partial (ex: page.partial.html)
	if strings.HasSuffix(withoutExt, ".partial") {
		isPartial = true
		withoutExt = strings.TrimSuffix(withoutExt, ".partial")
	}

	// Détecter la méthode HTTP dans le nom du fichier (users.POST.js → "POST")
	// Exemple : "api/users.POST" → base="api/users", method="POST"
	// On utilise knownHTTPMethods globale (exclut "ANY" — ANY n'est pas un nom de fichier valide)
	parts := strings.Split(withoutExt, ".")
	if len(parts) >= 2 {
		lastPart := strings.ToUpper(parts[len(parts)-1])
		if knownHTTPMethods[lastPart] && lastPart != "ANY" {
			method = lastPart
			withoutExt = strings.Join(parts[:len(parts)-1], ".")
		}
	}

	// Segmenter le chemin
	segments := strings.Split(withoutExt, "/")
	var urlSegments []string

	for _, seg := range segments {
		// Groupe de layout : (auth) → ignorer le segment
		if strings.HasPrefix(seg, "(") && strings.HasSuffix(seg, ")") {
			continue
		}

		// Index → segment vide (supprimé)
		if seg == cfg.IndexFile {
			continue
		}

		// Catch-all : [...param] → *
		if strings.HasPrefix(seg, "[...") && strings.HasSuffix(seg, "]") {
			param := seg[4 : len(seg)-1]
			urlSegments = append(urlSegments, "+"+param) // marqueur interne
			isDynamic = true
			isCatchAll = true
			continue
		}

		// Paramètre dynamique : [param] → :param
		if strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") {
			param := seg[1 : len(seg)-1]
			urlSegments = append(urlSegments, ":"+param)
			isDynamic = true
			continue
		}

		urlSegments = append(urlSegments, seg)
	}

	// Construire le pattern
	if len(urlSegments) == 0 {
		urlPattern = "/"
	} else {
		// Remplacer le marqueur catch-all par *
		var finalSegs []string
		for _, s := range urlSegments {
			if strings.HasPrefix(s, "+") {
				finalSegs = append(finalSegs, "*")
				break // * doit être en dernière position
			}
			finalSegs = append(finalSegs, s)
		}
		urlPattern = "/" + strings.Join(finalSegs, "/")
	}

	return urlPattern, method, isDynamic, isCatchAll, isPartial
}

// fsPathToURL convertit un chemin relatif filesystem en préfixe URL.
func fsPathToURL(relDir string) string {
	if relDir == "." || relDir == "" {
		return "/"
	}
	u := "/" + filepath.ToSlash(relDir)
	// Supprimer les groupes (auth)
	reGroup := regexp.MustCompile(`/\([^)]+\)`)
	u = reGroup.ReplaceAllString(u, "")
	return u
}

// collectMiddlewares retourne les chemins des _middleware.js applicables
// pour un répertoire donné (du plus proche de la racine au plus proche du fichier).
func collectMiddlewares(baseDir, fileDir string, middlewareMap map[string]string) []string {
	relFileDir, _ := filepath.Rel(baseDir, fileDir)
	segments := strings.Split(filepath.ToSlash(relFileDir), "/")

	var result []string
	accumulated := "/"

	// Middleware racine
	if mw, ok := middlewareMap["/"]; ok {
		result = append(result, mw)
	}

	for _, seg := range segments {
		if seg == "." || seg == "" {
			continue
		}
		if accumulated == "/" {
			accumulated = "/" + seg
		} else {
			accumulated += "/" + seg
		}
		if mw, ok := middlewareMap[accumulated]; ok {
			result = append(result, mw)
		}
	}

	return result
}

// ==================== MATCHING ====================

// matchRoute vérifie si une requête (method, path) correspond à une routeEntry.
// Popule les params Fiber si la route est dynamique.
// Retourne (pathMatched, methodMatched).
func matchRoute(r *routeEntry, method, path string, c fiber.Ctx) (bool, bool) {
	pathMatch := false

	if r.isCatchAll {
		// Le pattern est /prefix/* : vérifier le préfixe
		prefix := strings.TrimSuffix(r.urlPattern, "*")
		if len(path) >= len(prefix) && strings.EqualFold(path[:len(prefix)], prefix) {
			pathMatch = true
			// Injecter le paramètre catch-all via Locals (en conservant la casse originale pour la valeur)
			c.Locals("_fsrouter_catchall", path[len(prefix):])
		}
	} else if !r.isDynamic {
		pathMatch = strings.EqualFold(r.urlPattern, path)
	} else {
		// Route dynamique : matcher segment par segment
		patternSegs := strings.Split(strings.TrimPrefix(r.urlPattern, "/"), "/")
		pathSegs := strings.Split(strings.TrimPrefix(path, "/"), "/")

		if len(patternSegs) == len(pathSegs) {
			pathMatch = true
			params := make(map[string]string)
			for i, ps := range patternSegs {
				if strings.HasPrefix(ps, ":") {
					params[ps[1:]] = pathSegs[i]
				} else if !strings.EqualFold(ps, pathSegs[i]) {
					pathMatch = false
					break
				}
			}
			if pathMatch {
				// Injecter les params dans les Locals
				c.Locals("_fsrouter_params", params)
			}
		}
	}

	if !pathMatch {
		return false, false
	}

	methodMatch := (r.method == method || r.method == "ANY")
	return true, methodMatch
}

// ==================== HANDLER BUILDER ====================

// buildRouteHandler construit le handler Fiber pour une routeEntry.
func buildRouteHandler(r *routeEntry, cfg RouterConfig, cache *fileCache) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Exposer les params dynamiques dans le contexte Fiber
		if params, ok := c.Locals("_fsrouter_params").(map[string]string); ok {
			for k, v := range params {
				c.Locals("param_"+k, v)
			}
		}

		if r.isExport {
			return handleJSExport(c, r.filePath, r.exportKey, r.isPartial, r.layouts, cfg, cache)
		}
		if r.isTemplate {
			return handleTemplate(c, r.filePath, r.isPartial, r.layouts, cfg, cache)
		}
		if r.isJS {
			return handleJS(c, r.filePath, r.isPartial, r.layouts, cfg, cache)
		}
		if r.isStatic {
			return c.SendFile(r.filePath)
		}
		return fiber.ErrNotFound
	}
}

// handleTemplate traite un fichier template via processor.ProcessFile.
// handleTemplate exécute un template HTML avec injection de context et params.
func handleTemplate(c fiber.Ctx, filePath string, isPartial bool, layouts []string, cfg RouterConfig, cache ...*fileCache) error {
	res, err := processor.ProcessFile(filePath, c, cfg.AppConfig, cfg.Settings)
	if err != nil {
		if cfg.ErrorHandler != nil {
			return cfg.ErrorHandler(c, err)
		}
		return c.Status(fiber.StatusInternalServerError).
			SendString(fmt.Sprintf("Template Error: %v", err))
	}

	// Appliquer les layouts (sauf si c'est un partial)
	if !isPartial {
		for _, lPath := range layouts {
			c.Locals("content", res)
			ext := filepath.Ext(lPath)
			switch ext {
			case cfg.TemplateExt:
				res, err = processor.ProcessFile(lPath, c, cfg.AppConfig, cfg.Settings)
			case ".js":
				if len(cache) > 0 && cache[0] != nil {
					res, err = runAndCaptureJS(c, lPath, cfg, cache[0])
				}
			}
			if err != nil {
				return err
			}
		}
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(res)
}

// handleJS traite un fichier .js via processor.New + RunString.
func handleJS(c fiber.Ctx, filePath string, isPartial bool, layouts []string, cfg RouterConfig, cache *fileCache) error {
	res, err := runAndCaptureJS(c, filePath, cfg, cache)
	if err != nil {
		return err
	}

	// Appliquer les layouts (sauf si partial)
	if !isPartial {
		for _, lPath := range layouts {
			c.Locals("content", res)
			ext := filepath.Ext(lPath)
			if ext == cfg.TemplateExt {
				res, err = processor.ProcessFile(lPath, c, cfg.AppConfig, cfg.Settings)
			} else if ext == ".js" {
				res, err = runAndCaptureJS(c, lPath, cfg, cache)
			}
			if err != nil {
				return err
			}
		}
	}

	// Si la réponse a déjà été écrite (context.JSON, context.SendString, etc.)
	// et que les layouts n'ont pas modifié le corps, on ne fait rien.
	// Sinon, on envoie le résultat final.
	if len(c.Response().Body()) > 0 {
		return nil
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(res)
}

// runAndCaptureJS exécute un script JS et retourne sa sortie (print ou return).
func runAndCaptureJS(c fiber.Ctx, filePath string, cfg RouterConfig, cache *fileCache) (string, error) {
	content, err := cache.ReadFile(filePath)
	if err != nil {
		if cfg.ErrorHandler != nil {
			_ = cfg.ErrorHandler(c, err)
		}
		return "", err
	}

	vm := processor.New(filepath.Dir(filePath), c, cfg.AppConfig)

	// Injecter les settings
	if cfg.Settings != nil {
		settingsObj := vm.NewObject()
		for k, v := range cfg.Settings {
			settingsObj.Set(k, v)
		}
		vm.Set("settings", settingsObj)
	}

	// Exposer les params dynamiques dans le VM
	if params, ok := c.Locals("_fsrouter_params").(map[string]string); ok {
		paramsObj := vm.NewObject()
		for k, v := range params {
			paramsObj.Set(k, v)
		}
		vm.Set("params", paramsObj)
	}
	if catchall, ok := c.Locals("_fsrouter_catchall").(string); ok {
		vm.Set("catchall", catchall)
	}

	res, jsErr := vm.RunString(string(content))
	if jsErr != nil {
		if strings.Contains(jsErr.Error(), "__FIBER_ERROR__") {
			return "", parseFiberError(jsErr)
		}
		if cfg.ErrorHandler != nil {
			_ = cfg.ErrorHandler(c, jsErr)
		}
		return "", jsErr
	}

	// Si la réponse est déjà écrite
	if body := c.Response().Body(); len(body) > 0 {
		c.Response().ResetBody()
		return string(body), nil
	}

	// Vérifier le buffer print()
	if outRes, runErr := vm.RunString("__output()"); runErr == nil {
		out := outRes.String()
		if out != "" && out != "undefined" {
			return out, nil
		}
	}

	// Valeur de retour du script
	if res != nil && !res.Equals(vm.ToValue(nil)) && !res.SameAs(vm.ToValue(nil)) {
		exported := res.Export()
		if exported != nil {
			return fmt.Sprint(exported), nil
		}
	}

	return "", nil
}

// ==================== MIDDLEWARE ====================

// buildMiddlewareChain construit la chaîne de handlers pour les _middleware.js.
func buildMiddlewareChain(mwPaths []string, _ /*middlewareMap*/ map[string]string, cfg RouterConfig, cache *fileCache) []fiber.Handler {
	var handlers []fiber.Handler
	for _, mwPath := range mwPaths {
		p := mwPath // capture
		handlers = append(handlers, func(c fiber.Ctx) error {
			return runMiddlewareJS(c, p, cfg, cache)
		})
	}
	return handlers
}

// runMiddlewareJS exécute un _middleware.js.
// Le script JS peut :
//   - Appeler next() pour passer au handler suivant
//   - Retourner sans appeler next() pour court-circuiter
//   - Lancer une erreur / utiliser throwError()
func runMiddlewareJS(c fiber.Ctx, filePath string, cfg RouterConfig, cache *fileCache) error {
	content, err := cache.ReadFile(filePath)
	if err != nil {
		return c.Next() // middleware manquant → ignorer
	}

	vm := processor.New(filepath.Dir(filePath), c, cfg.AppConfig)

	nextCalled := false
	vm.Set("next", func() {
		nextCalled = true
	})

	if cfg.Settings != nil {
		settingsObj := vm.NewObject()
		for k, v := range cfg.Settings {
			settingsObj.Set(k, v)
		}
		vm.Set("settings", settingsObj)
	}

	_, jsErr := vm.RunString(string(content))
	if jsErr != nil {
		if strings.Contains(jsErr.Error(), "__FIBER_ERROR__") {
			parts := strings.Split(jsErr.Error(), "__FIBER_ERROR__")
			if len(parts) == 2 {
				fields := strings.Fields(parts[1])
				if len(fields) > 0 {
					code := 500
					_, _ = fmt.Sscanf(fields[0], "%d", &code)
					return fiber.NewError(code, strings.TrimSpace(parts[0]))
				}
			}
		}
		return jsErr
	}

	if nextCalled {
		return nil // Le middleware demande de continuer
	}

	// Si la réponse a été écrite → court-circuit
	if len(c.Response().Body()) > 0 {
		return nil
	}

	// Gérer l'absence explicite de next() comme une fin de chaîne s'il n'y a pas de config stricte,
	// mais pour faciliter l'usage, on continue silencieusement si c'est la fin du script.
	return nil
}

// runChain exécute une chaîne de handlers séquentiellement.
// Les middlewares JS sont exécutés ; s'ils écrivent dans c.Response,
// la chaîne est interrompue.
func runChain(c fiber.Ctx, handlers []fiber.Handler) error {
	if len(handlers) == 0 {
		return nil // Pas de handler, ne rien faire
	}

	// Exécuter chaque middleware
	for _, h := range handlers[:len(handlers)-1] {
		if err := h(c); err != nil {
			return err
		}
		// Court-circuit : si le middleware a écrit une réponse
		if len(c.Response().Body()) > 0 {
			return nil
		}
	}

	// Handler final
	return handlers[len(handlers)-1](c)
}

// ==================== 404 ====================

// find404Handler cherche le handler 404 le plus proche dans la hiérarchie.
func find404Handler(path string, notFoundHandlers map[string]string, layoutMap map[string][]string, cfg RouterConfig, cache *fileCache) fiber.Handler {
	// Chercher du plus profond au plus superficiel
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for depth := len(segments); depth >= 0; depth-- {
		prefix := "/"
		if depth > 0 {
			prefix = "/" + strings.Join(segments[:depth], "/")
		}
		if filePath, ok := notFoundHandlers[prefix]; ok {
			fPath := filePath
			ext := filepath.Ext(fPath)
			// Collecter les layouts pour ce handler 404
			layouts := collectLayouts(cfg.Root, filepath.Dir(fPath), layoutMap)
			return func(c fiber.Ctx) error {
				c.Status(fiber.StatusNotFound)
				if ext == cfg.TemplateExt {
					return handleTemplate(c, fPath, false, layouts, cfg, cache)
				}
				return handleJS(c, fPath, false, layouts, cfg, cache)
			}
		}
	}
	return nil
}

// ==================== PRIORITY ====================

// routePriority retourne une valeur de priorité pour le tri des routes.
// Plus élevée = traitée en premier.
//
//	Statique exacte          → 1000
//	Statique avec sous-path  → 900 - profondeur
//	Dynamique                → 500 - nombre de params
//	Catch-all                → 0
func routePriority(r routeEntry) int {
	if r.isCatchAll {
		return 0
	}
	if !r.isDynamic {
		depth := strings.Count(r.urlPattern, "/")
		return 1000 - depth
	}
	paramCount := strings.Count(r.urlPattern, ":")
	return 500 - paramCount*10
}

// ==================== ERROR HANDLERS ====================

// isHTTPErrorCode retourne true si s est un code HTTP valide (100–599).
func isHTTPErrorCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n >= 100 && n <= 599
}

// findErrorHandler cherche le handler le plus proche pour un code HTTP donné.
//
// Algorithme de résolution (du dossier de la requête vers la racine) :
//  1. Chercher {code}.<ext> dans le dossier courant
//  2. Sinon chercher _error.<ext> dans le dossier courant
//  3. Remonter au dossier parent et recommencer
//  4. Retourner nil si rien n'est trouvé jusqu'à la racine
//
// Le code "404" est traité comme un code normal — find404Handler reste
// le handler de routing (URL inconnue), findErrorHandler intercepte les
// erreurs HTTP levées par les handlers de route.
func findErrorHandler(code int, reqPath string, errorHandlers map[string]map[string]string) (filePath, kind string) {
	codeStr := fmt.Sprintf("%d", code)

	// Décomposer le chemin de la requête en segments pour remonter
	segments := strings.Split(strings.TrimPrefix(reqPath, "/"), "/")

	for depth := len(segments); depth >= 0; depth-- {
		var prefix string
		if depth == 0 {
			prefix = "/"
		} else {
			prefix = "/" + strings.Join(segments[:depth], "/")
		}

		if handlers, ok := errorHandlers[prefix]; ok {
			// Priorité 1 : code exact
			if fp, ok := handlers[codeStr]; ok {
				return fp, "code"
			}
			// Priorité 2 : wildcard _error
			if fp, ok := handlers["_error"]; ok {
				return fp, "wildcard"
			}
		}
	}
	return "", ""
}

// handleHTTPError intercepte une erreur Fiber, cherche le handler approprié
// dans errorHandlers et l'exécute. Si aucun handler n'est trouvé, l'erreur
// est retournée telle quelle (comportement Fiber par défaut).
//
// Variables injectées dans le VM ou le template :
//
//	errorCode    int    — code HTTP (ex: 404, 500)
//	errorMessage string — message de l'erreur
func handleHTTPError(c fiber.Ctx, err error, reqPath string, errorHandlers map[string]map[string]string, layoutMap map[string][]string, cfg RouterConfig, cache *fileCache) error {
	code := fiber.StatusInternalServerError
	msg := "Internal Server Error"
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		msg = e.Message
	} else if err != nil {
		msg = err.Error()
	}

	fp, _ := findErrorHandler(code, reqPath, errorHandlers)
	if fp == "" {
		if cfg.ErrorHandler != nil {
			return cfg.ErrorHandler(c, err)
		}
		return err
	}

	c.Status(code)
	ext := filepath.Ext(fp)

	// Collecter les layouts pour l'error handler
	layouts := collectLayouts(cfg.Root, filepath.Dir(fp), layoutMap)

	if ext == cfg.TemplateExt {
		return handleErrorTemplate(c, fp, code, msg, false, layouts, cfg, cache)
	}
	return handleErrorJS(c, fp, code, msg, false, layouts, cfg, cache)
}

func handleErrorTemplate(c fiber.Ctx, filePath string, code int, msg string, isPartial bool, layouts []string, cfg RouterConfig, cache ...*fileCache) error {
	c.Locals("errorCode", code)
	c.Locals("errorMessage", msg)

	res, err := processor.ProcessFile(filePath, c, cfg.AppConfig, cfg.Settings)
	if err != nil {
		if cfg.ErrorHandler != nil {
			return cfg.ErrorHandler(c, err)
		}
		return c.SendString(fmt.Sprintf("Error %d: %s", code, msg))
	}

	// Appliquer les layouts (sauf si partial)
	if !isPartial {
		for _, lPath := range layouts {
			c.Locals("content", res)
			c.Locals("errorCode", code)
			c.Locals("errorMessage", msg)
			ext := filepath.Ext(lPath)
			if ext == cfg.TemplateExt {
				res, err = processor.ProcessFile(lPath, c, cfg.AppConfig, cfg.Settings)
			} else if ext == ".js" {
				if len(cache) > 0 && cache[0] != nil {
					res, err = runAndCaptureJS(c, lPath, cfg, cache[0])
				}
			}
			if err != nil {
				return err
			}
		}
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(res)
}

// handleErrorJS exécute un handler JS d'erreur.
func handleErrorJS(c fiber.Ctx, filePath string, code int, msg string, isPartial bool, layouts []string, cfg RouterConfig, cache *fileCache) error {
	content, err := cache.ReadFile(filePath)
	if err != nil {
		return c.SendString(fmt.Sprintf("Error %d: %s", code, msg))
	}

	vm := processor.New(filepath.Dir(filePath), c, cfg.AppConfig)
	vm.Set("errorCode", code)
	vm.Set("errorMessage", msg)
	vm.Set("params", vm.NewObject()) // vide pour les error handlers

	if cfg.Settings != nil {
		settingsObj := vm.NewObject()
		for k, v := range cfg.Settings {
			settingsObj.Set(k, v)
		}
		vm.Set("settings", settingsObj)
	}

	jsRes, jsErr := vm.RunString(string(content))
	if jsErr != nil {
		return jsErr
	}

	res := ""

	// Si la réponse a été écrite via context.SendString()
	if body := c.Response().Body(); len(body) > 0 {
		c.Response().ResetBody()
		res = string(body)
	}

	// Buffer print()
	if outRes, runErr := vm.RunString("__output()"); runErr == nil {
		out := outRes.String()
		if out != "" && out != "undefined" {
			if res != "" {
				res += "\n"
			}
			res += out
		}
	}

	// Valeur de retour du script
	if res == "" && jsRes != nil && !jsRes.Equals(vm.ToValue(nil)) {
		exported := jsRes.Export()
		if exported != nil {
			res = fmt.Sprint(exported)
		}
	}

	// Appliquer les layouts (sauf si partial)
	if !isPartial {
		for _, lPath := range layouts {
			c.Locals("content", res)
			c.Locals("errorCode", code)
			c.Locals("errorMessage", msg)
			ext := filepath.Ext(lPath)
			if ext == cfg.TemplateExt {
				res, err = processor.ProcessFile(lPath, c, cfg.AppConfig, cfg.Settings)
			} else if ext == ".js" {
				res, err = runAndCaptureJS(c, lPath, cfg, cache)
			}
			if err != nil {
				return err
			}
		}
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(res)
}

// ==================== MODULE.EXPORTS SUPPORT ====================

// probeModuleExports exécute un fichier JS dans un VM léger (sans fiber.Ctx)
// et retourne la liste des méthodes HTTP trouvées dans module.exports.
//
// Exemple de fichier reconnu :
//
//	module.exports = {
//	    GET:    function() { ... },
//	    POST:   function() { ... },
//	    DELETE: function() { ... },
//	}
//
// Retourne nil si le fichier n'exporte pas d'objet ou si les clés ne sont pas
// des méthodes HTTP reconnues.
func probeModuleExports(filePath string) []string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	vm := newProbeVM()
	_, err = vm.RunString(string(content))
	if err != nil {
		return nil
	}

	exportsVal := vm.Get("__exports__")
	if exportsVal == nil || exportsVal.Export() == nil {
		return nil
	}

	obj := exportsVal.ToObject(vm.Runtime)
	if obj == nil {
		return nil
	}

	var methods []string
	for _, key := range obj.Keys() {
		upper := strings.ToUpper(key)
		if knownHTTPMethods[upper] {
			methods = append(methods, upper)
		}
	}
	return methods
}

// newProbeVM crée un VM goja minimal pour inspecter module.exports.
// N'expose pas fiber.Ctx — utilisé uniquement pour l'introspection au scan.
func newProbeVM() *processor.Processor {
	vm := processor.NewVM()
	vm.AttachGlobals()

	// Shim CommonJS module.exports
	moduleObj := vm.NewObject()
	exportsObj := vm.NewObject()
	moduleObj.Set("exports", exportsObj)
	vm.Set("module", moduleObj)
	vm.Set("exports", exportsObj)

	// Capturer le module.exports final après exécution du script
	// On utilise un getter pour lire module.exports après RunString
	vm.Set("__get_exports__", func() goja.Value {
		return moduleObj.Get("exports")
	})

	// Exécuter un shim pour exposer module.exports sous __exports__
	// après que le script ait pu reassigner module.exports
	_, _ = vm.RunString(`
		Object.defineProperty(typeof globalThis !== 'undefined' ? globalThis : this, '__exports__', {
			get: function() { return module.exports; },
			configurable: true,
		});
	`)

	// Fonctions no-op pour éviter les erreurs si le script utilise console.log etc.
	vm.Set("console", map[string]interface{}{
		"log":   func(...interface{}) {},
		"error": func(...interface{}) {},
		"warn":  func(...interface{}) {},
		"info":  func(...interface{}) {},
		"debug": func(...interface{}) {},
	})
	vm.Set("require", func(string) goja.Value { return goja.Undefined() })

	return vm
}

// handleJSExport exécute la fonction exportée sous la clé `method` dans module.exports.
func handleJSExport(c fiber.Ctx, filePath, method string, isPartial bool, layouts []string, cfg RouterConfig, cache *fileCache) error {
	res, err := runAndCaptureJSExport(c, filePath, method, cfg, cache)
	if err != nil {
		return err
	}

	// Appliquer les layouts (sauf si partial)
	if !isPartial {
		for _, lPath := range layouts {
			c.Locals("content", res)
			ext := filepath.Ext(lPath)
			switch ext {
			case cfg.TemplateExt:
				res, err = processor.ProcessFile(lPath, c, cfg.AppConfig, cfg.Settings)
			case ".js":
				res, err = runAndCaptureJS(c, lPath, cfg, cache)
			}
			if err != nil {
				return err
			}
		}
	}

	if len(c.Response().Body()) > 0 {
		return nil
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(res)
}

// runAndCaptureJSExport exécute une méthode exportée et retourne sa sortie.
func runAndCaptureJSExport(c fiber.Ctx, filePath, method string, cfg RouterConfig, cache *fileCache) (string, error) {
	content, err := cache.ReadFile(filePath)
	if err != nil {
		if cfg.ErrorHandler != nil {
			_ = cfg.ErrorHandler(c, err)
		}
		return "", err
	}

	vm := processor.New(filepath.Dir(filePath), c, cfg.AppConfig)

	// Shim CommonJS module/exports
	moduleObj := vm.NewObject()
	exportsObj := vm.NewObject()
	moduleObj.Set("exports", exportsObj)
	vm.Set("module", moduleObj)
	vm.Set("exports", exportsObj)

	// Injecter les settings
	if cfg.Settings != nil {
		settingsObj := vm.NewObject()
		for k, v := range cfg.Settings {
			settingsObj.Set(k, v)
		}
		vm.Set("settings", settingsObj)
	}

	// Construire l'objet params
	paramsObj := vm.NewObject()
	if params, ok := c.Locals("_fsrouter_params").(map[string]string); ok {
		for k, v := range params {
			paramsObj.Set(k, v)
		}
	}
	if catchall, ok := c.Locals("_fsrouter_catchall").(string); ok {
		paramsObj.Set("catchall", catchall)
	}
	vm.Set("params", paramsObj)

	// Exécuter le fichier pour peupler module.exports
	_, runErr := vm.RunString(string(content))
	if runErr != nil {
		if strings.Contains(runErr.Error(), "__FIBER_ERROR__") {
			return "", parseFiberError(runErr)
		}
		if cfg.ErrorHandler != nil {
			_ = cfg.ErrorHandler(c, runErr)
		}
		return "", runErr
	}

	exportsVal := moduleObj.Get("exports")
	if exportsVal == nil || exportsVal.Export() == nil {
		return "", fmt.Errorf("module.exports is empty")
	}
	exportsObject := exportsVal.ToObject(vm.Runtime)

	var handlerFn goja.Callable
	for _, key := range []string{method, "ANY"} {
		fnVal := exportsObject.Get(key)
		if fnVal != nil && !goja.IsUndefined(fnVal) && !goja.IsNull(fnVal) {
			if fn, ok := goja.AssertFunction(fnVal); ok {
				handlerFn = fn
				break
			}
		}
	}

	if handlerFn == nil {
		return "", fiber.ErrMethodNotAllowed
	}

	settingsArg := vm.Get("settings")
	if settingsArg == nil {
		settingsArg = goja.Undefined()
	}

	res, callErr := handlerFn(
		goja.Undefined(),
		vm.Get("context"),
		vm.Get("params"),
		settingsArg,
	)
	if callErr != nil {
		if strings.Contains(callErr.Error(), "__FIBER_ERROR__") {
			return "", parseFiberError(callErr)
		}
		if cfg.ErrorHandler != nil {
			_ = cfg.ErrorHandler(c, callErr)
		}
		return "", callErr
	}

	// Si la réponse est déjà écrite
	if body := c.Response().Body(); len(body) > 0 {
		c.Response().ResetBody()
		return string(body), nil
	}

	if outRes, runErr2 := vm.RunString("__output()"); runErr2 == nil {
		out := outRes.String()
		if out != "" && out != "undefined" {
			return out, nil
		}
	}

	if res != nil && !goja.IsUndefined(res) && !goja.IsNull(res) {
		exported := res.Export()
		if exported != nil {
			return fmt.Sprint(exported), nil
		}
	}

	return "", nil
}

// parseFiberError extrait le code et le message depuis une erreur __FIBER_ERROR__.
func parseFiberError(err error) error {
	parts := strings.Split(err.Error(), "__FIBER_ERROR__")
	if len(parts) == 2 {
		fields := strings.Fields(parts[1])
		if len(fields) > 0 {
			code := 500
			fmt.Sscanf(fields[0], "%d", &code)
			msg := strings.TrimSpace(parts[0])
			if msg == "" {
				msg = fiber.ErrInternalServerError.Message
			}
			return fiber.NewError(code, msg)
		}
	}
	return err
}

// ==================== DEBUG ====================

// FsRouterDebug retourne un string listant toutes les routes scannées.
// Utile au démarrage pour vérifier le mapping filesystem → URL.
func FsRouterDebug(cfgs ...RouterConfig) string {
	cfg := RouterConfig{}
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
	cfg.normalize()

	layoutMap := make(map[string][]string)
	routes, middlewareMap, notFoundHandlers, errorHandlers, _, _, _ := scanDirectory(cfg.Root, cfg.Root, layoutMap, cfg)

	sort.SliceStable(routes, func(i, j int) bool {
		return routePriority(routes[i]) > routePriority(routes[j])
	})

	var sb strings.Builder
	serveFilesStr := "disabled"
	if cfg.ServeFiles {
		serveFilesStr = "enabled"
	}
	sb.WriteString(fmt.Sprintf("FsRouter — root: %s  [ServeFiles:%s, Exclude patterns:%d]\n\n",
		cfg.Root, serveFilesStr, len(cfg.Exclude)))

	var handlerRoutes, staticRoutes []routeEntry
	for _, r := range routes {
		if r.isStatic {
			staticRoutes = append(staticRoutes, r)
		} else {
			handlerRoutes = append(handlerRoutes, r)
		}
	}

	sb.WriteString("Routes:\n")
	for _, r := range handlerRoutes {
		rel, _ := filepath.Rel(cfg.Root, r.filePath)
		dynamic := ""
		if r.isCatchAll {
			dynamic = " [catch-all]"
		} else if r.isDynamic {
			dynamic = " [dynamic]"
		}
		mwCount := ""
		if len(r.middlewares) > 0 {
			mwCount = fmt.Sprintf(" (mw:%d)", len(r.middlewares))
		}
		kind := ""
		switch {
		case r.isExport:
			kind = " [export]"
		case r.isTemplate:
			kind = " [template]"
		case r.isJS:
			kind = " [js]"
		}
		sb.WriteString(fmt.Sprintf("  %-8s %-30s ← %s%s%s%s\n",
			r.method, r.urlPattern, rel, dynamic, kind, mwCount))
	}

	if len(staticRoutes) > 0 {
		sb.WriteString("\nStatic files:\n")
		for _, r := range staticRoutes {
			rel, _ := filepath.Rel(cfg.Root, r.filePath)
			sb.WriteString(fmt.Sprintf("  %-8s %-30s ← %s\n", r.method, r.urlPattern, rel))
		}
	}

	if len(middlewareMap) > 0 {
		sb.WriteString("\nMiddlewares:\n")
		var keys []string
		for k := range middlewareMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			rel, _ := filepath.Rel(cfg.Root, middlewareMap[k])
			sb.WriteString(fmt.Sprintf("  %-30s ← %s\n", k, rel))
		}
	}

	if len(notFoundHandlers) > 0 {
		sb.WriteString("\n404 handlers:\n")
		var keys []string
		for k := range notFoundHandlers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			rel, _ := filepath.Rel(cfg.Root, notFoundHandlers[k])
			sb.WriteString(fmt.Sprintf("  %-30s ← %s\n", k, rel))
		}
	}

	if len(errorHandlers) > 0 {
		sb.WriteString("\nError handlers:\n")
		var urlDirs []string
		for d := range errorHandlers {
			urlDirs = append(urlDirs, d)
		}
		sort.Strings(urlDirs)
		for _, d := range urlDirs {
			codes := errorHandlers[d]
			var codeKeys []string
			for ck := range codes {
				codeKeys = append(codeKeys, ck)
			}
			sort.Strings(codeKeys)
			for _, ck := range codeKeys {
				rel, _ := filepath.Rel(cfg.Root, codes[ck])
				label := d + " [" + ck + "]"
				sb.WriteString(fmt.Sprintf("  %-36s ← %s\n", label, rel))
			}
		}
	}

	if len(layoutMap) > 0 {
		sb.WriteString("\nLayouts:\n")
		var keys []string
		for k := range layoutMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, lPath := range layoutMap[k] {
				rel, _ := filepath.Rel(cfg.Root, lPath)
				sb.WriteString(fmt.Sprintf("  %-30s ← %s\n", k, rel))
			}
		}
	}

	return sb.String()
}

// collectLayouts collecte les chemins des fichiers _layout.<ext> applicables.
// Retourne les layouts du plus profond au plus superficiel.
func collectLayouts(baseDir, entryDir string, layoutMap map[string][]string) []string {
	var layouts []string
	relDir, _ := filepath.Rel(baseDir, entryDir)
	urlDir := fsPathToURL(relDir)

	segments := strings.Split(strings.TrimPrefix(urlDir, "/"), "/")
	if urlDir == "/" {
		segments = []string{""}
	}

	for depth := len(segments); depth >= 0; depth-- {
		var prefix string
		if depth == 0 {
			prefix = "/"
		} else {
			prefix = "/" + strings.Join(segments[:depth], "/")
			if prefix == "//" {
				prefix = "/"
			}
		}

		if depth == 0 && len(segments) > 0 && segments[0] == "" {
			// Déjà traité par depth=1 si c'est la racine
			continue
		}

		if ls, ok := layoutMap[prefix]; ok {
			layouts = append(layouts, ls...)
		}
	}
	// Supprimer les doublons éventuels (sécurité)
	unique := make([]string, 0, len(layouts))
	seen := make(map[string]bool)
	for _, l := range layouts {
		if !seen[l] {
			unique = append(unique, l)
			seen[l] = true
		}
	}
	return unique
}

// ==================== CRON SCHEDULER ====================

// parseCronHeader extrait l'expression cron depuis la première ligne du fichier :
// # CRON * * * * *
func parseCronHeader(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		line := scanner.Text()
		re := regexp.MustCompile(`(?i)^#\s*CRON\s+(.+)$`)
		if matches := re.FindStringSubmatch(line); len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}
	return ""
}
