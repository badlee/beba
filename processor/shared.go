package processor

// shared.go — Partage de valeurs Go entre plusieurs runtimes goja via ES6 Proxy.
//
// # Types supportés
//
//   Scalaires  : bool, string, rune (int32), int/int8/int16/int32/int64,
//                uint/uint8/uint16/uint32/uint64, float32/float64
//   Composites : struct, *struct (pointeur déréférencé),
//                slice/array ([]T et [N]T),
//                map[K]V  (K = string ou entier)
//   Nil        : interface nil / pointeur nil → null JS
//
// # Accès JavaScript
//
//   // Enregistrement Go
//   processor.RegisterGlobal("Config", &myConfig)
//
//   // Dans le script JS (variable "config")
//   config.host                  // lecture champ
//   config.host = "localhost"    // écriture champ → modifie le Go
//   Object.keys(config)          // ["host", "port", "tls", ...]
//
//   config.tags[0]               // lecture index slice
//   config.tags[1] = "v2"        // écriture index
//   config.tags.length           // longueur
//
//   config.env["KEY"]            // lecture clé map
//   config.env["KEY"] = "val"    // écriture clé
//   delete config.env["OLD"]     // suppression clé
//
//   config.tls.certFile          // struct imbriquée (proxy récursif)
//
// # Subscribe / Unsubscribe
//
//   var id = config.subscribe(function(ev) {
//       // ev.path     — "tls.certFile" / "tags.0" / "env.KEY"
//       // ev.oldValue — ancienne valeur Go
//       // ev.newValue — nouvelle valeur
//       print("changed: " + ev.path)
//   })
//   config.unsubscribe(id)
//
// # Cohérence live
//
//   Toutes les lectures passent par un "accessor" (func() reflect.Value) qui
//   remonte depuis le pointeur racine à chaque accès. Les modifications
//   écrivent directement dans la mémoire Go via reflect.Value.CanSet().

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/dop251/goja"
)

// ==================== PUBLIC API ====================
type ObjectInitialiser interface {
	ToJSObject(vm *goja.Runtime) goja.Value
}

// ChangeEvent décrit une modification effectuée depuis une VM JS.
type ChangeEvent struct {
	// Path — chemin depuis la racine, séparé par '.'
	// Exemples : "host", "tls.certFile", "tags.0", "env.KEY"
	Path string
	// OldValue — valeur Go avant modification (nil si création de clé map)
	OldValue interface{}
	// NewValue — valeur Go après modification (nil si suppression de clé map)
	NewValue interface{}
}

// SharedObject partage une valeur Go avec plusieurs runtimes goja.
type SharedObject struct {
	name string

	// mu protège data contre les lectures/écritures concurrentes entre VMs.
	// Les modifications via reflect.Value.Set() sont également sous ce verrou.
	mu   sync.RWMutex
	data interface{} // DOIT être un pointeur (*struct, *slice, *map, *scalar)

	subsMu      sync.RWMutex
	subscribers []sharedSub
	nextSubID   atomic.Uint64
}

type sharedSub struct {
	id uint64
	fn func(ChangeEvent)
}

var (
	sharedObjectsMu sync.RWMutex
	sharedObjects   = make(map[string]*SharedObject)
)

// RegisterGlobal enregistre une valeur Go à partager entre toutes les runtimes.
// data DOIT être un pointeur vers la valeur à partager.
//
//	processor.RegisterGlobal("Config", &myConfig)        // *Config
//	processor.RegisterGlobal("Tags",   &myTags)          // *[]string
//	processor.RegisterGlobal("Config", &myConfig, true)  // ignore si déjà enregistré
func RegisterGlobal(name string, data interface{}, ignoreIfExist ...bool) {
	sharedObjectsMu.Lock()
	defer sharedObjectsMu.Unlock()
	if len(ignoreIfExist) > 0 && ignoreIfExist[0] {
		if _, ok := sharedObjects[name]; ok {
			return
		}
	}
	sharedObjects[name] = &SharedObject{name: name, data: data}
}

// HasGlobal vérifie si un SharedObject est enregistré globalement.
//
//	processor.HasGlobal("Config")
func HasGlobal(name string) bool {
	sharedObjectsMu.RLock()
	defer sharedObjectsMu.RUnlock()
	_, ok := sharedObjects[name]
	return ok
}

// UnregisterGlobal retire un SharedObject enregistré globalement.
//
//	processor.UnregisterGlobal("Config")
func UnregisterGlobal(name string) {
	sharedObjectsMu.Lock()
	delete(sharedObjects, name)
	sharedObjectsMu.Unlock()
}

// AttachGlobals installe tous les SharedObjects dans la VM goja.
// À appeler dans processor.New() avant tout RunString.
func AttachGlobals(vm any) {
	if p, ok := vm.(*Processor); ok {
		p.AttachGlobals()
	} else if r, ok := vm.(*goja.Runtime); ok {
		(&Processor{Runtime: r}).AttachGlobals()
	}
}

// Register enregistre une valeur Go à partager avec une seule VM goja.
// Contrairement à RegisterGlobal, la valeur n'est PAS ajoutée au registre global
// et ne sera pas visible dans les autres VMs créées ultérieurement.
// data DOIT être un pointeur vers la valeur à partager.
//
// Enregistre un SharedObject directement sur une VM spécifique (usage manuel).
func Register(vm any, name string, data interface{}) {
	if p, ok := vm.(*Processor); ok {
		p.Register(name, data)
	} else if r, ok := vm.(*goja.Runtime); ok {
		(&Processor{Runtime: r}).Register(name, data)
	}
}

