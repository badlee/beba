# Binder Configuration (.bind)

Le format `.bind` permet de définir des serveurs réseaux, des protocoles et du routage de manière déclarative.

---

## Structure Globale

```hcl
REGISTER PROTOCOL [NAME] "[js_file]"   // Enregistre un protocole JS custom (optionnel, niveau fichier)

DATABASE [provider_url]                // Connexion DB (SQLite, MySQL, Postgres, ':default:')
PAYMENT [provider_url]                 // Connexion Paiement (Stripe, MoMo, X402)
SECURITY [name]                        // Profil de sécurité réutilisable

AUTH [name] DEFINE                     // Enregistre un gestionnaire d'authentification global
    SECRET "my-secret"                 // Configuration globale
    DATABASE "sqlite://auth.db"

    // + Définition des stratégies locales (USER, USERS, AUTH CSV, AUTH BEGIN...)
    // + Définition des clients OAuth2 (STRATEGY google DEFINE...)
    
    SERVER DEFINE                      // Beba comme Provider OAuth2
        TOKEN_EXPIRATION "1h"
        ISSUER "beba-auth"
        LOGIN "./public/login.html"    // Interface personnalisée optionnelle
    END SERVER
END AUTH

[PROTOCOL] [address]                   // Groupe d'écoute (TCP, UDP, HTTP, HTTPS, MQTT, MAIL...)
    // Si imbriqué dans TCP, [address] est optionnel (hérité du parent)
    DISABLE [TYPE] [FEATURE] 
    // -- Configuration & Environnement --
    ENV PREFIX [prefix]                // Préfixe pour les vars d'env (défaut: APP_)
    ENV [filepath]                     // Charge un fichier .env
    ENV SET [KEY] [VALUE]              // Définit une variable d'environnement
    ENV DEFAULT [KEY] [VALUE]          // Définit si non définie
    ENV REMOVE [KEY]                   // Supprime une variable d'environnement
    CONF [JSON|YAML|TOML|ENV] [filepath] // Charge un fichier de configuration typé
    CONF [filepath]                    // Charge par auto-détection via extension
    SET [KEY] [VALUE]                  // Définit une paire clé/valeur de configuration interne

    // -- SSL / TLS --
    SSL AUTO [domain] [admin_email]    // Certificat Let's Encrypt (ACME)
    SSL [key_path] [cert_path]         // Certificat manuel

    // -- Workers en Arrière-Plan --
    WORKER [js_file] [KEY=VALUE ...]   // Exécute un script JS en arrière-plan
    WORKER [KEY=VALUE ...]   // Exécute un script JS en arrière-plan
        /* JS code */
    END WORKER

    // -- Global Middlewares --
    MIDDLEWARE @HELMET @CSRF @CORS[origins="*"] // Monoligne pour les middlewares nommés
    MIDDLEWARE BEGIN                            // Bloc pour JS inline (sans @)
        console.log("Global middleware hit");
    END MIDDLEWARE
    MIDDLEWARE auth.js                          // Fichier pour JS externe (sans @)

    // -- Flux & Routage --
    PROXY [path] [remote_url]                         // Proxy HTTP/WS simple
    PROXY [TYPE] [path] [remote_url]                  // Proxy typé (HTTP, WS)
    REWRITE [pattern] [substitution]                  // Réécriture interne (regex, groupes $1 $2...)
    REWRITE [pattern] [substitution] [js_cond]        // Avec condition JS booléenne
    REDIRECT [pattern] [substitution]                 // Redirection 302 (sans code ni condition)
    REDIRECT [code] [pattern] [substitution]          // Avec code HTTP (301, 302, 307...)
    REDIRECT [code] [pattern] [substitution] [js_cond] // Avec code et condition JS
    STATIC [path] [dir_or_file]                       // Fichiers statiques
    ROUTER [path] [views_dir]                         // Routeur de vues templates

    // -- Gestion d'Erreurs --
    // Inline JS par défaut (pas de type explicite)
    ERROR [code]                                      // code optionel
        /* JS Code */
    END ERROR
    // Inline avec type et ContentType explicites
    ERROR [code] [TEMPLATE|HEX|BASE64|BASE32|BINARY|TEXT|JSON] [contentType]
        /* Contenu selon le type */
    END ERROR
    // Fichier externe (HANDLER, FILE, TEMPLATE + chemin)
    ERROR [code] HANDLER [filepath] [contentType]
    ERROR [code] FILE [filepath] [contentType]        // alias HANDLER
    ERROR [code] TEMPLATE [filepath] [contentType]

    // -- Authentication --
    // Montage d'un gestionnaire global:
    AUTH [name] [path]                 // Monte le gestionnaire d'authentification [name] sur la route [path] (ex: /api/auth)
    
    // Déclarations en ligne (dépréciées au profit du gestionnaire global) :
    AUTH [JSON|YAML|TOML|ENV] [filepath] // Fichier clé/valeur pour user:pwd
    AUTH CSV [filepath]               // Fichier CSV "username;pwd;[proto_bool]"
    AUTH USER [USER_NAME] [PWD]        // Déclaration utilisateur unique
    AUTH [KEY=VALUE ...]               // Authentification scriptée JS
        /* JS code */
    END AUTH

    // -- Événements (HTTP/DTP) --
    EVENT [NAME] [KEY=VALUE ...]       // Événement inline
        /* JS Code */
    END EVENT
    ON [NAME] HANDLER [filepath] [KEY=VALUE ...] // Alias, version fichier

    // -- Protocoles Imbriqués (Multiplexage) --
    HTTP                               // Bloc HTTP imbriqué dans TCP
        [directives HTTP...]
        // AUTH est aussi supporté ici
    END HTTP

    DTP                                // Bloc DTP natif
        // Directives DTP spécifiques:
        AUTH CSV "devices.csv"         // Recommandé pour la gestion des appareils
        DATA [subtype] HANDLER [js]    // Handler pour DATA (subtype: nom ou hex) - Recommandé pour capteurs
        EVENT [subtype] HANDLER [js]   // Handler pour EVENT (alertes, événements d'état)
        ACK [subtype] HANDLER [js]     // Handler pour ACK
        NACK [subtype] HANDLER [js]    // Handler pour NACK
        ERR [subtype] HANDLER [js]     // Handler pour ERROR
        ONLINE                         // Tracking de status session (publie vers SSE)
        QUEUE                          // Gère la mise en file d'attente des messages pour les appareils hors ligne ou en mode "synchronisation".
    END DTP

    // -- Syntaxe Globale Des Méthodes --
    
    // Syntaxe globale des methodes (non-inline : [TYPE] et [filepath] optionnels)
    // [METHOD] [middlewares...]? [path_or_name] [JSON|YAML|TOML|ENV|TEMPLATE|HANDLER|FILE|BINARY|BASE32|BASE64|HEX]? [filepath]? [ContentType]? [arguments...]?
    
    // Fichiers statiques et Handlers JS externes 
    [METHOD] [path_or_name] [JS filepath] [ContentType]          // Script externe
    [METHOD] [path_or_name] HANDLER [JS filepath] [ContentType]  // Script externe explicite
    [METHOD] [path_or_name] TEMPLATE [Html filepath] [ContentType] // Render Template html
    [METHOD] [path_or_name] FILE [filepath] [ContentType]        // Sert un fichier
    
    // Syntaxe globale des methodes (inline)
    // [METHOD] [middlewares...]? [path_or_name] [TYPE]? [ContentType]? [arguments...]?
    //    // Contenu inline depend du type
    // END [METHOD]
    
    [METHOD] [path_or_name] [ContentType] BEGIN
        // JS CODE INLINE (par défaut)
    END [METHOD]
    
    [METHOD] [path_or_name] JSON BEGIN               // JSON inline
        {"key": "value"}
    END [METHOD]
    
    [METHOD] [path_or_name] HEX [ContentType] BEGIN  // HEX inline
        FF8800BEEF...
    END [METHOD]
    // ... BASE64, BASE32, BINARY, TOML, YAML, TEXT, etc.

    // Syntaxe globale des groupes de méthodes (Routes Imbriquées)
    // GROUP [middlewares...]? [path_or_name] DEFINE [arguments...]?
    //     // liste des méthodes d'enfant (inline, sous-groupes, non-inline)
    // END GROUP
    
    GROUP [path_or_name] DEFINE
        // Sous-routes imbriquées dynamiquement (HTTP/HTTPS)
    END GROUP
    // Syntaxe globale des route inline
    [METHOD] [middlewares...]? [path_or_name] [TYPE]? [ContentType]? BEGIN [arguments...]?
    // Contenu inline depend du type
    END [METHOD]
    // Syntaxe globale des route non-inline
    [METHOD] [middlewares...]? [path_or_name] [TYPE]? [filepath]? [ContentType]? [arguments...]?
    // Syntaxe globale des groupe de routes
    [METHOD] [middlewares...]? [path_or_name] DEFINE [arguments...]?
        // contient 0 ou plusieurs methodes (inline, group, non-inline)
        [METHOD] [middlewares...]? [path_or_name] [TYPE]? [ContentType]? [arguments...]?
            // Contenu inline depend du type
        END [METHOD]
        [METHOD] [middlewares...]? [path_or_name] [TYPE]? [filepath]? [ContentType]? [arguments...]?
        //... 
        // Sous-routes imbriquées dynamiquement
    END [METHOD]
    

    // -- Handlers Spécifiques au runtime --
    SSE [path]                          // Server-Sent Events passif (recois les events, uniquement des channels enregistre (?channel=... ou ?channels=...))
    SSE [path] HANDLER [filepath]       // Server-Sent Events fichier
    SSE [path] BEGIN                    // Server-Sent Events inline
        /* JS Code */
    END SSE
    WS [path]                          // WebSocket passif (recois les events, uniquement, et ne publie que sur les channels enregistre (?channel=... ou ?channels=...) ou si rien n'est enregistre alors publie sur global), la structure d'un message est : {id: string, channel: string, data: any /* any serialized json data */}
    WS [path] HANDLER [filepath]        // WebSocket fichier
    WS [path] BEGIN                     // WebSocket inline
        /* JS Code */
    END WS
    IO [path]                          // Socket.IO passif (recois les events, uniquement, et ne publie que sur les channels enregistre (?channel=... ou ?channels=...))
    IO [path]  HANDLER [filepath]       // Socket.IO ficher
    IO [path] BEGIN                     // inline handler
        /* JS Code */
    END IO
    MQTT [path]                          // MQTT Over Websocket passif (recois les events, uniquement, et ne publie que sur les channels enregistre (?channel=... ou ?channels=...))
    MQTT [path]  HANDLER [filepath]     // MQTT Over Websocket ficher
    MQTT [path] BEGIN                   // inline handler
        /* JS Code */
    END MQTT
END [PROTOCOL]
```

