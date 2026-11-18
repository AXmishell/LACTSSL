---
hide:
  - navigation
  - toc
---

<section class="hero" markdown>
<div class="hero-badge">For enterprise teams and individuals / small teams · Self-hosted · Single-tenant</div>

# LACTSSL

<p class="hero-sub">Consolidate public certificate issuance, certificate deployment, expiry monitoring, and internal PKI into a single auditable, self-hosted console. Enterprises use it to govern certificates across business lines; individual developers, site owners, and small teams can run fully automated renewal and deployment on a single small host.</p>

[Get started](getting-started.md){ .md-button .md-button--primary }
[Deployment architecture](architecture.md){ .md-button }
[GitHub repository](https://github.com/AXmishell/LACTSSL){ .md-button target="_blank" rel="noreferrer" }
</section>

[![LACTSSL dashboard](assets/images/dashboard.png){ .doc-screenshot .doc-screenshot--home }](assets/images/dashboard.png){ target="_blank" rel="noreferrer" }

## Why choose LACTSSL

One product serves both enterprise certificate governance and personal sites, small teams, and home labs.

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-certificate-outline: Reduced certificate expiry risk
Centrally manage ACME certificate requests, renewals, deployment, and expiry monitoring, reducing the scattered scripts and manual spreadsheets behind outages caused by expired certificates. Enterprises can cover multiple business lines; a personal site only needs one domain.
</div>

<div class="feature-card" markdown>
### :material-server-network: Internal PKI and ACME service
Issue certificates for internal services with a private CA, and expose a standard ACME v2 endpoint to clients such as certbot, lego, and acme.sh; home labs and development environments can run their own local CA too.
</div>

<div class="feature-card" markdown>
### :material-file-export-outline: Multi-target delivery
Local directories and SSH/SFTP are built in; FTP, Webhook, major cloud CDNs, BaoTa, 1Panel, SafeLine WAF, and other targets can be added on demand through deployment plugins, covering everything from cloud hosts to panel-managed sites.
</div>

<div class="feature-card" markdown>
### :material-shield-check-outline: Auditable and self-hosted
Single binary, SQLite, no external dependencies; secure entry, TOTP/passkeys, field-level encryption, operation logs, and encrypted backups keep data within the enterprise boundary — or simply on your own server. The footprint is small enough to run long-term on a modest host.
</div>

</div>

## Use cases

| Scenario | Audience | Typical problem | How LACTSSL addresses it |
| --- | --- | --- | --- |
| Personal sites, blogs, and side projects | Individuals / small teams | Renewal reminders rely on memory, and renewal scripts are scattered across machines | One task handles request, renewal, and deployment; it runs automatically before expiry and keeps an audit trail |
| Home labs and internal services | Individuals / small teams | Self-signed certificates trigger browser warnings, and client trust and rotation are painful | Hierarchical issuance from a private CA, plus a standard ACME endpoint |
| Certificate governance across multiple domains and business units | Enterprises | Certificates are scattered across personal accounts, scripts, and spreadsheets, and renewal failures go unnoticed | Unified task orchestration, automatic renewal, deployment records, and an expiry dashboard |
| TLS for internal services | Enterprises | Internal services use self-signed certificates, making client trust and rotation difficult | Hierarchical issuance from a private CA, plus a standard ACME endpoint |
| Automated certificate delivery | Both | After issuance, certificates still have to be uploaded manually to panels, CDNs, or servers | Local, SSH, and plugin deployments orchestrated within a task, with optional reload commands |
| Security and audit requirements | Enterprises | Unable to answer who issued what, when, and where it was deployed | Operation logs, system logs, scoped API keys, and encrypted backups |

## Scope and boundaries

Both enterprise and individual adopters should confirm product boundaries first. The following reflects the actual scope of the current version:

| Capability | Current version | Impact on individuals / small teams |
| --- | --- | --- |
| Deployment model | Self-hosted single instance, single-tenant | Runs on a small host, VPS, or NAS |
| Administrative accounts | A single administrator account; no multi-user, roles, or organizational structure | Shared by an individual or a small team |
| Machine identity | API keys authorized by "resource:action", with optional expiration and enable/disable | Issue a scoped key for CI/CD or scripts |
| Identity federation | No LDAP / OIDC / SAML / SCIM; cannot connect to enterprise SSO | Local sign-in only, no dependency on a corporate identity system |
| High availability | No built-in clustering or active-active; SQLite single-writer; for production, use a single active instance with backups and fast recovery | Regular backups are enough for personal use |
| Key custody | Private keys are stored encrypted in the local database; no HSM / KMS custody | No extra key service required |
| Monitoring integration | Operation logs, system logs, and an authentication API; no Prometheus metrics endpoint | Built-in notifications are sufficient |
| Compliance certifications | The project itself does not carry certifications such as China's Multi-Level Protection Scheme (MLPS) or ISO 27001; control items and audit evidence can be provided | Not relevant for personal use |
| Support model | The project uses the MIT license; commercial support, SLAs, and managed services require a separate agreement | Community docs and issue support |

> If an enterprise requires multi-user RBAC, SSO, multi-tenancy, or active-active clustering, these must be provided by a higher-level platform or organizational process. LACTSSL should not be treated as a complete IAM/PKI platform.

## Reference architecture

```text
                   Enterprise users / individuals / automation systems
                           │ HTTPS
                    ┌──────▼──────┐
                    │ Reverse proxy/WAF │  TLS termination, access control, audit
                    └──────┬──────┘
                           │ HTTP (internal network)
                    ┌──────▼──────────────────────────────┐
                    │ LACTSSL single instance              │
                    │ Admin console + API + scheduler      │
                    │ Private CA + ACME service (optional) │
                    │ SQLite + local encryption key        │
                    └──┬──────────┬──────────┬─────────────┘
                       │          │          │
                 DNS API    Deployment   Notification
               (public CA)    targets      channels
                            (SSH/panel/CDN) (SMTP/Webhook)
```

For detailed topology, ports, capacity, and availability design, see [Deployment architecture](architecture.md).

## Reading guide by role

| Role | Focus | Recommended reading |
| --- | --- | --- |
| Individual developers / site owners / small teams | Getting it running, connecting a domain, automatic renewal and deployment | [Installation](install.md), [DNS plugins](dns-plugins.md), [Deployment plugins](deploy-plugins.md) |
| Business owners / solution evaluation | Value, boundaries, pilot duration, acceptance criteria | [Getting started and rollout](getting-started.md), [Features](features.md) |
| Platform / operations leads | Deployment topology, backup and recovery, upgrades, alerting | [Deployment architecture](architecture.md), [Operations guide](operations.md) |
| Security / compliance | Identity, encryption, audit, supply chain, control items | [Security baseline](security.md), [Administration console](administration.md) |
| Integration / automation developers | API keys, permissions, CI/CD, error handling | [API integration](api.md), [Extension development](plugin-development.md) |

## Recommended rollout path

**Individuals / small teams**: start an instance with Docker → install DNS and deployment plugins → create one domain task → verify renewal and notifications. This usually takes half a day; the full enterprise pilot process is not required.

**Enterprise teams**:

1. **Evaluation (0.5–1 day)**: Start a pilot instance with Docker and complete one test certificate and one local deployment.
2. **Pilot (1–2 weeks)**: Onboard one or two real domains or one internal CA scenario; validate DNS credentials, deployment targets, and the notification path.
3. **Production (per change window)**: Assign owners for the reverse proxy, backups, monitoring, and API keys; complete the security baseline before cutover.
4. **Operations (ongoing)**: Review expiry, backups, logs, and plugin versions monthly; run recovery drills quarterly.

For detailed steps and the acceptance checklist, see [Getting started and rollout](getting-started.md).

## Capabilities at a glance

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-autorenew: ACME automation
DNS-01 validation with support for single-domain, multi-domain, and wildcard certificates; automatic renewal before expiry, with retries and an audit trail on failure.
</div>

<div class="feature-card" markdown>
### :material-sitemap-outline: Task orchestration
Request → deploy (to one or more targets) → notify; nodes can be enabled, disabled, and reordered, and the same credential can be reused across tasks.
</div>

<div class="feature-card" markdown>
### :material-lock-outline: Private CA
Generate or import root and intermediate CAs, and configure path length, Key Usage, Name Constraints, CRL/OCSP URLs, and other PKI parameters.
</div>

<div class="feature-card" markdown>
### :material-api: Standard ACME server
Bind private CAs to endpoints; supports EAB, domain allowlists, HTTP-01/DNS-01/TLS-ALPN-01, mTLS, and order rate limiting.
</div>

<div class="feature-card" markdown>
### :material-monitor-eye: Endpoint certificate monitoring
Periodically read certificates from external endpoints, validate the certificate chain and hostname, and notify automatically on anomalies or upcoming expiry.
</div>

<div class="feature-card" markdown>
### :material-bell-ring-outline: Notifications and audit
Email is built in; 20+ channels are provided by plugins. Operation logs and system logs can be filtered by condition and exported to CSV/JSON.
</div>

</div>

## Documentation

<div class="doc-grid" markdown>

<div class="doc-card" markdown>
[:material-rocket-launch-outline: Getting started and rollout](getting-started.md)
<span>Quick start for individuals, plus enterprise piloting, acceptance, cutover, and responsibilities.</span>
</div>

<div class="doc-card" markdown>
[:material-server-network: Deployment architecture](architecture.md)
<span>Topology, ports, capacity, availability, and network boundaries.</span>
</div>

<div class="doc-card" markdown>
[:material-shield-lock-outline: Security baseline](security.md)
<span>Identity, encryption, audit, outbound protection, and control mapping.</span>
</div>

<div class="doc-card" markdown>
[:material-wrench-outline: Operations guide](operations.md)
<span>Backup and recovery, monitoring, change management, incident response, and drills.</span>
</div>

<div class="doc-card" markdown>
[:material-code-braces: API integration](api.md)
<span>Key governance, permission matrix, and automation examples.</span>
</div>

<div class="doc-card" markdown>
[:material-lifebuoy: Monitoring and troubleshooting](troubleshooting.md)
<span>Common failures, diagnostic paths, and escalation information.</span>
</div>

</div>

## Technology stack

| Layer | Implementation |
| --- | --- |
| Backend | Go + Gin + SQLite |
| Frontend | Vue 3 + Vite + Element Plus (embedded in the binary after build) |
| ACME | lego / RFC 8555 |
| Storage | SQLite, sensitive fields encrypted with AES-256-GCM |
| Deployment | Single binary, no external database or message queue |
