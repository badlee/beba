# Module: CRUD (Automated Data APIs)

Le module **CRUD** permet de générer instantanément des APIs REST, des interfaces d'administration et des flux temps-réel à partir de définitions de schémas.

---

## Fonctionnalités Clés

- **Zero-Code APIs** : Génération automatique des routes `GET`, `POST`, `PUT`, `DELETE` pour vos modèles.
- **Persistence Native** : Intégration avec GORM (SQLite, PostgreSQL, MySQL).
- **Vision Globale** : Toute base de donnée initialisée par un bloc `CRUD` est enregistrée dans le registre global du serveur (accessible via `require('db')` ou par la directive `MQTT STORAGE`).
- **SSE Real-time** : Diffusion automatique des changements (`insert`, `update`, `delete`) sur le Hub SSE central.
- **Admin UI** : Interface d'administration intégrée accessible via `/_admin` (si configurée).

---

## Configuration Binder

```hcl
CRUD "sqlite://data.db"
    NAME "main_api"
    IS_DEFAULT ON
    
    SCHEMA user DEFINE
        FIELD name string [required]
        FIELD email string [unique, required]
        FIELD age int
    END SCHEMA
END CRUD
```

### Paramètres de Directive
- **`NAME [alias]`** : Définit le nom de la connexion DB dans le registre global.
- **`IS_DEFAULT [ON|OFF]`** : Si `ON`, cette base devient la cible par défaut du module `db` en JavaScript.

---

## Interopérabilité MQTT

Grâce à l'enregistrement global des connexions, un bloc `CRUD` peut servir de backend de stockage pour le broker MQTT :

```hcl
CRUD "sqlite://iot.db"
    NAME "iot_storage"
END CRUD

TCP :1883
    MQTT
        STORAGE "iot_storage" # Se connecte à la DB créée par CRUD cidessus
    END MQTT
END TCP
```

---

## API JavaScript

Le module `db` permet d'accéder aux modèles CRUD :

```javascript
const db = require("db");
const User = db.model("user");

async function findActiveUsers() {
    return await User.find({ age: { $gte: 18 } }).exec();
}
```

---

## Événements Temps-Réel

Chaque opération CRUD publie un événement sur le Hub SSE :
- **Channel** : `crud.{schema}.{operation}`
- **Payload** : JSON contenant l'objet créé/modifié (ainsi qu'un snapshot `prev` pour les updates).

---

> [!TIP]
> Pour plus d'informations sur la définition des schémas, consultez [doc/DATABASE.md](DATABASE.md).
