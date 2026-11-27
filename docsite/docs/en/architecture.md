# Deployment architecture
This document covers the deployment unit, network boundaries, capacity, and availability for developers deploying and maintaining LACTSSL; all recommendations assume the current version's **single-instance, SQLite single-writer** model.

## Deployment unit
LACTSSL is a self-contained Go binary:

- Frontend build artifacts are embedded, with no separate web server or Node runtime; data lives in local SQLite with no external database, cache, or message queue.
- Official DNS, deployment, and notification implementations are compiled in; uploaded third-party helpers run as one-shot subprocesses with files under the data directory.
- The only persistent inputs are `config.yaml` and the `data/` directory.

```text
/opt/lactssl/
├── lactssl                 # Binary
├── config.yaml             # Configuration and keys, 0600
└── data/                   # 0700
    ├── lactssl.db          # Main SQLite database
    ├── lactssl.db-wal      # WAL (runtime)
    ├── lactssl.db-shm      # Shared memory (runtime)
    ├── plugins/            # uploaded helpers (dns / deploy / notify)
    ├── plugin-icons/       # Plugin icon cache
    ├── upgrade-backups/    # Migration and program upgrade backups
    └── restore-pending/    # Backup packages pending restore
```

The backup boundary is `config.yaml` + `data/`. Backing up only the database loses `salt`, making encrypted credentials undecryptable.

## Reference topologies
### Single host with reverse proxy (recommended starting point)
```text
Internet / corporate intranet
        │ HTTPS 443
┌───────▼────────┐
│ Nginx / Caddy  │  TLS termination, source restrictions, access logs
│ / WAF / load balancer │
└───────┬────────┘
        │ HTTP 127.0.0.1:8080
┌───────▼─────────────────────────────┐
│ LACTSSL                             │
│  ├─ Admin console / API             │
│  ├─ Scheduler (checks due tasks every 30 seconds) │
│  ├─ Private CA / ACME service (optional) │
│  └─ SQLite + local plugins          │
└───┬──────────┬──────────┬───────────┘
    │          │          │
 DNS API   Deployment  Notification/email
           targets
```

Applies to most single-environment deployments with dozens to thousands of certificates.

Key points:

- The admin port has no TLS configuration; production must terminate TLS at a reverse proxy, ideally on the same host or subnet, with the backend port on loopback or the internal network only.
- The proxy must overwrite or correctly append `X-Forwarded-For` and pass `X-Forwarded-Proto`; register its exact IP or smallest CIDR in `server.trusted_proxies`, and every proxy in an append chain.
- Set `security.public_url` to the fixed external address; links such as password reset must not depend on the request Host.

### DMZ proxy with internal application
```text
Users ──HTTPS──► DMZ reverse proxy ──internal HTTP──► LACTSSL (application segment)
                                                       │
                                                       ├─► DNS API / public CA (outbound 443)
                                                       ├─► SSH / panel / CDN (per target)
                                                       └─► SMTP / webhook (per channel)
```

Applies to environments with higher security requirements, where the admin plane is not exposed directly on the internal network.

Key points:

- Allow only the management port from the proxy to LACTSSL, and whitelist outbound traffic by target; if domain allowlisting is not feasible, restrict destination ports and subnets.
- If the ACME server must be exposed separately in the DMZ, use a dedicated port rather than the management port.

### Internal PKI / ACME service
```text
Internal services / certbot / lego / acme.sh
        │ ACME v2 (HTTPS or mTLS)
┌───────▼─────────────┐
│ LACTSSL ACME endpoint│  Dedicated port :14000
│ Bound to private CA  │  Domain allowlist / EAB / challenge types
└───────┬─────────────┘
        │
   Private CA hierarchy
```

Applies to internal service, device, and test environment certificates.

Key points:

- Certificates are not issued until an ACME endpoint is created and enabled; every endpoint needs a domain allowlist.
- When reachable from the internet, enable EAB, restrict sources at the network layer, and use mTLS for higher assurance.
- `acme.base_url` must be the fixed address clients use, not the request Host; CRL/OCSP endpoints are organized per CA and should stay reachable after enrollment is disabled.

### Container platforms
```text
Ingress / Service
        │
┌───────▼────────┐
│ Pod: lactssl   │
│  /app/config   │── PVC or configuration volume
│  /app/data     │── PVC
└────────────────┘
```

Key points:

- The official image always reads `/app/config/config.yaml` and uses `/app/data`; the entrypoint fixes ownership, then drops to the `lactssl` user, with a read-only root filesystem and dropped capabilities under Compose.
- **Single replica**: do not let multiple pods write to the same SQLite volume concurrently.
- Self-upgrade inside a container does not change the image; upgrade production through image tags, and use `restart: unless-stopped` or the orchestrator equivalent so the service returns after a restore.

## Ports and network flows

| Direction | Port/protocol | Purpose | Recommendation |
| --- | --- | --- | --- |
| Inbound | 443/TCP | Admin console and API (through reverse proxy) | Open only to controlled sources |
| Inbound | 8080/TCP | LACTSSL main listener (default) | Listen only on loopback/internal network; accessed by the proxy |
| Inbound | Custom/TCP | Dedicated ACME listener (optional) | Dedicated port; restrict sources |
| Outbound | 443/TCP | DNS provider APIs, public ACME CAs, notifications, webhooks, panels, cloud CDNs | Allowlist by domain/subnet |
| Outbound | 22/TCP or custom | SSH/SFTP deployment | Allow only target hosts |
| Outbound | 21/990/TCP | FTP/FTPS deployment (plugin) | Prefer FTPS; restrict targets |
| Outbound | 25/465/587/TCP | SMTP notifications | Use an SMTP service |
| Outbound | Custom | Self-hosted Gotify, internal webhooks, monitoring targets | Allow on demand; be mindful of SSRF boundaries |

