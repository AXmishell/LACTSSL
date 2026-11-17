# Plugin governance

LACTSSL provides DNS, certificate deployment, and notification capabilities through plugins. The main program only loads plugins, runs one-shot helper subprocesses, and manages installation, enablement, and removal. For enterprises, plugins are not ordinary extensions but a **supply chain boundary**:

- DNS plugins receive DNS provider API credentials.
- Deployment plugins receive certificates and private keys.
- Notification plugins receive channel tokens/webhooks.
- All plugins run under the service account and are equivalent to trusted code.

Plugins should therefore be subject to change management and admission review rather than being installed by any user at any time.

## 1. Plugin model

- **Merged packages by kind**: the official plugin repository builds merged packages for each kind: common DNS, large DNS cloud providers, deployment, and notification, plus a full DNS refactor package covering all 194 providers. Each package contains multiple implementations of that kind and shares a single Go runtime.
- **Install on demand**: release packages ship no plugins; install the merged package for the capability you need.
- **Protocol v1**: a plugin is an executable that communicates with the main program through subcommands such as `manifest`, `present/cleanup`, `deploy`, and `send`.
- **Separate directories**:
  - DNS: `<data_dir>/plugins/dns`
  - Deployment: `<data_dir>/plugins/deploy`
  - Notification: `<data_dir>/plugins/notify`
- **One-shot subprocesses**: each invocation starts the helper and exits when execution completes; helpers do not stay resident in memory.

## 2. Repository and trust

```yaml
plugins:
  repo:
    enabled: true
    url: "https://github.com/AXmishell/LACTSSL-Plugins/releases/download/plugin-index/index.json"
    channel: stable
    auto_update: false
    public_key: "t7Q4vd0B1zh3mRf0t6Q6qlBViL7cKhoipgpGWx5WSss="
    timeout_seconds: 30
    max_download_mb: 256
    check_interval_hours: 24
```

You can also change the repository URL, channel, auto-update, and public key on the Plugins page. UI settings are stored in the database and take precedence over `config.yaml`, but are not written back to the configuration file.

### 2.1 Verification chain

1. When a public key is configured, `index.json.sig` (Ed25519) is verified mandatorily.
2. The locally cached index is additionally protected with an HMAC using a server-side key.
3. Before download, the index must provide an asset SHA-256; a missing or malformed value is rejected outright.
4. After download, the asset SHA-256 is verified mandatorily.
5. During installation, `plugin.json`, the ID and version in the manifest, the platform, and `min_host` are validated.
6. Installation records store file digests; they are rechecked during discovery, registration, and before every DNS/deployment/notification invocation, and replaced files are refused for loading or execution.
7. Any failed step is rolled back, leaving no partial state.

When no signing public key is configured, a risk warning is displayed and unattended automatic updates are skipped. Disabling signature verification in production is not recommended.

## 3. Enterprise deployment models

### 3.1 Direct connection to the official repository

Applies to: networks with GitHub access, a small number of plugins, and acceptance of the official repository as the trust source.

Requirements:

- Configure the official public key and enforce signature verification.
- Disable automatic updates and upgrade manually within change windows.
- Record the version and digest of every installation/upgrade.

### 3.2 Internal mirror (recommended)

Applies to: production environments, offline/semi-offline environments, and scenarios requiring pinned versions and admission review.

Option 1: point the index at an internal HTTPS mirror:

```yaml
plugins:
  repo:
    url: "https://mirror.example.com/lactssl-plugins/index.json"
    public_key: "<enterprise signing public key>"
```

Option 2: use an offline directory:

```yaml
plugins:
  repo:
    url: "file:///srv/lactssl-plugins/index.json"
```

An internal mirror should:

- Sync the official index and assets, preserving the original SHA-256 values.
- Re-sign the index with the enterprise's own Ed25519 key, or at least verify the official signature before serving it.
- Sync only approved versions and do not follow `latest` automatically.
- Keep the mirror directory read-only; only the sync account may write to it.
- Record version, source, digest, approver, and rollback version.

### 3.3 Manual upload

You can upload a single helper on the Plugins page, or place files in the plugin directory and rescan. This suits temporary validation or third-party plugins.

- Uploads accept only a single helper executable, up to 256 MB.
- After upload, `manifest` is probed on the temporary file; the protocol, ID, and SHA-256 are verified, and only then is the file written to the plugin directory and rescanned.
- Overwriting a file with the same name is rejected by default; during upgrades, "Allow overwriting plugins with the same name" can be selected, and the old file is backed up first.
- Manually uploaded plugins are marked `manual` and do not participate in official repository upgrades.
- Upload only trusted files; helpers execute under the service account.

