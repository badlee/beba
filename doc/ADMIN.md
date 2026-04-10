# Interface d'Administration DATABASE (Admin UI)

Le module DATABASE de **http-server** dispose d'une interface d'administration temps réel intégrée, propulsée par HTMX, SSE (Server-Sent Events) et le système de templating natif `processor`.

Elle permet d'ajouter très facilement de nouvelles métriques, pages, et liens externes dans son menu via une API native simple.

---

## 🚀 Fonctionnement natif

L'Admin UI ne requiert aucune configuration complexe ni compilation React/Vue. Dès qu'une instance `DATABASE` (avec configuration CRUD) est enregistrée sur votre routeur, le dashboard est automatiquement monté.

```bind
HTTP :8080
    CRUD ma_base /api
END HTTP
```

L'interface sera disponible sur : `http://localhost:8080/api/_admin/`

> **Note :** L'authentification à l'admin nécessite un compte **Root** (soit un utilisateur en base de données dont la colonne `namespace_id` est `NULL`).

---

## 🧩 API d'Extensibilité (Custom Pages & Links)

L'interface d'administration est hautement modulaire. Vous pouvez étendre son menu latéral et ajouter de nouvelles pages entièrement gérées par le layout de l'application grâce au registre global du module DATABASE.

Ces configurations se font côté serveur, avant ou pendant le démarrage de `http-server`.

### 1. Ajouter une page interne (`AdminPage`)

La structure `crud.AdminPage` définit une route personnalisée intégrée au Layout (Menu, CSS global, connexions au flux SSE de base de données).

```go
package main

import "http-server/modules/crud"

func init() {
    crud.RegisterAdminPage(crud.AdminPage{
        Path:     "/metrics",              // URL relative (devient /api/_admin/metrics)
        Title:    "Vue Système",           // Nom affiché dans le menu de gauche
        Icon:     "bar-chart",             // (Optionnel) Classe d'icône SVG/etc
        Order:    10,                      // (Optionnel) Position dans le menu
        Template: `
            <h1>Métriques temps réel</h1>
            <div class="card">
                <p>État de la mémoire...</p>
                <?js
                   // Injecter un peu de logique JS serveur
                   var memDate = new Date().toISOString();
                ?>
                <small>Actualisé à : <?= memDate ?></small>
            </div>
        `,
    })
}
```

#### Variables de template disponibles
Votre `Template` est envoyé au package `processor` (qui supporte les balises **Mustache** et le JS serveur `<?js ?>`). Vous avez accès par défaut à :
- `{{pageTitle}}` : Le titre configuré (ex: "Vue Système").
- `{{adminPrefix}}` : Le préfixe global de l'Admin UI (utile pour construire des URLs relatives de navigation `href="{{adminPrefix}}/metrics"`).
- `{{apiPrefix}}` : Le préfixe de l'API REST CRUD (utile pour requêter HTMX ou Fetch l'API `hx-get="{{apiPrefix}}/users"`).

### 2. Ajouter un lien de menu externe (`AdminLink`)

Si vous souhaitez rediriger un administrateur hors de l'application (ex: Dashboard Grafana, Stripe, Analytics externes), enregistrez un `AdminLink`.

```go
crud.RegisterAdminLink(crud.AdminLink{
    Title: "Grafana Analytics",
    Icon:  "monitoring", 
    URL:   "https://grafana.mondomaine.com",
    Order: 20, // Plus l'Order est bas, plus il apparaît haut.
})
```
*Ces liens s'ouvrent par défaut dans un nouvel onglet de navigateur (`target="_blank"`).*

---

## ⚡ HTMX & Base de Données (Bonnes pratiques)

Si vous développez des vues complexes dans vos `AdminPage`, vous pouvez (et êtes encouragés à) utiliser les attributs `hx-*` préchargés :

```html
<button class="btn btn-primary"
    hx-post="{{apiPrefix}}/collections/_custom_action"
    hx-swap="outerHTML">
    Forcer le nettoyage
</button>
```

Le layout principal importe :
- **HTMX Core** (`htmx.org`) pour le support SPA.
- **SSE Extension** (`htmx-ext-sse`) pour les subscriptions aux websockets unilatéraux.
- **Le CSS global "Admin"** comprenant des classes utilitaires telles que `.card`, `.btn btn-primary`, `.form-group`, ou `.table-wrap`.

### Réagir aux événements CRUD temps-réel (SSE)

Le client écoute sur la route `/_admin/sse` (qui diffuse les actions `crud::create`, `crud::update`, `crud::delete`, etc.). Le backend JS client génère automatiquement un événement HTML global (`crud-event`).
Vous pouvez l'attraper dans n'importe quel `<script>` ajouté depuis votre `AdminPage` personnalisé :

```javascript
<script>
document.addEventListener('crud-event', function(evt) {
    const data = evt.detail;
    // data.action = "update", "create", "delete"...
    // data.schema = le slug de collection
    console.log("Activité détectée :", data);
});
</script>
```
