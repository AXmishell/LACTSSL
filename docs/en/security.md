# Security baseline

This document is intended for security leads, auditors, and platform teams. It describes LACTSSL's security model, implemented controls, controls that enterprises must add, and a pre-launch hardening checklist. It is not a compliance certification conclusion and does not replace the enterprise's own risk assessment.

## 1. Security model and trust boundaries

LACTSSL is designed as a self-hosted single instance, with the following trust boundaries:

```text
[Untrusted] Internet / enterprise network users
    │ HTTPS (terminated at the reverse proxy)
[Boundary] Reverse proxy / WAF / network ACLs
    │ HTTP (loopback or internal network)
[Trusted] LACTSSL process
    ├─ Administrator session: equivalent to full control
    ├─ API keys: restricted by permission scope
    ├─ Private CA private keys: encrypted in the local database
    └─ Plugin subprocesses: run as the service account, treated as trusted code
[External] DNS APIs / CAs / SMTP / deployment targets / notification channels
```

Key conclusions:

- **An administrator session is equivalent to full control**, including executing local deployment commands, exporting private keys, and restoring backups.
- **API keys are machine identities** intended to reduce privileges in automation scenarios, not multi-user RBAC.
- **Plugins are trusted code**; deployment plugins in particular have access to certificate private keys, so supply chain security must be enforced by the enterprise.
- **The database and `config.yaml` are equally sensitive**; losing `salt` makes encrypted credentials unusable, and leaking it may allow credentials to be decrypted.

## 2. Identity and access control

### 2.1 Administrator account

- Single administrator account; no multi-user, roles, organizational structure, or approval workflow.
- Passwords are stored as bcrypt hashes and must be at least 8 characters; for production, use random passwords of 16 characters or more.
- Supports TOTP two-factor authentication and passkeys (WebAuthn/Passkey).
- Enabling TOTP, registering a passkey, changing SMTP/notification targets, and sending test mail or notifications all require re-authentication (current password or TOTP), preventing a stolen session from binding the account to an attacker-controlled authenticator or forwarding stored SMTP/notification secrets to an attacker host.
- Passkeys must be used through an HTTPS domain or localhost; a bare IP address cannot be used as a WebAuthn origin.
- Sign-in with a passkey does not ask for TOTP again; the passkey itself performs device fingerprint, face, or system PIN verification.
- Changing the password increments the token version and deletes all server-side sessions, API keys, and passkeys, immediately invalidating all sign-ins.
- Resetting the password by email also disables TOTP and deletes passkeys and API keys; the recovery email itself must be secured.
- Security alerts are sent for password, email, TOTP, passkey, API key, and notification target changes; email changes notify both the old and new addresses. New-device sign-ins are reminded at most once within the configured window (30 days by default); failed sign-ins are aggregated at 5 failures within 10 minutes, at most once per hour.
- Alerts use email by default. Plugin channels must be explicitly selected, and selecting them sends the sign-in IP, device, and account email to the corresponding third party. Alerts do not replace MFA, strong passwords, or forwarding-rule checks on the mailbox itself.
- Changing the security alert policy requires re-authentication and notifies the previous recipients and channels using the old SMTP configuration snapshot; a stolen session cannot silently disable alerts or redirect them to attacker-controlled channels.
- Security alerts have an hourly limit (10 by default) for high-frequency sign-in/failure alerts only; critical credential changes and new-device sign-ins bypass it, preventing an attacker from drowning critical alerts in noise. The settings page shows per-target delivery status for recent events.

### 2.2 API keys

- Keys are 86-character Base64URL random strings (64 bytes of entropy) with no fixed prefix; the server stores only a SHA-256 digest.
- Permissions are divided by "resource:action"; `*` represents all system API capabilities.
- Sensitive permissions are listed separately: `certificates:key`, `private_cas:key`, `deploy:execute`, `backup:download`, `backup:restore`, `plugins:write`, `settings:write`, `api_keys:write`, and others.
- When managing other keys through an API key, a key cannot grant permissions it does not have, nor modify, rotate, or delete keys with higher privileges than its own.
- Account security operations (password changes, passkeys, session management) are allowed only from browser sign-in sessions and cannot be called with API keys.
- Create a separate key for each automation system, set an expiration, grant least privilege, and rotate regularly.