---

## Exemples Avancés

### Exemple d'Imbrication de Directives (`DEFINE`)
La directive `DEFINE` permet de cibler n'importe quel constructeur et transformer la directive courante en un parent qui englobera ses propres directive enfants. 

```hcl
DATABASE 'postgres://user:pass@localhost:5432/mydb' [default]
    NAME myapi
    SCHEMA user DEFINE
        FIELD name string [required]
        FIELD email string [required]
        FIELD age number
        FIELD roles array
        VIRTUAL fullName
            done(this.firstName + ' ' + lastName); 
        END VIRTUAL
        PRE save
            console.log('Saving ' + name);
        END PRE
        METHOD sayHello
            done("Hello, I am " + name);
        END METHOD
        STATIC findByEmail
            done(findOne({ email: email }));
        END STATIC
    END SCHEMA
END DATABASE
```

---

## Référence des Directives

### `INCLUDE`

Inclus le contenu d'un autre fichier `.bind` à l'endroit de l'appel. Les inclusions sont récursives et les chemins relatifs sont résolus par rapport au fichier parent.

```hcl
INCLUDE "auth_rules.bind"
INCLUDE "routes/api.bind"
```

> [!CAUTION]
> Une détection de récursivité circulaire est intégrée. Si une boucle d'inclusion est détectée, le serveur lèvera une erreur fatale au démarrage.

