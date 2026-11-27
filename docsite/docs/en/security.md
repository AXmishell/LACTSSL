# Security baseline
This document is for developers deploying and integrating LACTSSL: the security model, implemented controls, and a pre-launch checklist.

## Security model and trust boundaries
LACTSSL is a self-hosted single instance with these trust boundaries:

```text
[Untrusted] Internet / external network users
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
- **API keys are machine identities** that reduce privileges in automation, not multi-user RBAC.
- **Plugins are trusted code**; deployment helpers can read certificate private keys, so the deployer must enforce supply chain security.
- **The database and `config.yaml` are equally sensitive**; losing `salt` makes encrypted credentials unusable, and leaking it may allow decryption.

## Identity and access control

### Administrator account
- Single administrator account; no multi-user or RBAC support.
- Passwords are bcrypt hashes of at least 8 characters; for production, use random passwords of 16 characters or more. TOTP and passkeys (WebAuthn/Passkey) are supported.
- Enabling TOTP and registering a passkey require on-the-spot re-authentication (current password; TOTP also when enabled), preventing a stolen session from binding an attacker-controlled authenticator. Changing SMTP/notification targets and sending test messages need only a logged-in session or the corresponding API scope.
- Re-authentication allows sensitive actions for 5 minutes in the same browser session; password, email, TOTP, and passkey changes still require a fresh check. Passkeys require an HTTPS domain or localhost, and passkey sign-in does not ask for TOTP again.
- Changing the password increments the token version and deletes all sessions, API keys, and passkeys; an email reset also disables TOTP and deletes passkeys and API keys, so the recovery mailbox must itself be secured.
- Alerts cover password, email, TOTP, passkey, API key, and notification target changes; email changes notify both old and new addresses. New-device sign-ins are reminded at most once per 30-day window, and failed sign-ins aggregate at 5 per 10 minutes, at most hourly. Email is the default; plugin channels receive the sign-in IP, device, and account email, and alerts do not replace MFA or mailbox forwarding checks.
- A policy change notifies the previous recipients and channels using the old SMTP snapshot; an hourly limit (10 by default) applies only to high-frequency sign-in/failure alerts, while critical credential changes and new-device sign-ins bypass it. The settings page shows per-target delivery status.

### API keys
- Keys are 86-character Base64URL random strings (64 bytes of entropy) with no prefix; the server stores only a SHA-256 digest. Permissions use `resource:action`, and `*` represents all system API capabilities.
- Sensitive permissions include `certificates:key`, `private_cas:key`, `deploy:execute`, `plugins:write`, `settings:write`, and `api_keys:write`; backup download/restore, entry changes, and database switching are browser-session only.
- A key cannot grant permissions it lacks, nor modify, rotate, or delete keys with higher privileges than its own. Account security operations (password changes, passkeys, sessions) are browser-session only.
- Issue a separate key per automation system, set an expiration, grant least privilege, and rotate regularly.

## Network and TLS

### Inbound
- The console must be accessed through an HTTPS reverse proxy; the main program listens on HTTP only, and backend ports must stay on loopback or the internal network.
- Configure source IP allowlists, VPN, zero-trust policies, or a WAF at the proxy layer.
- The proxy must overwrite or correctly append `X-Forwarded-For` and pass `X-Forwarded-Proto`; LACTSSL trusts only the exact IPs or smallest CIDRs in `server.trusted_proxies`, including every proxy in an append chain. Without a trusted proxy, all requests appear to come from the proxy IP, breaking rate limiting and `Secure` cookie detection.
- Configure HSTS, TLS 1.2+, disabled weak ciphers, and request body size limits at the proxy (backup restore and helper uploads require larger limits).

### Outbound
The server initiates outbound requests to:

- DNS provider APIs, public ACME CAs, and generic upstreams.
- Notification channels such as SMTP, Gotify, Telegram, DingTalk, WeCom, and webhooks.
- Deployment targets such as SSH/SFTP, FTP/FTPS, cloud CDNs, BaoTa, 1Panel, and SafeLine WAF; site certificate monitoring targets.

- In production, apply egress allowlists by domain or CIDR. When a proxy is required, configure it through system environment variables (Go HTTP client behavior) rather than in-application configuration.
- Review custom targets such as deployment and notification webhooks to avoid sending private keys or tokens to untrusted addresses.
- For internal HTTPS targets, import the internal CA public key under "Settings → TLS trust chain" instead of disabling TLS verification; imported and private CAs are merged into `SSL_CERT_FILE` for plugins and deployment commands.

### SSRF protection
- Literal and resolved addresses are rejected for cloud metadata, link-local, reserved, documentation, and NAT64 ranges (such as `169.254.169.254`, `100.100.100.200`, `fd00:ec2::254`, `0.0.0.0/8`, and `240.0.0.0/4`); loopback and private addresses remain allowed for Gotify, self-hosted panels, and internal webhooks, and should be restricted at the network layer if unneeded.
- Deployment webhooks carry private keys, so public destinations must use `https://`; loopback, private, `.local`, and `.internal` destinations may use HTTP.
- During dial, DNS results are re-validated and the verified IP is connected directly, preventing DNS rebinding; URL paths and query parameters are redacted before failures are logged, keeping Telegram, Gotify, and ServerChan secrets out of logs and backups.

