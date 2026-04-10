package config

import (
	"fmt"
	"log"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Les champs "statiques" ne peuvent pas être changés à chaud.
// Ils sont identifiés par le symbole '#' dans les tags `flag` de AppConfig.
// Un changement de ces champs déclenche un avertissement et est ignoré.

// Champs hot-reloadables — tout ce qui n'est pas dans staticFields.
// Ces champs sont pris en compte immédiatement sans redémarrage :
//
//   - DirListing, AutoIndex, IndexFile
//   - HtmxURL, NoHtmx, InjectHTML, TemplateExt, NoTemplate
//   - Gzip, Brotli, Deflate
//   - Silent, Stdout, Stderr
//   - CORS, CacheTime
//   - ProxyURL
//   - Robots, RobotsFile
//   - ReadTimeout, WriteTimeout, IdleTimeout  (pris en compte sur les nouvelles connexions)
//   - Find, Match
//   - BindFile

// ChangeType décrit le type d'un changement détecté lors d'un reload.
type ChangeType int

const (
	ChangeHotReload ChangeType = iota // champ dynamique — appliqué immédiatement
	ChangeStatic                      // champ statique — ignoré, redémarrage requis
)

// FieldChange décrit un champ qui a changé entre deux configs.
type FieldChange struct {
	Field   string // nom du champ Go (ex: "Port")
	OldVal  any    // ancienne valeur
	NewVal  any    // nouvelle valeur (ignorée si Type == ChangeStatic)
	Type    ChangeType
	Warning string // message d'avertissement si Type == ChangeStatic
}

// Watcher observe les fichiers de config et déclenche un rechargement
// automatique à chaque modification. Thread-safe via atomic pointer.
//
// Seuls les champs dynamiques sont appliqués à chaud.
// Les champs statiques (Port, Address, Socket, HTTPS, Cert, Key...)
// produisent un avertissement dans les logs — la valeur initiale
// reste active jusqu'au prochain redémarrage.
//
// Usage :
//
//	w, cfg, err := LoadConfigWithWatcher()
//	defer w.Close()
//
//	// Callback appelé après chaque rechargement réussi.
//	// changes liste uniquement les champs dont la valeur a changé.
//	w.OnChange(func(next *AppConfig, changes []FieldChange) {
//	    for _, c := range changes {
//	        if c.Type == config.ChangeStatic {
//	            log.Printf("WARN: %s — redémarrage requis", c.Warning)
//	        }
//	    }
//	    // Appliquer next pour les champs dynamiques (ex: mettre à jour
//	    // les middlewares de compression, les timeouts sur nouvelles connexions...)
//	})
type Watcher struct {
	ptr      atomic.Pointer[AppConfig]
	initial  AppConfig // config de démarrage — jamais modifiée
	watcher  *fsnotify.Watcher
	loadFn   func() (*AppConfig, error)
	mu       sync.Mutex
	handlers []func(*AppConfig, []FieldChange)
	done     chan struct{}
}

// NewWatcher crée un Watcher et charge la config initiale.
// loadFn est appelée à chaque modification détectée.
func NewWatcher(loadFn func() (*AppConfig, error), conf ...*AppConfig) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		watcher: fw,
		loadFn:  loadFn,
		done:    make(chan struct{}),
	}

	if len(conf) > 0 && conf[0] != nil {
		w.initial = *conf[0]
		w.ptr.Store(conf[0])
	} else {
		// Chargement initial
		cfg, err := loadFn()
		if err != nil {
			fw.Close()
			return nil, err
		}
		if err := Validate(cfg); err != nil {
			fw.Close()
			return nil, err
		}

		w.initial = *cfg
		w.ptr.Store(cfg)
	}
	go w.watch()
	return w, nil
}

// Config retourne un pointeur vers la config courante (lecture atomique, zéro lock).
// Les champs statiques reflètent toujours la valeur initiale même si le fichier
// a été modifié — voir FieldChange.Type == ChangeStatic.
func (w *Watcher) Config() *AppConfig {
	return w.ptr.Load()
}

// OnChange enregistre un callback appelé après chaque rechargement réussi.
// Le paramètre changes liste uniquement les champs dont la valeur a effectivement
// changé, avec leur type (ChangeHotReload ou ChangeStatic).
// Plusieurs callbacks peuvent être enregistrés ; ils sont appelés dans l'ordre.
func (w *Watcher) OnChange(fn func(*AppConfig, []FieldChange)) {
	w.mu.Lock()
	w.handlers = append(w.handlers, fn)
	w.mu.Unlock()
}

