# Deployment plugins

Deployment plugins deliver certificates to targets such as FTP, webhooks, cloud CDNs, control panels, and WAFs. They are a **high-risk component with access to certificate private keys**, so enterprises should include them in admission and change management.

## 1. Built-in and plugin targets

| Type | Target | Built-in | Description |
| --- | --- | --- | --- |
| Built-in | Local directory | Yes | Writes to a local path, with an optional reload command |
| Built-in | SSH / SFTP | Yes | Password or private key authentication, host fingerprint verification |
| Plugin | FTP / FTPS | No | Plain FTP, explicit TLS, implicit TLS |
| Plugin | Webhook | No | Custom authentication headers, optional TLS verification bypass (not recommended) |
| Plugin | Alibaba Cloud CDN | No | Updates domain certificates |
| Plugin | Qiniu Cloud | No | CDN accelerated domain or Kodo custom origin domain |
| Plugin | BaoTa panel / BaoTa WAF | No | Website, panel, or WAF |
| Plugin | 1Panel | No | Website or panel |
| Plugin | SafeLine WAF | No | Matched by certificate ID or domain |
| Plugin | Tencent Cloud / Volcengine / AcePanel, and similar | No | Subject to the entries in the plugin market |

> **Deployment plugins receive certificate private keys.** Install only helpers from trusted sources, and ensure that only the service user can write to the plugin directory. Deployment plugins are disabled by default and must be enabled explicitly.

## 2. Credentials and deployment parameters

**Target credentials** store only authentication and connection information, such as host, port, username, password, panel URL, and API key. Parameters such as certificate paths, reload commands, deployment domains, and sites are entered on the **Deploy certificate** node of the task workflow.

This design provides the following benefits:

- The same credential can be reused by multiple tasks.
- Credential rotation does not affect task orchestration.
- Deployment parameters are recorded with the task version, which supports auditing and rollback.

## 3. Configuration

```yaml
deploy_plugins:
  enabled: false          # plugins can access private keys; disabled by default
  dir: ""                 # empty means <data_dir>/plugins/deploy
  memory_limit_mb: 128    # resident memory limit per helper
  timeout_seconds: 300    # timeout for a single deploy; raise it for large uploads
  max_output_kb: 64       # helper output limit
```

The three plugin types use separate subdirectories under `<data_dir>/plugins`:

- DNS: `<data_dir>/plugins/dns`
- Deployment: `<data_dir>/plugins/deploy`
- Notification: `<data_dir>/plugins/notify`

Restart the service after changing the configuration. The directory is scanned automatically at startup. After installing a new helper, click **Rescan plugins** on the **Plugins** page, or call:

```bash
curl -X POST -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/plugins/rescan
```

## 4. Installing and enabling

1. Set `deploy_plugins.enabled: true` in the configuration and restart the service.
2. Open the **Plugins** page, confirm the plugin repository URL, and click **Refresh index**.
3. Install the required deployment targets from the **Plugin market**.
4. Enable specific targets in the deployment target list.

If no plugin is installed or enabled, only local files and SSH appear under target credentials and task workflow deployment types. Existing credentials are preserved and remain usable after plugins are installed.

Enablement state is stored in the settings table under `plugins.deploy.enabled_deployers` and records only enabled target IDs; it persists across restarts and rescans. Merged targets show a single vendor entry under **Target credentials**; the specific product is selected through **Deployment target** on the task deploy node.

## 5. Target practices

### 5.1 Local directory

- The service user must have write permission on the target directory.
- Prefer atomic writes: write a temporary file first, then `rename` it into place so that readers never see a partial file.
- Reload command examples: `nginx -s reload`, `systemctl reload nginx`, `systemctl reload postfix`.
- A failed reload command does not roll back the certificate file; retain the previous version on the target side.

### 5.2 SSH / SFTP

- By default, the target host fingerprint (`SHA256:...`) is required. TOFU is used only when **Automatically trust the host key on first connection** is explicitly enabled; subsequent fingerprint mismatches reject the connection.
- After the server's key is replaced, clear the old fingerprint and record it again.
- Use a dedicated deployment user with write access only to the target path and the minimum required reload permissions (sudoers scoped to specific commands).
- Prefer private key authentication over passwords. Store the private key in a controlled location, protect it with a passphrase, and rotate it regularly.

### 5.3 FTP / FTPS

- Explicit TLS is the default; plain FTP is used only when `none` is explicitly selected.
- Certificate private keys and login passwords traverse the network; prefer SFTP or FTPS in production.
- Restrict target IPs, ports, and directory permissions; do not use shared accounts.

### 5.4 Webhook

- Public targets are forced to HTTPS; loopback, private network, `.local`, and `.internal` addresses may use HTTP.
- Webhooks carry the certificate and private key, so the receiver must be trusted. Use a dedicated token and source IP restrictions.
- Do not enable **Skip TLS verification** unless necessary; if required, limit it to internal self-signed targets.
- Receivers should implement idempotency and signature verification to prevent replay.

### 5.5 Cloud CDN / panel / WAF

- Use dedicated sub-accounts with only certificate management permissions.
- Deployment parameters (domain, site, certificate name) must match the target system.
- If the target system enforces an IP allowlist, allow LACTSSL egress addresses.
- CDNs and WAFs may cache certificates; after deployment, verify the certificate that is actually in effect.

## 6. Deployment records and verification

- Each deployment generates a deployment record; results are available in certificate details and logs.
- Manual deployments reuse the deployment node parameters of the certificate's task where possible, and can also accept parameters entered separately for the target.
- Local and SSH deployments run the reload command; API keys must explicitly hold `deploy:execute`.
- A failed deployment does not automatically roll back the previous certificate on the target system; retain the previous version on the target side or use atomic release.

