# Administration

This document is intended for LACTSSL administrators and describes how accounts, permissions, keys, auditing, and day-to-day operations are organized. The product currently uses a **single administrator + API key delegation** model; enterprises should design roles and processes around this model.

## 1. Operating model

### 1.1 Three identity types

| Identity | Capabilities | Intended for |
| --- | --- | --- |
| Administrator account | After browser sign-in, can perform all operations, including exporting private keys, restoring backups, and changing settings | Platform owner (role-based account) |
| API key | Authorized by `resource:action`; suitable for automation and read-only integrations | CI/CD, health checks, CMDB, deployment scripts |
| ACME client account | Access to a specific ACME endpoint only, restricted by EAB/domain allowlist | certbot, lego, acme.sh, etc. |

The administrator account is a single point and must be bound to a role rather than an individual. API keys cannot replace human approval and cannot access account security endpoints (password changes, passkeys, session management, and similar).

### 1.2 Recommended separation of duties

| Responsibility | Suggested owner | Uses administrator account |
| --- | --- | --- |
| Day-to-day certificate issuance and deployment | Platform/Operations | Yes, or a restricted API key |
| Custody of DNS/deployment credentials | Owner of the corresponding system | No; credentials are entered by an administrator and rotated as needed |
| Automation integration | Integration development | No; use a dedicated API key |
| Security audit | Security/Audit | No; use a read-only key with `logs:read` / `system_logs:read` |
| Backup restore | Operations lead | Yes; restore restarts the service |
| Plugins and upgrades | Platform owner | Yes; these are high-privilege changes |

If the enterprise requires two-person review, implement it at the process layer (for example, change requests, bastion hosts, or an approval system). The product does not provide a built-in approval workflow.

## 2. Initialization and handover

### 2.1 Initialization

1. Sign in through the secure entry shown in the startup log.
2. Create the administrator with the one-time setup token.
3. Enable TOTP or a passkey immediately.
4. Set and verify the recovery email.
5. Configure `security.public_url`, trusted proxies, and logging.
6. Create the first encrypted backup and confirm that the recovery password works.
7. Install and enable the required plugins.

### 2.2 Role handover

At minimum, complete the following during handover:

- Transfer the administrator password, TOTP seed, or passkey devices.
- Transfer control of the recovery email.
- Review the API key inventory: purpose, permissions, expiration, and owner.
- Review the DNS, deployment, and notification credential inventory.
- Document backup locations, recovery passwords, and the most recent restore drill record.
- Document outstanding tasks, monitoring items, and alert routing.

Do not send plaintext passwords, API keys, or backup passwords through chat tools; use an enterprise password manager or a sealed handover process.

## 3. Account security

### 3.1 Sign-in methods

- Passwords are stored as bcrypt hashes and must be at least 8 characters; for production, use random passwords of 16 characters or more.
- When TOTP is enabled, password sign-in still requires a one-time code; the same code cannot be reused within its validity window.
- A passkey (WebAuthn/Passkey) can serve as an additional sign-in method but does not replace the password; it requires an HTTPS domain or localhost.
- Changing the password increments the token version and deletes all server-side sessions, API keys, and passkeys, immediately invalidating all sign-ins.
- Resetting the password by email also disables TOTP and deletes passkeys and API keys, serving as a recovery path when the authenticator is lost.

### 3.2 Sessions

- Session persistence is enabled by default, so sessions survive backend restarts. Setting `security.session_persistence: false` clears sessions on every restart. Persistence extends session lifetime, so decide in line with enterprise policy.
- Periodically review and revoke unknown devices under "Settings → Sessions".
- A signed-in session is equivalent to administrator privileges; do not stay signed in on shared browsers or untrusted devices.
- Keep core credential alerts enabled under "Notifications → Security alerts", and enable new-device sign-ins and failed sign-in aggregation as needed; the default threshold is 5 failures within 10 minutes. Changing the email notifies both the old and new addresses, and changing notification targets notifies the previous recipients and channels.
- The default hourly limit is 10 alerts for high-frequency sign-in/failure alerts only; critical credential changes and new-device sign-ins bypass it. Beyond the limit events are combined into a digest. The "Recent security events" dialog shows per-target delivery status and errors and can export JSON.
- Security alerts are independent of certificate notifications; do not disable them by turning off certificate email. If plugin channels are used, select only trusted channels because they receive the sign-in IP, device, and account email.

