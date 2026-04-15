# Coding Rules & Standards - HTTP-Server

Ce document définit les règles de codage et les standards à suivre pour le projet `http-server`. Il est destiné à la fois aux développeurs et aux agents IA intervenant sur le codebase.

### HTTP Engine & Middlewares

1. **Named Middlewares**: When implementing a new `@Middleware`, always use `mw.Get(key, default)` and `mw.Has(key)` from the `MiddlewareUse` struct.
2. **Variadic Registration**: Route registration in `http_protocol.go` MUST follow the `app.Add(methods, path, handlers[0], handlers[1:]...)` pattern.
3. **Optimized Stack**: Priority must be given to modern and high-performance middleware implementations.
4. **Binary Content**: Always use `[]byte` for content (via `RouteConfig.Content()`) to ensure binary safety (images, downloads, etc.). Avoid using `string` for raw payload.

### Real-time Communication (Hub SSE)

1. **Unity**: Use the unified Hub for all real-time protocols. A message published on a channel MUST be deliverable to SSE, WS, MQTT, and Socket.IO clients simultaneously.
2. **Socket.IO**: The `IO` method in Binder should be preferred for complex real-time apps. Use `c.Locals("sid")` to pre-seed session IDs from cookies.
3. **MQTT**: Topics are mapped 1:1 to Hub channels. Avoid using `#` or `+` in channel names unless intended for MQTT wildcard matching.
4. **Hierarchical Channels**: Standardize CRUD channels as `crud:{ns}:{schema}:{id}:{action}`. Use `broadcastCRUD` helper in `modules/crud` for consistency.
5. **Channel Injection**: Modules can pre-configure SSE channels by setting `c.Locals("channels")` (string or `[]string`) before calling `sse.Handler`.

### CRUD & Administration (Admin HTMX)

1. **Native Rendering**: L'interface d'administration `/api/_admin` utilise **exclusivement** le package `processor` (Mustache + `<js? ?>`) pour le rendu des vues HTML; l'usage du package standard `text/template` ou `html/template` y est interdit.
2. **Extensibility**: L'injection de nouvelles métriques ou pages doit obligatoirement passer par les APIs `RegisterAdminPage()` et `RegisterAdminLink()`.
3. **Assets**: Les CSS et templates HTML de l'admin sont embarqués de manière native via `embed.FS` (pas de requêtes vers des CDNs extérieurs ou fichiers de dépendance locaux non compilés).

### Payment Module

1. **Provider URIs**: Use `stripe://`, `momo://`, `cinetpay://`, `x402://`, `crypto://` URI schemes for native providers. Use `custom` for fully scriptable providers.
2. **Webhook Phases**: Always implement `@PRE` for signature verification before `@POST` for business logic.
3. **Custom Operations**: Each operation (`CHARGE`, `VERIFY`, `REFUND`, `CHECKOUT`, `PUSH`) must define `ENDPOINT`, `METHOD`, and `RESPONSE`.
4. **JS API**: Use `require('payment')` for the default connection. Use `.get(name)` for named connections.
5. **Identification**: Use `USER_ID_LOOKUP` to define how to identify a user across sessions for payment history purposes.
6. **Persistence**: All payments must be recorded via the `SCHEMA` directive. If absent, a default memory-backed schema is used.

### FsRouter (File-System Routing)

1. **Naming**: Use `[id].js` for dynamic parameters and `[...catchall].js` for catch-all routes.
2. **Handlers**: exported handlers via `module.exports = { GET: (c) => ... }` are preferred for clarity, but `.GET.js` suffixes are supported for simple cases.
3. **Middlewares**: `_middleware.js` files are applied recursively. Ensure `c.Next()` is called to propagate the chain.
4. **Layouts**: `_layout.html` or `_layout.js` files are recursive and must use the `content` variable (Mustache `{{content}}` or JS global `content`).
5. **Partials**: Use `.partial.` in the filename (e.g., `info.partial.html`) to bypass layout wrapping for AJAX or API fragments.
6. **Context**: Use `c.Locals("_fsrouter_params")` and `c.Locals("_fsrouter_catchall")` to access routing variables if needed natively.

### Virtual Hosts (Vhost)

1. **Isolation**: Each vhost runs as its own child process. Do NOT share state between vhosts in memory.
2. **`.vhost` Config**: Use HCL syntax. Fields: `domain`, `aliases`, `port`, `cert`, `key`, `http {}`, `https {}`, `listen {}`.
3. **Naming**: The folder name is the default hostname. Use `domain` in `.vhost` to override.
4. **Flags**: The master excludes `--vhost`, `--port`, `--address`, `--silent`, `--socket` from child propagation. All other flags are forwarded.
5. **Sockets**: Internal UDS paths are auto-generated in `/tmp`. Public sockets use `normalizeSocketPath` for cross-platform support.
6. **HCL Parsing**: Use `hclsimple.Decode` with a `.hcl` filename hint (not `DecodeFile`) for `.vhost` files.

