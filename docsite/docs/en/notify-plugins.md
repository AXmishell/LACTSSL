# Notification plugins

Notification plugins push issuance, deployment, monitoring, and system events to IM, email, or webhooks. Focus on **alert routing, channel availability, key rotation, and noise control**.

## Built-in and plugin channels

| Type | Channel | Default | Description |
| --- | --- | --- | --- |
| Built-in | Email (SMTP) | Enabled | Supports SSL; implicit TLS by default |
| Built-in | DingTalk, WeCom bot/app/intelligent bot, Feishu; Telegram, Bark, PushDeer | Disabled | IM and mobile push |
| Built-in | Gotify, ServerChan, ntfy, PushPlus, WxPusher, Synology Chat, and similar | Disabled | Self-hosted or third-party push |
| Built-in | Generic custom webhook | Disabled | Integrate with an internal alerting platform |

Official channels are built in and enabled per item; email is always available. Existing channel configurations are preserved when a channel is disabled.

## Configuration

```yaml
notify_plugins:
  enabled: true           # enabled by default
  dir: ""                 # empty means <data_dir>/plugins/notify
  memory_limit_mb: 128    # resident memory limit per helper
  timeout_seconds: 30     # timeout for a single send
  max_output_kb: 64       # helper output limit
```

The three plugin types use separate subdirectories under `<data_dir>/plugins`: `dns`, `deploy`, and `notify`. Restart after changing the configuration; the directory is scanned at startup. After installing a helper, click **Rescan plugins** or call:

```bash
curl -X POST -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/plugins/rescan
```

## Installing and enabling

1. Open the notification tab on the **Plugins** page and enable the channels you need.
2. Configure channel credentials and send a test message on the **Notifications** page.

Only enabled channels appear on the **Notifications** page and in task notification nodes; email is always available and cannot be disabled.

## Alerting design

### Routing

| Event | Recommended destination | Description |
| --- | --- | --- |
| Certificate issuance failure | Operations alert channel | Requires immediate action; may affect renewal |
| Deployment failure | Operations + target system owner | Check the target system and reload command |
| Certificate nearing expiry / backup failure | Operations + target system owner / platform team | Tiered at 30/14/7 days; keep backup alerts separate |
| Security event | Security team | Anomalous sign-ins, plugin verification failures |
| Test message | Dedicated test channel | Do not send to the production alert group |

### Noise reduction

- Use different channels and groups for different environments.
- Set task notification nodes to **always / success only / failure only**, tier monitoring by importance, add aggregation or silence windows in the external IM or alerting platform, and remove inactive channels periodically.

### Availability

- Configure at least two independent channels (for example, email plus IM).
- Send test messages to confirm tokens and webhooks have not expired, monitor provider rate limits and review policies, and watch notification failures in the log.

## Channel credential governance

- Use dedicated bots or applications, not personal account tokens, and grant only the permission to send messages.
- Record the token owner, creation date, and rotation interval; use different bots for different environments and teams.
- Credentials are encrypted before storage and masked in API responses; never put tokens or webhook URLs in task names, notes, or logs. On exposure, revoke the credential at the channel immediately.

## Email (SMTP)

- Implicit TLS is used by default (`smtp.ssl=true`). When disabled, the service refuses to send if the server lacks STARTTLS, preventing plaintext transmission of password reset tokens.
- Use an SMTP service, a dedicated sender account, and SPF/DKIM/DMARC.
- Keep the recovery email account separate from the notification sender; use different recipients for test and production.

## Helper protocol v1 (integration development)

A helper is an executable with two subcommands:

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

- `secret: true` or `type: "password"` fields are masked and stored encrypted. Supported types are `text`, `password`, `textarea`, `number`, `select`, and `switch`. A channel `id` allows lowercase letters, digits, underscores, and hyphens; plugins duplicating the built-in `mail` channel are ignored. `label_key` is optional; third-party plugins can provide `label`.

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

- Exit code `0` means success; non-zero means failure. stderr goes to the notification log with sensitive values redacted.
- stdout may be empty or one JSON line `{"ok": true, "message": "..."}`; `ok: false` is a failure. Malformed JSON is an error; write debug logs to stderr.
- `LACTSSL_PLUGIN_MEMORY_LIMIT` sets the byte budget; the host checks `VmRSS` and kills the process group above `memory_limit_mb`. Only a minimal environment is passed.
- Plugin files must be executable and not writable by group or others; a non-empty `manifest.sha256` is verified. The digest is recorded at scan time and re-verified before every send; replacing a helper requires a rescan.

## Security recommendations

- Enable only trusted helpers; a notification plugin can read channel secrets and is equivalent to trusted code.
- Set the plugin directory to `0700` and helpers to `0755`, owned by the service user.
- Provide `sha256` in the manifest, update the digest after upgrades, and set `memory_limit_mb` and `timeout_seconds`; avoid sending domains or internal addresses to public channels.

## Troubleshooting

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Only email appears, or a channel is missing from task nodes | Channel not enabled | Enable it on the **Plugins** page |
| Test message or email fails | Token, URL, network, or SMTP TLS/authentication | Check channel configuration, outbound policy, implicit TLS, STARTTLS, and SPF/DKIM |
| Notification delay or sensitive content | Rate limiting, network, or weak redaction | Check channel status and logs; avoid sending credentials |

For more troubleshooting, see [Monitoring and troubleshooting](troubleshooting.md).
