# Security Guide — Sentinel 5-Layer Defense

The **http-server** security architecture, known as **Sentinel**, relies on a 5-layer defense-in-depth model, ranging from socket-level filtering (L4/L1) to advanced observability and cryptographic auditing (L5).

---

## 🏗️ The 5-Layer Sentinel Architecture

### Layer 1: Network & Socket Security (L4)
Filtering begins at the socket acceptance level (TCP) or packet level (UDP), before any protocol analysis occurs.

*   **Connection Rate Limiting**: Protection against SYN floods and rapid-fire network attacks.
    *   Directive: `CONNECTION RATE [limit] [window] [burst] [mode=ip]`
*   **IP Access Control**: Fully supports CIDR ranges and external blacklist files.
    *   Directive: `CONNECTION [ALLOW|DENY] [value]`
*   **Precision Geo-Fencing**: Block by country (ISO) or exact zones via GeoJSON.
    *   Directive: `GEOJSON [name] [file]` + `CONNECTION ALLOW [name]`
*   **Programmable Hooks**: Custom JavaScript for IP-level (`CONNECTION IP`) and Geo-level (`CONNECTION GEO`) filtering.
    *   Example: `CONNECTION IP BEGIN if(CONN.ip === '1.2.3.4') reject('Blocked'); END CONNECTION`

---

## 🏗️ Protocol Unified Security
Since the **Sentinel** layer operates at the socket level, all protocols (HTTP, MQTT, DTP) share the same protection. Even if a protocol is multiplexed on a single port, the security block configured for that port (or the `default` baseline) is applied before the protocol handlers are summoned.

### Layer 2: Protocol Hardening (HTTP/DTP/MQTT)
Strict validation of protocol characteristics and resource usage.

*   **Body Size Limits**: Prevents memory exhaustion via large payloads.
    *   Default: 4MB (configurable).
*   **Content-Type Enforcement**: Rejects requests that don't match the expected MIME type.
    *   Middleware: `@CONTENTTYPE["application/json"]`
*   **Path Traversal Prevention**: Automatic confinement for all file handlers.

### Layer 3: Application Inspection (WAF)
Deep payload analysis to detect injections and logical attacks.

*   **Coraza WAF Engine**: Native integration of Coraza v3.
*   **OWASP Core Rule Set (CRS)**: Protection against SQLi, XSS, LFI, RCE, and more.
    *   Directive: `OWASP [rules_path]`
*   **Transactional Hooks**: Intercept WAF lifecycle events (`ON REQUEST_HEADERS`, `ON INTERRUPTED`).

### Layer 4: Identity & Behavior (Anti-Bot)
Distinguishing between real users and automated agents.

*   **Bot Detection**: Analysis of signals (User-Agent, headers) to calculate a suspicion score.
    *   Middleware: `@BOT[js_challenge=true threshold=50]`
*   **Proof-of-Work Challenges**: Interactive JS challenges for suspect clients.
*   **CSRF Protection**: Native anti-forgery token validation.

### Layer 5: Observability & Integrity (Audit)
Unfalsifiable logging and real-time security metrics.

*   **Cryptographically Signed Audit Logs**: **HMAC-SHA256** chaining of every log entry to detect tampering.
    *   Middleware: `@AUDIT[path="security.log" sign=true]`
*   **Security Metrics**: Detailed Prometheus counters for blocks (waf, geo, limiter).

---

## 🛡️ Default Security Baseline

Even without explicit configuration, **http-server** enforces a hardcoded security baseline:

| Feature | Default Setting |
|---|---|
| **Global Rate Limit** | 100 req/s (Burst: 10, Window: 1s) |
| **Body Limit** | 4MB (Global) |
| **Secret Key** | 32-byte random (crypto/rand) if not provided |
| **Path Traversal** | Enabled (Strict confinement) |
| **Panic Recovery** | Enabled (Catches application crashes) |

---

## 🚀 Example: Full Production Shield

```hcl
# High-security profile
SECURITY production_shield [default]
    ENGINE On
    OWASP "./crs/*.conf"
    
    # Layer 1: Network
    CONNECTION RATE 200r/s 1m burst=20
    GEOJSON data_center "dc.geojson"
    CONNECTION ALLOW data_center
    
    # Layer 5: Integrity
    AUDIT DEFINE
        Path "logs/audit.log"
        Signed true
    END AUDIT
END SECURITY

HTTP 0.0.0.0:443
    SSL cert.pem key.pem
    SECURITY production_shield
    
    # Sensitive routes with Bot Protection
    GET @BOT[js_challenge=true] "/login" HANDLER login.js
    
    # API with CSRF and Audit
    POST @WAF @CSRF @AUDIT "/api/v1/vault" HANDLER vault.js
END HTTP
```
OST @CONTENTTYPE["application/json"] "/api/v1/update" HANDLER update.js
END HTTP
```
