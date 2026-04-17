# Spécifications et Roadmap du Projet HTTP-Server

Ce document définit les tâches prioritaires pour l'évolution du projet.

## 📋 Tâches à accomplir

### 1. Finalisation du Binder
- [x] Implémentation de la logique de Binder pour les protocoles de base.
- [x] Support des Binders écrits en JavaScript pour une extensibilité maximale (Approach: isolated functions & Node-style Duplex).
- [x] **[Technique]** Implémenter la méthode `Handle()` dans `JSDirective` pour permettre le traitement des sockets en JS.
- [x] **[Technique]** Refonte de l'architecture SSE avec un système de Sharded Hub et Ring Buffers pour haute performance (1M+ connexions).
- [x] **[Technique]** Intégration du support WebSocket bidirectionnel couplé au Hub SSE.
- [x] **[Technique]** Intégrer la nouvelle architecture SSE/WS dans `HTTPDirective` pour supporter les événements dans les routes Binder.
- [x] **[Technique]** Directive `WORKER [js_file] [KEY=VALUE...]` : exécute un script JS en arrière-plan avec injection des `config` (args worker) et `settings` (directives `SET`).
- [x] **[Technique]** Gestion précise de l'environnement process : `ENV SET`, `ENV REMOVE`, `ENV DEFAULT`, `ENV [filepath]`, `ENV PREFIX`.
- [x] **[Technique]** Directive `SET [KEY] [VALUE]` pour la configuration interne (objet global `settings` en JS), distincte des variables d'env process.
- [x] **[Technique]** Support des regex avec groupes de capture (`$1`, `$2`) pour `REWRITE` et `REDIRECT`.
- [x] **[Technique]** Conditions JS booléennes pour `REWRITE` et `REDIRECT` (`js_cond` : accès au contexte via `Method()`, `Get()`, `Path()`, `IP()`, `Hostname()`).
- [x] **[Technique]** Syntaxe canonique `REDIRECT [code] pattern sub [js_cond]` — code optionel **avant** le pattern. Tokeniseur quote-aware (`tokenizeLine`) pour préserver les guillemets internes dans les conditions JS.
- [x] **[Technique]** `ERROR` inline JS par défaut (sans type), type explicite (`TEMPLATE`, `HEX`...), et fichier externe (`HANDLER`, `FILE`) — tokeniseur quote-aware utilisé.
- [x] **[Technique]** Support exhaustif et standardisé de tous les types de handler (`TEMPLATE`, `HEX`, `BASE64`, `BASE32`, `BINARY`, `TEXT`, `JSON`, `HANDLER/FILE`, JS inline) pour les directives de routage HTTP (`GET`, `POST`, `PATCH`, etc.) via tokeniseur quote-aware.
- [x] **[Technique]** `processor.ProcessString` pour les templates inline (Mustache + JS) avec injection de `settings`.
- [x] **[Technique]** `SSE` inline auto-injecte `const sse = require("sse")`.
- [x] **[Technique]** Support SSL/TLS manuel (`SSL [key] [cert]`) et automatique Let's Encrypt (`SSL AUTO`) dans `HTTPDirective`.
- [x] **[Technique]** Multiplexage de protocoles sur le même port (blocs imbriqués ex. `HTTP` + `DTP` dans `TCP` ou `UDP`).
- [x] **[Technique]** Protocole `DTP` : integration native TCP/UDP avec bridge automatique vers le Hub SSE (`dtp.device.<id>`).
- [x] **[Technique]** `SubTypeFromString` : support des chaines hexadécimales (ex: `0x01`) pour les subtypes DTP.
- [x] **[Technique]** Directive `AUTH` universelle : support multi-format (JSON, YAML, CSV, USER...) et scriptable (JS `allow()`/`reject(msg...)`) avec support **Bcrypt** et canaux bufferisés.
- [x] **[Technique]** Module `dtp` en JavaScript : client complet avec `newClient`, `connect`, `on`, `sendData`, `ping`, `disconnect`.
- [x] **[Technique]** Multiplexage intelligent : optimisation pour un protocole unique sur un port (évite le timeout de peeking).
- [x] **[Technique]** Centralisation de la résolution de contenu via `RouteConfig.Content()` pour tous les protocoles.
- [x] **[Technique]** Injection automatique de `Content-Type: text/html` pour les handlers JS HTTP par défaut.
- [x] **[Technique]** Support de la directive `QUEUE` dans le bloc DTP.
- [x] **[Technique]** Support des Route Groups récursifs avec `GROUP [path] DEFINE`.
- [x] **[Technique]** `REGISTER PROTOCOL [NAME] [file]` pour les protocoles JS custom.
- [x] **[Technique]** Directive `INCLUDE [filepath]` pour l'inclusion récursive de fichiers Binder avec détection de récursivité (fatal error).
- [x] **[Technique]** Gestion des erreurs HTTP 405 (Method Not Allowed) dans le `FsRouter`.
- [x] **[Technique]** Support du protocole Socket.IO unifié via la méthode `IO`.
- [x] **[Technique]** Support des layouts hiérarchiques (`_layout.html`, _layout.js) avec héritage et injection de contenu.
- [x] **[Technique]** Support des fichiers partiels (`.partial.[html|js]`) pour bypasser les layouts.
- [x] **[Sécurité]** Implémentation d'une couche de sécurité de niveau 4 (SYN/Accept pour TCP, Packet-level pour UDP) : directive `SECURITY` avec support `CONNECTION` (Rate, IP, Geo) et `GEOJSON`.
- [x] **[Sécurité]** Protection par défaut (Baseline) de 100r/s (burst 10) appliquée globalement à TOUS les protocoles (TCP, UDP, HTTP, DTP).
- [x] **[Sécurité]** Surcharge de la politique globale via l'argument `[default]` dans un bloc `SECURITY`.
- [x] **[Sécurité]** Intégration du WAF Coraza (L7) avec support des directives `@WAF`, `@IP`, `@GEO`, `@BOT`, `@AUDIT`.
- [x] Documentation complète de toutes les directives dans `doc/BINDER.md`, `doc/WAF.md`, `RULES.md`, `README.md`.
- [x] Exemples de tests dans `examples/` : `test_all_features.bind`, `multiplex_test.bind`, `rewrite_test.bind`, `security_geojson_*.bind`, `security_default_override.bind`.

