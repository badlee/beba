# Server-Side JS Scripting

The HTTP Server allows executing server-side JavaScript directly within your HTML files using special tags. This execution happens before the page is served to the client, primarily to prepare data for the [Templating System](TEMPLATING.md).

## Special Tags

### `<?js ... ?>`
Executes JavaScript code. Does not output anything to the page.

```javascript
<?js
    var title = "Welcome to my Server";
    var user = { name: "John" };
?>
```

### `<?= ... ?>`
Executes an expression and outputs the result directly into the HTML.

```html
<h1><?= title ?></h1>
<p>Hello, <?= user.name ?>!</p>
```

### `<script server>`
Executes a block of JS code or an external script file.

```html
<!-- Inline script -->
<script server>
    const db = require("db").connect("sqlite:///data.db");
    var items = db.Model("Item").find().exec();
</script>

<!-- External script -->
<script server src="logic.js"></script>
```

---

## Global Objects & Functions

The following objects and functions are available in the server-side JS environment:

| Object | Description |
|--------|-------------|
| `db` | access to the [Database Module](DATABASE.md) |
| `require(path)` | Load standard modules or local JS files |
| `console` | Standard console for logging to terminal/log files |
| `sse` | Access to the Server-Sent Events & WebSocket high-performance Hub |
| `include(file)` | Process and include another template file |
| `print(arg)` | Append data to the output buffer |
| `cookies` | Access and modify request cookies |
| `settings` | Access to global configuration defined via `SET` in `.bind` |

### `cookies` Methods
- `get(name)`: Returns a cookie value.
- `set(name, value)`: Sets a cookie.
- `remove(name)`: Clears a cookie.
- `has(name)`: Checks if a cookie exists.

### `sse` Methods
The `sse` object connects to the high-performance Sharded Hub (supporting 1M+ concurrent connections / WebSockets):
- `publish(event, data)`: Broadcasts a message to the `global` channel.
- `to(channel).publish(event, data)`: Broadcasts a message to a specific named channel.
- `attach(sid)`: Binds the current request context to a client session ID (useful in custom JS route handlers).
- `send(event, data)`: Sends a private message directly to the client ID attached via `attach()` or via the `sid` cookie.


### `include(file)`
The `include` function recursively processes the target file as a template, allowing for modular HTML fragments. See the [Templating Guide](TEMPLATING.md) for more details on component architecture.

```javascript
<?= include("header.html") ?>
```

---

## Modularity with `require`

You can use `require` to load local modules. The server looks for modules in:
1. The current directory.
2. `libs/`
3. `modules/`
4. `node_modules/`
5. `js_modules/`

```javascript

---

## Database Module (JS)

The `db` module provides a Mongoose-like interface for managing relational databases (SQL). It allows you to define schemas, models, and perform CRUD operations with a syntax familiar to Node.js developers.

### Connection

The module supports multiple database systems via connection URLs:

```javascript
const db = require('db');

// SQLite
const conn = db.connect('sqlite:///data.db');

// In-Memory (SQLite)
const memoryConn = db.connect(':memory:');

// PostgreSQL
const pgConn = db.connect('postgres://user:pass@localhost:5432/mydb');

// MySQL
const mysqlConn = db.connect('mysql://user:pass@localhost:3306/mydb');
```

### Schemas

A schema defines the structure of documents in a collection (table).

```javascript
const userSchema = new conn.Schema({
    name: { type: 'string', required: true },
    email: { type: 'string', unique: true },
    age: 'number',
    roles: 'array'
});

// Virtuals (Getters/Setters)
userSchema.virtual('fullName').get(function() {
    return this.firstName + ' ' + this.lastName;
});

// Middleware (Hooks)
userSchema.pre('save', function() {
    console.log('Saving ' + this.name);
});

// Instance Methods
userSchema.methods.sayHello = function() {
    return "Hello, I am " + this.name;
};

// Static Methods
userSchema.statics.findByEmail = function(email) {
    return this.findOne({ email: email });
};
```

### Models

Models are constructors compiled from schema definitions.

```javascript
const User = conn.model('User', userSchema);

// Create a document
const newUser = new User({ name: 'Alice', age: 30 });
newUser.save();

// Direct creation
User.create({ name: 'Bob', age: 25 });
```

### Queries

The module supports query chaining similar to Mongoose.

```javascript
// Find with filters
User.find({ age: { $gt: 18 } })
    .sort('-age')
    .limit(10)
    .select('name email')
    .exec()
    .then(users => {
        console.log(users);
    });

// findOne
const user = await User.findOne({ email: 'alice@example.com' });