## 4. Installation and enablement

### 4.1 Installation

1. Open the Plugins page, confirm the repository URL, and click **Refresh index**.
2. Open the **Plugin market** and filter or search by DNS / deployment / notification.
3. Install the required plugins, then enable them in the corresponding lists.

Each plugin displays:

- Name, ID, version, and size.
- Whether an installation package exists for the current platform.
- Compatibility with the current host version.
- Installed version and update availability.

Newly installed plugins are disabled by default; upgrades or reinstalls preserve the previous enablement state.

### 4.2 Two-level enablement

Each row on the Plugins page corresponds to one helper file, and enablement has two levels:

1. **File level**: the toggle at the start of the row controls whether the helper participates in discovery and loading. When disabled, the file's `manifest` is no longer executed and its items are not registered at runtime; existing item selections are retained and restored when the file is re-enabled.
2. **Item level**: click **Manage items** to enable providers, deployment targets, or notification channels individually, or select several and enable/disable them in bulk. Items of newly installed plugins are still disabled by default.

Notes:

- When a file is disabled, its items are still displayed (from the host's cached manifest summary), but the toggles are grayed out; enable the file first.
- Merged packages (for example, `lactssl-dns-core` with 184 items) are not enabled all at once by the row toggle; enabling more than 10 items at once requires confirmation.
- Deleting a file or uninstalling a plugin also clears the manifest cache and file-level state, and removes all items of that file from the item allowlist.

## 5. Updates, rollback, and uninstall

- **Update**: click **Update** in the plugin market, or enable automatic updates to check periodically according to `check_interval_hours`.
- **Bulk update**: `POST /api/plugins/repo/update` with an empty request body updates all installed plugins.
- **Rollback**: for production, keep the previous version in the internal mirror; if an upgrade fails, reinstall the old version.
- **Uninstall**: only plugins installed from the plugin repository can be uninstalled; uninstalling deletes plugin files and installation records and removes entries from the enablement allowlist.
- **Delete files**: each plugin tab lists the actual helper files in the plugin directory (including manual uploads and legacy all-in-one bundles) and allows direct deletion; deletion removes all items provided by the file, its installation record, and its enablement state, and triggers an immediate rescan.

Upgrades follow the sequence download → verify → backup → atomic replace → manifest validation; any failed step restores the old files.

### 5.1 Recommended change process

1. Install the target version in staging.
2. Validate `manifest`, item lists, and critical paths (DNS issuance, deployment, notification).
3. Record the version, digest, and test results.
4. Upgrade within a production change window and monitor task logs.
5. Roll back to the previous version in the mirror on failure.

## 6. Package size and resources

Sizes shown in the plugin market are **extracted** sizes, that is, the actual helper size written to disk. Current official merged packages (linux/amd64) are approximately:

| Package | Extracted |
|---|---:|
| DNS common (184 providers) | about 21 MiB |
| DNS large cloud providers (10 providers) | about 39 MiB |
| DNS complete/refactored (194 providers) | about 18 MiB |
| Deployment (7 targets) | about 7 MiB |
| Notification (23 channels) | about 7 MiB |

- The common DNS package installs about 21 MiB and peaks at about 22 MiB of memory when running `manifest`.
- Install the roughly 39 MiB heavy package when you need large-SDK providers such as Google Cloud, Yandex, Sakura, Route53, and Azure DNS.
- To cover all 194 providers, install only the roughly 18 MiB complete/refactored package.
- Installing everything is about 73 MiB, still roughly 96% less than the approximately 1.7 GiB extracted total of 224 individual plugin packages.

Installation and update unpacking cause a transient memory peak; leave headroom in capacity planning. See [Deployment architecture](architecture.md).

## 7. Migrating legacy all-in-one plugins

Deployments that used `lactssl-dns-helper-extra`, `lactssl-deploy-helper`, or `lactssl-notify-helper` before upgrading see a migration notice on the Plugins page:

1. The system reads the legacy plugin's manifest and identifies providers that are currently enabled or still used by accounts/tasks.
2. It downloads and installs the corresponding merged packages; the merged packages replace the legacy per-provider/all-in-one helpers.
3. After all installations succeed, the legacy files are deleted and the directory is rescanned.
4. If any step fails, this installation is rolled back and the legacy plugin remains usable.

Legacy plugins are not deleted before migration completes, preventing interruption of sites that are currently issuing certificates.

## 8. Provider icons

DNS and deployment provider logos are plugin metadata delivered by the plugin repository; the main program no longer includes an icon table:

- **Before installation**: icons are inlined in the repository index `icons` and covered by index signature verification, and are used for display in the plugin market.
- **After installation**: `icons/` in the plugin package is written to `<data_dir>/plugin-icons/<kind>/<plugin ID>/`, so the credentials page can still display icons offline or with the repository disabled.
- **API**: the frontend fetches all icons at once through `GET /api/plugins/icons`; missing icons, corrupted icons, or older host versions fall back to initial-letter avatars.
- **Aliases**: legacy IDs such as `aliyun_cdn`, `baota_site`, and `qiniu_kodo` are mapped to canonical icons by `assets/icons/icons.json` in the repository.
- **Merged package icons**: only the package-level icon for the package ID is used; if no package-level icon is provided, none is displayed, or an item in the package can declare `hide_icon: true` to force it off.

Icons must be svg / png / webp and no larger than 64 KB each; invalid entries are discarded without affecting plugin installation or upgrades.

## 9. Security controls

- Use only trusted repositories; in production, configure a signing public key and enforce signature verification.
- Require an asset SHA-256 before download, and recheck file digests after installation.
- Deployment plugins handle private keys, are disabled by default, and must only be installed from trusted sources.
- Plugin directories use `0700` and helpers use `0755`; group and other users must not have write access.
- Symbolic links are not allowed; replaced files are refused for loading.
- Limit `memory_limit_mb`, `timeout_seconds`, and `max_output_kb` to prevent a faulty plugin from overwhelming the host.
- Plugins run under the service account; do not grant the `lactssl` user additional privileges, and do not place the plugin directory on a shared writable path.
- Record plugin installation/upgrade/uninstall operations for change auditing.

## 10. API

| Method | Path | Description |
|---|---|---|
| GET | `/api/plugins` | Plugin status, items, and helper files in the directory |
| GET | `/api/plugins/repo` | Repository configuration and index status |
| PUT | `/api/plugins/repo` | Update configuration |
| POST | `/api/plugins/repo/refresh` | Force-refresh the index |
| GET | `/api/plugins/repo/plugins` | Repository plugin list |
| POST | `/api/plugins/repo/install` | Install `{kind,id,version}` |
| POST | `/api/plugins/repo/update` | Update `{kind,id}` or everything |
| DELETE | `/api/plugins/repo/plugins/:kind/:id` | Uninstall |
| DELETE | `/api/plugins/files/:kind/:file` | Delete a helper file from the plugin directory |
| GET | `/api/plugins/repo/legacy` | Detect legacy all-in-one plugins |
| POST | `/api/plugins/repo/migrate` | Migrate `{kind}` |

Plugin management requires `plugins:read` / `plugins:write`. Upload, install, update, uninstall, and repository configuration changes are all sensitive operations.

## 11. Troubleshooting

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Index fetch fails | Repository URL, network proxy, TLS certificate | Open `index.json` in a browser first; check outbound policy |
| Signature verification fails | Public key does not match the repository signing key | Verify the public key; do not clear `public_key` casually in production |
| Platform unavailable | No plugin package exists for the current platform | Check `GOOS/GOARCH` and the index `assets` |
| Host version incompatible | Plugin `min_host` is higher than the current version | Upgrade the host or install a compatible version |
| ID conflict | The provider is already supplied by a legacy bundle or manual plugin | Complete the migration or remove the legacy file |
| Not visible after installation | The plugin kind is not enabled or its items are not enabled | Check `plugins.enabled`, `deploy_plugins.enabled`, `notify_plugins.enabled`, and the item toggles |
| File rejected | Group/other users have write access, symbolic link, or digest mismatch | Fix permissions or reinstall |

For more troubleshooting, see [Monitoring and troubleshooting](troubleshooting.md).

## 12. Plugin admission checklist

- [ ] Source: official repository or internal enterprise mirror, with a pinned version.
- [ ] Integrity: index signature, asset SHA-256, and installation record digest verified.
- [ ] Permissions: plugin directory and file permissions meet requirements.
- [ ] Risk: whether deployment plugins handle private keys; whether notification plugins hold long-lived tokens.
- [ ] Compatibility: host version, platform, and `min_host` match.
- [ ] Testing: DNS/deployment/notification paths validated in staging.
- [ ] Rollback: previous version and rollback steps retained.
- [ ] Records: version, digest, approver, installation time, and change ticket.

## Related documentation

- [DNS plugins](dns-plugins.md)
- [Deployment plugins](deploy-plugins.md)
- [Notification plugins](notify-plugins.md)
- [Security](security.md)
