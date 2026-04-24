# Authentication System (AUTH)

La directive `AUTH` fournit un système d'authentification flexible, unifié et hautement sécurisé pour l'ensemble de l'écosystème Beba (HTTP, HTTPS, MQTT, DTP).

Il repose sur deux piliers :
1.  **Gestionnaires Globaux (`AUTH DEFINE`)** : Définition de banques d'utilisateurs réutilisables, avec support OAuth2 (Client et Serveur).
2.  **Directives de Montage** : Application d'un gestionnaire ou d'une stratégie locale sur un protocole ou une route spécifique.

---

## 1. Gestionnaires Globaux (`AUTH [name] DEFINE`)

Un gestionnaire global permet de centraliser la configuration de sécurité (Secret JWT, base de données de sessions) et de combiner plusieurs sources d'utilisateurs.

### Syntaxe exhaustive
```hcl
AUTH "main-auth" DEFINE
    SECRET "votre-cle-secrete-jwt"     // Utilisé pour signer les jetons d'accès
    DATABASE "sqlite://auth.db"        // Stockage des jetons révoqués (JTI) et sessions

    // --- Serveur OAuth2 (Beba comme Provider) ---
    SERVER DEFINE
        TOKEN_EXPIRATION "1h"          // Durée de validité des Access Tokens
        ISSUER "beba-cloud"            // Nom de l'émetteur dans le JWT
        LOGIN "./public/login.html"    // Interface de login personnalisée
    END SERVER

    // --- Clients OAuth2 (Social Login) ---
    STRATEGY "google" DEFINE
        CLIENTID "xxx.apps.googleusercontent.com"
        CLIENTSECRET "GOCSPX-xxx"
        REDIRECT "https://votre-site.com/auth/callback/google"
        SCOPE "openid email profile"
    END STRATEGY

    // --- Sources d'utilisateurs locales ---
    USER "admin" "{BCRYPT}$2a$12$..." // Utilisateur statique
    USERS JSON "./users.json"          // Fichier JSON (Map username -> pwd)
    USERS CSV "./devices.csv"          // Fichier CSV (username;pwd;[proto])
    
    // Handler scripté complexe
    AUTH BEGIN
        if (user === "root" && pwd === "secret") allow();
        else reject("Accès interdit");
    END AUTH
END AUTH
```

---

## 2. Support du Hachage (Hashing)

Beba supporte nativement le hachage sécurisé des mots de passe. Le système détecte automatiquement l'algorithme via le préfixe `{ALG}` ou le format du hash.

### Algorithmes supportés
- **BCRYPT** : Recommandé. Détecté par le préfixe `$2a$`, `$2b$`, `$2y$` ou explicitement par `{BCRYPT}`.
- **SHA512** : Préfixe `{SHA512}`.
- **SHA256** : Préfixe `{SHA256}`.
- **SHA1** : Préfixe `{SHA1}`.
- **MD5** : Préfixe `{MD5}` (déconseillé pour les mots de passe, utile pour les legacy).

### Encodages des Hashs
Le hash binaire résultant peut être stocké dans les formats suivants (détection automatique) :
*   **Base64** : Standard (ex: `K7gNU3sdo+OL0wNhqoVWhr3g6s1xYv72ol/pe/Unols=`)
*   **Hexadécimal** : Minuscule ou Majuscule (ex: `2bb80d537b1d...`)
*   **Base32** : Sans padding, RFC4648 (ex: `FP4A2U3X...`)

**Exemple d'usage :**
```hcl
AUTH USER "admin" "{SHA256}K7gNU3sdo+OL0wNhqoVWhr3g6s1xYv72ol/pe/Unols="
```

---

## 3. Intégration par Protocole

### HTTP / HTTPS
Montage d'un gestionnaire global sur une route. Les endpoints `/login`, `/me`, `/logout` et `/callback/:strategy` sont automatiquement générés.

```hcl
HTTP :80
    // Monte le gestionnaire "main-auth" sur le préfixe /auth
    AUTH "main-auth" /auth
    
    GET /private BEGIN
        // Cette route nécessite une authentification via le manager associé
        if (!ctx.User()) return ctx.SendStatus(401);
        ctx.SendString(`Hello ${ctx.User().Username}`);
    END GET
END HTTP
```

### MQTT
Utilisé pour valider le paquet `CONNECT`.

```hcl
TCP :1883
    MQTT
        AUTH BEGIN
            // Logique spécifique aux objets connectés
            if (username.startsWith("device_") && password === "secret123") {
                allow();
            }
        END AUTH
    END MQTT
END TCP
```

### DTP (Distributed Transmission Protocol)
Utilisé pendant le handshake pour valider l'identité de l'appareil (DeviceID/Secret).

```hcl
TCP :80
    DTP
        USERS CSV "./inventory.csv" // Format: device_id;secret;use_proto
        
        DATA "TELEMETRY" BEGIN
            print(`Données de ${device.DeviceID}: ${payload}`);
        END DATA
    END DTP
END TCP
```

---

## 4. Authentification Scriptée (JavaScript)

Permet d'injecter du code arbitraire pour valider les accès.

**Variables disponibles :**
- `user`, `username` : Identifiant fourni par le client.
- `pwd`, `password` : Mot de passe ou token secret.
- `config` : Map d'arguments passés à la directive.

**Méthodes :**
- `allow(secret, useProto)` : Valide l'accès. 
    - *Secret* : (Optionnel) Pour DTP, définit le secret utilisé pour le HMAC.
    - *useProto* : (Optionnel) Pour DTP, booléen forçant l'usage de Protobuf.
- `reject(message)` : Refuse l'accès avec un message explicatif.

**Debug & Environnement :**
Les scripts d'authentification s'exécutent dans un environnement **unifié** (via le package `processor`). Ils bénéficient de :
- **Modules natifs** : `require('db')`, `require('auth')`, `require('http')`, etc.
- **Fonctions de log** : `print()` et `console.log()` pour le débogage en temps réel dans les logs du serveur.
- **Base Directory** : Accès relatif aux fichiers par rapport au dossier racine du site/vhost.