---

### `REGISTER PROTOCOL`

Enregistre un protocole réseau custom défini en JavaScript. Doit apparaître **avant** les groupes qui l'utilisent.

```hcl
REGISTER PROTOCOL DTP "protocols/dtp.js"
```

> Le fichier JS doit exposer deux fonctions globales : `match(buffer)` et `handle()`.

### `DISABLE [TYPE] [FEATURE]`
Allows programmatically deactivating core features or protocols. Features are cached for high-performance lookups.

| Type | Feature | Description |
|---|---|---|
| `DEFAULT` | `API` | Disables **all** default endpoints (CRUD + Realtime). |
| `DEFAULT` | `CRUD` | Disables only the auto-generated database REST API (`/api/*`). |
| `DEFAULT` | `DATABASE` | Alias for `CRUD`. Disables the database REST API. |
| `DEFAULT` | `REALTIME` | Disables only the Realtime/SSE/WS endpoints (`/api/realtime/*`). |
| `ADMIN`   | `UI`       | Disables the administration dashboard on `/_admin`. |

```hcl
HTTP 0.0.0.0:8080
    DISABLE DEFAULT API      // Disables both CRUD and Realtime default endpoints
    DISABLE DEFAULT REALTIME // Disables only the Realtime /api/realtime endpoints
END HTTP
```

### `SECURITY [name] [default?]`
Définit un profil de sécurité (WAF/Network) réutilisable par d'autres protocoles ou appliqué globalement.
- **`[default]`** : Si présent, définit cette politique comme la protection par défaut du serveur (Baseline).

Cette directive est critique car elle est appliquée à **chaque acceptation de socket** (`net.Accept`), avant même le multiplexage des protocoles.

```hcl
SECURITY my_global_rules [default]
    CONNECTION RATE 100r/s 1s burst=10
    CONNECTION DENY "blacklist.txt"
    GEOJSON restricted_zone "data/restricted.geojson"
    CONNECTION ALLOW restricted_zone
END SECURITY
```
Pour plus de détails, voir [doc/WAF.md](WAF.md) et [doc/SECURITY.md](SECURITY.md).

### Sécurité Multi-couches (`SECURITY`)

Le serveur implémente une défense en profondeur à 5 couches. Pour un guide complet sur le filtrage réseau (L1), le WAF (L3), la détection de bots (L4) et l'audit signé (L5), consultez le **[Guide de Sécurité (SECURITY.md)](./SECURITY.md)**.

#### Middlewares de Sécurité (@...)

| Directive | Effet | Exemple |
|---|---|---|
| `@SECURITY[name]` | Applique un profil `SECURITY` nommé (WAF + L1). | `GET @SECURITY[api_waf] "/"` |
| `@BOT[...args]` | Active la détection de bots et les JS challenges. | `@BOT[js_challenge=true]` |
| `@AUDIT[...args]` | Active la journalisation signée (HMAC). | `@AUDIT[sign=true]` |
| `@CONTENTTYPE[ext]` | Force un type MIME spécifique pour la requête. | `@CONTENTTYPE[application/json]` |
| `@UNSECURE` | Désactive le WAF global pour cette route. | `GET @UNSECURE "/health"` |
| `@AUTH[...args]` | Active l'authentification (Basic/Bearer/Script). | `@AUTH[redirect="/login"]` |