// Register enregistre une valeur Go à partager avec une seule VM goja.
// Contrairement à RegisterGlobal, la valeur n'est PAS ajoutée au registre global
// et ne sera pas visible dans les autres VMs créées ultérieurement.
// data DOIT être un pointeur vers la valeur à partager.
//
// Enregistre un SharedObject directement sur une VM spécifique (usage manuel).
func (vm *Processor) Register(name string, data interface{}) {
	so := &SharedObject{name: name, data: data}
	_ = so.attach(vm.Runtime)
}

// AttachGlobals installe tous les SharedObjects dans la VM goja.
// À appeler dans processor.New() avant tout RunString.
func (vm *Processor) AttachGlobals() {
	sharedObjectsMu.RLock()
	defer sharedObjectsMu.RUnlock()
	for _, so := range sharedObjects {
		if oi, ok := so.data.(ObjectInitialiser); ok {
			o := oi.ToJSObject(vm.Runtime)
			vm.Set(so.name, o)
		} else {
			_ = so.attach(vm.Runtime)
		}
	}
}

// Subscribe enregistre un listener Go appelé à chaque modification depuis JS.
func (so *SharedObject) Subscribe(fn func(ChangeEvent)) uint64 {
	id := so.nextSubID.Add(1)
	so.subsMu.Lock()
	so.subscribers = append(so.subscribers, sharedSub{id: id, fn: fn})
	so.subsMu.Unlock()
	return id
}

// Unsubscribe supprime le listener identifié par id.
func (so *SharedObject) Unsubscribe(id uint64) {
	so.subsMu.Lock()
	out := so.subscribers[:0]
	for _, s := range so.subscribers {
		if s.id != id {
			out = append(out, s)
		}
	}
	so.subscribers = out
	so.subsMu.Unlock()
}

// ==================== ATTACH ====================

// attach installe le proxy racine dans la VM sous le nom camelCase,
// avec subscribe() et unsubscribe() disponibles directement sur l'objet.
//
// Architecture :
//
//	Un seul Proxy dont les traps délèguent vers le proxy du struct Go,
//	sauf pour "subscribe" et "unsubscribe" qui sont gérés localement.
//	Les fonctions JS subscribe/unsubscribe sont des goja.Value capturées
//	dans des variables — pas besoin d'un objet wrapper intermédiaire.
func (so *SharedObject) attach(vm *goja.Runtime) error {
	// Accessor racine — relit toujours depuis le pointeur original
	rootAccessor := func() reflect.Value {
		return reflect.ValueOf(so.data)
	}

	// Proxy du nœud racine (struct/slice/map Go)
	innerProxy := so.buildProxy(vm, rootAccessor, "")

	// subscribe et unsubscribe comme goja.Value capturées
	subscribeFn := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		fn, ok := goja.AssertFunction(call.Arguments[0])
		if !ok {
			return goja.Undefined()
		}
		id := so.Subscribe(func(ev ChangeEvent) {
			obj := vm.NewObject()
			_ = obj.Set("path", ev.Path)
			_ = obj.Set("oldValue", vm.ToValue(ev.OldValue))
			_ = obj.Set("newValue", vm.ToValue(ev.NewValue))
			_, _ = fn(goja.Undefined(), obj)
		})
		return vm.ToValue(id)
	})

	unsubscribeFn := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		id := uint64(call.Arguments[0].ToInteger())
		so.Unsubscribe(id)
		return goja.Undefined()
	})

	// Proxy unique : délègue vers innerProxy sauf subscribe/unsubscribe
	outerTarget := vm.NewObject()
	outerProxyRaw := vm.NewProxy(outerTarget, &goja.ProxyTrapConfig{

		Get: func(target *goja.Object, property string, receiver goja.Value) goja.Value {
			switch property {
			case "subscribe":
				return subscribeFn
			case "unsubscribe":
				return unsubscribeFn
			}
			return innerProxy.Get(property)
		},

		Set: func(target *goja.Object, property string, value goja.Value, receiver goja.Value) bool {
			if property == "subscribe" || property == "unsubscribe" {
				return false // non modifiable
			}
			_ = innerProxy.Set(property, value)
			return true
		},

		Has: func(target *goja.Object, property string) bool {
			if property == "subscribe" || property == "unsubscribe" {
				return true
			}
			v := innerProxy.Get(property)
			return v != nil && !goja.IsUndefined(v)
		},

		DeleteProperty: func(target *goja.Object, property string) bool {
			if property == "subscribe" || property == "unsubscribe" {
				return false
			}
			_ = innerProxy.Delete(property)
			return true
		},

		OwnKeys: makeOwnKeysTrap(vm, innerProxy, "subscribe", "unsubscribe"),

		GetOwnPropertyDescriptor: func(target *goja.Object, prop string) goja.PropertyDescriptor {
			switch prop {
			case "subscribe", "unsubscribe":
				return goja.PropertyDescriptor{
					Writable:     goja.FLAG_FALSE,
					Enumerable:   goja.FLAG_TRUE,
					Configurable: goja.FLAG_TRUE,
				}
			}
			// Déléguer au proxy interne via Reflect.getOwnPropertyDescriptor en JS
			v := innerProxy.Get(prop)
			if v == nil || goja.IsUndefined(v) {
				return goja.PropertyDescriptor{}
			}
			return goja.PropertyDescriptor{
				Value:        v,
				Writable:     goja.FLAG_TRUE,
				Enumerable:   goja.FLAG_TRUE,
				Configurable: goja.FLAG_TRUE,
			}
		},

		IsExtensible:      func(target *goja.Object) bool { return true },
		PreventExtensions: func(target *goja.Object) bool { return false },
		GetPrototypeOf:    func(target *goja.Object) *goja.Object { return nil },
		SetPrototypeOf:    func(target *goja.Object, p *goja.Object) bool { return false },
	})

	outerProxy := vm.ToValue(outerProxyRaw).(*goja.Object)
	vm.Set(pascalToCamel(so.name), outerProxy)
	return nil
}