- The server validates outbound targets for SSRF and rejects cloud metadata, link-local, and reserved addresses; loopback and private networks remain allowed for internal integration.
- Deployment webhooks carry certificate private keys; public targets are forced to HTTPS, while loopback, private networks, `.local`, and `.internal` may use HTTP. If the proxy terminates TLS, pass `X-Forwarded-Proto` correctly, or the `Secure` cookie and rate-limit decisions will be wrong.

## Capacity planning
These are **reference starting points**, not capacity guarantees; actual capacity depends on certificate count, task frequency, log volume, plugin types, and backup strategy. Load-test with realistic traffic before production.

| Scale | Certificates/tasks | Suggested configuration | Notes |
| --- | --- | --- | --- |
| Small | < 100 certificates, few tasks | 2 vCPU / 1 GB / 10 GB | Keep memory headroom for concurrent helpers |
| Medium | 100–1,000, routine renewals | 2–4 vCPU / 2 GB / 20 GB | Enable log rotation and periodic cleanup |
| Larger | 1,000–5,000, frequent deployments | 4–8 vCPU / 4 GB / 50 GB+ | Monitor SQLite write amplification, backup windows, and plugin peaks |

- The main process idles at a few dozen MB; helpers are subprocesses limited by `memory_limit_mb` (128 MB by default), and concurrent tasks may start several at once, so leave headroom.
- Database growth comes mainly from certificates, task logs, operation logs, system logs, and ACME orders; operation and system logs share the `logs` table but are separated by `category`, with independent retention and clearing.
- Certificates, logs, and ACME diagnostics have cleanup entry points; system logs have `retention_days` and `max_rows`, `data/upgrade-backups/` keeps the five most recent migration snapshots, and program upgrades keep three old binaries. Do not write backups back to the data directory; reserve several times the database size.

## Availability and disaster recovery
### Availability boundaries of the current version
- **No built-in clustering or active-active**: SQLite is single-writer, and multiple instances on the same data directory cause lock contention or corruption.
- **No automatic failover** and **a single administrator account**: a proxy can probe liveness, but traffic must not switch to another instance writing the same data; losing the account requires a backup or password reset, so bind it to a role with proper handover.

### Single active instance with fast recovery (recommended)
```text
Primary instance ──scheduled encrypted backup──► Separate backup storage
   │
   └─ Failure ──► New host/new container ──restore backup──► Switch traffic
```

| Metric | Recommendation |
| --- | --- |
| RPO | Based on acceptable downtime, at least daily; hourly for critical environments |
| RTO | Estimated from "prepare host + restore backup + startup validation"; document in a runbook after drills |
| Backup retention | At least 7–30 days; longer for critical environments as needed |
| Drill frequency | At least one full restore drill per quarter |

For restore steps, see the [Operations guide](operations.md).

### Container and host-level high availability
Orchestration platforms can ensure **process** restarts but cannot solve the single-writer data problem:

- Fix the replica count at 1 and use persistent volumes so pod recreation does not lose data.
- Health-check with a TCP port probe or an authenticated `/api/status`; do not use pages that require the gate cookie.
- When a node fails, the platform reschedules the single replica; the data volume must be re-mountable.

### Multi-environment isolation
Production, staging, and test should use separate instances with separate `config.yaml`, `security.salt`, data directories, DNS sub-accounts, deployment credentials, and notification channels. Do not copy a production database to test and keep the same credentials; if copying is unavoidable, rotate them first.

## Monitoring and observability
LACTSSL has no Prometheus metrics endpoint, but monitoring can use these signals:

| Signal | Method | Recommendation |
| --- | --- | --- |
| Process liveness | TCP port probe | Every 30–60 seconds |
| Deep health | `GET /api/status` with an API key that has `system:read` | Every 1–5 minutes; check for 200 and response time |
| Task failures | Operation log filters or notification channels | Alert immediately on task/deployment failure |
| Certificate expiry | Dashboard, endpoint monitoring, task renewal status | Tiered reminders at 30/14/7 days before expiry |
| Log anomalies | System log keywords, reverse proxy 5xx | Forward to an external log system |
| Disk/backup | Host or platform monitoring | Alert when disk < 20% and on backup failure |

`/api/status` requires authentication, so anonymous probes can only determine whether the port is reachable.

## TLS and certificate boundaries
- The admin console's TLS comes from the reverse proxy; the main port does not configure TLS. Use an internal or public CA certificate with HSTS, TLS 1.2+, and weak cipher suites disabled; the dedicated ACME listener can terminate TLS directly (`acme.tls.cert/key`) or behind the proxy, and mTLS requires both the ACME TLS certificate and private key.
- Private keys are stored in the database encrypted with a key derived from `security.salt`; HSM/KMS custody is not provided. Backup packages contain encrypted credentials and key material, so treat them as confidential.

## Architecture decision checklist

| Decision | Options | Recommendation |
| --- | --- | --- |
| Deployment method | Docker / binary / orchestration platform | Prefer the image when a container platform exists; otherwise binary + systemd |
| TLS termination | Reverse proxy / dedicated ACME listener | Route the admin plane through the reverse proxy |
| Data storage | Local disk / persistent volume / network storage | Use single-writer mounts only; validate SQLite locking semantics for network storage |
| Availability | Single active + backup / platform restart | Do not use active-active; define RTO/RPO |
| Outbound policy | Allow all / domain allowlist / proxy | Allowlist by target in production; use a proxy when necessary |
| Plugin source | Built-in / third-party helper upload | Use built-in providers and verified helpers in production |
| Upgrade method | Image tag / binary replacement | Use images for containers; use release packages for binaries and retain rollback capability |
