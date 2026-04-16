# FsRouter

Le **FsRouter** est le cœur du système de routage de `http-server`. Contrairement aux routeurs traditionnels où les routes sont déclarées programmatiquement, le FsRouter utilise la structure de votre répertoire comme table de routage.

Inspiré par des frameworks modernes comme Next.js, il permet une organisation intuitive et une séparation claire entre le contenu statique, les templates dynamiques et les endpoints API.

---

## Sommaire

- [Structure et Correspondances](#structure-et-correspondances)
- [Fichiers Spéciaux](#fichiers-spéciaux)
- [Cycle de Vie du Serveur](#cycle-de-vie-du-serveur)
- [Tâches Planifiées (CRON)](#tâches-planifiées-cron)
- [Routes Dynamiques et Catch-all](#routes-dynamiques-et-catch-all)
- [Méthodes HTTP](#méthodes-http)
- [Middlewares et Layouts](#middlewares-et-layouts)
- [Gestion des Erreurs](#gestion-des-erreurs)

---

## Structure et Correspondances

Le dossier racine (par défaut `./pages/`) définit l'arborescence des URLs.

| Fichier | URL | Type |
|---|---|---|
| `index.html` | `/` | Template |
| `about.html` | `/about` | Template |
| `api/status.js` | `/api/status` | Handler JS |
| `assets/logo.png` | `/assets/logo.png` | Fichier Statique |
| `blog/[slug].html` | `/blog/:slug` | Route Dynamique |

---

## Fichiers Spéciaux

Les fichiers commençant par un underscore `_` ont des comportements spéciaux et ne sont pas routés directement comme des pages publiques.

| Nom | Description |
|---|---|
| `_middleware.js` | S'exécute avant toute route dans ce répertoire et ses sous-dossiers. |
| `_layout.html` | Enveloppe commune pour toutes les pages du répertoire. |
| `_start.js` | S'exécute une seule fois au démarrage du serveur. |
| `_close.js` | S'exécute lors de la fermeture propre du serveur. |
| `_*.cron.js` | Tâche planifiée (ex: `_cleanup.cron.js`). |

---

## Cycle de Vie du Serveur

Le FsRouter permet d'exécuter du code JS lors des phases critiques du serveur.

### Démarrage (`_start.js`)
Tout fichier nommé `_start.js` à la racine d'un dossier du projet sera exécuté lors de l'initialisation du routeur. C'est l'endroit idéal pour initialiser des bases de données ou pré-remplir des caches.

```js
// _start.js
console.log("System initialization...");
// Initialisation de ressources globales
```

### Fermeture (`_close.js`)
Le fichier `_close.js` est enregistré auprès du système de fermeture de l'application. Il garantit que vos scripts de nettoyage s'exécutent même si le serveur reçoit un signal d'arrêt (SIGTERM/SIGINT).

```js
// _close.js
console.log("Cleaning up resources before exit...");
```

---

## Tâches Planifiées (CRON)

Les fichiers dont le nom suit le pattern `_*.cron.js` sont gérés par le planificateur interne.

### Définition du planning
La première ligne du fichier doit contenir un en-tête `CRON` suivi d'une expression standard (Minute, Heure, Jour, Mois, Jour de la semaine).

```js
# CRON */5 * * * *
// S'exécute toutes les 5 minutes
console.log("Running periodic cleanup: " + new Date());
```

Si l'en-tête est absent, le fichier est exécuté une seule fois au démarrage (comportement identique à `_start.js`).

### Syntaxe supportée
- `*` : Toutes les valeurs.
- `*/n` : Intervalles (ex: `*/15` pour toutes les 15 unités).
- `n-m` : Plages de valeurs (ex: `1-5`).
- `1,3,5` : Listes de valeurs.

---

## Routes Dynamiques et Catch-all

### Paramètres de route
Utilisez des crochets `[]` pour capturer des segments de l'URL.
- `pages/user/[id].js` → `/user/42` (accessible via `params.id`).

### Catch-all
Utilisez `[...]` pour capturer tout le reste du chemin.
- `pages/docs/[...slug].html` → `/docs/intro/getting-started` (accessible via `catchall`).

---

## Méthodes HTTP

Un fichier JS peut gérer plusieurs méthodes de deux manières :

1. **Suffixe de fichier** : `users.POST.js`, `items.DELETE.js`.
2. **Export d'objet** :
```js
module.exports = {
    GET: function(ctx) { ... },
    POST: function(ctx) { ... }
};
```

---

## Middlewares et Layouts

### Middlewares (`_middleware.js`)
Les middlewares s'appliquent en cascade (du dossier racine vers les sous-dossiers).

```js
// _middleware.js
if (!context.Get("session")) {
    throwError(401, "Auth Required");
}
next();
```

### Layouts (`_layout.html`)
Les layouts permettent d'entourer le contenu d'une page avec des éléments communs (header, footer). Le contenu de la page est injecté à l'emplacement du tag `{{content}}`.

---

## Gestion des Erreurs

Les fichiers nommés par un code HTTP (ex: `404.html`, `500.js`) ou `_error.html` servent de handlers pour les codes d'erreur correspondants. Le routeur cherche le handler le plus proche dans la hiérarchie des dossiers.
