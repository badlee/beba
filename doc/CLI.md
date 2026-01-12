# CLI Documentation

The HTTP Server comes with a versatile CLI to serve files, process templates, and run automated tests.

## Basic Usage

Serving the current directory:
```bash
./http-server
```

Serving a specific directory:
```bash
./http-server ./public
```

## Options

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--port` | `-p` | `8080` | Port to use |
| `--address` | `-a` | `0.0.0.0` | Address to use |
| `--show-dir` | `-d` | `true` | Show directory listings |
| `--auto-index`| `-i` | `true` | Display `index.html` automatically |
| `--gzip` | `-g` | `false` | Enable gzip compression |
| `--brotli` | `-b` | `false` | Enable brotli compression |
| `--silent` | `-s` | `false` | Suppress log messages |
| `--cors` | | `false` | Enable CORS |
| `--cache` | `-c` | `3600` | Cache time (seconds) |
| `--proxy` | `-P` | | Fallback proxy if file not found |
| `--ssl` | `-S` | `false` | Enable HTTPS |
| `--cert` | `-C` | `cert.pem`| Path to SSL certificate |
| `--key` | `-K` | `key.pem` | Path to SSL key |
| `--robots` | `-r` | `false` | Respond to `/robots.txt` |
| `--ext` | `-e` | | Default file extension if none supplied |
| `--no-template`| | `false` | Disable template engine |
| `--stdout` | | `stdout.log` | File to redirect stdout to in test mode |
| `--stderr` | | `stderr.log` | File to redirect stderr to in test mode |

## Template Testing Command

The `test` subcommand allows you to render a template and validate its output without running a full server.

### Usage
```bash
./http-server test [file] [options]
```

### Test Flags
- `--find [selector]`: CSS selector to target specific elements in the rendered HTML (e.g., `h1`, `.status`, `#title`).
- `--match [expression]`: Validation expression to check the targeted elements.

### Match Expressions
- **RegExp**: Wrapped in slashes with optional JS-style flags (e.g., `--match "/Success/i"` for case-insensitive). Supported flags: `i`, `m`, `s`.
- **JS Expression**: Simple JS boolean logic. Available variables:
    - `text`: The text content of the element.
    - `html`: The inner HTML of the element.
    - `stdout`: The captured output of the rendered template.
    - `stderr`: The captured errors of the rendered template.

### Examples
Check if the title contains "Home":
```bash
./http-server test index.html --find "title" --match "/Home/"
```

Check if a price value is correct using JS:
```bash
./http-server test product.html --find ".price" --match "text == '$19.99'"
```

### Log Redirection
When running a test, `stdout` and `stderr` are automatically captured to:
- `stdout.log`: Contains all `console.log` and standard output from your server-side scripts.
- `stderr.log`: Contains errors and warnings.
These logs are displayed at the end of the test run for debugging.
