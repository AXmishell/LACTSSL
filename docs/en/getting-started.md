# Getting started and rollout

This guide is for teams and individuals introducing LACTSSL for the first time. Its goal is to answer three questions as directly as possible: **whether it fits the scenario, which external dependencies are required, and how to move to production safely.** Individuals and small teams can skip straight to the quick path below instead of running the full enterprise evaluation.

## Quick path: individuals and small teams

Typical scenarios: personal blogs and sites, home labs, and internal services for a small team. All you need is **a Linux host that runs Docker (VPS, NAS, or small machine), a domain with DNS API credentials, and a deployment target**.

Four steps, usually within 30 minutes:

1. Start an instance with Docker as described in [Installation](install.md), and complete initialization through the secure entry printed in the startup logs.
2. Open the **Plugins** page, refresh the index, and install a DNS plugin; add deployment plugins for panel/CDN targets, and a notification plugin if you want alerts.
3. Create a DNS account → create a task under **Certificate request**, select a public CA and your domain → add a **Deploy certificate** node (local directory, SSH, or panel) → optionally add a notify node.
4. Run the task manually and confirm the certificate files were updated and the service reloaded; then observe one automatic renewal or trigger a renewal manually.

Minimum security baseline for personal use:

- Access the admin UI through an HTTPS domain and enable TOTP or a passkey.
- Keep the backup password separate from `config.yaml`; without `salt`, encrypted credentials in the database cannot be decrypted.
- Use a dedicated DNS sub-account that is authorized only for the domains you need.

> Enterprise adoption still requires the fit assessment, pilot, and production checklist in sections 1–7 below.

## 1. Confirm fit first

Typical conditions for adopting LACTSSL:

- Multiple domains or internal services require centrally managed TLS certificates.
- You want issuance, deployment, expiry monitoring, and notifications in a single auditable console.
- You accept a self-hosted single instance and can provide a Linux host, a data directory, and a backup policy.
- You have DNS provider API credentials, or you need to build an internal private CA / ACME service.
- Automation can integrate through API keys rather than requiring multi-user RBAC or enterprise SSO.

Cases where LACTSSL should not be the sole platform:

- You require multi-tenant isolation, organizational structure, approval workflows, or multi-user roles.
- You require active-active clustering, automatic cross-site failover, or HSM/KMS custody of private keys.
- You require the product to carry compliance certifications (such as MLPS or ISO) rather than completing the compliance assessment within your own organization.

These boundaries do not prevent LACTSSL from serving as a certificate automation component, but responsibilities must be defined explicitly during solution design.

## 2. Pre-evaluation checklist

| Item | What to confirm | Owner |
| --- | --- | --- |
| Host | Linux amd64/arm64; Docker Compose v2 or the ability to run a static binary; disk and backup capacity | Platform/operations |
| Domains and DNS | Pilot domains, DNS provider, API credentials that can create TXT records | Network/operations |
| Inbound access | Admin domain, reverse proxy, TLS certificate, source IP allowlist | Network/security |
| Outbound access | Reachability of DNS APIs, SMTP, webhooks, deployment targets, and monitoring targets | Network/security |
| Private keys and credentials | Custodians, rotation cycles, and least-privilege accounts for DNS/deployment/notification credentials | Security |
| Change and backup | Change windows, backup location, recovery owner, RPO/RTO targets | Operations |
| Integration | CI/CD, CMDB, and alerting platforms to integrate, plus API permissions | Integration development |

During the pilot, use **a dedicated domain, a dedicated DNS sub-account, and a dedicated notification channel**; do not reuse production administrator credentials.

## 3. 30-minute pilot

### 3.1 Start an instance

Docker is the most suitable option for evaluation:

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml && chmod 600 config/config.yaml
docker compose up -d
docker compose logs -f lactssl
```

The repository uses 1Panel's external `1panel-network` by default. If you are not using 1Panel, remove the service-level `networks` and the top-level `networks` block in `docker-compose.yml`, or run `docker network create 1panel-network` first. The GHCR image is available starting from the first stable release; if it is not published yet, build locally as described in [Installation](install.md). During the pilot you can use `http://127.0.0.1:8080`; passkeys (WebAuthn) require an HTTPS domain or localhost.

### 3.2 Complete initialization

Startup logs print two pieces of one-time information:

1. **Secure entry**: in the form `http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/`.
2. **Setup token**: printed before initialization and used for initialization operations such as creating the administrator and restoring backups.

Open the page through the secure entry and create the administrator account when prompted. The token becomes invalid immediately after initialization. If you lose the entry, set `security.entry` in `config.yaml` and restart to override it.

### 3.3 Install a minimal plugin set

The main program does not include DNS, deployment, or notification plugins. Open the **Plugins** page:

1. Confirm the plugin repository URL and click **Refresh index**.
2. Open the **Plugin market** and install at least one DNS plugin (for example, a commonly used DNS package) and one notification plugin.
3. If you need deployment targets other than SSH, install deployment plugins as well.
4. Enable the required providers, deployment targets, or notification channels in the corresponding lists.

Official plugins are built in, so offline environments need no extra index. Third-party helpers can still be uploaded; see [Plugin governance](plugins.md).

### 3.4 Run one complete workflow

Start with a private CA so the first workflow does not depend on public DNS:

