# Templating System

The HTTP Server features a powerful two-stage templating system that combines **Server-Side JavaScript** for logic and **Mustache** for data binding and presentation.

## How it Works

The rendering process follows a strict sequence:

1.  **Stage 1: JavaScript Execution**: The server parses and executes all `<script server>`, `<?js ... ?>`, and `<?= ... ?>` blocks.
2.  **Stage 2: Variable Export**: All global variables defined in the JS environment are exported as a context object.
3.  **Stage 3: Mustache Rendering**: The resulting content is passed to the Mustache engine, which uses the exported variables to fill in `{{tags}}`.
4.  **Stage 4: Post-Processing**: HTML comments are removed, and HTMX is injected if applicable.

---

## Variable Sharing

Variables defined in JavaScript are automatically available to Mustache.

```html
<script server>
    var title = "My Dashboard";
    var user = { name: "Alice", isAdmin: true };
    var stats = [10, 20, 30];
</script>

<h1>{{title}}</h1>
<p>Welcome, {{user.name}}</p>

<ul>
    {{#stats}}
    <li>Value: {{.}}</li>
    {{/stats}}
</ul>
```

---

## Logic and Control Flow

Since Mustache is logic-less, all complex transformations and filterings should be done in the JavaScript stage.

### Conditionals

```html
<script server>
    var showFeature = true;
    var items = ["A", "B"];
    var hasItems = items.length > 0;
</script>

{{#showFeature}}
    <div class="feature">Feature is enabled</div>
{{/showFeature}}

{{#hasItems}}
    <p>You have {{items.length}} items.</p>
{{/hasItems}}
{{^hasItems}}
    <p>No items found.</p>
{{/hasItems}}
```

### Loops and Aggregations

Use JavaScript to prepare your data, then iterate with Mustache.

```html
<script server>
    const db = require("db").connect("sqlite:///data.db");
    const Product = db.Model("Product");
    
    // Fetch and prepare data
    var products = Product.find({ price: { $gt: 100 } }).exec();
    var totalValue = products.reduce((acc, p) => acc + p.price, 0);
</script>

<h2>Total Value: ${{totalValue}}</h2>
<ul>
    {{#products}}
    <li>{{name}}: ${{price}}</li>
    {{/products}}
</ul>
```

---

## Partials and Components

### Mustache Partials
Mustache partials are useful for static HTML fragments or simple data-bound components. They are looked up in the same directory as the current file.

```html
<!-- index.html -->
<div>
    {{> sidebar}}
</div>
```

```html
<!-- Using JS include -->
<?= include("dynamic_widget.html") ?>
```

### Layouts and Shells

The `FsRouter` provides hierarchical layout support using `_layout.html`. The page content (or child layout) is injected via the `{{content}}` tag.

```html
<!-- _layout.html -->
<div class="wrapper">
    <header>My Website</header>
    {{content}}
</div>
```

If a file has `.partial` in its name (e.g., `info.partial.html`), it **skips** the layout wrapping but still undergoes the full JS + Mustache rendering process. This is ideal for fragments meant to be injected into an existing page.

---

## Best Practices

1.  **Prepare in JS, Reveal in Mustache**: Keep your HTML clean. Do all heavy lifting (DB queries, mapping, filtering) in `<script server>` blocks.
2.  **Safety**: The server automatically removes `<!-- comments -->` before processing JS to prevent accidental code exposure in the final HTML.
3.  **HTMX Integration**: Templates are HTMX-aware. If you return a full page, `htmx.js` is automatically injected into the `<head>`.
