# Administration
This document is for developers deploying and maintaining LACTSSL: accounts, permissions, keys, auditing, and day-to-day operations. The product uses a **single administrator + API key delegation** model.

## Operating model

### Three identity types

| Identity | Capabilities | Intended for |
| --- | --- | --- |
| Administrator account | After browser sign-in, can perform all operations, including exporting private keys, restoring backups, and changing settings | Platform owner |
| API key | Authorized by `resource:action`; suitable for automation and read-only integrations | CI/CD, health checks, CMDB, deployment scripts |
| ACME client account | Access to a specific ACME endpoint only, restricted by EAB/domain allowlist | certbot, lego, acme.sh, etc. |

The administrator account is a single point and should be held by a fixed owner. API keys cannot replace manual confirmation and cannot access account security endpoints (password changes, passkeys, session management, and similar).

### Suggested permission split

| Responsibility | Suggested owner | Uses administrator account |
| --- | --- | --- |
| Day-to-day certificate issuance and deployment | Platform/Operations | Yes, or a restricted API key |
| Custody of DNS/deployment credentials | Owner of the corresponding system | No; credentials are entered by an administrator and rotated as needed |
| Automation integration | Integration development | No; use a dedicated API key |
| Security audit | Security/Audit | No; use a read-only key with `logs:read` / `system_logs:read` |
| Backup restore | Operations lead | Yes; restore restarts the service |
| Plugins and upgrades | Platform owner | Yes; these are high-privilege changes |

## Initialization and handover

### Initialization
1. Sign in through the secure entry shown in the startup log.
2. Create the administrator with the one-time setup token, then enable TOTP or a passkey immediately and verify the recovery email.
3. Configure `security.public_url`, trusted proxies, and logging.
4. Create the first encrypted backup and confirm that the recovery password works.
5. Enable the required built-in plugins.

### Owner handover
- Transfer the administrator password, TOTP seed, or passkey devices, and control of the recovery email.
- Review the API key and credential inventories: purpose, permissions, expiration, owner, and rotation.
- Document backup locations, recovery passwords, the most recent restore drill record, outstanding tasks, monitoring items, and alert routing.

Do not send plaintext passwords, API keys, or backup passwords through chat tools; use a password manager or a sealed handover process.

## Account security

### Sign-in methods
- Passwords are stored as bcrypt hashes and must be at least 8 characters; for production, use random passwords of 16 characters or more.
- When TOTP is enabled, password sign-in still requires a one-time code, which cannot be reused within its validity window.
- A passkey (WebAuthn/Passkey) is an additional sign-in method, not a password replacement, and requires an HTTPS domain or localhost.
- Changing the password increments the token version and deletes all server-side sessions, API keys, and passkeys, invalidating all sign-ins; an email reset also disables TOTP and deletes passkeys and API keys, serving as recovery when the authenticator is lost.

### Sessions
- Session persistence is enabled by default, so sessions survive backend restarts; `security.session_persistence: false` clears them on every restart. Persistence extends session lifetime, so decide based on your deployment policy.
- Periodically review and revoke unknown devices under "Settings → Sessions"; a signed-in session equals administrator privileges, so do not stay signed in on shared browsers or untrusted devices.
- Keep core credential alerts enabled under "Settings → Security alerts", and enable new-device sign-ins and failed sign-in aggregation as needed; the default threshold is 5 failures within 10 minutes. Changing the email notifies both old and new addresses, and changing notification targets notifies the previous recipients and channels.
- The default hourly limit is 10 alerts for high-frequency sign-in/failure alerts only; critical credential changes and new-device sign-ins bypass it, and further events are combined into a digest. "Recent security events" shows per-target delivery status and errors and can export JSON.
- Security alerts are independent of certificate notifications; do not disable them by turning off certificate email. Plugin channels receive the sign-in IP, device, and account email, so select only trusted channels.

### Recovery paths
1. **Email reset**: for a forgotten password when the email account is under control; clears TOTP, passkeys, and API keys.
2. **Backup restore**: for a damaged account, database, or configuration; requires the backup decryption password and the setup token (for an uninitialized instance).

Document recovery procedures as a runbook and rehearse them regularly. Do not store the recovery password in the same location as backup files.

## API key governance
API keys are suited to machine identities, not people. Manage them on a "one key per system" basis.

### Role-to-permission mapping

| Consumer | Suggested permissions | Notes |
| --- | --- | --- |
| Read-only health checks/CMDB | `system:read`, `dashboard:read`, `certificates:read`, `tasks:read`, `monitors:read`, `deployments:read` | Do not grant any `*:write` |
| Certificate inventory sync | `certificates:read`, `tasks:read` | Do not grant private key permissions |
| CI/CD renewal trigger | `tasks:read`, `tasks:run`; add `deploy:execute` if deployment nodes are included | Scope to the required tasks |
| Certificate download and distribution | `certificates:read`, `certificates:key`, `deploy:execute` | Highly sensitive; restrict source and expiration |
| Log audit | `logs:read`, `system_logs:read` | Read-only key with a short expiration |
| Monitoring and alerting | `system:read`, `monitors:read` | For `/api/status` and monitor reads |
| Platform automation | Combine permissions as needed; avoid `*` | `*` is equivalent to all system capabilities |

