# Beba – Le Backend Unifié pour Tous

**Beba** (signifie *"Tous, Tout le monde"* en langue Akélé du Gabon) est un **serveur hyper-média** et un **backend Open Source** "all-in-one" distribué sous la forme d'un **seul fichier** binaire auto-contenu.

Oubliez la complexité des infrastructures Docker et micro-services : déployez une **application fullstack** complète en quelques secondes avec un moteur alliant la rapidité du **SSR** (Mustache/JS) à l'élégance de **HTMX**.

> *Beba. Pour tous, partout.*

---

## Table des matières

- [Pourquoi Beba ?](#pourquoi-beba-)
- [Fonctionnalités clés](#fonctionnalités-clés)
- [Beba vs les autres](#beba-vs-les-autres)
- [Installation](#installation)
- [Utilisation](#utilisation)
- [Exemple : API payante avec géofencing](#exemple--api-payante-avec-géofencing)
- [Initialisation automatique](#initialisation-automatique-sans-bind)
- [Structure de projet recommandée](#structure-de-projet-recommandée-fsrouter)
- [Commandes et options](#commandes-et-options)
- [Documentation complète](#documentation-complète)
- [Pourquoi le nom Beba ?](#pourquoi-le-nom-beba-)
- [Contribution](#contribution)
- [Licence](#licence)

---

## Pourquoi Beba ?

| Le problème | La solution Beba |
|-------------|------------------|
| Docker, Kubernetes, 15 services à orchestrer | **Un seul binaire** (50-70 Mo) |
| `npm install` + 500 dépendances | **Zéro dépendance**, zéro `node_modules` |
| Builds de 5 minutes, configurations fragiles | **Démarrage instantané** (10 ms), hot-reload |
| Perte des données en mode "simple" | **Persistance réelle** (dossier `./.data` créé automatiquement) |
| Sécurité à ajouter (WAF, rate limiting, HTTPS) | **Sentinelle 5 couches** intégrée |
| Séparer API, base, realtime, MQTT, paiements | **Tout est unifié**, ponts automatiques |

---

## Fonctionnalités clés

### Routage par fichiers (FsRouter)
Comme dans **Next.js** ou **Nuxt.js**, la structure de votre répertoire définit vos routes. Support natif des paramètres dynamiques (`[id]`), des groupes (`(group)`), des layouts imbriqués (`_layout.html`) et des middlewares en cascade (`_middleware.js`).

### SSR et scripting JS natif
Exécutez du **JavaScript côté serveur** directement dans vos templates (`<script server>`, `<?js ?>`, `<?= ... ?>`). Accédez à votre base de données, au hub temps-réel, aux sessions – sans API intermédiaire.

> [!CAUTION] Seules les variables déclarées avec `var` sont exposées au moteur de template Mustache.

### Base de données et CRUD unifié
Basculez en mode **Headless CMS** instantanément. Définissez vos schémas en DSL, avec relations (`has=one`, `many`, `many2many`). Beba génère automatiquement :
- Une **API REST complète**
- Une **interface d'administration temps-réel** (HTMX + SSE)
- Des **migrations sécurisées** (Dual Struct)

**En mode simple (`./beba`)** : le dossier `./.data` est **généré automatiquement** dès le premier lancement. Contrairement à un simple serveur statique, Beba offre une **persistance réelle** (SQLite, Sessions, Cache). Redémarrez votre serveur autant que vous voulez, vos données restent intactes.

### Authentification & OAuth2 Unifiés
Gérez vos utilisateurs et identités externes avec une syntaxe déclarative globale `AUTH [name] DEFINE`.
- Sources locales : Fichiers JSON/YAML/TOML/CSV, utilisateurs statiques.
- Logique custom : Authentification scriptable en JavaScript (`allow()` / `reject()`).
- **OAuth2 Client** : Connexion via Google, GitHub, etc. (`STRATEGY`).
- **OAuth2 Server** : Transformez Beba en fournisseur d'identité avec des Access Tokens JWT sans état (`SERVER DEFINE`).
- **API JS Unifiée** : Pilotez l'authentification et les JWT depuis vos scripts via `require('auth')`.
Montage automatique des APIs standard (`/auth/login`, `/auth/me`, `/auth/callback/:strategy`, `/oauth2/*`) sur vos routes.

### Hub realtime massivement scalable
Le cœur du système : un hub de messagerie haute performance capable de gérer **plus d'un million de clients simultanés**.
- **SSE** (Server-Sent Events)
- **WebSocket** classique
- **MQTT 5.0** (broker natif, accessible sur `/api/realtime/mqtt`)
- **Socket.IO**

**Bridge automatique** : un message MQTT d'un capteur IoT est immédiatement diffusé en SSE vers vos dashboards web.

### Sécurité – Architecture Sentinelle 5 couches
Beba embarque une défense en profondeur, sans module externe :

| Couche | Niveau | Protection |
|--------|--------|------------|
| **L1** | Network | Filtrage IP/CIDR, **Géofencing GeoJSON** au niveau socket |
| **L2** | Protocol | Validation des méthodes, limites de corps (4 Mo par défaut) |
| **L3** | Applicative | **WAF Coraza** + règles OWASP CRS (SQLi, XSS, LFI) |
| **L4** | Identity | Détection de bots, **défi Proof-of-Work** |
| **L5** | Audit | Logs immuables signés par **chaînage HMAC** |

**Par défaut** : rate limiting 100 req/s, limite de corps 4 Mo, protection anti-path traversal.

### Paiements intégrés
Une directive `PAYMENT` et c'est tout. Support natif :
- **Stripe** (cartes, checkout)
- **Mobile Money** (MTN, Orange, Airtel)
- **Crypto** (protocole X402)
- **Providers personnalisés** (DSL complet)

```hcl
GET @PAYMENT[name=stripe price="9.99"] "/premium"
    context.JSON({ data: "contenu premium" })
END GET
```

### Génération PDF native
Middleware `@PDF` pour transformer n'importe quelle réponse HTML en document PDF professionnel.

```hcl
GET @PDF[name="facture" format="A4"] "/invoice"
    <h1>Facture</h1><p>Montant: 100€</p>
END GET
```

### Multiplexage de protocoles (Binder)
Un seul port, des protocoles multiples. Grâce au **Binder**, vous pouvez mixer sur la même socket :
- HTTP / HTTPS
- MQTT
- DTP (protocole IoT maison)
- Protocoles JavaScript personnalisés
- **Contrôle déclaratif** : Désactivation fine des fonctionnalités (ex. `DISABLE ADMIN UI`, `DISABLE DEFAULT API`) via la directive `DISABLE`.
- **API CRUD Standardisée** : REST API automatique sur `/api/_schema` et `/api/:schema`.
- **Admin UI** : Dashboard moderne accessible sur `/_admin`.

Configuration déclarative via des fichiers `.bind`.

### Virtual hosts (multi-sites)
Mode **Master-Worker** : chaque site tourne dans son propre processus, avec son environnement JavaScript isolé. Configuration via fichier `.vhost` ou `.vhost.bind`.

```bash
./beba ./vhosts --vhosts
```

### Emails intégrés (MAIL)
Support natif de SMTP, SendGrid, Mailgun, Postmark, et providers REST personnalisés.
- Templates Mustache
- Pièces jointes
- Middlewares `@PRE` / `@POST`

### Tâches planifiées (CRON)
Les fichiers `_*.cron.js` sont automatiquement planifiés. L'en-tête `# CRON * * * * *` définit le planning.

```js
# CRON */5 * * * *
console.log("Tâche exécutée toutes les 5 minutes");
```

### Cycle de vie du serveur
- `_start.js` : exécuté une seule fois au démarrage
- `_close.js` : exécuté à l'arrêt propre (SIGTERM/SIGINT)
- `_middleware.js` : middleware en cascade
- `_layout.html` : layout imbriqué

---

## Beba vs les autres

| Fonctionnalité | **Beba** | Nginx | Apache | PocketBase | Supabase |
|----------------|----------|-------|--------|------------|----------|
| **Binaire unique** | ✅ | ❌ | ❌ | ✅ | ❌ |
| **Zero config par défaut** | ✅ | ❌ | ❌ | ✅ | ❌ |
| **Persistance des données sans config** | ✅ (dossier `./.data`) | ❌ | ❌ | ✅ | ❌ |
| **Base de données intégrée** | ✅ (SQLite/Postgres/MySQL) | ❌ | ❌ | ✅ (SQLite) | ✅ (PostgreSQL) |
| **API CRUD auto** | ✅ | ❌ | ❌ | ✅ | ✅ |
| **Admin UI intégrée** | ✅ (HTMX + SSE) | ❌ | ❌ | ✅ | ✅ |
| **WAF intégré** | ✅ (Coraza + CRS) | ❌ | ❌ | ❌ | ❌ |
| **Paiements natifs** | ✅ (Stripe/MoMo/Crypto) | ❌ | ❌ | ❌ | ❌ |
| **Génération PDF native** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **MQTT Broker** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Protocole IoT maison (DTP)** | ✅ (TCP/UDP) | ❌ | ❌ | ❌ | ❌ |
| **Hub temps-réel unifié** | ✅ (SSE/WS/MQTT/IO) | ❌ | ❌ | ✅ (SSE/WS) | ✅ (Realtime) |
| **HTTPS + Let's Encrypt** | ✅ | via certbot | via certbot | ❌ | ❌ |
| **Hot-reload** | ✅ | ❌ | ❌ | ✅ | ❌ |
| **Routage fichiers (Next.js-like)** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Tâches CRON intégrées** | ✅ | ❌ | ❌ | ✅ | ❌ |
| **Scripting JS serveur** | ✅ | (Lua/NJS) | (PHP) | ✅ (JS + Go hooks) | ❌ |
| **Emails intégrés** | ✅ (SMTP/SendGrid/Mailgun) | ❌ | ❌ | ✅ | ❌ |
| **Multiplexage de protocoles (1 port)** | ✅ (HTTP/MQTT/DTP/JS) | ❌ | ❌ | ❌ | ❌ |
| **Géofencing GeoJSON** | ✅ | ❌ | ❌ | ❌ | ❌ |

---

## Installation

### Depuis les sources

```bash
git clone https://github.com/badlee/beba.git
cd beba
go build -o beba .
```

### Binaire pré-compilé (à venir)

```bash
# Linux
wget https://github.com/badlee/beba/releases/latest/beba-linux-amd64
chmod +x beba-linux-amd64
./beba-linux-amd64
```

---

## Utilisation

### 1. Mode simple (serveur de fichiers statiques + CRUD persistant + Admin UI)

```bash
./beba
```

**Vous avez immédiatement** :
- Serveur HTTP sur `http://localhost:8080`
- Base de données **SQLite persistante** dans `./.data/beba.db`
- Sessions persistantes dans `./.data/sessions.db`
- API REST automatique sur `/api`
- Interface d'administration sur `/_admin`
- Hub SSE sur `/sse` (passif, utilise `?channel=...`)
- WebSocket sur `/ws` (passif, utilise `?channel=...`)
- MQTT sur `/api/realtime/mqtt` (WebSocket)
- Broker MQTT TCP sur port 1883
- Routage par fichiers (FsRouter) actif (`./pages/` par défaut)

> [!IMPORTANT]
> **Zéro configuration, mais persistance réelle** : Le dossier `./.data` est créé automatiquement au démarrage pour stocker vos bases SQLite et vos sessions. C'est cette gestion native de la donnée qui différencie Beba d'un simple serveur de fichiers éphémère : vos données survivent aux redémarrages sans aucun réglage complexe.

### 2. Avec un fichier de configuration `.bind`

```bash
./beba --bind app.bind
```

### 3. Mode Virtual Hosts (multi-sites)

```bash
./beba ./vhosts --vhosts
```

### 4. Avec HTTPS et Let's Encrypt

```bash
# CLI
./beba --https --cert cert.pem --key key.pem

# Ou dans le fichier .bind
HTTPS 0.0.0.0:443
    SSL AUTO exemple.com admin@exemple.com
END HTTPS
```

---

## Exemple : API payante avec géofencing

**Fichier `app.bind` :**

```hcl
# Base de données persistante
DATABASE "sqlite://.data/monapp.db"
    SCHEMA users DEFINE
        FIELD email string [unique, required]
        FIELD name string [required]
    END SCHEMA
END DATABASE

# Paiement Stripe
PAYMENT "stripe://sk_live_xxx"
    NAME stripe_prod
    CURRENCY EUR
END PAYMENT

# Sécurité
SECURITY production [default]
    CONNECTION RATE 100r/s 1s burst=10
    GEOJSON europe "geo/europe.geojson"
    CONNECTION ALLOW europe
END SECURITY

# Serveur HTTP
HTTP :8080
    CRUD default /api
    PAYMENT stripe_prod /pay

    GET @PAYMENT[name=stripe_prod price="9.99"] "/premium"
        context.JSON({ status: "paid", data: "Top secret" })
    END GET
END HTTP
```

**Lancement :**
```bash
./beba --bind app.bind
```

---

## Initialisation automatique (sans `.bind`)

Placez un fichier `_start.js` dans `./pages/`. Il sera exécuté **une seule fois** au démarrage :

```javascript
// pages/_start.js
const db = require('db');

// Créer une collection avec schéma (persistante dans .data)
db.createCollection('users', {
    schema: {
        email: { type: 'string', required: true, unique: true },
        name: { type: 'string', required: true },
        role: { type: 'string', default: 'user' }
    }
});

// Créer un admin par défaut
const adminExists = db.collection('users').findOne({ email: 'admin@beba.local' });
if (!adminExists) {
    db.collection('users').create({
        email: 'admin@beba.local',
        name: 'Administrateur',
        role: 'admin'
    });
    console.log('✅ Admin créé : admin@beba.local (mot de passe à définir)');
}

console.log('✅ Base de données initialisée dans .data/');
```

---

## Structure de projet recommandée (FsRouter)

```
mon-projet/
├── .data/                      # PERSISTANCE (créé automatiquement)
│   ├── beba.db                 # Base de données SQLite
│   └── sessions.db             # Sessions persistantes
├── pages/                      # Dossier racine des routes
│   ├── _start.js               # Initialisation (une fois)
│   ├── _close.js               # Nettoyage (arrêt)
│   ├── _middleware.js          # Middleware global
│   ├── _layout.html            # Layout global
│   ├── index.html              # Page d'accueil (/)
│   ├── about.html              # Page statique (/about)
│   ├── (group)/                # Groupe de routes (n'apparaît pas dans l'URL)
│   │   └── page.html           # /page
│   ├── blog/
│   │   ├── _middleware.js      # Middleware local
│   │   ├── index.html          # /blog
│   │   └── [slug].html         # Route dynamique /blog/:slug
│   ├── api/
│   │   ├── users.js            # Endpoint GET /api/users
│   │   └── users.POST.js       # Endpoint POST /api/users
│   └── _cleanup.cron.js        # Tâche planifiée toutes les X minutes
└── uploads/                    # Fichiers statiques (images, etc.)
```

**Fichiers spéciaux :**

| Nom | Description |
|-----|-------------|
| `_middleware.js` | S'exécute avant toute route du dossier |
| `_layout.html` | Layout commun (tag `{{content}}` pour le contenu) |
| `_start.js` | Une seule fois au démarrage |
| `_close.js` | Une seule fois à l'arrêt |
| `_*.cron.js` | Tâche planifiée (CRON en première ligne) |

---

## Commandes et options

| Flag | Description | Défaut |
|------|-------------|--------|
| `--port, -p` | Port d'écoute | 8080 |
| `--bind, -b` | Fichier(s) de configuration `.bind` | - |
| `--hot-reload, -H` | Rechargement à chaud | true |
| `--cache-ttl` | Durée de vie du cache fichier FsRouter (ex: `5m`, `30s`, `0` = permanent) | 5m |
| `--vhosts, -V` | Mode Virtual Hosts | false |
| `--https` | Activer HTTPS | false |
| `--cert`, `--key` | Certificat SSL | - |
| `--no-template` | Désactiver le moteur de templates | false |
| `--schedule` | Activer les tâches CRON | true |
| `--config-file, -c` | Fichier de config (JSON/YAML/TOML) | app |
| `--env-file` | Fichier d'environnement (.env) | .env |
| `--silent, -S` | Supprimer les logs | false |

---

## Documentation complète

| Fichier | Description |
|---------|-------------|
| [BINDER.md](doc/BINDER.md) | **Configuration `.bind`** – Référence complète |
| [ROUTER.md](doc/ROUTER.md) | **FsRouter** – Routage par fichiers (Next.js-like) |
| [HTTP.md](doc/HTTP.md) | **HTTP/HTTPS** – Moteur web, SSL, middlewares |
| [DATABASE.md](doc/DATABASE.md) | **Base de données** – Schémas, relations, API CRUD |
| [ADMIN.md](doc/ADMIN.md) | **Admin UI** – Interface d'administration |
| [JS_SCRIPTING.md](doc/JS_SCRIPTING.md) | **Scripting JS** – API serveur, modules natifs |
| [SECURITY.md](doc/SECURITY.md) | **Sécurité** – Architecture Sentinelle 5 couches |
| [PAYMENT.md](doc/PAYMENT.md) | **Paiements** – Stripe, Mobile Money, Crypto X402 |
| [MQTT.md](doc/MQTT.md) | **MQTT** – Broker temps-réel unifié |
| [DTP.md](doc/DTP.md) | **DTP** – Protocole IoT natif (TCP/UDP) |
| [IO.md](doc/IO.md) | **Socket.IO** – Support natif |
| [MAIL.md](doc/MAIL.md) | **Emails** – SMTP, SendGrid, Mailgun |
| [TEMPLATING.md](doc/TEMPLATING.md) | **Templates** – Mustache + JavaScript |
| [STORAGE.md](doc/STORAGE.md) | **Session & Cache** – Persistance et JWT |
| [VHOST.md](doc/VHOST.md) | **Virtual Hosts** – Multi-sites, Master-Worker |
| [CLI.md](doc/CLI.md) | **Ligne de commande** – Flags et options |

---

## Pourquoi le nom Beba ?

**Beba** signifie *"Tous, Tout le monde"* en langue **Akélé** (Gabon). Ce choix n'est pas anodin :

- **Universalité** : Beba sert tous les développeurs, tous les projets, tous les protocoles.
- **Communauté** : Comme le sens du mot, Beba rassemble – il fédère base de données, API, temps-réel, sécurité et paiements dans une seule et même entité.
- **Rareté** : Un nom unique, sans collision, qui porte une histoire et une profondeur.

> *Beba. Pour tous, partout.*

---

## Contribution

Les contributions sont les bienvenues. Voici comment aider :

1. **Tester** le projet sur vos cas d'usage
2. **Signaler** des bugs ou des manques dans la documentation
3. **Soumettre** des pull requests
4. **Écrire** des exemples ou des tutoriels
5. **Rejoindre** le serveur Discord (lien à venir)

---

## Licence

Open Source – voir le fichier [LICENSE](LICENSE).

---

*Déployez, Sécurisez, Encaissez. Beba.*
