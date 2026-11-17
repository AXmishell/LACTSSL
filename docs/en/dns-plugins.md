# DNS plugins

DNS plugins provide domain provider API access for DNS-01 validation by public CAs. When evaluating options, the priority is not how many providers are supported, but whether the solution can deliver **least-privilege credentials, rotation, auditability, and offline deployment**.

## 1. Prerequisites

- Credentials for the relevant provider have been created under **DNS accounts**.
- The DNS plugin and the specific provider have been installed and enabled on the **Plugins** page.
- The zone containing the domain is visible to the credential and permits creating and deleting TXT records.
- Outbound access to the provider API is available. If a proxy is used, confirm the Go HTTP client proxy configuration.

If no DNS plugin is installed, the DNS provider list is empty. Existing DNS accounts are preserved, but issuance fails with an explicit error when the provider is unavailable.

## 2. Choosing a plugin package

The official [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins) repository provides three DNS packages covering all 194 providers:

| Package | Coverage | Extracted size | Recommended for |
| --- | --- | ---: | --- |
| DNS common | 184 common providers | about 21 MiB | Most enterprises |
| DNS heavy cloud providers | 10 providers with heavy SDKs | about 39 MiB | Google Cloud, Azure, Route 53, and similar |
| DNS full (refactored) | 194 providers | about 18 MiB | A single package covering every provider |

- Each package shares one Go runtime, avoiding the disk and process overhead of packaging providers individually.
- Main release packages bundle no providers; install them on demand.
- Provider icons are delivered with the plugin package and repository index, then cached under `<data_dir>/plugin-icons/` after installation.

## 3. Installing and enabling

1. Open the **Plugins** page, confirm the plugin repository URL, and click **Refresh index**.
2. Open the **Plugin market**, filter by DNS, and install the required package.
3. Enable specific providers in the DNS provider list. Newly installed entries are disabled by default.
4. Create credentials under **DNS accounts** and select the provider.

A merged package is not enabled in full by the row-level switch; enabling more than 10 entries at once requires confirmation. When a file is disabled at the file level, its entries are not registered at runtime, but existing selections are retained.

Install, update, and uninstall require only a login session or `plugins:write`. Plugin packages verify the index signature (when a public key is configured), the asset SHA-256, and the provider IDs in `plugin.json` and the manifest. Any mismatch rejects the installation and rolls it back.

## 4. Credential governance

### 4.1 Least privilege

- Use a dedicated sub-account or API key; do not use a personal account or a global administrator key.
- Grant DNS read and write access only to the target zone. Do not grant account-level billing, domain registration, or zone deletion permissions.
- Where the provider supports per-domain or per-project authorization, limit access to the domains that are actually issued.
- Use separate credentials and DNS sub-accounts for testing and production.

### 4.2 Rotation

- Record the credential owner, creation date, permission scope, and rotation interval.
- When rotating, update the credential in LACTSSL first, then revoke the old credential at the provider.
- After rotation, run a test task to confirm that TXT creation and cleanup work correctly.
- Rotate immediately after staff departures, key exposure, or provider security incidents.

### 4.3 Storage

- Credentials are encrypted with AES-256-GCM before being written to the database and are masked in API responses.
- When editing an account, leave a secret field empty to keep the existing value.
- Do not write credentials into task names, notes, tickets, or logs.
- Backup archives contain encrypted credentials; handle them as confidential files.

## 5. Common providers

| Provider | Main credentials | Notes |
| --- | --- | --- |
| Alibaba Cloud DNS (`alidns`) | AccessKey ID / Secret | Optional Region |
| Alibaba Cloud ESA (`aliesa`) | AccessKey ID / Secret | Optional Region |
| Cloudflare | API Token, or Email + Global API Key | Optional API upstream URL |
| DNSPod | API Token | |
| Tencent Cloud (`tencentcloud`) | SecretId / SecretKey | Optional Region |
| Huawei Cloud (`huaweicloud`) | AccessKey ID / SecretAccessKey / Region | |
| Volcengine (`volcengine`) | AccessKey / SecretKey | Optional Region, API host, and scheme |
| Baidu AI Cloud (`baiducloud`) | AccessKey ID / SecretAccessKey | |
| JD Cloud (`jdcloud`) | AccessKey ID / AccessKey Secret | Optional Region |
| AWS Route 53 | AccessKey / SecretAccessKey | Optional Region, Hosted Zone, private zone |
| Google Cloud DNS | Service account JSON | Optional project ID |
| Azure DNS | Tenant ID / Client ID / Client Secret / Subscription ID / resource group | |
| DigitalOcean / Vultr / Linode / Hetzner | API token / API key | |
| Gandi / GoDaddy / Namecheap / NameSilo / Porkbun | Provider-specific API credentials | |
| PowerDNS (`pdns`) | API URL / API key | Optional server name and API version |
| RFC 2136 dynamic update | DNS server | Optional TSIG key / secret / algorithm and zone list |