### 2.3 Enterprise responsibilities

- Multi-user RBAC, SSO (OIDC/SAML/LDAP), SCIM, and approval workflows.
- Bastion hosts, PAM, or zero-trust access proxies.
- Two-person review of the administrator account and periodic access reviews.
- Centralized custody of API keys (Vault/KMS) and source IP restrictions.

## 3. Network and TLS

### 3.1 Inbound

- The administration console must be accessed through an HTTPS reverse proxy; the main program listens on HTTP only.
- Backend ports must listen only on loopback or the internal network and must not be exposed directly to the internet.
- Configure source IP allowlists, VPN, zero-trust policies, or a WAF at the proxy layer.
- The proxy must overwrite or correctly append `X-Forwarded-For` and pass `X-Forwarded-Proto`; LACTSSL trusts only the exact IPs or smallest CIDRs in `server.trusted_proxies`. In an append chain, every proxy in the chain must be trusted.
- When no trusted proxy is configured, all requests are treated as coming from the proxy IP, which breaks rate limiting and `Secure` cookie detection.
- Configure HSTS, TLS 1.2+, disabled weak ciphers, and request body size limits at the proxy layer (backup restore and plugin uploads require larger limits).

### 3.2 Outbound

The server initiates outbound requests to:

- DNS provider APIs (to create and clean up TXT records).
- Public ACME CAs and generic upstreams.
- Notification channels such as SMTP, Gotify, Telegram, DingTalk, WeCom, and webhooks.
- Deployment targets such as SSH/SFTP, FTP/FTPS, cloud CDNs, BaoTa, 1Panel, and SafeLine WAF.
- Site certificate monitoring targets.

Recommendations:

- In production, apply egress allowlists by domain or CIDR and permit only the destinations actually required.
- When an enterprise proxy is required, configure it through system environment variables (Go HTTP client behavior) rather than in-application configuration.
- Review the source of custom targets such as deployment webhooks and notification webhooks to avoid sending private keys or tokens to untrusted addresses.
- For internal HTTPS targets, import the internal CA public key under "Settings → TLS trust chain" instead of disabling TLS verification; imported and private CAs are merged into `SSL_CERT_FILE` for plugins and deployment commands.

### 3.3 SSRF protection

- Literal and resolved destination addresses are rejected for cloud metadata, link-local, reserved, documentation, and NAT64 ranges (such as `169.254.169.254`, `100.100.100.200`, `fd00:ec2::254`, `0.0.0.0/8`, and `240.0.0.0/4`).
- Loopback and private addresses remain allowed for scenarios such as Gotify, self-hosted panels, and internal webhooks; restrict them further at the network layer if not needed.
- Deployment webhooks carry certificate private keys, so public destinations must use `https://`; loopback, private, `.local`, and `.internal` destinations may use HTTP.
- During the dial stage, DNS results are re-validated and the verified IP is connected to directly, preventing DNS rebinding bypasses.
- Before third-party request failures are written to logs, URL paths and query parameters are redacted to keep Telegram bot tokens, Gotify tokens, ServerChan SendKeys, and similar secrets out of logs and backups.

## 4. Secure entry, sessions, and CSRF

### 4.1 Secure entry

Administration pages are not exposed at the root path; access requires entering through a randomly generated entry first:

```text
https://your-domain/<entry>/
```

