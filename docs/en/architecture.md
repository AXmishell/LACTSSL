# Deployment architecture

This document is for platform architects and operations leads. It describes LACTSSL's deployment unit, network boundaries, capacity estimates, and availability design. All recommendations assume the current version's **single-instance, single-tenant, SQLite single-writer** model.

## 1. Deployment unit

LACTSSL is a self-contained Go binary:

- Frontend build artifacts are embedded in the binary; no separate web server or Node runtime is required.
- Data is stored in a local SQLite database; there is no dependency on an external database, cache, or message queue.
- DNS, deployment, and notification plugins run as one-shot subprocesses, with plugin files stored under the data directory.
- At runtime there are only two persistent inputs: `config.yaml` and the `data/` directory.

```text
/opt/lactssl/
├── lactssl                 # Binary
├── config.yaml             # Configuration and keys, 0600
└── data/                   # 0700
    ├── lactssl.db          # Main SQLite database
    ├── lactssl.db-wal      # WAL (runtime)
    ├── lactssl.db-shm      # Shared memory (runtime)
    ├── plugins/            # dns / deploy / notify helpers
    ├── plugin-icons/       # Plugin icon cache
    ├── upgrade-backups/    # Migration and program upgrade backups
    └── restore-pending/    # Backup packages pending restore
```

The backup boundary is `config.yaml` + `data/`. Backing up only the database loses `salt`, which makes encrypted credentials undecryptable.

## 2. Reference topologies

### 2.1 Single host with reverse proxy (recommended starting point)

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

Applies to: most enterprise single-environment deployments with dozens to thousands of certificates.

Key points:

- The main program does not provide TLS configuration for the admin port; production must terminate TLS at a reverse proxy.
- The reverse proxy and LACTSSL should be on the same host or subnet; the backend port should listen only on loopback or the internal network.
- The reverse proxy must pass `X-Forwarded-For` and `X-Forwarded-Proto`, and the proxy address must be registered in `server.trusted_proxies`.
- `security.public_url` must be set to the fixed external address; links such as password reset do not depend on the request Host.

### 2.2 DMZ proxy with internal application

```text
Users ──HTTPS──► DMZ reverse proxy ──internal HTTP──► LACTSSL (application segment)
                                                       │
                                                       ├─► DNS API / public CA (outbound 443)
                                                       ├─► SSH / panel / CDN (per target)
                                                       └─► SMTP / webhook (per channel)
```

Applies to: environments with higher security requirements, where the admin plane is not exposed directly on the business network.

Key points:

- On the firewall, allow only the management port from the proxy to LACTSSL.
- Whitelist LACTSSL's outbound access by target; if domain allowlisting is not feasible, at least restrict destination ports and subnets.
- If the ACME server must be exposed separately in the DMZ, use a dedicated listening port and do not reuse the management port.

### 2.3 Internal PKI / ACME service

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

Applies to: internal service certificates, device certificates, and test environment certificates.

Key points:

- Certificates are not issued until an ACME endpoint is created and enabled; every endpoint must have a domain allowlist.
- When reachable from the internet, enable EAB and restrict sources at the network layer; use mTLS for higher assurance.
- `acme.base_url` must be the fixed address that clients actually use; public deployments should not depend on the request Host.
- CRL/OCSP endpoints are organized per CA and should remain reachable after the ACME enrollment endpoint is disabled (for example, when clients need to check revocation).

### 2.4 Container platforms

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

- With the official image, the container always reads `/app/config/config.yaml` and uses `/app/data` as the data directory.
- The entrypoint script fixes ownership of the mounted directories, then drops privileges to run as the `lactssl` user; Compose uses a read-only root filesystem and drops capabilities by default.
- **Single replica**: Do not let multiple pods write to the same SQLite data volume concurrently.
- Self-upgrade inside a container does not change the image; a rebuilt container reverts to the image version. Upgrade production through image tags.
- Use `restart: unless-stopped` or the orchestrator's equivalent policy so that the service starts automatically after a backup restore.

