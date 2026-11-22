# Monitoring and troubleshooting

This page describes the most common failures, diagnostic paths, and emergency procedures during enterprise rollout and operation. When troubleshooting, preserve the current state first, then locate the issue in the order dependencies → authentication → business → data. Do not restart services or clear logs prematurely.

## 1. Initial triage

### 1.1 Determine the scope of impact

- Is a single certificate or task failing, or all tasks?
- Is the admin entry unavailable, or only issuance/deployment failing?
- Is a single deployment target affected, or all targets?
- Did the issue appear after an upgrade or configuration change, or did it start suddenly during normal operation?
- Are notifications, monitoring, or ACME clients affected at the same time?

### 1.2 Collect baseline information

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

Also record:

- When the issue occurred, the time zone, and whether it falls within a change window.
- The relevant task / certificate / deployment record ID.
- The `X-Request-ID` response header.
- The original error message (after masking sensitive values).

### 1.3 Check dependencies

| Dependency | How to check |
| --- | --- |
| Reverse proxy | Proxy logs, backend port, forwarded headers |
| DNS API | Credential permissions, quotas, domain/zone, TXT records |
| Public CA | Status page, rate limits, CAA, EAB |
| Deployment target | Network, authentication, path, permissions, reload command |
| SMTP/IM | Connectivity, authentication, channel configuration |
| Host | Disk, memory, file permissions, time synchronization |

## 2. Access and sign-in

### 2.1 Root path returns 404

**Symptom**: `https://ssl.example.com/` returns 404.

**Cause**: The request did not go through the secure entry. The entry is a random path, and the bare root path always returns 404.

**Resolution**:

1. Check the startup log for the secure entry.
2. Or query `security.entry` in the `settings` table of the database.
3. Or set `security.entry` in `config.yaml` and restart to override it.

### 2.2 Forgotten secure entry

- The startup log (the full entry is printed during initialization).
- The `security.entry` value in the database `settings` table.
- Modify `security.entry` in `config.yaml` before startup; the value is overridden after restart.

After changing the entry, previous links stop working immediately. Record the new entry and notify the relevant parties.

### 2.3 Signed out immediately after sign-in / session invalid

**Possible causes**:

- The service restarted and `security.session_persistence` was explicitly disabled.
- The password was changed, which deletes all sessions.
- The service was started with a different `jwt_secret`.
- The reverse proxy does not pass `X-Forwarded-Proto`, causing incorrect `Secure` cookies.
- The browser or proxy cached old cookies.

**Resolution**:

1. Confirm whether the service was just restarted or the password was changed.
2. Check the proxy headers and `trusted_proxies`.
3. Clear browser cookies and enter again through the secure entry.
4. Session persistence is enabled by default; if it was set to `security.session_persistence: false`, change it back to `true` to keep sessions across restarts.

### 2.4 Cookie issues and inaccurate rate limiting

**Symptom**: Sign-in loops, CSRF failures, or all requests sharing the same IP quota.

**Cause**: No trusted proxy is configured, the proxy does not pass forwarding headers, or a multi-hop chain trusts only the nearest hop.

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

If you use append mode (`$proxy_add_x_forwarded_for`), a multi-hop chain such as CDN → Nginx → LACTSSL must list every proxy that can appear in XFF under `trusted_proxies`; otherwise the client IP stops at the upstream proxy.

Check the following under **Settings → Configuration file → Auto detect**:

- `peer_ip`: the TCP peer;
- `applied_trusted_proxies`: the currently applied trusted list;
- `effective_client_ip` / `client_ip_source`: the final result and its source;
- `trusted_skipped`: how many trusted entries were skipped from right to left;
- `header_warnings`: invalid or oversized XFF and similar parsing issues.

Access logs also include `ip_source`, `ip_skipped`, and `ip_warnings`. Restart after saving; before restart, `configured_trusted_proxies` and `applied_trusted_proxies` may differ.

### 2.5 429 Too Many Requests

- Back off according to the `Retry-After` response header.
- Check whether requests share a proxy IP; after configuring trusted proxies, quotas are counted by the real client IP.
- Sign-in, TOTP, password reset, backup download, private key export, and ACME protocol calls each have independent rate limits.
- Do not raise rate-limit thresholds to mask brute-force attacks or runaway scripts.

### 2.6 Passkeys unavailable

