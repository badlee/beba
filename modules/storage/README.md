# Storage Module

The Storage module provides persistent and volatile data management for the HTTP server, leveraging **GORM** and **SQLite**. It exposes a powerful and easy-to-use API to the Javascript execution environment.

## Overview

The module manages data in a key-value fashion, with support for typed operations (Numbers, Strings, Lists, Hashes). Data can be associated with specific sessions or shared globally.

### Files
- [storage.go](file:///Users/hobb/Dev/tools/http-server/modules/storage/storage.go): Core implementation, GORM integration, and JS API registration.
- [storage_test.go](file:///Users/hobb/Dev/tools/http-server/modules/storage/storage_test.go): Unit tests for JWT sessions and storage operations.

## Features

- **Multi-backend**: Automatically handles persistent storage (disk) and volatile storage (memory-like behavior in SQLite).
- **Stateless JWT Sessions**: Full support for JWT-based sessions with easy signing and validation.
- **Auto-discovery**: Automatically finds session IDs or JWT tokens in headers, cookies, and query parameters.
- **Typed Operations**: specialized methods for common data types to ensure thread-safe-ish or consistent updates (e.g., `incr`, `push`, `concat`).
- **TTL Management**: Automatic sliding window expiration for session data.

## Javascript API Reference

### Global Functions

#### `config(options)`
Configures the storage module settings.
```javascript
storage.config({
    jwtSecret: "your-secret",
    jwtSigningMethod: "HS256", // HS256, HS384, HS512
    jwtCookieNames: ["jwtToken", "token"],
    jwtQueryNames: ["token"],
    sessionCookieNames: ["sid"],
    sessionQueryNames: ["sid"]
});
```

#### `session([id])`
Returns a storage object for the specified session ID. If no ID is provided, it attempts to discover it from cookies or query parameters.
```javascript
const mySess = storage.session();
mySess.num.incr("visits", 1);
```

#### `shared`
A shared storage object for global data (uses session ID `@`).

#### `cache`
A volatile/temporary storage object (uses session ID `#`).

---

### Storage Objects (Typed Containers)

All storage objects (from `session`, `shared`, or `cache`) provide the following containers:

#### `num` (Numbers)
- `get(key)`: Returns the numeric value.
- `incr(key, val)` / `add(key, val)`: Increments the value.
- `decr(key, val)` / `sub(key, val)`: Decrements the value.
- `mul(key, val)` / `div(key, val)`: Multiplication/Division.
- `mod(key, val)` / `divInt(key, val)`: Modulo/Integer Division.
- `define(key, val)`: Sets the value only if it doesn't exist.
- `undefine(key)`: Completely removes the key.
- `defined(key)`: Returns `true` if defined.
- `undefined(key)`: Returns `true` if not defined.

#### `str` (Strings)
- `get(key)`: Returns the string value.
- `concat(key, val)`: Appends `val` to the current string.
- `define(key, val)`, `undefine(key)`, `defined(key)`, `undefined(key)`.

#### `list` (Arrays)
- `get(key)`: Returns the full array.
- `push(key, val)`: Appends an item.
- `pop(key)`: Removes the last item.
- `define(key, val)`, `undefine(key)`, `defined(key)`, `undefined(key)`.

#### `hash` (Objects)
- `set(key, val)`: Sets the value (JSON encoded).
- `get(key)`: Returns the decoded object.
- `has(key)`: Returns `true` if key exists.
- `keys(key)`: Returns all keys of the stored object.
- `define(key, val)`, `undefine(key)`, `defined(key)`, `undefined(key)`.

---

### `JWTSession` Class

For stateless session management.

#### Constructor
```javascript
const sess = new JWTSession(cookies_or_token);
```
- If passed a strings, treats it as a raw token and validates it.
- If passed the `cookies` object, looks for the token in cookies.
- If no arguments, auto-discovers from `Authorization` header, cookies, or query.

#### Methods
- `get(key)` / `set(key, val)`: Manage private claims in the `data` segment.
- `remove(key)`: Removes a key from the data segment.
- `clear()`: Clears all data.
- `setExpire(unixTimestamp)` / `expire()`: Manage `exp` claim.
- `setAudience(str)` / `audience()`: Manage `aud` claim.
- `setIssuer(str)` / `issuer()`: Manage `iss` claim.
- `setSubject(str)` / `subject()`: Manage `sub` claim.
- `jti()`: Returns the unique token ID (UUIDv7).
- `getToken()`: Returns the signed JWT string.
- `save()`: Saves the token to a cookie (only if initialized with cookies).
- `destroy()`: Removes the cookie.

## Testing

Run the module tests:

```bash
go test ./modules/storage -v
```
