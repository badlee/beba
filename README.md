# Le Backend Hyper-média Ultime pour Développeurs Modernes

**http-server** est un **serveur hyper-média** et un **backend Open Source** "all-in-one" distribué sous la forme d'un **seul fichier** binaire auto-contenu. Oubliez la complexité des infrastructures Docker et micro-services : déployez une **application fullstack** complète en quelques secondes avec un moteur alliant la rapidité du **SSR** (Mustache/JS) à l'élégance de **HTMX**.

### **Pourquoi intégrer http-server à votre stack ?**

*   **📂 Routage par Fichiers (inspiré de Next.js/Nuxt.js)**  
    Organisez votre logique backend avec la simplicité du routage basé sur les dossiers. Comme dans **Next.js** ou **Nuxt.js**, la structure de votre répertoire définit vos routes, incluant le support natif des paramètres dynamiques (`[id]`), des layouts imbriqués et des middlewares en cascade.
    
*   **⚡ Rendu SSR & Scripting JS Natif**  
    Boostez vos performances avec un moteur **JavaScript** serveur intégré. Définissez votre logique métier directement dans vos templates ou scripts isolés, bénéficiant d'un pont direct vers vos données sans l'overhead d'une API externe.
    
*   **🛠️ Headless CMS & Admin UI Temps-Réel**  
    Basculez en mode **Headless CMS** instantanément. Le module **CRUD** génère automatiquement vos API REST et une interface d'administration temps-réel (propulsée par HTMX + SSE), vous permettant de piloter vos données (SQLite, PostgreSQL, MySQL) dès le lancement.
    
*   **📡 Hub Realtime Massivement Scalable (+1M de clients)**  
    Le cœur du système : un hub de messagerie haute performance capable de gérer **plus d'un million de clients simultanément**. Support natif de **SSE**, **WebSocket**, **MQTT over WebSocket** et **Socket.IO**. Grâce au **Binder** innovant, multiplexez ces protocoles sur un seul port pour une interopérabilité totale.
    
*   **⚙️ Performance "Bare-Metal" & Hot-Reload**  
    Profitez d'un binaire unique sans dépendances externes, incluant la gestion sécurisée des sessions (JWT), le chiffrement des cookies et un système de **Hot-Reload** qui met à jour votre configuration à la volée sans aucune interruption de service.

## Architecture & Modules
- **Binder (`modules/binder`)** : Multiplexage de protocoles (HTTP, DTP, MQTT, JS custom) sur un même port via des fichiers `.bind`.
- **Temps-Réel (`modules/sse`)** : Hub SSE/WS sharded haute performance (1M+ connexions).
- **Base de Données (`modules/db`)** : API Mongoose-like pour SQLite, MySQL, Postgres, etc.
- **Scripting Script (`processor/`)** : Rendu hybride Mustache/JS et exécution server-side isolée.
- **Plugins (`plugins/`)** : Hot-reload (`config`), Serveur HTTP (`httpserver`), et Modules JS (`require`).

## ⚔️ Le "Killer Feature" Table : http-server vs Nginx vs Apache

| Caractéristique | http-server | Nginx | Apache HTTPD |
|---|---|---|---|
| **Découverte & Run** | **Binaire Unique (0 dep)** | Paillage complexe | Paillage complexe |
| **Port Multiplexing** | **Natif (1 Port = N Protos)** | Séparé par Port / Proxy | Séparé par Port |
| **Web App Firewall** | **Coraza + CRS (Natif)** | Module ModSec (tiers) | Module ModSec (tiers) |
| **Security Audit** | **Signé (HMAC Chain)** | Texte Simple | Texte Simple |
| **Scripting Logic** | **JavaScript Natif (Isolé)** | Lua (Complexe) / NJS | PHP/C (Interpréteur ext) |
| **Géo-fencing** | **GeoJSON, GéoIP et Plus Code** | GéoIP (Pays uniquement) | GéoIP (Pays uniquement) |
| **Real-time Hub** | **Natif (+1M Connexions)** | Plugins tiers (Nchan) | Support minimal |
| **IoT Integration** | **DTP & MQTT Unifié** | Websocket simple | Plugins lourds |
| **Dev Experience** | **Hot-reload & FsRouter** | Configuration Statique | Configuration Statique |

