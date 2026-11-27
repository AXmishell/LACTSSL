# Configuration reference

LACTSSL application configuration comes only from `config.yaml` and built-in defaults; no application-level environment variables are read. Restart the service for configuration changes to take effect. Some runtime settings (secure entry, plugin enablement state) are stored in the database and take precedence over the configuration file.

## Configuration file model

By default, `config.yaml` in the current working directory is read. You can specify another path at startup with `-config`.

- If the file does not exist, it is generated automatically on first startup, with a randomly generated `jwt_secret` and `salt` written to it.
- The file contains secrets; keep permissions at `0600`.
- Application configuration comes only from `config.yaml` and built-in defaults. Go HTTP client behavior, such as system proxy settings, can still be configured through environment variables.
- Configuration changes take effect after a restart. The secure entry can be changed in the UI and takes effect immediately.
- For a complete annotated example, see [`config.yaml.example`](https://github.com/AXmishell/LACTSSL/blob/main/config.yaml.example) in the repository root.

### Minimal production configuration

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

data_dir: "data"

security:
  public_url: "https://ssl.example.com"
  session_persistence: true

log:
  level: info
  format: json
  file: "data/logs/lactssl.log"
  also_stdout: true
  max_size_mb: 50
  max_backups: 10
  max_age_days: 30
  compress: true
  access_log: true
  system:
    enabled: true
    level: info
    access: false
    retention_days: 30
    max_rows: 200000
  archive:
    enabled: true
    compress: true
    retention_days: 0
    max_total_mb: 0
    quota_policy: fail
```

### Configuration precedence

| Setting | Source | Notes |
| --- | --- | --- |
| Listen address, logging, secrets, ACME | `config.yaml` | Restart required after changes |
| Secure entry | Database; `config.yaml` can override | UI changes take effect immediately; a non-empty `security.entry` overrides at startup |
| Plugin enablement state | Database allowlist | Preserved after restarts and rescans |
| System language, notification channels, etc. | Database | Changed in the UI |

### Web configuration editor

After signing in, open **Settings → Configuration file** to edit whitelisted `config.yaml` fields, including the listen address, client IP mode, PROXY protocol, trusted reverse proxies, `security.public_url`, session persistence, rate limits, ACME, logging and log archives, upgrade and plugin runtime settings.

- The rate limit tab shows effective rules after merging defaults; saving writes to `config.yaml` and takes effect after restart. **Reset to default** removes `security.rate_limit.rules.<name>` and restores the built-in default.
- `upgrade.program.allow_unsigned` skips release signature verification; use it only in controlled environments. `jwt_secret`, `salt`, `data_dir`, `database.path`, the log file, and plugin directories remain server-side only.
- A snapshot is created under `<data_dir>/config-backups/` before every save (config only, not the database). Restoring rewrites whitelisted fields only; `jwt_secret`, `salt`, `data_dir`, and `database.path` stay unchanged.
- Restart to apply changes; a listen-port change triggers a port preflight, and an unavailable address may prevent recovery, so keep SSH or console access. **Trusted reverse proxies** auto-detection uses only the TCP peer, never adds forwarding headers, and shows saved/applied lists, client IP source, skipped entries, and parsing warnings.

## Full field example

See [`config.yaml.example`](https://github.com/AXmishell/LACTSSL/blob/main/config.yaml.example) for the complete annotated file. The sections below describe every field and default; the minimal production configuration is in §1.1.

Do not copy the annotated example verbatim to production, and never commit a populated `config.yaml`; it contains `jwt_secret` and `salt`.

## `server`

- `addr`: full listen address such as `:8080` or `127.0.0.1:8080`.
- `host`, `port`: combined when `addr` is empty. The Go binary serves pages and APIs directly; frontend assets are embedded and not proxied from Vite. Port `5173` exists only under `make dev`.
- `trusted_proxies`: trusted reverse proxy IPs/CIDRs. Empty trusts no proxy headers, suitable for direct deployments; behind a TLS-terminating proxy this is required or `Secure` cookies and client IPs break.

Production recommendations: bind `host` to `127.0.0.1` or an internal address and use a reverse proxy; list only actual proxies (never `0.0.0.0/0`), using the internal CIDR for a separate proxy host.

## `data_dir` and `database.path`

The data directory stores the database, plugins, logs, and upgrade backups. It is created with `0700` permissions. When `database.path` is empty, `<data_dir>/lactssl.db` is used.

- Do not place the data directory on a shared writable path or in a public directory.
- Do not put SQLite on multi-writer shared storage such as NFS/SMB; locking is unreliable there. Back up `config.yaml` plus the entire `data/` directory.

## `log`

Process-level logging and operation logs serve different purposes: operation logs record who did what and appear on the Logs → Operation tab; process logs are for runtime troubleshooting, can be written asynchronously to the database for the Logs → System tab, and are also emitted to stdout/files.

- `level` / `format`: `debug`, `info`, `warn`, `error`; `debug` adds source locations for temporary troubleshooting. Use `text` for reading and Docker logs, `json` for collectors.
- `file` / `also_stdout` / `max_size_mb` / `max_backups` / `max_age_days` / `compress`: empty `file` means stdout only; relative paths use the working directory. Missing directories are created (`0700`) and files `0600`; keep `also_stdout: true` in Docker. `max_size_mb` (1–1024) rotates with a timestamp, `max_backups` and `max_age_days` (0 = unlimited) delete the oldest files, and `compress` gzips rotated files.
- `access_log`: records method, route template, status, latency, and client IP; no query parameters, bodies, or authentication headers. The secure entry becomes `:entry`, and every response carries `X-Request-ID`.
- `system.enabled` / `level` / `access` / `retention_days` / `max_rows`: database logging for Logs → System. `level` cannot be lower than `log.level`; `access` defaults to `false` to avoid per-request SQLite writes; retention is 1–365 days and 100–1000000 rows, deleting the oldest rows.
- `archive.enabled` / `dir` / `compress` / `retention_days` / `max_total_mb` / `quota_policy`: archive before deletion; an archive failure blocks deletion. The directory defaults to `<data_dir>/archives/logs` (`0700`, files `0600`, `.ndjson` when compression is off), retention is 0–3650 days (0 keeps forever), and `max_total_mb` 0 means unlimited. `fail` rejects archiving and blocks deletion; `prune_oldest` deletes the oldest archives and destroys audit material.

Each archive consists of a `*.ndjson.gz` (or `.ndjson`) data file and a `*.manifest.json` containing row count, ID range, time range, trigger reason, operator, SHA-256, and file size. The archive directory is not included in the default ZIP backup and must be backed up separately.

Writes use a bounded asynchronous queue, so a busy database does not block application requests; when the queue is full, new logs are dropped. System logs may contain client IPs, domains, and error details; manage them as operational data.

Production recommendations: place log files on a dedicated disk or mounted volume; use `format: json` to integrate with a log platform; keep `access_log: true` for auditing; leave `system.access` at `false` unless short-term troubleshooting requires it.

## `security`

- `entry`: 8–32 letters, digits, underscores, or hyphens; when empty, a random 20-character value is generated on first startup and saved to the database.
- `public_url`: external URL such as `https://ssl.example.com` (no path). Password-reset links use it to prevent Host header injection; when unset, only loopback requests may derive the address from Host.
- `session_persistence`: defaults to `true`. When true, startup keeps the `sessions` table and signs JWTs with the fixed `jwt_secret`; when false, every startup invalidates all logins.
- `jwt_secret` / `salt`: generated and written back when empty; manual values must be at least 32 characters or startup is refused. `salt` encrypts DNS/deployment credentials, notification keys, and private CA/certificate keys.
- `rate_limit.max_keys`: maximum in-memory token-bucket keys (default `10000`); the least recently used are evicted.
- `rate_limit.rules`: `max` requests per `window_seconds`; omitted rules use defaults, omitting `window_seconds` keeps the default, and `max: 0` disables a rule. Unknown names fail startup.

Common rules and defaults:

| Rule | Default | Purpose |
| --- | --- | --- |
| `login_ip` / `login_account` / `login_totp` | 10/5 min, 20/15 min, 5/10 min | Login IP, account failures, TOTP verification |
| `forgot_ip` / `forgot_account` | 5/10 min, 3/hour | Password reset |
| `passkey_login_begin` / `passkey_login_fail` | 20/5 min, 10/5 min | Passkey sign-in |
| `reauth` | 10/10 min | Reauthentication failures for sensitive actions |
| `api_ip` / `api_anonymous` / `entry` | 600/min, 120/min, 60/min | `/api` total, anonymous requests, and entry probing |
| `api_key_ip_denied` | 10/10 min | Audit log throttling for API key requests rejected by source IP |
| `api_key_dns_denied` | 10/10 min | Audit log throttling for API key attempts to use an unauthorized DNS account |
| `csr_create` | 120/hour | ACME client CSR submissions |
| `acme_request` / `acme_account` / `acme_pki` | 1200/min, 30/hour, 600/min | ACME protocol entry, account creation, CRL/OCSP |
| `acme_order` | 300/hour | Order limit when an ACME endpoint does not set its own |

> Rate limit changes require a service restart. The per-endpoint "orders per hour" setting in the admin UI takes precedence over `acme_order`.

> **Always back up `config.yaml`.** If `salt` is lost, credentials already encrypted in the database cannot be decrypted; losing or changing `jwt_secret` invalidates existing login sessions. Session persistence is enabled by default, so a restart keeps server-side sessions. Set `security.session_persistence: false` to restore strict mode, where every startup clears sessions and rotates the JWT signing subkey.

- Keep `entry` as a long random string, not `admin`, `ssl`, or `panel`; `public_url` must be a fixed HTTPS address and must not depend on the request Host.
- Choose `session_persistence` based on your deployment policy; it is enabled by default, so a restart does not force reauthentication and a longer session lifetime must be acceptable.
- Store `jwt_secret` and `salt` in a password manager, never in version control. Changing `salt` makes existing encrypted credentials unusable; plan the migration and never change it directly.

## `acme`

- `enabled`: enables the built-in ACME server (on by default). No certificates are issued externally until an endpoint is created; `false` disables the protocol endpoint entirely.
- `addr`: dedicated listen address such as `:14000`; when empty, ACME mounts under `/acme` on the main port.
- `base_url`: the URL clients access, used for the directory, account resource URLs, and CRL/OCSP addresses. Public deployments must set a fixed address and never rely on the request Host; when unset it falls back to `security.public_url`, then to the request Host with a startup warning.
- `tls.cert` / `tls.key` and `mtls.enabled` / `mtls.client_ca`: configure the first pair when the dedicated listener terminates TLS directly, and the mTLS pair when client certificates are required (the ACME TLS certificate and key are then also mandatory).

- For public deployments, use a dedicated port, restrict source addresses, configure a domain allowlist on every endpoint, and enable EAB in production.
- `base_url` must be a fixed public address to prevent Host header injection; if ACME is not needed, set `enabled: false` to reduce the attack surface.

## `plugins`, `deploy_plugins`, `notify_plugins`

The three plugin kinds use `<data_dir>/plugins/dns`, `<data_dir>/plugins/deploy`, and `<data_dir>/plugins/notify` respectively; the subdirectories are never mixed.

### DNS plugins

```yaml
plugins:
  enabled: true
  dir: ""                 # Empty means <data_dir>/plugins/dns
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64
```

Uploaded plugins use the `strict` sandbox by default. On Linux it applies Landlock file rules, rlimits, and `no_new_privs`; if unsupported, the host falls back to `basic`. Do not use `off` in production.

### Deployment plugins

Deployment plugins receive certificate private keys and are enabled by default:

```yaml
deploy_plugins:
  enabled: true
  dir: ""
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64
```

### Notification plugins

Email and official notification channels are built in; notification plugins are enabled by default:

```yaml
notify_plugins:
  enabled: true
  dir: ""
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64
```

Plugin directories use `0700` permissions and helpers use `0755`, owned by the service account. In production, configure `public_key` and disable `auto_update`. For detailed governance, see [Plugin governance](plugins.md).

## `upgrade`

`upgrade` controls automatic migration at startup and optimization behavior on the `/upgrade` page:

```yaml
upgrade:
  auto: true              # Run legacy migrations automatically at startup
  auto_optimize: true     # Add indexes, update statistics, checkpoint WAL at startup; no VACUUM
  vacuum: true            # Allow VACUUM during manual optimization on /upgrade
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable       # stable or beta
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    token: ""
```

- Program upgrades download the release package for the platform from GitHub Release, verify its SHA-256 against `checksums.txt`, atomically replace the current binary, and back up the previous version to `<data_dir>/upgrade-backups/program/`.
- Read-only filesystems, platforms without a release package, and `dev` builds are shown as not upgradable.
- Upgrading inside a container does not change the image; recreating the container returns to the image version. In production, upgrade by image tag.
- When `public_key` is configured, `checksums.txt.sig` is verified mandatorily; `token` raises the GitHub API rate limit.

## Command-line flags

| Flag | Description |
| --- | --- |
| `-config` | Configuration file path; defaults to `config.yaml` in the current working directory |

```bash
./bin/lactssl -config /etc/lactssl/config.yaml
```

The secure entry can also be changed after startup under Settings → Secure entry, without a restart.

## Reverse proxy requirements

### Trust boundary

- Only TCP peers in `server.trusted_proxies` are trusted; an empty list trusts no forwarding headers, which suits direct deployments. `X-Forwarded-For`, `X-Real-IP`, `Forwarded`, and `CF-Connecting-IP` are client-controlled until the proxy is trusted, and auto-detection only provides hints.
- Never configure `0.0.0.0/0`, `::/0`, or broad internal ranges; any host in a trusted range can claim to speak for clients.

### Overwrite mode (recommended)

Have the proxy closest to LACTSSL replace the forwarding headers with the real client address:

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-Proto $scheme;
```

In this mode `trusted_proxies` only needs the nearest proxy address, and one resolution step yields the client IP. In multi-hop deployments, let the edge proxy recover the real client first and then have the nearest hop overwrite the headers; do not let an intermediate proxy overwrite XFF with its upstream proxy address.

### Append mode

Nginx `$proxy_add_x_forwarded_for` preserves any client-supplied XFF and appends the current peer. LACTSSL walks the chain from right to left, skipping trusted entries:

- Single proxy: trusting only Nginx is sufficient. Multiple proxies: list every proxy that can appear in XFF in `trusted_proxies`, or resolution stops at the upstream proxy IP. Never add the leftmost client address to the trusted list.

For example, with `client → CDN → Nginx → LACTSSL`, append mode produces `client, CDN edge IP`. Trusting only Nginx yields the CDN edge IP; trust the official CDN ranges as well, or make Nginx overwrite XFF.

### CDN and cloud load balancers

- Trust `CF-Connecting-IP` only when the peer is in Cloudflare's official ranges; otherwise direct clients can spoof it. Enable `real_ip` in your own Nginx with those ranges and overwrite XFF with `$remote_addr`, so LACTSSL trusts only Nginx.
- CDN edge IPs change, so use official ranges rather than individual addresses. An L4/TCP load balancer exposes no HTTP headers; use PROXY protocol or place an HTTP-aware proxy behind it.

### Docker and Kubernetes

- The peer inside a container is often a bridge gateway or Service address, not the real client. Prefer the exact proxy container/node IP; if dynamic, use a dedicated network and trust only the proxy address or the smallest CIDR.
- Do not trust `172.16.0.0/12` merely because it is private, and verify whether the Ingress Controller sanitizes XFF instead of assuming all Ingresses behave the same.

### Apply and verify

1. Add proxy addresses to `trusted_proxies` and configure `security.public_url`; when TLS terminates at the proxy, pass `X-Forwarded-Proto: https`.
2. Give backup restore and plugin uploads a larger body limit (for example, Nginx `client_max_body_size 256m`) and read timeouts covering restore, plugin downloads, and deployments (for example, 600 seconds).
3. Restart after saving and check `peer_ip`, `applied_trusted_proxies`, `effective_client_ip`, and `client_ip_source` under **Settings → Configuration file → Auto detect**. Spoof-test with `X-Forwarded-For: 203.0.113.9` from an untrusted address; the server must still use the TCP peer, and access logs include `ip_source`, `ip_skipped`, and `ip_warnings`.

### Advanced proxy identity

Resolution order: trusted peer identity → optional Cloudflare header → `X-Forwarded-For` → `X-Real-IP` → RFC 7239 `Forwarded` → TCP peer. No mode allows forwarding headers from an untrusted source.

- **RFC 7239 `Forwarded`**: parsed automatically when earlier headers are unavailable. Supports `for=203.0.113.9`, `for=203.0.113.9:4711`, and `for="[2001:db8::1]:4711"`; obfuscated values such as `unknown` or `_hidden` are rejected.
- **Cloudflare mode**:
  ```yaml
  server:
    client_ip_mode: cloudflare
    trusted_proxies:
      - "173.245.48.0/20"
      - "103.21.244.0/22"
      # ... other official Cloudflare ranges
  ```
  `CF-Connecting-IP` is honored only when the peer is in `trusted_proxies`. Do not combine this mode with a local Nginx that merely overwrites headers; restrict the origin to Cloudflare.
- **PROXY protocol**:
  ```yaml
  server:
    proxy_protocol: true
  ```
  Accepts v1/v2 headers only from peers in `trusted_proxies`; a PROXY header from an untrusted source closes the connection. Normal HTTP without a PROXY header still works. An L4 load balancer must be configured to send it.
- **mTLS proxy identity**:
  ```yaml
  server:
    tls:
      cert: "/etc/lactssl/server.crt"
      key: "/etc/lactssl/server.key"
      proxy_client_ca: "/etc/lactssl/proxy-ca.pem"
  ```
  The main listener serves HTTPS; set `security.public_url` to an `https://` URL. A connection presenting a client certificate signed by `proxy_client_ca` is treated as a trusted proxy regardless of IP. Browsers without a client certificate still work. This mode is only for proxies that connect to LACTSSL directly over TLS; when TLS terminates at the proxy, keep IP/CIDR trust or PROXY protocol. Use a CA dedicated to proxies, not a browser client CA.

For a complete example, see [Installation](install.md).

## Configuration validation and troubleshooting

Common startup failure causes:

| Error | Cause | Resolution |
| --- | --- | --- |
| `jwt_secret` too short | Manually configured with fewer than 32 characters | Use at least 32 characters or leave empty to auto-generate |
| `salt` too short | Manually configured with fewer than 32 characters | Use at least 32 characters or leave empty to auto-generate |
| Listen failure | Port in use or insufficient permissions | Check the port and the runtime user |
| Incomplete mTLS configuration | mTLS enabled without a TLS certificate/private key | Configure `acme.tls.cert/key` as well |
| Configuration validation failed | Invalid restore package or manual edit | Fix according to the log; restore packages are validated before being applied |
| Changes not taking effect | Configuration requires a restart | Restart the service; secure entry and plugin page settings are exceptions |

Configuration restore and migration are handled by the upgrade tool; see [Upgrade and migration](upgrade.md).
