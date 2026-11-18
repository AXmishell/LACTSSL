# Upgrade and Change Management

Version upgrades are among the highest-risk routine changes for a certificate platform: database migrations must be reversible, and upgrades must not introduce outages during periods of concentrated certificate expiry. This document covers pre-upgrade assessment, migration mechanics, program upgrades, rollback, and change window checklists.

## 1. Change Principles

- **Back up before upgrading**: `config.yaml` plus the entire `data/`, or use the encrypted UI backup.
- **Staging before production**: validate at least the four paths: issuance, deployment, notification, and API.
- **Pin versions**: do not run an untraceable `latest` tag in production long term.
- **Keep rollback points**: database migration snapshots and old binary/image tags.
- **Avoid peak periods**: do not overlap with concentrated certificate expiry, bulk deployments, or business release windows.
- **Maintain observability**: after upgrading, observe at least one scheduling cycle and one task run.

## 2. What an Upgrade Does

LACTSSL has accumulated historical data formats and compatibility branches across releases. `internal/upgrade` automatically runs legacy migrations the first time a new version starts; users can also sign in and visit `/upgrade` for preflight checks, manual migration, and database optimization.

### 2.1 Automatic Migration Sequence

1. Read and validate `config.yaml`.
2. Open SQLite and create the base tables.
3. Create the upgrade tool and read `upgrade.schema_version`.
4. If the version is behind, back up the database and configuration first, then run the migration steps in order.
5. Write the new version number and run low-cost database optimization.
6. Continue initializing accounts, the scheduler, and the HTTP service.

- Automatic migration is controlled by `upgrade.auto` and enabled by default.
- Migration steps are idempotent; on failure the version number is not advanced, and the failed step is retried on the next start.
- Pre-migration backups are stored in `data/upgrade-backups/<UTC time>/`; the latest 5 are retained by default.

### 2.2 Migration Scope

| Issue | Legacy behavior | Upgrade tool handling |
| --- | --- | --- |
| Database has no format version | Structure could only be inferred from `ensureColumn` and scattered migration keys | Writes `upgrade.schema_version` and runs idempotent steps in version order |
| Sensitive fields in plaintext | Legacy TOTP secrets and certificate private keys were stored directly in the database | Encrypts with AES-256-GCM; records already encrypted are skipped |
| Legacy key file | `data/jwt.secret` and `config.yaml` held duplicate keys | After confirming the configuration has stored the key, moves it to the upgrade backup directory and cleans up |
| Global EAB credentials | `acme.eab_kid` / `acme.eab_hmac` applied only to the default CA | Migrates to independent settings for the default CA and removes the old keys |
| Deployment parameters mixed into credentials | Paths, sites, and reload commands were stored in target credentials | Splits them into deploy nodes in the task workflow; credentials retain authentication information only |
| Duplicate deployment targets | Legacy IDs such as `qiniu_cdn` and `baota_site` were scattered | Merges them into canonical IDs and writes the website/panel or CDN/Kodo selection into the node |
| Legacy notification settings | Gotify / ServerChan used separate setting keys | Converts to generic notification channel configuration, then removes the old keys |
| Plugin migration leftovers | Legacy full helpers, disabled lists, and missing enabled whitelists | Initializes DNS/deployment/notification whitelists and preserves the default enabled state of built-in entries |
| Legacy verification modes | Historical values such as `password_passkey` | Normalizes to `passkey`; invalid values fall back |
| Database bloat | Free pages remain after deleting certificates, logs, and ACME orders | `VACUUM` reclaims space and `ANALYZE` updates statistics |
| Oversized WAL | The `-wal` file keeps growing after long uptime | `wal_checkpoint(TRUNCATE)` folds and truncates it |
| Missing hot-path indexes | Certificate filtering, scheduling, and log cleanup perform full table scans | Automatically adds indexes; safe to run repeatedly |
| Split log tables | System logs lived in `system_logs`, operation logs in `logs` | Copies system logs to `logs(category=runtime)` and keeps the old table for rollback |
| Outdated configuration structure | Missing new sections such as `plugins.repo`, `notify_plugins`, `log.system`, and `upgrade` | Fills in missing fields for the current version, preserves comments, and normalizes equivalent forms |

## 3. Pre-Upgrade Checks

