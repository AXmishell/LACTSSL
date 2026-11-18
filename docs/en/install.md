# Installation and startup

This document provides production-ready installation steps. For a first evaluation, read [Evaluation and rollout](getting-started.md) first to confirm the scenario and acceptance criteria, then deploy using this guide.

## 1. Requirements

| Item | Requirement |
| --- | --- |
| Operating system | Linux amd64 / arm64; release packages are also available for FreeBSD, macOS, and Windows |
| Containers | Docker and Docker Compose v2 (recommended) |
| Building from source | Go 1.27+, Node.js 22.12+, npm |
| Host resources | See [Deployment architecture](architecture.md) for capacity planning; 2 vCPU / 1 GB is a suggested starting point |
| Network | Admin entry through an HTTPS reverse proxy; outbound access to DNS APIs, CAs, SMTP, and deployment targets |
| Domains | One admin domain and its TLS certificate; an optional dedicated domain for the ACME server |

The main program does not require an external database, Redis, or a message queue. All state resides in the `data/` directory.

## 2. Option 1: Docker Compose (recommended)

### 2.1 Prepare directories

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
```

The `docker-compose.yml` included in the repository uses the GHCR image by default:

```yaml title="docker-compose.yml"
--8<-- "docker-compose.yml"
```

If you are not using 1Panel, change `1panel-network` at the end of the file to the default network; otherwise `docker compose up` fails because the external network does not exist:

```yaml
# Remove the networks configuration to let Compose use the default network
```

### 2.2 Start

```bash
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```

Default mounts:

- `./config/config.yaml`: configuration file
- `./data`: SQLite database, plugins, logs, and upgrade backups

Inside the container, `/app/config/config.yaml` is always read as the configuration file and `/app/data` is the data directory. The entrypoint script runs as root to fix ownership of the mounted directories, then drops privileges to run as the `lactssl` user. Compose enables a read-only root filesystem, `no-new-privileges`, a capability allowlist, and `pids_limit` by default.

### 2.3 Pin the version

Do not run `latest` long term. Pin the version through an environment variable or by editing the image tag directly:

```bash
LACTSSL_VERSION=1.2.3 docker compose up -d
```

Stable tags include `<version>`, `<major>.<minor>`, `<major>`, and `latest`; pre-release versions are published only with the full version tag.

### 2.4 Build the image locally

Build locally after modifying the source:

```bash
LACTSSL_VERSION=1.2.3 docker compose up -d --build
```

Or build manually:

```bash
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
docker run -d --name lactssl \
  -p 127.0.0.1:8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  --restart unless-stopped \
  lactssl:1.2.3
```

## 3. Option 2: Binary + systemd

### 3.1 Install

```bash
sudo useradd --system --home /opt/lactssl --shell /usr/sbin/nologin lactssl
sudo mkdir -p /opt/lactssl/data
sudo cp lactssl /opt/lactssl/
sudo cp config.yaml.example /opt/lactssl/config.yaml
sudo chown -R lactssl:lactssl /opt/lactssl
sudo chmod 600 /opt/lactssl/config.yaml
sudo chmod 700 /opt/lactssl/data
```

### 3.2 systemd unit

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

If logs are written to `/opt/lactssl/data/logs/`, the `ReadWritePaths` above already covers it; other directories require additional allowances. When using privileged ports such as 80/443, forward traffic through a reverse proxy rather than granting privileges to the binary.

### 3.3 Build from source

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```

`make build` first builds the frontend and copies it to `internal/webui/dist`, then compiles `bin/lactssl` with the frontend assets embedded. To build only the backend, use `make backend`, but you must provide the frontend build artifacts yourself.

## 4. First-time initialization

Startup logs print the secure entry and a one-time setup token:

```text
Secure entry: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
Setup token (valid for this startup only): ...
```

1. Open the page through the secure entry.
2. Create the administrator account when prompted.
3. To restore from a backup, import it under advanced features on `/install` and provide the setup token.
4. The token becomes invalid immediately after successful initialization.

Before initialization, the **Runtime configuration** section on `/install` can modify common allowlisted fields: listen address and trusted reverse proxies, `public_url` and session persistence, ACME (dedicated listener, external URL, TLS/mTLS), logging (rotation, access log, system log), upgrade and program self-upgrade, DNS/deployment/notification plugin switches, and the plugin repository and resource limits. Restart the service to apply changes.