### Authentication & Authorization

1. **Context-Aware**: Any `Auth` implementation MUST take `fiber.Ctx` as its first argument to support session-based or token-based logic.
2. **Hashing**: New passwords stored in configurations SHOULD use `{SHA512}` or `{BCRYPT}`.
3. **Escaping**: Binder variables and arguments MUST support multiple quote types (``,"",'') with backslash escaping.

### Security Constants

- Default `CSRF` cookie name: `__Host-csrf_`
- Default `Session` cookie name: `__Host-sid_csrf`
- **Baseline Security** : 100 requests per second (burst 10) apply to all protocols by default.
- Always enable `CookieSecure: true` and `CookieHTTPOnly: true` for sensitive data.

## 1. Standards Natifs (Backend)
- **Framework** : Utiliser exclusivement le moteur HTTP interne.
- **Logging** : Utiliser **Zerolog**. Séparer les logs par niveau :
  - `Trace`, `Debug`, `Info` -> stdout via `app.Info()`, `app.Debug()`, etc.
  - `Warn`, `Error`, `Fatal`, `Panic` -> stderr via `app.Error()`, `app.Warn()`, etc.
- **Erreurs** : Suivre le pattern idiomatique (`if err != nil`). Les erreurs renvoyées doivent utiliser les codes HTTP appropriés (ex: `ErrNotFound`).
- **Concurrence** : Utiliser les Mutex (`sync.Mutex` ou `sync.RWMutex`) ou des patterns basés sur les Channels et variables atomiques pour protéger les ressources partagées dans les modules (ex: `sse.Hub` avec Shards, `db.Connection`).
- **Protocol (Binder)** : Les nouveaux protocoles doivent implémenter l'interface `Directive` (`Name`, `Match`, `Handle`, `HandlePacket`, `Close`).
  - `Match(peek []byte)` : Détection par "peeking" (512 octets).
  - `Handle(conn net.Conn)` : Traitement des flux stream-based (TCP/TLS).
  - `HandlePacket(data []byte, addr net.Addr, pc net.PacketConn)` : Traitement des paquets (UDP).
  - **Sécurité** : La Baseline (100r/s) est automatique. Pour UDP, le filtrage `SECURITY` est appliqué par paquet via `AllowPacket`.
  - `PROXY [type] [path] [url]` : Délégation HTTP/WS.
  - `REWRITE [pattern] [sub] [js_cond?]` : Réécriture d'URL interne. Pattern = **regexp**, sub = remplacement regexp (`$1`, `$2`). `js_cond` optionel (expr JS booléenne, accès au contexte via `Method()`, `Get()`, `Path()`, `Query()`, `IP()`, `Hostname()`).
  - `REDIRECT [code?] [pattern] [sub] [js_cond?]` : Redirection HTTP 3xx. `code` optionel avant le pattern (défaut 302). Mêmes capacités regex et condition JS que `REWRITE`.
  - `ERROR [code?] [type?] [contentType|path?]` : Interception d'erreurs HTTP. `code` optionel (vide = toutes erreurs). `type` = `TEMPLATE`, `HEX`, `BASE64`, `BASE32`, `BINARY`, `TEXT`, `JSON`, `HANDLER`, `FILE` (optionel, défaut = JS inline). ContentType optionel. Bloc `END ERROR` requis pour les formes inline.
  - `GROUP [path] DEFINE` : Déclaration de sous-groupes de routes HTTP avec des middlewares récursifs attachés. Bloqué par `END GROUP`.
  - `[METHOD] [path] [type?] [filepath|content?] [contentType?]` : Routes HTTP (`GET`, `POST`...). Supporte exactment les mêmes types (`TEMPLATE`, `HEX`, `JSON`, etc.) que `ERROR`. Par défaut: JS inline. Bloc `END [METHOD]` requis pour formes inline.
  - `ENV SET/REMOVE/DEFAULT [KEY] [VALUE]` : Manipule les variables d'environnement du **processus** avec préfixe (défaut `APP_`). `ENV [filepath]` charge un fichier `.env`. `ENV PREFIX` change le préfixe.
  - `SET [KEY] [VALUE]` : Définit une configuration **interne** (ne modifie pas l'env process). Accessible via `settings` en JS. Disponible dans les blocks TEMPLATE via `ProcessString`.
  - `WORKER [js_file] [KEY=VALUE...]` : Lance un script JS en arrière-plan dans une tâche isolée. `config` contient les args du worker, `settings` contient les `SET`. Répétable.
  - `SSL [key] [cert]` / `SSL AUTO [domain] [email]` : Configuration TLS/HTTPS.
  - `SSE [path]` : Server-Sent Events. `const sse = require("sse")` est **auto-injecté** dans les handlers inline.
  - `AUTH [format] [path]` / `AUTH USER [user] [pwd]` / `AUTH { scripts }` : Système d'authentification robuste. Supporte JSON, YAML, TOML, ENV, CSV et scripts JS (`allow()`, `reject(msg...)`, variables `username`, `password`, `config`). Supporte **Bcrypt**.
  - `DTP` : Directives spécifiques : `DATA [name]`, `EVENT [name]`, `PING`, `PONG`, `CMD`, `ACK`, `NACK`, `ERR`, `QUEUE`, `ONLINE`. Routage par subtype supportant les noms (ex: `TEMP`) ou les codes hex (ex: `0x01`). Intégration avec `AUTH` via `OnGetDevice` (helper `allow(secret, proto)`).
  - `MQTT` : **Broker MQTT 3.1.1/5.0** : Broker natif ultra-performant unifié avec le Hub SSE. Support de la persistence QoS 1/2 via GORM (`STORAGE`) et sécurisation native au niveau socket (`SECURITY`) par sniffing non-destructif.
  - **MQTT Testing** : Toujours utiliser `t.TempDir()` pour les bases `STORAGE` et des ports dynamiques (`:0`) pour éviter les conflits d'environnement.
  - `SECURITY [name] [default?]` : Définit un profil de sécurité (WAF/Network). L'argument `[default]` permet de surcharger la baseline globale du serveur (100r/s).
  - `CONNECTION RATE [nb] [interval] [burst=N]` : Limite SYN-level. Supporte `r/s`, `r/m`, `r/h`.
  - `CONNECTION [ALLOW|DENY] [IP|CIDR|OLC|GEOJSON_NAME]` : Filtrage IP ou géographique.
  - `GEOJSON [name] [path|BEGIN...END]` : Enregistre des zones géographiques (FeatureCollection, Feature, etc.) pour filtrage par `GEOJSON_NAME`.
  - `ACTION [On|Off|DetectionOnly]` : Moteur Coraza WAF.
  - `INCLUDE [filepath]` : Inclus un fichier Binder récursivement. Résolution relative au fichier parent. Détection de circularité fatale.
  - **Module DB** : API type Mongoose. Toujours privilégier les requêtes asynchrones en JS (`exec()`).
  - **Module SSE/WS** : Utiliser le Hub central pour toute communication temps-réel.
  - **Développement de Directives** : Utiliser systématiquement `RouteConfig.Content()` qui retourne désormais des `[]byte`. Ne jamais manipuler de `string` pour du contenu brut afin d'éviter les corruptions d'encodage.
  - **Enregistrement de Protocoles** : Pour tout nouveau protocole supportant le changement de contexte (ex: `MQTT`, `DATABASE`), appeler systématiquement `RegisterProtocolKeyword(name)` lors de l'initialisation du module pour assurer que le `Parser` Binder reconnaît le mot-clé comme une directive de haut niveau.
  - **Multiplexage** : Un bloc `TCP`/`UDP` peut contenir des sous-blocs (`HTTP`, `DTP`, etc.) pour partager un port. Si un seul protocole est présent, le peeking est sauté pour éviter les deadlocks.

## 2. Standards JavaScript (Server-side Logic)
- **Modules** : Utiliser `require("module_name")` pour importer les modules natifs (`db`, `sse`, `cookies`, `storage`, etc.).
- **Base de Données** : Suivre l'API type **Mongoose** :
  - Définir un `Schema`.
  - Créer un `Model`.
  - Utiliser les méthodes chaînables (`find()`, `sort()`, `limit()`, `exec()`).
- **Variables** : Privilégier `const` et `let` sur `var`.
- **Intégration HTML** : 
  - Utiliser `<?js ... ?>` pour la logique complexe (boucles, conditions, calculs).
  - Utiliser `<?= ... ?>` pour l'affichage direct de variables.

## 3. Standards HTML & Templating
- **Syntaxe** : Mélange de balises PHP-style (`<?js ?>`) et de **Mustache** (`{{ variable }}`).
- **Logique vs Rendu** : La logique métier doit être placée dans `<?js ... ?>` ou des fichiers `.js` séparés. Le HTML doit principalement contenir des tags Mustache pour l'affichage.
- **HTMX** : Utiliser les attributs `hx-*` pour les interactions asynchrones. Le script HTMX est automatiquement injecté par le serveur si ce n'est pas désactivé (`--no-htmx`), avec possibilité d'injecter du HTML brut dans le `<head>` (`--inject-html`).

## 4. Documentation & IA
- **Commentaires** : Chaque fonction native exportée doit avoir un commentaire de documentation.
- **Prompting** : Pour les agents IA, fournir des descriptions claires des structures de données (HCL pour Binder, Schémas pour DB).
- **Fichiers de Définition** : Maintenir les fichiers `.md` dans `doc/` à jour avec toute nouvelle fonctionnalité.

## 5. Configuration (plugins/config)
- **Centralisation** : Toute la configuration de l'application (serveur, timeouts, logs, etc.) est centralisée dans la structure `AppConfig` (`plugins/config/config.go`).
- **Ordre de Préséance** : Les valeurs de configuration sont chargées dans l'ordre de priorité suivant : `Défauts` < `Fichiers (.json, .yaml, .toml)` < `Variables d'environnement (.env, OS)` < `Flags CLI`.
- **Hot-Reloading** : Les modifications appliquées aux fichiers de configuration et d'environnement sont détectées par un `Watcher` et rechargées à chaud (désactivable via `--hot-reload=false`). Les changements sur les champs dynamiques (chemins, paramètres) sont automatiques. Les changements sur les champs statiques (liés au réseau : `Port`, `Address`, `Cert`, etc.) signalent un Warning et nécessitent un redémarrage.
- **Définition** : Ajouter des nouveaux paramètres implique de modifier `AppConfig` et de renseigner les tags associés (`json`, `yaml`, `env`, `flag`, `default`, `desc`). Tous les flags booléens supportent automatiquement le préfixe `--no-` pour la désactivation (ex: `--no-hot-reload`). Utilisez le symbole `#` dans le tag `flag` pour marquer un champ comme **statique** (nécessitant un redémarrage, ex: `flag:"#port|p"`).

## 6. Structure de Fichiers
- `/modules` : Logique métier native exposée au JS.
- `/plugins` : Extensions système (`httpserver` est le wrapper du serveur HTTP, `require` gère les modules).
- `/processor` : Logique de parsing et d'exécution des templates.
- `/doc` : Documentation technique détaillée.

## 8. Interopérabilité et Visibilité des Ressources
- **Enregistrement Global** : Tout module créant une ressource persistante (ex: `CRUD` ouvrant une DB) **DOIT** enregistrer cette ressource dans le registre global correspondant (ex: `db.RegisterConnection`) pour permettre aux autres modules (ex: `MQTT STORAGE`) d'y accéder par nom.
- **Multiplexage Multi-Protocoles** : Lors du développement de nouveaux handlers pour le bloc `TCP` (ex: `MQTT`), utiliser systématiquement l'API `EstablishConnection` ou une injection de socket respectant le peeking (`PeekedConn`) pour éviter la perte des octets initiaux du handshake.
- **Robustesse des Tests** : Pour les tests d'intégration impliquant des bases de données de persistence et des communications réseau, utiliser un timeout minimum de **5 à 10 secondes** pour absorber les latences environnementales sans compromettre la fiabilité.
- **Stratégie de Migration (Dual Struct)** : Pour éviter les panics GORM liés aux types dynamiques non-nommés (Segmentation Violation lors de la création de tables avec relations), implémenter systématiquement une "Dual Struct Strategy".
    - **Principe** : Générer deux types de structs via réflexion : un pour la migration (sans les champs d'association/shadow fields) et un pour le runtime (avec les relations et types Goja).
    - **Migration en Bloc (Bulk)** : Ne jamais appeler `AutoMigrate` sur un seul modèle à la fois. Enregistrer tous les types au préalable et appeler `conn.AutoMigrate()` globalement pour permettre à GORM de résoudre l'ordre des FK.

**Exemple de Dual Struct Implementation :**

```go
func (m *Model) buildStructType(forMigration bool) reflect.Type {
    var fields []reflect.StructField
    // ... Ajout des colonnes de base (ID, Timestamps) ...

    for _, field := range m.Schema.Paths {
        if field.Ref != "" {
            // [IMPORTANT] En mode migration, on ne crée QUE la colonne de FK (string/int)
            // On saute les shadow fields (ex: UserRef) qui causent des panics
            // car ils utilisent des types anonymes créés à la volée.
            if forMigration {
                fields = append(fields, reflect.StructField{
                    Name: ToCamelCase(field.Name),
                    Type: reflect.TypeOf(""), // Type de la colonne simple
                    Tag:  reflect.StructTag(fmt.Sprintf(`gorm:"column:%s"`, field.Name)),
                })
                continue
            }
            // En mode runtime, on ajoute les relations complexes pour l'usage JS...
        }
    }
    return reflect.StructOf(fields)
}
```

