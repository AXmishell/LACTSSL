# DNS plugins

LACTSSL serves every DNS provider through DNS plugins:

- The **default main binary compiles no DNS providers**; it only loads DNS plugins, keeping the binary and memory footprint small.
- The **35 providers that used to be built in** (Alibaba Cloud, Cloudflare, DNSPod, Tencent Cloud, Huawei Cloud, Volcengine, Route53, Azure, etc.) are now supplied by a helper plugin and **enabled by default**, so upgrades keep working.
- The **remaining lego providers** are also supplied by the helper and are disabled by default; enable the ones you need on the plugins page.
- The **plugin set**: both `helper-extra` and `helper-full` contain all 194 providers.
- The **full build** can still compile every lego DNS provider into the main binary via `-tags dns_all`.
- **External plugins** supply providers through a one-shot exec helper, without rebuilding; new providers can also be installed by uploading a helper.

## Build variants

```bash
make build        # lite build: bin/lactssl (no built-in providers, loaded via plugins)
make build-full   # full build: bin/lactssl-full (all 194 providers compiled in)
make helper       # reference helper (all 194 providers)
make helper-extra # plugin helper (all 194 providers)
make helper-full  # reference helper (all providers)
make gen-plugins  # regenerate plugin wrappers and build profiles
```

Build profiles are selected by mutually exclusive tags:

| Tag | Contents | Use |
|---|---|---|
| none | Empty | Default main binary (plugin host only) |
| `dns_extra` | All 194 | `helper-extra` / `helper` plugin |
| `dns_all` | All 194 | Full build / helper-full |
| `embed_helper` | Embedded helper | Release lite binary (used together with `embed`) |

The lite set is defined by the `lite` array in `scripts/dns_plugins.json` and defaults to empty. If you really want to compile providers into the main binary, add their IDs there and run `make gen-plugins`.

Without a helper the DNS provider list is empty. Once a helper is installed, the 35 former built-ins appear automatically. Existing account configuration is preserved; if the provider is unavailable, issuance fails with an explicit error.

## Plugin directory

DNS external plugins live in `<data_dir>/plugins/dns` (default `data/plugins/dns`), or configure. Deployment plugins use the sibling directory `<data_dir>/plugins/deploy`, so the two kinds of helper are never mixed:

```yaml
plugins:
  enabled: true
  dir: ""                 # empty means <data_dir>/plugins/dns
  memory_limit_mb: 128    # helper RSS limit
  timeout_seconds: 60     # per present/cleanup call
  max_output_kb: 64       # helper output cap
```

The directory is scanned at startup. After installing a helper, use **Plugins → Rescan plugins**, or call:

```bash
curl -X POST https://<host>/<entry>/api/plugins/rescan
```

The lite `lactssl` binary in release packages embeds `lactssl-dns-helper-extra`: on first start it is installed into `<data_dir>/plugins/dns`, and it is updated automatically when the embedded version changes. No manual copy is needed. Docker images use the same embedding mechanism.

You can still install or replace it with a custom helper manually; a helper saved under a different filename is never overwritten:

```bash
mkdir -p data/plugins/dns
cp lactssl-dns-helper-extra data/plugins/dns/
chmod 0755 data/plugins/dns/lactssl-dns-helper-extra
```

The full build (`dns_all`) compiles every provider into the main binary and does not need a helper.

## Uploading plugins

The DNS plugin card on the Plugins page has an **Upload plugin** button, so a helper executable can be installed without copying files onto the server:

- Only a single helper executable is accepted, up to 256 MB. It is written to a temporary file first; the `manifest` is probed and its protocol, provider IDs and `sha256` are validated before the file is moved into the plugin directory and rescanned.
- The saved filename can be changed. It may only contain letters, digits, dots, underscores and hyphens, and must start with a letter or digit.
- Replacing an existing file is rejected by default. Enable **Overwrite a plugin with the same filename** when upgrading; the old file is backed up and restored automatically if installation fails.
- Uploaded helpers run as the service account, so this operation always requires secondary verification, regardless of the per-action step-up toggles.
- Invalid or non-executable files are never installed, and partial files are not left in the plugin directory.
- Automation API: `POST /<entry>/api/plugins/upload` with multipart fields `kind` (`dns` or `deploy`), `file`, `name` and `overwrite`; complete secondary verification first and send the token in the `X-LACTSSL-VERIFY` header.

