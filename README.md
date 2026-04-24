# Beba – 0 SDK. 0 Framework. 0 Plugin.

**Beba** est un serveur hyper-média backend "all-in-one" distribué sous la forme d'un unique binaire auto-contenu. Aucune dépendance externe. Aucun runtime à installer. Aucune chaîne de build.

```bash
./beba
# Votre backend est en ligne. Persistant. Stable.
```

> *Beba. La seule dépendance, c'est Beba.*

---

## La dette technique, vous la connaissez.

Un projet abandonné 18 mois avec une stack classique (Next.js, React, Node.js) :

- `npm install` → conflits de dépendances
- `npm audit` → vulnérabilités critiques
- La moitié des tests ne passent plus
- Le framework a changé d'API deux fois

Un projet Beba abandonné 18 mois :

```bash
./beba
# Tout fonctionne. Rien n'a cassé.
```

**Pourquoi ?** Parce que Beba n'a **aucune dépendance externe** :

| Problème classique | Solution Beba |
|--------------------|---------------|
| `node_modules` (500 MB, 35 000 fichiers) | Un seul binaire (50-70 MB) |
| `package.json` à maintenir | Pas de fichier de dépendances |
| Conflits `ERESOLVE` | Impossible |
| Vulnérabilités dans les packages tiers | Pas de packages tiers |
| Mise à jour forcée du runtime (Node.js) | Le binaire contient tout |
| API framework qui change (Next.js App Router) | L'API est le système lui-même |
| SDK clients qui évoluent (Stripe, SendGrid...) | Appels HTTP natifs, pas de SDK |

---

## Ce que vous n'aurez jamais

| **Vous n'aurez pas à...** | **Parce que...** |
|---------------------------|------------------|
| Installer Node.js, Python, Ruby, PHP | Le binaire est autonome. |
| Gérer un `package.json` | Il n'y a aucune dépendance à lister. |
| Résoudre des conflits de versions | Il n'y a qu'une seule version : la vôtre. |
| Attendre `npm install` | Rien à installer. |
| Configurer Webpack, Vite, esbuild | Pas de bundler. |
| Écrire `useEffect` pour appeler votre API | Le rendu est serveur, prêt à l'emploi. |
| Maintenir la compatibilité entre SDKs | Pas de SDK. |
| Migrer de `pages/` vers `app/` | Le routage par fichiers est stable et documenté. |
| Subir une dépréciation d'API majeure | L'API, c'est Beba. Vous contrôlez sa mise à jour. |
| Scanner 500 packages pour une CVE | Pas de packages, pas de CVEs tierces. |

---

## Une seule dépendance

Voici tout ce dont vous avez besoin pour faire tourner un backend complet :

```bash
# Étape 1 : Obtenir Beba
git clone https://github.com/badlee/beba.git
cd beba
go build -o beba .

# Étape 2 : Lancer
./beba

# Étape 3 : C'est fini.
```

**Dès le premier lancement, sans aucun fichier de configuration :**

- Base de données SQLite persistante (`./.data/beba.db`)
- API REST automatique (CRUD) sur `/api`
- Interface d'administration (`/_admin`) (HTMX + SSE)
- Hub temps-réel : SSE, WebSocket, MQTT, Socket.IO
- Broker MQTT sur port 1883
- Routage par fichiers (FsRouter)
- JavaScript côté serveur (`<?js ?>`, `<script server>`)

**Zéro configuration. Zéro dépendance. Zéro surprise dans 2 ans.**

---

## Maintenabilité dans le temps

| Scénario | Stack classique | Beba |
|----------|-----------------|------|
| **Projet abandonné 2 ans** | `npm install` échoue, `next build` cassé, runtime obsolète. | `./beba` → fonctionne immédiatement. |
| **Changement de poste** | `git clone` + `npm install` (200 MB, 3 min, erreurs potentielles). | `git clone` + `go build` (60 MB, 10 secondes). |
| **Mise à jour OS** | Node.js peut ne plus être compatible. | Le binaire contient tout. Il tourne partout. |
| **Vulnérabilité critique** | Scanner 500 packages, espérer un correctif, prier. | Rien. Pas de dépendances. |
| **Passation à un autre développeur** | Expliquer la stack, les scripts, les versions. | `./beba`. Il comprend en 30 secondes. |

---

## Pourquoi c'est possible

Beba n'est pas un wrapper. Beba **est** l'outil. Chaque fonctionnalité est compilée dans le binaire, sans appel à des bibliothèques externes.

| Fonctionnalité | Implémentation interne | Dépendance externe ? |
|----------------|------------------------|---------------------|
| Serveur HTTP | Moteur Fiber/Fasthttp intégré | **Non** |
| Base de données | GORM + drivers SQLite/Postgres/MySQL compilés | **Non** |
| Moteur JavaScript | Goja (embarqué) | **Non** |
| Templates | Moteur Mustache maison + JS serveur | **Non** |
| Temps-réel | Hub SSE/WS/MQTT/IO shardé maison | **Non** |
| WAF | Coraza compilé dans le binaire | **Non** |
| Paiements | Stripe/MoMo/X402 – appels HTTP natifs | **Non** (pas de SDK) |
| Emails | SMTP/SendGrid/Mailgun – appels HTTP natifs | **Non** |
| Authentification | Système unifié interne | **Non** |

