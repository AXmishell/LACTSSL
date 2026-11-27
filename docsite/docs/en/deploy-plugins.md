# Deployment plugins

Deployment plugins deliver certificates to FTP, webhooks, cloud CDNs, panels, and WAFs. They are a **high-risk component with access to certificate private keys**, so include them in review and change management.

## Built-in and plugin targets

| Type | Target | Default | Description |
| --- | --- | --- | --- |
| Built-in | Local directory | Enabled | Writes to a local path, with an optional reload command |
| Built-in | SSH / SFTP | Enabled | Password or private key authentication, host fingerprint verification |
| Built-in | FTP / FTPS | Enabled | Plain FTP, explicit TLS, implicit TLS |
| Built-in | Webhook | Enabled | Custom authentication headers, optional TLS verification bypass (not recommended) |
| Built-in | Alibaba Cloud CDN / Qiniu Cloud | Enabled | CDN domain or Kodo custom origin domain |
| Built-in | BaoTa panel/WAF, 1Panel, SafeLine WAF, Tencent Cloud, Volcengine, AcePanel | Enabled | Website, panel, WAF, or certificate ID/domain match |

> **Deployment targets receive certificate private keys.** Install only trusted third-party helpers, and ensure that only the service user can write to the plugin directory. The category and official targets are enabled by default; disable any you do not need.

## Credentials and deployment parameters

**Target credentials** store only authentication and connection information such as host, port, username, password, panel URL, and API key. Certificate paths, reload commands, domains, and sites are entered on the **Deploy certificate** node of the task workflow. This lets one credential serve multiple tasks, keeps rotation independent of orchestration, and records deployment parameters with the task version for auditing and rollback.

## Configuration

```yaml
deploy_plugins:
  enabled: true           # official targets are on by default; plugins can access private keys
  dir: ""                 # empty means <data_dir>/plugins/deploy
  memory_limit_mb: 128    # resident memory limit per helper
  timeout_seconds: 300    # timeout for a single deploy; raise it for large uploads
  max_output_kb: 64       # helper output limit
```

The three plugin types use separate subdirectories under `<data_dir>/plugins`: `dns`, `deploy`, and `notify`. Restart after changing the configuration. The directory is scanned at startup; after installing a new helper, click **Rescan plugins** or call:

```bash
curl -X POST -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/plugins/rescan
```

## Installing and enabling

1. Confirm `deploy_plugins.enabled: true` in the configuration (the default) and restart after changes.
2. Open the deployment tab on the **Plugins** page and disable the targets you do not need.

When the category is disabled, only local files and SSH are shown; existing credentials are preserved. Enablement is stored in the settings table and persists across restarts and rescans. Merged targets show a single vendor entry under **Target credentials**, with the product chosen on the task deploy node.

## Target practices

### Local directory

- The service user must have write permission on the target directory.
- Prefer atomic writes (temporary file, then `rename`), and note that a failed reload does not roll back the file; retain the previous version. Reload examples: `nginx -s reload`, `systemctl reload nginx`, `systemctl reload postfix`.

### SSH / SFTP

- The target host fingerprint (`SHA256:...`) is required by default. TOFU applies only when first-use trust is explicitly enabled; later mismatches reject the connection. After the server key is replaced, clear the old fingerprint and record it again.
- Use a dedicated deployment user with write access only to the target path and the minimum reload permissions.
- Prefer private key authentication; protect the key with a passphrase and rotate it regularly.

### FTP / FTPS

- Explicit TLS is the default; plain FTP applies only when `none` is explicitly selected. Private keys and passwords traverse the network, so prefer SFTP or FTPS in production.
- Restrict target IPs, ports, and directory permissions; do not use shared accounts.

### Webhook

- Public targets are forced to HTTPS; loopback, private network, `.local`, and `.internal` addresses may use HTTP.
- Webhooks carry the certificate and private key; use a dedicated token, source IP restrictions, and receiver-side idempotency and signature verification. Avoid **Skip TLS verification**; for internal CAs, import the CA under "Settings → TLS trust chain" instead.

### Cloud CDN / panel / WAF

