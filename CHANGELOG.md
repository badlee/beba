# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.0.5] - 2026-04-22

### Added
- **Connection Peeking Optimization**: Eliminated the 512-byte `Peek` with a 2-second timeout for non-multiplexed protocols (such as HTTP, MQTT) when they are the only protocol bound to a port. This drastically reduces connection latency for standard setups by using a direct accept loop instead of the generic peeking orchestrator.

### Fixed
- **FsRouter Path Resolution**: Fixed a bug where the `ROUTER` directive with a single argument (e.g., `ROUTER .`) was incorrectly parsing the directory as the URL path, resulting in 404 errors. `ROUTER .` now correctly mounts the current directory to the root URL `/`.

## [0.0.4] - 2026-04-22

### Added
- **FsRouter Hot-Reload**: Real-time route reloading via `fsnotify` watcher. New files automatically register routes, deleted files remove them, and renamed files trigger a full rescan — all with a 150ms debounce to avoid thrashing.
- **Intelligent File Cache**: TTL-based lazy-loading in-memory cache (`fileCache`) for `.js` and template files. Files are loaded on first request, served from memory on subsequent requests, and automatically evicted after inactivity. Eliminates redundant disk I/O in the request path.
- **Cache TTL Control**: New `--cache-ttl` CLI flag (default: `5m`) controls how long cached files remain in memory. Set to `0` or negative for permanent caching (no cleanup goroutine).
- **Per-Router Cache TTL**: New `cacheTtl` argument for the `ROUTER` directive in `.bind` files (e.g., `ROUTER / ./pages @[cacheTtl="10m"]`), allowing per-vhost or per-route cache tuning.
- **Thread-Safe Route State**: `routerState` with `sync.RWMutex` ensures concurrent-safe access to the routing table. The watcher writes under exclusive lock; request handlers read under shared lock via `snapshot()`.
- **Production Mode Optimization**: When `--hot-reload=false`, the file cache is permanent (no TTL expiration, no cleanup goroutine) and no `fsnotify` watcher is started, minimizing resource usage.
- **ProcessFile Cache Integration**: `processor.ProcessFile` transparently reads from the FsRouter cache (via `c.Locals("_fsrouter_cache")` interface) when available, with fallback to `os.ReadFile` for standalone usage.

### Fixed
- **FsRouter Test Suite**: Fixed 6 tests (`Export_WithSettings`, `404_CustomGoHandler`, `ErrorHandler_JS`, `ErrorHandler_Template`, `Settings_Template`, `Settings_JSHandler`) that panicked due to `app.Use(FsRouter(...))` unpacking the `(fiber.Handler, error)` tuple into Fiber's variadic `...any`, passing `nil` as a second handler. All tests now properly destructure the return value.

## [0.0.3] - 2026-04-22

### Added
- **Unified Authentication System**: Centralized all authentication logic into a new `modules/auth` module.
- **Global AUTH DSL**: New top-level `AUTH [name] DEFINE` blocks supporting multiple sources: `USERS`, `USER`, `AUTH CSV`, and scripted `AUTH BEGIN`.
- **OAuth2 Client & Server**: Built-in support for OAuth2 social logins (`STRATEGY` block). Beba can now also act as a full OAuth2 Provider (`SERVER DEFINE` block) with standard endpoints (`/oauth2/authorize`, `/oauth2/token`, `/oauth2/userinfo`).
- **JWT & Session Management**: Built-in stateless JWT access token generation with database-backed JTI tracking for explicit token revocation.
- **HTTP AUTH Directive**: Mount authentication managers anywhere in your URL space using `AUTH [manager] [path]` within an `HTTP` block.
- **Unified JS API**: Expose the auth manager directly to JavaScript via `require('auth')`, providing programmatic `authenticate`, `generateToken`, `validateToken`, and `revokeToken` capabilities.
- **Unified APIs**: Standardized `/auth/login`, `/auth/me`, and `/auth/callback/:strategy` endpoints across all protocols.

### Changed
- Decoupled authentication from `modules/crud` and `modules/binder`.
- Refactored legacy authentication types (`AuthConfig`, `AuthResult`) to use the new plugin-based `Strategy` system.

## [0.0.2] - 2026-04-21

### Added
- **Unified Startup Lifecycle**: Standardized the initialization process for single-website, `beba website`, and vhost modes using the `binder.Manager`.
- **Auto-generation of .vhost.bind**: Automatically creates and loads a default `.vhost.bind` configuration when starting in a directory without an explicit config.
- **DISABLE Directive**: New DSL directive `DISABLE [TYPE] [FEATURE]` (e.g., `DISABLE DEFAULT API`, `DISABLE ADMIN UI`) to programmatically deactivate core features.
- **Feature Toggling API**: Implemented `Enabled`, `Disabled`, `AnyEnabled`, and `AnyDisabled` methods on `DirectiveConfig` with strict and loose validation logic.
- **Performance Optimization**: Introduced RWMutex-backed caching for `DISABLE` lookups, significantly reducing string processing overhead during request handling.
- **Standardized CRUD API**: Standardized API paths (`/api/_schema`, `GET /api/:schema?first=true|last=true`) and relocated Admin UI to global `/_admin`.
- **Default Feature Injection**: Improved `HTTPDirective` to automatically mount default CRUD and Realtime endpoints while honoring `DISABLE` overrides.

### Changed
- Refactored `main.go` to remove redundant manual server initialization, relying on the Binder orchestrator.
- Moved default CRUD and Realtime initialization from `main.go` to `modules/binder/http_protocol.go`.

### Fixed
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
