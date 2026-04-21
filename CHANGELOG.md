# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.0.3] - 2026-04-22

### Added
- **Unified Authentication System**: Centralized all authentication logic into a new `modules/auth` module.
- **Global AUTH DSL**: New top-level `AUTH [name] DEFINE` blocks supporting multiple sources: `USERS`, `USER`, `AUTH CSV`, and scripted `AUTH BEGIN`.
- **OAuth2 Client & Server**: Built-in support for OAuth2 social logins (`STRATEGY` block) and a skeleton for Beba as an OAuth2 Provider (`SERVER` block).
- **HTTP AUTH Directive**: Mount authentication managers anywhere in your URL space using `AUTH [manager] [path]` within an `HTTP` block.
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