### 1b. Module de Paiement
- [x] **[Technique]** Directive `PAYMENT` : intégration native Stripe, Mobile Money (MTN/Orange) et providers custom via DSL.
- [x] **[Technique]** Standard X402/Crypto : Intégration de paiements crypto via facilitation native.
- [x] **[Technique]** Opérations `CHARGE`, `VERIFY`, `REFUND`, `CHECKOUT`, `USSD`/`PUSH` avec scripts JS inline/fichier.
- [x] **[Technique]** Webhooks (`@PRE`/`@POST`) avec validation de signature et détection d'utilisateur dynamique.
- [x] **[Technique]** API JavaScript `require('payment')` avec gestion multi-connexions et calculs automatiques.
- [x] Documentation complète dans `doc/PAYMENT.md`.

### 2. Custom Logs (Vhost & Server Wrapper)
- [x] Dans `plugins/httpserver`, ajout de la configuration des messages de log personnalisés par instance/vhost.
- [x] Support de la redirection des flux (Stdout/Stderr/AccessLog) vers des fichiers spécifiques.

### 3. Tests et Validation
- [x] Résolution des conflits d'environnement JS (require, buffer) et stabilisation des tests `storage` et `sse`.
- [x] Vérification globale du codebase compilé après l'intégration des WebSockets et du Sharded Hub.
- [x] Tests manuels : `WORKER`, `SET`, `ENV SET/DEFAULT/REMOVE`, SSL, `REWRITE`/`REDIRECT` regex, multiplexage port unique.
- [x] Tests unitaires pour le parser Binder (`modules/binder/parser_test.go`).
- [x] Suite de tests exhaustifs pour les fonctionnalités de Virtual Hosting.
- [x] Tests de robustesse pour le multiplexage (détection de protocoles, timeouts).
- [x] Tests de charge et de performance (jusqu'à 1M+ connexions SSE/WS).
- [x] Validation intégrale de l'API Socket.IO native (`IO`) : Tests de routing HTTP (HTTP 426 Upgrade), registre bidirectionnel et propagation JSON Hub.
- [x] **[CRUD]** Propagation en temps réel de toute l'activité via SSE (channels hiérarchiques).
- [x] **[CRUD]** Support des diffs et snapshots `prev` dans les événements SSE `update`.
- [x] **[CRUD]** Endpoints `/changes` sécurisés pour un monitoring granulaire (NS, schéma, doc).
- [x] **[CRUD]** Interface d'administration HTMX (`/_admin`) intégrée (templates embarqués, JS, rendu natif).

### Generic Routes & Middlewares

All protocols (HTTP, DTP, MQTT) now support a unified route registration system.

#### Special Methods

This methods are not standard HTTP methods and are only available for HTTP protocol.

| PPROF | Profiling (GET only) | `PPROF /debug/pprof` |
| HEALTH | Health check endpoint | `HEALTH /health` |
| STATIC | Static file/dir or virtual file | `STATIC @CORS /public ./assets` |
| ROUTER | Static directory (intended for SPA) | `ROUTER /app ./dist` |

#### Arguments (`[ARGS...]`)
Routes support trailing arguments in brackets for specific configuration:
`[METHOD] [USE...]? [path] [TYPE]? [filepath]? [ContentType]? [key=value key2=value2 ...]?`

**Common Arguments for `STATIC` / `ROUTER`**:
- `indexName`: Names of index files (comma separated). Default: `index.html`.
- `browse`: Enable directory browsing (`true`/`false`).
- `compress`: Enable response compression (`true`/`false`).
- `byteRange`: Enable byte range requests (`true`/`false`).
- `download`: Enables direct download (`true`/`false`).
- `cache`: Expiration duration for cache (e.g., `10m`, `1h`, `0` for no cache).
- `maxAge`: Max-Age header in seconds (as duration, e.g., `3600s`).

```hcl
[METHOD] [@Middleware[args]]* [path]? [TYPE]? [ContentType]?
```

#### Named Middlewares (`@`)
Middlewares are applied in order before the route handler. Arguments support standard quoting (``,"",'') and escaping.

| Middleware | Description | Example |
|---|---|---|
| `HELMET` | Security headers (HSTS, CSP, etc.) | `@HELMET[xss=1]` |
| `CORS` | Cross-Origin Resource Sharing | `@CORS[origins="*"]` |
| `LIMITER` | Rate limiting | `@LIMITER[max=10 expiration="1m"]` |
| `ADMIN` | Built-in Auth protection | `@ADMIN[redirect="/auth" basic]` |
| `SESSION` | Enforce session presence | `@SESSION` |
| `CSRF` | Cross-Site Request Forgery protection | `@CSRF[name="_csrf"]` |
| `IDEMPOTENCY` | Fault-tolerant APIs | `@IDEMPOTENCY` |
| `ETAG` | Cache validation | `@ETAG` |
| `TIMEOUT` | Request timeout | `@TIMEOUT[expiration="5s"]` |
| `CONTENTTYPE` | MIME-type enforcement | `@CONTENTTYPE[type="application/json"]` |

#### The `MIDDLEWARE` Directive
Global middlewares are registered using the `MIDDLEWARE` command.

1. **Named Middlewares**: If it contains `@Middleware`, it **MUST** be single-line.
   `MIDDLEWARE @HELMET @CSRF @CORS`
2. **JavaScript Middlewares**: If it has **NO** `@...` tokens, it can use blocks or files.
   - `MIDDLEWARE auth.js`
   - `MIDDLEWARE BEGIN ... END MIDDLEWARE`

#### Authentication Hashing
The `AUTH` directive supports multiple hashing algorithms via the `{ALG}hash` prefix:
- `{BCRYPT}` (default if `$2a$` prefix found)
- `{SHA512}`, `{SHA256}`, `{SHA1}`, `{MD5}`
- Supported encodages for the hash: **Hex**, **Base32**, **Base64**.

Example:
```hcl
AUTH USER "admin" "{SHA256}K7gNU3sdo+OL0wNhqoVWhr3g6s1xYv72ol/pe/Unols="
```
### 4. Architecture & Refactoring Central
- [x] **[Technique]** Refonte complète de la gestion de la configuration (`plugins/config`). Chargement hiérarchique centralisé, hot-reload, support des flags négatifs `--no-xxx` et détection automatique des champs statiques via réflexion (symbole `#`).
- [x] Nettoyage de `main.go` et extraction de la logique de parsing `pflag`.
- [x] **[Technique]** Centralisation de la résolution de contenu via `RouteConfig.Content()` et passage au tout binaire (`[]byte`).

### 5. Support MQTT & IoT
- [x] Lancement de l'intégration et de la compilation d'un broker MQTT 3.1.1/5.0 natif sur TCP et WebSocket.
- [x] Unification globale MQTT ↔ Hub SSE (`ON_PUBLISH`).
- [x] Implémentation du système complet de Hooks Dynamiques via JS (Authentification, ACL, Events).
- [x] Bridge asynchrone intégré (`BRIDGE [url] [t1, t2]`).
- [x] Persistance QoS 1/2 en base de données native via GORM (`STORAGE [DBType]`).
- [x] **[Sécurité]** Couche WAF globale appliquée à la poignée de main TCP (`SECURITY`) utilisant un sniffing non-destructif (`bufio.Peek`).
- [x] **[Stabilité]** Refonte de l'injection de connexion MQTT : passage à l'API native `EstablishConnection` pour éliminer les race conditions et les proxys TCP intermédiaires.
- [x] **[Test]** Suite de tests d'intégration isolée (`t.TempDir()`) avec ports dynamiques et validation GORM atomique.
- [x] Enregistrement dynamique des protocoles (`MQTT`, `DATABASE`, `MAIL`, `DTP`) dans le parser via le `Manager`.
- [x] Support de la persistence QoS 1/2 inter-module : les connexions DB créées par `DATABASE` ou `CRUD` sont enregistrées globalement.

### 6. Documentation IA-Friendly
- [x] Rédaction d'une documentation technique structurée pour les agents IA.
- [x] Création de guides d'exemples clairs.
- [x] Ajout de descriptions détaillées pour les URLs.
- [x] Support du protocole MCP (Model Context Protocol) via injection de schémas.
- [x] **[Technique]** Stratégie de migration "Dual Struct" : séparation des schémas de migration et des modèles de runtime pour éviter les panics GORM.
- [x] **[Technique]** Migration en bloc (Bulk Migration) : résolution automatique des dépendances de clés étrangères.
- [x] **[Technique]** Support complet des relations (`has=one`, `many`, `many2many`) et contraintes (`OnDelete`, `OnUpdate`) dans le DSL `SCHEMA`.
- [x] **[Technique]** Unification des protocoles `DATABASE` et `CRUD`.
- [x] **[Technique]** Stabilisation finale du Runtime Temps Réel : thread-safety (SafeWrite), prévention des boucles via ConnID (loop filtering) et API événementielle JS unifiée (`onMessage`, `onClose`, `onError`).
- [x] **[Technique]** Priorisation des événements : système de canaux prioritaires dans le JS runtime pour garantir l'exécution des hooks de cycle de vie (`onClose`) même en cas de saturation.
- [x] **[Technique]** Hub Isolation & Reset : mécanisme de Reset pour les suites de tests et isolation robuste des shards.

### 7. Site Web du Projet
- [ ] Création d'un nom de domaine en `.js`
- [ ] Création d'une documentation en ligne moderne et dynamique.
- [ ] Création d'une page vitrine
- [ ] Intégration d'exemples interactifs.

## 📚 Documentation et Standards

### Standards de Codage
Pour tout développement sur le projet (JS, HTML), se référer au fichier suivant :
- [RULES.md](RULES.md) : Définit les normes de structure, de logging, de gestion d'erreurs et les bonnes pratiques pour les IA et les développeurs.

### Cartographie de la Documentation
Voici la liste des fichiers de documentation et leur utilité :

- [README.md](README.md) : Point d'entrée principal. Présentation globale, installation et exemples rapides.
- [SPECS.md](SPECS.md) (ce fichier) : Roadmap, tâches en cours et vision du projet.
- [doc/CLI.md](doc/CLI.md) : Manuel d'utilisation de l'interface en ligne de commande (flags, arguments).
- [doc/BINDER.md](doc/BINDER.md) : Guide de configuration du multiplexeur de protocoles via les fichiers `.bind`. Référence complète de toutes les directives.
- [doc/DATABASE.md](doc/DATABASE.md) : Documentation du module DB (API Mongoose, Drivers, Migrations).
- [doc/JS_SCRIPTING.md](doc/JS_SCRIPTING.md) : Fonctionnement de l'interpréteur JavaScript et des balises `<script server>`.
- [doc/TEMPLATING.md](doc/TEMPLATING.md) : Guide du moteur de rendu hybride PHP-style et Mustache.
- [doc/STORAGE.md](doc/STORAGE.md) : Utilisation des modules de stockage persistants et des sessions.
- [doc/DTP.md](doc/DTP.md) : Manuel du protocole IoT DTP.
- [doc/MQTT.md](doc/MQTT.md) : Guide du broker MQTT intégré.
- [doc/VHOST.md](doc/VHOST.md) : Architecture Master-Worker pour l'hébergement multi-sites (Virtual Hosts).
- [doc/AUTH.md](doc/AUTH.md) : Système d'authentification unifié (Basic, Bcrypt, SHA, scripts JS).
- [doc/CRUD.md](doc/CRUD.md) : Module CRUD complet avec SSE temps réel, endpoints `/changes`, et API JS.
- [doc/IO.md](doc/IO.md) : Module Socket.IO unifié avec le Hub SSE/WS/MQTT.
- [doc/PAYMENT.md](doc/PAYMENT.md) : Module de paiement (Stripe, MoMo, providers custom).

---
*Dernière mise à jour : 15 Avril 2026*
