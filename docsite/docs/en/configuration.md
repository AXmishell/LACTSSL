# Configuration reference

LACTSSL application configuration comes only from `config.yaml` and built-in defaults; no application-level environment variables are read. Restart the service for configuration changes to take effect. Some runtime settings (secure entry, plugin enablement state) are stored in the database and take precedence over the configuration file.

## 1. Configuration file model

By default, `config.yaml` in the current working directory is read. You can specify another path at startup with `-config`.

- If the file does not exist, it is generated automatically on first startup, with a randomly generated `jwt_secret` and `salt` written to it.
- The file contains secrets; keep permissions at `0600`.
- Application configuration comes only from `config.yaml` and built-in defaults. Go HTTP client behavior, such as system proxy settings, can still be configured through environment variables.
- Configuration changes take effect after a restart. The secure entry can be changed in the UI and takes effect immediately.
- For a complete annotated example, see [`config.yaml.example`](https://github.com/AXmishell/LACTSSL/blob/main/config.yaml.example) in the repository root.

### 1.1 Minimal production configuration

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

### 1.2 Configuration precedence

| Setting | Source | Notes |
| --- | --- | --- |
| Listen address, logging, secrets, ACME | `config.yaml` | Restart required after changes |
| Secure entry | Database; `config.yaml` can override | UI changes take effect immediately; a non-empty `security.entry` overrides at startup |
| Plugin repository URL/channel/auto-update/public key/download proxy | Database takes precedence; `config.yaml` provides defaults | Saving on the Plugins page does not write back to the configuration file |
| Plugin enablement state | Database allowlist | Preserved after restarts and rescans |
| System language, notification channels, etc. | Database | Changed in the UI |

### 1.3 Web configuration editor

After signing in, open **Settings → Configuration file** to edit whitelisted `config.yaml` fields, including the listen address, client IP mode, PROXY protocol, trusted reverse proxies, `security.public_url`, session persistence, rate limits, ACME, logging and log archives, upgrade and plugin runtime settings.

- The rate limit tab shows effective rules after merging built-in defaults; saving writes them back to `config.yaml` and takes effect after a restart.
- Rate limit rules support **Reset to default**: after saving, the corresponding `security.rate_limit.rules.<name>` key is removed from `config.yaml` and the built-in default is restored.
- `upgrade.program.allow_unsigned` can be enabled in the form, but it skips release signature verification and should only be used in controlled environments.
- `jwt_secret`, `salt`, `data_dir`, `database.path`, the log file, plugin directories and repository trust roots remain server-side only.
- A snapshot is created under `<data_dir>/config-backups/` before every save; you can also create one manually. Snapshots contain only `config.yaml`, unlike the encrypted full backup that includes the database.
- Restoring a snapshot only rewrites whitelisted fields. The current `jwt_secret`, `salt`, `data_dir` and `database.path` remain unchanged, so database decryption is not broken.
- Restart the service to apply changes. When the listen port changes, the server performs a port preflight; if the new address is unavailable the service may not recover automatically, so keep SSH or console access available.
- **Trusted reverse proxies** supports auto-detection. Detection only uses the TCP peer address of the current request; headers such as `X-Forwarded-For` are shown for reference and are never added automatically. The result distinguishes the saved and currently applied trusted lists and shows the client IP source, skipped trusted entries, and forwarding-header parsing warnings. Configure `security.public_url` when running behind a proxy.

## 2. Full field example

```yaml
server:
  addr: ""              # Full listen address, e.g. ":8080"; takes precedence over host + port when non-empty
  host: ""              # Network interface to bind; empty means all interfaces
  port: 8080            # Listen port
  trusted_proxies: []   # Trusted reverse proxy CIDRs
  client_ip_mode: "auto"  # auto or cloudflare
  proxy_protocol: false   # Accept PROXY protocol v1/v2 from trusted proxies
  tls:                    # Main listener TLS; leave empty when the proxy terminates TLS
    cert: ""
    key: ""
    proxy_client_ca: ""   # Optional: client CA for mTLS proxy identity

data_dir: "data"        # Data directory containing the SQLite database

database:
  path: ""              # Empty means <data_dir>/lactssl.db

log:
  level: info           # Process log level: debug, info, warn, error
  format: text          # Output format: text or json
  file: ""              # Log file; empty means stdout only
  also_stdout: true     # Whether to also write to stdout when a file is configured
  max_size_mb: 20       # Maximum size per file; rotates when exceeded
  max_backups: 5        # Number of historical files to keep; 0 means unlimited
  max_age_days: 30      # Days to keep historical files; 0 means unlimited
  compress: true        # Whether to compress historical files
  access_log: true      # Whether to record HTTP access logs
  system:
    enabled: true       # Whether to write to the database for the Logs → System tab
    level: info         # Minimum level written to the database
    access: false       # Whether to also write HTTP access logs to the database
    retention_days: 7   # Retention in days
    max_rows: 20000     # Maximum rows
  archive:
    enabled: true       # Archive logs before deletion; archive failure blocks deletion
    dir: ""             # Empty means <data_dir>/archives/logs
    compress: true      # gzip compression
    retention_days: 0   # Archive retention in days; 0 keeps them forever
    max_total_mb: 0     # Archive directory quota in MB; 0 means unlimited
    quota_policy: fail  # fail or prune_oldest

security:
  entry: ""             # Secure entry; randomly generated on first startup when empty
  public_url: ""        # External URL, e.g. "https://ssl.example.com"
  session_persistence: true  # When false, login sessions are cleared on process restart
  jwt_secret: ""        # JWT signing key; generated and written back when empty; at least 32 characters when configured manually
  salt: ""              # Encryption salt for sensitive fields; generated and written back when empty; at least 32 characters when configured manually
  rate_limit:
    max_keys: 10000     # Maximum number of keys kept by the in-memory limiter
    rules:
      # max is the allowed count per window (0 disables the rule); window_seconds is the window length.
      # Rules not listed here use built-in defaults; see config.yaml.example for the full list.
      login_ip: {max: 10, window_seconds: 300}
      login_account: {max: 20, window_seconds: 900}
      api_ip: {max: 600, window_seconds: 60}
      api_key_ip_denied: {max: 10, window_seconds: 600}
      api_key_dns_denied: {max: 10, window_seconds: 600}
      acme_request: {max: 1200, window_seconds: 60}
      acme_order: {max: 300, window_seconds: 3600}

acme:
  enabled: true         # Whether to enable the built-in ACME server; enabled by default
  addr: ""              # Dedicated listen address, e.g. ":14000"; empty mounts at /acme on the main port
  base_url: ""          # External URL, e.g. "https://pki.example.com:14000"
  tls:
    cert: ""            # TLS certificate for the dedicated ACME listener
    key: ""             # TLS private key for the dedicated ACME listener
  mtls:
    enabled: false      # Whether to require client certificates
    client_ca: ""       # Path to the client CA certificate

plugins:
  enabled: true
  dir: ""                 # Empty means <data_dir>/plugins/dns
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64

deploy_plugins:
  enabled: false
  dir: ""                 # Empty means <data_dir>/plugins/deploy
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64

notify_plugins:
  enabled: true
  dir: ""                 # Empty means <data_dir>/plugins/notify
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64

upgrade:
  auto: true
  auto_optimize: true
  vacuum: true
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    # Official builds embed the release public key automatically; self-built binaries without it reject online upgrades unless unsigned upgrades are explicitly allowed in controlled environments
    allow_unsigned: false
    token: ""
```

## 3. `server`

- `addr`: full listen address, for example `:8080` or `127.0.0.1:8080`.
- `host`, `port`: combined into the listen address when `addr` is not set. The Go binary serves pages and APIs on this port directly; frontend assets are embedded in the binary and are not reverse-proxied from Vite's `5173`. Port `5173` exists only under `make dev`.
- `trusted_proxies`: list of trusted reverse proxy IPs/CIDRs. Empty means no proxy headers are trusted, which is suitable for direct deployments. When a reverse proxy terminates TLS, this must be configured; otherwise `Secure` cookies and client IP identification will be incorrect.

Production recommendations:

- Set `host` to `127.0.0.1` or an internal address, and access it through a reverse proxy.
- Include only actual proxy addresses in `trusted_proxies`; do not configure `0.0.0.0/0`.
- If the proxy and LACTSSL run on different hosts, use the internal CIDR.

## 4. `data_dir` and `database.path`

The data directory stores the database, plugins, logs, and upgrade backups. It is created with `0700` permissions. When `database.path` is empty, `<data_dir>/lactssl.db` is used.

- Do not place the data directory on a shared writable path or in a public directory.
- Do not place the database on multi-writer shared storage such as NFS/SMB; SQLite is single-writer and its locking semantics are unreliable there.
- The backup scope is `config.yaml` plus the entire `data/` directory.

## 5. `log`

Process-level logging and operation logs serve different purposes: operation logs record who did what and appear on the Logs → Operation tab; process logs are for runtime troubleshooting, can be written asynchronously to the database for the Logs → System tab, and are also emitted to stdout/files.

- `level`: minimum output level; supports `debug`, `info`, `warn`, and `error`. `debug` includes source locations and is intended for temporary troubleshooting.
- `format`: `text` for direct reading and Docker logs; `json` for log collection systems.
- `file`: log file path. When empty, output goes to stdout only; relative paths are based on the current working directory of the service process. Missing directories are created automatically (`0700`), and files are created with `0600`.
- `also_stdout`: whether to still write to stdout when a file is configured. Keep `true` for Docker deployments.
- `max_size_mb`: maximum size per file; when reached, the file is rotated with a timestamp. Supports `1–1024`.
- `max_backups`: number of historical files to keep; `0` means unlimited. The oldest files are deleted when the limit is exceeded.
- `max_age_days`: number of days to keep historical files; `0` means unlimited.
- `compress`: whether to compress rotated historical files as `.gz`.
- `access_log`: whether to record HTTP access logs. Logs contain only the method, route template, status code, latency, client IP, and similar fields; query parameters, request bodies, and authentication headers are not recorded. The secure entry is uniformly replaced with `:entry` in paths. Every response carries an `X-Request-ID`.
- `system.enabled`: whether to write to the database for viewing on the Logs → System tab.
- `system.level`: minimum level written to the database. It cannot be lower than `log.level`; otherwise lower-level logs are not generated.
- `system.access`: whether to also write HTTP access logs to the database. Defaults to `false` to avoid writing to SQLite on every request.
- `system.retention_days`: retention in days, range `1–365`.
- `system.max_rows`: maximum rows, range `100–1000000`; the oldest records are deleted when the limit is exceeded.
- `archive.enabled`: archive database logs before deleting or clearing them. Enabled by default; an archive failure blocks deletion so logs are never silently dropped.
- `archive.dir`: archive root; empty means `<data_dir>/archives/logs`. Created with `0700` permissions; files are `0600`.
- `archive.compress`: gzip compression; when disabled, archives are stored as `.ndjson`.
- `archive.retention_days`: archive file retention in days, range `0–3650`; `0` (default) keeps archives forever.
- `archive.max_total_mb`: archive directory quota in MB; `0` means unlimited. `quota_policy` controls what happens when it is exceeded.
- `archive.quota_policy`: `fail` (default, reject archiving and block deletion) or `prune_oldest` (delete the oldest archives). `prune_oldest` destroys audit material and must be configured explicitly.

Each archive consists of a `*.ndjson.gz` (or `.ndjson`) data file and a `*.manifest.json` containing row count, ID range, time range, trigger reason, operator, SHA-256, and file size. The archive directory is not included in the default ZIP backup and must be backed up separately.

Writes use a bounded asynchronous queue, so a busy database does not block business requests; when the queue is full, new logs are dropped. System logs may contain client IPs, domains, and error details; manage them as operational data.

Production recommendations: place log files on a dedicated disk or mounted volume; use `format: json` to integrate with a log platform; keep `access_log: true` for auditing; leave `system.access` at `false` unless short-term troubleshooting requires it.

## 6. `security`

- `entry`: path segment of the secure entry for the admin UI. Manual values must be 8–32 characters from letters, digits, underscores, or hyphens. When empty, a random 20-character combination of upper- and lowercase letters and digits is generated on first startup and saved to the database.
- `public_url`: external URL, for example `https://ssl.example.com` (without a path). Links in password reset emails are generated from this address to prevent Host header injection; when unset, only local loopback requests may derive the address from the Host header.
- `session_persistence`: session persistence, defaults to `true`. When `true`, service startup no longer clears the `sessions` table, and JWTs are signed with the fixed `jwt_secret`, so browsers can continue using existing login sessions after the backend process restarts; when `false`, every startup invalidates all previous logins.
- `jwt_secret`: signing key for login tokens. When empty, a random 32-byte value is generated and written back to the configuration file; manual values must be at least 32 characters, otherwise startup is refused.
- `salt`: encryption salt for sensitive fields in the database (DNS/deployment credentials, notification keys, private CA and certificate private keys, etc.). When empty, it is generated and written back; manual values must be at least 32 characters, otherwise startup is refused.
- `rate_limit.max_keys`: maximum number of keys kept by the in-memory token bucket, default `10000`. Each client IP, account, or operation consumes one key; when the limit is reached, the least recently used keys are evicted.
- `rate_limit.rules`: per-rule overrides. Each entry has `max` (allowed requests per window) and `window_seconds` (window length). Rules that are omitted use built-in defaults; omitting `window_seconds` keeps the default window; `max: 0` disables the rule. Unknown rule names fail startup so typos are caught early.

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

Security recommendations:

- Keep `entry` as a long random string; do not use guessable values such as `admin`, `ssl`, or `panel`.
- `public_url` must be a fixed HTTPS address and must not depend on the request Host.
- Choose `session_persistence` according to enterprise session policy. It is enabled by default, so a process restart does not force reauthentication; a longer session lifetime must be acceptable.
- Generate and store `jwt_secret` and `salt` with a password manager or configuration management system; do not commit them to version control.
- Changing `salt` makes existing encrypted credentials unusable. A migration plan is mandatory, and the value must never be changed directly.

## 7. `acme`

- `enabled`: whether to enable the built-in ACME server, enabled by default. No certificates are issued externally until an ACME endpoint is created; set to `false` to disable the protocol endpoint entirely.
- `addr`: dedicated listen address, for example `:14000`. When empty, the ACME protocol is mounted under the `/acme` prefix on the main port.
- `base_url`: ACME base URL that clients actually access, used to generate the directory, account resource URLs, and the CRL/OCSP addresses in certificates. Public deployments must configure a fixed address and must not rely on the request Host; when unset, it falls back to `security.public_url`, and if both are empty, it is derived from the request Host with a startup warning.
- `tls.cert` / `tls.key`: used when the dedicated ACME listener provides TLS directly; leave empty when a reverse proxy terminates TLS.
- `mtls.enabled` / `mtls.client_ca`: enable when client certificates are required, and specify the client CA certificate. When mTLS is enabled, the ACME TLS certificate and private key must also be configured.

Security recommendations:

- For public deployments, use a dedicated port and restrict source addresses.
- Every ACME endpoint must be configured with a domain allowlist; enable EAB in production.
- `base_url` must be a fixed public address to prevent Host header injection.
- If the ACME service is not needed, set `enabled: false` to reduce the attack surface.

## 8. `plugins`, `deploy_plugins`, `notify_plugins`

The three plugin kinds use `<data_dir>/plugins/dns`, `<data_dir>/plugins/deploy`, and `<data_dir>/plugins/notify` respectively; the subdirectories are never mixed.

### 8.1 DNS plugins

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

### 8.2 Deployment plugins

Deployment plugins receive certificate private keys and are disabled by default:

```yaml
deploy_plugins:
  enabled: false
  dir: ""
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64
```

### 8.3 Notification plugins

The main program only includes email notifications; notification plugins are enabled by default:

```yaml
notify_plugins:
  enabled: true
  dir: ""
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64
```

Plugin directories use `0700` permissions and helpers use `0755`, owned by the service account. In production, configure `public_key` and disable `auto_update`. For detailed governance, see [Plugin governance](plugins.md).

## 9. `upgrade`

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

## 10. Command-line flags

| Flag | Description |
| --- | --- |
| `-config` | Configuration file path; defaults to `config.yaml` in the current working directory |

```bash
./bin/lactssl -config /etc/lactssl/config.yaml
```

The secure entry can also be changed after startup under Settings → Secure entry, without a restart.

## 11. Reverse proxy requirements

### 11.1 Trust boundary

- Only the TCP peer addresses listed in `server.trusted_proxies` are trusted proxies. An empty list disables trust in all forwarding headers and is appropriate for direct deployments.
- `X-Forwarded-For`, `X-Real-IP`, `Forwarded`, and `CF-Connecting-IP` are client-controlled claims until the proxy is trusted. Auto-detection in Settings only provides hints; it never adds an address automatically.
- Do not configure `0.0.0.0/0`, `::/0`, or broad internal ranges. Any host inside a trusted range can claim to speak for clients.

### 11.2 Overwrite mode (recommended)

Have the proxy closest to LACTSSL replace the forwarding headers with the real client address:

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-Proto $scheme;
```

In this mode `trusted_proxies` only needs the nearest proxy address, and one resolution step yields the client IP. In multi-hop deployments, let the edge proxy recover the real client first and then have the nearest hop overwrite the headers; do not let an intermediate proxy overwrite XFF with its upstream proxy address.

### 11.3 Append mode

Nginx `$proxy_add_x_forwarded_for` preserves any client-supplied XFF and appends the current peer. LACTSSL walks the chain from right to left, skipping trusted entries:

- Single proxy: trusting only the Nginx address is sufficient.
- Multiple proxies: every proxy that can appear in XFF must be listed in `trusted_proxies`. If only the nearest hop is trusted, resolution stops at the upstream proxy IP.
- Never add the leftmost client address to the trusted list.

For example, with `client → CDN → Nginx → LACTSSL`, append mode produces `client, CDN edge IP`. Trusting only Nginx yields the CDN edge IP; trust the official CDN ranges as well, or make Nginx overwrite XFF.

### 11.4 CDN and cloud load balancers

- Trust `CF-Connecting-IP` only when the TCP peer belongs to Cloudflare's official ranges. Trusting the header merely because it exists allows direct clients to spoof it.
- Recommended: enable `real_ip` on your own Nginx with the official ranges, then overwrite XFF with `$remote_addr`. LACTSSL trusts only that Nginx.
- CDN edge IPs change; do not add them one by one. Use the official ranges or add your own proxy behind the CDN.
- An L4/TCP load balancer exposes no HTTP headers, so only the LB address is visible. Use PROXY protocol or place an HTTP-aware proxy behind it.

### 11.5 Docker and Kubernetes

- The peer seen inside a container is often a bridge gateway or Service forwarding address, not the real client.
- Prefer the exact proxy container/node IP. If it is dynamic, use a dedicated network and trust only the proxy address or the smallest possible CIDR.
- Do not trust `172.16.0.0/12` merely because it is private; other containers in the same network can forge forwarding headers.
- Verify whether the Ingress Controller sanitizes XFF; do not assume every Ingress behaves the same.

### 11.6 Apply and verify

1. Add proxy addresses to `trusted_proxies` and configure `security.public_url`.
2. When TLS terminates at the proxy, pass `X-Forwarded-Proto: https`.
3. Backup restore and plugin uploads need a larger body limit (for example, Nginx `client_max_body_size 256m`); proxy read timeouts must cover restore, plugin downloads, and deployments (for example, 600 seconds).
4. Restart after saving, then check `peer_ip`, `applied_trusted_proxies`, `effective_client_ip`, and `client_ip_source` under **Settings → Configuration file → Auto detect**. Before restart, `configured_trusted_proxies` and `applied_trusted_proxies` may differ.
5. Spoof test: send `X-Forwarded-For: 203.0.113.9` from an untrusted address; the server must still use the TCP peer. Access logs include `ip_source`, `ip_skipped`, and `ip_warnings` for troubleshooting.

### 11.7 Advanced proxy identity

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

## 12. Configuration validation and troubleshooting

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

## Related documentation

- [Installation](install.md)
- [Deployment architecture](architecture.md)
- [Security](security.md)
- [Operations](operations.md)