## Enabling and disabling providers

Provider availability is controlled by the plugin whitelist:

- On first start (no whitelist in settings yet), the 35 former built-in providers are enabled by default; all other plugin providers are disabled.
- The 35 default-enabled providers: alidns, aliesa, cloudflare, dnspod, tencentcloud, huaweicloud, volcengine, baiducloud, jdcloud, digitalocean, godaddy, namecheap, namesilo, porkbun, spaceship, cloudns, bunny, rainyun, westcn, pdns, dnsimple, dnsmadeeasy, route53, gcloud, azuredns, netlify, vercel, duckdns, freemyip, vultr, linode, hetzner, gandi, gandiv5, rfc2136.
- The enabled state is stored in the settings table as `plugins.dns.enabled_providers` (provider IDs only) and survives restarts and rescans.
- Only enabled plugin providers appear in the DNS provider list or the account creation dropdown; disabling one hides it immediately.
- Default-enabled providers can also be disabled. The change persists and is not reverted by a helper restart or rescan.
- Existing DNS accounts and task configurations are kept. When a provider is unavailable, the credentials page shows a notice and issuance returns a clear error.
- API: `PUT /<entry>/api/plugins/providers/{id}` with `{"enabled": true}`; bulk `PUT /<entry>/api/plugins/providers` with `{"enabled": true, "ids": []}` (an empty `ids` applies to every discovered plugin provider).
- Toggling immediately rescans the plugin directory and rebuilds the registry using the same validation as "Rescan plugins".

## Helper protocol v1

A helper is an executable with three subcommands:

```bash
dns-helper manifest   # print the JSON manifest
dns-helper present    # read a request from stdin and add the TXT record
dns-helper cleanup    # read a request from stdin and remove the TXT record
```

Example `manifest` output:

```json
{
  "protocol": "v1",
  "name": "example dns helper",
  "version": "1.0.0",
  "sha256": "optional helper checksum",
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

- Exit code `0` means success; anything else means failure. stderr is written to the task log with secrets redacted.
- stdout stays empty; output from `present/cleanup` is treated as an error.
- The helper receives its memory budget through `LACTSSL_PLUGIN_MEMORY_LIMIT` (bytes); calling `debug.SetMemoryLimit` at startup is recommended.
- The main process polls the helper's `VmRSS` and kills it when it exceeds `plugins.memory_limit_mb`.
- Only a minimal environment (PATH, HOME, proxies, TLS certificates, etc.) is passed; server secrets are not leaked.
- Helper files must not be writable by other users; a non-empty `manifest.sha256` is verified.
- `version` is informational and describes the helper/provider implementation itself; the reference helper reports the upstream lego version rather than the LACTSSL build version. The UI adds a `v` prefix only when the value does not already have one.

## Reusing lego providers

The reference implementation in `cmd/dns-helper` exposes compiled lego providers as a plugin. The default build complements the plugin set:

```bash
make helper-extra
mkdir -p data/plugins/dns
cp bin/lactssl-dns-helper-extra data/plugins/dns/dns-helper-extra
chmod 0755 data/plugins/dns/dns-helper-extra
```

`helper-extra` contains all 194 providers. The helper starts only when needed and exits after issuance, so it is not resident. To compile every provider into the main binary instead:

```bash
make build-full
# or
make helper-full
cp bin/lactssl-dns-helper-full data/plugins/dns/dns-helper-full
chmod 0755 data/plugins/dns/dns-helper-full
```

The full build (`dns_all`) compiles providers into the main binary; built-in IDs take precedence over a helper, and duplicates in the helper are skipped and reported by the rescan result. Release packages already include `lactssl-dns-helper-extra` (`.exe` on Windows); place it in the plugin directory and rescan.

## Security recommendations

- The plugin directory should only be writable by the service user; prefer `0700`.
- Only install helpers from trusted sources and provide `sha256` in the manifest.
- Set sensible `memory_limit_mb` and `timeout_seconds` to protect the host from malicious or malfunctioning plugins.
- Helpers run as the service account; do not grant the `lactssl` user extra privileges.