1. Under **Private CA**, generate a root CA (ECDSA P-256, validity based on pilot needs).
2. Under **Certificate request**, create a task, select that private CA as the issuer, and enter a test domain.
3. Add a **Deploy certificate** node to the task workflow, select **Local directory**, and enter the certificate/private key paths and an optional reload command.
4. Add a **Notify** node, select an enabled channel, and set the trigger to **Always**.
5. Run the task manually and check whether the certificate, deployment record, and notification succeeded.

Then add a public workflow: create a DNS account → select a public CA → use a test domain with DNS-01 → deploy to a target → observe the automatic renewal time. Once both workflows succeed, move on to the pilot.

### 3.5 Verify key operational actions

| Action | How to verify | Pass criteria |
| --- | --- | --- |
| Backup | Settings → Backup and restore → Download encrypted backup | A ZIP is generated and its password decrypts it |
| Restore | Import the backup into an isolated instance | Accounts, certificates, tasks, and settings are complete |
| Logs | Filter/export operation logs and system logs | Initialization, task, and deployment records are visible |
| API | Create a read-only key and call `/api/certificates` | Returns 200; unauthorized endpoints return 403 |
| Notifications | Send a test notification | The target channel receives the message |
| Monitoring | Add an external endpoint | The certificate is read and its status displayed |

## 4. Suggested pilot scope

A pilot should not aim for full coverage; it should cover the paths most likely to surface problems:

- One or two real business domains, including one wildcard or multi-domain certificate.
- One real deployment target (choose one of SSH, panel, CDN, or webhook).
- One internal service scenario (private CA or ACME endpoint).
- One alert recipient channel, with a named on-call owner and escalation path.
- One automation client (CI/CD or an inspection script) to validate API key permissions.

A pilot of 1–2 weeks is recommended, with at least one automatic or manual renewal observed, confirming that deployment reload commands do not affect the business.

## 5. Production readiness checklist

### 5.1 Network and access

- [ ] The admin entry is exposed only through an HTTPS reverse proxy; the port behind the proxy is not directly reachable from the internet.
- [ ] `server.trusted_proxies` and `security.public_url` are configured.
- [ ] The secure entry is a long random string and does not use guessable paths such as `admin` or `ssl`.
- [ ] The admin entry enforces a source IP allowlist, VPN, or zero-trust access policy.
- [ ] If the ACME server is enabled, it uses a dedicated port with source restrictions; EAB is enabled by default.

### 5.2 Identity and keys

- [ ] Administrators use strong passwords and enable TOTP or passkeys.
- [ ] The recovery mailbox is controlled by the security team and has multi-factor authentication enabled.
- [ ] Each automation system has a dedicated API key, authorized with least privilege and an expiration time.
- [ ] DNS, deployment, and notification credentials use dedicated accounts, not personal accounts or global administrator keys.
- [ ] `config.yaml` has permissions `0600`, `data/` has permissions `0700`, and both are owned by the service user.

### 5.3 Data and recovery

- [ ] RPO/RTO are defined, and encrypted backups run at least daily.
- [ ] Backups are stored on separate media with controlled access, and the recovery password is kept separately.
- [ ] At least one restore drill has been completed, confirming that `salt` matches the database.
- [ ] Log retention, export scope, and storage capacity are confirmed.

### 5.4 Operations and change management

- [ ] The service owner, alert recipients, and change windows are defined.
- [ ] Process liveness checks and alerts for certificate expiry/task failure are configured.
- [ ] Image or binary versions are pinned; `latest` is not used for long-running production workloads.
- [ ] [Upgrade and change management](upgrade.md) has been reviewed, and migration and rollback steps are confirmed.
- [ ] High-priority items in the [Security baseline](security.md) have been completed.

## 6. Suggested responsibility split

| Responsibility | Suggested owner | Notes |
| --- | --- | --- |
| Host, network, and reverse proxy | Platform/operations | Ensure availability, inbound/outbound policy, and TLS termination |
| LACTSSL administrator account | Platform/operations lead | Single account; must be held by a role rather than an individual long term |
| DNS/deployment/notification credentials | Security or the owning system team | Dedicated accounts, regular rotation, least privilege |
| API keys | Integration development lead | Issued per system; record purpose, permissions, and expiration |
| Backup and restore drills | Operations | Keep the backup password and recovery procedure under separate custody |
| Compliance evidence | Security/audit | Use operation logs, system logs, and change records |

## 7. Common pilot failure causes

- **Treating the secure entry as a security boundary**: The entry is only an additional layer of protection; it does not replace HTTPS, strong passwords, and network access controls.
- **Using a personal DNS account for API credentials**: Automatic renewal fails after the person leaves or enables two-factor authentication.
- **Not validating deployment reload commands**: Certificate files are updated, but Nginx or the panel is not reloaded, so the business keeps using the old certificate.
- **Backing up only the database, not `config.yaml`**: Without `salt`, encrypted credentials in the database cannot be decrypted.
- **Sharing one full-privilege API key across all automation**: If leaked, an attacker can issue certificates, deploy them, and export private keys.
- **Using self-upgrade inside a container**: A rebuilt container reverts to the image version; upgrade production through image tags.

## Next steps

- [Deployment architecture](architecture.md): Define the production topology, ports, and capacity.
- [Administration console](administration.md): Implement accounts, keys, audit, and day-to-day responsibilities.
- [Security baseline](security.md): Complete the pre-production security controls.
- [API integration](api.md): Integrate CI/CD, inspection, or CMDB.
