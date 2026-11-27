# Installation and startup

These are production installation steps; for a first deployment, read [Quick start](getting-started.md) first. You can deploy directly with Docker Compose.

## Requirements

| Item | Requirement |
| --- | --- |
| Operating system | Linux amd64 / arm64; release packages are also available for macOS and Windows |
| Containers | Docker and Docker Compose v2 (recommended) |
| Building from source | Go 1.27+, Node.js 22.12+, npm |
| Host resources | See [Deployment architecture](architecture.md); 2 vCPU / 1 GB is a suggested start, and 1 vCPU / 1 GB suits a single-domain personal site |
| Network | Admin entry through an HTTPS reverse proxy; outbound access to DNS APIs, CAs, SMTP, and deployment targets |
| Domains | One admin domain and its TLS certificate; an optional dedicated domain for the ACME server |

No external database, Redis, or message queue is required; all state resides in `data/`.

!!! tip "Quick deployment"
    Start with Docker Compose, initialize, and enable built-in plugins. Panel-hosted sites (BaoTa, 1Panel) use built-in deployment targets, and automatic renewal needs a dedicated DNS sub-account authorized only for your domains.

## Option 1: Docker Compose (recommended)

### Prepare directories
```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
```

The included `docker-compose.yml` uses the GHCR image:
```yaml title="docker-compose.yml"
--8<-- "docker-compose.yml"
```

It uses 1Panel's external `1panel-network` by default. Without 1Panel, remove the service-level `networks` and the top-level `networks` block so Compose uses its default network, or create the network first:
```bash
docker network create 1panel-network
```

### Start
```bash
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```

!!! note "Image and network"
    The GHCR image is available from the first stable release; if `docker compose pull` reports it does not exist, build locally as described below. Port 8080 is published on `127.0.0.1` by default; change `ports` for LAN access and put a TLS reverse proxy in front. The image uses `latest` by default; pin a version by editing the image tag.

Mounts: `./config/config.yaml` for configuration, and `./data` for the SQLite database, plugins, logs, and upgrade backups. Inside the container these are always `/app/config/config.yaml` and `/app/data`. The entrypoint fixes ownership as root, then drops to the `lactssl` user; Compose enables a read-only root filesystem, `no-new-privileges`, a capability allowlist, and `pids_limit`.

### Pin the version
For production, pin a concrete version in `docker-compose.yml`:
```yaml
image: ghcr.io/axmishell/lactssl:1.2.3
```
Stable tags include `<version>`, `<major>.<minor>`, `<major>`, and `latest`; pre-releases use only the full version tag.

### Build the image locally
The Dockerfile copies a Linux binary built outside Docker; database backup and restore are native Go, so `pg_dump`/`mysqldump` are not needed. Prepare the frontend and binary first:
```bash
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
docker run -d --name lactssl \
  -p 127.0.0.1:8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  --restart unless-stopped \
  lactssl:1.2.3
```

## Option 2: Binary + systemd

### Install
```bash
sudo useradd --system --home /opt/lactssl --shell /usr/sbin/nologin lactssl
sudo mkdir -p /opt/lactssl/data
sudo cp lactssl /opt/lactssl/
sudo cp config.yaml.example /opt/lactssl/config.yaml
sudo chown -R lactssl:lactssl /opt/lactssl
sudo chmod 600 /opt/lactssl/config.yaml
sudo chmod 700 /opt/lactssl/data
```

!!! note "Native database backup"
    PostgreSQL/MySQL logical backup and restore run in LACTSSL's Go code; `pg_dump`, `pg_restore`, `mysqldump`, and `mysql` clients are not required. SQLite uses a `VACUUM INTO` snapshot.

### systemd unit
```ini title="/etc/systemd/system/lactssl.service"
[Unit]
Description=LACTSSL certificate manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lactssl
Group=lactssl
WorkingDirectory=/opt/lactssl
ExecStart=/opt/lactssl/lactssl -config /opt/lactssl/config.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/lactssl/data

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lactssl
sudo journalctl -u lactssl -f
```
Logs under `/opt/lactssl/data/logs/` are covered by `ReadWritePaths`; other directories need additional allowances. For ports 80/443, forward through a reverse proxy rather than granting privileges to the binary.