Verification checklist:

- [ ] Target path or site is correct.
- [ ] Certificate chain is complete (fullchain).
- [ ] Private key matches the certificate.
- [ ] The service was reloaded and actually loaded the new certificate.
- [ ] All instances and nodes are updated.
- [ ] CDN/WAF caches are refreshed.
- [ ] The browser or client shows the new certificate serial number or fingerprint.

## 7. Offline and manual installation

- Upload a single helper on the **Plugins** page, up to 256 MB; it is available only after `deploy_plugins.enabled` is enabled.
- After upload, the `manifest`, deployment target IDs, and SHA-256 are validated before the file is written to the deployment plugin directory and rescanned immediately.
- Overwriting a file with the same name is rejected by default. When upgrading, select **Allow overwriting a plugin with the same filename**; the old file is backed up first.
- Plugins whose IDs duplicate a built-in deployment target are discovered, but the rescan result marks them as **ignored**.

## 8. Helper protocol v1 (integration development)

A helper is an executable that supports two subcommands:

```bash
deploy-helper manifest   # print the JSON manifest
deploy-helper deploy     # read a request from stdin and perform the deployment
```

Example `manifest` output:

```json
{
  "protocol": "v1",
  "name": "example deploy helper",
  "version": "1.0.0",
  "sha256": "optional helper file checksum",
  "deployers": [
    {
      "id": "example-panel",
      "name": "Example Panel",
      "name_i18n": {"zh-CN": "示例面板", "en-US": "Example Panel"},
      "description": "Push certificates to an Example panel",
      "description_i18n": {
        "zh-CN": "把证书推送到 Example 面板",
        "en-US": "Push certificates to an Example panel"
      },
      "aliases": ["example"],
      "fields": [
        {
          "key": "panel_url",
          "label": "Panel URL",
          "label_i18n": {"zh-CN": "面板地址", "en-US": "Panel URL"},
          "type": "text",
          "required": true
        },
        {
          "key": "api_token",
          "label": "API Token",
          "label_i18n": {"zh-CN": "API Token", "en-US": "API Token"},
          "type": "password",
          "secret": true,
          "required": true
        }
      ],
      "deploy_fields": [
        {
          "key": "sites",
          "label": "Sites",
          "label_i18n": {"zh-CN": "站点", "en-US": "Sites"},
          "type": "text",
          "required": true
        }
      ]
    }
  ]
}
```

Field reference:

- `fields` are the authentication and connection information in target credentials; fields marked `secret: true` or with `type: "password"` are masked in API responses and stored encrypted with the credential.
- `deploy_fields` are parameters on the **Deploy certificate** node of the task workflow.
- `key` must be unique across both field lists. Supported field types are `text`, `password`, `textarea`, `number`, `select`, and `switch`.
- A deployment target `id` may contain only lowercase letters, digits, underscores, or hyphens. Plugins that duplicate a built-in target are ignored.
- `name` / `description` / `label` / `placeholder` / `help` are default-language strings; optional `*_i18n` maps override them per locale.
- Locale keys use BCP 47 form, such as `zh-CN` and `en-US`; each string map supports up to 32 locales.

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

- Exit code `0` means success; a non-zero exit means failure. stderr is written to the deployment record (sensitive values are redacted).
- stdout may be empty or contain one JSON line: `{"ok": true, "message": "..."}`; `ok` set to `false` is treated as a failure.
- Malformed JSON is treated as an error; write debug logs to stderr.
- The helper receives its memory budget from `LACTSSL_PLUGIN_MEMORY_LIMIT` (bytes).
- The main program periodically checks the helper's `VmRSS` and terminates the entire process group, logging an error, when it exceeds `memory_limit_mb`.
- The main program passes only a minimal set of environment variables; server secrets are not exposed.
- Plugin files must be executable and must not be writable by group or other users. A non-empty `manifest.sha256` is verified.
- The main program records the file digest at scan time and re-verifies it before every deployment; replacing a helper requires a rescan.

## 9. Security recommendations

- Enable only trusted helpers; a deployment plugin can read certificate private keys and is equivalent to trusted code.
- Set the plugin directory to `0700` and helpers to `0755`, owned by the service user.
- In production, provide `sha256` in the manifest and update the digest after upgrades.
- Set appropriate `memory_limit_mb` and `timeout_seconds` to prevent a malfunctioning plugin from degrading the host.
- Do not grant the `lactssl` user additional privileges, and do not place the plugin directory on a shared writable path.
- Use least-privilege accounts for deployment targets and scope reload commands to specific services.

## 10. Troubleshooting

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Only local/SSH deployment types are available | Plugin not enabled or not installed | Enable `deploy_plugins.enabled`, then install and enable the target |
| Plugin fails to load | Permissions, digest, protocol, or symbolic link | Correct permissions or reinstall |
| SSH fingerprint mismatch | Server key replaced or misconfiguration | Verify the fingerprint, clear the old record, and retry |
| Deployment succeeds but the service still serves the old certificate | Reload missing, caching, or multiple instances | Configure the reload command, refresh caches, and check all nodes |
| Webhook rejected | Public HTTP, or untrusted certificate | Use HTTPS or an internal address, and check TLS |
| CDN/panel failure | Credential permissions, parameters, or API version | Check permissions and parameters on the target side |
| Deployment times out | Slow target or large files | Increase `timeout_seconds` and check the network |

For more troubleshooting, see [Monitoring and troubleshooting](troubleshooting.md).

## Related documentation

- [Plugin governance](plugins.md)
- [Features](features.md)
- [DNS plugins](dns-plugins.md)
- [Notification plugins](notify-plugins.md)
