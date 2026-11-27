# Monitoring and troubleshooting

This page covers common failures, diagnostic paths, and emergency procedures. Preserve the current state first, then locate the issue in the order dependencies → authentication → application → data. Do not restart services or clear logs prematurely.

## Initial triage

### Determine the scope of impact

- Is one certificate/task/deployment target failing, or all of them?
- Is the admin entry unavailable, or only issuance/deployment?
- Did the issue follow an upgrade or configuration change, or start suddenly? Are notifications, monitoring, or ACME clients affected too?

### Collect baseline information

```bash
# Version and process
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/status

# Docker logs
docker compose logs --since=30m lactssl

# systemd logs
sudo journalctl -u lactssl --since "30 min ago"

# Ports
ss -lntp | grep -E '8080|14000'
```

Also record the time, time zone, and change-window status; the relevant task/certificate/deployment record ID; the `X-Request-ID`; and the original error with sensitive values masked.

### Check dependencies

| Dependency | How to check |
| --- | --- |
| Reverse proxy | Proxy logs, backend port, forwarded headers |
| DNS API | Credential permissions, quotas, domain/zone, TXT records |
| Public CA | Status page, rate limits, CAA, EAB |
| Deployment target | Network, authentication, path, permissions, reload command |
| SMTP/IM | Connectivity, authentication, channel configuration |
| Host | Disk, memory, file permissions, time synchronization |

## Access and sign-in

### Root path returns 404

**Symptom**: `https://ssl.example.com/` returns 404.

**Cause**: The request bypassed the random secure entry; the bare root always returns 404.

**Resolution**:

1. Check the startup log for the secure entry.
2. Query `security.entry` in the database `settings` table.
3. Set `security.entry` in `config.yaml` and restart to override it.

### Forgotten secure entry

- The startup log prints the full entry during initialization.
- Read `security.entry` from the database `settings` table.
- Set `security.entry` in `config.yaml` before startup; it is overridden after restart.

After changing the entry, previous links stop working immediately; record and announce the new value.

### Signed out immediately after sign-in / session invalid

**Possible causes**:

- The service restarted with `security.session_persistence` disabled.
- The password was changed (deletes all sessions) or the service started with a different `jwt_secret`.
- The reverse proxy does not pass `X-Forwarded-Proto`, causing incorrect `Secure` cookies; old cookies may be cached.

**Resolution**:

1. Confirm whether the service restarted or the password changed.
2. Check proxy headers and `trusted_proxies`, then clear browser cookies and re-enter via the secure entry.
3. Session persistence is on by default; set `security.session_persistence: true` to keep sessions across restarts.

### Cookie issues and inaccurate rate limiting

**Symptom**: Sign-in loops, CSRF failures, or all requests sharing one IP quota.

**Cause**: No trusted proxy, missing forwarding headers, or a multi-hop chain trusting only the nearest hop.

**Resolution**:

```yaml
server:
  trusted_proxies: ["127.0.0.1"]
security:
  public_url: "https://ssl.example.com"
```

Confirm that the proxy sets:

```nginx
proxy_set_header X-Real-IP $remote_addr;
# Recommended overwrite mode: trusting this proxy is sufficient
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Forwarded-Proto $scheme;
```

In append mode (`$proxy_add_x_forwarded_for`), a chain such as CDN → Nginx → LACTSSL must list every proxy that can appear in XFF under `trusted_proxies`, or resolution stops at the upstream proxy.

Under **Settings → Configuration file → Auto detect** check `peer_ip`, `applied_trusted_proxies`, `effective_client_ip` / `client_ip_source`, `trusted_skipped`, and `header_warnings`. Access logs also include `ip_source`, `ip_skipped`, and `ip_warnings`; restart after saving.

### 429 Too Many Requests

- Back off according to `Retry-After`; check whether clients share a proxy IP, and configure trusted proxies so quotas use the real client IP.
- Sign-in, TOTP, password reset, backup download, private key export, and ACME calls have separate limits. Do not raise limits to mask brute-force attacks or runaway scripts.

### Passkeys unavailable

- Access must use an HTTPS domain or localhost; IP addresses cannot be a WebAuthn origin. Check browser WebAuthn support and system biometrics/PIN.
- A passkey cannot replace a password; with a lost device, use the password plus TOTP or another recovery path.

## Certificate issuance failures

### DNS-01 validation fails

| Possible cause | Check |
| --- | --- |
| Credential or zone issue | Read/write access to the target zone; domain visible to the account |
| Slow TXT propagation / CNAME | TTL, authoritative DNS, resolution path for `_acme-challenge` |
| Wildcard | Wildcards must use DNS-01 |
| Provider or proxy limits | Quotas, rate limits, API changes, custom gateway reachability and TLS |

Resolution:

1. Check the task log for the specific DNS provider error.
2. Inspect the TXT record with `dig`/`nslookup` and confirm permissions by creating and deleting a record manually.
3. Check the CA's CAA records, fix the issue, and rerun the task.

### CA rate limits / EAB errors