func (so *SharedObject) wrapMethod(vm *goja.Runtime, method reflect.Value) goja.Value {
	return vm.ToValue(func(call goja.FunctionCall) goja.Value {
		methodType := method.Type()
		numIn := methodType.NumIn()
		isVariadic := methodType.IsVariadic()

		var goArgs []reflect.Value

		if isVariadic {
			// Pour les méthodes variadiques, on ne remplit PAS le dernier arg (slice)
			// si aucun argument JS correspondant n'est fourni.
			requiredArgs := numIn - 1
			goArgs = make([]reflect.Value, 0, len(call.Arguments))

			// Arguments non-variadiques
			for i := 0; i < requiredArgs; i++ {
				argType := methodType.In(i)
				if i < len(call.Arguments) {
					val, err := jsToGo(vm, call.Arguments[i], argType)
					if err != nil {
						panic(vm.ToValue(fmt.Sprintf("argument %d: %v", i, err)))
					}
					goArgs = append(goArgs, reflect.ValueOf(val))
				} else {
					goArgs = append(goArgs, reflect.Zero(argType))
				}
			}

			// Arguments variadiques (chaque arg JS supplémentaire = un élément)
			variadicElemType := methodType.In(numIn - 1).Elem()
			for i := requiredArgs; i < len(call.Arguments); i++ {
				val, err := jsToGo(vm, call.Arguments[i], variadicElemType)
				if err != nil {
					panic(vm.ToValue(fmt.Sprintf("argument %d: %v", i, err)))
				}
				goArgs = append(goArgs, reflect.ValueOf(val))
			}
		} else {
			goArgs = make([]reflect.Value, numIn)
			for i := 0; i < numIn; i++ {
				argType := methodType.In(i)
				if i < len(call.Arguments) {
					val, err := jsToGo(vm, call.Arguments[i], argType)
					if err != nil {
						panic(vm.ToValue(fmt.Sprintf("argument %d: %v", i, err)))
					}
					goArgs[i] = reflect.ValueOf(val)
				} else {
					goArgs[i] = reflect.Zero(argType)
				}
			}
		}

		results := method.Call(goArgs)
		if len(results) == 0 {
			return goja.Undefined()
		}

		resVal := results[0]
		// Support ToJSObject
		if resVal.CanInterface() {
			if jsConverter, ok := resVal.Interface().(interface {
				ToJSObject(*goja.Runtime) goja.Value
			}); ok {
				return jsConverter.ToJSObject(vm)
			}
		}

		// Erreur en deuxième retour ?
		if len(results) > 1 {
			if err, ok := results[1].Interface().(error); ok && err != nil {
				panic(vm.ToValue(err.Error()))
			}
		}

		// Proxying récursif via reflectToJS
		staticAcc := func() reflect.Value { return resVal }
		return so.reflectToJS(vm, staticAcc, resVal, resVal.Type(), "")
	})
}

// makeOwnKeysTrap construit la trap OwnKeys qui fusionne les clés du proxy interne
// avec les clés extra (subscribe, unsubscribe).
func makeOwnKeysTrap(vm *goja.Runtime, inner *goja.Object, extras ...string) func(*goja.Object) *goja.Object {
	return func(target *goja.Object) *goja.Object {
		// Récupérer les clés du proxy interne via sa propre OwnKeys
		arr := vm.NewArray()
		i := 0

		// Appeler Reflect.ownKeys sur le proxy interne depuis JS
		reflectObj := vm.GlobalObject().Get("Reflect")
		if reflectObj != nil && !goja.IsUndefined(reflectObj) {
			ownKeysFn, _ := goja.AssertFunction(reflectObj.ToObject(vm).Get("ownKeys"))
			if ownKeysFn != nil {
				result, err := ownKeysFn(goja.Undefined(), inner)
				if err == nil && result != nil {
					if resultObj, ok := result.(*goja.Object); ok {
						lengthVal := resultObj.Get("length")
						if lengthVal != nil {
							n := int(lengthVal.ToInteger())
							for j := 0; j < n; j++ {
								k := resultObj.Get(strconv.Itoa(j))
								if k != nil && !goja.IsUndefined(k) {
									arr.Set(strconv.Itoa(i), k.String())
									i++
								}
							}
						}
					}
				}
			}
		}

		for _, extra := range extras {
			arr.Set(strconv.Itoa(i), extra)
			i++
		}
		return arr
	}
}

// proxyAsObject crée un ES6 Proxy et retourne le *goja.Object correspondant.
// vm.NewProxy retourne goja.Proxy (struct) ; vm.ToValue le convertit en goja.Value
// dont la valeur concrète est *goja.Object.
func proxyAsObject(vm *goja.Runtime, target *goja.Object, cfg *goja.ProxyTrapConfig) *goja.Object {
	return vm.ToValue(vm.NewProxy(target, cfg)).(*goja.Object)
}

// ==================== CORE : buildProxy ====================

// accessor est une fonction qui retourne le reflect.Value LIVE de la valeur
// (en relisant depuis le pointeur racine ou le champ parent à chaque appel).
type accessor func() reflect.Value

