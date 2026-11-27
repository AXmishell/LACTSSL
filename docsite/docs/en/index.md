---
hide:
  - navigation
  - toc
---

<section class="hero" markdown>
<div class="hero-badge">Self-hosted · single instance · open source</div>

# LACTSSL

<p class="hero-sub">Certificate issuance, auto-renewal, deployment, and expiry monitoring in one self-hosted console. Developers can run the full workflow on a single small host.</p>

[Quick start](getting-started.md){ .md-button .md-button--primary }
[Install](install.md){ .md-button }
</section>

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-autorenew: Automatic renewal and deployment
Request, renew, deploy, and monitor ACME certificates in one place instead of scattered scripts and spreadsheets.
</div>

<div class="feature-card" markdown>
### :material-server-network: Private CA and internal ACME
Generate or import a root / intermediate CA and expose a standard ACME v2 endpoint for certbot, lego, and acme.sh.
</div>

<div class="feature-card" markdown>
### :material-file-export-outline: Multi-target delivery
Local directories and SSH/SFTP are built in; FTP, Webhook, cloud CDNs, BaoTa, 1Panel, and SafeLine WAF are added through deployment plugins.
</div>

<div class="feature-card" markdown>
### :material-monitor-eye: Endpoint expiry monitoring
Read external site certificates on a schedule, verify the chain and hostname, and notify on errors or expiry.
</div>

<div class="feature-card" markdown>
### :material-shield-check-outline: Auditable and self-hosted
Single binary, SQLite, no external dependencies; secure entry, TOTP/passkeys, field encryption, operation logs, and encrypted backups.
</div>

<div class="feature-card" markdown>
### :material-bell-ring-outline: Notifications and integration
Email is built in, common channels ship with the app, and the REST API with scoped API keys supports CI/CD.
</div>

</div>

## Documentation

<div class="doc-grid" markdown>

<div class="doc-card" markdown>
[:material-rocket-launch-outline: Quick start](getting-started.md)
<span>Install, initialize, and issue your first certificate.</span>
</div>

<div class="doc-card" markdown>
[:material-download-outline: Install](install.md)
<span>Docker, binary, reverse proxy, and HTTPS.</span>
</div>

<div class="doc-card" markdown>
[:material-format-list-checks: Capabilities](features.md)
<span>Certificates, tasks, private CA, ACME service, and monitoring.</span>
</div>

<div class="doc-card" markdown>
[:material-server-network: Architecture](architecture.md)
<span>Topology, ports, capacity, and availability.</span>
</div>

<div class="doc-card" markdown>
[:material-shield-lock-outline: Security baseline](security.md)
<span>Identity, encryption, auditing, and the go-live checklist.</span>
</div>

<div class="doc-card" markdown>
[:material-cog-outline: Configuration](configuration.md)
<span>Every field, reverse proxy notes, and plugin settings.</span>
</div>

</div>

## Interface

[![LACTSSL dashboard](assets/images/dashboard.png){ .doc-screenshot .doc-screenshot--home }](assets/images/dashboard.png){ target="_blank" rel="noreferrer" }

<p class="home-note">LACTSSL is a self-hosted tool with one administrator and one instance. It does not provide multi-user RBAC, SSO, clustering, or HSM. See [Capabilities](features.md) for the full scope.</p>