- Public CAs impose limits on accounts, domains, and failed validations; check the CA status page and error details.
- ZeroSSL, Google Trust Services, LiteSSL, ZoTrus, Actalis, and SSL.com require EAB; confirm the KID/HMAC is correct and unexpired (for ZoTrus, the KID is the registered email).
- ZoTrus derives the record name from `tokenPath` (usually `_<tokenPath>.<domain>`, not `_acme-challenge`); add the records shown in the task log, then click "Continue validation". ZoTrus does not yet support wildcards, CA pools, or Target Flow Beta. Generic ACME upstreams must use HTTPS (loopback exempt), and critical domains need a backup CA.

### Certificate issued but not trusted by browsers

- Check that the full chain (fullchain) is deployed and that the certificate/private key pair and SNI/SANs match.
- Private CA certificates require the root CA in client trust stores; check system time as well.

### Private CA issuance fails

- Confirm the CA is unexpired, has certificate-signing usage, and its path length permits subordinate CAs; validity must not exceed the CA expiration.
- An imported CA needs a matching certificate and key with basic constraints. Delete subordinate CAs before deleting a CA.

## Deployment failures

### SSH/SFTP

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Host fingerprint mismatch | Server key changed or misconfiguration | Verify the fingerprint, clear the old one, and record it again |
| First connection rejected | No fingerprint and TOFU not enabled | Provide `SHA256:...` or explicitly enable first-use trust |
| Authentication or path failure | Wrong credentials or missing write permission | Check credentials, target SSH configuration, and directory permissions |
| Reload command fails | Command missing, insufficient permission, or wrong service | Run it manually on the target to verify |

### Local directory

- The service user lacks write permission, the path does not exist, or SELinux/AppArmor blocks access.
- The reload command failed but files were updated; confirm the service actually loaded them.

### Panels/CDNs/Webhooks

- Credentials expired, permissions insufficient, or API version mismatch; deployment parameters do not match the target.
- The target IP allowlist excludes LACTSSL egress, public webhooks use HTTP, or the deployment plugin is disabled or failed verification.

### Certificate renewed but the service still serves the old certificate

- No reload command is configured, or it did not take effect; the target caches certificates (CDN, WAF, load balancer).
- The certificate went to the wrong path/site, only one instance was updated, or the serial/fingerprint does not match.

## Notifications and monitoring

### Notifications not delivered

- Is the channel enabled on the Plugins page and notifications enabled on the notifications page? Is the task notification node enabled with a matching trigger?
- Has the token/webhook expired? For SMTP, implicit TLS is used and sending is rejected without STARTTLS support. Check operation and system logs for send errors; sensitive values are masked.
- Security alerts: check the master switch, sign-in mode, failed sign-in aggregation, event toggles, and channels under "Settings → Security alerts". Beyond the hourly limit only a digest is sent, and recipient changes use the old SMTP snapshot. Security alerts are independent of certificate notifications.

### False positives in site monitoring

- Monitoring validates the hostname and system trust chain; self-signed or incomplete chains and hostname mismatches are abnormal.
- Check the port, SNI, CA store, time, load-balancer node stability, frequency, and timeout.

### No expiry reminders received

- Confirm automatic renewal and a reasonable `RenewDays`, and that the scheduler is running.
- Check channel availability, and whether the certificate was cleaned up or task references cleared.

## Plugin issues

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Provider not listed | DNS category or entry disabled | Enable it on the **Plugins** page |
| Helper rejected at load time | Group/other writable, symbolic link, or digest changed | Fix permissions or re-upload |
| Deployment target unavailable | `deploy_plugins.enabled: false` (explicitly disabled) | Set it to true and restart |
| Notification channel missing | Channel not enabled | Enable it on the **Plugins** page |

Plugin directories:

```text
data/plugins/dns
data/plugins/deploy
data/plugins/notify
```

Permissions: directories `0700`, helpers `0755`, owned by the service runtime user.

### Plugins still report TLS verification failure after importing a trust chain

- **Certificate expired / not yet valid**: renew or update the target certificate; importing a CA does not extend validity.
- **Certificate does not match the target address**: use a domain in the SAN; for IP access the certificate needs that IP SAN.
- **Certificate is not trusted**: import the issuing CA (root or intermediate), not the leaf. One PEM file may contain multiple CAs; no restart is needed.
- Otherwise inspect the chain with `openssl s_client -connect host:port -servername host -showcerts`.

Plugin subprocesses read `<data_dir>/trusted-ca-bundle.pem` via `SSL_CERT_FILE`; the bundle merges system roots, imported CAs, and private CAs. Import only internal CAs you control.

## Backup and restore

### Backup download fails

- Re-enter the current password and, with TOTP enabled, a one-time code; the backup password must be at least 12 characters.
- Check reverse proxy body limits, disk space, and the operation/system logs.

### Credentials cannot be decrypted after restore

- The backup `config.yaml` does not match the database (`salt` differs), or only the database was restored.
- Restore from the complete ZIP backup; do not replace the database alone.

### Cannot sign in after restore