// buildProxy crée le proxy goja adapté au type retourné par acc().
// Cette fonction est appelée récursivement pour les composites imbriqués.
func (so *SharedObject) buildProxy(vm *goja.Runtime, acc accessor, path string) *goja.Object {
	rv := liveValue(acc)

	switch rv.Kind() {
	case reflect.Struct:
		return so.buildStructProxy(vm, acc, path)
	case reflect.Slice, reflect.Array:
		return so.buildSliceProxy(vm, acc, path)
	case reflect.Map:
		return so.buildMapProxy(vm, acc, path)
	default:
		// Scalaire à la racine (rare) — proxy opaque autour d'un objet vide
		return proxyAsObject(vm, vm.NewObject(), &goja.ProxyTrapConfig{
			IsExtensible:      func(target *goja.Object) bool { return false },
			PreventExtensions: func(target *goja.Object) bool { return true },
			GetPrototypeOf:    func(target *goja.Object) *goja.Object { return nil },
			SetPrototypeOf:    func(target *goja.Object, p *goja.Object) bool { return false },
		})
	}
}

// liveValue déréférence l'accessor jusqu'à obtenir la valeur concrète.
func liveValue(acc accessor) reflect.Value {
	rv := acc()
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return reflect.Value{}
		}
		rv = rv.Elem()
	}
	return rv
}

// ==================== STRUCT PROXY ====================

func (so *SharedObject) buildStructProxy(vm *goja.Runtime, acc accessor, path string) *goja.Object {
	target := vm.NewObject()

	return proxyAsObject(vm, target, &goja.ProxyTrapConfig{

		Get: func(target *goja.Object, property string, receiver goja.Value) goja.Value {
			rv := liveValue(acc)
			if !rv.IsValid() {
				return goja.Undefined()
			}
			fv, ft, ok := structFieldByName(rv, property)
			if ok {
				childPath := joinPath(path, property)
				return so.reflectToJS(vm, fieldAccessor(acc, property), fv, ft, childPath)
			}
			// Repli : Vérifier si c'est une méthode (en priorité sur le pointeur original)
			methodName := camelToPascal(property)
			ptr := acc()
			if method := ptr.MethodByName(methodName); method.IsValid() {
				// log.Printf("DEBUG: Found method %s on ptr %T", methodName, ptr.Interface())
				return so.wrapMethod(vm, method)
			}
			// Si c'est un pointeur vers interface ou autre déréférence possible
			if ptr.Kind() == reflect.Ptr || ptr.Kind() == reflect.Interface {
				if !ptr.IsNil() {
					if method := ptr.Elem().MethodByName(methodName); method.IsValid() {
						// log.Printf("DEBUG: Found method %s on ptr.Elem %T", methodName, ptr.Elem().Interface())
						return so.wrapMethod(vm, method)
					}
				}
			}
			// Et enfin sur la valeur concrète (déjà adressable ou non)
			if method := rv.MethodByName(methodName); method.IsValid() {
				// log.Printf("DEBUG: Found method %s on concrete rv %T", methodName, rv.Interface())
				return so.wrapMethod(vm, method)
			}
			if rv.CanAddr() {
				if method := rv.Addr().MethodByName(methodName); method.IsValid() {
					// log.Printf("DEBUG: Found method %s on rv.Addr %T", methodName, rv.Addr().Interface())
					return so.wrapMethod(vm, method)
				}
			}

			return goja.Undefined()
		},

		Set: func(target *goja.Object, property string, value goja.Value, receiver goja.Value) bool {
			so.mu.Lock()
			defer so.mu.Unlock()

			rv := liveValue(acc)
			if !rv.IsValid() {
				return false
			}
			fv, ft, ok := structFieldByName(rv, property)
			if !ok || !fv.CanSet() {
				return false
			}

			old := safeInterface(fv)
			newGo, err := jsToGo(vm, value, ft)
			if err != nil {
				return false
			}
			nv := reflect.ValueOf(newGo)
			if !nv.Type().AssignableTo(ft) {
				if !nv.Type().ConvertibleTo(ft) {
					return false
				}
				nv = nv.Convert(ft)
			}
			fv.Set(nv)
			so.notify(ChangeEvent{Path: joinPath(path, property), OldValue: old, NewValue: newGo})
			return true
		},

		Has: func(target *goja.Object, property string) bool {
			rv := liveValue(acc)
			if !rv.IsValid() {
				return false
			}
			_, _, ok := structFieldByName(rv, property)
			if ok {
				return true
			}
			methodName := camelToPascal(property)
			if rv.MethodByName(methodName).IsValid() {
				return true
			}
			if rv.CanAddr() && rv.Addr().MethodByName(methodName).IsValid() {
				return true
			}
			return false
		},

		OwnKeys: func(target *goja.Object) *goja.Object {
			rv := liveValue(acc)
			return structOwnKeys(vm, rv)
		},

		GetOwnPropertyDescriptor: func(target *goja.Object, prop string) goja.PropertyDescriptor {
			rv := liveValue(acc)
			if !rv.IsValid() {
				return goja.PropertyDescriptor{}
			}
			_, _, ok := structFieldByName(rv, prop)
			if ok {
				return writableDescriptor()
			}
			methodName := camelToPascal(prop)
			var method reflect.Value
			if method = rv.MethodByName(methodName); method.IsValid() {
				return goja.PropertyDescriptor{
					Value:        vm.ToValue(method.Interface()),
					Writable:     goja.FLAG_FALSE,
					Enumerable:   goja.FLAG_TRUE,
					Configurable: goja.FLAG_TRUE,
				}
			} else if rv.CanAddr() {
				if method = rv.Addr().MethodByName(methodName); method.IsValid() {
					return goja.PropertyDescriptor{
						Value:        vm.ToValue(method.Interface()),
						Writable:     goja.FLAG_FALSE,
						Enumerable:   goja.FLAG_TRUE,
						Configurable: goja.FLAG_TRUE,
					}
				}
			}
			return goja.PropertyDescriptor{}
		},

		IsExtensible:      func(target *goja.Object) bool { return false },
		PreventExtensions: func(target *goja.Object) bool { return true },
		DeleteProperty:    func(target *goja.Object, property string) bool { return false },
		GetPrototypeOf:    func(target *goja.Object) *goja.Object { return nil },
		SetPrototypeOf:    func(target *goja.Object, p *goja.Object) bool { return false },
	})
}

