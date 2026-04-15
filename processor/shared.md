# SharedObject

Partage de valeurs natives avec plusieurs runtimes JavaScript via **ES6 Proxy**.  
Les modifications JS écrivent directement dans la mémoire native. Les lectures relisent toujours la valeur live.

---

## Sommaire

- [Enregistrement](#enregistrement) — RegisterGlobal · HasGlobal · UnregisterGlobal · Register
- [Accès JavaScript](#accès-javascript)
- [Types supportés](#types-supportés)
- [Résolution des noms de champs](#résolution-des-noms-de-champs)
- [Valeurs nil](#valeurs-nil)
- [Subscribe / Unsubscribe](#subscribe--unsubscribe)
- [Cohérence live](#cohérence-live)
- [Limitations](#limitations)
- [Référence API Native](#référence-api-native)

---

## Enregistrement

```go
type ServerConfig struct {
    Host string
    Port int
    Tls  TLSConfig
    Tags []string
    Env  map[string]string
}

var cfg = ServerConfig{Host: "0.0.0.0", Port: 8080}

// Enregistrer avant de créer les VMs.
// Le nom PascalCase → camelCase en JS : "Config" → variable "config"
processor.RegisterGlobal("Config", &cfg)  // DOIT être un pointeur

// Dans processor.New(), avant RunString :
processor.AttachGlobals(vm)
```

> `data` **doit être un pointeur**. Une valeur non-pointeur rend les modifications JS invisibles côté Go.

---

## Accès JavaScript

### Struct

```js
config.host                  // lecture → "0.0.0.0"
config.port                  // → 8080
config.host = "localhost"    // écriture → modifie cfg.Host
config.port = 9090

"host" in config             // → true
Object.keys(config)          // → ["host", "port", "tls", ..., "subscribe", "unsubscribe"]
```

### Struct imbriquée

```js
config.tls.certFile          // lecture récursive
config.tls.enabled = true    // écriture récursive → modifie cfg.Tls.Enabled

// Profondeur arbitraire
config.db.replica.city = "Paris"
```

### Slice

```js
config.tags.length           // nombre d'éléments
config.tags[0]               // lecture par index
config.tags[1] = "v2"        // écriture en place
config.tags[99]              // → undefined (hors bornes, pas d'exception)

for (var i = 0; i < config.tags.length; i++) { ... }
```

> `push`, `pop`, `splice` et les méthodes Array ne sont **pas supportés**. La slice Go a une taille fixe.

### Map

```js
config.env["KEY"]            // lecture
config.env["KEY"] = "val"    // écriture (crée ou met à jour)
delete config.env["OLD"]     // suppression

"KEY" in config.env          // → true / false
Object.keys(config.env)      // → tableau des clés
```

### subscribe / unsubscribe

Ces deux fonctions sont exposées directement sur l'objet proxy, aux côtés des champs Go.

```js
// Elles apparaissent dans Object.keys :
Object.keys(config)   // [..., "subscribe", "unsubscribe"]

// Ce sont des fonctions non modifiables :
typeof config.subscribe    // → "function"
config.subscribe = "nope"  // ignoré silencieusement
```

---

## Types supportés

| Type Go | Rendu JS | Opérations |
|---|---|---|
| `bool` | `boolean` | get, set |
| `string` | `string` | get, set |
| `int`, `int8/16/32/64` | `number` | get, set |
| `rune` (`int32`) | `number` (entier Unicode) | get, set |
| `uint`, `uint8/16/32/64` | `number` | get, set |
| `byte` (`uint8`) | `number` | get, set |
| `float32`, `float64` | `number` | get, set |
| `struct` | Proxy objet | get, set, has, ownKeys |
| `*struct` non-nil | Proxy objet (déréférencé) | get, set, has, ownKeys |
| `*struct` nil | `null` | — |
| `[]T`, `[N]T` | Proxy array-like | `[i]`, `.length` |
| `[]T` nil | Proxy vide (`length === 0`) | — |
| `map[string]V` | Proxy objet | get, set, delete, ownKeys |
| `map[int]V` | Proxy objet (clés converties) | get, set, delete |
| `map[K]V` nil | Proxy vide (`Object.keys === []`) | — |

### Conversions JS → Go à l'écriture

| Valeur JS | Export moteur | Cibles Natives |
|---|---|---|
| `42` | `int64` | `int`, `int8`, `uint8`, `rune`… via `Convert` |
| `3.14` | `float64` | `float32`, `float64` |
| `true` | `bool` | `bool` |
| `"str"` | `string` | `string` |
| `null` / `undefined` | `nil` | valeur zéro du type cible (`0`, `""`, `false`…) |
| `[1,2,3]` | `[]interface{}` | `[]T` via coerce récursif |
| `{a:1}` | `map[string]interface{}` | `map[K]V` ou `struct` |

---

## Résolution des noms de champs

Pour `config.certFile`, le proxy cherche le champ Go dans cet ordre :

1. **PascalCase exact** : `certFile` → cherche `CertFile`
2. **Tag `json`** : champ avec `json:"certFile"`
3. **Insensible à la casse** : `certFile` matche `CERTFILE`, `Certfile`…

```go
type TLSConfig struct {
    CertFile  string `json:"certFile"`  // → config.tls.certFile
    KeyFile   string `json:"keyFile"`   // → config.tls.keyFile
    Enabled   bool                      // → config.tls.enabled  (PascalCase)
    unexported string                   // inaccessible depuis JS
}
```

---

## Valeurs nil

| Type Go | Valeur JS | Comportement |
|---|---|---|
| `*Struct` nil | `null` | Accès à une propriété → `undefined` |
| `[]T` nil | Proxy vide | `length === 0`, `[0] === undefined`, pas `null` |
| `map[K]V` nil | Proxy vide | `Object.keys === []`, `"k" in m === false`, pas `null` |

```js
// *struct nil
config.nilPtr === null          // → true

// []T nil
config.nilSlice !== null        // → true
config.nilSlice.length          // → 0
config.nilSlice[0]              // → undefined

// map nil
config.nilMap !== null          // → true
Object.keys(config.nilMap)      // → []
"key" in config.nilMap          // → false
```

---

## Subscribe / Unsubscribe

Toute modification délivre un `ChangeEvent` à tous les abonnés enregistrés.

### Depuis JS

```js
var id = config.subscribe(function(ev) {
    // ev.path     — chemin de la propriété modifiée
    // ev.oldValue — valeur avant
    // ev.newValue — valeur après (null si clé map supprimée)
    print(ev.path + ": " + ev.oldValue + " → " + ev.newValue)
})

config.host = "new"
// → "host: old → new"

config.tls.certFile = "/new.pem"
// → "tls.certFile:  → /new.pem"

config.tags[0] = "v2"
// → "tags.0: v1 → v2"

config.env["KEY"] = "val"
// → "env.KEY:  → val"   (oldValue = null : nouvelle clé)

delete config.env["KEY"]
// → "env.KEY: val → "   (newValue = null : clé supprimée)

config.unsubscribe(id)
// Les événements suivants ne sont plus reçus
```

### Côté natif

```go
id := so.Subscribe(func(ev processor.ChangeEvent) {
    log.Printf("[%s] %v → %v", ev.Path, ev.OldValue, ev.NewValue)
})

so.Unsubscribe(id)
```

### Chemins des événements

| Modification JS | `ev.Path` |
|---|---|
| `config.host = "x"` | `"host"` |
| `config.tls.certFile = "x"` | `"tls.certFile"` |
| `config.tags[2] = "x"` | `"tags.2"` |
| `config.env["KEY"] = "x"` | `"env.KEY"` |
| `delete config.env["KEY"]` | `"env.KEY"` (newValue = nil) |

### Les lectures ne déclenchent pas d'événements

```js
config.host        // aucun événement
config.tags[0]     // aucun événement
```

---

## Cohérence live

Toutes les lectures relisent depuis le pointeur racine — les valeurs sont toujours fraîches.

```go
// Mutation native → visible depuis JS immédiatement
cfg.Host = "mutated"
// Dans toute VM : config.host === "mutated"

// Mutation JS → visible côté natif immédiatement
// script: config.port = 9090
fmt.Println(cfg.Port) // → 9090

// Mutation JS (VM 1) → visible depuis JS (VM 2)
// VM 1 : config.host = "shared"
// VM 2 : config.host  →  "shared"
```

Les accès concurrents depuis plusieurs goroutines / VMs sont protégés par `sync.RWMutex`.

---

## Limitations

### Slice : taille fixe

```js
config.tags.push("new")   // ✗ — non supporté
config.tags[0] = "new"    // ✓ — modification en place
```

Pour redimensionner, modifier la slice côté Go et relire depuis JS.

### Map nil : écriture ignorée

Une `map[K]V` nil est exposée en lecture comme un proxy vide, mais **les écritures sont ignorées** (on ne peut pas écrire dans une map Go nil). Initialiser la map côté Go avant d'écrire depuis JS :

```go
cfg.NilMap = make(map[string]int) // initialiser avant
```

```js
config.nilMap["key"] = 42  // ✓ seulement après initialisation Go
```

### Valeurs de map non adressables

Les valeurs d'une map Go ne sont pas adressables. Un élément de map qui est une struct ne peut pas être modifié champ par champ :

```go
// ✗ — map[string]Service : les champs de Service ne sont pas modifiables
type Config struct { Services map[string]Service }

// ✓ — map[string]*Service : les champs sont modifiables via le pointeur
type Config struct { Services map[string]*Service }
```

### Champs non exportés

Les champs Go non exportés sont invisibles depuis JS (`undefined`).

---

## Référence API Native

### `RegisterGlobal(name string, data interface{}, ignoreIfExist ...bool)`

Enregistre une valeur dans le registre global. `data` doit être un pointeur.  
Nom JS = camelCase de `name` : `"AppConfig"` → `appConfig`.

```go
processor.RegisterGlobal("Config",   &myConfig)        // écrase si existant
processor.RegisterGlobal("Config",   &myConfig, true)  // ignore si déjà enregistré
processor.RegisterGlobal("AppState", &state)
```

### `HasGlobal(name string) bool`

Vérifie si un nom est enregistré dans le registre global.

```go
if !processor.HasGlobal("Config") {
    processor.RegisterGlobal("Config", &cfg)
}
```

### `UnregisterGlobal(name string)`

Supprime une entrée du registre global. Les VMs déjà créées conservent leur proxy.

```go
processor.UnregisterGlobal("TempConfig")
```

### `Register(vm *goja.Runtime, name string, data interface{})`

Attache une valeur Go à **une seule VM** sans modifier le registre global.  
Les autres VMs créées ultérieurement ne verront pas cette valeur.

```go
vm := processor.NewVM()
// Utile pour des données spécifiques à une requête ou une goroutine
processor.Register(vm.Runtime, "Request", &reqData)
processor.Register(vm.Runtime, "Session", &session)
```

### `AttachGlobals(vm *goja.Runtime)`

Installe tous les SharedObjects du registre global dans la VM.

```go
vm := processor.NewVM()
processor.AttachGlobals(vm.Runtime)
// puis vm.RunString(...)
```

### `(*SharedObject).Subscribe(fn func(ChangeEvent)) uint64`

Enregistre un listener Go. Retourne un ID d'abonnement.

```go
id := so.Subscribe(func(ev processor.ChangeEvent) {
    log.Printf("[%s] %v → %v", ev.Path, ev.OldValue, ev.NewValue)
})
```

### `(*SharedObject).Unsubscribe(id uint64)`

Supprime le listener.

```go
so.Unsubscribe(id)
```

### `ChangeEvent`

```go
type ChangeEvent struct {
    Path     string      // ex: "host", "tls.certFile", "tags.0", "env.KEY"
    OldValue interface{} // nil si nouvelle clé map
    NewValue interface{} // nil si clé map supprimée
}
```
