# HTTP / HTTPS Protocols

The server provides a high-performance HTTP engine (powered by Fiber/Fasthttp) built for massive concurrency, real-time communication, and automated content generation.

---

## 🏗️ Protocol Directives

### `HTTP [address]`
Defines an HTTP listener.
```hcl
HTTP 0.0.0.0:80
    GET "/" "Hello world"
END HTTP
```

### `HTTPS [address]`
Defines a secure HTTPS listener. Requires valid SSL configuration.
```hcl
HTTPS 0.0.0.0:443
    SSL AUTO example.com admin@example.com
    GET "/" "Secured Hello"
END HTTPS
```

### `AUTH [manager_name] [path]`
Mounts a globally defined authentication manager onto a specific path prefix for this HTTP protocol block. If no path is provided, it defaults to `/auth`.
```hcl
HTTP 0.0.0.0:80
    // Assumes 'central' was defined globally via AUTH central DEFINE
    AUTH central /api/auth
END HTTP
```

### 🛑 Feature Deactivation
You can selectively disable default features using the `DISABLE` directive.

| Type | Feature | Description |
|---|---|---|
| `DEFAULT` | `API` | Disables **all** default endpoints (CRUD + Realtime). |
| `DEFAULT` | `CRUD` | Disables only the auto-generated database REST API (`/api/*`). |
| `DEFAULT` | `DATABASE` | Alias for `CRUD`. Disables the database REST API. |
| `DEFAULT` | `REALTIME` | Disables only the Realtime/SSE/WS endpoints (`/api/realtime/*`). |
| `ADMIN`   | `UI`       | Disables the administration dashboard on `/_admin`. |

```hcl
HTTP 0.0.0.0:8080
    DISABLE DEFAULT API      // Complete lockdown of default endpoints
    
    // OR be more specific:
    DISABLE DEFAULT CRUD     // Keep Realtime, but disable CRUD
END HTTP
```

---

## 🔒 SSL / TLS Configuration

### Auto-SSL (Let's Encrypt)
Managed via the ACME protocol. Certificates are automatically requested, cached, and renewed.
```hcl
SSL AUTO [domain] [admin_email]
```

### Manual SSL
Provide local paths to your private key and certificate bundle.
```hcl
SSL [key_path] [cert_path]
```

---

## 🛠️ Middleware Reference (@...)

Middlewares can be applied globally to a protocol block or per route using the `@` prefix. They are executed in the order they are defined.

### 🛡️ Security Middlewares

| Middleware | Arguments | Description |
|---|---|---|
| `@SECURITY` | `name` (string) | Applies a named security profile defined in a `SECURITY` block. |
| `@WAF` | `rules` (list), `auditLog` (path) | Enables the Coraza L7 WAF engine for deep payload inspection. |
| `@BOT` | `js_challenge` (bool), `threshold` (int), `path` (string) | Detects automated agents and can trigger Proof-of-Work JS challenges. |
| `@CSRF` | - | Protects against Cross-Site Request Forgery (Form/Header validation). |
| `@HELMET` | `csp` (strict/compatible), `xss`, `frameOptions`, etc. | Sets standard security headers (CSP, HSTS, X-Frame). |
| `@IP` | `allow` (list), `block` (list) | L4 filtering based on IP addresses or CIDR ranges. |
| `@GEO` | `allow` (list), `block` (list) | Geofencing based on ISO-3166 country codes. |
| `@AUDIT` | `path` (string), `sign` (bool), `level` (string) | Generates cryptographically signed logs of the request. |
| `@UNSECURE` | - | Explicitly disables the default/global WAF for a specific route. |

### ⚡ Performance & Quality

| Middleware | Arguments | Description |
|---|---|---|
| `@COMPRESS` | - | Enables Gzip or Brotli compression based on client capability. |
| `@ETAG` | `weak` (bool) | Generates ETag headers for efficient browser caching. |
| `@CACHE` | `key` (string), `expiration` (duration) | Caches the response body on the server for a specific duration. |
| `@LIMITER` | `max` (int), `expiration` (duration) | Per-route rate limiting (e.g. `@LIMITER[max=5 expiration=1m]`). |
| `@TIMEOUT` | `expiration` (duration) | Cancels the request if the handler takes longer than the specified time. |

### 🔧 Utilities

| Middleware | Arguments | Description |
|---|---|---|
| `@CORS` | `origins`, `methods`, `headers`, `credentials`, `maxAge` | Configures Cross-Origin Resource Sharing. |
| `@IDEMPOTENCY` | `header` (string), `expiration` (duration), `responseHeaders` | Ensures a request is only processed once within a time window. |
| `@REQUESTID` | `header` (string) | Injects or extracts a unique request ID for tracing. |
| `@REQUESTTIME` | `header` (string) | Injects the total processing time into the response headers. |
| `@CONTENTTYPE` | `type` (string) | Enforces that the client sends a specific `Content-Type` (e.g. `application/json`). |

### 💎 Specialized Middlewares

#### `@PDF` (Automated Conversion)
The `@PDF` middleware intercepts the output (HTML or Text) and converts it into a PDF document on-the-fly.

| Argument | Type | Description |
|---|---|---|
| `name` | string | Filename for download (e.g., `invoice`). |
| `orientation` | string | `portrait` (default) or `landscape`. |
| `format` | string | Page format (e.g., `A4`, `A5`, `Letter`). |
| `font-family` | string | Primary font family (e.g., `helvetica`). |
| `font-size` | number | Primary font size in points. |
| `pdfa` | bool | Enable PDF/A compliance. |
| `title`, `author` | string | PDF Metadata fields. |

#### `@PAYMENT` (Paywall Gate)
Restricts access to a route until a payment is confirmed via the integrated payment module.

| Argument | Type | Description |
|---|---|---|
| `name` | string | Name of the configured payment provider. |
| `price` | string | Amount and currency (e.g., `10.00 USD`). |
| `desc` | string | Description shown to the user on the payment page. |
| `ttl` | duration| Delay before the payment request expires. |

#### `@ADMIN` (Root Access)
Restricts the route to users with administrative (Root) privileges.

| Argument | Type | Description |
|---|---|---|
| `redirect` | string | Path to redirect unauthorized users to. |
| `message` | string | Message shown if access is denied. |
| `basic` | bool | If true, triggers a browser Basic Auth prompt. |

---

## 🛤️ Advanced Routing

### PROXY
Délègue les requêtes à un serveur distant. Gère automatiquement les headers `X-Forwarded-For` et assure la transition transparente vers WebSocket (Upgrade).
```hcl
PROXY /api http://internal-service:9000
```

### REWRITE / REDIRECT
Modifie l'URL en interne (`REWRITE`) ou renvoie un code 3xx au client (`REDIRECT`). Supporte les expressions régulières et les conditions JavaScript.

```hcl
REWRITE "/v1/(.*)" "/v2/$1"
REDIRECT 301 "/old" "/new" c.Get("User-Agent").includes("Mobile")
```
