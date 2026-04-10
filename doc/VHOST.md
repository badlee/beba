# Virtual Hosts (Vhost)

Le mode **Virtual Host** permet d'héberger plusieurs sites web indépendants sur une seule instance `http-server`. Chaque site dispose de son propre processus, son propre environnement JavaScript, et son propre répertoire racine.

---

## Sommaire

- [Activation](#activation)
- [Architecture Master-Worker](#architecture-master-worker)
- [Structure des répertoires](#structure-des-répertoires)
- [Fichier `.vhost`](#fichier-vhost)
- [Blocs `http` et `https`](#blocs-http-et-https)
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
│   ├── .vhost                   # Configuration HCL (optionnel)
│   ├── index.html               # Contenu du site
│   └── ...
├── site-b.local/
│   ├── .vhost
│   └── ...
└── api.internal/
    ├── .vhost
    └── ...
```

**Règles** :
- Seuls les **dossiers** sont scannés (les fichiers à la racine sont ignorés)
- Chaque dossier = un vhost
- Sans fichier `.vhost`, le **nom du dossier** est utilisé comme hostname
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

## Fichier `.vhost`

Le fichier `.vhost` utilise la syntaxe **HCL** (HashiCorp Configuration Language) pour configurer le vhost.

### Champs disponibles

| Champ | Type | Description |
|---|---|---|
| `domain` | `string` | Nom de domaine principal (remplace le nom du dossier) |
| `aliases` | `list(string)` | Noms de domaine alternatifs |
| `port` | `int` | Port d'écoute (si aucun bloc `http`/`https` n'est défini) |
| `cert` | `string` | Chemin du certificat SSL (active HTTPS si `key` est aussi défini) |
| `key` | `string` | Chemin de la clé privée SSL |
| `email` | `string` | Email global Let's Encrypt pour les notifications |
| `http { }` | block | Bloc listener HTTP |
| `https { }` | block | Bloc listener HTTPS |
| `listen { }` | block | Bloc listener générique (répétable) |

### Exemple minimal

```hcl
# .vhost — le dossier "example.com/" contient ce fichier
domain  = "example.com"
aliases = ["www.example.com"]
```

Le vhost écoute sur le port par défaut (celui du master, `--port`), protocole HTTP.

### Exemple avec port custom

```hcl
domain = "api.local"
port   = 9000
```

### Exemple avec certificats (auto-détection HTTPS)

```hcl
domain = "secure.local"
cert   = "/etc/ssl/secure.local.pem"
key    = "/etc/ssl/secure.local.key"
```

Si `cert` et `key` sont définis mais qu'aucun bloc `http`/`https` n'est présent, le protocole passe automatiquement en **HTTPS**.

---

## Blocs `http` et `https`

Les blocs `http` et `https` permettent de configurer des listeners dédiés.

### Champs des blocs

| Champ | Type | Description |
|---|---|---|
| `port` | `int` | Port d'écoute (défaut: 80 pour `http`, 443 pour `https`) |
| `socket` | `string` | Chemin vers un Unix socket public (optionnel) |

### HTTP seul

```hcl
domain = "app.local"

http {
  port = 8080
}
```

### HTTP + HTTPS (Let's Encrypt)
Si les champs `cert` et `key` sont omis, le serveur tentera d'obtenir automatiquement un certificat SSL gratuit via **Let's Encrypt** pour les domaines listés dans `domain` et `aliases`. Le champ `email` facultatif permet de lier le compte Let's Encrypt à cette adresse pour recevoir des notifications d'expiration.

```hcl
domain = "example.com"
email  = "admin@example.com"

http {
  port = 80
}

https {
  port = 443
}
```

### HTTP + HTTPS dual

```hcl
domain  = "mysite.com"
aliases = ["www.mysite.com"]
cert    = "/etc/letsencrypt/live/mysite.com/fullchain.pem"
key     = "/etc/letsencrypt/live/mysite.com/privkey.pem"

http {
  port = 80
}

https {
  port = 443
}
```

### Bloc `listen` générique

Le bloc `listen` peut être utilisé à la place de ou en complément de `http`/`https`. Il est **répétable**.

```hcl
domain = "multi.local"

listen {
  port = 8080
  # protocol absent → défaut "http"
}

listen {
  port = 9090
}
```

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

Le champ `socket` dans les blocs `http`/`https` permet d'écouter sur un socket fichier au lieu d'un port TCP.

```hcl
http {
  socket = "/var/run/myapp.sock"
}
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
cat > vhosts/mon-site.local/.vhost << 'EOF'
domain = "mon-site.local"
http {
  port = 8080
}
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
│   ├── .vhost          # domain = "blog.example.com"
│   └── index.html
├── shop.example.com/
│   ├── .vhost          # domain = "shop.example.com"
│   └── index.html
└── docs.example.com/
    └── index.html      # Pas de .vhost → hostname = "docs.example.com"
```

### Production HTTPS multi-domaines

```hcl
# sites/app.example.com/.vhost
domain  = "app.example.com"
aliases = ["www.example.com"]

http {
  port = 80
}

https {
  port = 443
  # autocert Let's Encrypt
}
```

Voir aussi : [examples/vhosts/](../examples/vhosts/) pour des exemples fonctionnels.

---
*Dernière mise à jour : 18 Mars 2026*