### One-click deployment script
`scripts/install.sh` downloads the Linux amd64/arm64 binary from GitHub Releases, creates the runtime user and data directory, installs a systemd service, and generates an Nginx reverse proxy configuration. TLS is off by default: the binary listens on `127.0.0.1:8080`, and Nginx listens on port 80.
```bash
sudo ./scripts/install.sh --port 8080 --domain ssl.example.com
```

Without cloning the repository:
```bash
curl -fsSL https://raw.githubusercontent.com/AXmishell/LACTSSL/main/scripts/install.sh \
  | sudo bash -s -- --port 8080 --domain ssl.example.com
```
Linux release archives also include `install.sh`; options:

| Option | Description |
| --- | --- |
| `--port PORT` | Binary listen port, default `8080`, must be >= 1024 |
| `--domain DOMAIN` | Nginx `server_name`, e.g. `ssl.example.com`; default `_` |
| `--version TAG` | Release tag such as `v1.2.3`; defaults to the latest stable release |
| `--no-nginx` | Deploy only the systemd service; do not install or modify Nginx |
| `--tls-cert` / `--tls-key` | Optional; enable Nginx HTTPS and redirect 80 to 443 |
| `--no-tls` | Disable TLS in an existing Nginx configuration (TLS is off by default) |
| `--read-write-path DIR` | Additional writable directory; repeatable, for local certificate deployment targets |
| `--skip-checksum` | Skip SHA-256 verification (not recommended) |
| `--release-public-key KEY` | Base64 Ed25519 release public key for `checksums.txt.sig` (or set `LACTSSL_RELEASE_PUBLIC_KEY`) |
| `--allow-unsigned` | Allow installation without a release signature (not recommended; controlled environments only) |

With HTTPS:
```bash
sudo ./scripts/install.sh \
  --domain ssl.example.com \
  --port 8080 \
  --tls-cert /etc/nginx/certs/ssl.example.com.pem \
  --tls-key  /etc/nginx/certs/ssl.example.com.key
```
The script verifies `checksums.txt` and, by default, its Ed25519 signature `checksums.txt.sig`; set `LACTSSL_RELEASE_PUBLIC_KEY` (the same key as `upgrade.program.public_key`) or pass `--release-public-key`, and use `--allow-unsigned` only in controlled environments. Re-running replaces the binary and restarts the service while preserving `config.yaml` and `data/`; `--port` also updates `server.addr`, `server.host`, and `server.port`. Omitted `--domain` / `--tls-cert` / `--tls-key` reuse the existing Nginx values; pass `--no-tls` to disable TLS explicitly. Nginx is installed through the package manager if missing (`--no-install-deps` disables this, `--no-nginx` skips Nginx). Because the unit uses `ProtectSystem=strict`, web UI self-upgrade may be unavailable; re-run this script instead. For local deployment targets such as `/etc/nginx`, pass `--read-write-path` (repeatable) and grant the `lactssl` user write permission.

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1", "::1"]
security:
  public_url: "http://ssl.example.com"
```
Nginx configuration is written to `/etc/nginx/conf.d/lactssl.conf` with `client_max_body_size 256m` and 600-second timeouts. Then initialize through the secure entry from the startup log.

### Build from source
```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```
`make build` builds the frontend into `internal/webui/dist`, then compiles `bin/lactssl` with the assets embedded. `make backend` builds only the backend, so provide the frontend artifacts yourself.

## First-time initialization
Startup logs print the secure entry and a one-time setup token:
```text
Secure entry: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
Setup token (valid for this startup only): ...
```
1. Open the page through the secure entry.
2. Create the administrator account when prompted.
3. To restore from a backup, import it under advanced features on `/install` and provide the setup token; the token becomes invalid immediately after successful initialization.

Before initialization, **Runtime configuration** on `/install` can modify allowlisted fields: listen address and trusted proxies, `public_url` and session persistence, ACME, logging, upgrade, plugin switches, and resource limits; restart to apply. Sensitive fields such as `jwt_secret`, `salt`, the secure entry, data directory, database and log files, plugin directories, and the signing public key must be edited in `config.yaml` on the server; after sign-in, the entry can be changed under **Settings → Secure entry**.

> The secure entry is not a security boundary. Production must use HTTPS, strong passwords, TOTP/passkeys, and network access controls.

## Reverse proxy and HTTPS
The program listens on HTTP only; terminate TLS at Nginx, Caddy, Traefik, a load balancer, or a WAF.

### Required configuration
```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

