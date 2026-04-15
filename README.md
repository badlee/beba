# Le Backend Hyper-média Ultime pour Développeurs Modernes

**http-server** est un **serveur hyper-média** et un **backend Open Source** "all-in-one" distribué sous la forme d'un **seul fichier** binaire auto-contenu. Oubliez la complexité des infrastructures Docker et micro-services : déployez une **application fullstack** complète en quelques secondes avec un moteur alliant la rapidité du **SSR** (Mustache/JS) à l'élégance de **HTMX**.

### **Pourquoi intégrer http-server à votre stack ?**

*   **📂 Routage par Fichiers (inspiré de Next.js/Nuxt.js)**  
    Organisez votre logique backend avec la simplicité du routage basé sur les dossiers. Comme dans **Next.js** ou **Nuxt.js**, la structure de votre répertoire définit vos routes, incluant le support natif des paramètres dynamiques (`[id]`), des layouts imbriqués et des middlewares en cascade.
    
*   **⚡ Rendu SSR & Scripting JS Natif**  
    Boostez vos performances avec un moteur **JavaScript** serveur intégré. Définissez votre logique métier directement dans vos templates ou scripts isolés, bénéficiant d'un pont direct vers vos données sans l'overhead d'une API externe.
    
*   **🛠️ Engine Database/CRUD Unifié & Admin UI**  
    Basculez en mode **Headless CMS** instantanément. Le module **DATABASE** unifie désormais le moteur CRUD et les schémas dynamiques. Il génère automatiquement vos API REST et une interface d'administration temps-réel (propulsée par HTMX + SSE), gérant nativement les relations (`has=one`, `many`, `many2many`) et les migrations différées sécurisées (Dual Struct).
    
*   **📡 Hub Realtime Massivement Scalable (+1M de clients)**  
    Le cœur du système : un hub de messagerie haute performance capable de gérer **plus d'un million de clients simultanément**. Support natif de **SSE**, **WebSocket**, **MQTT over WebSocket** et **Socket.IO**. Grâce au **Binder** innovant, multiplexez ces protocoles sur un seul port pour une interopérabilité totale.
    
*   **🛡️ Sentinelle de Sécurité intégrée**  
    Allez au-delà du simple HTTPS. Profitez d'une protection en profondeur à 5 couches : filtrage IP/Géo au niveau socket (L4), détection de bots par Proof-of-Work, et un WAF Coraza (L7) natif pour bloquer SQLi et XSS avant qu'ils ne touchent votre code.

## Architecture & Modules
- **Binder (`modules/binder`)** : Multiplexage de protocoles (HTTP, DTP, MQTT, JS custom) sur un même port via des fichiers `.bind`.
- **Temps-Réel (`modules/sse`)** : Hub SSE/WS/MQTT/IO sharded haute performance (1M+ connexions).
- **Base de Données (`modules/db` & `modules/crud`)** : Moteur unifié Mongoose-like (GORM) avec support des relations, schémas dynamiques et migrations "Dual Struct" anti-panique.
- **Paiement (`modules/binder/payment`)** : Intégration unifée Stripe, Mobile Money et Crypto (X402).
- **Sécurité (`modules/security`)** : Moteur de filtrage L4 et pare-feu applicatif (WAF).
- **Scripting Script (`processor/`)** : Rendu hybride Mustache/JS et exécution server-side isolée.

## ⚔️ Le "Killer Feature" Table : http-server vs Nginx vs Apache

| Caractéristique | http-server | Nginx | Apache HTTPD |
|---|---|---|---|
| **Découverte & Run** | **Binaire Unique (0 dep)** | Paillage complexe | Paillage complexe |
| **Port Multiplexing** | **Natif (1 Port = N Protos)** | Séparé par Port / Proxy | Séparé par Port |
| **Paiements Natifs** | **Directives Stripe/MoMo/X402** | N / A | N / A |
| **Web App Firewall** | **Coraza + CRS (Natif)** | Module ModSec (tiers) | Module ModSec (tiers) |
| **Security Audit** | **Signé (HMAC Chain)** | Texte Simple | Texte Simple |
| **Scripting Logic** | **JavaScript Natif (Isolé)** | Lua (Complexe) / NJS | PHP/C (Interpréteur ext) |
| **Géo-fencing** | **GeoJSON, GéoIP et Plus Code** | GéoIP (Pays uniquement) | GéoIP (Pays uniquement) |
| **Real-time Hub** | **Natif (+1M Connexions)** | Plugins tiers (Nchan) | Support minimal |
| **IoT Integration** | **DTP & MQTT Unifié** | Websocket simple | Plugins lourds |
| **Dev Experience** | **Hot-reload & FsRouter** | Configuration Statique | Configuration Statique |

## 🛡️ Une Forteresse à 5 Couches (Architecture Sentinelle)

http-server embarque une défense en profondeur à 5 couches pour protéger vos applications IoT et Web :