## Secure entry, sessions, and CSRF

### Secure entry
Administration pages are not exposed at the root; access requires a randomly generated entry:

```text
https://your-domain/<entry>/
```

- The entry is 20 random letters and digits by default and stored in the database; pages accessed without it return `404`, including bare `/`.
- APIs are mounted under bare `/api` and protected by a login session or API key; the legacy `/<entry>/api` remains as a compatibility alias.
- The correct entry sets a signed `HttpOnly` gate cookie valid for 24 hours, and probing is rate-limited against brute force.
- Change the entry under "Settings → Secure entry" (8–32 characters, effective immediately). Its full URL is printed once at first initialization; if forgotten, change `security.entry` in `config.yaml` and restart, or look it up in the `settings` table.
- Before initialization, the startup log prints a one-time setup token required for creating the administrator, initial configuration, restarting setup, and restoring from backup (`X-LACTSSL-SETUP-TOKEN`); it is invalidated after success and regenerated on restart.

> The entry is an additional layer of protection; it does not replace HTTPS, strong passwords, or network access controls.

### Sessions
- Sessions use `HttpOnly` cookies unreadable by scripts, with `SameSite=Lax` for console deep links; state-changing requests are protected by double-submit CSRF and Origin checks.
- Session tokens are compact JWTs with a 24-byte (192-bit) random session ID and 32-character CSRF tokens; a long-lived (1 year) `HttpOnly` device cookie (`lactssl_device`, 32 hex characters) is issued on first entry or sign-in.
- On HTTPS, session, CSRF, and entry cookies use the `__Host-` prefix so sibling subdomains cannot overwrite them. Session persistence is enabled by default and keeps sessions across restarts with `jwt_secret`.
- With `security.session_persistence: false`, every start clears server-side sessions and mixes a startup random value into the JWT signing subkey. Changing the password increments the token version and deletes all sessions, API keys, and passkeys.

### CSRF and cross-site protection
- State-changing requests (POST/PUT/DELETE) must carry `X-LACTSSL-CSRF` matching the `lactssl_csrf` cookie; legacy sessions receive it on first request, and header-only fallback is not accepted.
- Unauthenticated endpoints such as sign-in, initialization, forgot password, and password reset also validate `Origin`/`Sec-Fetch-Site`.
- `Authorization: Bearer` API calls are exempt from CSRF validation. Page responses use a per-request CSP nonce, and `script-src` no longer allows arbitrary inline scripts; inline styles remain allowed for the component library.

## Password reset and public URL
Links in reset emails must use a fixed address:

```yaml
security:
  public_url: "https://ssl.example.com"
```

- Without `public_url`, links derive from the current Host only for loopback requests with a Host of `localhost` or a loopback IP; other "forgot password" requests are rejected, and the client-forgeable `X-Forwarded-Host` is never trusted.
- In reverse proxy deployments, configure `public_url` so attackers cannot forge the Host and point reset links (including the token and secure entry) at their own domain.

## Credential and data protection

### Encryption
- DNS, deployment, SMTP, notification, TOTP secret, private CA, and certificate private key fields are encrypted with AES-256-GCM before being written to the database; legacy plaintext data migrates automatically at startup. The encryption key comes from `security.salt`.
- JWTs are signed with an independent subkey derived from `security.jwt_secret` and a startup random value via HKDF, separate from the database key; manually configured `jwt_secret` and `salt` must be at least 32 characters or startup is refused.
- APIs mask secret fields when returning configuration; leaving them empty while editing keeps the existing value.