- [ ] Read the release notes and migration notes for the target version.
- [ ] Confirm the current version, data format version, and pending migration steps.
- [ ] Back up `config.yaml` and `data/`, and verify that the backup is readable.
- [ ] Confirm sufficient disk space (`VACUUM` requires temporary space).
- [ ] Validate issuance, deployment, notification, and API in staging.
- [ ] Confirm that the old binary/image tag is available.
- [ ] Notify stakeholders and choose an off-peak window.
- [ ] Pause batch tasks to avoid concurrent writes during the upgrade.

## 4. Upgrade Methods

### 4.1 Docker (Recommended)

```bash
# Pin the target version
LACTSSL_VERSION=1.2.3 docker compose pull
LACTSSL_VERSION=1.2.3 docker compose up -d
docker compose logs -f lactssl
```

- Use explicit version tags to simplify rollback.
- Do not use self-upgrade inside a container: recreating the container returns to the image version.
- The official Compose file uses a read-only root filesystem by default, so the program directory is not writable and the page reports that self-upgrade is unsupported; this is expected behavior.

### 4.2 Binary

```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo chmod 0755 /opt/lactssl/lactssl
sudo systemctl start lactssl
sudo journalctl -u lactssl -f
```

Notes:

- Upgrading preserves the original file permission bits but not extended attributes such as `setcap`; deployments using privileged ports or file capabilities must reapply them after upgrading.
- Self-upgrade by directly `exec`-ing the new binary works on Linux/macOS/FreeBSD; on Windows, the service manager must restart the process.
- `dev` and source builds, as well as release packages without either `checksums.txt` or an asset SHA-256, are rejected for self-upgrade; replace the binary manually and verify it yourself.

### 4.3 Program Self-Upgrade (Optional)

The top of the `/upgrade` page provides **Program upgrade**:

