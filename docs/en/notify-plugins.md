# Notification plugins

Notification plugins push certificate issuance, deployment, monitoring, and system events to enterprise IM, email, or webhooks. For B2B use, the focus is **alert routing, channel availability, key rotation, and noise control**.

## 1. Built-in and plugin channels

| Type | Channel | Built-in | Description |
| --- | --- | --- | --- |
| Built-in | Email (SMTP) | Yes | Supports SSL; implicit TLS by default |
| Plugin | Gotify, ServerChan, ntfy | No | Self-hosted or third-party push |
| Plugin | Telegram, Bark, PushDeer | No | Mobile push |
| Plugin | DingTalk, WeCom bot/app/intelligent bot, Feishu | No | Enterprise IM |
| Plugin | GoCQHTTP, iGot, PushPlus, WePlus, WxPusher, WxPusher SPT, OpenILink, WPush, Synology Chat, PushMe, ChronoCat | No | Other channels |
| Plugin | Generic custom webhook | No | Integrate with an internal alerting platform |

If no helper is installed or enabled, only email appears on the notifications page. Existing channel configurations are preserved and remain usable after the helper is installed.

## 2. Configuration

```yaml
notify_plugins:
  enabled: true           # enabled by default
  dir: ""                 # empty means <data_dir>/plugins/notify
  memory_limit_mb: 128    # resident memory limit per helper
  timeout_seconds: 30     # timeout for a single send
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

## 3. Installing and enabling

1. Open the **Plugins** page, confirm the plugin repository URL, and click **Refresh index**.
2. Install the required notification channels from the **Plugin market**.
3. Enable the plugin file first, then enable channels in the channel list; newly downloaded files and entries are disabled by default.
4. Configure channel credentials and send a test message on the **Notifications** page.

Enablement state is stored in the settings table under `plugins.notify.enabled_channels` and persists across restarts and rescans. Only enabled channels appear on the **Notifications** page and in task notification nodes.

Email is not a plugin; it is always available and cannot be disabled.

## 4. Enterprise alerting design

### 4.1 Routing

| Event | Recommended destination | Description |
| --- | --- | --- |
| Certificate issuance failure | Operations on-call | Requires immediate action; may affect renewal |
| Deployment failure | Operations + target system owner | Check the target system and reload command |
| Certificate nearing expiry | Operations + business owner | Tiered at 30/14/7 days |
| Endpoint monitoring alert | Operations | Distinguish network, chain, and hostname issues |
| Backup failure | Platform team | Keep separate from certificate alerts |
| Security event | Security team | Anomalous sign-ins, plugin verification failures, and similar events |
| Test message | Dedicated test channel | Do not send to the production on-call group |

### 4.2 Noise reduction

- Use different channels and groups for different environments.
- Set task notification nodes to **always / success only / failure only** so that success messages do not drown out failures.
- Configure aggregation or silence windows for similar events (implemented by the external IM or alerting platform).
- Tier monitoring items by business importance to avoid frequent alerts for unstable targets.
- Periodically remove inactive channels and duplicate configurations.

### 4.3 Availability

- Configure at least two independent channels (for example, email plus enterprise IM) to avoid a single point of failure.
- Send test messages periodically to confirm that tokens and webhooks have not expired.
- Monitor channel provider rate limits and content review policies.
- Notification failures are written to the log; include notification failures themselves in monitoring.

## 5. Channel credential governance

- Use dedicated bots or applications, not personal account tokens.
- Grant only the permissions required to send messages; do not grant organization management, contact directory read, or similar permissions.
- Use different bots for different environments and teams.
- Record the token owner, creation date, and rotation interval.
- Credentials are encrypted before being written to the database and masked in API responses.
- Do not write tokens or webhook URLs into task names, notes, or logs.
- On exposure, revoke the credential at the channel immediately and update the LACTSSL configuration.

## 6. Email (SMTP)

- Implicit TLS is used by default (`smtp.ssl=true`).
- When implicit TLS is disabled, the service refuses to send if the SMTP server does not support STARTTLS, preventing sensitive messages such as password reset tokens from being transmitted in plaintext.
- Use an enterprise email gateway, a dedicated sender account, and SPF/DKIM/DMARC.
- Keep the recovery email account and the notification sender account separate to avoid mutual impact.
- Use different recipients for test email and production notifications.

## 7. Helper protocol v1 (integration development)

A helper is an executable that supports two subcommands:

```bash
notify-helper manifest   # print the JSON manifest
notify-helper send       # read a request from stdin and send the notification
```

Example `manifest` output:

```json
{
  "protocol": "v1",
  "name": "example notify helper",
  "version": "1.0.0",
  "sha256": "optional helper file checksum",
  "channels": [
    {
      "id": "example-push",
      "name": "Example Push",
      "description": "Push notifications to the Example service",
      "fields": [
        {"key": "url", "label": "Service URL", "type": "text", "required": true},
        {"key": "token", "label": "Token", "type": "password", "secret": true, "required": true}
      ]
    }
  ]
}
```

Field reference:

- Fields marked `secret: true` or with `type: "password"` are masked in API responses and stored encrypted with the channel configuration.
- Supported field types are `text`, `password`, `textarea`, `number`, `select`, and `switch`.
- A channel `id` may contain only lowercase letters, digits, underscores, or hyphens; plugins that duplicate the built-in email (`mail`) channel are ignored.
- `label_key` is optional; the official frontend prefers the i18n key for localization, and third-party plugins can provide `label` directly.

`send` stdin:

```json
{
  "action": "send",
  "protocol": "v1",
  "channel": "example-push",
  "config": {"url": "https://push.example.com", "token": "..."},
  "title": "Certificate issued: example.com",
  "body": "Task: example.com\nStatus: success"
}
```

Conventions:

- Exit code `0` means success; a non-zero exit means failure. stderr is written to the notification log (sensitive values are redacted).
- stdout may be empty or contain one JSON line: `{"ok": true, "message": "..."}`; `ok` set to `false` is treated as a failure.
- Malformed JSON is treated as an error; write debug logs to stderr.
- The helper receives its memory budget from `LACTSSL_PLUGIN_MEMORY_LIMIT` (bytes).
- The main program periodically checks the helper's `VmRSS` and terminates the entire process group, logging an error, when it exceeds `memory_limit_mb`.
- The main program passes only a minimal set of environment variables; server secrets are not exposed.
- Plugin files must be executable and must not be writable by group or other users. A non-empty `manifest.sha256` is verified.
- The main program records the file digest at scan time and re-verifies it before every send; replacing a helper requires a rescan.

## 8. Security recommendations

- Enable only trusted helpers; a notification plugin can read channel secrets and is equivalent to trusted code.
- Set the plugin directory to `0700` and helpers to `0755`, owned by the service user.
- In production, provide `sha256` in the manifest and update the digest after upgrades.
- Set appropriate `memory_limit_mb` and `timeout_seconds` to prevent a malfunctioning plugin from slowing notification delivery.
- Notification content may include domains, error details, and internal addresses; avoid sending it to public channels.
- Use HTTPS and signature verification for webhook targets to prevent forgery and replay.

## 9. Troubleshooting

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Only email appears on the notifications page | Notification plugin not installed or not enabled | Install and enable a notification plugin |
| Channel missing from task nodes | Channel not enabled | Enable it on the **Plugins** page and the **Notifications** page |
| Test message fails | Token, URL, or network | Check channel configuration and outbound policy |
| Email sending fails | SMTP TLS, authentication, or sender | Check implicit TLS, STARTTLS, and SPF/DKIM |
| Notification delay | Channel rate limiting or network | Check channel status and logs |
| Notification content contains sensitive information | Insufficient template or log redaction | Adjust the content and avoid sending credentials or private keys |

For more troubleshooting, see [Monitoring and troubleshooting](troubleshooting.md).

## Related documentation

- [Plugin governance](plugins.md)
- [Features](features.md)
- [DNS plugins](dns-plugins.md)
- [Deployment plugins](deploy-plugins.md)