## 🛡️ Une Forteresse à 5 Couches (Nouveauté WAF)

http-server n'est pas qu'un serveur, c'est une sentinelle. Il embarque une défense en profondeur à 5 couches :

1.  **L1 Network** : Filtrage IP/CIDR et **GeoJSON Dynamic Fencing**.
2.  **L2 Protocol** : Validation stricte des méthodes et des limites de corps (BodyLimit).
3.  **L3 Applicative** : Moteur WAF (Firewall Applicatif) avec analyse de payload SQLi/XSS.
4.  **L4 Identity** : Détection de Bots et **JS Challenge (Proof-of-Work)** natifs.
5.  **L5 Audit** : Journalisation immuable via **Chaînage de hash HMAC**.

## ✨ Fonctionnalités Clés

- **Ultra-Rapide** : Propulsé par un moteur haute performance.
- **SSR & JS Server-Side** : Exécution de `<script server>` dans vos fichiers HTML, templates Mustache et PHP-style (`<?js ?>`).
- **FsRouter (File-System Routing)** : Routage automatique basé sur la structure des dossiers avec support des paramètres dynamiques `[id]`, catch-all `[...]`, layouts parenthésés `(group)` et middlewares en cascade `_middleware.js`.
- **Module Base de Données** : API type Mongoose pour SQLite, PostgreSQL, MySQL et SQL Server.
- **DTP (Device Transfer Protocol)** : Protocole natif pour l'Internet des Objets (IoT) supportant TCP et **UDP**. Routage intelligent par subtype et gestion de file d'attente (`QUEUE`) avec bridge automatique vers le Hub SSE.
- **Broker MQTT 3.1.1/5.0** : Broker natif ultra-performant unifié avec le Hub SSE. Support de la persistence QoS 1/2 via GORM (`STORAGE`) et sécurisation native (L4) sur TCP et UDP.
- **Sécurité Native** : Chiffrement automatique des cookies, gestion de session sécurisée, et **Security Baseline** globale de 100r/s appliquée dès l'acceptation de la socket pour TCP et au niveau du paquet pour **UDP**.
- **Geofencing GeoJSON** : Filtrage géographique précis (L4) via la directive `GEOJSON` supportant les collections complexes et les polygones multiples, désormais compatible avec les flux **UDP**.
- **Middlewares Nommés** : Système déclaratif `@Name` pour appliquer des middlewares (CORS, Helmet, Rate-Limiting, etc.) directement dans vos routes.
- **Auth Multi-Algorithmes** : Support Bcrypt, SHA-512, SHA-256, SHA-1, MD5 avec encodages multiples (Hex, Base32, Base64).
- **CLI Avancée & Hot-Reload** : Configuration hiérarchique (Défauts < Fichiers < Env < Flags), flags négatifs `--no-xxx`, hot-reload dynamique.
- **Binder – Multiplexage de Protocoles** : Configuration déclarative `.bind` pour servir plusieurs protocoles sur la même adresse. Directives avancées :
  - `WORKER` – Scripts JS en arrière-plan avec injection `config` + `settings`.
  - `SET / ENV` – Gestion précise de la configuration interne et de l'environnement process.
  - `REWRITE / REDIRECT` – Avec regex, groupes de capture et conditions JS.
  - `SECURITY` / `CONNECTION` – Pare-feu réseau de niveau 4 (SYN/Accept sync) pour TCP et filtrage par paquet pour **UDP**, avec rate-limiting, géo-blocking (GeoJSON, OLC) et hooks JS.
  - `AUTH` – Universel (HTTP/DTP) avec support Bcrypt et scripting JS (`allow()`/`reject()`).
  - Typage exhaustif (`TEMPLATE`, `JSON`, `HEX`, `BINARY`, `BASE64`, `TEXT`...).
  - `INCLUDE`, `PROXY`, `STATIC`, `ROUTER`, `SSE`, `WS`, `IO`, `MQTT`, `GEOJSON`.

