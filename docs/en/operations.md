# Operations Manual

This document is intended for the on-call and operations teams responsible for the day-to-day operation of LACTSSL. It covers backup and restore, monitoring and alerting, capacity maintenance, change and upgrade management, and incident response. The objective is to make the service transferable, auditable, and recoverable after failure.

## 1. Service Objectives and Responsibilities

LACTSSL does not include an SLA. Each enterprise should define the following internally:

| Item | Recommended definition |
| --- | --- |
| Service tier | Certificate issuance and deployment as a critical supporting system, or classified by business impact |
| Availability hours | Aligned with the dependent DNS, CA, reverse proxy, and deployment targets |
| RPO | At least one backup per day; hourly for critical environments |
| RTO | Based on the results of a "provision host + restore + startup verification" exercise |
| Change window | Avoid periods of concentrated certificate expiry and business peak load |
| Alert response | Respond to task failures and certificate anomalies within 15–30 minutes (per enterprise policy) |

Do not treat LACTSSL process liveness as business availability. True business availability also depends on the DNS API, CA, deployment targets, and notification path.

## 2. Backup and Restore

### 2.1 Backup Scope

Back up both of the following:

1. `config.yaml` (or the path specified by `-config`)
2. The entire `data/` directory

`data/` contains at least `lactssl.db`; at runtime it may also contain `lactssl.db-wal` and `lactssl.db-shm`. The database, plugins, logs, and upgrade backups all reside under `data/`.

Backing up only the database loses the `salt`, leaving encrypted credentials undecryptable; backing up only the configuration loses business data. The two must be kept together.

### 2.2 Encrypted Backup in the UI (Recommended)

**System Settings → Backup and Restore** provides online operations:

- **Download backup**: generates a ZIP containing a SQLite snapshot, the current `config.yaml`, and version information in `manifest.json`.
- Re-enter the current account password before download; a one-time code is also required when TOTP is enabled.
- The backup encryption password is independent of the account password and must be at least 12 characters.
- Backup contents are encrypted with PBKDF2-HMAC-SHA256 (600,000 iterations) + AES-256-GCM; the same password is required to restore.
- If the password is lost, the backup cannot be recovered.

Recommendations:

- Store backup files on separate media or object storage, not under `data/`.
- Store the backup password in the enterprise password manager, separately from the backup files.
- Record the backup time, size, checksum, and storage location.
- Retain backups for at least 7–30 days; extend retention for critical environments as required by compliance.

### 2.3 Command-Line Backup

When a file-level backup is required, stop the service before copying to ensure SQLite is in a consistent state:

