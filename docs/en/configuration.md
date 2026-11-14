# Configuration

## Configuration file

By default, `config.yaml` in the current working directory is used. You can specify another path with `LACTSSL_CONFIG`.

- If the file does not exist, it is generated on first startup, with a random `jwt_secret` and `salt`.
- Restart the service after changing the configuration.
- The configuration contains keys, so keep its permissions at `0600`.
- Precedence: **environment variables > `config.yaml` > built-in defaults**.

See [`config.yaml.example`](https://github.com/AXmishell/LACTSSL/blob/main/config.yaml.example) in the repository root for a complete commented example.

## Field reference

```yaml
server:
  addr: ""              # Full listen address, e.g. ":8080"; takes precedence over host + port when set
  host: ""              # Interface to bind; empty means all interfaces
  port: 8080            # Listen port
  trusted_proxies: []   # Trusted reverse proxy CIDRs

data_dir: "data"        # Data directory containing the SQLite database

database:
  path: ""              # Empty means <data_dir>/lactssl.db

log:
  level: info           # Process log level: debug, info, warn, error
  format: text          # Output format: text or json
  file: ""              # Log file; empty means stdout only
  also_stdout: true     # Keep writing to stdout when a file is configured
  max_size_mb: 20       # Rotate when a file reaches this size
  max_backups: 5        # Number of rotated files to keep; 0 means unlimited
  max_age_days: 30      # Days to keep rotated files; 0 means unlimited
  compress: true        # Compress rotated files
  access_log: true      # Record HTTP access logs
  system:
    enabled: true       # Persist to the System Logs page
    level: info         # Minimum level persisted
    access: false       # Persist HTTP access logs too
    retention_days: 7   # Retention in days
    max_rows: 20000     # Maximum rows

security:
  entry: ""             # Secret entry; randomly generated on first startup when empty
  public_url: ""        # Public URL, e.g. "https://ssl.example.com"
  jwt_secret: ""        # JWT signing key; generated and written back when empty; at least 32 characters when set manually
  salt: ""              # Encryption salt for sensitive fields; generated and written back when empty; at least 32 characters when set manually

acme:
  enabled: true         # Enable the built-in ACME server (enabled by default)
  addr: ""              # Dedicated listen address, e.g. ":14000"; empty mounts it at /acme on the main port
  base_url: ""          # Public URL, e.g. "https://pki.example.com:14000"
  tls:
    cert: ""            # TLS certificate for the dedicated ACME listener
    key: ""             # TLS private key for the dedicated ACME listener
  mtls:
    enabled: false      # Require client certificates
    client_ca: ""       # Path to the client CA certificate
```

### `server`

- `addr`: full listen address, such as `:8080` or `127.0.0.1:8080`.
- `host`, `port`: combined into the listen address when `addr` is not set. The Go binary serves pages and APIs on this port directly; frontend assets are embedded in the binary, and it **does not reverse proxy Vite's `5173`**. Port `5173` only exists when running `make dev`.
- `trusted_proxies`: list of trusted reverse proxy IPs/CIDRs. Empty means no proxy headers are trusted, which suits direct deployments. When a reverse proxy terminates TLS this must be set, otherwise `Secure` cookies and client IP detection will be incorrect.

### `data_dir` and `database.path`

The data directory stores the database. It is created with `0700` permissions. When `database.path` is empty, `<data_dir>/lactssl.db` is used.

### `log`

Process logs are separate from the UI operation log. Operation logs are stored in the database and shown on the Logs page; process logs cover startup, HTTP, scheduling, plugins, ACME, and other runtime troubleshooting information.

- `level`: minimum level: `debug`, `info`, `warn`, or `error`. `debug` also includes the source location.
- `format`: `text` for humans and Docker logs, or `json` for log collectors.
- `file`: log file path. Empty means stdout only; relative paths are resolved from the process working directory. Missing directories are created with `0700`, and the file is written with `0600`.
- `also_stdout`: keep writing to stdout when a file is configured. Recommended for Docker so `docker compose logs` still works.
- `max_size_mb`: rotate after a file reaches this size (`1-1024`).
- `max_backups`: number of rotated files to keep; `0` means unlimited.
- `max_age_days`: days to keep rotated files; `0` means unlimited.
- `compress`: gzip rotated files.
- `access_log`: record HTTP access logs. Entries contain method, route template, status, latency, and client IP only; **query strings, request bodies, and authentication headers are never logged**. The secret entry is replaced with `:entry`, and every response includes `X-Request-ID` for correlation.

Example: persist logs under the data directory and keep them for seven days:

```yaml
log:
  level: info
  file: "data/logs/lactssl.log"
  also_stdout: true
  max_size_mb: 20
  max_backups: 10
  max_age_days: 7
  compress: true
  access_log: true
  system:
    enabled: true
    level: info
    access: false
    retention_days: 7
    max_rows: 20000
```

#### System logs

`log.system` controls which process logs are persisted to the database and shown on the System Logs page. It is independent of the operation log:

- `enabled`: persist new process logs. When disabled, no new records are created, but historical records remain visible and can be cleared.
- `level`: minimum level persisted. It cannot be lower than `log.level`; for example, with `log.level: info`, setting `level: debug` still will not record debug logs.
- `access`: persist HTTP access logs as well. Defaults to `false` to avoid writing every request to SQLite; high traffic can fill the database quickly.
- `retention_days`: retention in days (`1-365`).
- `max_rows`: maximum rows (`100-1000000`); the oldest records are removed first.

Writes use a bounded asynchronous queue, so a busy database never blocks requests. When the queue is full, new entries are dropped. System logs may contain client IPs, domains, and error details; treat them as operational data.

### `security`

- `entry`: path segment of the admin secret entry. Manual values must be 8-32 letters, digits, underscores, or hyphens. When empty, a random 20-character mix of upper- and lowercase letters and digits is generated on first startup and saved to the database.
- `public_url`: public URL, such as `https://ssl.example.com` (without a path). Links in password reset emails are generated from this address to prevent Host header injection; when unset, only loopback requests with a `localhost`/loopback Host may derive the link from the Host header. It can also be overridden with the `LACTSSL_PUBLIC_URL` environment variable.
- `jwt_secret`: signing key for login tokens. When empty, a random 32-byte value is generated and written back to the configuration file; manual values must be at least 32 characters, otherwise startup is refused.
- `salt`: encryption salt for sensitive fields in the database (DNS/deployment credentials, notification keys, private CA and certificate private keys, etc.). When empty it is generated and written back; manual values must be at least 32 characters, otherwise startup is refused.

### `acme`

- `enabled`: whether to enable the built-in ACME server. Enabled by default. No certificates are issued until an endpoint is created; set to `false` to disable the protocol endpoint entirely.
- `addr`: dedicated listen address, such as `:14000`. When empty, the ACME protocol is mounted under the `/acme` prefix on the main port.
- `base_url`: ACME base URL that clients actually reach, used to generate directory and account resource URLs. Public deployments must set a fixed address and must not rely on the request Host.
- `tls.cert` / `tls.key`: used when the dedicated ACME listener provides TLS directly; leave empty when a reverse proxy terminates TLS.
- `mtls.enabled` / `mtls.client_ca`: enable when client certificates are required, and specify the client CA certificate. When mTLS is enabled, the ACME TLS certificate and private key must also be configured.

> **Always back up `config.yaml`.** Losing `salt` makes encrypted credentials in the database unrecoverable; losing or changing `jwt_secret` invalidates existing login sessions. The service also rotates the JWT signing subkey on every start, so a restart requires signing in again.

## Environment variables

| Variable | Description |
| --- | --- |
| `LACTSSL_CONFIG` | Configuration file path, defaults to `config.yaml` |
| `LACTSSL_ADDR` | Full listen address, overrides `server.addr` |
| `LACTSSL_PORT` | Listen port, overrides `server.port` |
| `LACTSSL_DATA` | Data directory, overrides `data_dir` |
| `LACTSSL_ENTRY` | Secret entry, overrides `security.entry` and writes it back to the database |
| `LACTSSL_PUBLIC_URL` | Public URL, overrides `security.public_url` |
| `LACTSSL_TRUSTED_PROXIES` | Comma-separated trusted proxy list, overrides `server.trusted_proxies` |
| `LACTSSL_ACME_ENABLED` | Whether to enable the ACME server, overrides `acme.enabled` |
| `LACTSSL_ACME_ADDR` | Dedicated ACME listen address, overrides `acme.addr` |
| `LACTSSL_ACME_BASE_URL` | ACME public URL, overrides `acme.base_url` |
| `LACTSSL_ACME_TLS_CERT` | ACME TLS certificate path |
| `LACTSSL_ACME_TLS_KEY` | ACME TLS private key path |
| `LACTSSL_ACME_MTLS_ENABLED` | Whether to require ACME client certificates |
| `LACTSSL_ACME_MTLS_CLIENT_CA` | ACME client CA certificate path |
| `LACTSSL_LOG_LEVEL` | Process log level, overrides `log.level` |
| `LACTSSL_LOG_FORMAT` | Log format, overrides `log.format` |
| `LACTSSL_LOG_FILE` | Log file path, overrides `log.file` |
| `LACTSSL_LOG_ALSO_STDOUT` | Keep writing to stdout when a file is configured |
| `LACTSSL_LOG_MAX_SIZE_MB` | Maximum size of a single log file in MB |
| `LACTSSL_LOG_MAX_BACKUPS` | Number of rotated files to keep; `0` means unlimited |
| `LACTSSL_LOG_MAX_AGE_DAYS` | Days to keep rotated files; `0` means unlimited |
| `LACTSSL_LOG_COMPRESS` | Compress rotated files |
| `LACTSSL_LOG_ACCESS` | Record HTTP access logs |
| `LACTSSL_LOG_SYSTEM_ENABLED` | Persist process logs to the database |
| `LACTSSL_LOG_SYSTEM_LEVEL` | Minimum level persisted |
| `LACTSSL_LOG_SYSTEM_ACCESS` | Persist HTTP access logs too |
| `LACTSSL_LOG_SYSTEM_RETENTION_DAYS` | System log retention in days |
| `LACTSSL_LOG_SYSTEM_MAX_ROWS` | Maximum number of system log rows |
| `LACTSSL_LANG` | CLI output language, such as `zh-CN` or `en-US` |

Example:

```bash
LACTSSL_PORT=9000 \
LACTSSL_DATA=/var/lib/lactssl \
LACTSSL_TRUSTED_PROXIES=127.0.0.1,10.0.0.0/8 \
./bin/lactssl
```

The secret entry can also be changed after startup under "Settings → Secret entry" without restarting.

## DNS plugins

The `plugins` section controls DNS exec helpers; see [DNS plugins](dns-plugins.md). The default main binary compiles no DNS providers. The 35 former built-in providers are supplied by a helper and enabled by default; the remaining providers are disabled by default and can be enabled individually under the Plugins page (the state is stored in the database, not in this file).

```yaml
plugins:
  enabled: true
  dir: ""                 # empty means <data_dir>/plugins/dns
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64
```

## Deployment plugins

The `deploy_plugins` section controls external certificate deployment plugins; see [Deployment plugins](deploy-plugins.md). These plugins receive the certificate private key and are disabled by default. DNS plugins and deployment plugins use separate subdirectories, `<data_dir>/plugins/dns` and `<data_dir>/plugins/deploy`, so the two kinds of helper are never mixed.

```yaml
deploy_plugins:
  enabled: false
  dir: ""                 # empty means <data_dir>/plugins/deploy
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64
```

## Related documentation

- [Security](security.md)
- [Operations and backup](operations.md)