- The entry is generated by default as 20 random upper- and lowercase letters and digits and stored in the database.
- Pages accessed without the entry return `404`, including common probe paths such as bare `/`.
- APIs are mounted under bare `/api` and protected by a login session or API key; the legacy `/<entry>/api` is retained as a compatibility alias.
- Visiting the correct entry sets a signed `HttpOnly` gate cookie valid for 24 hours; the server also validates the expiration.
- Entry probing is rate-limited to prevent brute-force enumeration.
- The entry can be changed under "Settings → Secure entry" and takes effect immediately; a manual entry must be 8–32 characters.
- On first initialization, the full entry URL is printed to the startup log; it is no longer printed after initialization is complete.
- If the entry is forgotten, change `security.entry` in `config.yaml` and restart to override it, or look up `security.entry` in the database `settings` table.
- Before the system is initialized, the startup log also outputs a one-time setup token; creating the administrator, changing initial configuration, restarting the setup process, and restoring from backup all require this token (`X-LACTSSL-SETUP-TOKEN`).
- The token is invalidated immediately after successful initialization and regenerated when the service restarts.

> The entry is an additional layer of protection; it does not replace HTTPS, strong passwords, or network access controls.

### 4.2 Sessions

- Login sessions are stored in `HttpOnly` cookies that frontend scripts cannot read; cookies use `SameSite=Strict`.
- Session tokens are padded to at least 512 characters; CSRF tokens are 32 characters.
- The first visit to the secure entry or a sign-in issues a long-lived (1 year) `HttpOnly` device identifier cookie (`lactssl_device`, 32 hex characters).
- On HTTPS deployments, session, CSRF, and entry cookies automatically use the `__Host-` prefix, preventing sibling subdomains under a shared parent domain from overwriting them.
- By default, every service start clears server-side session records and mixes a startup random value into the JWT signing subkey; all previous sign-ins are invalidated immediately after a restart.
- With `security.session_persistence: true`, startup no longer clears sessions and uses a fixed `jwt_secret` for signing, so existing sessions remain usable after a backend restart.
- Changing the password increments the token version and deletes all sessions, API keys, and passkeys.

### 4.3 CSRF and cross-site protection

- All requests that change server state (POST/PUT/DELETE, etc.) must carry an `X-LACTSSL-CSRF` request header that matches the `lactssl_csrf` cookie.
- Legacy sessions receive this cookie automatically on their first request; header-only fallback validation is not accepted.
- Unauthenticated endpoints such as sign-in, initialization, forgot password, and password reset additionally validate `Origin`/`Sec-Fetch-Site`.
- API calls that explicitly use `Authorization: Bearer <token>` are exempt from CSRF validation, which simplifies scripts and external integrations.
- Page responses use a random CSP nonce per request, and `script-src` no longer allows arbitrary inline scripts; inline styles remain allowed to meet component library requirements.

## 5. Password reset and public URL

Links in password reset emails must be generated from a fixed address:

```yaml
security:
  public_url: "https://ssl.example.com"
```

- When `public_url` is not configured, links are derived from the current Host only for requests from the local loopback address with a Host of `localhost`/a loopback IP; "forgot password" requests from other sources are rejected.
- The client-forgeable `X-Forwarded-Host` is not trusted.
- In reverse proxy deployments, `public_url` must be configured to prevent attackers from forging the Host and pointing reset links (including the token and secure entry) at their own domain.

## 6. Credential and data protection

### 6.1 Encryption

- Sensitive fields such as DNS credentials, deployment credentials, SMTP passwords, notification tokens, TOTP secrets, private CA private keys, and certificate private keys are encrypted with AES-256-GCM before being written to the database.
- Legacy plaintext data is migrated automatically at startup.
- The encryption key comes from `security.salt` in `config.yaml`.
- JWTs are signed with `security.jwt_secret`; the effective key is an independent subkey derived from `jwt_secret` and a startup random value via HKDF, separate from the database AES-GCM key.
- When `security.jwt_secret` and `security.salt` are configured manually, they must be at least 32 characters; shorter values cause startup to be refused.
- APIs mask secret fields when returning configuration; leaving them empty while editing keeps the existing value.

### 6.2 Files and backups

- Keep `config.yaml` at `0600` and `data/` at `0700`, owned by the service user.
- The database, WAL, plugins, logs, and upgrade backups are all located under `data/`.
- Downloading a backup requires an encryption password of at least 12 characters; backup packages are encrypted with PBKDF2-HMAC-SHA256 (600,000 iterations) + AES-256-GCM.
- Backup packages contain the database, configuration, and version information and must be handled as confidential files; do not distribute unencrypted database or configuration copies.
- Restore packages are fully validated before use (key lengths, secure entry, listen address, TLS/mTLS combinations, and so on); invalid configurations are rejected.
- After successful initial setup, restore packages that have not been applied are cleared to prevent content staged before initialization from overwriting the new account on a later restart.

