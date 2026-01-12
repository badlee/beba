# Session and Cache Module

The `session` module provides persistent and volatile storage capabilities for server-side JavaScript scripts. It is powered by an embedded [SurrealDB](https://surrealdb.com) instance.

## Storage Types

The module provides three main entry points for data storage:

| Entry Point | Persistence | Scope | Internal ID | Backend |
|-------------|-------------|-------|-------------|---------|
| `session(id)` | Persistent | Session-specific | Provided `id` | SurrealKV (File) |
| `shared` | Persistent | Global | `@` | SurrealKV (File) |
| `cache` | Volatile | Global (per restart) | `#` | SurrealDB (Memory) |

### Persistence & TTL (Time To Live)

- **Isolation**: Each storage object is strictly isolated using its own record ID (e.g., `session:my_session_id`). This ensures that keys like `visits` are not shared between different users.
- **TTL**: Data has a default **3-hour sliding-window TTL**. Every access (read or write) resets the timer.
- **Cleanup**: A background worker runs every 5 minutes to prune expired records from the database.

### Usage in `<script server>`

```javascript
const {Session, cache, shared, config} = require("session");
const userSession = new Session("user-123");
const globalCache = cache;
const fastExchange = shared;

// Customize defaults
config({
    jwtSecret: "my-very-secret-key",
    jwtCookieNames: ["custom-jwt-token"]
});
```

---

## Global Configuration

You can redefine default values using `config(options)`:

- `jwtSecret`: (String) Default secret for signing/verifying JWT.
- `jwtSigningMethod`: (String) Default algorithm (`"HS256"`, `"HS384"`, `"HS512"`).
- `jwtCookieNames`: (Array) List of cookies to search for tokens.
- `jwtQueryNames`: (Array) List of query parameters to search for tokens.
- `sessionCookieNames`: (Array) List of cookies to search for `sid`.
- `sessionQueryNames`: (Array) List of query parameters to search for `sid`.
- `sessionTTL`: (String) Default TTL for database sessions (e.g., `"3h"`, `"30m"`).

## Data Operations

Each storage object (Session, Cache, or Exchange) supports operations grouped by data types.

### 1. Numeric Operations (`num`)

Perform atomic arithmetic operations on numeric fields.

- `get(key)`: Retrieves the numeric value of `key`.
- `add(key, val)` / `incr(key, val)`: Adds `val` to `key`. Returns updated value.
- `sub(key, val)` / `decr(key, val)`: Subtracts `val` from `key`. Returns updated value.
- `mul(key, val)`: Multiplies `key` by `val`. Returns updated value.
- `div(key, val)`: Divides `key` by `val`. Returns updated value.
- `mod(key, val)`: Remainder of `key / val`. Returns updated value.
- `divInt(key, val)`: Integer division (floor). Returns updated value.
- `define(key, val)`: Sets `key` to `val` ONLY if `key` is not currently defined (ignored otherwise).
- `defined(key)`: Returns `true` if `key` is defined.
- `undefined(key)`: Returns `true` if `key` is NOT defined.
- `undefine(key)`: Unsets the key completely.

**Example:**
```javascript
cache().num.define("counter", 100);
cache().num.undefine("counter");
const isUndef = cache().num.undefined("counter"); // true
```

### 2. List Operations (`list`)

Manage arrays and perform aggregates.

- `get(key)`: Retrieves the list at `key`.
- `push(key, val)`: Appends `val` to the end.
- `pop(key)`: Removes the last element.
- `shift(key)`: Removes the first element.
- `unshift(key, val)`: Prepends `val` to the start.
- `min(key)`, `max(key)`: Returns min/max value in list.
- `count(key)`: Returns number of elements.
- `sum(key)`, `avg(key)`: Returns sum or average of elements.
- `define(key, val)`: Sets `key` to `val` (should be a list) only if `key` is not defined.
- `defined(key)`: Returns `true` if `key` is defined.
- `undefined(key)`: Returns `true` if `key` is NOT defined.
- `undefine(key)`: Unsets the key.

### 3. String Operations (`str`)

- `get(key)`: Retrieves the string at `key`.
- `concat(key, val)`: Appends `val` to the string.
- `sub(key, start, end)`: Returns a substring.
- `split(key, separator)`: Splits string into a list.
- `at(key, index)`: Returns character at position.
- `define(key, val)`: Sets `key` to `val` (should be a string) only if `key` is not defined.
- `defined(key)`: Returns `true` if `key` is defined.
- `undefined(key)`: Returns `true` if `key` is NOT defined.
- `undefine(key)`: Unsets the key.

### 4. Hash / Object Operations (`hash`)

- `set(key, val)`: Stores any JS value (primitives, objects, arrays).
- `get(key)`: Retrieves the value.
- `has(key)` / `defined(key)`: Returns `true` if key exists.
- `undefined(key)`: Returns `true` if key doesn't exist.
- `undefine(key)`: Unsets the key.
- `keys(key)`: Returns list of keys if the value is an object.
- `all()`: Returns the complete session record.
- `define(key, val)`: Sets `key` to `val` only if `key` is not defined.

**Example:**
```javascript
cache().hash.set("config", { theme: "dark", lang: "en" });
cache().hash.undefine("config");
```

---

## JWTSession (Cookie-based, No DB)

`JWTSession` provides fully portable sessions where all data is stored inside a signed JWT token in a cookie. This requires no database on the server, ensuring infinite scalability and statelessness.

### Constructor
`new JWTSession(cookiesOrToken?, cookieName?)`
- `cookies`: (Object) The global cookie object.
- `token`: (String) A raw JWT token string.
- (None): If no arguments are provided, `JWTSession` attempts to discover the token in:
    1. `Authorization` header (`Bearer <token>`).
    2. Cookies (configured list, default: `jwtToken`, `jwt`, `token`).
    3. Query parameters (configured list, default: `jwttoken`, `jwt-token`, etc.).
- `cookieName`: (Optional) Custom cookie name for saving.

> [!NOTE]
> When initialized with a token string, `save()` and `destroy()` methods are **no-ops**. If the provided token is invalid, the script execution will be interrupted immediately.

### Configuration
- `setSigningMethod(method)`: Set the algorithm for `getToken()` and `save()`. Supports `"HS256"` (default), `"HS384"`, and `"HS512"`.

### Private Claims (Data)
- `get(key)`: Retrieve a private claim.
- `set(key, value)`: Set a private claim.
- `remove(key)`: Remove a private claim.
- `clear()`: Clear all private claims.

### Standard Claims
Mapped directly to JWT registered claims:
- `setIssuer(val)` / `issuer()` (`iss`)
- `setSubject(val)` / `subject()` (`sub`)
- `setAudience(val)` / `audience()` (`aud`)
- `setExpire(val)` / `expire()` (`exp` - Unix timestamp)
- `setNotBefore(val)` / `notBefore()` (`nbf`)
- `setIssuedAt(val)` / `issuedAt()` (`iat`)
- `jti()`: Returns the unique session ID.

### Actions
- `save()`: Signs the token and updates the cookie.
- `destroy()`: Removes the cookie.
- `getToken()`: Returns the raw signed JWT string.

**Example:**
```javascript
const session = new JWTSession(cookies);
session.set("user_role", "admin");
session.setExpire(Math.floor(Date.now() / 1000) + 3600); // 1 hour
session.save();
```

---

## Persistence Details

- **File Storage**: Persistent SurrealDB data is stored in the `./data/sessions.db` directory.
- **Auto-Initialization**: The module initializes SurrealDB automatically upon the first use.
- **Namespacing**: SurrealDB data is under the `http-server` namespace.

---

## Cookie Integration

The preprocessor exposes a `cookies` object with the following methods:

- `cookies.get(name)`: Retrieves a cookie value.
- `cookies.set(name, value)`: Sets a cookie value.
- `cookies.remove(name)`: Clears a cookie.
- `cookies.has(name)`: Checks if a cookie exists.

### Automatic Session Persistence (Cookie-based)

Using `new Session(cookiesOrId?, cookieName?)` initializes a database session.
- `id`: (String) A manual session ID.
- (None): If no arguments are provided, `Session` attempts to discover the ID in:
    1. Cookies (configured list, default: `sid`).
    2. Query parameters (configured list, default: `sid`).
- `cookies`: (Object) The global cookie object (for specific cookie lookup).
- `cookieName`: (Optional) Custom cookie name.

```javascript
<script server>
    const {Session} = require("session");
    // Initialize session using the global cookies object and a custom cookie name
    const session = new Session(cookies, "my_session_id");

    // Now you can use session as a persistent store for the current visitor
    if (!session.hash.has("visits")) {
        session.hash.set("visits", 1);
    } else {
        const visits = session.hash.get("visits");
        session.hash.set("visits", visits + 1);
    }

    // Export to template
    var visitCount = session.hash.get("visits");
</script>

<h1>Welcome, you have visited this page <?= visitCount ?> times.</h1>
```