// fieldAccessor retourne un accessor qui relit le champ fieldName depuis le parent.
// Nécessaire pour que les sous-proxies soient live.
func fieldAccessor(parentAcc accessor, camelName string) accessor {
	return func() reflect.Value {
		rv := liveValue(parentAcc)
		if !rv.IsValid() {
			return reflect.Value{}
		}
		fv, _, ok := structFieldByName(rv, camelName)
		if !ok {
			return reflect.Value{}
		}
		return fv
	}
}

// ==================== SLICE PROXY ====================

func (so *SharedObject) buildSliceProxy(vm *goja.Runtime, acc accessor, path string) *goja.Object {
	target := vm.NewObject()

	return proxyAsObject(vm, target, &goja.ProxyTrapConfig{

		Get: func(target *goja.Object, property string, receiver goja.Value) goja.Value {
			rv := liveValue(acc)
			// slice nil → longueur 0, pas d'éléments (comportement identique à []T{})
			if !rv.IsValid() {
				if property == "length" {
					return vm.ToValue(0)
				}
				return goja.Undefined()
			}

			if property == "length" {
				return vm.ToValue(rv.Len())
			}

			idx, err := strconv.Atoi(property)
			if err != nil || idx < 0 || idx >= rv.Len() {
				return goja.Undefined()
			}

			elem := rv.Index(idx)
			childPath := joinPath(path, property)
			childAcc := indexAccessor(acc, idx)
			return so.reflectToJS(vm, childAcc, elem, elem.Type(), childPath)
		},

		Set: func(target *goja.Object, property string, value goja.Value, receiver goja.Value) bool {
			so.mu.Lock()
			defer so.mu.Unlock()

			rv := liveValue(acc)
			if !rv.IsValid() {
				return false
			}

			idx, err := strconv.Atoi(property)
			if err != nil || idx < 0 || idx >= rv.Len() {
				return false
			}

			elem := rv.Index(idx)
			if !elem.CanSet() {
				return false
			}

			old := safeInterface(elem)
			newGo, err := jsToGo(vm, value, elem.Type())
			if err != nil {
				return false
			}
			nv := reflect.ValueOf(newGo)
			if !nv.Type().AssignableTo(elem.Type()) {
				if !nv.Type().ConvertibleTo(elem.Type()) {
					return false
				}
				nv = nv.Convert(elem.Type())
			}
			elem.Set(nv)
			so.notify(ChangeEvent{Path: joinPath(path, property), OldValue: old, NewValue: newGo})
			return true
		},

		Has: func(target *goja.Object, property string) bool {
			rv := liveValue(acc)
			// slice nil → length existe (= 0), aucun index n'existe
			if !rv.IsValid() {
				return property == "length"
			}
			if property == "length" {
				return true
			}
			idx, err := strconv.Atoi(property)
			return err == nil && idx >= 0 && idx < rv.Len()
		},

		OwnKeys: func(target *goja.Object) *goja.Object {
			rv := liveValue(acc)
			arr := vm.NewArray()
			// slice nil → tableau vide de clés
			if !rv.IsValid() {
				return arr
			}
			for i := 0; i < rv.Len(); i++ {
				arr.Set(strconv.Itoa(i), strconv.Itoa(i))
			}
			return arr
		},

		GetOwnPropertyDescriptor: func(target *goja.Object, prop string) goja.PropertyDescriptor {
			rv := liveValue(acc)
			// slice nil → length=0, pas d'index
			if !rv.IsValid() {
				if prop == "length" {
					return goja.PropertyDescriptor{
						Value:        vm.ToValue(0),
						Writable:     goja.FLAG_TRUE,
						Enumerable:   goja.FLAG_FALSE,
						Configurable: goja.FLAG_FALSE,
					}
				}
				return goja.PropertyDescriptor{}
			}
			if prop == "length" {
				return goja.PropertyDescriptor{
					Value:        vm.ToValue(rv.Len()),
					Writable:     goja.FLAG_TRUE,
					Enumerable:   goja.FLAG_FALSE,
					Configurable: goja.FLAG_FALSE,
				}
			}
			idx, err := strconv.Atoi(prop)
			if err != nil || idx < 0 || idx >= rv.Len() {
				return goja.PropertyDescriptor{}
			}
			return writableDescriptor()
		},

		IsExtensible:      func(target *goja.Object) bool { return false },
		PreventExtensions: func(target *goja.Object) bool { return true },
		DeleteProperty:    func(target *goja.Object, property string) bool { return false },
		GetPrototypeOf:    func(target *goja.Object) *goja.Object { return nil },
		SetPrototypeOf:    func(target *goja.Object, p *goja.Object) bool { return false },
	})
}

// indexAccessor retourne un accessor live pour l'élément idx d'une slice.
func indexAccessor(sliceAcc accessor, idx int) accessor {
	return func() reflect.Value {
		rv := liveValue(sliceAcc)
		if !rv.IsValid() || idx >= rv.Len() {
			return reflect.Value{}
		}
		return rv.Index(idx)
	}
}