### 6.3 Credential rotation

- Use dedicated accounts for DNS/deployment/notification credentials and rotate them regularly.
- SSH requires a host fingerprint by default; TOFU is used only when "auto-trust host key on first connection" is explicitly enabled, and later fingerprint mismatches cause the connection to be rejected.
- FTP deployment uses explicit TLS by default; plaintext FTP is used only when `none` is explicitly selected.
- Set expiration times for API keys, issue them per system, and rotate immediately on suspected compromise.

## 7. Audit and logging

| Log | Content | Retention/export |
| --- | --- | --- |
| Operation logs | Tasks, certificates, deployments, notifications, monitors, settings changes | Filter by source/level/time; export the most recent 90 days as CSV/JSON; archived before deletion |
| System logs | Startup, HTTP, scheduling, plugins, ACME | 7 days/20,000 rows by default; adjustable and clearable; archived before deletion |
| Log archives | Raw NDJSON and manifests for operation/system logs | `data/archives/logs/`; kept forever by default; download or delete from Logs → Archive |
| Process logs | stdout or `log.file` | Rotated by size/days; can output JSON |
| Reverse proxy logs | Access, source IP, status code | Managed by the enterprise log platform |

Recommendations:

- Export operation logs, system logs, and archives to a SIEM or log platform regularly to avoid audit gaps caused by administrators clearing logs, deleting archives, or data corruption.
- Operation logs and system logs share the `logs` table but are isolated by `category`; clearing system logs never deletes operation logs.
- Every response carries an `X-Request-ID` that can be used to correlate access logs, panics, and errors.
- Access logs do not record query parameters, request bodies, or authentication headers; the secure entry is consistently replaced with `:entry` in paths.
- Logs and archives may contain client IPs, domains, error details, and operator identities; manage them as operational data and avoid sharing them publicly. Archive directories use `0700` and files use `0600`.
- Archives are not tamper-proof: an administrator with data-directory access can delete or modify them. High-compliance environments still need external WORM storage or a SIEM.
- Link critical operations (private key export, backup restore, CA generation, plugin installation, secure entry changes) to change requests.

## 8. Plugin and supply chain security

- The official plugin repository supports Ed25519 index signatures; once a public key is configured, `index.json.sig` is verified mandatorily.
- Program self-upgrade requires `upgrade.program.public_key` by default; without it online upgrades are rejected, and only controlled environments may enable `upgrade.program.allow_unsigned`.
- Assets must provide a 64-character hexadecimal SHA-256 digest, which is verified after download.
- Installation validates `plugin.json`, the ID and version in the manifest, and other attributes; any failure triggers a rollback.
- File digests from installation records are rechecked during discovery, registration, and before every DNS/deployment/notification call; replaced files are refused for loading or execution.
- When no signing public key is configured, a risk warning is displayed and unattended automatic updates are skipped.
- Cached indexes (including 304 reuse) are re-verified against the current public key on every load; when the key changes or the cache has no signature, the ETag is discarded and a full fetch is forced. Configuring a key alone never marks a cache as verified.
- The plugin directory permission is `0700` and helpers are `0755`; group or other write access is not allowed, and symlinks are rejected.
- Deployment plugins receive certificate private keys and are disabled by default; only install them from trusted sources.
- The main program limits helper timeouts, resident memory, and output size, and redacts credentials and private keys from error messages.

Production recommendations:

- Use an enterprise internal mirror (HTTPS or `file://`), pin versions, and disable automatic updates.
- Maintain a plugin admission list that records the source, version, digest, approver, and rollback version.
- Validate plugin upgrades in staging before production.

## 9. ACME server security