```bash
go build -o http-server .
```

## 🚀 Exemples d'utilisation

### 1. Simple (Serveur de fichiers statiques)
Lance un serveur sur le port 8080 servant le répertoire courant :
```bash
./http-server
```

### 2. Multi-sites (Virtual Hosts)
Lance le mode Master qui gère plusieurs sites isolés basés sur les sous-dossiers de `./vhosts` :
```bash
./http-server ./vhosts --vhosts
```

### 3. Avancé (Multiplexage avec Binder)
Lance le serveur en utilisant un ou plusieurs fichiers de configuration `.bind` pour mixer HTTP, DTP (IoT), MQTT, etc. :
```bash
# Avec un seul fichier
./http-server --bind server.bind

```bash
# Avec plusieurs fichiers combinés
./http-server --bind app.bind --bind iot.bind --bind security.bind
```

## 🎛️ Options de la ligne de commande

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--config-file` | `-c` | `app` | Fichiers de config (json/yaml/toml) |
| `--env-file` | | `.env` | Fichiers d'environnement (.env/.conf) |
| `--env-prefix`| | `APP_`  | Préfixe des variables d'env |
| `--hot-reload`| `-H` | `true` | Activer le rechargement à chaud |
| `--port` | `-p` | `8080` | Port d'écoute |
| `--address` | `-a` | `0.0.0.0` | Adresse d'écoute |
| `--socket` | `-s` | | Ecouter sur un socket Unix |
| `--dir-listing` | `-L` | `true` | Afficher le contenu des dossiers |
| `--auto-index` | `-I` | `true` | Servir l'index.html automatiquement |
| `--index` | | `index.html` | Nom du fichier index |
| `--template-ext` | `-e` | `.html` | Extension des templates |
| `--no-template` | | `false` | Désactiver le moteur de template |
| `--htmx-url` | | `...` | URL CDN de HTMX |
| `--no-htmx` | | `false` | Désactiver l'injection HTMX |
| `--inject-html` | | | Injecter du HTML personnalisé |
| `--gzip` | `-G` | `true` | Activer la compression Gzip |
| `--brotli` | `-B` | `true` | Activer la compression Brotli |
| `--deflate` | `-D` | `true` | Activer la compression Deflate |
| `--silent` | `-S` | `false` | Mode silencieux (pas de logs) |
| `--stdout` | | | Fichier de sortie (mode test) |
| `--stderr` | | | Fichier d'erreur (mode test) |
| `--cors` | | `false` | Activer le middleware CORS |
| `--cache` | | `3600` | Durée du cache (secondes) |
| `--proxy` | | | Proxy de secours |
| `--https` | | `false` | Activer HTTPS |
| `--cert` | | `cert.pem`| Chemin du certificat SSL |
| `--key` | | `key.pem` | Chemin de la clé SSL |
| `--robots` | | `false` | Répondre à /robots.txt |
| `--robots-file` | | `robots.txt`| Fichier robots.txt à servir |
| `--read-timeout` | | `30` | Timeout de lecture (secondes) |
| `--write-timeout` | | `0` | Timeout d'écriture (secondes) |
| `--idle-timeout` | | `120` | Timeout d'inactivité (secondes) |
| `--vhosts` | `-V` | `false` | Activer le mode Virtual Hosts |
| `--bind` | `-b` | | Fichiers de configuration `.bind` |
| `--help` | `-?` | `false` | Afficher l'aide |

Pour une liste exhaustive et détaillée de chaque option, consultez la [Documentation CLI](doc/CLI.md).

## Exemple Binder Rapide

