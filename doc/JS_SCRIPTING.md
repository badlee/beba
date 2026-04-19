# Server-Side JS Scripting

The HTTP Server allows executing server-side JavaScript directly within your HTML files using special tags. This execution happens before the page is served to the client, primarily to prepare data for the [Templating System](TEMPLATING.md).

## Special Tags

> [!CAUTION] Seules les variables declare avec var sont exposees au moteur de template.

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

## Global Scope & Injected Objects

The server-side JavaScript environment provides two types of global access:
1. **Built-in Globals**: Always available (e.g., `console`, `fetch`, `require`).
2. **Dynamic Injections**: Objects like `database`, `mail`, or `payment` are injected into the global scope **only if** their corresponding protocol directive is defined in the server's `.bind` configuration.

### Dynamic Injection Rules

| Protocol Directive | Global Object | Description |
| :--- | :--- | :--- |
| **`DATABASE`** | `database` | Unified access to connections and CRUD operations. |
| **`MAIL`** | `mail` | Advanced mailing engine (SMTP/API) with template support. |
| **`PAYMENT`** | `payment` | Unified payment integration (Stripe, PayPal, Crypto). |

> [!IMPORTANT]
> **Variable Naming via `NAME`**: When a directive includes a `NAME` instruction (e.g., `DATABASE ... NAME myapi`), the server automatically creates a **global variable** with that name (after sanitization) pointing directly to that instance.

### Built-in Global Registry

| Object | Description |
| :--- | :--- |
| `require(path)` | Load standard modules, Native Go modules, or local JS files. |
| `console` | Standard console for logging to terminal and system logs. |
| `fetch` | Standard Web Fetch API for HTTP/HTTPS requests. |
| `sse` | Access to the high-performance real-time Hub. |
| `cookies` | Managed access to the current request's cookies. |
| `settings` | Access to global configuration defined via `SET` in `.bind`. |
| `include(file)`| Process and include another template file. |
| `print(arg)` | Append data directly to the output buffer. |

### `cookies` Methods
- `get(name)`: Returns a cookie value.
- `set(name, value)`: Sets a cookie.
- `remove(name)`: Clears a cookie.
- `has(name)`: Checks if a cookie exists.

### Real-time & Hub Methods
The server uses a high-performance **Sharded Hub** (supporting 1M+ concurrent connections / WebSockets / Socket.IO / MQTT). In real-time handlers, the following functions are available globally:

- `onMessage(callback)`: Registers a handler for incoming messages from the client. The callback receives the raw message data (string or object).
- `onClose(callback)`: Registers a handler for when the connection is closed. Ideal for cleanup or final status updates.
- `onError(callback)`: Registers a handler for connection errors.
- `send(data, channel="global")`: Sends a message back to the current client.
- `publish(channel, data)`: Broadcasts a message to a specific channel in the Hub.
- `subscribe(channel, callback?)`: Subscribes the current connection to a channel. If a callback is provided, it will be triggered for messages on that channel.
- `unsubscribe(channel)`: Unsubscribes from a channel.

### Real-time Context (`ctx`)
When running inside a WebSocket, Socket.IO, or MQTT handler, the `ctx` object provides connection-specific metadata:
- `ctx.id`: The session ID of the client (often persistent across reconnections).
- `ctx.connID`: The unique physical connection ID (UUID).
- `ctx.query(key)`: Access to query parameters from the initial handshake.
- `ctx.params(key)`: Access to route parameters.
- `ctx.locals(key)`: Access to variables passed from previous middlewares.

> [!TIP]
> **Echo Prevention**: The Hub automatically filters out messages where the `SenderSID` matches the receiver's `ConnID`. This prevents infinite feedback loops when a script publishes a message it just received.


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

## PDF Generation Module (`pdf`)

The `pdf` module allows creating high-quality PDF documents directly from your server-side scripts. It supports vector graphics, images, and HTML rendering.

### Basic Example
```javascript
const pdf = require('pdf');

const doc = pdf.TCPDF({
    title: "My Report",
    orientation: "P",
    format: "A4"
});

doc.AddPage();
doc.SetFont("helvetica", "B", 18);
doc.Cell(0, 15, "Automated Report", 0, 1, "C");

doc.SetFont("helvetica", "", 11);
doc.WriteHTML("<p>This document was <b>generated</b> automatically.</p>", true, false);

doc.SavePDF("report.pdf");
```

