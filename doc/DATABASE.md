# Database and CRUD Module

The `database` module provides a Mongoose-inspired interface for managing relational databases (SQL). It allows for schema definitions, models, and CRUD operations with a syntax familiar to Node.js developers.

## 1. Connection (Binder Directive)

The `DATABASE` directive establishes a connection to a database provider. By default, it registers the connection globally as `database`.

### Syntax
```hcl
DATABASE [provider_url] [default?]
    NAME [js_name]      // Name for require('db').get(js_name)
    [Sub-directives...]
END DATABASE
```

### Supported Providers
- **SQLite**: `sqlite:///path/to/db.sqlite` or `:memory:`
- **PostgreSQL**: `postgres://user:pass@localhost:5432/mydb`
- **MySQL / MariaDB**: `mysql://user:pass@localhost:3306/mydb`
- **SQL Server**: `sqlserver://user:pass@localhost:1433/mydb`

---

## 2. Defining Schemas (DSL)

Inside a `DATABASE` or `CRUD` block, use the `SCHEMA` directive to define your models.

```hcl
DATABASE "sqlite://data.db" [default]
    NAME myapi

    SCHEMA products DEFINE [icon=box]
        FIELD name string [required]
        FIELD price number [default=0]
        FIELD category string [index]
        
        # Virtual field (computed)
        FIELD fullName BEGIN
            return this.name + " (" + this.category + ")";
        END FIELD

        # Hooks
        HOOK onsave BEGIN
            console.log("Saving product: " + this.name);
        END HOOK
    END SCHEMA
END DATABASE
```

### 2.1 Relationships (Foreign Keys)
You can link fields to other schemas using the `[Schema].[field]` syntax.

```hcl
DATABASE "sqlite://data.db"
    SCHEMA User
        FIELD name string
    END SCHEMA

    SCHEMA Profile
        # 'has=one' is the default. GORM will create a 'user_id' column.
        FIELD user_id User.id [delete=CASCADE update=CASCADE]
        FIELD bio text
    END SCHEMA

    SCHEMA Order
        # 'has=many' creates a virtual association to the target schema.
        FIELD customer_id User.id [has=many]
        FIELD amount number
    END SCHEMA

    SCHEMA User
        # 'has=many2many' handles join tables automatically.
        FIELD roles Role.id [has=many2many]
    END SCHEMA
END DATABASE
```

#### Relationship Options
- `has`: `one` (default), `many`, `many2many`.
  - Aliases: `one_to_one`, `one_to_many`, `many_to_many`.
- `delete`: `CASCADE`, `SET NULL`, `RESTRICT`, `NO ACTION`.
- `update`: `CASCADE`, `SET NULL`, `RESTRICT`, `NO ACTION`.

### Field Options
- `type`: `string`, `number`, `boolean`, `int`, `float`, `date`, `datetime`, `geo`, `array`, `object`, `text`.
- `required`: Boolean.
- `index`: Creates a database index.
- `unique`: Ensures uniqueness.
- `default`: Default value.

---

## 3. Dynamic Migration Strategy (Dual Struct)

`http-server` implements a sophisticated, "anti-panic" migration strategy designed to bypass GORM limitations with dynamic types.

### 3.1 The Problem
When creating a database schema at runtime (via `.bind` or JS), GORM often struggles with unnamed types for relationships, leading to segmentation violations or invalid SQL syntax during `AutoMigrate`.

### 3.2 The Solution: Dual Struct
The system generates two internal Go structs for every schema:
1.  **Migration Struct**: Contains only base columns (IDs, Dates, Strings, Numbers). All relationship fields are **skipped** to ensure the table structure is created safely without complex FK dependency chains.
2.  **Runtime Struct**: A full model including shadow fields (`UserRef`), virtuals, and association preloading logic, used for all subsequent CRUD operations.

### 3.3 Bulk Migration
To ensure integrity, it is recommended to define all `SCHEMA` blocks first and then perform a **Bulk Migration**. The unified `DATABASE` directive automatically handles this by registering all models and calling `AutoMigrate` once for the entire connection, resolving FK constraints in a single pass.

---

## 4. JavaScript API — `require('db')`

### Initialization
```javascript
const db = require('db'); // Uses the [default] connection
// OR
const myConn = db.get('myapi');
```

### Models and Documents
```javascript
const User = db.Model('User');

// Create
const user = await User.create({ name: 'Alice', age: 30 });

// Find
const users = await User.find({ age: { $gt: 18 } })
    .sort('-age')
    .limit(10)
    .preload('Profile') // Preload related documents
    .exec();

// Nested Preloading
const orders = await User.find({})
    .preload('Orders.Items')
    .exec();

// Update
user.age = 31;
await user.save();

// Delete
await user.remove();
```

### Query Operators
The module supports a rich set of MongoDB-style operators:

| Operator | Description |
|---|---|
| `$eq`, `$ne` | Equal / Not equal |
| `$gt`, `$gte` | Greater than (or equal) |
| `$lt`, `$lte` | Less than (or equal) |
| `$in`, `$nin` | In / Not in list |
| `$or`, `$and`, `$nor` | Logical union / intersection / negation |
| `$exists` | Check if field is NULL |
| `$regex` | Regular expression matching |
| `$geoWithin` | Geospatial search (requires `geo` type) |

---

## 4. Admin UI and CMS

If you define `SCHEMA` blocks with the `DEFINE` keyword, `http-server` automatically generates a secure Admin UI.

### Admin Customization
```hcl
DATABASE "sqlite://cms.db"
    ADMIN DEFINE
        PAGE "/stats" [title="Analytics" icon=bar-chart] BEGIN
            <div class="card">
                <h2>Total Products: <?= db.Model('products').count() ?></h2>
            </div>
        END PAGE
    END ADMIN
END DATABASE
```

Access the Admin UI at `/_admin` (if mounted via `CRUD /api` in an HTTP block).