1.  **L1 Network** : Filtrage IP/CIDR et **GeoJSON Dynamic Fencing** au niveau de l'acceptation de la socket.
2.  **L2 Protocol** : Validation stricte des méthodes et des limites de corps (BodyLimit) pour TCP et UDP.
3.  **L3 Applicative (WAF)** : Moteur Coraza intégré avec les règles CRS (Core Rule Set) pour bloquer SQLi/XSS.
4.  **L4 Identity** : Détection de Bots et **JS Challenge (Proof-of-Work)** natifs pour les requêtes suspectes.
5.  **L5 Audit** : Journalisation immuable via **Chaînage de hash HMAC** garantissant l'intégrité des logs.

## ✨ Fonctionnalités Clés

- **Ultra-Rapide** : Optimisé pour des performances "Bare-Metal" sans overhead de runtime externe.
- **SSR & JS Server-Side** : Exécution de `<script server>` et balises PHP-style (`<?js ?>`) hautement performantes.
- **FsRouter (File-System Routing)** : Routage automatique type Next.js avec `[id]`, `[...]`, `(group)` et `_middleware.js`.
- **DTP (Device Transfer Protocol)** : Protocole IoT natif TCP/UDP avec bridge automatique vers le Hub SSE.
- **Broker MQTT 5.0** : Broker natif ultra-performant unifié avec le Hub SSE et persistance GORM (`STORAGE`).
- **Module Paiement Universel** : Directive `PAYMENT` supportant Stripe, Mobile Money (MTN/Orange) et les protocoles crypto **X402**.
- **Geofencing GeoJSON** : Filtrage géographique précis (L4) via la directive `GEOJSON` compatible TCP et **UDP**.
- **Middlewares Nommés** : `@CORS`, `@LIMITER`, `@HELMET`, `@CSRF`, `@AUDIT`, `@BOT`, `@WAF` applicables par route.
- **Auth Multi-Algorithmes** : Support natif Bcrypt, SHA-512, et scripting JS (`allow()`/`reject()`).
- **Binder – Multiplexage de Protocoles** : Configuration déclarative `.bind` pour mixer HTTP, DTP, MQTT, etc. sur le même port.

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

Consultez la [Documentation CLI](doc/CLI.md) pour la liste complète. Les options majeures :
- `--port, -p` : Port d'écoute (défaut: 8080).
- `--bind, -b` : Fichiers de configuration `.bind`.
- `--hot-reload, -H` : Activer le rechargement à chaud (défaut: true).
- `--vhosts, -V` : Activer le mode Virtual Hosts.
- `--secure, -S` : Activer HTTPS (nécessite `--cert` et `--key`).

## Exemple Binder (Sécurité + Paiement)

```hcl
# main.bind
SECURITY globale [default]
    CONNECTION RATE 500r/s 1s burst=50
    GEOJSON office_zone "data/office.geojson"
    CONNECTION ALLOW office_zone
END SECURITY

PAYMENT "stripe://sk_live_xxx" [default]
    NAME my_stripe
    CURRENCY EUR
    REDIRECT success "/success"
    WEBHOOK @POST /stripe/callback BEGIN
        if (payment.status === "succeeded") { sse.publish("donations", payment) }
    END WEBHOOK
END PAYMENT

HTTP 0.0.0.0:8080
    SET APP_NAME "SecureApp"

    # Route protégée par WAF et nécessitant un paiement
    POST @WAF @PAYMENT[name=my_stripe price="10.00" ref="premium_access"] "/premium/data" JSON
        {"status": "paid", "data": "Top secret info"}
    END POST

    # Schéma avec relations et contraintes
    DATABASE "sqlite://:memory:"
        SCHEMA Project DEFINE
            FIELD name string [required]
            FIELD owner_id User.id [has=one delete=cascade]
            FIELD members User.id [has=many2many]
        END SCHEMA
    END DATABASE

    # Hub Real-time unifié
    SSE /events
END HTTP
```

## Documentation

| Fichier | Description |
|---|---|
| [Binder Config (.bind)](doc/BINDER.md) | **Référence complète** (HTTP, DTP, MQTT, SECURITY, PAYMENT) |
| [WAF & Security](doc/WAF.md) | Guide des **5 couches Sentinelle** et du filtrage L4 |
| [Payment Module](doc/PAYMENT.md) | Stripe, Mobile Money et standard Crypto X402 |
| [Database & CRUD](doc/DATABASE.md) | API Mongoose-like, drivers GORM et Admin HTMX |
| [Server-Side JS](doc/JS_SCRIPTING.md) | Interpréteur Javascript et API disponibles |
| [Real-time & MQTT](doc/MQTT.md) | Hub unifié SSE/WS/MQTT/IO |
| [DTP IoT Protocol](doc/DTP.md) | Protocole industriel natif |
| [CLI Usage](doc/CLI.md) | Flags et options avancés |

---
*Déployez, Sécurisez, Encaissez. http-server.*
Exemples Rapides

- [Showcase Temps-Réel (SSE/WS/MQTT/IO)](examples/realtime_showcase.bind)
- [Routage Fichier (FsRouter)](examples/advanced_features.bind)
- [Multiplexage TCP (HTTP + DTP)](examples/multiplex_test.bind)