---

### Gestion de l'Environnement (`ENV`)

Ces directives manipulent les variables d'environnement du **processus**. Les variables sont préfixées par `ENV PREFIX` (défaut : `APP_`).

| Directive | Effet |
|---|---|
| `ENV PREFIX [prefix]` | Définit le préfixe (ex: `APP_`, `MY_APP_`). Défaut: `APP_`. |
| `ENV [filepath]` | Charge un fichier `.env` (format `KEY=VALUE`). |
| `ENV SET [KEY] [VALUE]` | Définit `APP_KEY=VALUE` (écrase si existante). |
| `ENV DEFAULT [KEY] [VALUE]` | Définit `APP_KEY=VALUE` **seulement si elle n'existe pas déjà**. |
| `ENV REMOVE [KEY]` | Supprime `APP_KEY` de l'environnement du processus. |

Les variables sont accessibles en JS via `process.env.APP_MY_KEY`.

**Exemple :**
```hcl
HTTP 0.0.0.0:8080
    ENV PREFIX APP_
    ENV .env.prod
    ENV SET DB_HOST "localhost"
    ENV DEFAULT LOG_LEVEL "info"
    ENV REMOVE OLD_API_KEY
END HTTP
```

---

### Configuration Interne (`SET`)

`SET` définit des paires clé/valeur de **configuration interne**, distincts des variables d'environnement. Ces valeurs ne modifient pas l'environnement du processus.

```hcl
SET [KEY] [VALUE]
```

- Accessibles dans tous les scripts JS via l'objet global **`settings`**.
- Les valeurs peuvent être entre guillemets simples `'...'`, doubles `"..."` ou backtick `` `...` ``.

**Exemple :**
```hcl
HTTP 0.0.0.0:8080
    SET API_VERSION "2.1"
    SET MAX_RETRIES 3

    GET "/info"
        context.JSON({ version: settings.API_VERSION, maxRetries: settings.MAX_RETRIES });
    END GET
END HTTP
```

---

### Configuration Fichier (`CONF`)

Charge un fichier de configuration structuré. Les données sont mergées dans la configuration de l'application.

```hcl
CONF JSON config/app.json
CONF YAML config/app.yaml
CONF config/app.toml    // auto-détection par extension (.json, .yaml, .yml, .toml, .env)
```

---

### Workers (`WORKER`)

Exécute un script JavaScript en **arrière-plan** (tâche isolée). Peut être répété pour lancer plusieurs workers.

```hcl
WORKER [js_file] [KEY=VALUE] [KEY="VALUE"] [KEY='VALUE'] [KEY=`VALUE`]
```