### 3.3 Recovery paths

Prepare at least two independent recovery paths:

1. **Email reset**: for a forgotten password when the email account is under control; clears TOTP, passkeys, and API keys.
2. **Backup restore**: for a damaged account, database, or configuration; requires the backup decryption password and the setup token (for an uninitialized instance).

Document recovery procedures as a runbook and rehearse them regularly. Do not store the recovery password in the same location as the backup files.

## 4. API key governance

API keys are suited to machine identities, not people. Manage them on a "one key per system" basis.

### 4.1 Role-to-permission mapping

| Consumer | Suggested permissions | Notes |
| --- | --- | --- |
| Read-only health checks/CMDB | `system:read`, `dashboard:read`, `certificates:read`, `tasks:read`, `monitors:read`, `deployments:read` | Do not grant any `*:write` |
| Certificate inventory sync | `certificates:read`, `tasks:read` | Do not grant private key permissions |
| CI/CD renewal trigger | `tasks:read`, `tasks:run`; add `deploy:execute` if deployment nodes are included | Scope to the required tasks |
| Certificate download and distribution | `certificates:read`, `certificates:key`, `deploy:execute` | Highly sensitive; restrict source and expiration |
| Log audit | `logs:read`, `system_logs:read` | Read-only key with a short expiration |
| Monitoring and alerting | `system:read`, `monitors:read` | For `/api/status` and monitor reads |
| Platform automation | Combine permissions as needed; avoid `*` | `*` is equivalent to all system capabilities |

### 4.2 Lifecycle

1. Record the purpose, owner, permissions, and expiration.
2. Store the key in an enterprise password manager or secrets management system immediately after creation; never commit plaintext to a code repository.
3. Configure the key in the caller and verify least privilege.
4. Rotate before expiration; the old token is invalidated immediately after rotation.
5. Disable or delete the key immediately when the system is decommissioned or personnel change.

### 4.3 Protection requirements

- Do not place keys in logs, screenshots, tickets, or public repositories.
- Transmit keys only over HTTPS and avoid untrusted proxies.
- Apply source IP restrictions to keys (prefer the built-in IP/CIDR allowlist, with the network or gateway layer as defense in depth).
- Periodically review keys with the `api_keys:read` permission to prevent privilege creep.
- If compromise is suspected, rotate or delete the key immediately and check operation logs for anomalous calls.

> API keys cannot change passwords, register passkeys, or sign out other sessions; these operations are available only to browser sign-in sessions.

## 5. Certificate and CA governance

### 5.1 Public CAs

- Use a dedicated email address for ACME accounts; do not bind a personal mailbox.
- Maintain EAB credentials per CA and limit their scope.
- Track CA rate limits and domain validation requirements; prepare a standby CA for critical domains.
- Generic ACME upstreams must use HTTPS (except for local loopback).

### 5.2 Private CAs

- Use the root CA private key only to sign intermediate CAs; do not use it directly to issue end-entity certificates.
- Set path length, Key Usage, Name Constraints, and validity for intermediate CAs.
- After exporting the root CA private key, store it offline. The private key in the LACTSSL database remains protected by `security.salt`, but it is not an HSM.
- Before deleting a CA, confirm that there are no subordinate CAs or dependent certificates.
- Existing certificates do not automatically gain CRL/OCSP extensions and must be reissued.

### 5.3 ACME server

