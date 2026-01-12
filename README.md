# HTTP Server (Go Version)

A powerful, extensible HTTP server written in Go using Fiber v3, featuring a full-featured database module and server-side script execution.

## Features

- **Blazing Fast**: Powered by Fiber v3 and Go.
- **SSR & JS execution**: Run `<script server>` inside your HTML files.
- **Full Database Module**: Mongoose-like API for SQLite, PostgreSQL, MySQL, and SQL Server.
- **Advanced Sessions**: Persistent SurrealDB sessions or stateless, cookie-based **JWTSession**.
- **Flexible Storage**: Atomic operations with `undefine` and `undefined` support for all storage types.
- **HTMX & SSE Support**: Built-in support for Server-Sent Events and HTMX partial rendering.
- **Offline Testing**: Verify your templates without starting the server using the enhanced `test` command.
- **Rich CLI**: Comprehensive options for static serving, proxying, and more.

## Installation

```bash
go build -o http-server .
```

## Database Module Usage

The database module follows a Mongoose-like pattern:

```javascript
/* Inside an HTML file or <script server src="..."> */
const db = require("db").connect("sqlite:///data.db");

const UserSchema = db.Schema({
    username: { type: "string", unique: true },
    age: "number"
});

// Middleware hooks
UserSchema.pre("save", function() {
    console.log("Saving user: " + this.username);
});

const User = db.Model("User", UserSchema);

// Querying
const adults = User.find({ age: { $gt: 18 } }).sort("-age").limit(10).exec();
```

## Verification

You can verify all database examples using the built-in testing tool:

```bash
# Verify CRUD operations
go run . test examples/db_crud.html --find "li" --match "/Laptop/"

# Verify Queries and Filters
go run . test examples/db_queries.html --find "p" --match "/A1/"

# Verify Hooks and Virtuals
go run . test examples/db_hooks_virtuals.html --find "b" --match "/CLARK/"

# Verify full Mongoose-style API
go run . test examples/mongoose_db.html --find "pre" --match "/Johnny Storm/"
```

## CLI Reference

See [doc/CLI.md](doc/CLI.md) for a full list of commands and options.