- Access must use an HTTPS domain or localhost; IP addresses cannot be used as a WebAuthn origin.
- Check whether the browser supports WebAuthn and whether biometrics or a PIN are enabled on the system.
- A passkey cannot replace a password; if the device is lost, use the password plus TOTP or another recovery path.

## 3. Certificate issuance failures

### 3.1 DNS-01 validation fails

| Possible cause | Check |
| --- | --- |
| Credential lacks permission | Does the API key have read/write access to the target zone? |
| Domain/zone mismatch | Is the domain inside a zone visible to the current account? |
| Slow TXT propagation | Wait for the TTL; check whether authoritative DNS returns the TXT record |
| Multi-level domain/CNAME | Confirm the resolution path for `_acme-challenge` |
| Wildcard | Wildcards must use DNS-01 |
| Provider limits | Quotas, rate limits, API changes |
| Proxy/gateway | Is the custom API address reachable, and is its certificate trusted? |

Resolution:

1. Check the task logs for the specific DNS provider error.
2. Use `dig`/`nslookup` to inspect the TXT record for `_acme-challenge.<domain>`.
3. Create and delete a TXT record manually in the provider console with the same credential to confirm permissions.
4. Check whether the CA's CAA records permit that CA.
5. Re-run the task after fixing the issue.

### 3.2 CA rate limits / EAB errors

- Public CAs such as Let's Encrypt impose rate limits on accounts, domains, and failed validations; check the CA status page and error details.
- ZeroSSL, Google Trust Services, LiteSSL, ZoTrus, Actalis, and SSL.com require EAB; confirm that the EAB KID/HMAC is correct and has not expired (for ZoTrus, the KID is the registered email).
- Generic ACME upstream directories must use HTTPS; local loopback is exempt.
- Prepare a backup CA for critical domains so that rate limits at a single CA do not cause total failure.

### 3.3 Certificate issued but not trusted by browsers

- Certificates from public CAs should be trusted; check whether the full chain is deployed (fullchain).
- Certificates from a private CA require the root CA to be installed in the client trust stores.
- Check whether the wrong certificate/private key pair was deployed.
- Check whether SNI, the domain, and the certificate SANs match.
- Check whether the system time is accurate.

### 3.4 Private CA issuance fails

- Confirm that the CA has not expired, has the certificate signing usage, and has a path length that permits subordinate CAs.
- Confirm that the certificate validity period does not exceed the CA expiration.
- An imported CA must have a matching certificate and private key and include the CA basic constraints.
- Delete subordinate CAs before deleting a CA.

## 4. Deployment failures

### 4.1 SSH/SFTP

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Host fingerprint mismatch | Server key changed or misconfiguration | Verify the fingerprint; clear the old fingerprint and record it again |
| First connection rejected | No fingerprint provided and TOFU not enabled | Provide `SHA256:...` or explicitly enable first-use trust |
| Authentication failure | Wrong username/password/private key | Check the credentials and the target SSH configuration |
| Path not writable | The deployment user lacks write permission | Adjust directory permissions or use an account with permission |
| Reload command fails | Command not found, insufficient permission, or wrong service name | Run the command manually on the target host to verify |

### 4.2 Local directory

- The service user lacks write permission on the target directory.
- The path does not exist; the directory must be created in advance.
- SELinux/AppArmor restrictions.
- The reload command failed, but the certificate files were already updated; check whether the service actually loaded the new files.

### 4.3 Panels/CDNs/Webhooks

- Credentials expired, insufficient permissions, or API version mismatch.
- Deployment parameters (site, domain, certificate name) do not match the target system.
- The target system has an IP allowlist that does not include LACTSSL egress.
- Public webhook targets must use HTTPS; loopback and private networks may use HTTP.
- The deployment plugin is not installed, not enabled, or failed file verification.

### 4.4 Certificate renewed but the service still serves the old certificate

- No reload command is configured on the deployment node, or the command did not take effect.
- The target system has caching (CDN, WAF, load balancer).
- The certificate was deployed to the wrong path or site.
- In a multi-instance deployment, only one instance was updated.
- Check whether the certificate serial number/fingerprint matches the target system.

## 5. Notifications and monitoring

### 5.1 Notifications not delivered