### Lifecycle
1. Record the purpose, owner, permissions, and expiration.
2. Store the key in a password manager or secrets management system immediately after creation; never commit plaintext to a code repository.
3. Configure the key in the caller, verify least privilege, and rotate before expiration; the old token is invalidated immediately after rotation.
4. Disable or delete the key immediately when the system is decommissioned or personnel change.

### Protection requirements
- Do not place keys in logs, screenshots, tickets, or public repositories; transmit them only over HTTPS and avoid untrusted proxies.
- Apply source IP restrictions (prefer the built-in IP/CIDR allowlist, with the network or gateway layer as defense in depth).
- Periodically review keys with the `api_keys:read` permission to prevent privilege creep; if compromise is suspected, rotate or delete the key immediately and check operation logs for anomalous calls.

> API keys cannot change passwords, register passkeys, or sign out other sessions; these operations are available only to browser sign-in sessions.

## Certificate and CA governance

### Public CAs
- Use a dedicated email address for ACME accounts and maintain EAB credentials per CA with limited scope.
- Track CA rate limits and domain validation requirements; prepare a standby CA for critical domains.
- Generic ACME upstreams must use HTTPS (except for local loopback).

### Private CAs
- Use the root CA private key only to sign intermediate CAs, and set path length, Key Usage, Name Constraints, and validity for intermediates.
- After exporting the root CA private key, store it offline. The private key in the database remains protected by `security.salt`, but it is not an HSM.
- Before deleting a CA, confirm that there are no subordinate CAs or dependent certificates; existing certificates do not gain CRL/OCSP extensions automatically and must be reissued.

### ACME server
- Bind each endpoint to one private CA and configure a domain allowlist.
- EAB is enabled by default in production; when disabled, anyone who can reach the endpoint can create an account.
- For public endpoints, use a dedicated port, source restrictions, and mTLS (if supported by clients); periodically clean up expired orders and long-unused accounts. Deleting an account removes its orders but not issued certificates.

## Plugins and supply chain
- Official DNS, deployment, and notification implementations are built in; enable only the providers you need.
- Third-party helper upload is for extensions only; deployment helpers can read certificate private keys, so enable only trusted helpers.
- Set the plugin directory to `0700` and helpers to `0755`, owned by the service user; retain versions, installation times, and change records to support rollback.

For detailed procedures, see [Plugin governance](plugins.md).

## Audit and evidence

| Log | Content | Purpose |
| --- | --- | --- |
| Operation logs | Tasks, certificates, deployments, notifications, monitors, and settings changes | Operational audit, change tracking |
| System logs | Process logs for startup, HTTP, scheduling, plugins, ACME, and similar | Operational troubleshooting, security incident analysis |
| Process logs | stdout or `log.file` | Log platform ingestion, panic analysis |

- Retain operation logs for at least 90 days (the maximum export range); export them regularly when longer retention is required.
- System logs are retained for 7 days or 20,000 rows by default; adjust `log.system.retention_days` and `max_rows` as needed.
- Store reverse proxy access logs separately with the source IP, status code, and `X-Request-ID`; when exporting CSV, guard against formula injection, and never send logs containing credentials to third parties.
- Link critical changes (CA generation, private key export, backup restore, plugin installation, secure entry changes) to a change request or ticket.

## Routine operations checklist
### Daily
- [ ] Review alerts for failed tasks, failed deployments, and certificate anomalies.
- [ ] Check site monitors and confirm that backup jobs succeeded (if scheduled externally).

### Weekly
- [ ] Review certificates approaching expiry and automatic renewal status.
- [ ] Check system logs for errors, rate limiting, and anomalous sign-ins, and reconcile new or disabled API keys and credentials.

### Monthly
- [ ] Check disk usage, database size, WAL, log retention, and notification channel reachability.
- [ ] Check plugin versions and security advisories, and spot-check operation logs against change records.

### Quarterly
- [ ] Complete a backup restore drill.
- [ ] Review the administrator account, recovery email, TOTP/passkeys, and API key permissions and expiration; evaluate upgrade windows and rollback plans.

## Change management

| Operation | Risk | Suggested control |
| --- | --- | --- |
| Modify the secure entry | May lock out the administrative entry | Record the new entry and retain the `config.yaml` override method |
| Change `security.salt` | Existing encrypted credentials cannot be decrypted | Do not change it arbitrarily; a migration must accompany any change |
| Delete a private CA | Affects subordinate CAs and dependent certificates | Confirm dependencies first and export a backup |
| Restore a backup | Overwrites current data and restarts the service | Confirm before proceeding and choose an off-peak window |
| Upload a third-party deployment helper | Can read certificate private keys | Review the source, verify signatures, and apply least privilege |
| Upgrade the software | Data migration is irreversible | Back up, validate in staging, and prepare a rollback plan |
| Bulk cleanup of certificates/logs | Data cannot be recovered | Preview and export first, then confirm |