### Reference
- **`AddPage()`**: Creates a new page.
- **`SetFont(family, style, size)`**: Changes the font. Style can be `B` (bold), `I` (italic), `U` (underline).
- **`WriteHTML(html, ln, fill)`**: Renders a subset of HTML tags.
- **`Cell(w, h, txt, ...)`**: Prints a rectangular area with text.
- **`Image(path, x, y, w, h, type)`**: Inserts an image from the filesystem.
- **`SetTextColor(r, g, b)`**: Sets the drawing color for text (RGB 0-255).
- **`GetOutPDFString()`**: Returns the raw PDF content for streaming or processing.

---

## Core Internal Modules

The environment provides several internal modules. Some are available as **Globals** (see above), while others must be explicitly loaded using **`require()`**.

### 📦 Quick Reference Table

| Module | Type | Access | Description |
| :--- | :--- | :--- | :--- |
| **Console** | Built-in | Global | Core logging interface. |
| **Web Fetch** | Built-in | Global | Standard HTTP/HTTPS requests. |
| **Database** | Built-in | Global / `db` | Unified ORM and CRUD engine (auto-init). |
| **Mail** | **Conditional** | Global | SMTP and Mail-API bridge. |
| **Payment** | **Conditional** | Global | Integrated payment gateways. |
| **File System** | Native | `fs` | Native file operations (Promise compatible). |
| **Path** | Native | `path` | Cross-platform path utilities. |
| **PDF** | Native | `pdf` | High-fidelity PDF generation (TCPDF). |
| **Storage** | Native | `storage` | SQLite backed sessions and KV cache. |
| **DTP** | Native | `dtp` | Device Transfer Protocol client. |

---

## 🌍 Global & Injected Objects Details

### `database` (Global)
In JavaScript, all database and CRUD features are accessed via the `database` global object. This object is always injected and available because the server automatically initializes a default SQLite database (`.data/beba.db`) behind the scenes upon startup.

```javascript
const db = database.connection("my_inventory") 
// Or use the default connection:
const db = database.default

// -- High-level CRUD --
const { token, user } = db.login("alice@email.com", { password: "secret" })
const products = db.collection("products")
const items = products.find({ price: { $gt: 100 } })

// -- Raw GORM access (Classic mode) --
const User = db.model('User', { name: 'string' })
const user = User.findOne({ name: 'Alice' })
```

### `mail` (Global)
Injected if the `MAIL` protocol is defined. Allows sending transactional emails and managing templates.
- `mail.send(to, subject, body)`
- `mail.template(name, data)`

### `payment` (Global)
Injected if the `PAYMENT` protocol is defined. Provides a unified interface for transaction management.
- `payment.checkout(amount, options)`
- `payment.verify(transactionID)`

### `console`
The standard logging interface. Output is redirected to the server's stdout/stderr.
- `console.log(...args)`: Standard output.
- `console.error(...args)`: Standard error.
- `console.info(...args)`: Informational messages.
- `console.warn(...args)`: Warning messages.

### `cookies`
- `get(name)`: Read a cookie value.
- `set(name, value)`: Set a cookie.
- `remove(name)`: Delete a cookie.
- `has(name)`: Check for existence.

### `fetch` (Web Fetch)
A standard implementation of the Web Fetch API for server-to-server requests.
```javascript
const response = await fetch("https://api.example.com/data");
const json = await response.json();
```

### `sse` (The Hub)
Access to the high-performance communication Hub.
- `publish(channel, data)`: Send a message to all subscribers.
- `to(channel).publish(event, data)`: Send a targeted event.

---

## 📘 Detailed API Reference

### 🗄️ Database & CRUD (`database`)

The `database` global object provides unified access to both high-level CRUD operations and identity management. It is always available by default due to the server's auto-initialization of `.data/beba.db`.

#### **Authentication & Identity**
- **`login(identity, options)`**: Authenticates a user.
    - `identity`: Email or Username.
    - `options`: `{ password, namespace }`.
    - Returns: `{ token, user }` if successful.