```hcl
# server.bind
# Surcharge de la sécurité par défaut (100r/s baseline)
SECURITY globale [default]
    CONNECTION RATE 500r/s 1s burst=50
    # Geofencing
    GEOJSON ma_zone "data/zone.geojson"
    CONNECTION ALLOW ma_zone
END SECURITY

HTTP 0.0.0.0:8080
    SET APP_NAME "MyServer"
    SET API_VERSION "1.0"

    ENV PREFIX APP_
    ENV SET DB_HOST "localhost"
    ENV DEFAULT DEBUG "false"

    WORKER workers/monitor.js INTERVAL=5000 TARGET="https://api.me"
    
    REWRITE "/v1/(.*)" "/api/v1/$1"
    REDIRECT 301 "/old-page" "/new-page"

    # Route avec Middlewares (CORS + Protection Admin)
    GET @CORS @ADMIN[redirect="/login"] "/admin" JSON
        {"status": "ok", "message": "Access granted"}
    END GET

    # Route avec Quoted Arguments
    GET @LIMITER[max=5 expiration="1m"] "/api/data" JSON
        {"data": "sensitive information"}
    END GET

    # Groupe de routes imbriquées
    GROUP /v2 DEFINE
        GET "/status"
            res.json({status: "v2 ok"});
        END GET
    END GROUP

    ERROR 404 TEMPLATE text/html
        <h1>404 – Page introuvable</h1>
    END ERROR

    STATIC /public "./public" [browse=true cache=10m]
END HTTP

TCP 0.0.0.0:1883
    MQTT :1883
        STORAGE "local_db"
        SECURITY "iot_firewall"
        AUTH "admin" "secret"
    END MQTT
END TCP
```

## 🎛️ CRUD & Admin UI (Nouveau)

`http-server` embarque un module CRUD complet (génération d'API REST) assité par une interface d'administration temps-réel (HTMX + SSE) intégrée et extensible.

```bind
# app.bind
CRUD 'sqlite://data.db' [default]
    NAME myapi
    AUTH USER root "mon-mot-de-passe"

    # Définition de modèle
    SCHEMA products DEFINE [icon=box]
        FIELD name string [required]
        FIELD price number [default=0]
    END SCHEMA

    # Extension de l'interface d'administration
    ADMIN DEFINE
        PAGE "/custom" [title="Analytics" icon=bar-chart order=10] BEGIN
            <div class="card">
                <h2>Statistiques</h2>
                <?js var now = new Date(); ?>
                <p>Mise à jour à : <?= now.toLocaleString() ?></p>
            </div>
        END PAGE
    END ADMIN
END CRUD

HTTP 0.0.0.0:8080
    # Montage sur le port 8080 (accès admin: http://localhost:8080/api/_admin)
    CRUD myapi /api
END HTTP
```

## 🛠️ Module Base de Données (Low-level)

```javascript
const db = require("db").connect("sqlite:///data.db");

const UserSchema = db.Schema({
    username: { type: "string", unique: true },
    age: "number"
});

const User = db.Model("User", UserSchema);
const adults = User.find({ age: { $gt: 18 } }).sort("-age").limit(10).exec();
```

## Documentation

| Fichier | Description |
|---|---|
| [CLI Usage](doc/CLI.md) | Flags et options de la ligne de commande |
| [Binder Config (.bind)](doc/BINDER.md) | **Référence complète** de toutes les directives Binder |
| [Database Module](doc/DATABASE.md) | API Mongoose, drivers, migrations |
| [Server-Side JS](doc/JS_SCRIPTING.md) | Interpréteur Javascript et API disponibles |
| [WAF & Network Security](doc/WAF.md) | **Sécurité L4**, Rate-limiting, GeoJSON, Hooks JS |
| [Templating Guide](doc/TEMPLATING.md) | Moteur hybride Mustache + PHP-style |
| [Session & Storage](doc/STORAGE.md) | Modules de stockage persistants |
| [DTP Protocol](doc/DTP.md) | Protocole IoT natif |
| [MQTT Broker](doc/MQTT.md) | Guide du broker MQTT intégré |
| [Virtual Hosts](doc/VHOST.md) | Architecture Master-Worker multi-sites |
| [Socket.IO Module](doc/IO.md) | Communication temps-réel Socket.IO |

## 🚀 Exemples Rapides

- [Showcase Temps-Réel (SSE/WS/MQTT/IO)](examples/realtime_showcase.bind)
- [Routage Fichier (FsRouter)](examples/advanced_features.bind)
- [Multiplexage TCP (HTTP + DTP)](examples/multiplex_test.bind)