// ==================== MAP PROXY ====================

func (so *SharedObject) buildMapProxy(vm *goja.Runtime, acc accessor, path string) *goja.Object {
	target := vm.NewObject()

	rv := liveValue(acc) // pour récupérer Key/Elem types (stables)
	var keyType, elemType reflect.Type
	if rv.IsValid() {
		keyType = rv.Type().Key()
		elemType = rv.Type().Elem()
	}

	return proxyAsObject(vm, target, &goja.ProxyTrapConfig{

		Get: func(target *goja.Object, property string, receiver goja.Value) goja.Value {
			rv := liveValue(acc)
			if !rv.IsValid() || keyType == nil {
				return goja.Undefined()
			}
			key := stringToKey(property, keyType)
			if !key.IsValid() {
				return goja.Undefined()
			}
			val := rv.MapIndex(key)
			if !val.IsValid() {
				return goja.Undefined()
			}
			childPath := joinPath(path, property)
			childAcc := mapElemAccessor(acc, key)
			return so.reflectToJS(vm, childAcc, val, elemType, childPath)
		},

		Set: func(target *goja.Object, property string, value goja.Value, receiver goja.Value) bool {
			so.mu.Lock()
			defer so.mu.Unlock()

			rv := liveValue(acc)
			if !rv.IsValid() || keyType == nil {
				return false
			}
			key := stringToKey(property, keyType)
			if !key.IsValid() {
				return false
			}

			old := interface{}(nil)
			if oldVal := rv.MapIndex(key); oldVal.IsValid() {
				old = safeInterface(oldVal)
			}

			newGo, err := jsToGo(vm, value, elemType)
			if err != nil {
				return false
			}
			nv := reflect.ValueOf(newGo)
			if !nv.Type().AssignableTo(elemType) {
				if !nv.Type().ConvertibleTo(elemType) {
					return false
				}
				nv = nv.Convert(elemType)
			}
			rv.SetMapIndex(key, nv)
			so.notify(ChangeEvent{Path: joinPath(path, property), OldValue: old, NewValue: newGo})
			return true
		},

		Has: func(target *goja.Object, property string) bool {
			rv := liveValue(acc)
			if !rv.IsValid() || keyType == nil {
				return false
			}
			key := stringToKey(property, keyType)
			if !key.IsValid() {
				return false
			}
			return rv.MapIndex(key).IsValid()
		},

		OwnKeys: func(target *goja.Object) *goja.Object {
			rv := liveValue(acc)
			arr := vm.NewArray()
			// map nil → tableau vide (pas d'erreur, juste zéro clé)
			if !rv.IsValid() {
				return arr
			}
			for i, k := range rv.MapKeys() {
				arr.Set(strconv.Itoa(i), fmt.Sprintf("%v", k.Interface()))
			}
			return arr
		},

		GetOwnPropertyDescriptor: func(target *goja.Object, prop string) goja.PropertyDescriptor {
			rv := liveValue(acc)
			if !rv.IsValid() || keyType == nil {
				return goja.PropertyDescriptor{}
			}
			key := stringToKey(prop, keyType)
			if !key.IsValid() || !rv.MapIndex(key).IsValid() {
				return goja.PropertyDescriptor{}
			}
			return writableDescriptor()
		},

		DeleteProperty: func(target *goja.Object, property string) bool {
			so.mu.Lock()
			defer so.mu.Unlock()

			rv := liveValue(acc)
			if !rv.IsValid() || keyType == nil {
				return false
			}
			key := stringToKey(property, keyType)
			if !key.IsValid() {
				return false
			}
			old := interface{}(nil)
			if oldVal := rv.MapIndex(key); oldVal.IsValid() {
				old = safeInterface(oldVal)
			}
			rv.SetMapIndex(key, reflect.Value{})
			so.notify(ChangeEvent{Path: joinPath(path, property), OldValue: old, NewValue: nil})
			return true
		},

		IsExtensible:      func(target *goja.Object) bool { return true },
		PreventExtensions: func(target *goja.Object) bool { return false },
		GetPrototypeOf:    func(target *goja.Object) *goja.Object { return nil },
		SetPrototypeOf:    func(target *goja.Object, p *goja.Object) bool { return false },
	})
}

// mapElemAccessor retourne un accessor live pour une clé de map.
// Les maps Go ne sont pas adressables — la valeur est copiée, mais le proxy
// peut quand même la relire depuis la map à chaque accès.
func mapElemAccessor(mapAcc accessor, key reflect.Value) accessor {
	return func() reflect.Value {
		rv := liveValue(mapAcc)
		if !rv.IsValid() {
			return reflect.Value{}
		}
		return rv.MapIndex(key)
	}
}

// ==================== reflectToJS ====================