- Use dedicated sub-accounts with only certificate management permissions.
- Deployment parameters must match the target; BaoTa website names can be loaded from the panel API or typed manually. Allow LACTSSL egress addresses when the target has an IP allowlist.
- CDNs and WAFs may cache certificates; verify the certificate actually in effect.

## Deployment records and verification

Each deployment generates a record visible in certificate details and logs. Manual deployments reuse task parameters where possible and can accept separate parameters. Local and SSH deployments run the reload command; API keys need `deploy:execute`. A failed deployment does not roll back the target automatically; retain the previous version or use atomic release.

Verification checklist:

- [ ] Target path or site is correct; the chain is complete (fullchain); the key matches.
- [ ] The service reloaded on all instances; CDN/WAF caches are refreshed and clients show the new serial.

## Offline and manual installation

- Upload a single helper on the **Plugins** page, up to 256 MB; it requires `deploy_plugins.enabled: true` (the default).
- After upload, the `manifest`, deployment target IDs, and SHA-256 are validated before the file is written and rescanned immediately.
- Overwriting a same-name file is rejected by default; select **Allow overwriting a plugin with the same filename** when upgrading, and the old file is backed up first.
- Helpers whose IDs duplicate a built-in target are discovered but marked **ignored** in the rescan result.

## Helper protocol v1 (integration development)

A helper is an executable that supports the following subcommands:

```bash
deploy-helper manifest   # print the JSON manifest
deploy-helper deploy     # read a request from stdin and perform the deployment
deploy-helper options    # read dynamic select options (optional)
deploy-helper verify     # read-only authentication and scope probe (optional)
```

Example `manifest` output:

```json
{
  "protocol": "v1",
  "capabilities": ["verify-v1"],
  "name": "example deploy helper",
  "version": "1.0.0",
  "sha256": "optional helper file checksum",
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

Field reference:

- `fields` are authentication and connection information; `secret: true` or `type: "password"` values are masked and stored encrypted.
- `deploy_fields` are parameters on the **Deploy certificate** node. `key` must be unique across both lists; types are `text`, `password`, `textarea`, `number`, `select`, and `switch`.
- Target `id` allows lowercase letters, digits, underscores, and hyphens; IDs duplicating a built-in target are ignored. `name` / `description` / `label` and similar are default-language strings; optional `*_i18n` maps (BCP 47, up to 32 locales) override them.

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

Conventions:

- Exit code `0` means success; non-zero means failure. stderr goes to the deployment record with sensitive values redacted.
- stdout may be empty or one JSON line `{"ok": true, "message": "..."}`; `ok: false` is a failure. Malformed JSON is an error; write debug logs to stderr.
- `LACTSSL_PLUGIN_MEMORY_LIMIT` sets the byte budget; the host checks `VmRSS` and kills the process group above `memory_limit_mb`.
- The host passes only a minimal environment; server secrets are not exposed.
- Plugin files must be executable and not writable by group or others; a non-empty `manifest.sha256` is verified.
- The digest is recorded at scan time and re-verified before every deployment; replacing a helper requires a rescan.

## Security recommendations

- Enable only trusted helpers; a deployment plugin can read certificate private keys and is equivalent to trusted code.
- Set the plugin directory to `0700` and helpers to `0755`, owned by the service user.
- Provide `sha256` in the manifest, update the digest after upgrades, and keep the plugin directory off shared writable paths.
- Set `memory_limit_mb` and `timeout_seconds`; do not grant extra privileges, and use least-privilege target accounts with scoped reload commands.

## Troubleshooting

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Only local/SSH types are available | Plugin category or target not enabled | Enable `deploy_plugins.enabled`, then the target |
| Plugin fails to load | Permissions, digest, protocol, or symbolic link | Correct permissions or reinstall |
| SSH fingerprint mismatch | Server key replaced or misconfiguration | Verify the fingerprint, clear the old record, and retry |
| Deployment succeeds but old certificate is served | Reload missing, caching, or multiple instances | Configure reload, refresh caches, and check all nodes |
| Webhook rejected / deployment times out | Public HTTP, untrusted certificate, slow target, or large files | Use HTTPS, check TLS, increase `timeout_seconds` |

For more troubleshooting, see [Monitoring and troubleshooting](troubleshooting.md).
