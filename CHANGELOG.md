# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.0.2] - 2026-04-24

### Added
- **Unified JavaScript Engine (Processor)**: Standardized all script executions (HTTP, DTP, Auth, Workers) to use the `processor` package. This ensures a consistent environment with access to all internal modules and proper base directory context.
- **JS Auth Debugging**: Added `console.log` and `print` support to the authentication script VM, enabling server-side logging for complex auth logic.
- **DTP Scripting Support**: Enabled JavaScript handlers for all DTP message types (`DATA`, `EVENT`, `CMD`, `PING`, `PONG`, `ACK`, `NACK`, `ERR`). Handlers have access to `device`, `packet`, and `payload`.
- **Global Authentication Managers**: Introduced `AUTH [name] DEFINE` blocks for centralized security management. Managers support multiple identity sources, database-backed session tracking, and JWT signing.
- **Unified Password Hashing**: Implemented a comprehensive hashing system in `modules/auth`. Supports `{BCRYPT}`, `{SHA512}`, `{SHA256}`, `{SHA1}`, and `{MD5}`.
- **Automatic Encoding Detection**: The hashing engine automatically identifies and decodes **Base64**, **Hex**, and **Base32** (RFC4648) encodings for checksum-based hashes.
- **Bcrypt Auto-Detection**: Passwords starting with `$2` (standard bcrypt format) are automatically treated as Bcrypt hashes.
- **Enhanced Auth DSL**: Added support for `USERS [FORMAT]`, `USERS CSV`, and `AUTH CSV` within global managers, allowing flexible identity source declarations.
- **OAuth2 Identity Provider**: Finalized the `SERVER DEFINE` block, enabling Beba to act as a full OIDC-compliant Identity Provider with stateless JWT tokens and JTI revocation.
- **MQTT TCP Multiplexing**: Enabled robust MQTT protocol support within shared `TCP` binder groups. MQTT can now share a single port with other protocols (like HTTP or DTP) using reactive peeking.
- **Dynamic MQTT Listener Injection**: Implemented "ghost" listener support for Mochi-MQTT, allowing the Binder to establish connections on behalf of the MQTT server without requiring a dedicated listening port.
- **Two-Phase Reactive Peeking**: Optimized the TCP peeking orchestrator to use a 1ms timeout for immediate matching, falling back to a full 2-second wait only when multiplexing is active. This ensures zero-latency for standard single-protocol ports.
- **Binder Parser Registry**: Registered `MQTT`, `DATABASE`, and `MAIL` as recognized protocol keywords in the binder parser, allowing nested configurations within `TCP` and `UDP` blocks.
- **Special Router Files Documentation**: Added exhaustive documentation for the FSRouter's special files system, including `_start.js`, `_close.js`, `_middleware.js`, `_layout`, method-specific fallbacks (`_GET.js`), and cron scripts (`*.cron.js`).
- **Connection Peeking Optimization**: Eliminated the 512-byte `Peek` with a 2-second timeout for non-multiplexed protocols (such as HTTP, MQTT) when they are the only protocol bound to a port. This drastically reduces connection latency for standard setups by using a direct accept loop instead of the generic peeking orchestrator.
- **FsRouter Strict JS Routing**: Re-engineered `.js` file routing to cleanly separate backend routes from frontend static assets. Only files prefixed with a HTTP method/route (`_GET.js`, `_route.js`) or containing dynamic parameters (`[id].js`) are executed on the server. All other `.js` files (like `app.js` or `utils.js`) are served as standard static files.
- **FsRouter Priority Hierarchy**: Implemented a robust scoring system (`Static > Exact > Dynamic > Fallback`) ensuring predictable route resolution. Depth-based scoring ensures that nested routes correctly override root-level fallbacks.
- **Recursive Error Resolution**: Error handlers (`_404.js`, `_error.html`, etc.) are now resolved recursively by traversing upwards from the requested path. Added support for method-specific error handlers (e.g., `_404.POST.js`) and unified the `cfg.NotFound` hook for Go-based customization.
- **FsRouter Universal Export & 405 Handling**: `module.exports` in JS routes can now be defined directly as a function (acts as the `ANY` method). When exporting an object, method resolution is fully case-insensitive. If a request's HTTP method is not exported (and no `ANY` fallback is present) but the path exists, the router now returns a strict `405 Method Not Allowed` with a descriptive message.
- **FsRouter Directory Index Fallback**: Requests to a physical directory now automatically fallback to searching for an index file (e.g., `index.html`). This fallback is permissive for templates: a `POST` request to a directory will serve the `index.html` template even if it's registered as a `GET` route, facilitating simple hybrid workflows.
- **Enhanced Error Handling**: Improved the global HTTP error handler to return specific error messages (e.g., "Method not allowed", "Not found") instead of a generic "Internal Server Error" for common routing issues.
- **FsRouter Hot-Reload**: Real-time route reloading via `fsnotify` watcher. New files automatically register routes, deleted files remove them, and renamed files trigger a full rescan — all with a 150ms debounce to avoid thrashing.
- **Intelligent File Cache**: TTL-based lazy-loading in-memory cache (`fileCache`) for `.js` and template files. Files are loaded on first request, served from memory on subsequent requests, and automatically evicted after inactivity. Eliminates redundant disk I/O in the request path.
- **Cache TTL Control**: New `--cache-ttl` CLI flag (default: `5m`) controls how long cached files remain in memory. Set to `0` or negative for permanent caching (no cleanup goroutine).
- **Per-Router Cache TTL**: New `cacheTtl` argument for the `ROUTER` directive in `.bind` files (e.g., `ROUTER / ./pages @[cacheTtl="10m"]`), allowing per-vhost or per-route cache tuning.
- **Thread-Safe Route State**: `routerState` with `sync.RWMutex` ensures concurrent-safe access to the routing table. The watcher writes under exclusive lock; request handlers read under shared lock via `snapshot()`.
- **Production Mode Optimization**: When `--hot-reload=false`, the file cache is permanent (no TTL expiration, no cleanup goroutine) and no `fsnotify` watcher is started, minimizing resource usage.
- **ProcessFile Cache Integration**: `processor.ProcessFile` transparently reads from the FsRouter cache (via `c.Locals("_fsrouter_cache")` interface) when available, with fallback to `os.ReadFile` for standalone usage.
- **Unified Authentication System**: Centralized all authentication logic into a new `modules/auth` module.
- **Global AUTH DSL**: New top-level `AUTH [name] DEFINE` blocks supporting multiple sources: `USERS`, `USER`, `AUTH CSV`, and scripted `AUTH BEGIN`.
- **OAuth2 Client & Server**: Built-in support for OAuth2 social logins (`STRATEGY` block). Beba can now also act as a full OAuth2 Provider (`SERVER DEFINE` block) with standard endpoints (`/oauth2/authorize`, `/oauth2/token`, `/oauth2/userinfo`).
- **JWT & Session Management**: Built-in stateless JWT access token generation with database-backed JTI tracking for explicit token revocation.
- **HTTP AUTH Directive**: Mount authentication managers anywhere in your URL space using `AUTH [manager] [path]` within an `HTTP` block.
- **Unified JS API**: Expose the auth manager directly to JavaScript via `require('auth')`, providing programmatic `authenticate`, `generateToken`, `validateToken`, and `revokeToken` capabilities.
- **Unified APIs**: Standardized `/auth/login`, `/auth/me`, and `/auth/callback/:strategy` endpoints across all protocols.
- **Unified Startup Lifecycle**: Standardized the initialization process for single-website, `beba website`, and vhost modes using the `binder.Manager`.
- **Auto-generation of .vhost.bind**: Automatically creates and loads a default `.vhost.bind` configuration when starting in a directory without an explicit config.
- **DISABLE Directive**: New DSL directive `DISABLE [TYPE] [FEATURE]` (e.g., `DISABLE DEFAULT API`, `DISABLE ADMIN UI`) to programmatically deactivate core features.
- **Feature Toggling API**: Implemented `Enabled`, `Disabled`, `AnyEnabled`, and `AnyDisabled` methods on `DirectiveConfig` with strict and loose validation logic.
- **Performance Optimization**: Introduced RWMutex-backed caching for `DISABLE` lookups, significantly reducing string processing overhead during request handling.
- **Standardized CRUD API**: Standardized API paths (`/api/_schema`, `GET /api/:schema?first=true|last=true`) and relocated Admin UI to global `/_admin`.
- **Default Feature Injection**: Improved `HTTPDirective` to automatically mount default CRUD and Realtime endpoints while honoring `DISABLE` overrides.