- **`js_file`** : chemin vers le fichier JS (relatif au fichier `.bind`).
- **`config`** : les paires `KEY=VALUE` sont exposées dans le script via l'objet global `config`.
- Les variables de `SET` sont accessibles via l'objet global `settings`.
- Les quotes (`"`, `'`, `` ` ``) sont automatiquement supprimées des valeurs.

**Exemple de fichier `.bind` :**
```hcl
HTTP 0.0.0.0:8080
    SET APP_NAME "MyServer"
    WORKER workers/monitor.js INTERVAL=5000 TARGET="https://api.example.com"
    WORKER workers/cleanup.js KEEP_DAYS=7
END HTTP
```

**Exemple de script worker (`workers/monitor.js`) :**
```javascript
// Les configs KEY=VALUE sont disponibles dans `config`
const interval = parseInt(config.INTERVAL) || 10000;
const target = config.TARGET;

// Les valeurs SET sont dans `settings`
print("Worker started for: " + settings.APP_NAME);
print("Monitoring: " + target + " every " + interval + "ms");

// Les workers s'exécutent jusqu'à la fin du script.
// Utiliser setInterval pour des tâches répétitives (voir note ci-dessous).
```

> [!NOTE]
> Les workers s'exécutent dans un contexte de moteur JavaScript isolé. Ils ont accès aux modules natifs (`require`, `print`, etc.) et aux objets `config` et `settings`. Ils n'ont pas accès à l'objet `context` du serveur.

---

### SSL / TLS

```hcl
// Let's Encrypt (automatique)
SSL AUTO example.com admin@example.com

// Certificat manuel (chemin clé, chemin certificat)
SSL key.pem cert.pem
```

> Pour HTTPS, utiliser le protocol `HTTPS` à la place de `HTTP`.

---

### Proxy (`PROXY`)

Délègue les requêtes entrantes à un serveur distant.

```hcl
PROXY /api/realtime http://backend:8080             // Proxy Intelligent (Gère HTTP et WebSocket dynamiquement)
PROXY WS /api/realtime/ws ws://socket-server:9000   // Proxy strictement WebSocket
PROXY HTTP /rpc http://rpc-service:7070             // Proxy strictement HTTP
```

**Comportement Intelligent** :
Si aucun type n'est précisé (1ère ligne), le système gérera à la fois le trafic HTTP standard et interceptera automatiquement les demandes d'Upgrade WebSocket (`ws://` ou `wss://`) pour les proxyfier full-duplex de manière transparente vers la même IP cible. 

**SSL/TLS**
Le proxy ignore les erreurs de certificats SSL/TLS lors du relai vers des cibles HTTPS ou WSS sécurisées par des certificats auto-signés ou expirés (par exemple sur un réseau interne).

---

### Réécriture (`REWRITE`)

Modifie l'URL de la requête **en interne** (le client ne le sait pas). Supporte les **regex** avec groupes de capture (`$1`, `$2`, ...). Une condition JS optionnelle contrôle si la règle s'applique.

```hcl
REWRITE [pattern] [substitution]
REWRITE [pattern] [substitution] [js_cond]
```

Dans `js_cond`, les méthodes du contexte sont directement accessibles : `Method()`, `Path()`, `Get()`, `Query()`, `IP()`, `Hostname()`.

```hcl
# Sans condition (toujours appliqué)
REWRITE "/old/(.*)" "/new/$1"

# Avec condition JS (seulement GET)
REWRITE "/api/v1/(.*)" "/api/v2/$1" Method() === "GET"
```

---

### Fichiers Statiques (`STATIC`)

Sert des fichiers statiques depuis un répertoire ou un fichier unique.

```hcl
STATIC [path] [dir_or_file] [options]
```

- **`options`** : Arguments optionnels entre crochets `[...]`.
  - `indexName` : Noms des fichiers d'index (séparés par des virgules). Défaut : `index.html`.
  - `browse` : Active le parcours de répertoire (`true`/`false`).
  - `compress` : Active la compression Brotli/Gzip (`true`/`false`).
  - `byteRange` : Supporte les requêtes de plage d'octets (`true`/`false`).
  - `download` : Force le téléchargement direct (`true`/`false`).
  - `cache` : Durée d'expiration (ex: `10m`, `1h`, `0` pour aucun).
  - `maxAge` : En-tête Max-Age en secondes (ex: `3600s`).

**Exemple :**
```hcl
STATIC /public "./public" [browse=true cache=10m compress=true]
STATIC /downloads "/var/www/files" [download=true]
```

---

### Routeur de Vues (`ROUTER`)

Système de routage automatique pour les templates HTML/Mustache. Mappe les URLs aux fichiers du répertoire de vues. Supporte le **hot-reload** (ajout/suppression/modification de fichiers détectés en temps réel) et un **cache fichier intelligent** avec TTL configurable.

```hcl
ROUTER [path] [views_dir] [options]
```

**Arguments spécifiques à `ROUTER` :**

| Argument | Type | Description |
|----------|------|-------------|
| `cacheTtl` | duration | Durée de vie des fichiers en cache mémoire. Overrides le flag `--cache-ttl`. Ex: `10m`, `30s`, `1h`. `0` = cache permanent. |
| `exclude` | string | Patterns de fichiers/dossiers à exclure (séparés par des virgules). |

Les autres options sont identiques à ceux de `STATIC` (`indexName`, `browse`, `compress`, `cache`, etc.).

**Exemples :**
```hcl
# Cache fichier de 10 minutes (override du --cache-ttl global)
ROUTER /app "./views" @[cacheTtl="10m"]

# Cache permanent (pas de goroutine de cleanup)
ROUTER / "./pages" @[cacheTtl="0"]

# Avec exclusion de dossiers
ROUTER / "./pages" @[exclude="node_modules,tmp"]
```

> [!NOTE]
> Le hot-reload des routes (ajout/suppression de fichiers `.html`/`.js`) est actif par défaut en mode développement (`--hot-reload`). Les modifications de contenu invalident uniquement le cache ; les changements de structure (nouveaux fichiers, suppressions) déclenchent un rescan automatique avec un debounce de 150ms.
>
> En mode production (`--no-hot-reload`), le cache est permanent et aucun watcher n'est démarré.

---

### Redirection (`REDIRECT`)

Envoie un code HTTP **3xx** au client pour changer d'URL. `code` est optionnel (défaut: 302) et placé **avant** le pattern. Supporte les **regex** avec groupes de capture et une condition JS.

> **Note** : Le server retourne toujours 303 "See Other" pour les redirects GET, conformément à la RFC 7231 §6.4.4

```hcl
REDIRECT [pattern] [substitution]              // 302, sans condition
REDIRECT [code] [pattern] [substitution]       // code explicite
REDIRECT [code] [pattern] [substitution] [js_cond]
```

```hcl
# Permanent (301)
REDIRECT 301 "/promo" "/offres"

# Temporaire (302), regex
REDIRECT 302 "/products/(.*)" "/shop/$1"

# Avec condition JS (redirect seulement si pas de header Authorization)
REDIRECT 302 "/secure/(.*)" "/login?next=/secure/$1" !Get("Authorization")

# Sans code explicite (défaut 302)
REDIRECT "/jump" "/target"
```

---

### Gestion d'Erreurs (`ERROR`)

Intercepte les codes d'erreur HTTP et y répond avec du contenu spécifique. `code` et `type` sont optionnels.

```hcl
// Inline JS par défaut (sans type)
ERROR [code]
    /* JS Code */
END ERROR

// Inline avec type et ContentType explicites
ERROR [code] [TEMPLATE|TEXT|JSON|HEX|BASE64|BASE32|BINARY|HANDLER] [contentType]
    /* Contenu selon le type */
END ERROR

// Fichier externe
ERROR [code] HANDLER [filepath] [contentType]
ERROR [code] FILE [filepath] [contentType]      // alias de HANDLER
ERROR [code] TEMPLATE [filepath] [contentType]  // fichier template
```

**Exemples :**

```hcl
// JS inline pour 404
ERROR 404
    context.Status(404).JSON({ error: "not found", path: context.Path() });
END ERROR

// Template pour 500
ERROR 500 TEMPLATE text/html
    <h1>Erreur Serveur</h1><p>Code: 500</p>
END ERROR

// Template pour toutes les erreurs (code omis)
ERROR TEMPLATE text/html
    <h1>Erreur {{code}}</h1>
END ERROR

// Fichier JS externe
ERROR 403 HANDLER handlers/forbidden.js

// Données binaires (ex: image 404)
ERROR 404 BASE64 image/png
    iVBORw0KGgoAAAANSUHEUgAAAAEAAAABCAYAAAAfFcSJ...
END ERROR
```

**Types supportés :**

| Type | Description |
|---|---|
| *(vide)* | JS inline (défaut) |
| `TEXT` | Texte brut |
| `JSON` | JSON brut |
| `TEMPLATE` | Mustache/JS (inline ou fichier) |
| `HANDLER` | Fichier JS externe |
| `FILE` | Alias de `HANDLER` |
| `HEX` | Données hex |
| `BASE64` | Données base64 |
| `BASE32` | Données base32 |
| `BINARY` | Données binaires (auto-detect hex/b64/b32) |

---

### Middlewares (`MIDDLEWARE`)

La directive `MIDDLEWARE` permet d'enregistrer des middlewares globaux. Elle suit des règles de syntaxe strictes selon son contenu :

#### 1. Middlewares Nommés (`@...`)
Lorsqu'elle contient des middlewares natifs commençant par `@`, la directive **doit être monoligne** et ne peut pas utiliser de bloc `BEGIN`/`END`.

```hcl
MIDDLEWARE @HELMET @CSRF @CORS[origins="*"]
```

#### 2. Middlewares JavaScript
Lorsqu'elle ne contient **pas** de middlewares nommés, elle peut définir du code personnalisé.

**Bloc JS Inline (recommandé pour les scripts courts) :**
```hcl
MIDDLEWARE BEGIN [priority=10]
    console.log(`[${new Date().toISOString()}] ${context.Method()} ${context.Path()}`);
END MIDDLEWARE
```

**Fichier JS externe (recommandé pour la logique complexe) :**
```hcl
MIDDLEWARE handlers/auth.js [priority=5]
```

> [!IMPORTANT]
> Les arguments `[...]` (comme `priority`) doivent toujours apparaître après le mot-clé `BEGIN` ou après le chemin du fichier.

---

### Routes HTTP (`GET`, `POST`, `PUT`, `DELETE`, etc.)

Les méthodes HTTP déclarent des handlers pour des chemins spécifiques.

```hcl
[METHOD] [middlewares] [path] [TYPE] [options]
```

- **`middlewares`** : Liste optionnelle de middlewares nommés commençant par `@`.
  - Exemple : `@CORS`, `@LIMITER[max=10]`, `@BOT[js_challenge=true]`, `@AUTH[redirect="/"]`.
- **`path`** : Chemin de la route.
- **`TYPE`** : Type de contenu ou de handler (`HANDLER`, `JSON`, `TEMPLATE`, `TEXT`, `HEX`, `BINARY`, `BASE64`).
- **`options`** : Arguments de route `[key=value]` (identiques à ceux de `STATIC`).

**Exemple avec Middlewares et Arguments :**
```hcl
GET @CORS @AUTH "/api/data" JSON [cache=5m]
    {"status": "ok"}
END GET
```

---

### Syntaxe des Handlers

// JS inline (défaut, pas de type)
GET "/hello"
    context.SendString("Hello, " + context.Query("name", "World") + "!");
END GET

// Fichier JS externe
POST "/signup" HANDLER handlers/signup.js
POST "/login" FILE handlers/login.js

// Réponse texte rapide
GET "/status" TEXT text/plain
    OK
END GET

// Réponse JSON rapide
GET "/version" JSON
    {"version": "2.0", "ok": true}
END GET

// Template Mustache/JS
GET "/home" TEMPLATE text/html
    <h1>Bienvenue, {{name}}!</h1>
END GET

// Fichier statique binaire (inline)
GET "/logo" BASE64 image/png
    iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QAANMAn399K60AAAAAElFTkSuQmCC
END GET

// Fichier HTML (template externe)
GET "/about" TEMPLATE "views/about.html" text/html
```

**Méthodes supportées :** `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `HEAD`, `OPTIONS`.

**Types supportés :** *(vide)* (JS inline), `TEXT`, `JSON`, `TEMPLATE`, `HANDLER` (ou `FILE`), `HEX`, `BASE64`, `BASE32`, `BINARY`, `BYTES`.

> [!NOTE]
> Tous les types binaires (`HEX`, `BASE64`, `BASE32`, `BINARY`, `BYTES`) ainsi que `FILE`/`HANDLER` sont gérés par le décodeur central `RouteConfig.Content()`, garantissant une intégrité binaire totale (Binaire-Safe). `BINARY` et `BYTES` tentent d'auto-détecter le format hex/b64/b32 avant de retomber sur du brut.

---

Le système Binder permet à plusieurs protocoles d'écouter sur le même port en utilisant le "peeking" (inspection non-destructive des premiers octets de la connexion via `bufio.Peek`).

Cette approche garantit que les protocoles comme MQTT reçoivent l'intégralité du flux d'octets original, y compris les octets utilisés pour l'identification.

```hcl
REGISTER PROTOCOL DTP "protocols/dtp.js"

TCP 0.0.0.0:8080
    HTTP
        AUTH USER admin password
        GET "/"
            context.SendString("HTTP response");
        END GET
    END HTTP

    DTP
        AUTH CSV "devices.csv"
        DATA "SENSOR_DATA"
            print("Received data from " + device.DeviceID);
        END DATA
    END DTP
END TCP
```

> Le protocole `HTTP`/`HTTPS` s'identifie via les méthodes HTTP (`GET`, `POST`...) ou le handshake TLS (octet `0x16`). Les protocoles custom s'identifient via leur fonction JS `match(buffer)`.

### Multiplexage UDP
Contrairement au TCP, le multiplexage UDP ne nécessite pas de "peeking" temporel. Chaque paquet reçu est confronté aux signatures des protocoles enregistrés dans le bloc `UDP`.

```hcl
UDP 0.0.0.0:8002
    DTP
        SECURITY "@LIMITER[max=50]"
    END DTP
    MQTT
        STORAGE "iot_db"
    END MQTT
END UDP
```

---

### Protocoles JavaScript Custom

```javascript
// protocols/dtp.js

// Retourne true si ce protocole doit gérer la connexion
function match(buffer) {
    const view = new Uint8Array(buffer);
    return view[0] === 0x44; // 'D' pour DTP
}

// Logique de traitement
function handle() {
    print("DTP connection accepted");

    socket.on("data", (data) => {
        if (data.includes("PING")) socket.write("PONG\n");
    });

    socket.on("error", (err) => print("Error: " + err));
    socket.on("close", () => print("Connection closed"));
}
```

---

## Exemple Complet

```hcl
REGISTER PROTOCOL DTP "protocols/dtp.js"

HTTP 0.0.0.0:8080
    // -- Environment & Configuration --
    ENV PREFIX APP_
    ENV .env.production
    ENV SET NODE_NAME "web-01"
    ENV DEFAULT LOG_LEVEL "warn"

    SET API_VERSION "3.0"
    SET RATE_LIMIT 100

    CONF config/app.json

    // -- Workers --
    WORKER workers/cache_warmer.js TTL=300 URL="https://api.example.com/data"
    WORKER workers/health_check.js INTERVAL=5000

    // -- Middleware global --
    MIDDLEWARE
        const start = Date.now();
        context.Locals("startTime", start);
    END MIDDLEWARE

    // -- Proxy --
    PROXY /api http://backend:8080

    // -- Rewrites & Redirects --
    REWRITE "/old/(.*)" "/new/$1"
    REDIRECT "/v1/(.*)" "/api/v1/$1" 301

    // -- Routes --
    GET "/health"
        context.JSON({ status: "ok", version: settings.API_VERSION });
    END GET

    POST "/echo" HANDLER handlers/echo.js

    GET "/logo" BASE64 image/png
        iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QAANMAn399K60AAAAAElFTkSuQmCC
    END GET

    // -- SSE & WebSockets --
    SSE "/events" HANDLER handlers/sse_events.js
    WS "/live" HANDLER handlers/ws_live.js

    // -- Erreurs --
    ERROR 404 TEMPLATE text/html
        <html><body><h1>404 - Not Found</h1><p>Path: {{path}}</p></body></html>
    END ERROR

    ERROR 500 HANDLER handlers/error_500.js

    // -- Static --
    STATIC /public "./public"
END HTTP

// HTTPS avec certificat Let's Encrypt
HTTPS 0.0.0.0:443
    SSL AUTO example.com admin@example.com
    PROXY / http://127.0.0.1:8080
END HTTPS
```

---

## Priorité de Résolution des Requêtes

```
MIDDLEWARE → PROXY → REWRITE/REDIRECT → [METHOD] routes → ROUTER → STATIC → ERROR(404)
```

---

> [!IMPORTANT]
> Le fichier `.bind` est un **paramètre statique**. Toute modification de son contenu ou de son chemin (`--bind-file`) nécessite un redémarrage complet du serveur.

---

## Groupes Supportés

| Protocole | Description |
|---|---|
| `HTTP` | Serveur web HTTP standard. |
| `HTTPS` | Serveur web avec support TLS/SSL. |
| `TCP` | Listener TCP générique (pour le multiplexage). |
| `UDP` | Listener UDP générique. |
| Custom | Tout protocole enregistré via `REGISTER PROTOCOL`. |


### Middlewares Nommés (`@`)

Le système de middlewares nommés permet d'appliquer des fonctionnalités transversales (sécurité, limites, headers) de manière déclarative avant le handler de la route.

```hcl
[METHOD] [@Middleware[args]]* [path] [TYPE]? [ContentType]?
```

#### Liste des Middlewares Disponibles

| Nom | Description | Arguments Communs |
|---|---|---|
| `HELMET` | Headers de sécurité (HSTS, CSP, XSS) | `xss=1`, `frameOptions="SAMEORIGIN"` |
| `CORS` | Cross-Origin Resource Sharing | `origins="*"`, `methods="GET,POST"`, `credentials=true` |
| `LIMITER` | Limiteur de débit (Rate Limiting) | `max=10`, `expiration="1m"` |
| `ADMIN` | Protection d'accès (Auth HTTP) | `redirect="/login"`, `message="Accès refusé"`, `basic` |
| `CSRF` | Protection contre les attaques CSRF | `name="_csrf"`, `secure=true`, `httpOnly=true` |
| `IDEMPOTENCY` | Gestion des requêtes idempotentes | `header="X-Idempotency-Key"`, `expiration="30s"` |
| `ETAG` | Support des ETags pour le cache | `weak=true` |
| `TIMEOUT` | Limite de temps pour la requête | `expiration="5s"` |
| `REQUESTID` | Injection d'un ID de requête unique | `header="X-Request-ID"` |
| `REQUESTTIME` | Ajout du temps de réponse dans les headers | `header="X-Response-Time"` |
| `PPROF` | Profiling (uniquement pour GET) | Aucun |
| `HEALTH` | Point de contrôle de santé | Aucun |

**Exemple :**
```hcl
HTTP 0.0.0.0:8080
    # Route publique avec CORS
    GET @CORS "/api/public" JSON
        {"status": "public"}
    END GET

    # Route protégée avec Rate Limiting et redirection Admin
    POST @LIMITER[max=5 expiration="1m"] @ADMIN[redirect="/auth"] "/api/secure" JSON
        {"status": "secure"}
    END POST

    # Debugging avec PPROF
    GET @PPROF "/debug/pprof"
END HTTP
```

---

### Arguments de Route (`[ARGS...]`)

Les routes acceptent un bloc d'arguments optionnels à la fin de la ligne de déclaration. Ces arguments permettent de configurer le comportement du handler de manière déclarative, sans modifier le code.

#### Syntaxe

```hcl
[METHOD] [@Middleware]* [path] [TYPE]? [filepath]? [ContentType]? [key=value key2 ...]
```

- Les arguments sont entourés de `[` et `]`
- Chaque argument est de la forme `KEY=VALUE` ou simplement `KEY` (équivaut à `KEY=true`)
- Les valeurs peuvent être entre guillemets : `"double"`, `'simple'`, `` `backtick` ``
- Plusieurs arguments sont séparés par des espaces

#### Exemple général

```hcl
HTTP 0.0.0.0:8080

    # Route statique avec arguments
    STATIC /public /var/www/app/assets [browse=true cache=10m indexName=index.html,default.html]

    # Route SPA avec middleware CORS
    ROUTER @CORS /app /var/www/dist [indexName=index.html]

    # Fichier statique virtuel (inline)
    STATIC /config.json json [maxAge=3600s]
    {"version": "1.0", "env": "production"}
    END STATIC

    # Route protégée
    GET @ADMIN[redirect=/login] @CORS /api/admin/stats HANDLER ./handlers/stats.js

END HTTP
```

#### Arguments pour `STATIC` / `ROUTER`

| Argument | Type | Défaut | Description |
|---|---|---|---|
| `indexName` | `string` (virgules) | `index.html` | Fichiers d'index (ex: `index.html,default.html`) |
| `browse` | `bool` | `false` | Active le listing du répertoire |
| `compress` | `bool` | `false` | Active la compression de la réponse |
| `byteRange` | `bool` | `false` | Active les requêtes byte-range (streaming partiel) |
| `download` | `bool` | `false` | Force le téléchargement direct |
| `cache` | `duration` | `10s` | Durée de cache interne du serveur (ex: `5m`, `1h`, `0` pour désactiver) |
| `maxAge` | `duration` | `0` | Header HTTP `Cache-Control: max-age=N` (ex: `maxAge=3600s`) |

> [!TIP]
> Les arguments `STATIC` sont transmis directement au moteur statique. Des valeurs comme `browse=true` peuvent impacter la sécurité — à utiliser avec précaution en production.

#### Access dans les Handlers JS (future extension)

Pour les routes classiques (`GET`, `POST`, etc.), les arguments peuvent être lus depuis le contexte route configuré. La fonctionnalité est extensible pour des cas comme des limites personnalisées ou des comportements spécifiques au handler.

---
*Dernière mise à jour : 18 Mars 2026*
