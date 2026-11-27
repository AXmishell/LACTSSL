# Operations Manual

This manual is for developers deploying and maintaining LACTSSL. It covers backup and restore, monitoring, capacity, change and upgrade management, and incident response, so the service stays transferable, auditable, and recoverable.

## Service Objectives

Define the following before deployment:

| Item | Recommended definition |
| --- | --- |
| Availability hours | Aligned with the dependent DNS, CA, reverse proxy, and deployment targets |
| RPO / RTO | At least one backup per day (hourly for critical environments); RTO based on a "provision host + restore + startup verification" exercise |
| Change window | Avoid periods of concentrated certificate expiry and release peaks |
| Alert response | Respond to task failures and certificate anomalies within 15–30 minutes (per deployment policy) |

Process liveness is not service availability; true availability also depends on the DNS API, CA, deployment targets, and notification path.

## Backup and Restore

### Backup Scope

Back up `config.yaml` (or the `-config` path) and the entire `data/` directory together. `data/` contains at least `lactssl.db`, possibly `lactssl.db-wal` and `lactssl.db-shm`, plus plugins, logs, log archives, and upgrade backups.

The ZIP produced by **Download backup** **does not include** `data/archives/logs/`; copy archives separately. Backing up only the database loses the `salt`, leaving encrypted credentials undecryptable, while backing up only the configuration loses application data.

The `security_events` table stores security events, recipients, and source IP/UA and is included in database backups; events are retained 90 days by default (adjustable in Settings → Security alerts). The old SMTP snapshot used by notification-target changes is encrypted and cleared after delivery. Protect backups as both personal information and credentials.

### Encrypted Backup in the UI (Recommended)

**System Settings → Backup and Restore** provides online operations:

- **Download backup** creates a ZIP with a SQLite snapshot, the current `config.yaml`, and `manifest.json`. Re-enter the account password, plus a one-time code when TOTP is enabled.
- The backup password is independent, at least 12 characters, and used with PBKDF2-HMAC-SHA256 (600,000 iterations) + AES-256-GCM. If lost, the backup cannot be recovered.
- Store files off `data/`, keep the password in a password manager, record time/size/checksum/location, and retain backups for at least 7–30 days.

### Command-Line Backup

For a file-level backup, stop the service first to keep SQLite consistent:

