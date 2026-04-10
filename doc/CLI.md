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

Serving virtual hosts from a directory:
```bash
./http-server ./vhosts --vhosts
```

## Options

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--config-file` | `-c` | `app` | Config files (json/yaml/toml), comma-separated |
| `--env-file` | | `.env` | Env files (.env/.conf), comma-separated |
| `--env-prefix`| | `APP_`  | Environment variable prefix |
| `--hot-reload`| `-H` | `true` | Enable hot-reload |
| `--port` | `-p` | `8080` | Port to use |
| `--address` | `-a` | `0.0.0.0` | Address to use |
| `--socket` | `-s` | | Listen on a Unix domain socket (internal use/advanced) |
| `--dir-listing` | `-L` | `true` | Show directory listings |
| `--auto-index` | `-I` | `true` | Display `index.html` automatically |
| `--index` | | `index.html` | Specify the default index file name |
| `--template-ext` | `-e` | `.html` | Default template file extension if none supplied |
| `--no-template` | | `false` | Disable the JavaScript and Mustache template engine |
| `--htmx-url` | | `...htmx.org@2` | HTMX URL to inject |
| `--no-htmx` | | `false` | Disable HTMX injection |
| `--inject-html` | | | Inject custom HTML |
| `--gzip` | `-G` | `true` | Enable gzip compression |
| `--brotli` | `-B` | `true` | Enable brotli compression |
| `--deflate` | `-D` | `true` | Enable deflate compression |
| `--silent` | `-S` | `false` | Suppress log messages |
| `--stdout` | | | File to redirect stdout to in test mode |
| `--stderr` | | | File to redirect stderr to in test mode |
| `--cors` | | `false` | Enable CORS |
| `--cache` | | `3600` | Cache time (seconds) |
| `--proxy` | | | Fallback proxy if file not found |
| `--https` | | `false` | Enable HTTPS |
| `--cert` | | `cert.pem`| Path to SSL certificate |
| `--key` | | `key.pem` | Path to SSL key |
| `--robots` | | `false` | Respond to `/robots.txt` using the file |
| `--robots-file` | | `robots.txt`| The robots.txt file to serve |
| `--read-timeout` | | `30` | Connection read timeout (seconds) (0=disabled) |
| `--write-timeout` | | `0` | Connection write timeout (seconds) (0=disabled, recommended for SSE/WS) |
| `--idle-timeout` | | `120` | Connection idle timeout (seconds) (0=disabled) |
| `--find` | | | CSS selector to find elements in test mode |
| `--match` | | | Validation expression (regex or JS) in test mode |
| `--vhosts` | `-V` | `false` | Enable virtual hosts (root becomes vhost directory) |
| `--bind` | `-b` | | Paths to `.bind` configuration files (multiple allowed) |
| `--help` | `-?` | `false` | Print help and exit |

> [!NOTE]
> All boolean flags (e.g., `--gzip`, `--hot-reload`) support a `--no-` prefix (e.g., `--no-gzip`, `--no-hot-reload`) to explicitly disable the feature. If both are provided, the last one in the command line takes precedence.
>
> Flags marked as **static** (identified by `#` in internal tags, such as `--port`, `--address`, `--cert`, `--bind`) cannot be reloaded at runtime. Changing them in a config file will trigger a warning and require a server restart.


## Configuration Loading Order

The server loads its configuration in the following order (highest priority last):

1. **Defaults**: Built-in default values (e.g. port `8080`).
2. **Config Files**: JSON, YAML, or TOML files specified via `--config-file` (default: `app.json`/`app.yaml`/`app.toml`).
3. **Environment Files**: `.env` or `.conf` files specified via `--env-file` (default: `.env`).
4. **Environment Variables**: OS-level environment variables, prefixed via `--env-prefix` (default `APP_`). For example, `APP_PORT=9000`.
5. **CLI Flags**: Command-line arguments override any previous configuration.

### Hot-Reloading

The HTTP Server actively watches your configuration files (`--config-file` and `--env-file`) for changes. 
When a modification is saved, the server dynamically reloads the configuration:
- **Dynamic Fields** (e.g., timeouts, directories, HTTP injection, compression) are updated on-the-fly without dropping active connections.
- **Static Fields** (e.g., port, address, socket path, HTTPS certificates, vhost mode) log a warning and are ignored until the server is fully restarted, as they control core network attachments.

## Template Testing Command

The `test` subcommand allows you to render a template and validate its output without running a full server.

### Usage
```bash
./http-server test [file] [options]
```

## Multi-Process Isolation

When `--vhosts` is active, the server operates in a **Master-Worker** mode:

1.  **Master**: Listens on public ports, handles SNI (SSL/TLS), and acts as a reverse proxy.
2.  **Workers**: For each vhost, a dedicated child process is spawned and listens on a private Unix socket (located in `/tmp`).

This ensures that:
- Each vhost has its own memory and JavaScript environment.
- Crashes or heavy processing in one vhost do not affect others.
- Child processes are automatically terminated when the master process stops.

### .vhost Configuration
You can place a `.vhost` file inside a vhost subfolder to override defaults using **HCL syntax**:

```hcl
domain  = "my-explicit-domain.com"
aliases = ["alias1.com", "alias2.com"]

email = "admin@my-explicit-domain.com"
cert  = "/path/to/cert.pem"
key   = "/path/to/key.pem"

http {
  port = 80
}

https {
  port = 443
}
```

> For the complete Virtual Hosts reference (architecture, autocert, sockets, flags propagation, examples), see **[doc/VHOST.md](VHOST.md)**.

### Flags
- `--socket [path]` (or `-s`): Listen on a Unix domain socket instead of a TCP port (used internally by workers).

### Test Flags
- `domain = "my-explicit-domain.com"`: Sets the hostname for this folder.
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