## 3. Ports and network flows

| Direction | Port/protocol | Purpose | Recommendation |
| --- | --- | --- | --- |
| Inbound | 443/TCP | Admin console and API (through reverse proxy) | Open only to controlled sources |
| Inbound | 8080/TCP | LACTSSL main listener (default) | Listen only on loopback/internal network; accessed by the proxy |
| Inbound | Custom/TCP | Dedicated ACME listener (optional) | Dedicated port; restrict sources |
| Outbound | 443/TCP | DNS provider APIs, public ACME CAs, notifications, webhooks, panels, cloud CDNs | Allowlist by domain/subnet |
| Outbound | 22/TCP or custom | SSH/SFTP deployment | Allow only target hosts |
| Outbound | 21/990/TCP | FTP/FTPS deployment (plugin) | Prefer FTPS; restrict targets |
| Outbound | 25/465/587/TCP | SMTP notifications | Use the enterprise mail gateway |
| Outbound | Custom | Self-hosted Gotify, internal webhooks, monitoring targets | Allow on demand; be mindful of SSRF boundaries |

Notes:

- The server performs SSRF validation on outbound targets and rejects cloud metadata, link-local, and reserved network addresses; loopback and private networks are still allowed for internal integration.
- Deployment webhooks carry certificate private keys; public targets are forced to HTTPS, while loopback, private networks, `.local`, and `.internal` may use HTTP.
- If the reverse proxy terminates TLS, the backend is accessed over HTTP; `X-Forwarded-Proto` must be passed correctly, otherwise the `Secure` cookie decision and rate-limit identification will be incorrect.

## 4. Capacity planning

The following are **reference starting points**, not vendor commitments. Actual capacity depends on the number of certificates, task frequency, log volume, plugin types, and backup strategy. Load-test with realistic traffic before production.

| Scale | Certificates/tasks | Suggested configuration | Notes |
| --- | --- | --- | --- |
| Pilot/small | < 100 certificates, few tasks | 2 vCPU / 1 GB / 10 GB | Avoid installing large plugin packages at the same time |
| Medium | 100–1,000, routine renewals | 2–4 vCPU / 2 GB / 20 GB | Enable log rotation and periodic cleanup |
| Larger | 1,000–5,000, frequent deployments | 4–8 vCPU / 4 GB / 50 GB+ | Monitor SQLite write amplification, backup windows, and plugin peaks |

Memory considerations:

- The main process has an idle baseline of a few dozen MB; plugins are subprocesses started per invocation, and each helper is limited by `memory_limit_mb` (128 MB by default).
- Plugin installation/update extraction causes transient memory spikes, especially with large DNS packages; do not size container memory exactly to idle usage.
- Multiple concurrent tasks may start several helpers at once; leave headroom for peaks.

Storage considerations:

- Database growth mainly comes from certificates, task logs, operation logs, system logs, and ACME orders.
- Certificates, logs, and ACME diagnostic data all have cleanup entry points; system logs have `retention_days` and `max_rows`.
- `data/upgrade-backups/` retains the five most recent migration snapshots by default, and program upgrades retain three old binaries by default.
- Do not write backup files back to the same data directory; reserve backup space at several times the database size.

## 5. Availability and disaster recovery

### 5.1 Availability boundaries of the current version

- **No built-in clustering or active-active**. SQLite is single-writer; multiple instances mounting the same data directory concurrently cause lock contention or data corruption.
- **No automatic failover**. A reverse proxy can probe liveness, but traffic must not be automatically switched to another instance writing to the same data.
- **The single administrator account** is a single point of failure: if the account is lost, recovery requires a backup or password reset. Bind administrator responsibility to a role and ensure proper handover.

### 5.2 Single active instance with fast recovery (recommended)

```text
Primary instance ──scheduled encrypted backup──► Separate backup storage
   │
   └─ Failure ──► New host/new container ──restore backup──► Switch traffic
```

Suggested targets:

