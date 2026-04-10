# Exemple : Virtual Hosts

Cet exemple montre comment héberger plusieurs sites sur une seule instance `http-server` grâce au mode **Virtual Host**.

## Structure

```
vhosts/
├── site-a.local/           # Site HTTP avec FsRouter avancé
│   ├── .vhost              # domain + alias + http { port = 8080 }
│   ├── index.html          # Page d'accueil vue
│   ├── _middleware.js      # Middleware appliqué à ce vhost
│   └── api/
│       └── [id].js         # Route dynamique gérée par JS
├── site-b.local/           # Site HTTPS + HTTP
│   ├── .vhost              # domain + alias + https + http blocks
│   └── index.html
└── api.internal/           # API sur port custom
    ├── .vhost              # domain + port = 9000
    └── index.html
```

## Lancer

```bash
# Depuis la racine du projet
./http-server examples/vhosts --vhost --port 8080
```

Le processus master :
1. Scanne chaque sous-dossier de `examples/vhosts/`
2. Lit les fichiers `.vhost` (syntaxe HCL)
3. Spawn un worker par site sur un Unix socket interne
4. Écoute sur le port 8080 et route par hostname

## Tester localement

Ajouter dans `/etc/hosts` :
```
127.0.0.1  site-a.local www.site-a.local
127.0.0.1  site-b.local www.site-b.local
127.0.0.1  api.internal
```

Puis :
```bash
curl -H "Host: site-a.local" http://localhost:8080/
curl -H "Host: site-b.local" http://localhost:8080/
curl -H "Host: api.internal" http://localhost:8080/
```

## Documentation complète

Voir [doc/VHOST.md](../../doc/VHOST.md) pour la référence complète.