- **`logout(token)`**: Invalidates a session token.

#### **Data Operations**
- **`collection(schema_name)`**: Accesses a data collection (Table).
    - `find(filter)`: Returns a query builder (supports `$gt`, `$in`, etc.).
    - `create(data)`: Inserts a new document.
    - `update(id, patch)`: Updates a document.
    - `delete(id)`: Deletes (or trashes) a document.
- **`ns(slug)`**: Switches the context to a specific **Namespace** (Multi-tenancy).
    - Example: `database.ns("company_a").collection("products").find({})`

#### **Management API**
- **`schemas`**: `list()`, `get(id)`, `create(m)`, `update(id, patch)`, `delete(id)`, `move(schemaID, nsID)`.
- **`namespaces`**: `list()`, `create(m)`.
- **`users`**: `list()`, `create(m)`.
- **`roles`**: `list()`.

---

### 📧 Mail Service (`mail`)

Handles transactional emails through various providers (SMTP, Postmark, SendGrid, etc.). Injected if the `MAIL` protocol is defined.

#### **Sending Emails**
- **`send(message)`**: Sends an email via the default connection.
    - **`message` Object**:
        - `to`: Array of strings or single string.
        - `cc` / `bcc`: Array of strings.
        - `subject`: Email subject.
        - `text`: Plain text body.
        - `html`: HTML body.
        - `attachments`: Array of `{ filename, data, contentType }`.

#### **Templating**
- **`template(name, data)`**: Renders a mail template defined in the `.bind` file.

#### **Multi-Connection**
- **`connect(url, name, options)`**: Dynamically creates a new connection.
- **`connection(name)`**: Returns a specific connection instance.

---

### 💳 Payment Integration (`payment`)

Unified API for gateways like Stripe, Flutterwave, CinetPay, and **x402 (Crypto)**. Injected if the `PAYMENT` protocol is defined.

#### **Core Methods**
- **`checkout(amount, options)`**: Redirect-based payment.
    - `amount`: Number (base unit).
    - `options`: `{ currency, orderId, email, phone, success_url, cancel_url }`.
- **`charge(options)`**: Direct payment / Mobile Money prompt.
- **`verify(transactionID)`**: Manual status check.
- **`push(options)`**: Initiates a USSD Push (STK Push) for Mobile Money.
    - Alias: **`ussd()`**.

#### **Webhooks**
Inside a `WEBHOOK` block in `.bind`:
- **`request`**: Access `headers`, `body`, and `query`.
- **`payment`**: Populated with transaction data after provider parsing.
- **`verify(body, sig, secret)`**: Helper to validate signature.

---

## 🛠️ Native Modules (`require`)

### `fs` (File System)
Provides Node.js compatible file operations. Includes both Sync and Promise-based methods.
- **Sync**: `readFileSync`, `writeFileSync`, `existsSync`, `readdirSync`, etc.
- **Async**: `readFile`, `writeFile`, `readdir`, etc.

```javascript
const fs = require('fs');
const config = JSON.parse(fs.readFileSync('./config.json'));
```

### `path`
Cross-platform path manipulation (Node.js compatible).
- `join(...parts)`, `resolve(...parts)`, `basename(path)`, `dirname(path)`.

### `dtp` (Device Transfer Protocol)
Used for secure communication with hardware devices.

```javascript
const dtp = require('dtp');
const client = dtp.newClient("127.0.0.1:5000", "device_01", "secret_key");

client.on('connect', () => console.log("DTP Connected!"));
client.on('data', (pkt) => {
    console.log("Received: " + pkt.Payload); // pkt: { ID, Type, Subtype, Payload }
});
client.connect();
```
- **`sendData(subtype, payload, needAck?)`**: Send data to device.
- **`ping()`**: Blocking ping-pong check (returns response).

### `pdf`
High-fidelity PDF generation (TCPDF).
```javascript
const pdf = require('pdf');
const doc = pdf.TCPDF();
doc.AddPage();
doc.WriteHTML("<h1>Hello</h1>");
doc.SavePDF("output.pdf");
```

### `storage`
Persistent SQLite Key-Value storage.
- `session(id)`: Store for specific session.
- `shared`: Global persistent store.
- `cache`: High-speed volatile store.