```bash
# Docker
docker compose stop

# Or stop the systemd service
sudo systemctl stop lactssl

tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

If downtime is not possible, prefer the encrypted UI backup; copying a running SQLite database directly may produce an inconsistent snapshot.

### 2.4 Restore

#### Restore in the UI

1. Upload a ZIP backup or a standalone `.db` file.
2. Enter the current password to confirm; encrypted backups also require the backup decryption password.
3. The service writes the restored content to `data/restore-pending/` and restarts automatically.
4. Docker deployments are brought back by `restart: unless-stopped`; when running the binary directly, restart the process yourself.
5. Sign in with the administrator account from the backup.

#### Manual Restore

1. Stop the service.
2. Put `config.yaml` and `data/` from the backup back in their original locations.
3. Verify permissions: configuration file `0600`, data directory `0700`, owned by the service user.
4. Start the service.
5. Check the secure entry, administrator login, certificates, tasks, and plugins.

#### Restore Considerations

- After restoring, sign in with the administrator account from the backup.
- By default, every service start clears sessions and rotates the JWT signing subkey, so existing logins become invalid.
- If the `config.yaml` in the backup uses a different `jwt_secret`, old tokens fail validation.
- If a different `salt` is used, encrypted credentials in the database can only be decrypted with the matching configuration; therefore, always restore from a complete ZIP backup.
- If the secure entry in the backup differs from the current one, check the startup log for the new entry.
- If only the database is restored and the `salt` in `config.yaml` is lost, encrypted DNS, deployment, and notification credentials cannot be decrypted and must be re-entered.

### 2.5 Restore Drill

Perform at least once per quarter:

- [ ] Restore the most recent backup in an isolated environment.
- [ ] Verify administrator login, TOTP/passkeys, and the recovery email.
- [ ] Verify the certificate list, tasks, deployment credentials, and notification channels.
- [ ] Run a test task and deploy to a non-production target.
- [ ] Record the actual RTO, issues, and improvement items.
- [ ] Confirm that the backup password is still held by an authorized person.

## 3. Monitoring and Alerting

### 3.1 Monitoring Signals

| Signal | Collection method | Recommended threshold/action |
| --- | --- | --- |
| Process liveness | TCP port probe | Every 30–60 seconds; alert after 3 consecutive failures |
| Authentication health | `GET /api/status` + `system:read` API key | Every 1–5 minutes; alert on non-200 or high latency |
| Task failures | Operation logs or notifications | Alert on failure; distinguish DNS/CA/deployment/notification |
| Certificate nearing expiry | Dashboard, site monitoring | Tiered reminders at 30/14/7 days |
| Deployment failures | Deployment records/notifications | Alert immediately; check the target system and reload command |
| Disk | Host/platform monitoring | Warn above 80% usage; alert above 90% |
| Backup | Backup jobs/file checks | Alert on failure or when the interval is exceeded |
| HTTP 5xx | Reverse proxy logs | Set thresholds against the baseline |
| Login anomalies | Operation logs/system logs | Alert on repeated failures or unusual source IPs |

### 3.2 Alert Routing

- Task failures, certificate anomalies, and deployment failures go to the operations on-call channel.
- Security events (unusual logins, secure entry probing, plugin verification failures) go to the security channel.
- Backup failures and insufficient disk space go to the platform channel.
- Do not send test notifications to the production on-call group; use a separate channel.

Notification channel availability depends on external services. Configure at least two independent channels (for example, email + enterprise IM) to avoid a single point of failure.

### 3.3 Monitoring Practices to Avoid

- Do not use bare `/` or `/login` for anonymous health checks: requests that have not passed the secure entry return 404, which is expected.
- Do not store administrator passwords or long-lived full-privilege API keys in monitoring systems.
- Do not include certificate private keys or backup passwords in alert content.

## 4. Routine Maintenance

### 4.1 Daily

- Review task failures, deployment failures, and site monitoring anomalies.
- Confirm that the certificate expiry dashboard has no new high-risk items.
- Check that backups are generated as scheduled.

### 4.2 Weekly

- Check that automatic renewal tasks are scheduled correctly.
- Review errors, rate limiting, and unusual logins in the system logs.
- Verify newly added or disabled API keys and credentials.

### 4.3 Monthly

- Check disk, database size, WAL, and log retention.
- Check plugin versions and security advisories.
- Spot-check operation logs and change records.
- Verify notification channels.

### 4.4 Quarterly

- Complete the backup and restore drill.
- Review administrator accounts, recovery email, and TOTP/passkeys.
- Review API key permissions and expiry.
- Assess upgrade windows and rollback plans.

## 5. Capacity and Cleanup

### 5.1 Capacity to Monitor

| Item | Growth source | Handling |
| --- | --- | --- |
| SQLite | Certificates, task logs, operation logs, system logs, ACME orders | Purge historical data, optimize the database, archive logs |
| WAL | Long uptime, frequent writes | `wal_checkpoint(TRUNCATE)` performed by the upgrade/optimization tool |
| Plugins | Old files accumulate from installs/updates | Uninstall unused plugins and remove old helpers |
| Upgrade backups | Migration snapshots, old binaries | Purge per retention policy |
| Backup storage | Daily/weekly backups | Set retention and offsite copies |

### 5.2 Cleanup Entry Points

- **Certificate Management → One-click cleanup**: filter by status, expiry/revocation age, issuance time, domain, issuer, and source; optionally retain the latest certificate for each task. Certificate references in affected tasks are cleared after cleanup.
- **ACME Diagnostics → Cleanup**: remove invalid orders and accounts by endpoint, status, and unused age; deleting an account deletes historical orders but does not affect issued certificates.
- **Logs → System → Clear**: clearing is recorded in the operation log; export logs first.
- **Upgrade/optimization**: run database optimization during off-peak hours; `VACUUM` briefly monopolizes the database and rewrites the file, so reserve disk space.

### 5.3 Database Maintenance

- The service runs maintenance cleanup every 6 hours; confirm in the logs page that the scheduler is running.
- On first start, a new version automatically runs historical migrations and creates a pre-migration snapshot in `data/upgrade-backups/`.
- To reclaim disk space, visit `/upgrade` during off-peak hours and run "Optimize database & config only" with VACUUM enabled.
- Ensure a usable backup and sufficient disk space before running; a `VACUUM` failure is returned as a warning and does not prevent the service from continuing to run.

## 6. Versions and Releases

### 6.1 Version Identification

- The git tag is the sole source of the version number; the repository does not maintain a `VERSION` file.
- Local builds derive the version from `git describe --tags --always --dirty` and strip the `v` prefix.
- Build scripts inject the version with `-ldflags -X 'lactssl/internal/api.Version=...'`.
- View the build version, build time, and architecture under **Settings → About**.
- `go run` and development builds display `dev`.

### 6.2 Release Channels

- Stable release tags include `latest`, `<version>`, `<major>.<minor>`, and `<major>`.
- Pre-releases push only the full version tag and do not overwrite `latest`.
- Pin production to an explicit version; do not run an untraceable `latest` tag long term.

### 6.3 Release Artifacts

Releases build Linux/macOS/FreeBSD/Windows binaries, `checksums.txt`, and `linux/amd64` and `linux/arm64` images. Archives contain the binary, `README.md`, `LICENSE`, and `config.yaml.example`.

For release process, signing, and local build details, see [Development: source and build](development.md).

## 7. Upgrade and Change

Before upgrading:

1. Read the release notes and migration notes for the target version.
2. Back up `config.yaml` and `data/`.
3. Validate critical paths (issuance, deployment, notifications, API) in staging.
4. Confirm the rollback version and rollback steps.
5. Choose an off-peak window and notify stakeholders.

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

After upgrading:

- Look for "data format upgrade completed" in the logs.
- Sign in to `/upgrade` and confirm step status.
- Verify certificates, tasks, deployments, notifications, and the API.
- Observe at least one scheduling cycle (at least 30 seconds) and one task run.

For complete migration, rollback, and program upgrade instructions, see [Upgrade and change management](upgrade.md).

## 8. Incident Response

### 8.1 Severity Levels

| Level | Example | Target |
| --- | --- | --- |
| P1 | Administrative entry point unavailable; large numbers of certificates cannot be renewed; private key compromise | Respond immediately and activate the incident process |
| P2 | A single task fails; a deployment target is abnormal; a notification channel is unavailable | Respond within 30 minutes |
| P3 | A single false monitoring alert; log anomalies; non-critical plugin issues | Handle during business hours |

### 8.2 General Steps

1. **Confirm the scope of impact**: which certificates, tasks, deployment targets, and business systems are affected.
2. **Preserve evidence**: do not clear logs; record the time, request ID, and error messages.
3. **Check dependencies**: DNS API, CA, network, reverse proxy, target systems, SMTP/IM.
4. **Isolate changes**: pause affected tasks to avoid repeated failures or overwriting certificates.
5. **Restore service**: fix the dependency or roll back the change; restore from backup if necessary.
6. **Verify**: rerun tasks and check certificates, deployments, and notifications.
7. **Review**: record the root cause, timeline, improvement items, and owners.

For specific symptoms and commands, see [Monitoring and troubleshooting](troubleshooting.md).

## 9. Handover Checklist

- [ ] Service topology, hosts, ports, reverse proxy, and network policies.
- [ ] Custody of administrator accounts, TOTP/passkeys, and the recovery email.
- [ ] API key inventory: purpose, permissions, expiry, and owner.
- [ ] DNS/deployment/notification credential inventory and rotation schedule.
- [ ] Backup location, backup password, and records of recent restore drills.
- [ ] Monitoring items, alert routing, and on-call schedule.
- [ ] Current version, upgrade window, and rollback plan.
- [ ] Open tasks, known issues, and vendor contacts.

## Related Documentation

- [Deployment architecture](architecture.md)
- [Security baseline](security.md)
- [Upgrade and change management](upgrade.md)
- [Monitoring and troubleshooting](troubleshooting.md)
