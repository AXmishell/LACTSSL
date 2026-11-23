# Plugin governance

LACTSSL provides DNS, certificate deployment, and notification capabilities through plugins. Official implementations live in `internal/plugin/impl/` and are compiled into the host; `plugins/` is the external plugin development SDK. Uploaded third-party helpers still run as one-shot subprocesses and are sandboxed by default.

## 1. Model

- **Built-in plugins**: official DNS/deploy/notify implementations ship with the host; no online installation.
- **Uploaded plugins**: administrators may upload a single helper for testing or third-party extensions; disabled by default, executed as a subprocess when enabled.
- **Two-level enablement**:
  - category: `plugins.enabled`, `deploy_plugins.enabled`, `notify_plugins.enabled`;
  - entry: DNS providers, deploy targets, notification channels.
- **No plugin repository**: no index, signature index, marketplace, or auto-update. Official plugin updates ship with host releases.

Protocol v1 remains:

```text
manifest / present / cleanup / deploy / options / plan / send
```

Built-in implementations are called in-process; uploaded helpers use stdin/stdout JSON.

## 2. Configuration

```yaml
plugins:
  enabled: true
  dir: ""                 # uploaded DNS helpers; default <data_dir>/plugins/dns
  sandbox: strict         # strict / basic / off
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64

deploy_plugins:
  enabled: false          # deploy plugins receive private keys
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

The `sandbox` setting applies **only to uploaded/external helpers**; built-in plugins are called in-process and never enter the sandbox. On Linux, `strict` uses Landlock file-access rules, rlimits, `no_new_privs`, and a separate process group. If the kernel/container does not support it, the host falls back to `basic` (minimal environment, private temp dir, timeout/RSS/output limits) and logs the downgrade once. If the host process still runs as root without `LACTSSL_RUN_UID`/`LACTSSL_RUN_GID`, uploaded helpers are refused: Landlock cannot stop root from bypassing it via mount/ptrace. `off` is for controlled debugging only.

## 3. Built-ins and memory

- Built-in implementations are compiled into the host; disabling a category or entry does not unload code, but avoids registering the implementation and constructing providers.
- To reduce binary/memory size, use build tags:
  - `make backend-no-dns`: excludes built-in DNS entirely; idle RSS ~36 MB;
  - official releases are full builds (idle RSS ~70 MB).
- Most of the full build's memory comes from lego v5 code pages, not plugin metadata (~1 MB). Enabling all 235 providers adds only ~3–5 MB over the default empty list.
- DNS built-ins use a global constructor lock to set and restore lego v5 environment variables; `present/cleanup` can run concurrently.

## 4. Enable and disable

### 4.1 Category

The category switch controls whether built-in implementations of that kind are registered. When disabled:

- its built-in entries are unavailable;
- uploaded helpers of the same kind are not executed;
- saved accounts, tasks, and allowlists are preserved.

### 4.2 Entries

- DNS: no providers are enabled by default; enable them on the Plugins page.
- Deploy: category is off by default; once enabled, 25 canonical targets are enabled.
- Notify: 23 channels are enabled by default.

Enabling more than 10 entries at once requires confirmation.

## 5. Uploaded plugins and sandbox

Flow:

1. choose kind and helper file;
2. run `manifest` inside the sandbox and validate protocol, ID, version, digest, platform;
3. write to the plugin directory; new files are disabled by default;
4. enable the file, then enable entries.

Limits:

- single executable, up to 256 MB;
- reject symlinks and group/other-writable files;
- uploaded plugins cannot occupy built-in IDs/aliases;
- `strict` sandbox: only system CA/DNS files and the helper itself are readable; only a private temp dir is writable; data dir, config, database, and other plugins are blocked;
- the sandbox is not network isolation: uploaded plugins can still make outbound connections; only upload trusted code.

## 6. Updates, rollback, uninstall

- Built-ins update with the host; no separate install/update/uninstall. Enablement is preserved across upgrades.
- Uploaded plugins are re-uploaded by the administrator; the old file is backed up and restored on failure.
- Deleting an uploaded file removes its digest, metadata, and allowlist entries.

## 7. Legacy repository plugins

1. Official plugins are now built in; old helper files should be disabled or deleted.
2. Accounts, tasks, and allowlists are preserved.
3. Custom plugins not covered by built-ins remain as legacy/upload plugins and run with `strict` sandbox by default.
4. Legacy plugins have no update source; replace or remove them.

## 8. Security

- Built-ins are part of the host supply chain and ship with the signed release/image digest.
- Uploaded plugins run sandboxed by default; do not set `sandbox: off` in production.
- Deploy plugins receive private keys; keep the category disabled unless needed.
- Plugin directory `0700`, helper `0755`, no symlinks, no group/other write.
- Uploaded helper digests are rechecked on discovery, registration, and every call.
- Plugin management requires `plugins:read` / `plugins:write`; uploads and toggles are audited, and a strict-sandbox downgrade is written to the host startup log.

## 9. API

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

## 10. Troubleshooting

| Symptom | Cause | Action |
|---|---|---|
| Built-in entry unavailable | category off or entry disabled | check category config and entry allowlist |
| Uploaded plugin has no entries | file disabled by default | enable file, then entries |
| Uploaded plugin fails | strict sandbox blocked a legitimate path | inspect error; explicitly downgrade to `basic` and audit |
| Landlock unavailable | kernel/container seccomp | use newer kernel or `basic`; verify Docker seccomp |
| ID conflict | uploaded plugin uses built-in ID | built-in IDs are reserved; choose another ID |
| Deploy private key risk | deploy category enabled | enable only trusted targets; keep off by default |

## Related

- [DNS plugins](dns-plugins.md)
- [Deployment plugins](deploy-plugins.md)
- [Notification plugins](notify-plugins.md)
- [Security baseline](security.md)
