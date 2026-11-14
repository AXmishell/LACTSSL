# Deployment plugins

LACTSSL supports two kinds of certificate deployment targets:

- **Built-in targets**: local files and SSH/SFTP. These are compiled into the main binary and do not require a plugin.
- **Deployment plugins**: FTP/FTPS, Webhook, Alibaba Cloud CDN, Qiniu, BaoTa, 1Panel, SafeLine WAF and similar targets are provided by one-shot exec helpers without rebuilding the main program. The official reference helper ships all of them.

!!! warning "Deployment plugins receive the certificate private key"
    DNS plugins only receive DNS API credentials, while deployment plugins need the certificate and private key to perform a deployment. Only install trusted helpers and keep the plugin directory writable by the service user only. Deployment plugins are disabled by default and must be enabled explicitly.

## Configuration

```yaml
deploy_plugins:
  enabled: false          # plugins can access private keys, disabled by default
  dir: ""                 # empty means <data_dir>/plugins/deploy
  memory_limit_mb: 128    # per-helper resident memory limit
  timeout_seconds: 300    # single deploy timeout; raise it for large uploads
  max_output_kb: 64       # helper output limit
```

DNS plugins and deployment plugins use two separate subdirectories under `<data_dir>/plugins`, so the two kinds of helper cannot be mixed up:

- DNS plugins: `<data_dir>/plugins/dns`
- Deployment plugins: `<data_dir>/plugins/deploy`

Environment overrides:

| Variable | Description |
| --- | --- |
| `LACTSSL_DEPLOY_PLUGINS_ENABLED` | Enable deployment plugins |
| `LACTSSL_DEPLOY_PLUGINS_DIR` | Plugin directory |
| `LACTSSL_DEPLOY_PLUGINS_MEMORY_MB` | Memory limit in MB, 0 means unlimited |
| `LACTSSL_DEPLOY_PLUGINS_TIMEOUT_SECONDS` | Execution timeout in seconds |
| `LACTSSL_DEPLOY_PLUGINS_MAX_OUTPUT_KB` | Output limit in KB |

The directory is scanned at startup. After installing a helper, click "Rescan plugins" on the Plugins page or call:

```bash
curl -X POST https://<host>/<entry>/api/plugins/rescan
```

## Uploading plugins

The deployment plugin card on the Plugins page also has an **Upload plugin** button, available only when `deploy_plugins.enabled` is true:

- Only a single helper executable is accepted, up to 256 MB. The `manifest`, deployment target IDs and `sha256` are validated before the file is moved into the deployment plugin directory and rescanned.
- Replacing an existing file is rejected by default. Enable **Overwrite a plugin with the same filename** when upgrading; the old file is backed up and restored automatically if installation fails.
- Deployment plugins can access certificate private keys. Make sure the file comes from a trusted source; this operation always requires secondary verification.
- Helpers whose IDs duplicate built-in targets are still discovered, but the rescan result marks them as skipped.
- New non-built-in deployment targets are disabled after upload and must be enabled in the deployment target list.

## Enabling and disabling deployment targets

Deployment target state is managed by a plugin whitelist:

- On first start (no whitelist stored in settings), the canonical targets migrated from the built-in list are enabled by default: `ftp`, `webhook`, `aliyun_cdn`, `qiniu`, `baota`, `1panel`, `safeline`. Their legacy IDs (`qiniu_cdn`, `qiniu_kodo`, `baota_site`, `baota_panel`, `1panel_site`, `1panel_panel`) remain in the whitelist for compatibility with older helpers and historical data. Other plugin targets are disabled by default.
- During upgrade, if the whitelist contains only legacy IDs, the merged canonical IDs are added automatically so the targets do not disappear.
- The state is stored in the `plugins.deploy.enabled_deployers` setting as a list of enabled IDs and survives restarts and rescans.
- Only enabled targets appear in the credentials and workflow deploy type lists; disabling a target hides it immediately.
- Targets enabled by default can also be disabled. The choice is persisted and is not restored by a helper restart or rescan.
- The Plugins page supports a per-target switch plus enable all and disable all. Each change rescans the plugin directory and rebuilds the registry.
- Local files and SSH are not plugins, are always available, and cannot be disabled.

## Build and install the reference helper

When running from source, use the Makefile to build and install it:

```bash
make deploy-helper-install
```

This is equivalent to building `bin/lactssl-deploy-helper` and copying it into the default plugin directory:

```bash
make deploy-helper
mkdir -p data/plugins/deploy
cp bin/lactssl-deploy-helper data/plugins/deploy/deploy-helper
chmod 0755 data/plugins/deploy/deploy-helper
```

