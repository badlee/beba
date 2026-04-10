# Module Cookies

Le module `cookies` permet de manipuler les cookies du navigateur directement depuis l'environnement JavaScript du serveur.

## Utilisation

Le module est généralement accessible via la variable `cookies` ou en utilisant `require('cookies')`.

```javascript
const cookies = require('cookies');

// Récupérer un cookie
const theme = cookies.get('theme');

// Définir un cookie
cookies.set('user_id', '12345');

// Vérifier l'existence d'un cookie
if (cookies.has('session')) {
    console.log('Utilisateur connecté');
}

// Supprimer un cookie
cookies.remove('temp_data');
```

## API Reference

### `get(name)`
Récupère la valeur du cookie spécifié par `name`.
- **Retourne** : `string` (valeur du cookie ou chaîne vide si non trouvé).

### `set(name, value)`
Définit un cookie avec le nom et la valeur fournis.
- **Note** : Actuellement, cette méthode utilise les paramètres par défaut du serveur (cookie de session).

### `remove(name)`
Supprime le cookie spécifié par `name`.

### `has(name)`
Vérifie si un cookie existe et possède une valeur non vide.
- **Retourne** : `boolean`.
