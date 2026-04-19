# 📚 Documentation Hub

Welcome to the **beba** official documentation. This server is a high-speed, multi-protocol engine designed for modern web applications, IoT backends, and secure infrastructure.

---

## 🚀 Core Concepts

- **[CLI Reference](CLI.md)**: Command-line arguments, environment variables, and startup flags.
- **[Binder Guide (.bind)](BINDER.md)**: Learn how to use the declarative DSL to configure your entire stack in a single file.
- **[JS Scripting API](JS_SCRIPTING.md)**: Documentation for the server-side JavaScript environment, **dynamic global database injection**, and native modules.
- **[Persistence by Default](DATABASE.md#the-default-database)**: Every project automatically starts with a ready-to-use SQLite database at `./.data/beba.db`.
- **[Templating System](TEMPLATING.md)**: Dynamic HTML generation using `<?js ?>` tags and Mustache syntax.

---

## 🔌 Networking & Protocols

- **[HTTP / HTTPS](HTTP.md)**: Documentation for the web engine, SSL/TLS (Auto-SSL), and advanced middlewares including **Automated PDF Generation**.
- **[Real-time Hub (SSE/WS/Socket.IO)](IO.md)**: Our high-performance sharded hub for handling 1M+ concurrent connections.
- **[MQTT Broker](MQTT.md)**: Integrated IoT messaging with native database persistence.
- **[DTP Protocol](DTP.md)**: Optimized protocol for high-frequency hardware data transfer.
- **[Mail (SMTP)](MAIL.md)**: Built-in mail server with automated handling logic.

---

## 🛡️ Security (Sentinel Architecture)

- **[Security & WAF](SECURITY.md)**: Comprehensive guide on the 5-layer Sentinel defense, including Network Filtering (L1), Coraza WAF (L3), Bot Defense (L4), and Signed Auditing (L5).
- **[Authentication](AUTH.md)**: Strategies for securing your routes (Basic, JWT, OAuth2, and scripted).

---

## 💾 Data & Persistence

- **[Database & CRUD](DATABASE.md)**: Relational database management (SQLite, PostgreSQL, MySQL), Schema definitions, and Zero-Code REST APIs with an integrated **Admin UI**.
- **[Storage](STORAGE.md)**: Global key-value storage and file persistence systems.

---

## 🌐 Advanced Management

- **[Virtual Hosting (VHost)](VHOST.md)**: Multi-domain management and SSL termination.
- **[Admin Interface](ADMIN.md)**: Guide to the built-in web dashboard for monitoring and management.
- **[Routing System](ROUTER.md)**: Deep dive into path matching, rewrites, and redirects.