- Is the channel enabled on the Plugins page, and are notifications enabled on the notifications page?
- Has the token/webhook expired, and is the target reachable?
- Does SMTP use implicit TLS? Sending is rejected when the server does not support STARTTLS.
- Is the task notification node enabled, and does the trigger condition match?
- Check operation logs and system logs for send errors; sensitive values are masked.
- Security alerts not delivered: check the master switch, sign-in mode, failed sign-in aggregation, event toggles, and channels under "Notifications → Security alerts". Review the per-target status and errors in the "Recent security events" dialog. Beyond the hourly limit only a digest is sent; when SMTP or channels change, previous recipients receive the alert through the old SMTP configuration snapshot. Security alerts are independent of certificate notifications.

### 5.2 False positives in site monitoring

- Monitoring validates the hostname and the system trust chain; self-signed certificates, incomplete chains, and hostname mismatches are flagged as abnormal.
- Check the target port, SNI, system CA trust store, and time.
- Different nodes behind a load balancer may return different certificates; make sure the monitoring target is stable.
- Check whether the monitoring frequency and timeout are reasonable; avoid frequent alerts for unstable targets.

### 5.3 No expiry reminders received

- Is automatic renewal enabled for the task, and is `RenewDays` reasonable?
- Is the scheduler running (check scheduling records in the logs)?
- Is the notification channel available?
- Has the certificate been cleaned up, or have the task references been cleared?

## 6. Plugin issues

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Plugin market list is empty | Index not refreshed / repository unreachable | Check the repository URL, network, and TLS |
| Installation fails | Signature, SHA-256, platform, or `min_host` mismatch | Review the error details; verify the public key and version |
| Provider does not appear after installation | File/item not enabled | Enable the helper file and the specific item |
| Plugin rejected at load time | Group/other writable, symbolic link, or digest changed | Fix permissions or reinstall |
| Legacy plugin migration fails | Legacy helper in use / file conflict | Follow the migration prompts; failures roll back |
| Deployment plugin unavailable | `deploy_plugins.enabled: false` | Enable it explicitly and restart |
| Notification channel missing | Helper not installed/enabled | Install and enable the notification plugin |

Plugin directories:

```text
data/plugins/dns
data/plugins/deploy
data/plugins/notify
```

Permission requirements: directories `0700`, helpers `0755`, owned by the service runtime user.

### 6.1 Plugins still report TLS verification failure after importing a trust chain

Use the specific message to identify the cause:

- **Certificate expired or not yet valid**: renew or update the target certificate; importing a CA does not extend its validity.
- **Certificate does not match the target address**: use a domain listed in the certificate SAN. If you access the target by IP, the certificate must contain that IP SAN.
- **Certificate is not trusted**: make sure you imported the CA that issued the certificate (root or intermediate), not the site leaf certificate. One PEM file may contain multiple CA certificates. No restart is required after import.
- Otherwise, inspect the actual chain returned by the target with `openssl s_client -connect host:port -servername host -showcerts`.

Plugin subprocesses read `<data_dir>/trusted-ca-bundle.pem` through `SSL_CERT_FILE`. The bundle merges system roots, imported CAs, and private CAs; import only internal CAs that you actually control.

## 7. Backup and restore

### 7.1 Backup download fails

- The current password must be entered again; when TOTP is enabled, a one-time code is also required.
- The backup password must be at least 12 characters.
- Reverse proxy request/response body limits.
- Insufficient disk space.
- Check operation logs and system logs.

### 7.2 Credentials cannot be decrypted after restore

- The `config.yaml` in the backup does not match the database; the `salt` differs.
- Only the database was restored, and the configuration was lost.
- Resolution: restore from the complete ZIP backup; do not replace the database on its own.

### 7.3 Cannot sign in after restore

- Use the administrator account from the backup.
- Session persistence is enabled by default, so a restart does not require signing in again. With `security.session_persistence: false`, a restart clears sessions.
- The secure entry may have changed; check the startup log.
- A different `jwt_secret` invalidates old tokens.
- The restore package is in `data/restore-pending/`; check whether it has been applied.

### 7.4 Service not started automatically after restore

- Docker: confirm `restart: unless-stopped`.
- systemd: confirm that the service is started.
- Running the binary directly: restart it manually.
- Check the startup log for configuration validation errors.

## 8. Database, disk, and memory

### 8.1 Database growth

- Certificates, task logs, operation logs, system logs, and ACME orders all grow over time.
- Use one-click certificate cleanup, ACME diagnostic cleanup, and system log clearing.
- During off-peak hours, run "Optimize database and configuration only" under `/upgrade`, and enable VACUUM if necessary.
- Check the WAL size; the optimization tool runs `wal_checkpoint(TRUNCATE)`.

