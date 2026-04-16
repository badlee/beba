# FsRouter

Routeur file-based intégré au package `httpserver`.  
Inspiré de Next.js — la structure du répertoire **est** le routeur.

---

## Sommaire

- [Installation](#installation)
- [Démarrage rapide](#démarrage-rapide)
- [Convention de fichiers](#convention-de-fichiers)
- [Fichiers spéciaux](#fichiers-spéciaux)
- [Routes statiques et dynamiques](#routes-statiques-et-dynamiques)
- [Méthodes HTTP](#méthodes-http)
- [Fichiers statiques](#fichiers-statiques)
- [Middlewares](#middlewares)
- [Error handlers](#error-handlers)
- [Groupes de layout](#groupes-de-layout)
- [Variables disponibles en JS](#variables-disponibles-en-js)
- [Configuration — RouterConfig](#configuration--routerconfig)
- [Exclusions — Exclude](#exclusions--exclude)
- [Priorité de résolution](#priorité-de-résolution)
- [Debug](#debug)
- [Référence complète des patterns de nommage](#référence-complète-des-patterns-de-nommage)

---

## Installation

```
import "http-server/plugins/httpserver"
```

---

## Démarrage rapide

```go
app := httpserver.New(httpserver.Config{AppName: "my-app"})

// Afficher la table de routing au démarrage (optionnel)
fmt.Print(httpserver.FsRouterDebug(httpserver.RouterConfig{
    Root:      "./pages",
    AppConfig: &cfg,
}))

// Enregistrer le routeur
app.Use(httpserver.FsRouter(httpserver.RouterConfig{
    Root:        "./pages",   // répertoire racine des pages
    TemplateExt: ".html",     // extension des templates (défaut)
    IndexFile:   "index",     // nom des fichiers index (défaut)
    AppConfig:   &cfg,        // config de l'application
    Settings:    map[string]string{
        "siteName": "Mon App",
    },
}))

app.Listen(":3000")
```

> **Note** : `FsRouter` scanne le répertoire **une seule fois** au démarrage. Les modifications de fichiers nécessitent un redémarrage.

---

## Convention de fichiers

```
pages/
├── index.html                  → GET  /
├── about.html                  → GET  /about
├── contact.html                → GET  /contact
│
├── blog/
│   ├── index.html              → GET  /blog
│   └── [slug].html             → GET  /blog/:slug
│
├── api/
│   ├── _middleware.js          → middleware pour tout /api/*
│   ├── users.js                → GET  /api/users         (module.exports multi-méthodes)
│   ├── users.POST.js           → POST /api/users         (fichier dédié)
│   ├── users/
│   │   └── [id].js             → GET  /api/users/:id
│   └── [...catchall].js        → GET  /api/*
│
├── (auth)/                     → groupe de layout — ignoré dans l'URL
│   ├── login.html              → GET  /login
│   └── dashboard.html          → GET  /dashboard
│
├── _middleware.js              → middleware global (toutes routes)
│
├── 404.html                    → handler 404
├── 500.html                    → handler erreur 500
├── _error.html                 → handler erreur générique
│
└── assets/
    ├── style.css               → GET  /assets/style.css  (fichier statique)
    ├── logo.png                → GET  /assets/logo.png
    └── app.js                  → GET  /app               (handler JS exécuté, pas servi brut)
```

---

## Fichiers spéciaux

| Nom | Description |
|---|---|
| `index.<ext>` | Route racine du répertoire (`/blog/index.html` → `/blog`) |
| `[param].<ext>` | Paramètre dynamique (`[slug].html` → `:slug`) |
| `[...param].<ext>` | Catch-all (`[...rest].js` → `*`) |
| `_middleware.js` | Middleware JS appliqué en cascade à tout le sous-arbre |
| `{code}.<ext>` | Handler d'erreur pour ce code HTTP (`404.html`, `500.js`) |
| `_error.<ext>` | Handler d'erreur générique (fallback pour tout code non couvert) |
| `(group)/` | Groupe de layout — le nom de répertoire est ignoré dans l'URL |
| `_layout.<ext>` | Layout (enveloppe commune pour les pages du répertoire) |
| `*.partial.<ext>` | Fichier partiel (ignore les layouts) |
| `_start.js` | Script d'initialisation (exécuté au démarrage) |
| `_close.js` | Script de nettoyage (exécuté à la fermeture) |
| `_*.cron.js` | Tâche planifiée (ex: `_cleanup.cron.js`) |

---

## Routes statiques et dynamiques

### Route statique

```
pages/about.html          → GET /about
pages/blog/index.html     → GET /blog
pages/api/status.html     → GET /api/status
```

### Route dynamique — paramètre

```
pages/blog/[slug].html        → GET /blog/:slug
pages/users/[id].js           → GET /users/:id
pages/orgs/[org]/repos/[repo].js  → GET /orgs/:org/repos/:repo
```

Dans un template, le paramètre est accessible via `params.slug`.  
Dans un handler JS, il est disponible comme `params.id`.

```js
// pages/users/[id].js
context.JSON({ id: params.id, name: "Alice" });
```

```html
<!-- pages/blog/[slug].html -->
<?js var title = params.slug.replace(/-/g, " "); ?>
<h1>{{title}}</h1>
```

### Route catch-all

Matche tout ce qui suit le préfixe, y compris les `/` intermédiaires.

```
pages/files/[...path].js    → GET /files/*
```

```js
// pages/files/[...path].js
// catchall contient le reste du chemin : "images/2024/photo.jpg"
context.SendString("Serving: " + catchall);
```

---

## Méthodes HTTP

### Méthode par suffixe de nom de fichier

Ajouter `.METHOD` avant l'extension pour déclarer une méthode explicite.

```
pages/users.GET.js       → GET  /users
pages/users.POST.js      → POST /users
pages/items/[id].PUT.js  → PUT  /items/:id
pages/items/[id].DELETE.js → DELETE /items/:id
```

Méthodes supportées : `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `HEAD`, `OPTIONS`.

### Méthodes multiples — `module.exports`

Un seul fichier `.js` peut gérer plusieurs méthodes via `module.exports` :

```js
// pages/api/users.js
module.exports = {
    GET: function(ctx, params, settings) {
        ctx.JSON({ users: [] });
    },

    POST: function(ctx, params, settings) {
        ctx.Status(201).JSON({ created: true });
    },

    DELETE: function(ctx, params) {
        ctx.SendString("deleted");
    },
};
```

La clé `ANY` sert de fallback pour toute méthode non listée explicitement :

```js
// pages/api/proxy.js
module.exports = {
    GET: function(ctx) { ctx.SendString("GET specific"); },
    ANY: function(ctx) { ctx.SendString("handled: " + ctx.Method()); },
};
```

Si une méthode n'est ni listée ni couverte par `ANY`, le router retourne **405 Method Not Allowed**.

**Arguments de la fonction handler** :

| Position | Nom | Type | Description |
|---|---|---|---|
| 1 | `ctx` | object | Contexte de la requête |
| 2 | `params` | object | Paramètres de route dynamique |
| 3 | `settings` | object | `RouterConfig.Settings` |

---

## Fichiers statiques

Tout fichier qui n'est ni un template (`TemplateExt`) ni un handler `.js` est servi directement via `c.SendFile`. L'extension est **conservée** dans l'URL.

```
pages/style.css         → GET /style.css
pages/assets/logo.png   → GET /assets/logo.png
pages/favicon.ico       → GET /favicon.ico
pages/fonts/inter.woff2 → GET /fonts/inter.woff2
pages/(public)/app.wasm → GET /app.wasm    (groupe ignoré)
```

> **Distinction `.js`** : les fichiers `.js` à la racine de `pages/` sont des **handlers exécutés** (pas servis bruts). Ils sont accessibles sans leur extension : `pages/app.js` → `GET /app`. Pour servir un fichier JS client brut, le placer dans un sous-dossier et le nommer avec une extension différente, ou désactiver le router sur cette route.

### Activer / désactiver

```go
// Désactiver les fichiers statiques
RouterConfig{
    ServeFiles: false,
    Exclude:    []*regexp.Regexp{regexp.MustCompile(`^\.`)},
    // Exclude doit être non-nil pour que ServeFiles=false soit respecté
}
```

---

## Middlewares

### `_middleware.js`

Placé dans un répertoire, il est exécuté avant **toute route** de ce sous-arbre.  
Les middlewares sont **cascadés** : racine → répertoire courant.

```
_middleware.js              → s'applique à toutes les routes
api/_middleware.js          → s'applique à /api/* en plus du middleware racine
api/admin/_middleware.js    → s'applique à /api/admin/* (racine + api + admin)
```

### Écrire un middleware

```js
// pages/_middleware.js

// Vérification auth
var token = context.Get("Authorization");
if (!token || token !== "Bearer " + settings.apiKey) {
    throwError(401, "Unauthorized");
}
// Appel obligatoire pour passer au handler suivant
next();
```

| Comportement | Comment |
|---|---|
| Passe au handler suivant | Appeler `next()` |
| Court-circuite la chaîne | Ne pas appeler `next()` (et écrire une réponse) |
| Retourne une erreur HTTP | `throwError(code, message)` |

Si `next()` n'est pas appelé **et** qu'aucune réponse n'a été écrite, le routeur continue quand même (comportement sûr par défaut).

### Middleware dans les `module.exports`

Les middlewares `_middleware.js` s'appliquent avant le handler, y compris pour les routes `module.exports` :

```
api/_middleware.js    →  appliqué avant api/users.js (toutes méthodes)
```

---

## Error handlers

### Nommage

```
pages/
├── 404.html       → URL inconnue (not found)
├── 500.html       → erreur serveur générique
├── 422.js         → erreur validation (code 422 spécifique)
├── _error.html    → fallback pour tout code non couvert
└── api/
    ├── 401.html   → non autorisé dans /api/*
    └── _error.js  → erreur générique /api/*
```

Formats reconnus : tout code HTTP de 100 à 599 (ex: `404`, `500`, `422`, `503`).  
Extension valide : `TemplateExt` (`.html` par défaut) ou `.js`.

### Règle de résolution

Pour une erreur de code `C` sur le chemin `/a/b/c/page` :

```
1. Chercher C.ext     dans /a/b/c/
2. Chercher _error.ext dans /a/b/c/
3. Chercher C.ext     dans /a/b/
4. Chercher _error.ext dans /a/b/
5. Chercher C.ext     dans /a/
6. Chercher _error.ext dans /a/
7. Chercher C.ext     dans /
8. Chercher _error.ext dans /
9. → Comportement par défaut
```

**Priorité** : code exact (`500.html`) > wildcard (`_error.html`), et dossier courant > parent.

### Variables disponibles dans les error handlers

| Variable | Type | Description |
|---|---|---|
| `errorCode` | `int` | Code HTTP de l'erreur (ex: `500`) |
| `errorMessage` | `string` | Message d'erreur |
| `context` | object | Contexte de la requête complet |
| `settings` | object | `RouterConfig.Settings` |

**Template HTML** :

```html
<!-- pages/500.html -->
<?js
  var code    = context.Locals("errorCode")    || 500;
  var message = context.Locals("errorMessage") || "Erreur serveur";
?>
<!DOCTYPE html>
<html>
<head><title>Erreur {{code}}</title></head>
<body>
  <h1>{{code}} — {{message}}</h1>
  <a href="/">Retour à l'accueil</a>
</body>
</html>
```

**Handler JS** :

```js
// pages/_error.js
context.JSON({
    error:   errorCode,
    message: errorMessage,
    path:    context.Path(),
});
```

### Déclencher une erreur depuis un handler

```js
// pages/api/users/[id].js
var user = db.find(params.id);
if (!user) {
    throwError(404, "User not found");
}
context.JSON(user);
```

```js
// pages/api/orders.js
module.exports = {
    POST: function(ctx) {
        if (!ctx.Body()) {
            throwError(422, "Body required");
        }
        ctx.Status(201).JSON({ ok: true });
    },
};
```

### Différence 404 routing vs 404 erreur

| Source | Mécanisme |
|---|---|
| URL inconnue (aucune route ne matche) | `find404Handler` → remonte la hiérarchie via `notFoundHandlers` |
| `throwError(404, "...")` depuis un handler | `findErrorHandler` → même algorithme de résolution que les autres codes |

Les deux mécanismes utilisent le même fichier `404.html` / `404.js`.

---

## Layouts

Les fichiers `_layout.html` ou `_layout.js` agissent comme des enveloppes pour toutes les pages situées dans le même répertoire ou ses sous-répertoires.

### Hiérarchie et imbrication

Les layouts sont **récursifs**. Si vous avez des layouts à plusieurs niveaux, ils s'imbriquent du plus profond vers la racine.

```
pages/
├── _layout.html          (1) Root Layout
├── admin/
│   ├── _layout.html      (2) Admin Layout
│   └── users.html        (3) Page
```

Le rendu final pour `/admin/users` sera : `Root Layout` wraps `Admin Layout` wraps `Page Content`.

### Layout HTML

Utilisez la variable `{{content}}` pour indiquer où injecter le contenu de la page (ou du layout enfant).

```html
<!-- pages/_layout.html -->
<div class="site-container">
  <header>Mon Site</header>
  <main>
    {{content}}
  </main>
  <footer>Pied de page</footer>
</div>
```

### Layout JS

Les layouts peuvent aussi être écrits en JavaScript. Le contenu est alors disponible dans la variable globale `content` ou `Locals.content`.

```js
// pages/_layout.js
"<html><body>" + content + "</body></html>"
```

### Variables disponibles

Les layouts ont accès aux mêmes variables que les handlers (si disponibles) :
- `content` : Le contenu HTML de la page ou du layout enfant
- `errorCode` / `errorMessage` : Uniquement lors du rendu d'une erreur
- `context` : `fiber.Ctx`
- `settings` : `RouterConfig.Settings`
- `params` : Paramètres de route dynamique

---

## Partials

Les fichiers dont le nom contient `.partial.` (ex: `info.partial.html`, `data.partial.js`) sont traités comme des pages normales pour le routage, mais ils **ne sont jamais enveloppés dans des layouts**.

### Exemple d'usage

C'est utile pour les fragments HTML chargés via AJAX ou pour des réponses API qui ne doivent pas avoir le header/footer du site.

```
pages/
├── _layout.html           (Layout global du site)
├── full-page.html         → GET /full-page (wrappé dans _layout.html)
└── widget.partial.html    → GET /widget    (SORTIE BRUTE sans _layout.html)
```

### Conversion automatique

Le suffixe `.partial` peut être combiné avec les méthodes HTTP :
- `users.partial.POST.js` → POST `/users` (sans layout)
- `users/[id].partial.js` → GET `/users/:id` (sans layout)

---

## Groupes de layout

Un répertoire entre parenthèses `(nom)` est ignoré dans l'URL générée. Il permet d'organiser les fichiers et d'appliquer des Middlewares ou des **Layouts** spécifiques à un groupe de pages sans affecter l'URL.

```
pages/
├── (auth)/
│   ├── _layout.html    → layout spécifique à login/register
│   ├── login.html      → GET /login
│   └── register.html   → GET /register
└── index.html          → GET /
```

Les groupes peuvent être imbriqués :

```
pages/(marketing)/(2024)/campaign.html  →  GET /campaign
```

Les groupes fonctionnent aussi pour les fichiers statiques :

```
pages/(public)/app.css   →  GET /app.css
```

---

## Variables disponibles en JS

Ces variables sont injectées dans tous les handlers JS et templates par le processor.

### Toujours disponibles

| Variable | Type | Description |
|---|---|---|
| `context` | object | Contexte de la requête complet (requête/réponse) |
| `params` | object | Paramètres de route (`params.id`, `params.slug`…) |
| `catchall` | `string` | Partie catch-all de l'URL (routes `[...param]`) |
| `settings` | object | `RouterConfig.Settings` |
| `process.env` | object | Variables d'environnement |
| `print(...)` | function | Écrit dans le buffer de sortie |
| `include(path)` | function | Inclut et exécute un autre fichier |
| `throwError(code, msg)` | function | Lève une erreur HTTP |
| `require(module)` | function | Charge un module Node-compatible |

### Uniquement dans les error handlers

| Variable | Type | Description |
|---|---|---|
| `errorCode` | `int` | Code HTTP de l'erreur |
| `errorMessage` | `string` | Message d'erreur |

### Uniquement dans les `_middleware.js`

| Variable | Type | Description |
|---|---|---|
| `next` | `function` | Passe au handler suivant |

### Comportement de sortie JS

Un handler JS peut retourner une réponse de trois façons (par ordre de priorité) :

1. **Direct** : `context.JSON(...)`, `context.SendString(...)`, `context.Send(...)` — réponse écrite dans `fiber.Ctx`
2. **Buffer print** : `print("hello")` — accumulé dans `__output()`, envoyé si la réponse est vide
3. **Valeur de retour** : `return "hello"` ou la dernière expression évaluée

---

## Configuration — RouterConfig

```go
type RouterConfig struct {
    // Répertoire racine des pages
    // Défaut : "./pages"
    Root string

    // Extension des fichiers template
    // Défaut : ".html"
    TemplateExt string

    // Nom du fichier index (sans extension)
    // Défaut : "index"
    IndexFile string

    // Config applicative transmise au processor JS/template
    // Défaut : &config.AppConfig{}
    AppConfig *config.AppConfig

    // Variables injectées comme `settings` dans les handlers JS et templates
    Settings map[string]string

    // Handler natif appelé quand aucune route ne matche et qu'aucun 404.html n'existe
    // Défaut : retourne ErrNotFound
    NotFound fiber.Handler

    // Handler natif appelé si le processor retourne une erreur (template/JS invalide)
    // Défaut : retourne HTTP 500
    ErrorHandler func(c fiber.Ctx, err error) error

    // Si true, /blog et /blog/ sont des routes distinctes
    // Défaut : false (trailing slash ignoré)
    StrictSlash bool

    // Patterns d'exclusion des fichiers (voir section Exclusions)
    // Défaut : []*regexp.Regexp{regexp.MustCompile(`^\.`)}
    Exclude []*regexp.Regexp

    // Active la livraison des fichiers statiques
    // Défaut : true
    ServeFiles bool
}
```

### Valeurs par défaut

| Champ | Défaut | Notes |
|---|---|---|
| `Root` | `"./pages"` | Relatif au répertoire de travail |
| `TemplateExt` | `".html"` | Le `.` est ajouté automatiquement si absent |
| `IndexFile` | `"index"` | Sans extension |
| `ServeFiles` | `true` | Opt-out : voir section Exclusions |
| `StrictSlash` | `false` | `/blog/` matche `/blog` |
| `Exclude` | `[regexp.MustCompile("^\.")]` | Exclut les dotfiles |

---

## Exclusions — Exclude

`Exclude` est une liste de regexp testées sur chaque **segment individuel** du chemin (nom de fichier + chaque répertoire) ainsi que sur le **chemin complet** préfixé par `/`.

Un fichier est ignoré si **au moins un pattern** matche **au moins un segment**.

### Défaut

```go
Exclude: []*regexp.Regexp{
    regexp.MustCompile(`^\.`),
}
```

Exclut tout fichier ou répertoire dont le nom commence par `.` : `.env`, `.git/`, `.DS_Store`, `.htaccess`.

### Exemples de patterns

```go
// Exclure les dotfiles (défaut)
regexp.MustCompile(`^\.`)

// Exclure un répertoire entier par nom exact
regexp.MustCompile(`^node_modules$`)
regexp.MustCompile(`^vendor$`)
regexp.MustCompile(`^__pycache__$`)

// Exclure par extension
regexp.MustCompile(`\.bak$`)
regexp.MustCompile(`\.log$`)
regexp.MustCompile(`\.test\.js$`)

// Exclure par chemin complet (depuis la racine)
regexp.MustCompile(`^/private/`)
regexp.MustCompile(`^/internal/`)

// Exclure les fichiers de configuration sensibles
regexp.MustCompile(`^\.env`)
regexp.MustCompile(`^secrets`)
```

### Combinaison de patterns

```go
RouterConfig{
    Root:      "./pages",
    AppConfig: &cfg,
    Exclude: []*regexp.Regexp{
        regexp.MustCompile(`^\.`),             // dotfiles
        regexp.MustCompile(`^node_modules$`),  // dépendances
        regexp.MustCompile(`\.test\.js$`),     // fichiers de test
        regexp.MustCompile(`^/private/`),      // dossier privé
    },
}
```

### Désactiver toutes les exclusions

```go
RouterConfig{
    Exclude:    []*regexp.Regexp{},  // slice vide, non-nil → aucune exclusion
    ServeFiles: true,
}
```

### Désactiver les fichiers statiques

```go
RouterConfig{
    ServeFiles: false,
    Exclude:    []*regexp.Regexp{regexp.MustCompile(`^\.`)},
    // Exclude doit être non-nil pour que ServeFiles=false soit effectif
}
```

> **Règle** : si `Exclude` est `nil` (config non définie), `normalize()` force `ServeFiles = true`. Si `Exclude` est non-nil (config explicite), `ServeFiles` est respecté tel quel.

### Portée de Exclude

`Exclude` s'applique à **tous les types de fichiers** scannés :

- Fichiers statiques (`.css`, `.png`, `.pdf`…)
- Templates (`.html`)
- Handlers JS (`.js`)

---

## Priorité de résolution

Les routes sont triées avant le dispatch. En cas de correspondance multiple, la règle est :

```
Priorité = f(type, profondeur)

Statique exacte    : 1000 − profondeur   (/about = 999, /blog/post = 998)
Dynamique          : 500  − (params × 10) (/blog/:slug = 490, /:a/:b = 480)
Catch-all          : 0
```

### Exemples

| Route | Type | Priorité |
|---|---|---|
| `/` | statique | 1000 |
| `/about` | statique | 999 |
| `/blog/new` | statique | 998 |
| `/blog/:slug` | dynamique 1 param | 490 |
| `/users/:id/posts/:pid` | dynamique 2 params | 480 |
| `/api/*` | catch-all | 0 |

**Conséquence pratique** :

```
GET /blog/new  →  blog/new.html  (statique, gagne)
GET /blog/foo  →  blog/[slug].html  (dynamique)
GET /api/x/y/z →  api/[...rest].js  (catch-all)
```

---

## Debug

`FsRouterDebug` retourne une représentation textuelle de toutes les routes scannées, utile au démarrage pour valider la configuration.

```go
fmt.Print(httpserver.FsRouterDebug(httpserver.RouterConfig{
    Root:        "./pages",
    TemplateExt: ".html",
    AppConfig:   &cfg,
}))
```

**Sortie exemple** :

```
FsRouter — root: ./pages  [ServeFiles:enabled, Exclude patterns:1]

Routes:
  GET      /                              ← index.html [template]
  GET      /about                         ← about.html [template]
  GET      /blog                          ← blog/index.html [template]
  GET      /blog/:slug                    ← blog/[slug].html [template] [dynamic]
  GET      /api/users                     ← api/users.js [export:GET]
  POST     /api/users                     ← api/users.js [export:POST]
  GET      /api/users/:id                 ← api/users/[id].js [export:GET] [dynamic]
  DELETE   /api/users/:id                 ← api/users/[id].js [export:DELETE] [dynamic]
  GET      /api/*                         ← api/[...catch].js [js] [catch-all]

Static files (3):
  GET      /assets/style.css              ← assets/style.css
  GET      /assets/logo.png               ← assets/logo.png
  GET      /favicon.ico                   ← favicon.ico

Middlewares:
  /                              ← _middleware.js
  /api                           ← api/_middleware.js

404 handlers:
  /                              ← 404.html

Error handlers:
  / [500]                        ← 500.html
  / [_error]                     ← _error.html
  /api [422]                     ← api/422.js
  /api [_error]                  ← api/_error.js
```

---

## Référence complète des patterns de nommage

### Mapping fichier → URL

| Fichier | URL | Méthode | Notes |
|---|---|---|---|
| `index.html` | `/` | GET | Index racine |
| `about.html` | `/about` | GET | Route statique |
| `blog/index.html` | `/blog` | GET | Index de sous-répertoire |
| `blog/[slug].html` | `/blog/:slug` | GET | Paramètre dynamique |
| `api/[...rest].js` | `/api/*` | GET | Catch-all |
| `users.POST.js` | `/users` | POST | Méthode par suffixe |
| `users/[id].DELETE.js` | `/users/:id` | DELETE | Méthode + param |
| `cart.js` (avec exports) | `/cart` | GET+POST+… | Multi-méthodes |
| `(auth)/login.html` | `/login` | GET | Groupe ignoré |
| `assets/style.css` | `/assets/style.css` | GET | Fichier statique |
| `(public)/app.wasm` | `/app.wasm` | GET | Statique + groupe |
| `_middleware.js` | *(middleware)* | — | Non routé |
| `404.html` | *(error handler)* | — | Non routé |
| `500.js` | *(error handler)* | — | Non routé |
| `_error.html` | *(error handler)* | — | Non routé |

### Caractères spéciaux dans les noms

| Syntaxe | Signification |
|---|---|
| `[param]` | Paramètre dynamique → `:param` |
| `[...param]` | Catch-all → `*` |
| `(group)` | Groupe de layout — ignoré dans l'URL |
| `.METHOD` | Méthode HTTP explicite (avant l'extension) |
| `_middleware` | Middleware JS |
| `_error` | Error handler générique |
| `_layout` | Layout (HTML ou JS) |
| `.partial`| Désactive l'enveloppement par les layouts |
| `{100-599}` | Error handler pour ce code HTTP |