// reflectToJS convertit un reflect.Value Go en valeur goja.
// Si le type est composite (struct/slice/map), crée un proxy récursif live.
// Si c'est un scalaire, retourne directement vm.ToValue().
func (so *SharedObject) reflectToJS(vm *goja.Runtime, acc accessor, rv reflect.Value, ft reflect.Type, path string) goja.Value {
	// Déréférencer les pointeurs et interfaces dans rv.
	// - *struct nil  → null  (un objet inexistant n'a pas de champs)
	// - *[]T nil     → proxy slice vide  (nil slice ≈ []T{} côté JS)
	// - *map[K]V nil → proxy map vide    (nil map ≈ {} côté JS)
	concrete := rv
	for concrete.Kind() == reflect.Ptr || concrete.Kind() == reflect.Interface {
		if concrete.IsNil() {
			// Déterminer ce que pointe ce ptr nilisé
			inner := concrete.Type()
			for inner.Kind() == reflect.Ptr {
				inner = inner.Elem()
			}
			switch inner.Kind() {
			case reflect.Slice, reflect.Array:
				// slice nil → proxy vide (length=0)
				derefAcc := derefAccessor(acc)
				return so.buildSliceProxy(vm, derefAcc, path)
			case reflect.Map:
				// map nil → proxy vide (Object.keys=[])
				derefAcc := derefAccessor(acc)
				return so.buildMapProxy(vm, derefAcc, path)
			default:
				// struct nil, scalaire nil → null
				return goja.Null()
			}
		}
		concrete = concrete.Elem()
	}
	// Déréférencer le type aussi
	concFT := ft
	for concFT.Kind() == reflect.Ptr {
		concFT = concFT.Elem()
	}

	switch concrete.Kind() {
	case reflect.Struct:
		// Créer un accessor déréférencé pour les structs via pointeur
		derefAcc := derefAccessor(acc)
		return so.buildStructProxy(vm, derefAcc, path)

	case reflect.Slice, reflect.Array:
		// slice nil → proxy d'une slice vide (length=0, pas null)
		// Cela permet : config.tags.length === 0  au lieu de  config.tags === null
		derefAcc := derefAccessor(acc)
		return so.buildSliceProxy(vm, derefAcc, path)

	case reflect.Map:
		// map nil → proxy d'une map vide (Object.keys === [])
		derefAcc := derefAccessor(acc)
		return so.buildMapProxy(vm, derefAcc, path)

	// ---- Scalaires ----
	case reflect.Bool:
		return vm.ToValue(concrete.Bool())
	case reflect.String:
		return vm.ToValue(concrete.String())
	// int* — couvre aussi rune (int32)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return vm.ToValue(concrete.Int())
	// uint*
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return vm.ToValue(concrete.Uint())
	case reflect.Float32, reflect.Float64:
		return vm.ToValue(concrete.Float())

	default:
		// Fallback générique (func, chan, etc.)
		return vm.ToValue(safeInterface(concrete))
	}
}

// derefAccessor retourne un accessor qui déréférence les pointeurs de l'accessor source.
func derefAccessor(acc accessor) accessor {
	return func() reflect.Value {
		rv := acc()
		for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
			if rv.IsNil() {
				return reflect.Value{}
			}
			rv = rv.Elem()
		}
		return rv
	}
}

// ==================== jsToGo ====================

// jsToGo convertit une valeur goja vers le type Go cible.
func jsToGo(vm *goja.Runtime, jsVal goja.Value, targetType reflect.Type) (interface{}, error) {
	if jsVal == nil || goja.IsNull(jsVal) || goja.IsUndefined(jsVal) {
		return reflect.Zero(targetType).Interface(), nil
	}

	exported := jsVal.Export()
	return coerce(exported, targetType)
}

// coerce convertit une valeur Go brute (exportée depuis goja) vers targetType.
//
// goja exporte les types JS ainsi :
//
//	number (entier)  → int64
//	number (float)   → float64
//	boolean          → bool
//	string           → string
//	Array            → []interface{}
//	Object           → map[string]interface{}
//	null/undefined   → nil
func coerce(v interface{}, t reflect.Type) (interface{}, error) {
	// nil → valeur zéro
	if v == nil {
		return reflect.Zero(t).Interface(), nil
	}

	// Dépiler les pointeurs cibles
	if t.Kind() == reflect.Ptr {
		inner, err := coerce(v, t.Elem())
		if err != nil {
			return nil, err
		}
		ptr := reflect.New(t.Elem())
		innerVal := reflect.ValueOf(inner)
		if !innerVal.Type().AssignableTo(t.Elem()) {
			if !innerVal.Type().ConvertibleTo(t.Elem()) {
				return nil, fmt.Errorf("cannot convert %T to %s", inner, t.Elem())
			}
			innerVal = innerVal.Convert(t.Elem())
		}
		ptr.Elem().Set(innerVal)
		return ptr.Interface(), nil
	}

	rv := reflect.ValueOf(v)

	// Assignable directement
	if rv.Type().AssignableTo(t) {
		return v, nil
	}

	// Conversion numérique directe (int64→int, float64→float32, etc.)
	if rv.Type().ConvertibleTo(t) {
		return rv.Convert(t).Interface(), nil
	}

	switch t.Kind() {

	case reflect.Slice:
		// []interface{} → []T
		arr, ok := v.([]interface{})
		if !ok {
			return nil, fmt.Errorf("cannot convert %T to %s", v, t)
		}
		result := reflect.MakeSlice(t, len(arr), len(arr))
		for i, elem := range arr {
			cv, err := coerce(elem, t.Elem())
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			ev := reflect.ValueOf(cv)
			if !ev.Type().AssignableTo(t.Elem()) {
				if !ev.Type().ConvertibleTo(t.Elem()) {
					return nil, fmt.Errorf("[%d]: cannot convert %T to %s", i, cv, t.Elem())
				}
				ev = ev.Convert(t.Elem())
			}
			result.Index(i).Set(ev)
		}
		return result.Interface(), nil

	case reflect.Map:
		// map[string]interface{} → map[K]V
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("cannot convert %T to %s", v, t)
		}
		result := reflect.MakeMap(t)
		for k, val := range m {
			ck, err := coerce(k, t.Key())
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", k, err)
			}
			cv, err := coerce(val, t.Elem())
			if err != nil {
				return nil, fmt.Errorf("value[%q]: %w", k, err)
			}
			kv := reflect.ValueOf(ck)
			if !kv.Type().AssignableTo(t.Key()) {
				kv = kv.Convert(t.Key())
			}
			vv := reflect.ValueOf(cv)
			if !vv.Type().AssignableTo(t.Elem()) {
				vv = vv.Convert(t.Elem())
			}
			result.SetMapIndex(kv, vv)
		}
		return result.Interface(), nil

	case reflect.Struct:
		// map[string]interface{} → struct
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("cannot convert %T to struct %s", v, t)
		}
		result := reflect.New(t).Elem()
		for k, val := range m {
			fv, ft, ok := structFieldByName(result, k)
			if !ok || !fv.CanSet() {
				continue
			}
			cv, err := coerce(val, ft)
			if err != nil {
				continue
			}
			ev := reflect.ValueOf(cv)
			if !ev.Type().AssignableTo(ft) {
				if !ev.Type().ConvertibleTo(ft) {
					continue
				}
				ev = ev.Convert(ft)
			}
			fv.Set(ev)
		}
		return result.Interface(), nil

	case reflect.String:
		// Nombre → string (rune reconverti en string)
		return fmt.Sprintf("%v", v), nil
	}

	return nil, fmt.Errorf("cannot convert %T (%v) to %s", v, v, t)
}