// Watch ajoute des fichiers à surveiller.
func (w *Watcher) Watch(paths ...string) error {
	for _, p := range paths {
		resolved, err := ResolveConfigFile(p)
		if err != nil {
			resolved, err = ResolveEnvFile(p)
			if err != nil {
				continue
			}
		}
		abs, err := filepath.Abs(resolved)
		if err != nil {
			continue
		}
		if err := w.watcher.Add(abs); err != nil {
			return err
		}
	}
	return nil
}

// Close arrête le watcher et libère les ressources.
func (w *Watcher) Close() error {
	close(w.done)
	return w.watcher.Close()
}

// StaticFieldsInfo retourne une map nom→description de tous les noms longs des flags
// qui sont marqués comme statiques. Utile pour l'affichage ou les logs.
func StaticFieldsInfo() map[string]string {
	out := make(map[string]string)
	rt := reflect.TypeOf(AppConfig{})
	for i := 0; i < rt.NumField(); i++ {
		meta, ok := parseFlagTag(rt.Field(i))
		if ok && meta.isStatic {
			out[rt.Field(i).Name] = fmt.Sprintf("--%s : ce paramètre nécessite un redémarrage", meta.long)
		}
	}
	return out
}

// watch est la goroutine principale de surveillance.
func (w *Watcher) watch() {
	var debounce *time.Timer
	const debounceDuration = 100 * time.Millisecond

	for {
		select {
		case <-w.done:
			if debounce != nil {
				debounce.Stop()
			}
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(debounceDuration, func() {
					if err := w.reload(); err != nil {
						log.Printf("config: reload error: %v", err)
					}
				})
			}

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("config: watcher error: %v", err)
		}
	}
}

// reload recharge la config, applique les champs dynamiques, journalise
// les tentatives de modification des champs statiques, et notifie les handlers.
func (w *Watcher) reload() error {
	next, err := w.loadFn()
	if err != nil {
		return err
	}
	if err := Validate(next); err != nil {
		return err
	}

	// Diff entre la config courante et la nouvelle
	current := w.ptr.Load()
	changes := diffConfigs(current, next)

	// Pour les champs statiques : annuler le changement en réappliquant
	// la valeur initiale, et logguer un avertissement.
	applied := *next // On travaille sur une copie pour le hot-reload
	av := reflect.ValueOf(&applied).Elem()
	iv := reflect.ValueOf(&w.initial).Elem()

	for _, ch := range changes {
		if ch.Type == ChangeStatic {
			// Remettre la valeur initiale dans la config appliquée
			av.FieldByName(ch.Field).Set(iv.FieldByName(ch.Field))
			log.Printf("config: WARN: changement ignoré — %s", ch.Warning)
		}
	}

	// Swap atomique avec la config corrigée
	w.ptr.Store(&applied)

	// Notifier les handlers avec la liste des changements réels
	w.mu.Lock()
	handlers := w.handlers
	w.mu.Unlock()

	for _, fn := range handlers {
		fn(&applied, changes)
	}

	return nil
}

// diffConfigs retourne la liste des champs dont la valeur a changé entre
// current et next, avec leur type (ChangeHotReload ou ChangeStatic).
func diffConfigs(current, next *AppConfig) []FieldChange {
	var changes []FieldChange

	cv := reflect.ValueOf(current).Elem()
	nv := reflect.ValueOf(next).Elem()
	rt := cv.Type()

	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		fname := sf.Name
		cval := cv.Field(i).Interface()
		nval := nv.Field(i).Interface()

		if reflect.DeepEqual(cval, nval) {
			continue
		}

		ch := FieldChange{
			Field:  fname,
			OldVal: cval,
			NewVal: nval,
		}

		// Détection via les tags (réflexion)
		meta, ok := parseFlagTag(sf)
		if ok && meta.isStatic {
			ch.Type = ChangeStatic
			ch.Warning = fmt.Sprintf("--%s : changement ignoré, redémarrage requis (actuel: %v, ignoré: %v)", meta.long, cval, nval)
		} else {
			ch.Type = ChangeHotReload
		}
		changes = append(changes, ch)
	}

	return changes
}