// Using custom static methods
const userByEmail = await User.findByEmail('alice@example.com');
```

#### Execution Methods
- `.exec()`: Directly returns results (blocking for the current script).
- `await query`: Query objects are "thenables", allowing direct use of `await` or `.then()`.

### Supported Operators

#### Comparison Operators
- `$eq`: Equality (optional if direct value).
- `$gt`, `$gte`: Greater than (or equal).
- `$lt`, `$lte`: Less than (or equal).
- `$ne`: Not equal.
- `$in`, `$nin`: Included (or not) in a list.

#### Logical Operators
- `$or`: OR.
- `$and`: AND.
- `$nor`: NOR.
- `$not`: NOT.

#### Element Operators
- `$exists`: Checks if a field is present (non-NULL).
- `$type`: Checks data type (via `typeof()` in SQLite).

#### Evaluation Operators
- `$mod`: Modulo.
- `$regex`: Regular expression (driver dependent).
- `$where`: Raw SQL expression.

#### Array Operators (SQL Adapted)
- `$all`: Must contain all specified values.
- `$size`: Checks the number of elements in the list.

### Documents

Model instances represent documents synchronized in real-time with the internal engine.

```javascript
const user = await User.findOne({ name: 'Alice' });

// Immediate sync between properties and methods
user.name = 'Smith'; 
user.set('age', 31); 

// Save and Remove
await user.save();
await user.remove();
```
```
# Protocol `DATABASE` — DSL Reference

The `DATABASE` protocol unifies raw database connectivity and high-level CRUD/Auth features. It allows you to define a complete backend (namespaces, schemas, documents, users, roles, JWT + OAuth2 auth) directly in your `.bind` file.

## Principle

The `DATABASE` directive establishes a connection to a database and optionally configures a REST API and an Admin UI. 
To expose the high-level API over HTTP, you must attach the instance to an `HTTP` server using the `CRUD [name] [prefix]` directive.

> [!NOTE]
> Inside the `HTTP` block, the directive is still named `CRUD` for historical/mounting reasons, while the top-level block is now exclusively `DATABASE`.

```bind
DATABASE 'postgres://user:pass@localhost/mydb' [default]
    NAME myapi
    // schema, auth, roles...
END DATABASE

HTTP :8080
    CRUD myapi /api          // Mount the "myapi" instance on /api
END HTTP
```

---

## Directives

### NAME
```bind
NAME myapi
```
Identifier for the instance. Used for mounting in `HTTP` blocks and accessing via `database.connection("name_of_crud")` or `database.default` in JS.

### SECRET
```bind
SECRET "your-jwt-secret"
```
JWT signature key. Defaults to `AppConfig.SecretKey` if omitted.

---

### AUTH — Root Authentication
At least one `AUTH` directive is **mandatory** for high-level administration. Root accounts have access to all namespaces.

```bind
AUTH USER root    "$2a$10$..."   // bcrypt or clear text
AUTH CSV          "admins.csv"          // columns: username;password
AUTH JSON         "admins.json"         // { "username": "password" }
AUTH BEGIN
    if (user === "root" && checkPwd("secret")) allow()
    else reject("unauthorized")
END AUTH
```

---

### OAUTH2 DEFINE — OAuth2 Providers
```bind
OAUTH2 google DEFINE
    CLIENTID     "your-client-id"
    CLIENTSECRET "your-secret"
    REDIRECTURL  "https://myapp.com/api/auth/google/callback"
    ENDPOINT     "https://accounts.google.com/o/oauth2/v2/auth"
    TOKENURL     "https://oauth2.googleapis.com/token"
    USERINFOURL  "https://www.googleapis.com/oauth2/v3/userinfo"
    SCOPE        "openid"
    SCOPE        "email"
    SCOPE        "profile"
END OAUTH2
```

---

### NAMESPACE DEFINE
Namespaces provide isolation for your data and users.

```bind
NAMESPACE global DEFINE [default auth=password,google]
    HOOK onRead BEGIN
        if (!user) reject("authentication required")
    END HOOK
END NAMESPACE
```

---

### ROLE DEFINE
```bind
ROLE admin DEFINE [namespace=global]
    PERMISSION * [actions=*]
END ROLE
```

---

### SCHEMA DEFINE
Defines the structure of documents (tables).

```bind
SCHEMA products DEFINE [namespace=global icon=box color=#3B82F6 softDelete=true]
    FIELD name     string  [required]
    FIELD price    number  [default=0]
    FIELD tags     array

    HOOK onRead BEGIN
        if (doc.price < 0) modify({ ...doc, price: 0 })
    END HOOK
END SCHEMA
```

---

## JavaScript API — `database`

In JavaScript, all database and CRUD features are accessed via the `database`'s global object.

```js
const db = database.connection("name_of_crud") 
// or default database : database.default

// -- High-level CRUD --
const { token, user } = db.login("alice@e.com", { password: "secret" })
const products = db.collection("products")
const items = products.find({ price: { $gt: 100 } })

// -- Raw GORM access (classic mode) --
const User = db.model('User', { name: 'string' })
const user = User.findOne({ name: 'Alice' })
```

---

## Admin UI

The Admin UI is automatically mounted on `/{prefix}/_admin` (e.g., `/api/_admin`). Access requires a **Root account**.

### Customization
```bind
DATABASE 'sqlite://data.db' [default]
    ADMIN DEFINE
        PAGE "/metrics" [title="Metrics"] BEGIN
            <h1>Server Metrics</h1>
        END PAGE
        LINK "https://docs.example.com" [title="Docs"]
    END ADMIN
END DATABASE
```