// ==================== REFLECT HELPERS ====================

// structFieldByName retourne le champ d'une struct identifié par nom camelCase.
// Résolution dans cet ordre :
//  1. Nom PascalCase exact (CamelCase → PascalCase)
//  2. Tag `json:"name"`
//  3. Correspondance case-insensitive
func structFieldByName(rv reflect.Value, camelName string) (reflect.Value, reflect.Type, bool) {
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return reflect.Value{}, nil, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return reflect.Value{}, nil, false
	}
	t := rv.Type()

	// 1. PascalCase exact
	pascalName := camelToPascal(camelName)
	if f, ok := t.FieldByName(pascalName); ok && f.IsExported() {
		return rv.FieldByIndex(f.Index), f.Type, true
	}

	// 2. Tag json / case-insensitive
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if tag, ok := f.Tag.Lookup("json"); ok {
			tagName := strings.Split(tag, ",")[0]
			if tagName == camelName {
				return rv.Field(i), f.Type, true
			}
		}
		if strings.EqualFold(f.Name, camelName) {
			return rv.Field(i), f.Type, true
		}
	}
	return reflect.Value{}, nil, false
}

// structOwnKeys retourne un tableau goja des noms JS des champs exportés et des méthodes.
func structOwnKeys(vm *goja.Runtime, rv reflect.Value) *goja.Object {
	arr := vm.NewArray()
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return arr
	}
	t := rv.Type()
	i := 0
	for j := 0; j < t.NumField(); j++ {
		f := t.Field(j)
		if !f.IsExported() {
			continue
		}
		name := pascalToCamel(f.Name)
		if tag, ok := f.Tag.Lookup("json"); ok {
			if tagName := strings.Split(tag, ",")[0]; tagName != "" && tagName != "-" {
				name = tagName
			}
		}
		arr.Set(strconv.Itoa(i), name)
		i++
	}
	// Ajouter les méthodes de la valeur
	for j := 0; j < t.NumMethod(); j++ {
		m := t.Method(j)
		if m.IsExported() {
			arr.Set(strconv.Itoa(i), pascalToCamel(m.Name))
			i++
		}
	}
	// Ajouter les méthodes du pointeur si accessible
	if rv.CanAddr() {
		pt := reflect.PointerTo(t)
		for j := 0; j < pt.NumMethod(); j++ {
			m := pt.Method(j)
			if m.IsExported() {
				if _, has := t.MethodByName(m.Name); !has {
					arr.Set(strconv.Itoa(i), pascalToCamel(m.Name))
					i++
				}
			}
		}
	}
	return arr
}

// stringToKey convertit une clé JS (string) en reflect.Value du keyType.
func stringToKey(s string, keyType reflect.Type) reflect.Value {
	switch keyType.Kind() {
	case reflect.String:
		return reflect.ValueOf(s).Convert(keyType)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return reflect.Value{}
		}
		return reflect.ValueOf(n).Convert(keyType)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return reflect.Value{}
		}
		return reflect.ValueOf(n).Convert(keyType)
	}
	return reflect.Value{}
}

// safeInterface capture rv.Interface() sans paniquer sur les types non-exportables.
func safeInterface(rv reflect.Value) (result interface{}) {
	defer func() { recover() }()
	if !rv.IsValid() {
		return nil
	}
	return rv.Interface()
}

// joinPath construit un chemin hiérarchique "parent.child".
func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// writableDescriptor retourne un PropertyDescriptor standard writable/enumerable/configurable.
func writableDescriptor() goja.PropertyDescriptor {
	return goja.PropertyDescriptor{
		Writable:     goja.FLAG_TRUE,
		Enumerable:   goja.FLAG_TRUE,
		Configurable: goja.FLAG_TRUE,
	}
}

// ==================== CASE HELPERS ====================

func pascalToCamel(s string) string {
	if len(s) == 0 {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func camelToPascal(s string) string {
	if len(s) == 0 {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// ==================== NOTIFY ====================

func (so *SharedObject) notify(ev ChangeEvent) {
	so.subsMu.RLock()
	subs := make([]sharedSub, len(so.subscribers))
	copy(subs, so.subscribers)
	so.subsMu.RUnlock()
	for _, s := range subs {
		s.fn(ev)
	}
}