The official reference helper provides every target except local files and SSH: FTP/FTPS, Webhook, Alibaba Cloud CDN, Qiniu Cloud, BaoTa, 1Panel and SafeLine WAF. Release archives include `lactssl-deploy-helper`; it is also useful for inspecting and debugging the plugin protocol.

After installing the helper, you still need to:

1. Set `deploy_plugins.enabled: true` in the configuration.
2. Click **Rescan plugins** on the Plugins page, or call `POST /api/plugins/rescan`.

Until the helper is installed and enabled, the credentials page and workflow deploy node only offer local files and SSH. Existing credentials are preserved and work again once the helper is installed.

!!! note "Docker image"
    The official image includes `/app/lactssl-deploy-helper` and copies it to `<data_dir>/plugins/deploy/deploy-helper` at startup. A file with the same name is replaced by the image version; use a different filename for custom helpers.

!!! note "Built-in targets are never overridden"
    Local files and SSH are always handled by the main program. A plugin that declares either ID is reported as skipped.

## Helper protocol v1

A helper is an executable with two subcommands:

```bash
deploy-helper manifest   # print the JSON manifest
deploy-helper deploy     # read a JSON request from stdin and deploy
```

Example `manifest` output:

```json
{
  "protocol": "v1",
  "name": "example deploy helper",
  "version": "1.0.0",
  "sha256": "optional helper checksum",
  "deployers": [
    {
      "id": "example-panel",
      "name": "Example Panel",
      "description": "Push certificates to an Example panel",
      "aliases": ["example"],
      "fields": [
        {"key": "panel_url", "label": "Panel URL", "type": "text", "required": true},
        {"key": "api_token", "label": "API Token", "type": "password", "secret": true, "required": true}
      ],
      "deploy_fields": [
        {"key": "sites", "label": "Sites", "type": "text", "required": true}
      ]
    }
  ]
}
```

Notes:

- `fields` are credential fields; `secret: true` or `type: "password"` are masked in API responses and encrypted with the credential.
- `deploy_fields` are parameters on the "Deploy certificate" workflow node.
- Keys must be unique across both lists. Supported field types: `text`, `password`, `textarea`, `number`, `select`, `switch`.
- IDs may contain lowercase letters, digits, underscores and hyphens. Plugins that reuse a built-in ID are ignored.

`deploy` stdin:

```json
{
  "action": "deploy",
  "protocol": "v1",
  "provider": "example-panel",
  "auth": {"panel_url": "https://panel.example.com", "api_token": "..."},
  "config": {"sites": "example.com,www.example.com"},
  "certificate": {
    "domain": "example.com",
    "domains": ["example.com", "www.example.com"],
    "issuer": "Let's Encrypt",
    "serial": "...",
    "not_before": "2026-01-01T00:00:00Z",
    "not_after": "2026-04-01T00:00:00Z",
    "cert_pem": "-----BEGIN CERTIFICATE-----...",
    "fullchain_pem": "-----BEGIN CERTIFICATE-----...",
    "key_pem": "-----BEGIN PRIVATE KEY-----..."
  }
}
```

Contract:

- Exit code `0` means success; a non-zero exit is a failure. stderr is written to the deployment record after redaction.
- stdout may be empty or contain one JSON line: `{"ok": true, "message": "..."}`.
  `ok: false` is treated as a failure. Invalid JSON is an error, so write debug logs to stderr.
- The helper receives a memory budget in `LACTSSL_PLUGIN_MEMORY_LIMIT` (bytes); call `debug.SetMemoryLimit` at startup.
- The main program polls `VmRSS` and kills the whole process group when it exceeds `memory_limit_mb`.
- Only a minimal environment (PATH, HOME, proxy and TLS settings) is passed; server secrets are not inherited.
- Helper files must be executable and must not be group/world writable. A non-empty `manifest.sha256` is verified against the file digest.
- The main program records the digest at scan time and re-verifies it before every deployment. After replacing a helper, rescan the plugin directory; otherwise deployments are rejected.

## Security recommendations

- Only enable trusted helpers; a deployment plugin can read the certificate private key and is effectively trusted code.
- Set the plugin directory to `0700` and helper files to `0755`, owned by the service user.
- Pin `sha256` in production and update it when the helper is upgraded.
- Set sensible `memory_limit_mb` and `timeout_seconds` values.
- Run the service without extra privileges and keep the plugin directory out of shared writable paths.

## Troubleshooting

The Plugins page shows loaded helpers, deployment targets, skipped entries and errors. Common causes:

- Missing execute permission, or the file is group/world writable.
- The manifest protocol is not `v1`, or a field definition is invalid.
- The `sha256` does not match the file digest.
- The target ID duplicates a built-in target and is reported as skipped.

If a plugin is not loaded, existing credentials are kept; the credentials page reports the missing plugin and does not write the configuration back.