### Files and backups
- Keep `config.yaml` at `0600` and `data/` at `0700`, owned by the service user. The database, WAL, plugins, logs, and upgrade backups live under `data/`.
- Backup download requires an encryption password of at least 12 characters; packages use PBKDF2-HMAC-SHA256 (600,000 iterations) + AES-256-GCM and contain the database, configuration, and version.
- Treat backup packages as confidential and never distribute unencrypted database or configuration copies. Restore packages are fully validated (key lengths, secure entry, listen address, TLS/mTLS combinations) and invalid configurations are rejected.
- After successful initial setup, unapplied restore packages are cleared so content staged before initialization cannot overwrite the new account on restart.

### Credential rotation
- Use dedicated DNS/deployment/notification accounts and rotate them regularly; issue API keys per system with expiration and rotate immediately on suspected compromise.
- SSH requires a host fingerprint by default; TOFU applies only when "auto-trust host key on first connection" is explicitly enabled, and later mismatches reject the connection.
- FTP uses explicit TLS by default; plaintext FTP applies only when `none` is explicitly selected.

## Audit and logging

| Log | Content | Retention/export |
| --- | --- | --- |
| Operation logs | Tasks, certificates, deployments, notifications, monitors, settings changes | Filter by source/level/time; export the most recent 90 days as CSV/JSON; archived before deletion |
| System logs | Startup, HTTP, scheduling, plugins, ACME | 7 days/20,000 rows by default; adjustable and clearable; archived before deletion |
| Log archives | Raw NDJSON and manifests for operation/system logs | `data/archives/logs/`; kept forever by default; download or delete from Logs → Archive |
| Process logs | stdout or `log.file` | Rotated by size/days; can output JSON |
| Reverse proxy logs | Access, source IP, status code | Managed by an external log system |

- Export operation logs, system logs, and archives to a SIEM regularly to avoid audit gaps after administrator clears, archive deletion, or corruption; the two database log categories share the `logs` table but stay isolated by `category`.
- Every response carries `X-Request-ID` for correlating access logs, panics, and errors. Access logs omit query parameters, bodies, and authentication headers, and the secure entry is replaced with `:entry`.
- Logs may contain client IPs, domains, and operator identities; archive directories use `0700` and files `0600`. Archives are not tamper-proof; if tamper-resistant audit is required, sync them to external WORM storage or a SIEM, and link critical operations to change records.

## Plugin and supply chain security
- Official implementations are compiled into the host and covered by the release signature/image digest. Uploaded helpers run in a Linux Landlock/rlimit sandbox by default, and their digests are rechecked for manifest probes and every call; if the host still runs as root without `LACTSSL_RUN_UID`/`LACTSSL_RUN_GID`, uploads are refused.
- Official builds embed `upgrade.program.public_key` and enforce release signature verification; self-built binaries without it reject online upgrades, and only controlled environments may enable `upgrade.program.allow_unsigned`.
- Assets must provide a 64-character hexadecimal SHA-256 digest verified after download. Installation validates `plugin.json`, the manifest ID and version, and other attributes, with rollback on failure.
- Installation-record digests are rechecked during discovery, registration, and before every DNS/deployment/notification call; replaced files are refused. If no signing public key is configured, a risk warning is shown and unattended automatic updates are skipped.
- The plugin directory is `0700` and helpers are `0755`; group/other write and symlinks are rejected. Deployment helpers receive private keys and are enabled by default; keep only trusted targets enabled. The host limits helper timeouts, memory, and output size while redacting credentials from errors.

Production recommendations:

- Record each helper's source, version, digest, reviewer, and rollback version; validate upgrades in staging.
- Keep only verified helpers in production; third-party code runs with the privileges of the service account.