| Metric | Recommendation |
| --- | --- |
| RPO | Based on business tolerance, at least daily; hourly for critical environments |
| RTO | Estimated from "prepare host + restore backup + startup validation"; document in a runbook after drills |
| Backup retention | At least 7–30 days; longer for critical environments per compliance requirements |
| Drill frequency | At least one full restore drill per quarter |

For restore steps, see the [Operations guide](operations.md).

### 5.3 Container and host-level high availability

Orchestration platforms can ensure **process** restarts but cannot solve the single-writer data problem:

- Fix the replica count at 1.
- Use persistent volumes to avoid data loss when pods are recreated.
- Use a TCP port probe or an authenticated `/api/status` for health checks; do not use pages that require the gate cookie for unauthenticated probes.
- When a node fails, the platform reschedules the single replica; the data volume must be re-mountable.

### 5.4 Multi-environment isolation

Production, staging, and test should use separate instances:

- Separate `config.yaml`, separate `security.salt`, and separate data directories.
- Separate DNS sub-accounts and deployment credentials so test tasks cannot affect production.
- Separate notification channels so test alerts do not reach production on-call groups.
- Do not copy a production database to a test environment and continue using the same credentials; if copying is unavoidable, rotate credentials first.

## 6. Monitoring and observability

LACTSSL does not provide a Prometheus metrics endpoint, but monitoring can be built on the following signals:

| Signal | Method | Recommendation |
| --- | --- | --- |
| Process liveness | TCP port probe | Every 30–60 seconds |
| Deep health | `GET /api/status` with an API key that has `system:read` | Every 1–5 minutes; check for 200 and response time |
| Task failures | Operation log filters or notification channels | Alert immediately on task/deployment failure |
| Certificate expiry | Dashboard, endpoint monitoring, task renewal status | Tiered reminders at 30/14/7 days before expiry |
| Log anomalies | System log keywords, reverse proxy 5xx | Forward to the enterprise log platform |
| Disk/backup | Host or platform monitoring | Alert when disk < 20% and on backup failure |

`/api/status` requires authentication and cannot serve as an anonymous health check; anonymous probes can only determine whether the port is reachable.

## 7. TLS and certificate boundaries

- The admin console's TLS is provided by the reverse proxy; the LACTSSL main port does not configure TLS directly.
- The reverse proxy should use a certificate issued by the enterprise CA or a public CA, with HSTS enabled, TLS 1.2+, and weak cipher suites disabled.
- The dedicated ACME listener can provide TLS directly (`acme.tls.cert/key`) or have TLS terminated by the reverse proxy; when mTLS is enabled, both the ACME TLS certificate and private key must be configured.
- Private keys for certificates managed by LACTSSL are stored in the database, encrypted with a key derived from `security.salt`; HSM/KMS custody is not provided.
- The database and configuration in backup packages contain encrypted credentials and key material; treat them as confidential files.

## 8. Architecture decision checklist

During design review, make the following decisions explicit:

| Decision | Options | Recommendation |
| --- | --- | --- |
| Deployment method | Docker / binary / orchestration platform | Prefer the image when a container platform exists; otherwise binary + systemd |
| TLS termination | Reverse proxy / dedicated ACME listener | Route the admin plane through the reverse proxy |
| Data storage | Local disk / persistent volume / network storage | Use single-writer mounts only; validate SQLite locking semantics for network storage |
| Availability | Single active + backup / platform restart | Do not use active-active; define RTO/RPO |
| Outbound policy | Allow all / domain allowlist / proxy | Allowlist by target in production; use the enterprise proxy when necessary |
| Plugin source | Official repository / internal mirror / manual upload | Prefer an internal mirror with signature verification in production |
| Upgrade method | Image tag / binary replacement | Use images for containers; use release packages for binaries and retain rollback capability |

## Related documentation

- [Installation](install.md): Detailed deployment steps.
- [Configuration reference](configuration.md): Ports, proxy, logging, and ACME settings.
- [Security baseline](security.md): Network, identity, outbound, and supply chain controls.
- [Operations guide](operations.md): Backup, recovery, monitoring, and drills.