**La seule chose que Beba appelle à l'extérieur, ce sont les APIs des services que vous utilisez (Stripe, SendGrid, etc.). Pas de bibliothèques clientes. Pas de SDK. Juste des appels HTTP standards.**

---

## Aucun SDK

Un SDK est une promesse de commodité qui devient une dette :

- Le SDK évolue. Votre code doit suivre.
- Le SDK a des dépendances. Leurs dépendances aussi.
- Le SDK change d'API tous les 18 mois.

**Avec Beba, vous n'utilisez aucun SDK.** Vous envoyez des requêtes HTTP standard. Vous manipulez du JSON. Vous écrivez du SQL ou utilisez l'API CRUD native.

```javascript
// Pas de SDK Stripe. Juste une requête HTTP.
const response = await fetch('https://api.stripe.com/v1/charges', {
  method: 'POST',
  headers: { 'Authorization': 'Bearer sk_xxx' },
  body: JSON.stringify({ amount: 999, currency: 'eur' })
});
```

**Votre code n'a pas besoin de savoir que Stripe existe.** Changez l'URL, changez de fournisseur. Pas de migration de SDK.

---

## Ce que vous gardez

| Élément | Standard | Portable ? |
|---------|----------|------------|
| **Données** | SQLite / PostgreSQL / MySQL | Oui. Exportez votre base, utilisez-la ailleurs. |
| **Templates** | HTML + Mustache + JS | Oui. Lisibles, modifiables, transférables. |
| **Routes** | Fichiers dans des dossiers | Oui. Rien de propriétaire. |
| **Scripts** | JavaScript standard | Oui. Pas de syntaxe exotique. |

Beba ne vous enferme pas. Beba vous libère des outils qui vous enferment.

---

## Installation et utilisation

### Depuis les sources

```bash
git clone https://github.com/badlee/beba.git
cd beba
go build -o beba .
```

### Binaire pré-compilé

```bash
wget https://github.com/badlee/beba/releases/latest/beba-linux-amd64
chmod +x beba-linux-amd64
./beba-linux-amd64
```

### Mode simple

```bash
./beba
```

**Vous avez immédiatement :**
- `http://localhost:8080` → votre site
- `http://localhost:8080/_admin` → interface d'administration
- `http://localhost:8080/api/users` → API REST automatique
- Broker MQTT sur port 1883
- Hub temps-réel sur `/sse`, `/ws`, `/api/realtime/mqtt`

### Avec configuration avancée

```bash
./beba --bind app.bind
```

### Mode Virtual Hosts

```bash
./beba ./vhosts --vhosts
```

---

## Documentation technique complète

| Fichier | Description |
|---------|-------------|
| [BINDER.md](doc/BINDER.md) | Configuration `.bind` – Référence complète |
| [ROUTER.md](doc/ROUTER.md) | FsRouter – Routage par fichiers (Next.js-like) |
| [HTTP.md](doc/HTTP.md) | HTTP/HTTPS – Moteur web, SSL, middlewares |
| [DATABASE.md](doc/DATABASE.md) | Base de données – Schémas, relations, API CRUD |
| [ADMIN.md](doc/ADMIN.md) | Admin UI – Interface d'administration |
| [JS_SCRIPTING.md](doc/JS_SCRIPTING.md) | Scripting JS – API serveur, modules natifs |
| [SECURITY.md](doc/SECURITY.md) | Sécurité – Architecture Sentinelle 5 couches |
| [PAYMENT.md](doc/PAYMENT.md) | Paiements – Stripe, Mobile Money, Crypto X402 |
| [MQTT.md](doc/MQTT.md) | MQTT – Broker temps-réel unifié |
| [DTP.md](doc/DTP.md) | DTP – Protocole IoT natif (TCP/UDP) |
| [IO.md](doc/IO.md) | Socket.IO – Support natif |
| [MAIL.md](doc/MAIL.md) | Emails – SMTP, SendGrid, Mailgun |
| [TEMPLATING.md](doc/TEMPLATING.md) | Templates – Mustache + JavaScript |
| [STORAGE.md](doc/STORAGE.md) | Session & Cache – Persistance et JWT |
| [VHOST.md](doc/VHOST.md) | Virtual Hosts – Multi-sites, Master-Worker |
| [CLI.md](doc/CLI.md) | Ligne de commande – Flags et options |

---

## Pourquoi le nom Beba ?

**Beba** signifie *"Tous, Tout le monde"* en langue **Akélé** (Gabon).

- **Universalité** : Beba sert tous les développeurs, tous les projets, tous les protocoles.
- **Communauté** : Beba rassemble base de données, API, temps-réel, sécurité et paiements.
- **Rareté** : Un nom unique, sans collision, qui porte une histoire.

> *Beba. Pour tous, partout.*

---

## Contribution

1. Tester le projet
2. Signaler des bugs
3. Soumettre des pull requests
4. Écrire des exemples ou tutoriels

---

## Licence

Beba License – voir le fichier [LICENSE](LICENSE).

---

*Déployez, Sécurisez, Encaissez. Beba.*