- Use the administrator account from the backup. Session persistence is on by default; with `security.session_persistence: false`, a restart clears sessions.
- The secure entry may have changed (check the startup log), a different `jwt_secret` invalidates old tokens, or `data/restore-pending/` has not been applied.

### Service not started automatically after restore

- Docker: confirm `restart: unless-stopped`. systemd: confirm the service is started. Direct binary: restart it manually.
- Check the startup log for configuration validation errors.

## Database, disk, and memory

### Database growth

- Certificates, task/operation/system logs, and ACME orders grow over time; use certificate cleanup, ACME diagnostic cleanup, and log clearing.
- Off-peak, run "Optimize database and configuration only" under `/upgrade` with VACUUM as needed, and check the WAL size (`wal_checkpoint(TRUNCATE)`).

### Insufficient disk space

- Check `data/`, logs, `upgrade-backups/`, and backup files; remove old snapshots and binaries per retention policy.
- Keep backups off the data directory. VACUUM needs temporary space and fails when disk is full.

### High memory / OOM killed

- The main process idles at a few tens of MB; plugins are one-shot subprocesses limited by `memory_limit_mb`.
- Extraction peaks and concurrent tasks start multiple helpers; check the container limit and leave headroom. Reduce concurrency or the helper limit, but keep plugins working.

### Database lock / write failure

- Confirm only one instance mounts the data directory; do not put SQLite on multi-writer shared storage.
- Check for a full disk, permissions, or a read-only filesystem, and avoid many concurrent tasks during backup/restore.

## Upgrade and migration

### Migration fails at startup

- Check the failing step in the logs, then disk, permissions, and database integrity.
- Restart directly to retry; steps are idempotent and do not advance the failed version. Do not delete `upgrade.schema_version`.

### Pages unavailable after upgrade

- Check the reverse proxy and static asset caching; force a refresh or purge the CDN cache.
- Confirm the binary/image version matches, and review the startup log and browser console.

### Self-upgrade rejected

- Read-only filesystems, `dev` builds, platforms without a release package, and missing checksums/signatures are rejected.
- Upgrading in a container does not change the image; use image tags. Check GitHub API rate limits and the outbound proxy.

## ACME server troubleshooting

| Client error | Possible cause | Resolution |
| --- | --- | --- |
| `badNonce` | Service restarted or nonce expired | The client retries automatically; check whether multiple instances share state |
| `unauthorized` | Account/JWS error, missing EAB | Check EAB, account key, and endpoint |
| `rejectedIdentifier` | Domain not in the allowlist | Check the endpoint domain allowlist |
| `caa` | CAA does not permit the CA | Adjust CAA or switch CA |
| `orderNotReady` | Challenge not completed | Finalize after authorization completes |
| `rateLimited` | Endpoint rate limit triggered | Back off according to `Retry-After` |
| HTTP-01 failure | Target unreachable / private network rejected | Check the CIDR allowlist, port, and routing |
| mTLS failure | Client certificate not trusted | Check `mtls.client_ca` and the client certificate chain |
| Resource URL error | `base_url` not configured | Configure a fixed `acme.base_url` |

Diagnostics:

- "ACME Diagnostics" shows accounts, orders, authorizations, challenges, and certificates; check `acme.base_url`, the endpoint allowlist, EAB, and challenge types.
- Public deployments must not derive URLs from the request Host; CRL/OCSP endpoints are organized by CA, so confirm clients can reach them.

## Incident response

### Suspected API key leak

1. Disable or delete the key on the "API Keys" page, create a new key, and update calling systems.
2. Check operation logs for abnormal calls, deployments, and private key downloads; rotate target credentials if involved.
3. Record the incident and conduct a postmortem.

### Suspected administrator account compromise

1. Change the password immediately; this deletes all sessions, API keys, and passkeys.
2. Check whether the recovery email changed, review operation, system, and reverse proxy logs, and rotate DNS/deployment/notification credentials.
3. Assess certificate reissuance; if `salt` exposure is suspected, follow the key leak process and rebuild/re-enter credentials if necessary.

### Certificate deployed or overwritten incorrectly

1. Pause the relevant tasks, then restore the previous version from the target backup or certificate history.
2. Check deployment node parameters and the reload command, redeploy, and verify the serial/fingerprint.
3. Record the root cause and add pre-deployment validation.

### ACME endpoint abuse

1. Disable the endpoint or turn off `acme.enabled`, and review accounts, orders, and source IPs.
2. Enable EAB, mTLS, or network ACLs, and clean up unexpected accounts and orders.
3. Assess whether unintended certificates were issued.

## Escalation information

When asking the upstream service or community for help, provide:

- LACTSSL version, build time, architecture, and deployment method.
- OS, container/orchestration version, and reverse proxy type.
- Reproduction steps, expected and actual results.
- Relevant masked log excerpts with time, `X-Request-ID`, and error codes; task/certificate/deployment IDs, recent changes, and steps already tried.

Do not provide:

- Administrator passwords, TOTP seeds, or API keys in plaintext.
- Certificate private keys, CA private keys, or backup passwords.
- Unmasked DNS/deployment/notification credentials.