### 8.2 Insufficient disk space

- Check `data/`, logs, `upgrade-backups/`, and backup files.
- Remove old upgrade snapshots and old binary backups according to the retention policy.
- Confirm that backups are not written back to the same data directory.
- VACUUM requires temporary space and fails when disk space is insufficient.

### 8.3 High memory / OOM killed

- The main process has an idle baseline of a few tens of MB; plugins are one-shot subprocesses limited by `memory_limit_mb`.
- Plugin installation and update extraction cause transient peaks, which are more noticeable with large DNS packages.
- Multiple concurrent tasks start multiple helpers at the same time.
- Check whether the container memory limit is too low; leave headroom for peaks.
- Reduce concurrency or lower the helper memory limit, but make sure plugins still work correctly.

### 8.4 Database lock / write failure

- Confirm that only one instance mounts the data directory.
- Do not place SQLite on shared storage with multiple writers.
- Check for a full disk, permissions, or a read-only filesystem.
- Avoid large numbers of concurrent tasks during backup/restore.

## 9. Upgrade and migration

### 9.1 Migration fails at startup

- Check the logs for the failing step.
- Check disk, permissions, and database integrity.
- After fixing the issue, restart directly to retry; migration steps are idempotent and do not advance the failed version number.
- Do not manually delete `upgrade.schema_version`.

### 9.2 Pages unavailable after upgrade

- Check reverse proxy and static asset caching.
- Force a refresh or purge the CDN cache.
- Check whether the binary/image version matches.
- Review the startup log and the browser console.

### 9.3 Self-upgrade rejected

- A read-only filesystem, a `dev` build, a platform without a release package, or a missing checksum/signature causes rejection.
- Upgrading inside a container does not change the image; upgrade by using image tags.
- Check GitHub API rate limits and the outbound proxy.

## 10. ACME server troubleshooting

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

- "ACME Diagnostics" shows accounts, orders, authorizations, challenges, and certificates.
- Check `acme.base_url`, the endpoint allowlist, EAB, and challenge types.
- Public deployments must not derive URLs from the request Host.
- CRL/OCSP endpoints are organized by CA; confirm that clients can reach them.

## 11. Incident response

### 11.1 Suspected API key leak

1. Disable or delete the key on the "API Keys" page.
2. Create a new key and update the calling systems.
3. Check operation logs for abnormal calls, deployments, and private key downloads.
4. If deployment targets are involved, rotate the corresponding credentials.
5. Record the incident and conduct a postmortem.

### 11.2 Suspected administrator account compromise

1. Change the password immediately; this deletes all sessions, API keys, and passkeys.
2. Check whether the recovery email has been changed.
3. Check operation logs, system logs, and reverse proxy logs.
4. Rotate DNS/deployment/notification credentials.
5. If `salt` exposure is suspected, follow the key leak process; rebuild the instance and re-enter credentials if necessary.
6. Assess whether affected certificates need to be reissued.

### 11.3 Certificate deployed or overwritten incorrectly

1. Pause the relevant tasks to prevent further overwrites.
2. Restore the previous version from the target system backup or certificate history.
3. Check the task deployment node parameters and the reload command.
4. Redeploy the correct certificate and verify the serial number/fingerprint.
5. Record the root cause and add pre-deployment validation.

### 11.4 ACME endpoint abuse

1. Disable the endpoint or turn off `acme.enabled`.
2. Review accounts, orders, and source IPs.
3. Enable EAB, mTLS, or network ACLs.
4. Clean up unexpected accounts and orders.
5. Assess whether unintended certificates were issued.

## 12. Escalation information

When asking the vendor or community for help, provide:

- LACTSSL version, build time, architecture, and deployment method.
- Operating system, container/orchestration version, and reverse proxy type.
- Reproduction steps, expected result, and actual result.
- Relevant log excerpts (masked): time, `X-Request-ID`, and error codes.
- Task / certificate / deployment record IDs.
- Whether an upgrade, configuration change, plugin change, or credential rotation occurred recently.
- Steps already attempted and their results.

Do not provide:

- Administrator passwords, TOTP seeds, or API keys in plaintext.
- Certificate private keys, CA private keys, or backup passwords.
- Unmasked DNS/deployment/notification credentials.

## Related documentation

- [Operations](operations.md)
- [Security baseline](security.md)
- [Upgrade and change management](upgrade.md)
- [Plugin governance](plugins.md)
- [API integration](api.md)
