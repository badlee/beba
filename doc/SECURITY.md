# Security Guide — 5-Layer Defense-in-Depth

L'architecture de sécurité de **http-server** repose sur un modèle de défense en profondeur à 5 couches, allant du filtrage réseau (L3/L4) à l'observabilité avancée.

---

## 🏗️ Architecture des 5 Couches

### Couche 1 : Sécurité Réseau (L3/L4)
Le filtrage commence dès l'acceptation de la connexion TCP, avant même l'analyse du protocole HTTP.

*   **Connection Rate Limiting** : Protection contre les inondations (SYN flood) et le brute-force réseau.
    *   Directive : `CONNECTION RATE [limit] [window] [burst] [mode=ip]`
*   **IP allowlist/blocklist** : Support complet des CIDR et des fichiers externes.
    *   Directive : `CONNECTION [ALLOW|DENY] [valeur]`
*   **Geo-Fencing** : Blocage par pays (ISO) ou par zones précises via GeoJSON.
    *   Directive : `GEOJSON [nom] [fichier]` + `CONNECTION ALLOW [nom]`
*   **Programmable Hooks** : JavaScript personnalisé pour le filtrage IP (`CONNECTION IP`) et Geo (`CONNECTION GEO`).

### Couche 2 : Durcissement du Protocole (HTTP)
Validation stricte des caractéristiques de la requête HTTP.

*   **Body Size Limit** : Empêche l'épuisement de la mémoire par de gros payloads.
    *   Défaut : 4 Mo (configurable via `BodyLimit`).
*   **Content-Type Enforcement** : Rejette les requêtes dont le type MIME ne correspond pas à l'attendu.
    *   Middleware : `@CONTENTTYPE["application/json"]`
*   **Path Traversal Prevention** : Confinement automatique des handlers de fichiers (`runFileHandler`).

### Couche 3 : Inspection Applicative (WAF)
Analyse profonde des payloads pour détecter les injections et attaques logiques.

*   **Moteur Coraza WAF** : Intégration native de Coraza v3.
*   **OWASP Core Rule Set (CRS)** : Protection contre SQLi, XSS, LFI, RCE, etc.
    *   Directive : `OWASP [chemin_rules]`
*   **Hooks JS** : Interception du cycle de vie WAF (`ON REQUEST_HEADERS`, `ON INTERRUPTED`).

### Couche 4 : Identité & Comportement (Anti-Bot)
Distinction entre utilisateurs réels et agents automatisés.

*   **Bot Detection** : Analyse des signaux (User-Agent, headers) pour calculer un score de suspicion.
    *   Middleware : `@BOT[js_challenge=true threshold=50]`
*   **JS Challenge** : Défi interactif "Proof-of-Work" pour les clients suspects.
*   **CSRF Protection** : Protection contre les requêtes inter-sites via validation de jetons/headers.

### Couche 5 : Observabilité & Audit
Journalisation infalsifiable et métriques en temps réel.

*   **Audit Log Signé** : Chaînage cryptographique **HMAC-SHA256** de chaque ligne pour détecter toute altération.
    *   Middleware : `@AUDIT[path="security.log" sign=true]`
*   **Métriques Prometheus** : Compteurs détaillés des blocages par raison (waf, geo, limit).

---

## 🛡️ Sécurité par Défaut (Baseline)

Même sans configuration explicite, **http-server** applique une politique de sécurité de base :

| Fonctionnalité | Paramètre par Défaut |
|---|---|
| **Rate Limit Global** | 100 req/s (Burst: 10, Window: 1s) |
| **Body Limit** | 4 Mo (configurable globalement) |
| **Secret Key** | Génération aléatoire de 32 octets (crypto/rand) si vide |
| **Path Traversal** | Activé (Confinement strict `filepath.Join` interdit) |
| **Panic Recovery** | Activé (Capture des crashs applicatifs) |

---

## ⚖️ Comparaison avec Nginx et Apache

| Caractéristique | http-server | Nginx | Apache |
|---|---|---|---|
| **Configuration** | DSL Unifié (HCL-like) | Conf modulaire complexe | Directives XML/Flat (Lourd) |
| **WAF Intégré** | Natif (Coraza + CRS) | Module externe (ModSecurity) | Module externe (ModSec) |
| **Scripting** | JavaScript natif | Lua (OpenResty) ou NJS | C ou divers (Perl/Python) |
| **Geo-fencing** | **GeoJSON natif (Haute précision)** | MaxMind GeoIP seulement | MaxMind GeoIP seulement |
| **Audit Log** | **Signé cryptographiquement** | Texte simple | Texte simple |
| **Couches L4 + L7** | **Config unique (BIND file)** | Divers (iptables + nginx.conf) | Divers (iptables + httpd.conf) |
| **Anti-Bot** | Natif (JS Challenge) | Complexe (Modules tiers) | Complexe (Modules tiers) |

---

## 🚀 Exemple de Configuration Globale

```hcl
# Profil de sécurité complet
SECURITY production_shield [default]
    ENGINE On
    OWASP "./rules/coreruleset/*.conf"
    
    # L1: Réseau
    CONNECTION RATE 200r/s 1m burst=20
    CONNECTION DENY "889CM4V2+PQ" # Plus Code
    
    # L5: Audit
    AUDIT DEFINE
        Path "logs/security_audit.log"
        Signed true
        Level security
    END AUDIT
END SECURITY

HTTP 0.0.0.0:80
    # Applique le shield à toutes les routes
    SECURITY production_shield
    
    # L4: Protection bot sur les zones sensibles
    GET @BOT[js_challenge=true] "/login" HANDLER login.js
    
    # L2: Validation type sur API
    POST @CONTENTTYPE["application/json"] "/api/v1/update" HANDLER update.js
END HTTP
```
