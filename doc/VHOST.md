# Virtual Hosts (Vhost)

Le mode **Virtual Host** permet d'héberger plusieurs sites web indépendants sur une seule instance `http-server`. Chaque site dispose de son propre processus, son propre environnement JavaScript, et son propre répertoire racine.

---

## Sommaire

- [Activation](#activation)
- [Architecture Master-Worker](#architecture-master-worker)
- [Structure des répertoires](#structure-des-répertoires)
- [Configuration unifiée (.bind)](#configuration-unifiee-bind)
- [Certificats SSL / TLS](#certificats-ssl--tls)
- [Sockets transparents](#sockets-transparents)
- [Propagation des flags](#propagation-des-flags)
- [Isolation des processus](#isolation-des-processus)
- [Exemples](#exemples)

---

## Activation

```bash
./http-server ./vhosts --vhost
./http-server ./vhosts -V             # shorthand
./http-server ./vhosts --vhost --port 8080
```

Le flag `--vhost` (ou `-V`) active le mode Virtual Host. Le premier argument positionnel désigne le **répertoire parent** contenant les dossiers des vhosts.

> [!NOTE]
> Le mode vhost est un **flag statique** (`#vhost`) : il ne peut pas être rechargé à chaud. Un redémarrage est requis pour activer ou désactiver le mode.

---

## Architecture Master-Worker

```
                    ┌────────────────────────────┐
                    │     Master Process         │
                    │  (reverse proxy + SNI)     │
                    │                            │
                    │  :8080   (HTTP)            │
                    │  :443    (HTTPS)           │
                    └─┬──────────┬──────────┬────┘
                      │          │          │
               ┌──────▼──-┐ ┌────▼────┐ ┌───▼─────┐
               │ Worker 0 │ │Worker 1 │ │Worker 2 │
               │ site-a   │ │ site-b  │ │  api    │
               │ UDS 0    │ │ UDS 1   │ │ UDS 2   │
               └──────────┘ └─────────┘ └─────────┘
```

1. **Master** : écoute sur les ports publics (HTTP, HTTPS), inspecte le `Host` header, et route la requête vers le bon worker via un proxy interne.
2. **Workers** : chaque vhost est un processus enfant indépendant, écoutant sur un Unix Domain Socket (UDS) privé dans `/tmp`.

Le master transmet la requête complète au worker via `fasthttp.Client` (proxy), préservant les headers, cookies et body.

---

## Structure des répertoires

```
vhosts/                          # Répertoire racine (passé en argument)
├── site-a.local/                # Un site = un dossier
│   ├── .vhost.bind              # Configuration unifiée (recommandé)
│   ├── index.html               # Contenu du site
│   └── ...
├── site-b.local/
│   ├── .vhost                   # Équivalent à .vhost.bind
│   └── ...
└── api.internal/
    ├── .vhost.bind
    └── ...
```

**Règles** :
- Seuls les **dossiers** sont scannés (les fichiers à la racine sont ignorés)
- Chaque dossier = un vhost
- Sans fichier `.vhost`, le **nom du dossier** est utilisé comme hostname
- Sans fichier `.vhost` ou `.vhost.bind`, le **nom du dossier** est utilisé comme hostname
- Chaque worker effectue un `chdir` dans son dossier avant de servir

### Routage puissant avec FsRouter

Chaque virtual host utilise nativement le système **FsRouter**. Cela signifie que le dossier racine du vhost agit comme un routeur exhaustif basé sur les fichiers, offrant automatiquement :
- **Pages fixes** (`index.html`, `about.html`) avec le moteur de templates
- **Endpoints API** via script JS (ex: `api/users.GET.js`)
- **Routes dynamiques** (ex: `blog/[slug].html` ou `api/[id].js`)
- **Middlewares locaux** (`_middleware.js` appliqué au dossier et sous-dossiers)
- **Handlers d'erreurs** (ex: `404.html`, `500.js`)
- **Fichiers statiques** (images, styles) servis intelligemment

> Pour désactiver FsRouter et servir uniquement des fichiers statiques de manière basique, lancez l'application avec `--no-template`.

---

## Configuration unifiée (.bind)

Le fichier de configuration (`.vhost` ou `.vhost.bind`) utilise la syntaxe **Binder**.

### Mots-clés disponibles

| Mot-clé | Description |
|---|---|
| `DOMAIN` | Nom de domaine principal (remplace le nom du dossier) |
| `ALIAS` | Nom de domaine alternatif (un seul) |
| `ALIASES` | Noms de domaine alternatifs (séparés par des virgules) |
| `PORT` | Port d'écoute spécifique pour ce bloc |
| `SSL` | `SSL cert.pem key.pem` (active HTTPS) |
| `EMAIL` | Email Let's Encrypt pour les notifications |

### Exemple minimal

```bind
HTTP ":80"
  DOMAIN "example.com"
  ALIAS "www.example.com"
END HTTP
```

Le vhost écoute sur le port 80, protocole HTTP.

### Exemple HTTPS (Let's Encrypt)

```bind
HTTPS ":443"
  DOMAIN "example.com"
  EMAIL "admin@example.com"
END HTTPS
```

Si `SSL` est omis dans un bloc `HTTPS`, le serveur tente d'obtenir un certificat via Let's Encrypt.

### Exemple avec Certificats manuels

```bind
HTTPS ":443"
  DOMAIN "secure.local"
  SSL "/etc/ssl/cert.pem" "/etc/ssl/key.pem"
END HTTPS
```

### Exemple Multi-Protocoles

```bind
HTTP ":80"
  DOMAIN "multi.local"
END HTTP

TCP ":9090"
  # Pas de domaine requis pour TCP brut, routage par port
END TCP

---

## Certificats SSL / TLS

### Certificats manuels

Fournir `cert` et `key` dans le bloc `https` ou à la racine du fichier `.vhost`.

### Autocert (Let's Encrypt)

Si un bloc `https` est défini **sans** `cert`/`key`, le master utilise automatiquement **Let's Encrypt** :

1. Procure un certificat via le protocole ACME
2. Gère les challenges sur le listener HTTP (port 80)
3. Cache les certificats dans le dossier local
4. Renouvelle avant expiration

#### Isolation et limitations de Rate-Limiting

Afin d'éviter d'être bloqué par les limites de requêtes Let's Encrypt (ex: 50 certificats par semaine pour un domaine global, etc.), l'architecture des `Managers` est asymétrique :

- **Avec un champ `email`** : Le vhost bénéficie de sa **propre instance** `autocert.Manager` complètement isolée. Le cache ACME est stocké dans un sous-répertoire dédié : `./certs/<domaine>/`. 
- **Sans champ `email`** : Le vhost utilise le Manager `autocert` **global**. Le cache est centralisé dans le répertoire parent `./certs/`.

La résolution SNI et le routage ACME (`/.well-known/acme-challenge/`) assurent dynamiquement que le challenge et le certificat sont servis par le bon Manager en fonction du hostname appelé !

> [!IMPORTANT]
> Pour l'autocert, un listener HTTP (port 80) est requis pour les challenges ACME. Assurez-vous qu'un bloc `http { port = 80 }` est défini.

---

## Sockets transparents

Le champ `SOCKET` (à venir) permettra d'écouter sur un socket fichier.

```bind
HTTP ":8080"
  # SOCKET "/var/run/myapp.sock"
END HTTP
```

Le comportement est transparent cross-platform :
- **Linux / macOS** : utilise le chemin directement (AF_UNIX)
- **Windows** : convertit automatiquement en Named Pipe (`C:\temp\site.sock` → `\\.\pipe\C_temp_site.sock`)

---

## Propagation des flags

Le master propage tous les flags CLI **explicitement fournis** aux workers enfants, sauf les flags réservés au master :

| Flag exclu | Raison |
|---|---|
| `--vhost` | Réservé au master |
| `--port` | Chaque worker a son propre socket |
| `--address` | Communication interne uniquement |
| `--silent` | Forcé à `true` pour les workers |
| `--socket` | Attribué automatiquement par le master |

Tous les autres flags (compression, templates, CORS, cache, etc.) sont transmis intégralement.

---

## Isolation des processus

Chaque vhost bénéficie de :

- **Mémoire isolée** : son propre espace mémoire dédié
- **Environnement JS isolé** : son propre runtime de moteur JavaScript
- **Répertoire de travail dédié** : `chdir` automatique vers le dossier du vhost
- **Fichiers .env locaux** : rechargement automatique depuis le répertoire du vhost
- **Crash isolation** : un crash dans un vhost n'affecte pas les autres

Le master gère le cycle de vie :
- `SIGTERM` / `SIGINT` → tue proprement tous les workers
- Nettoyage automatique des sockets UDS

---

## Exemples

### Démarrage rapide

```bash
# Créer la structure
mkdir -p vhosts/mon-site.local
echo '<h1>Mon Site</h1>' > vhosts/mon-site.local/index.html
cat > vhosts/mon-site.local/.vhost.bind << 'EOF'
HTTP
  DOMAIN "mon-site.local"
END HTTP
EOF

# Lancer
./http-server vhosts --vhost --port 8080

# Tester
curl -H "Host: mon-site.local" http://localhost:8080/
```

### Multi-sites sur un seul port

```bash
./http-server sites/ --vhost --port 80
```

```
sites/
├── blog.example.com/
│   ├── .vhost.bind     # DOMAIN "blog.example.com"
│   └── index.html
├── shop.example.com/
│   ├── .vhost          # DOMAIN "shop.example.com"
│   └── index.html
└── docs.example.com/
    └── index.html      # Pas de .vhost → hostname = "docs.example.com"
```

### Production HTTPS multi-domaines

```bind
# sites/app.example.com/.vhost.bind
HTTPS ":443"
  DOMAIN "app.example.com"
  ALIASES "www.example.com"
  EMAIL "admin@example.com"
END HTTPS
```

Voir aussi : [examples/vhosts/](../examples/vhosts/) pour des exemples fonctionnels.

---
*Dernière mise à jour : 16 Avril 2026*
