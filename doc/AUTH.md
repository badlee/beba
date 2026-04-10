# Authentication System (AUTH)

La directive `AUTH` fournit un système d'authentification flexible et unifié pour les protocoles HTTP, HTTPS et DTP.

## Syntaxe de base

La directive `AUTH` peut être utilisée de plusieurs manières selon la source de données.

### 1. Fichiers de configuration (JSON, YAML, TOML, ENV)
Charge une liste d'utilisateurs et de mots de passe (ou hachages) depuis un fichier.

```hcl
AUTH JSON config/users.json
AUTH YAML config/users.yaml
AUTH TOML config/users.toml
AUTH ENV .env.auth
```

> [!NOTE]
> Le format doit être un objet simple clé-valeur : `"username": "password"`.

### 2. Fichiers CSV
```hcl
AUTH CSV users.csv
```
Le fichier CSV doit utiliser le point-virgule `;` comme séparateur :
`username;password;[proto_bool]`
- `proto_bool` (optionnel) : Pour DTP, indique si l'appareil doit utiliser Protocol Buffers.

### 3. Utilisateur unique
```hcl
AUTH USER admin p@ssword
```

### 4. Authentification scriptée (JavaScript)
Permet une logique d'authentification complexe (ex: vérification dynamique, calculs).

```hcl
AUTH
    if (username === "admin" && password === config.SUPER_PWD) {
        allow();
    } else {
        reject("Accès refusé");
    }
END AUTH
```

- **Variables disponibles** : `username`, `password` (alias `user`, `pwd`), `config` (si des arguments `KEY=VALUE` sont passés à `AUTH`).
- **Helpers** :
  - `allow()` : Autorise l'accès (HTTP/DTP).
  - `reject(msg...)` : Refuse l'accès. Accepte un message optionnel (utilisé comme Realm dans HTTP).
  - `allow(secret, useProto)` : **Spécifique DTP** — Définit le secret de l'appareil et son mode de transport (Protocol Buffers).

> [!TIP]
> L'implémentation utilise des canaux bufferisés pour garantir que l'exécution du script ne bloque pas le serveur, même en cas d'appels multiples à `allow()` ou `reject()`.

## Support du Hachage (Hashing)
Tous les mots de passe peuvent être stockés en clair, hachés avec **Bcrypt** ou via d'autres algorithmes standards. Le serveur détecte automatiquement le format :

- **Bcrypt** : Détecté par les préfixes `$2a$`, `$2b$`, `$2y$`, `$2x$`.
- **Algorithmes {ALG}** : Supporte les préfixes `{SHA512}`, `{SHA256}`, `{SHA1}`, `{MD5}`.
- **Encodage** : Le hachage après le préfixe peut être en **Hexadécimal**, **Base32** (RFC4648, sans padding) ou **Base64** standard.

**Exemple de génération (SHA-256) :**
```bash
echo "{SHA256}`printf 'secret' | openssl dgst -binary -sha256 | base64`"
# Résultat : {SHA256}K7gNU3sdo+OL0wNhqoVWhr3g6s1xYv72ol/pe/Unols=
```

**Usage dans Binder :**
```hcl
AUTH USER admin "{SHA512}vSsar3708Jvp9Szi2NWZZ02Bqp1qRCFpbcTZPdBhnWgs5WtNZKnvCXdhztmeD2cmW192CF5bDufKRpayrW/isg=="
```

## Intégration par Protocole

### HTTP / HTTPS
L'authentification est implémentée via le standard **Basic Authentication**. Le client recevra un header `WWW-Authenticate` s'il n'est pas authentifié.

### DTP
L'authentification est utilisée pendant le handshake DTP.
- `OnGetDevice` : Utilise `AUTH` pour trouver le secret correspondant au `DeviceID`.
- `OnAuthDevice` : Vérifie le token HMAC généré à partir du secret.

### MQTT
Le broker MQTT (sur WebSocket) supporte l'authentification lors de la connexion via le paquet `CONNECT`. 
- **Mapping** : Les credentials fournis dans les champs `username` et `password` de la trame MQTT sont validés par le système `AUTH` configuré sur la route ou via une fonction native.