### Changed
- Decoupled authentication from `modules/crud` and `modules/binder`.
- Refactored legacy authentication types (`AuthConfig`, `AuthResult`) to use the new plugin-based `Strategy` system.
- Refactored `main.go` to remove redundant manual server initialization, relying on the Binder orchestrator.
- Moved default CRUD and Realtime initialization from `main.go` to `modules/binder/http_protocol.go`.

### Fixed
- **DTP Authentication Handshake**: Resolved a critical authentication failure where `AuthManagers` were lost during the vhost process transition.
- **DTP Device Cache Key**: Fixed a type mismatch in the DTP session cache, ensuring device IDs are correctly tracked as `uuid.UUID`.
- **Config Merge Logic**: Fixed a bug in `main.go` where authentication managers were not properly merged into the active configuration during startup/reload.
- **Auth Syntax Unification**: Resolved discrepancies between `USERS CSV` and `AUTH CSV` in the binder parser, ensuring both keywords correctly initialize the CSV strategy.
- **Hashing Priority**: Optimized the order of encoding detection in `CheckPassword` (Base32 > Base64 > Hex) to prevent false positives for short checksums.
- **MQTT Protocol Matching**: Stiffened the MQTT matching logic to strictly validate CONNECT packet headers (inspecting the first 16 bytes), preventing misidentification of binary traffic as MQTT.
- **MQTT SSE Publishing Hooks**: Fixed a race condition/deadlock in the SSE Hub's MQTT publishing hook registration during server restarts.
- **FSRouter Fallback Logic**: Fixed a regression where method-specific routes in `module.exports` were not correctly prioritized over generic directory fallbacks.
- **FsRouter Path Resolution**: Fixed a bug where the `ROUTER` directive with a single argument (e.g., `ROUTER .`) was incorrectly parsing the directory as the URL path, resulting in 404 errors. `ROUTER .` now correctly mounts the current directory to the root URL `/`.
- **Case-Insensitive Routing**: Updated `FsRouter` route matching to be fully case-insensitive for static files, exact routes, and dynamic parameters. `strings.EqualFold` is now used for URL matching, ensuring that paths like `/image/logo.png` correctly resolve to `./Images/logo.png` regardless of capitalization, matching Fiber's default case-insensitive behavior.
- **FsRouter Test Suite**: Fixed 6 tests (`Export_WithSettings`, `404_CustomGoHandler`, `ErrorHandler_JS`, `ErrorHandler_Template`, `Settings_Template`, `Settings_JSHandler`) that panicked due to `app.Use(FsRouter(...))` unpacking the `(fiber.Handler, error)` tuple into Fiber's variadic `...any`, passing `nil` as a second handler. All tests now properly destructure the return value.
- Fixed handler signature mismatch for `sse.Handler` in the unified startup.
- Fixed missing port error when starting HTTP protocols with explicit `PORT` directives.
- Fixed syntax errors and stray braces in `main.go` and `http_protocol.go` during refactoring.