- The built-in ACME server is enabled by default but does not issue certificates externally until an endpoint is created and enabled.
- Once enabled, it provides a dedicated protocol entry that does not use the administration secure entry, session cookies, or CSRF validation; set `acme.enabled` to `false` when not needed.
- For public deployments, use a dedicated port and restrict access at the firewall or reverse proxy layer.
- Every ACME endpoint must have a domain allowlist; enabling an endpoint with an empty allowlist is rejected.
- EAB can be enabled or disabled and is recommended on by default. With EAB disabled, anyone who can reach the port can create an account, so it must be combined with a domain allowlist, mTLS, or network ACLs.
- mTLS is a transport-layer gate and does not replace ACME JWS account authentication.
- `acme.base_url` should be configured as a fixed public address to prevent Host header injection from pointing resource URLs at the wrong location.
- HTTP-01 validation rejects loopback, private, link-local, and CGNAT/reserved ranges by default (such as `100.64.0.0/10` and `192.0.0.0/24`); internal scenarios must explicitly allow them through endpoint-level CIDR allowlists.
- ACME protocol resources use random public IDs rather than database auto-increment IDs.
- Custom ACME upstream directory URLs must use HTTPS; only local loopback addresses may use HTTP.
- Single-instance deployments use in-memory nonces; after a service restart, clients receive `badNonce` and retry automatically.
- CRL and OCSP endpoints are located under `/pki/{ca}/...` and usually must be publicly reachable; they are organized per CA and should remain available even when the ACME enrollment endpoint is disabled.
- When ACME is enabled, configure a fixed `acme.base_url` (or `security.public_url`); otherwise resource URLs are derived from the request Host, and public deployments should not rely on that fallback.
- CRL/OCSP endpoints are rate-limited by IP; CRLs cache results for the same revocation set and increment the CRL Number only when revocation content changes.
- ACME server rate limiting reads trusted proxies from `server.trusted_proxies` and counts by real client IP.
- CRL/OCSP only affects clients that actively check revocation status; browsers behave inconsistently for private CAs. Existing certificates must be reissued to include CRL/OCSP URLs.
- Revocation state is stored in a separate `revocations` table; deleting or cleaning up certificate rows does not erase revocations, and CRL/OCSP are generated from that table.
- Before issuing a leaf certificate, the CA's `ExtKeyUsage` (must include `serverAuth` or `any`) and NameConstraints (permitted/excluded DNS and IP) are enforced; task issuance and ACME finalize share the same validation.

## 10. Deployment and container hardening

- Run the service as a dedicated user; keep `config.yaml` at `0600` and the data directory at `0700`.
- The Docker image corrects volume permissions through an entrypoint script and then runs the main program as the non-root `lactssl` user.
- Compose drops all capabilities by default and adds back only those needed, such as `CHOWN`/`SETUID`, and enables a read-only root filesystem, a `/tmp` tmpfs, `pids_limit`, and `init`.
- Do not commit `data/` or `config.yaml` to version control or place them in public directories.
- By default, the service limits regular API request bodies to 2 MiB (except backup restore and plugin uploads) and sets read/write timeouts; configure request body size and timeouts at the reverse proxy layer as well.
- Pin production image/binary versions, scan images for vulnerabilities regularly, and monitor release security advisories.
- When using privileged ports such as 80/443, prefer forwarding through a reverse proxy rather than granting additional capabilities to the binary.

## 11. Control mapping

The following table helps security and audit teams align on "what the product provides and what the enterprise must add." It is not a certification conclusion; specific controls must be tailored to the enterprise's system and assessment scope.

