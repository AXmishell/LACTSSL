# DNS plugins

DNS plugins provide domain provider API access for DNS-01 validation. The priority is **least-privilege credentials, rotation, auditability, and offline operation**.

## Prerequisites

- Credentials are created under **DNS accounts**, and the zone is visible to them with permission to create and delete TXT records.
- Outbound access to the provider API is available; if a proxy is used, confirm the Go HTTP client proxy settings.
- One certificate may span multiple accounts: declare zones under **DNS accounts → Zone bindings** and select **Auto by domain binding**. Routing is by longest suffix; a single explicit account covers every domain.

DNS providers are disabled by default; issuing with a disabled provider fails with an explicit error, while existing accounts are preserved.

## Choosing a plugin package

Official DNS implementations are built in, with a default full build covering about 235 entries (228 lego v5 providers + 7 compatibility IDs); no plugin package is needed. All providers are disabled by default—enable them on the **Plugins** page.

For a smaller binary or memory footprint, use a reduced build: `make backend-dns-common` links only the common whitelist, `make backend-dns-minimal` keeps only Cloudflare, and `make backend-no-dns` omits built-in DNS entirely. Reduced builds still accept uploaded helpers.

## Installing and enabling

1. Open the **Plugins** page and confirm the DNS category is enabled.
2. Enable the providers you need in the DNS provider list; enabling more than 10 entries at once requires confirmation.
3. Create credentials under **DNS accounts** and select the provider.

Enablement is stored in the settings table; disabling the category unregisters entries at runtime but keeps existing selections.

## Credential governance

### Least privilege

- Use a dedicated sub-account or API key, not a personal or global administrator key.
- Grant DNS read/write only to the target zone; no billing, domain registration, or zone deletion.
- Use separate credentials for testing and production.

### Rotation

- Record owner, creation date, scope, and rotation interval.
- Update the credential in LACTSSL first, then revoke the old one at the provider.
- Run a test task to confirm TXT creation and cleanup, and rotate immediately after departures or exposure.

### Storage

- Credentials are encrypted with AES-256-GCM before storage and masked in API responses.
- Leave a secret field empty when editing to keep the existing value.
- Do not put credentials in task names, notes, tickets, or logs; backups contain encrypted credentials.

## Common providers

| Provider | Main credentials | Notes |
| --- | --- | --- |
| Alibaba Cloud DNS (`alidns`) / ESA (`aliesa`) | AccessKey ID / Secret | Optional Region |
| Cloudflare | API Token, or Email + Global API Key | Optional API upstream URL |
| DNSPod / Tencent Cloud (`tencentcloud`) | API Token; SecretId / SecretKey | Optional Region |
| Huawei Cloud (`huaweicloud`) / Baidu AI Cloud / JD Cloud | AccessKey ID / SecretAccessKey / Region | |
| Volcengine (`volcengine`) | AccessKey / SecretKey | Optional Region, API host, scheme |
| AWS Route 53 / Google Cloud DNS / Azure DNS | Access keys, service account JSON, or tenant credentials | Optional Region, zone, project |
| DigitalOcean / Vultr / Linode / Hetzner / Gandi / GoDaddy / Namecheap / NameSilo / Porkbun | API token / key or provider-specific credentials | |
| PowerDNS (`pdns`) / RFC 2136 | API URL / API key; DNS server + TSIG | Optional version, server name, zone list |

- Built-in providers cover all lego v5 entries (about 228).
- The `manual` provider requires adding TXT records by hand and is outside the automated flow; Cloudflare, DNSimple, PowerDNS, and Volcengine also accept a custom API upstream URL.
- The selector on **DNS accounts** is the authoritative list.

## Offline and manual installation

Built-in providers ship with the host, so offline environments need no extra installation. Third-party providers can be uploaded as a single helper on the **Plugins** page (up to 256 MB): protocol, ID, and SHA-256 are validated, overwriting a same-name file is rejected by default, and the old file is backed up during upgrades. A manually uploaded multi-provider bundle is marked `manual` and does not participate in automatic updates.

## Validation and troubleshooting

### Validation

1. Create and save a DNS account, then create a test task with a test domain.
2. Run it and observe TXT creation, CA validation, and cleanup.
3. Review the task and operation logs; delete or keep the test certificate.

### Common issues

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Provider dropdown is empty | DNS category or provider disabled | Enable the category and provider on the **Plugins** page |
| Credential save fails | Missing or invalid field | Correct it per the form and log messages |
| TXT creation / cleanup fails | Permissions, zone, quota, or already-deleted record | Check permissions and remove residual records manually |
| Validation times out | Slow propagation or stale authoritative DNS | Check TTL, authoritative resolution, and CAA |
| Custom API URL or helper fails | Unreachable URL, untrusted certificate, or bad digest | Check network, TLS, permissions, and the installation record |

## Helper protocol v1 (integration development)

For plugin developers; operations staff generally do not need it. A helper is an executable with four subcommands:

```bash
dns-helper manifest   # print the JSON manifest
dns-helper present    # read a request from stdin and add the TXT record
dns-helper cleanup    # read a request from stdin and delete the TXT record
dns-helper verify     # read a request from stdin and run a read-only probe (optional)
```

Example `manifest` output:

```json
{
  "protocol": "v1",
  "capabilities": ["verify-v1"],
  "name": "example dns helper",
  "version": "1.0.0",
  "sha256": "optional helper file checksum",
  "providers": [
    {
      "id": "example-dns",
      "name": "Example DNS",
      "description": "Example provider",
      "aliases": ["example"],
      "capabilities": ["verify-v1"],
      "fields": [
        {"key": "api_token", "label": "API Token", "type": "password", "required": true}
      ]
    }
  ]
}
```

`present` / `cleanup` stdin:

```json
{
  "action": "present",
  "provider": "example-dns",
  "config": {"api_token": "..."},
  "domain": "example.com",
  "token": "acme-token",
  "key_auth": "key-authorization"
}
```

`verify` stdin / stdout:

```json
{"action": "verify", "provider": "example-dns", "config": {"api_token": "..."}, "domain": "example.com", "mode": "read", "locale": "zh-CN"}
```

```json
{"ok": true, "checks": [{"key": "zone", "status": "ok", "message": "example.com"}]}
```

Conventions:

- Exit code `0` means success; non-zero means failure. stderr goes to the task log with sensitive values redacted.
- stdout must stay empty; output from `present` / `cleanup` is treated as an error.
- `verify` is read-only: it may list zones or read records, never create, update, or delete.
- Only providers declaring `verify-v1` receive `verify`; others fall back to a `present`/`cleanup` write test in `mode=auto`, and explicit `mode=read` reports unsupported. `checks[].status` is one of `ok`, `failed`, `skipped`, `unsupported`, `error`.
- `LACTSSL_PLUGIN_MEMORY_LIMIT` sets the byte budget; the host checks `VmRSS` and kills the process above `plugins.memory_limit_mb`.
- The host passes only a minimal environment (PATH, HOME, proxy, TLS certificates); server secrets are not exposed.
- Helper files must not be writable by others; a non-empty `manifest.sha256` is verified. `version` refers to the helper, not the LACTSSL build.

## Security recommendations

- Only the service user may write to the plugin directory; prefer `0700`.
- Install helpers only from trusted sources and provide `sha256` in the manifest.
- Set `memory_limit_mb` and `timeout_seconds`; plugins run as the service account, so do not grant the `lactssl` user extra privileges.