## ACME server security
- The ACME server is enabled by default but issues nothing externally until an endpoint is created and enabled. It has a dedicated protocol entry that does not use the admin secure entry, session cookies, or CSRF; set `acme.enabled: false` when unused.
- For public deployments, use a dedicated port and restrict access at the firewall or proxy. Every endpoint must have a domain allowlist; enabling one with an empty allowlist is rejected.
- EAB is recommended on. With EAB disabled, anyone who can reach the port can create an account, so combine it with a domain allowlist, mTLS, or network ACLs. mTLS is a transport gate and does not replace ACME JWS account authentication.
- Configure a fixed `acme.base_url` (or `security.public_url`) to prevent Host header injection from pointing resource URLs at the wrong location; custom upstream directory URLs must use HTTPS except on loopback.
- HTTP-01 validation rejects loopback, private, link-local, and CGNAT/reserved ranges by default (such as `100.64.0.0/10` and `192.0.0.0/24`); internal scenarios must explicitly allow them per endpoint.
- ACME resources use random public IDs, not database auto-increment IDs. Single-instance deployments use in-memory nonces; after a restart, clients receive `badNonce` and retry automatically.
- CRL/OCSP live under `/pki/{ca}/...`, are organized per CA, and usually must remain publicly reachable even when enrollment is disabled. They are IP rate-limited, cache by revocation set, and increment the CRL Number only when content changes; revocation state is stored in the `revocations` table and survives certificate cleanup.
- Before issuing a leaf, the CA's `ExtKeyUsage` (must include `serverAuth` or `any`) and NameConstraints are enforced for both task issuance and ACME finalize. Browsers handle private CA revocation inconsistently, and existing certificates must be reissued to include CRL/OCSP URLs.

## Deployment and container hardening
- Run as a dedicated user with `config.yaml` at `0600` and the data directory at `0700`. The Docker entrypoint corrects volume permissions, then runs as the non-root `lactssl` user.
- Compose drops all capabilities and adds back only what is needed (such as `CHOWN`/`SETUID`), with a read-only root filesystem, `/tmp` tmpfs, `pids_limit`, and `init`.
- Never commit `data/` or `config.yaml` to version control or place them in public directories. The service limits regular API bodies to 2 MiB (except backup restore and helper uploads) and sets read/write timeouts; configure body size and timeouts at the proxy too.
- Pin production image/binary versions, scan images for vulnerabilities, and monitor advisories. For ports 80/443, forward through a reverse proxy rather than granting capabilities to the binary.

## Known limitations
- No multi-user or RBAC support; the administrator session is a single point.
- No SSO, LDAP, OIDC, SAML, or SCIM support.
- No HSM/KMS support; private keys are stored encrypted in the local database.
- No active-active clustering; SQLite uses a single writer, and multiple instances cannot share a data directory.
- No tamper-proof audit; administrators can clear logs or delete local archives. If tamper-resistant audit is required, archive externally with WORM/retention locks.
- No Prometheus metrics endpoint; monitoring relies on logs, the authenticated API, and external probes.
- No automatic rollback on deployment targets; the target system must retain previous versions or use atomic releases.

## Pre-launch security checklist

### Identity
- [ ] The administrator uses a strong password and has TOTP or a passkey enabled.
- [ ] The recovery email is controlled and protected with MFA, and unknown sessions have been cleared.
- [ ] API keys are issued per system, follow least privilege, and have expiration times.

### Network
- [ ] The administration entry uses HTTPS only, and backend ports are not exposed directly to the internet.
- [ ] `trusted_proxies` and `public_url` are configured, and the proxy passes `X-Forwarded-For`/`X-Forwarded-Proto`.
- [ ] Source restrictions, a WAF, or zero-trust policies are configured.
- [ ] Outbound traffic is allowlisted by destination and goes through a proxy when required.

### Data
- [ ] `config.yaml` is `0600`, `data/` is `0700`, and ownership is correct.
- [ ] Encrypted backups have been created and stored offsite, with the password kept separately.
- [ ] A restore drill has been completed, and log retention and export meet operational audit needs.

### Plugins and ACME
- [ ] Only reviewed third-party helpers are uploaded, with a signing public key configured.
- [ ] Deployment helpers are enabled only as needed, and their sources have been reviewed.
- [ ] ACME endpoints have domain allowlists, and EAB is enabled in production.
- [ ] ACME entry points are source-restricted, with mTLS enabled when necessary.

## Security incident response
1. **Determine the scope**: account, API key, DNS/deployment credentials, plugin, or host.
2. **Isolate**: revoke sessions/API keys, disable plugins, disable ACME endpoints, and disconnect at the network layer if necessary.
3. **Preserve evidence**: export operation logs, system logs, and reverse proxy logs, and record `X-Request-ID`; do not clear logs first.
4. **Rotate**: changing the password clears all sessions and API keys; rotate DNS/deployment/notification credentials; if `salt` exposure is suspected, follow the key compromise procedure.
5. **Recover**: restore from a trusted backup, verify configuration and data consistency, and reissue any potentially affected certificates.
6. **Review**: update runbooks, permission inventories, and monitoring rules.

For troubleshooting, see [Monitoring and troubleshooting](troubleshooting.md); for backup and restore, see [Operations](operations.md).