```bash
# Docker
docker compose stop

# Or stop the systemd service
sudo systemctl stop lactssl

tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

If downtime is not possible, use the encrypted UI backup; copying a running SQLite database directly may produce an inconsistent snapshot.

### Restore

#### Restore in the UI

1. Upload a ZIP backup or a standalone `.db` file and enter the current password; encrypted backups also need the decryption password.
2. The service writes to `data/restore-pending/` and restarts automatically. Docker deployments return via `restart: unless-stopped`; restart a directly run binary yourself.
3. Sign in with the administrator account from the backup.

#### Manual Restore

1. Stop the service and put `config.yaml` and `data/` from the backup back in place.
2. Set configuration `0600` and data directory `0700`, owned by the service user, then start and check the secure entry, login, certificates, tasks, and plugins.

#### Restore Considerations

- Sign in with the backup's administrator account. Session persistence is on by default; with `security.session_persistence: false`, every start clears sessions and rotates the JWT signing subkey.
- A different `jwt_secret` invalidates old tokens; a different `salt` makes encrypted credentials undecryptable. Always restore from a complete ZIP backup, and check the startup log if the secure entry changed.
- Restoring only the database without the matching `salt` requires re-entering all DNS, deployment, and notification credentials.

### Restore Drill

Perform at least quarterly:

- [ ] Restore the most recent backup in an isolated environment.
- [ ] Verify administrator login, TOTP/passkeys, recovery email, certificates, tasks, credentials, and channels.
- [ ] Run a test task, deploy to a non-production target, record the actual RTO, and confirm the backup password is still held by an authorized person.

## Monitoring and Alerting

### Monitoring Signals

| Signal | Collection method | Recommended threshold/action |
| --- | --- | --- |
| Process liveness | TCP port probe | Every 30–60 seconds; alert after 3 consecutive failures |
| Authentication health | `GET /api/status` + `system:read` API key | Every 1–5 minutes; alert on non-200 or high latency |
| Task / deployment failures | Operation logs, notifications, deployment records | Alert immediately; distinguish DNS/CA/deployment/notification |
| Certificate nearing expiry | Dashboard, site monitoring | Tiered reminders at 30/14/7 days |
| Disk / backup | Host monitoring, backup jobs/file checks | Warn above 80%, alert above 90%; alert on backup failure or missed interval |
| HTTP 5xx / login anomalies | Reverse proxy and operation/system logs | Thresholds against baseline; alert on repeated failures or unusual source IPs |

### Alert Routing

- Task, certificate, and deployment failures go to the operations alert channel.
- Security events (unusual logins, secure entry probing, plugin verification failures) go to security; backup failures and low disk go to platform.
- Do not send test notifications to the production alert group. Configure at least two independent channels (for example, email + IM) to avoid a single point of failure.

### Monitoring Practices to Avoid

- Do not use bare `/` or `/login` for anonymous health checks: requests that bypass the secure entry return 404, which is expected.
- Do not store administrator passwords or long-lived full-privilege API keys in monitoring systems, and do not put private keys or backup passwords in alert content.

## Routine Maintenance

### Daily

- Review task/deployment failures and site monitoring; confirm no new high-risk expiry items and that backups ran.

### Weekly

- Check automatic renewal scheduling; review system-log errors, rate limiting, unusual logins, and API key/credential changes.

### Monthly

- Check disk, database size, WAL, log retention, plugin versions, and advisories; spot-check logs, change records, and channels.

### Quarterly

- Complete the backup and restore drill, and review administrator accounts, recovery email, TOTP/passkeys, API key permissions, and rollback plans.

## Capacity and Cleanup

### Capacity to Monitor

| Item | Growth source | Handling |
| --- | --- | --- |
| SQLite | Certificates, task/operation/system logs, ACME orders | Purge historical data, optimize the database, archive logs |
| Log archives | NDJSON.gz files and manifests under `data/archives/logs/` | Copy to external storage; delete from Logs → Archive when appropriate |
| WAL | Long uptime, frequent writes | `wal_checkpoint(TRUNCATE)` by the upgrade/optimization tool |
| Plugins / upgrade backups | Old helper files, migration snapshots, old binaries | Uninstall unused plugins and purge per retention policy |
| Backup storage | Daily/weekly backups | Set retention and offsite copies |

### Cleanup Entry Points

- **Certificate Management → One-click cleanup** filters by status, expiry/revocation age, issuance time, domain, issuer, and source, optionally keeping the latest certificate per task; task references are cleared after cleanup.
- **ACME Diagnostics → Cleanup** removes invalid orders and accounts; deleting an account removes historical orders but not issued certificates.
- **Logs → Operation/System → Clear** archives to `data/archives/logs/` first when enabled; an archive failure blocks deletion. **Logs → Archive** browses, downloads, or deletes archives under the configured retention.

### Database Maintenance

- Maintenance cleanup runs every 6 hours; confirm the scheduler in the logs page. A new version runs migrations on first start and creates a snapshot in `data/upgrade-backups/`.
- To reclaim space, visit `/upgrade` off-peak and run "Optimize database & config only" with VACUUM; ensure a backup and free disk space first. A `VACUUM` failure is a warning and does not stop the service.

## Versions and Releases

### Version Identification

- The git tag is the sole version source; local builds use `git describe --tags --always --dirty` and strip the `v`, injected with `-ldflags -X 'lactssl/internal/api.Version=...'`. **Settings → About** shows build version, time, and architecture; `go run` and development builds show `dev`.

### Release Channels

- Stable tags include `latest`, `<version>`, `<major>.<minor>`, and `<major>`; pre-releases push only the full version and do not overwrite `latest`.
- Pin production to an explicit version; do not run an untraceable `latest` tag long term.

### Release Artifacts

Releases build Linux/macOS/Windows binaries, `checksums.txt`, and `linux/amd64` and `linux/arm64` images; archives contain the binary, `README.md`, `LICENSE`, and `config.yaml.example`. See [Development: source and build](development.md).

## Upgrade and Change

Before upgrading:

1. Read the release and migration notes, and back up `config.yaml` and `data/`.
2. Validate issuance, deployment, notifications, and API in staging.
3. Confirm the rollback version and steps, choose an off-peak window, and notify stakeholders.

Docker:

```bash
docker compose pull
docker compose up -d
```

Binary:

```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo systemctl start lactssl
```

After upgrading, look for "data format upgrade completed", confirm step status on `/upgrade`, verify certificates, tasks, deployments, notifications, and the API, and observe at least one scheduling cycle (30 seconds) and one task run.

For complete migration, rollback, and upgrade instructions, see [Upgrade and change management](upgrade.md).

## Incident Response

### Severity Levels

| Level | Example | Target |
| --- | --- | --- |
| P1 | Administrative entry point unavailable; large numbers of certificates cannot be renewed; private key compromise | Respond immediately and activate the incident process |
| P2 | A single task fails; a deployment target is abnormal; a notification channel is unavailable | Respond within 30 minutes |
| P3 | A single false monitoring alert; log anomalies; non-critical plugin issues | Handle during working hours |

### General Steps

1. **Confirm the scope**: which certificates, tasks, deployment targets, and target systems are affected.
2. **Preserve evidence**: do not clear logs; record the time, request ID, and error messages.
3. **Check dependencies and isolate changes**: DNS API, CA, network, reverse proxy, target systems, SMTP/IM; pause affected tasks to avoid repeated failures or overwriting certificates.
4. **Restore, verify, and review**: fix the dependency or roll back, restore from backup if necessary, rerun tasks, check certificates, deployments, and notifications, and record the root cause, timeline, improvements, and owners.

For specific symptoms and commands, see [Monitoring and troubleshooting](troubleshooting.md).

## Handover Checklist

- [ ] Service topology, hosts, ports, reverse proxy, and network policies.
- [ ] Custody of administrator accounts, TOTP/passkeys, recovery email, and the API key inventory.
- [ ] DNS/deployment/notification credential inventory and rotation schedule.
- [ ] Backup location, password, and recent restore-drill records; monitoring, alert routing, alert schedule, current version, upgrade window, rollback plan, open tasks, known issues, and upstream service contacts.

## Related Documentation

- [Deployment architecture](architecture.md)
- [Security baseline](security.md)
- [Upgrade and change management](upgrade.md)
