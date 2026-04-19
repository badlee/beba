# Database and CRUD Module

The `database` module provides a Mongoose-inspired interface for managing relational databases (SQL). It unifies raw connectivity, schema-driven modeling, and automated API generation.

---

## 1. Connection (Binder Directive)

The `DATABASE` directive establishes a connection to a database provider (SQLite, PostgreSQL, MySQL).

### Syntax
```hcl
DATABASE [provider_url] [default?]
    NAME [js_name]      // Identifier for require('db').get(js_name)
    SECRET [jwt_secret] // Key for JWT signatures (Auth)
    [Schema/Auth/Admin directives...]
END DATABASE
```

### Supported Providers
- **Default Database**: `":default:"` or `""` (Targets the auto-generated default DB at `./.data/beba.db`)
- **SQLite**: `sqlite:///path/to/db.sqlite` or `:memory:`
- **PostgreSQL**: `postgres://user:pass@localhost:5432/mydb`
- **MySQL / MariaDB**: `mysql://user:pass@localhost:3306/mydb`

### The Default Database
By default, the server automatically initializes a SQLite database located at `./.data/beba.db`. This database is immediately ready for use in your JavaScript scripts via `database.default`, even without any `DATABASE` binder directive!

If you wish to attach schemas or configure this default database from your `.bind` files, simply use the `DATABASE ":default:"` shorthand instead of a strict path:
```hcl
DATABASE ":default:"
    SCHEMA products DEFINE
        FIELD name string
    END SCHEMA
END DATABASE
```

---

## 2. Automated CRUD and Admin UI

The server can instantly generate REST APIs and a secure Admin interface from your schemas.

### Mounting the API
To expose a database instance over HTTP, use the `CRUD` directive:

```hcl
DATABASE "sqlite://data.db"
    NAME myapi
    SCHEMA products DEFINE ...
END DATABASE

HTTP :8080
    CRUD myapi /api          // Mounts API on /api and Admin on /api/_admin
END HTTP
```

### Key Features
- **Zero-Code APIs**: Automatic generation of routes:
    - `GET /api/{schema}` : List documents (with filters: `?price[$gt]=10`).
    - `POST /api/{schema}` : Create document.
    - `PUT /api/{schema}/{id}` : Update document.
    - `DELETE /api/{schema}/{id}` : Remove document.
- **Admin UI**: A modern interface for data management, user roles, and system metrics.
- **Real-time SSE**: Every mutation publishes an event to the global Hub:
    - Channel: `crud.{schema}.{operation}`
    - Payload: `{ "id": "...", "data": {...} }`

---

## 3. Defining Schemas (DSL)

Inside a `DATABASE` block, use `SCHEMA` to define your data models.

```hcl
SCHEMA products DEFINE [icon=box color=#3B82F6 softDelete=true]
    FIELD name     string [required]
    FIELD price    number [default=0]
    FIELD category string [index]
    
    # Virtual field (computed in JS)
    FIELD fullName BEGIN
        return this.name + " (" + this.category + ")";
    END FIELD

    # Relationship (Foreign Key)
    FIELD category_id Category.id [delete=CASCADE]

    # Hooks
    HOOK onSave BEGIN
        console.log("Saving product: " + this.name);
    END HOOK
END SCHEMA
```

### Field Options
- **Types**: `string`, `number`, `boolean`, `int`, `float`, `date`, `geo`, `array`, `object`, `text`.
- **Validation**: `required`, `unique`, `index`.
- **Relationships**: Link fields using `[Schema].[field]` with `has=one|many|many2many`.

---

## 4. JavaScript API — `require('db')`

### Basic Usage
```javascript
const db = require('db'); // Uses the [default] connection
const User = db.Model('User');

// Create
const user = await User.create({ name: 'Alice', age: 30 });

// Query with chaining
const users = await User.find({ age: { $gt: 18 } })
    .sort('-age')
    .limit(10)
    .preload('Profile') 
    .exec();
```

### Query Operators (MongoDB-style)
The module supports: `$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte`, `$in`, `$nin`, `$or`, `$and`, `$exists`, `$regex`.

---

## 5. Persistence for Other Protocols

Database connections are registered globally and can be used by other protocols as backends.

### MQTT Persistence
```hcl
TCP :1883
    MQTT
        STORAGE "my_db_name" // Link MQTT broker to the DB instance
    END MQTT
END TCP
```