Sensitive or high-risk fields such as `jwt_secret`, `salt`, the secure entry, the data directory, database and log files, plugin directories, and the program repository and signing public key are not exposed on the install page; edit `config.yaml` on the server directly. After initialization and sign-in, the secure entry can be changed under **Settings → Secure entry**.

> The secure entry is not a security boundary. Production must use HTTPS, strong passwords, TOTP/passkeys, and network access controls.

## 5. Reverse proxy and HTTPS

The main program listens on HTTP only; production terminates TLS at Nginx, Caddy, Traefik, a load balancer, or a WAF.

### 5.1 Required configuration

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1", "10.0.0.0/8"]

security:
  public_url: "https://ssl.example.com"
```

- `trusted_proxies` must include the proxy addresses; otherwise client IP identification and rate limiting are inaccurate, and the `Secure` cookie decision may also be wrong.
- `public_url` is used for links such as password reset and must not depend on the request Host.
- If TLS terminates at the proxy, ensure the proxy passes `X-Forwarded-Proto: https`.

### 5.2 Nginx example

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
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

We recommend adding a source IP allowlist, VPN, zero-trust access policy, or WAF at the proxy layer. Reverse proxy access logs can serve as an independent audit source, but do not log cookies or the `Authorization` header.

### 5.3 ACME server (optional)

To provide ACME to internal clients:

```yaml
acme:
  enabled: true
  addr: ":14000"
  base_url: "https://pki.example.com:14000"
  tls:
    cert: "/etc/lactssl/acme.crt"
    key: "/etc/lactssl/acme.key"
```

- If `addr` is empty, ACME is mounted under the `/acme` prefix on the main port; a dedicated port is recommended for production.
- `base_url` must be the address that clients actually use; public deployments should not depend on the request Host.
- For EAB, domain allowlists, mTLS, and source restrictions, see the [Security baseline](security.md).

## 6. Minimum post-installation configuration

### 6.1 Account security

- Enable TOTP or add a passkey immediately.
- Set a controlled recovery email address; the mailbox itself must have multi-factor authentication enabled.
- Make it explicit that the administrator account is held by a role, with handover on departure or role change.
- Review and revoke unknown sessions under **Settings → Sessions**.

### 6.2 Data and backup

```bash
# Stop writes before copying, or use encrypted backup in the UI
tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

For production, use **System settings → Backup and restore** in the UI to generate an encrypted ZIP and store backups on separate media. Keep the backup password separate from `config.yaml`.

### 6.3 Plugins

The main program does not include any DNS, deployment, or notification plugins:

1. Open the **Plugins** page, confirm the repository URL, and click **Refresh index**.
2. Install the required plugins from the **Plugin market**.
3. Enable providers, deployment targets, or notification channels in the corresponding lists.
4. For production, use an internal mirror with pinned versions; see [Plugin governance](plugins.md).

Without plugins installed:

- The DNS provider list is empty, so DNS accounts cannot be created.
- Deployment targets include only local directories and SSH.
- Notifications include only email.

### 6.4 Verification

```bash
# Process and port
curl -I http://127.0.0.1:8080/          # Without the gate cookie this should return 404, which is expected
ss -lntp | grep 8080

# Authentication check with an API key
curl -H "Authorization: Bearer lactssl_xxx" \
  https://ssl.example.com/api/status
```

Then complete the following in the UI:

- Create and test a DNS account.
- Create and run a test task.
- Send a test notification.
- Add an endpoint monitor.
- Download and decrypt a backup.

## 7. Upgrade and uninstall

Back up before upgrading; see [Upgrade and change management](upgrade.md) for the steps.

Docker:

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

## 8. Common installation issues

| Symptom | Cause | Resolution |
| --- | --- | --- |
| `docker compose up` reports that an external network does not exist | Depends on `1panel-network` by default | Switch to the default network or create that network first |
| Accessing the root path returns 404 | Not using the secure entry | Use the entry address from the startup log |
| Cookies behave incorrectly or rate limiting is inaccurate after login | Trusted proxies are not configured | Configure `trusted_proxies` and pass forwarding headers |
| Password reset links point to the wrong domain | `public_url` is not configured | Set a fixed external address |
| Backup or plugin upload fails | Proxy request body limit | Adjust `client_max_body_size` |
| Passkeys are unavailable | Not HTTPS, or accessed by IP | Use an HTTPS domain or localhost |
| The container reverts to an older version after restart | Self-upgrade inside the container | Upgrade through image tags |

For more troubleshooting, see [Monitoring and troubleshooting](troubleshooting.md).
