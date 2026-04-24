# Beba – 0 SDK. 0 Framework. 0 Plugin.

**Beba** est un serveur backend "all-in-one" distribué sous la forme d'un unique binaire auto-contenu.

```bash
./beba
# Votre application tourne. Elle continuera de tourner.
```

> *Beba. La seule dépendance, c'est Beba.*

---

## Pour les non-développeurs (chefs de projet, décideurs)

### Ce que vous devez comprendre

Avec une stack logicielle classique, votre application repose sur une pyramide de dépendances :

```
Votre application
    ↓
Framework (Next.js, React, Laravel...)
    ↓
Langage (Node.js, Python, PHP...)
    ↓
Package manager (npm, pip, composer...)
    ↓
Centaines de bibliothèques tierces
    ↓
Système d'exploitation
```

**Chaque couche est une facture potentielle :**
- Mise à jour de sécurité obligatoire
- Migration de version majeure (payante en temps de développement)
- Obsolescence programmée (Node.js 16 n'est plus supporté)
- Vulnérabilités dans une bibliothèque tierce que vous n'avez jamais installée volontairement

**Avec Beba :**

```
Votre application
    ↓
Beba (un seul binaire)
    ↓
Système d'exploitation
```

**C'est tout.**

### Pourquoi vous n'aurez pas de surprises

| Scénario | Stack classique | Beba |
|----------|-----------------|------|
| **Votre application fonctionne. Vous ne voulez rien changer.** | Vous devez quand même mettre à jour pour des raisons de sécurité ou de compatibilité. Coût : jours de développement. | Vous ne changez rien. Beba continue de fonctionner. Coût : zéro. |
| **Vous revenez sur un projet après 2 ans d'arrêt.** | Probablement cassé. Il faut tout remettre à jour. Coût : semaines de travail. | `./beba` → ça marche. Coût : zéro. |
| **Une vulnérabilité est découverte dans une bibliothèque.** | Vous devez identifier laquelle, mettre à jour, tester. Coût : imprévisible. | Pas de bibliothèques tierces. Coût : zéro. |
| **Vous changez d'hébergeur ou de serveur.** | Reinstaller tout l'environnement (Node.js, npm, etc.). Coût : plusieurs heures. | Copier le binaire. Coût : 2 minutes. |

### Ce que vous économisez

- **Pas d'architecte DevOps** pour gérer la chaîne de build
- **Pas de veille technologique** pour suivre les évolutions de frameworks
- **Pas de migrations forcées** tous les 18 mois
- **Pas de "security patch"** qui casse votre application
- **Pas de coût caché** dans les dépendances

**Beba est livré sous licence Beba License. Vous l'utilisez. Il tourne. Point.**

---

## Pour les développeurs

### La dette technique, vous la connaissez.

Un projet abandonné 18 mois avec une stack classique :

```bash
git pull
npm install  # ERESOLVE unable to resolve dependency tree
npm audit    # 12 vulnerabilities (4 critical)
npm run build  # TypeScript errors, deprecated APIs
# 3 jours plus tard, ça ne tourne toujours pas.
```

Un projet Beba abandonné 18 mois :

```bash
./beba
# 200 OK. Tout fonctionne.
```

**Pourquoi ?** Beba n'a **aucune dépendance externe**.

| Métrique | Stack classique (Next.js/Node.js) | Beba |
|----------|----------------------------------|------|
| Fichiers dans `node_modules` | 35 000+ | 0 |
| Taille des dépendances | 500 MB - 2 GB | 0 |
| Fichiers de configuration | 8-15 | 0-1 |
| `package.json` à maintenir | Oui | Non |
| Vulnérabilités tierces | Oui | Non |
| Runtime externe requis | Node.js | Non |
| API framework sujette à changement | Oui (Next.js App Router) | Non (stable par conception) |

### Ce que vous n'aurez jamais à faire

| Tâche | Pourquoi vous ne la ferez jamais |
|-------|----------------------------------|
| `npm install` | Rien à installer. |
| Résoudre un conflit `ERESOLVE` | Pas de packages. |
| Configurer Webpack, Vite, esbuild | Pas de bundler. |
| Écrire `useEffect` pour appeler votre API | Rendu serveur natif. |
| Maintenir la compatibilité entre SDKs | Pas de SDK. |
| Scanner 500 packages pour une CVE | Pas de packages. |
| Migrer de `pages/` vers `app/` | Routage par fichiers stable. |
| Gérer plusieurs versions de Node.js | Pas de runtime externe. |

### Le prix de la liberté

Beba vous demande une seule chose : **garder la bonne version de Beba**.

- Pas de mise à jour forcée
- Pas de compatibility breaking imposée
- Vous contrôlez quand vous évoluez

Si votre application n'a pas besoin de nouvelles fonctionnalités, vous n'avez aucune raison de mettre à jour Beba.

### Ce que vous gardez

| Élément | Standard | Portable sans Beba ? |
|---------|----------|---------------------|
| Données | SQLite / PostgreSQL | Oui (fichier .db) |
| Templates | HTML + Mustache | Oui |
| Routes | Fichiers dans dossiers | Oui (structure simple) |
| Scripts | JavaScript standard | Oui |

**Beba ne vous enferme pas. Si vous partez, vos données et votre logique repartent avec vous.**

---

## Ce que Beba fait (techniquement)

Beba est un binaire Go (50-70 MB) qui contient **tout** :

| Fonctionnalité | Implémentation | Dépendance externe ? |
|----------------|----------------|---------------------|
| Serveur HTTP | Moteur Fiber/Fasthttp intégré | **Non** |
| Base de données | GORM + drivers SQLite/Postgres/MySQL | **Non** (compilés) |
| Moteur JavaScript | Goja (embarqué) | **Non** |
| Templates | Moteur Mustache maison + JS serveur | **Non** |
| Temps-réel | Hub SSE/WS/MQTT/IO shardé | **Non** |
| WAF | Coraza compilé | **Non** |
| Paiements | Appels HTTP natifs | **Non** (pas de SDK) |
| Emails | Appels HTTP natifs | **Non** |
| Authentification | Système unifié interne | **Non** |

**Aucun appel à `npm install`. Aucun `go get`. Aucun `pip install`. Rien.**

---

## Installation

```bash
git clone https://github.com/badlee/beba.git
cd beba
go build -o beba .
```

Ou binaire pré-compilé :

```bash
wget https://github.com/badlee/beba/releases/latest/beba-linux-amd64
chmod +x beba-linux-amd64
```

---

## Utilisation minimale

```bash
./beba
```

**Dès ce moment, sans aucun fichier :**
- `http://localhost:8080` → votre site (pages/ par défaut)
- `http://localhost:8080/_admin` → interface d'administration
- `http://localhost:8080/api/users` → API REST automatique
- Base SQLite persistante dans `./.data/beba.db`
- Broker MQTT sur port 1883
- Hub temps-réel (SSE/WebSocket/MQTT/Socket.IO)

**Vos données survivent aux redémarrages. Sans configuration.**

---

## Conclusion

> *"La meilleure dépendance est celle que vous n'avez pas."*

**Beba n'a pas de dépendances.**
- Pas de `node_modules` à auditer
- Pas de framework à migrer
- Pas de runtime externe à maintenir
- Pas de SDK à suivre
- Pas de plugin à compatibiliser

**Beba a un binaire, un dossier `.data`, et vos fichiers.**

Dans 5 ans, vous pourrez prendre le même binaire, et votre application tournera toujours.

**0 SDK. 0 Framework. 0 Plugin. Beba.**

---

*Beba – Un binaire. Zéro dépendance. Des possibilités infinies.*

---

**Liens :**
- [Documentation complète](doc/)
- [LICENSE](LICENSE)
- [GitHub](https://github.com/badlee/beba)
