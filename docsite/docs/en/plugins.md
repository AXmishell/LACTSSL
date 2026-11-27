# Plugin governance
Official DNS, deployment, and notification implementations are compiled into the host; uploaded third-party helpers run as one-shot subprocesses and are sandboxed by default. `internal/plugin/` holds the official implementations, and `plugins/` is the external plugin SDK.

## Model
- **Built-in plugins**: official DNS/deploy/notify implementations ship with the host; no online installation.
- **Uploaded plugins**: administrators may upload a single helper for testing or third-party extensions; disabled by default and run as a subprocess when enabled.
- **Two-level enablement**: category (`plugins.enabled`, `deploy_plugins.enabled`, `notify_plugins.enabled`), then entry (DNS providers, deploy targets, notification channels).
- **No plugin repository**: no index, signature index, marketplace, or auto-update; official updates ship with host releases.

Protocol v1 remains:

```text
manifest / present / cleanup / deploy / options / plan / send
```

Built-in implementations are called in-process; uploaded helpers use stdin/stdout JSON.

## Configuration
```yaml
plugins:
  enabled: true
  dir: ""                 # uploaded DNS helpers; default <data_dir>/plugins/dns
  sandbox: strict         # strict / basic / off
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64

deploy_plugins:
  enabled: true           # deploy targets are on by default; plugins receive private keys
  dir: ""
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64

notify_plugins:
  enabled: true
  dir: ""
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64
```

The `sandbox` setting applies **only to uploaded/external helpers**; built-ins are in-process and never enter the sandbox. On Linux, `strict` uses Landlock rules, rlimits, `no_new_privs`, and a separate process group; without kernel/container support the host falls back to `basic` (minimal environment, private temp dir, timeout/RSS/output limits) and logs the downgrade once. If the host still runs as root without `LACTSSL_RUN_UID`/`LACTSSL_RUN_GID`, uploaded helpers are refused because Landlock cannot stop root bypass via mount/ptrace. `off` is for controlled debugging only.

## Built-ins and memory
- Built-ins are compiled into the host; disabling a category or entry does not unload code but avoids registering the implementation and constructing providers.
- Build tags reduce size: `make backend-no-dns` excludes built-in DNS (idle RSS ~36 MB), while official releases are full builds (idle RSS ~70 MB). Most full-build memory comes from lego v5 code pages, not plugin metadata (~1 MB); enabling all 235 providers adds only ~3–5 MB.
- DNS built-ins use a global constructor lock to set and restore lego v5 environment variables; `present/cleanup` can run concurrently.

## Enable and disable

### Category
The category switch controls whether built-in implementations of that kind are registered. When disabled, its built-in entries are unavailable and uploaded helpers of the same kind are not executed, while saved accounts, tasks, and allowlists are preserved.

### Entries
- DNS: no providers are enabled by default; enable them on the Plugins page.
- Deploy: the category and 25 canonical targets are enabled by default; disable any you do not need.
- Notify: 23 channels are enabled by default. Enabling more than 10 entries at once requires confirmation.

## Uploaded plugins and sandbox
1. Choose the kind and helper file; run `manifest` inside the sandbox to validate protocol, ID, version, digest, and platform.
2. Write to the plugin directory (new files are disabled by default), then enable the file and its entries.

Limits:

- A single executable up to 256 MB; symlinks and group/other-writable files are rejected, and uploaded plugins cannot occupy built-in IDs/aliases.
- `strict` sandbox: only system CA/DNS files and the helper itself are readable; only a private temp dir is writable; data dir, config, database, and other plugins are blocked.
- The sandbox is not network isolation: uploaded plugins can make outbound connections, so upload only trusted code.

## Updates, rollback, uninstall
- Built-ins update with the host; there is no separate install/update/uninstall, and enablement is preserved across upgrades.
- Uploaded plugins are re-uploaded by the administrator; the old file is backed up and restored on failure. Deleting a file removes its digest, metadata, and allowlist entries.

## Legacy repository plugins
- Official plugins are now built in; disable or delete old helper files. Accounts, tasks, and allowlists are preserved.
- Custom plugins not covered by built-ins remain as legacy/uploaded helpers under the `strict` sandbox by default; they have no update source, so replace or remove them.

## Security
- Built-ins are part of the host supply chain and ship with the signed release/image digest.
- Uploaded plugins run sandboxed by default; do not set `sandbox: off` in production. Deploy plugins receive private keys, so keep only trusted targets enabled.
- Plugin directory `0700`, helper `0755`, no symlinks, no group/other write.
- Uploaded helper digests are rechecked on discovery, registration, and every call.
- Plugin management requires `plugins:read` / `plugins:write`; uploads and toggles are audited, and a strict-sandbox downgrade is written to the host startup log.

## API

| Method | Path | Description |
|---|---|---|
| GET | `/api/plugins` | built-in/uploaded status, entries, files |
| POST | `/api/plugins/rescan` | rescan uploaded plugins |
| POST | `/api/plugins/upload` | upload a helper |
| DELETE | `/api/plugins/files/:kind/:file` | delete an uploaded helper |
| PUT | `/api/plugins/files/:kind/:file` | file-level enable/disable |
| PUT | `/api/plugins/providers/:id` | DNS entry toggle |
| PUT | `/api/plugins/deployers/:id` | deploy entry toggle |
| PUT | `/api/plugins/notify-channels/:id` | notify entry toggle |
| GET | `/api/plugins/icons` | icons |

Repository APIs have been removed.

## Troubleshooting

| Symptom | Cause | Action |
|---|---|---|
| Built-in entry unavailable | category off or entry disabled | check category config and entry allowlist |
| Uploaded plugin fails | file disabled by default; strict sandbox blocked a legitimate path | enable the file and entries; inspect the error before downgrading to `basic` with audit |
| Landlock unavailable | kernel/container seccomp | use a newer kernel or `basic`; verify Docker seccomp |
| ID conflict or deploy key risk | uploaded plugin uses a built-in ID; deploy category enabled | built-in IDs are reserved, so choose another; enable only trusted deploy targets and disable unneeded ones |

## Related
- [DNS plugins](dns-plugins.md)
- [Deployment plugins](deploy-plugins.md)
- [Notification plugins](notify-plugins.md)
