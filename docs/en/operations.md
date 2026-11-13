# Operations

## Backup

Two parts need to be backed up:

1. `config.yaml` (or the file `LACTSSL_CONFIG` points to)
2. The entire `data/` directory

`data/` contains at least `lactssl.db`; while the service is running it may also contain `lactssl.db-wal` and `lactssl.db-shm`. Keep those as well.

### Backup and restore in the UI

"System Settings → Backup and Restore" provides online operations:

- **Download backup**: generates a ZIP backup containing a SQLite database snapshot (`lactssl.db`), the current configuration file (`config.yaml`), and version information (`manifest.json`). Re-entering the current account password is required before download (plus a one-time code when TOTP is enabled); the backup encryption password is independent of the account password. Backup contents are encrypted with AES-256-GCM, and the same password is required to restore. A lost password means the backup cannot be recovered.
- **Restore backup**: upload a ZIP backup or a standalone `.db` database file and confirm with the current password; encrypted backups also require the backup decryption password. The service writes the restored contents to `data/restore-pending/` and restarts automatically. Docker deployments are brought back by `restart: unless-stopped`; when running the binary directly, restart the process yourself.
- **Import on the setup page**: before the system is initialized, import a backup directly from "Advanced options" on `/install` to skip administrator creation. After importing, sign in with the account from the backup.

After restoring, sign in again with the administrator account from the backup. If the `config.yaml` in the backup uses a different `jwt_secret`, existing login sessions become invalid; if it uses a different `salt`, encrypted credentials in the database can only be decrypted with the matching configuration file, so always restore with the complete ZIP backup. If the backup's secret entry differs from the current one, check the new entry address in the startup log after restoring.

Backup packages contain keys and encrypted credentials; store them as confidential files.

### Command-line backup

Stopping the service before copying is recommended to ensure a consistent database:

```bash
# In the project directory
docker compose stop          # Docker deployment
# or stop the directly running process

tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

Backup packages contain keys and encrypted credentials; store them as confidential files.

## Restore

1. Stop the service.
2. Put `config.yaml` and `data/` from the backup back in their original locations.
3. Verify permissions: configuration file `0600`, data directory `0700`.
4. Start the service.

If only the database was restored and the `salt` in `config.yaml` was lost, encrypted DNS/deployment/notification credentials cannot be decrypted and must be entered again.

## Versioning

The `VERSION` file in the repository root is the single source of version information:

```bash
echo 1.0.0 > VERSION
make build
```

`make build`, Docker builds, and CI all read this file and inject the version into the binary through linker flags. After building, view the build version, build time, and architecture under "Settings → About".

When using `go run ./cmd/server` directly, the version is shown as `dev`.

### Automatic sync when pushing tags

When a tag such as `v1.0.1` is pushed, the `Version Sync` workflow writes the tag version back to `VERSION` on the default branch:

- The tag must be a semantic version `vX.Y.Z`, optionally with a pre-release suffix such as `-rc.1`; the `v` prefix is removed before writing.
- The commit the tag points to must be on the default branch, otherwise the workflow only warns and skips the sync.
- If `VERSION` already matches the tag, no commit is created.
- The sync commit message is `chore(release): 1.0.1` and is pushed with `GITHUB_TOKEN`, so CI is not triggered again.
- If branch protection is enabled on the default branch, GitHub Actions must be allowed to write, otherwise the workflow fails after retrying and asks you to update manually.

The tag determines the release version; `VERSION` is aligned with the latest tag after a release for subsequent local builds.

### Creating a release builds automatically

Publishing a GitHub Release triggers the `Release` workflow, which builds and uploads artifacts for each platform:

```bash
gh release create v1.0.1 --generate-notes
```

The workflow checks out the source from the tag, builds the frontend, embeds it in the binary, and produces:

- `lactssl_<version>_linux_amd64.tar.gz`
- `lactssl_<version>_linux_arm64.tar.gz`
- `lactssl_<version>_darwin_amd64.tar.gz`
- `lactssl_<version>_darwin_arm64.tar.gz`
- `lactssl_<version>_freebsd_amd64.tar.gz`
- `lactssl_<version>_freebsd_arm64.tar.gz`
- `lactssl_<version>_windows_amd64.zip`
- `checksums.txt` (SHA-256)

Archives contain the binary, `README.md`, `LICENSE`, and `config.yaml.example`. The version comes directly from the tag (with the `v` prefix removed) and does not depend on the `VERSION` file, so builds are correct even if `VERSION` has not been synced yet.

Docker images for `linux/amd64` and `linux/arm64` are also built and pushed to GHCR:

```text
ghcr.io/axmishell/lactssl:<version>
ghcr.io/axmishell/lactssl:<major>.<minor>
ghcr.io/axmishell/lactssl:<major>
ghcr.io/axmishell/lactssl:latest
```

Pre-releases only push the full `<version>` tag and do not update `latest`, `<major>.<minor>`, or `<major>`. After the first release, GHCR packages are private by default and must be changed to Public manually in the package settings.

Notes:

- The workflow only triggers when a Release is formally published; with `gh release create --draft`, the build starts after you publish it.
- Before first use, `.github/workflows/release.yml` must already be in the commit the tag points to; release events use the workflow file from the tag.
- Files pre-uploaded with `gh release create <tag> <files>...` that share names with automatic artifacts are overwritten by the workflow with `--clobber`; passing files at creation time is not recommended.
- Release assets take a few minutes to finish building; if you need "make public only after assets are ready", push the tag first and create the Release after CI finishes (this requires a separate tag-push flow).
- Rebuild a specific tag with `gh workflow run release.yml -f tag=v1.0.1`, or run it manually from the Actions page.
- If a Release is created by the workflow's own `GITHUB_TOKEN`, it will not trigger the `Release` workflow again; this flow uses a manual `gh release create`, so it is unaffected.

## Upgrade

Source deployment:

```bash
git pull
make build
# Stop the old process, then start ./bin/lactssl
```

Docker deployment:

```bash
docker compose up -d --build
```

Back up `config.yaml` and `data/` before upgrading. The database schema is migrated automatically at startup.

## Logs

- UI "Logs" page: task runs, certificate issuance, deployment, notifications, monitoring, and settings changes.
- Process output: startup information, secret entry, HTTP errors, and scheduler status.
- Docker: `docker compose logs -f lactssl`.

## FAQ

**Forgot the secret entry**
Check the entry address printed in the startup log, or look at the `security.entry` setting in the database. You can also override it with `LACTSSL_ENTRY` at startup.

**Cookies misbehave / rate limiting is inaccurate behind a reverse proxy**
Configure `trusted_proxies` and make sure the proxy passes `X-Forwarded-For` and `X-Forwarded-Proto`; see [Security](security.md).

**The service does not load the new certificate after renewal**
Configure a reload command on the deploy node in the task workflow, such as `nginx -s reload` or `systemctl reload nginx`. Target credentials only store authentication and connection details and no longer store paths or reload commands.

**The database file keeps growing**
The service runs maintenance cleanup every 6 hours; check the Logs page to confirm the scheduler is running. Historical certificates can be deleted from "Certificate Management" using "Clean up" filters by status, expiry/revocation age, issuance time, domain, issuer, and source, with the option to keep the latest certificate for each task; certificate references in affected tasks are cleared after cleanup. ACME diagnostic records can be cleaned up in "ACME Diagnostics" by endpoint, status, and unused age; authorizations and challenges of invalid orders are deleted in cascade, and deleting an account also deletes its historical orders and requires the corresponding client to register again.