- **Check for updates**: queries Releases according to `upgrade.program.channel`; `stable` accepts only final releases, while `beta` includes pre-releases.
- **Install update**: downloads `lactssl_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), enforces SHA-256 verification using `checksums.txt`, extracts `lactssl`, and replaces the current executable.
- **Release signature**: after `upgrade.program.public_key` is configured, verification of `checksums.txt.sig` is enforced; missing or invalid signatures are rejected.
- **Backup and rollback**: before replacement, the old binary is copied to `<data_dir>/upgrade-backups/program/`; the latest 3 are retained by default.
- **Restart to apply**: after installation, the page prompts for a restart; by default, restarting clears sessions, and with `security.session_persistence` enabled the existing session remains usable.

Limitations:

- Read-only filesystems, platforms without release packages, and `dev` builds are shown as not upgradeable.
- Upgrading inside a container does not change the image; recreating the container returns to the image version.
- The GitHub API is subject to anonymous rate limits when no token is configured; configure one with `upgrade.program.token`.
- When an outbound proxy is required, configure it through the system proxy environment variables (Go HTTP client behavior).

## 5. Post-Upgrade Verification

1. Look for "data format upgrade completed" in the startup logs.
2. Sign in and open `/upgrade`; confirm that every step is "done" or "skipped".
3. Check the version, build time, and architecture.
4. Verify administrator login and TOTP/passkeys.
5. Check the certificate list, tasks, deployment credentials, notification channels, and plugins.
6. Run a test task manually and confirm issuance, deployment, and notification.
7. Check API key permissions and calls.
8. Observe at least one scheduling cycle (30 seconds) and one automatic renewal.

## 6. Rollback

### 6.1 Data Rollback

The upgrade tool creates a snapshot before structural changes:

```text
data/upgrade-backups/20260101-120000/
├── lactssl.db
└── config.yaml
```

Steps:

1. Stop the service.
2. Replace the current database with `lactssl.db` from the backup, and delete `lactssl.db-wal` and `lactssl.db-shm`.
3. To roll back the configuration as well, replace the current configuration with `config.yaml` from the backup.
4. Start the old version of the program.

Notes:

- Older programs may not be able to read data written by a newer format; for cross-major-version rollbacks, roll back both the binary and the backup data.
- Do not manually delete `upgrade.schema_version`; otherwise the migration steps will run again.
- If automatic migration fails, the service refuses to start and prints the failed step; fix permissions, disk, or data issues and restart directly to retry.

### 6.2 Program Rollback

Program upgrade backups are stored in:

```text
data/upgrade-backups/program/
├── 20260101-120000-lactssl-1.2.2
└── 20260102-090000-lactssl-1.2.3
```

Steps:

1. Stop the service.
2. Overwrite the current binary with the backup file and restore execute permissions (`chmod 0755`).
3. If data rollback is also required, follow the previous section.
4. Start the service and confirm the version number and data format migration status.

## 7. Manual Migration and Database Optimization

After signing in, visit `/upgrade` (sidebar: **System → Upgrade**):

- **Preflight**: shows the program version, data format version, migration step status, database integrity, free pages, configuration changes, and risk warnings; it does not modify any data.
- **Run migration and optimize**: runs the migration steps again, then optimizes the configuration file and database.
- **Optimize database and configuration only**: does not rerun migrations; suitable for routine maintenance.
- **Reclaim disk space**: executes `VACUUM` when enabled, which briefly monopolizes the database; use during off-peak hours.

Results are written to the system logs and the `upgrade.last_run` setting.

### 7.1 Database Optimization Details

- `PRAGMA quick_check(1)`: fast integrity check.
- Base table structure uses `PRAGMA user_version` to record the version; when the structure is current, table creation and `ensureColumn` scans are skipped.
- Adds hot-path indexes: certificate status and expiry, task enabled state and next run, monitoring checks, log level and time, ACME orders/challenges, and deployment records.
- `PRAGMA optimize`: updates statistics based on query patterns.
- `ANALYZE`: fully analyzes tables and indexes (manual optimization).
- `PRAGMA wal_checkpoint(TRUNCATE)`: folds and truncates the WAL.
- `VACUUM`: reclaims free pages left by deleted data and reduces file size.

`VACUUM` does not delete business data, but it rewrites the database file. Before execution, the tool takes a database snapshot; insufficient disk space may cause a failure, which is returned as a warning and does not prevent the service from continuing to run.

### 7.2 Configuration Optimization Details

- Fills in all standard configuration sections and default values for the current version.
- Normalizes whitespace in `server.addr`, `acme.addr`, and similar fields.
- Removes trailing slashes from `security.public_url` and `acme.base_url`.
- Lowercases `log.level`, `log.format`, `log.system.level`, and `plugins.repo.channel`.
- Deduplicates and trims `server.trusted_proxies`.
- Checks risks such as ACME without a fixed endpoint, plugin repository without a signing public key, deployment plugins handling private keys, and overly short secrets.
- Before writing, fully validates using the startup loading logic; validation failures do not overwrite the original file.
- Edits YAML nodes to preserve comments and field order.

## 8. Configuration Options

```yaml
upgrade:
  auto: true              # Run legacy migrations automatically at startup
  auto_optimize: true     # Run low-cost optimization at startup; no VACUUM
  vacuum: true            # Allow VACUUM during manual optimization from /upgrade
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    token: ""
```

When `upgrade.auto` is disabled, the startup log reports pending migration data, but the service continues to start. Sign in to `/upgrade` and run the migration promptly to prevent new code from reading old-format data; historical system logs are not shown on the Logs → System tab until the log-merge step completes.

## 9. API

The upgrade tool uses the existing login session; endpoints are always under `/api`:

| Endpoint | Method | Permission | Description |
| --- | --- | --- | --- |
| `/upgrade` | GET | `upgrade:read` | Preflight status |
| `/upgrade/run` | POST | `upgrade:write` | Migration, configuration optimization, and database optimization |
| `/upgrade/optimize` | POST | `upgrade:write` | Optimize configuration and database only |
| `/upgrade/program` | GET | `upgrade:read` | Program upgrade status |
| `/upgrade/program/check` | POST | `upgrade:write` | Query GitHub Releases immediately |
| `/upgrade/program/apply` | POST | `upgrade:write` | Download and install the specified version |
| `/upgrade/program/restart` | POST | `upgrade:write` | Restart the service after installation |

Request bodies may include boolean fields such as `vacuum`, `analyze`, `config`, and `optimize`. Write endpoints require only a login session or an API key with the corresponding permission.

## 10. Change Window Checklist

- [ ] Stakeholders have been notified, and the window avoids concentrated certificate expiry.
- [ ] Backups are complete and the restore password has been verified.
- [ ] Disk space has been confirmed (backup + `VACUUM` temporary space).
- [ ] The old binary/image tag is available.
- [ ] Batch tasks and automated triggers have been paused.
- [ ] The upgrade and verification are complete.
- [ ] Batch tasks and automation have been resumed.
- [ ] The change record, version, time, and result have been documented.

## Related Documentation

- [Operations manual](operations.md): backup, monitoring, and incident response.
- [Configuration reference](configuration.md): fields in the `upgrade` section.
- [Monitoring and troubleshooting](troubleshooting.md): startup failures, migration failures, and session invalidation.