security:
  public_url: "https://ssl.example.com"
```

- `trusted_proxies` must include the proxy addresses, or client IP identification, rate limiting, and the `Secure` cookie decision are wrong. List the exact proxy IP; for a range, use the smallest CIDR, never broad ranges such as `10.0.0.0/8`.
- `public_url` is used for links such as password reset and must not depend on the request Host.
- If TLS terminates at the proxy, it must pass `X-Forwarded-Proto: https`.

### Nginx example
```nginx
server {
    listen 443 ssl;
    server_name ssl.example.com;

    ssl_certificate     /etc/nginx/certs/ssl.example.com.pem;
    ssl_certificate_key /etc/nginx/certs/ssl.example.com.key;
    ssl_protocols       TLSv1.2 TLSv1.3;

    client_max_body_size 256m;  # Backup restore and plugin uploads

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        # Overwrite mode: discard any client-supplied XFF and keep only the real client
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```
This **overwrite mode** means LACTSSL only needs to trust this Nginx. With `$proxy_add_x_forwarded_for` (append mode), single-proxy deployments still work; with multiple proxies, `trusted_proxies` must include every proxy that can appear in XFF. See the reverse proxy notes in [Configuration](configuration.md) for CDN, Docker/Kubernetes, and L4 load balancer differences. Add a source IP allowlist, VPN, zero-trust policy, or WAF at the proxy. Reverse proxy access logs can be an independent audit source, but do not log cookies or the `Authorization` header.

### ACME server (optional)
```yaml
acme:
  enabled: true
  addr: ":14000"
  base_url: "https://pki.example.com:14000"
  tls:
    cert: "/etc/lactssl/acme.crt"
    key: "/etc/lactssl/acme.key"
```
If `addr` is empty, ACME is mounted under `/acme` on the main port; a dedicated port is recommended for production. `base_url` must be the address clients actually use and should not depend on the request Host. For EAB, domain allowlists, mTLS, and source restrictions, see the [Security baseline](security.md).

## Minimum post-installation configuration

### Account security
- Enable TOTP or add a passkey immediately; set a controlled recovery email with multi-factor authentication, and make clear who owns the administrator account and complete a handover when that changes.
- Review and revoke unknown sessions under **Settings → Sessions**.

### Data and backup
```bash
# Stop writes before copying, or use encrypted backup in the UI
tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```
For production, use **System settings → Backup and restore** to generate an encrypted ZIP and store it on separate media. Keep the backup password separate from `config.yaml`.

### Plugins
Official DNS, deployment, and notification implementations are built in; no network installation or upload is needed. On the **Plugins** page, confirm the corresponding category is enabled, then enable the required DNS providers, deployment targets, or notification channels in the lists.

DNS providers are all disabled by default; deployment categories are on by default; notifications include email and common channels enabled by default. Third-party helpers can still be uploaded; see [Plugin governance](plugins.md).

### Verification
```bash
# Process and port
curl -I http://127.0.0.1:8080/          # Without the gate cookie this should return 404, which is expected
ss -lntp | grep 8080

# Authentication check with an API key
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/status
```

## Upgrade and uninstall
Back up before upgrading; see [Upgrade and change management](upgrade.md) for the steps. Docker:

```bash
docker compose pull
docker compose up -d
```
Binary:

```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo systemctl start lactssl
```
Export a backup before uninstalling. Deleting `data/` permanently loses the database, plugins, and encrypted credentials; deleting `config.yaml` loses `salt` and `jwt_secret`.

## Common installation issues

| Symptom | Cause | Resolution |
| --- | --- | --- |
| `docker compose up` reports a missing external network | Depends on `1panel-network` by default | Remove both `networks` sections, or create the network first |
| Root path returns 404, or cookies and rate limiting behave incorrectly | Not using the secure entry; trusted proxies not configured | Use the entry address from the startup log; configure `trusted_proxies` and forwarding headers |
| Backup or plugin upload fails; passkeys are unavailable | Proxy body limit; not HTTPS or accessed by IP | Adjust `client_max_body_size`; use an HTTPS domain or localhost |

For more troubleshooting, see [Monitoring and troubleshooting](troubleshooting.md).