## [0.0.1] - 2026-04-20

### Added
- Initial release of **beba**.
- Multi-protocol support: HTTP, HTTPS, TCP, UDP.
- Automated HTTPS certificate fallback: Let's Encrypt (ACME) with self-signed auto-generation.
- New configuration fields: `Domain` and `Email` for global ACME support.
- Improved console output with detailed HTTPS status (Cert/ACME/Self-signed) and URL list.
- Refined ACME manager to handle optional email addresses.
- Automatic initialization of default tables for `users`, `roles`, and `permissions` in the default database (`.data/beba.db`).
- Improved relationship support in the internal CRUD engine, including `many2many` join table creation.
- Real-time engine with SSE, WebSocket, and MQTT integration.
- Advanced VHost management with `.vhost.bind` configurations.
- Embedded JavaScript engine for dynamic routing and event handling.
- Integrated Coraza WAF for security.
- ClickHouse, Postgres, MySQL, and SQLite database connectors.
- Multi-platform release script (`scripts/release.sh`) with GitHub Release automation.
- Windows Named Pipe support and cross-platform compatibility.
- **Default CRUD API and Admin UI**: Automatically mounted at `/api` and `/api/_admin` on startup for the default database, enabling immediate user and role management.
- **Improved CRUD route matching**: Support for base paths without trailing slashes (e.g., `/api/namespaces`).

### Fixed
- Windows compilation errors related to Unix-specific socket syscalls (FD passing).
- Abstracted File Descriptor passing logic for consistent behavior across Linux, Mac, and Windows.
- Fixed release script to upload only release assets.
- Fixed default values for `cert` and `key` to be empty.