- Bind each endpoint to one private CA and configure a domain allowlist.
- EAB is enabled by default in production; when EAB is disabled, anyone who can reach the endpoint can create an account.
- For public endpoints, use a dedicated port, source restrictions, and mTLS (if supported by clients).
- Periodically clean up expired orders and long-unused accounts; deleting an account removes its historical orders but does not affect issued certificates.

## 6. Plugins and supply chain

- Use only the official repository or an enterprise internal mirror.
- Configure an Ed25519 public key and enforce signature verification; when none is configured, the UI warns of the risk and unattended automatic updates are skipped.
- Disable automatic updates in production and upgrade manually within change windows.
- Deployment plugins have access to certificate private keys and are disabled by default; enable only trusted helpers.
- Set the plugin directory to `0700` and helpers to `0755`, owned by the service user.
- Retain plugin versions, installation times, and change records to support rollback.

For detailed procedures, see [Plugin governance](plugins.md).

## 7. Audit and evidence

LACTSSL provides the following log categories:

| Log | Content | Purpose |
| --- | --- | --- |
| Operation logs | Tasks, certificates, deployments, notifications, monitors, and settings changes | Business audit, change tracking |
| System logs | Process logs for startup, HTTP, scheduling, plugins, ACME, and similar | Operational troubleshooting, security incident analysis |
| Process logs | stdout or `log.file` | Log platform ingestion, panic analysis |

Recommendations:

- Retain operation logs for at least 90 days (the maximum export range); when longer retention is required, export them to the enterprise log platform regularly.
- System logs are retained for 7 days or 20,000 rows by default; adjust `log.system.retention_days` and `max_rows` according to compliance requirements.
- Store reverse proxy access logs separately, recording the source IP, status code, and `X-Request-ID`.
- When exporting CSV, guard against formula injection; do not send logs containing credentials outside the organization.
- Critical changes (CA generation, private key export, backup restore, plugin installation, secure entry changes) should be linked to a change request or ticket.

## 8. Routine operations checklist

### Daily

- [ ] Review alerts for failed tasks, failed deployments, and certificate anomalies.
- [ ] Check site monitors for abnormal or near-expiry items.
- [ ] Confirm that backup jobs succeeded (if scheduled externally).

### Weekly

- [ ] Review certificates approaching expiry and automatic renewal status.
- [ ] Check system logs for errors, rate limiting, and anomalous sign-ins.
- [ ] Reconcile newly created or disabled API keys and credentials.

### Monthly

- [ ] Check disk usage, database size, WAL, and log retention.
- [ ] Check plugin versions and security advisories.
- [ ] Spot-check operation logs against change records for consistency.
- [ ] Verify that notification channels are reachable.

### Quarterly

- [ ] Complete a backup restore drill.
- [ ] Review the administrator account, recovery email, and TOTP/passkeys.
- [ ] Review API key permissions and expiration.
- [ ] Evaluate upgrade windows and rollback plans.

## 9. Change management

Route high-risk operations through change requests:

| Operation | Risk | Suggested control |
| --- | --- | --- |
| Modify the secure entry | May lock out the administrative entry | Record the new entry and retain the `config.yaml` override method |
| Change `security.salt` | Existing encrypted credentials cannot be decrypted | Do not change it arbitrarily; a migration must accompany any change |
| Delete a private CA | Affects subordinate CAs and dependent certificates | Confirm dependencies first and export a backup |
| Restore a backup | Overwrites current data and restarts the service | Require dual approval and choose an off-peak window |
| Install a deployment plugin | Can read certificate private keys | Review the source, verify signatures, and apply least privilege |
| Upgrade the software | Data migration is irreversible | Back up, validate in staging, and prepare a rollback plan |
| Bulk cleanup of certificates/logs | Data cannot be recovered | Preview and export first, and require dual approval |

## Related documentation

- [Security baseline](security.md): controls and hardening checklist.
- [API integration](api.md): key permissions and call examples.
- [Operations](operations.md): backup, restore, monitoring, and drills.
- [Plugin governance](plugins.md): plugin sources, mirrors, and upgrades.