- After installing the official DNS plugin, all lego providers are supported (about 190).
- The `manual` provider requires adding TXT records by hand and is not part of the automated flow.
- Cloudflare, DNSimple, PowerDNS, Volcengine, and others support a custom API upstream URL for proxies, self-hosted services, or compatible gateways.
- The provider selector on the **DNS accounts** page is the authoritative full list.

## 6. Offline and manual installation

- Upload a single helper executable on the **Plugins** page, up to 256 MB.
- After upload, the `manifest` is probed from a temporary file, and the protocol, provider IDs, and SHA-256 are validated before the file is written to the plugin directory and rescanned.
- Overwriting a file with the same name is rejected by default. When upgrading, select **Allow overwriting a plugin with the same filename**; the old file is backed up first.
- Alternatively, point `plugins.repo.url` at an internal mirror index:

```yaml
plugins:
  repo:
    url: "file:///srv/lactssl-plugins/index.json"
```

A manually uploaded multi-provider bundle still loads, but is marked `manual` and does not participate in official repository updates.

## 7. Validation and troubleshooting

### 7.1 Validation

1. Create a DNS account and save it successfully.
2. Create a test task and select the account and a test domain.
3. Run the task and observe TXT creation, CA validation, and TXT cleanup.
4. Review the task log and operation log.
5. Delete the test certificate or retain it as needed.

### 7.2 Common issues

| Symptom | Cause | Resolution |
| --- | --- | --- |
| Provider dropdown is empty | Plugin not installed or not enabled | Install a DNS package and enable the provider |
| Credential save fails | Required field missing or invalid format | Correct it according to the form and log messages |
| TXT creation fails | Permissions, zone, or quota | Check API permissions, domain, and quota |
| TXT cleanup fails | Permissions, or the record was already deleted | Remove the residual TXT record manually |
| Validation times out | Slow DNS propagation, or authoritative DNS not updated | Check TTL, authoritative resolution, and CAA |
| Custom API URL fails | URL unreachable or certificate untrusted | Check network, TLS, and the upstream URL |
| Plugin fails to load | Permissions, digest, or protocol | Check directory permissions and the installation record |

## 8. Helper protocol v1 (integration development)

This section is intended for plugin developers; operations staff generally do not need it.

A helper is an executable that supports three subcommands:

```bash
dns-helper manifest   # print the JSON manifest
dns-helper present    # read a request from stdin and add the TXT record
dns-helper cleanup    # read a request from stdin and delete the TXT record
```

Example `manifest` output:

```json
{
  "protocol": "v1",
  "name": "example dns helper",
  "version": "1.0.0",
  "sha256": "optional helper file checksum",
  "providers": [
    {
      "id": "example-dns",
      "name": "Example DNS",
      "description": "Example provider",
      "aliases": ["example"],
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

Conventions:

- Exit code `0` means success; a non-zero exit means failure. stderr is written to the task log (sensitive values are redacted).
- stdout must remain empty; output from `present/cleanup` is treated as an error.
- The helper receives its memory budget from `LACTSSL_PLUGIN_MEMORY_LIMIT` (bytes).
- The main program periodically checks the helper's `VmRSS` and terminates the process, logging an error, when it exceeds `plugins.memory_limit_mb`.
- The main program passes only a minimal set of environment variables (PATH, HOME, proxy, TLS certificates, and similar); server secrets are not exposed.
- Helper files must not be writable by other users. When `manifest.sha256` is non-empty, the file digest is verified.
- `version` is the version of the helper or provider implementation, not the LACTSSL build version.

## 9. Security recommendations

- Only the service user may write to the plugin directory; prefer `0700`.
- Install only helpers from trusted sources, and provide `sha256` in the manifest.
- Set appropriate `memory_limit_mb` and `timeout_seconds` to prevent a malfunctioning plugin from degrading the host.
- Plugins run under the service account; do not grant the `lactssl` user additional privileges.
- In production, use an internal mirror, pin versions, and disable automatic updates.

## Related documentation

- [Plugin governance](plugins.md)
- [Features](features.md)
- [Deployment plugins](deploy-plugins.md)
- [Notification plugins](notify-plugins.md)