| Control area | Provided by LACTSSL | Enterprise responsibilities |
| --- | --- | --- |
| Asset inventory | Certificates, tasks, CAs, credentials, deployment records | CMDB linkage, business ownership, accountable owners |
| Access control | Single administrator, TOTP/passkeys, API key scopes | SSO, RBAC, bastion hosts, approval workflows |
| Cryptography | AES-256-GCM field encryption, TLS provided by the proxy, private CA | HSM/KMS, key custody, cryptographic module compliance |
| Log auditing | Operation logs, system logs, local archives, request IDs, export | SIEM, long-term retention, tamper protection, alert rules |
| Supply chain | Plugin signatures, SHA-256, installation record verification | Internal mirrors, admission review, vulnerability scanning |
| Backup and recovery | Encrypted ZIP backups, restore validation, migration snapshots | Offline copies, offsite storage, restore drills |
| Change management | Operation logs, upgrade backups, rollback documentation | Change requests, two-person review, maintenance windows |
| Vulnerability management | CI, release verification, dependency updates | Image scanning, penetration testing, patch SLAs |
| Network security | Secure entry, rate limiting, SSRF protection, trusted proxies | Firewalls, WAF, zero trust, egress allowlists |
| Data retention | Log retention configuration, certificate/log cleanup, local archives | Compliance retention periods, external archival, tamper resistance, and destruction procedures |

## 12. Known limitations

- No multi-user or RBAC support; the administrator session is a single point.
- No SSO, LDAP, OIDC, SAML, or SCIM support.
- No HSM/KMS support; private keys are stored encrypted in the local database.
- No active-active clustering; SQLite uses a single writer, and multiple instances cannot share a data directory.
- No tamper-proof audit; administrators can clear logs or delete local archives, so high-compliance environments must archive externally with WORM/retention locks.
- No Prometheus metrics endpoint; monitoring relies on logs, the authenticated API, and external probes.
- No automatic rollback on deployment targets; the target system must retain previous versions or use atomic releases.
- No MLPS/ISO certification is included; compliance conclusions must be issued by the enterprise's assessment body.

## 13. Pre-launch security checklist

### Identity

- [ ] The administrator uses a strong password and has TOTP or a passkey enabled.
- [ ] The recovery email is controlled and protected with MFA.
- [ ] Unknown sessions have been cleared.
- [ ] API keys are issued per system, follow least privilege, and have expiration times.

### Network

- [ ] The administration entry uses HTTPS only, and backend ports are not exposed directly to the internet.
- [ ] `trusted_proxies` and `public_url` are configured.
- [ ] The proxy passes `X-Forwarded-For`/`X-Forwarded-Proto`.
- [ ] Source restrictions, a WAF, or zero-trust policies are configured.
- [ ] Outbound traffic is allowlisted by destination and goes through the enterprise proxy when required.

### Data

- [ ] `config.yaml` is `0600`, `data/` is `0700`, and ownership is correct.
- [ ] Encrypted backups have been created and stored offsite.
- [ ] The backup password is stored separately from the backup files.
- [ ] A restore drill has been completed.
- [ ] Log retention and export meet audit requirements.

### Plugins and ACME

- [ ] Only trusted repositories/internal mirrors are used, and a signing public key is configured.
- [ ] Automatic updates are disabled in production.
- [ ] Deployment plugins are enabled only as needed, and their sources have been reviewed.
- [ ] ACME endpoints have domain allowlists, and EAB is enabled in production.
- [ ] ACME entry points are source-restricted, with mTLS enabled when necessary.

## 14. Security incident response

1. **Determine the scope**: account, API key, DNS/deployment credentials, plugin, or host.
2. **Isolate**: revoke sessions/API keys, disable plugins, disable ACME endpoints, and disconnect at the network layer if necessary.
3. **Preserve evidence**: export operation logs, system logs, and reverse proxy logs, and record `X-Request-ID`; do not clear logs first.
4. **Rotate**: changing the password clears all sessions and API keys; rotate DNS/deployment/notification credentials; if `salt` exposure is suspected, follow the key compromise procedure.
5. **Recover**: restore from a trusted backup, verify configuration and data consistency, and reissue any potentially affected certificates.
6. **Review**: update runbooks, permission inventories, and monitoring rules.

For specific troubleshooting steps, see [Monitoring and troubleshooting](troubleshooting.md); for backup and restore, see [Operations](operations.md).

## Related documentation

- [Administration](administration.md): accounts, keys, auditing, and responsibilities.
- [Architecture](architecture.md): network, ports, and availability.
- [Operations](operations.md): backup, restore, upgrades, and drills.
- [Plugin governance](plugins.md): supply chain and internal mirrors.
