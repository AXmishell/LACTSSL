# Development: plugin protocol

This page is intended for plugin developers, integrators, and enterprise platform teams. It describes the LACTSSL plugin protocol, build, packaging, index, signing, and release process. **Operations staff usually only need [Plugin governance](plugins.md) and the relevant DNS, deployment, or notification plugin pages.**

## 1. Repository layout

Plugin sources, build tooling, and the repository index live in the separate [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins) repository. The host implements protocol v1 manifest decoding locally and downloads merged plugin packages by kind at runtime; the two repositories no longer share Go source through `go.mod`.

```text
protocol/        wire format for plugin protocol v1 (plugin-side implementation)
sdk/helper/      helper bootstrap: memory budget, JSON, error exit
catalog/         plugin-internal provider / deploy target metadata
i18n/            plugin-side translations
outbound/        SSRF-safe outbound HTTP client
dns/providers/<id>        194 DNS provider wrappers
deploy/providers/<id>     deploy target implementations
notify/providers/channels notification channel implementations
cmd/dns-helper/           DNS merged bundle entrypoint (all providers)
cmd/deploy-helper/        deploy merged bundle entrypoint (all targets)
cmd/notify-helper/        notification merged bundle entrypoint (all channels)
plugins.json              merged bundle manifest
scripts/pluginctl         generate, build, package, index, sign
```

## 2. Protocol v1

A plugin is an executable that starts on each invocation and exits when finished. The protocol remains `v1`; only breaking changes bump it to `v2`.

### 2.1 Subcommands

DNS:

```text
dns-plugin manifest
dns-plugin present   # stdin JSON
dns-plugin cleanup   # stdin JSON
```

Deployment:

```text
deploy-plugin manifest
deploy-plugin deploy
```

Notification:

```text
notify-plugin manifest
notify-plugin send
```

### 2.2 Conventions

- Exit code `0` means success; any non-zero value means failure. stderr goes to the main program log.
- DNS `present` / `cleanup` stdout must be empty.
- Deployment / notification stdout may be empty or contain a single line `{"ok": true, "message": "..."}`; invalid JSON is treated as an error.
- The memory budget is passed through `LACTSSL_PLUGIN_MEMORY_LIMIT` (bytes); call `debug.SetMemoryLimit` at startup.
- The main program passes only a minimal environment variable set (PATH, HOME, proxy, TLS certificates, and similar).
- The main program polls the helper's `VmRSS` and terminates the entire process group when it exceeds `memory_limit_mb`.
- Helper files must be executable and must not be writable by group or other users; symbolic links are rejected.
- `manifest.sha256` may be left empty; repository installs rely on the index asset SHA-256 and installation records for integrity.

## 3. Merged bundle entrypoints

The two common/heavy DNS packages share one entrypoint and select providers through build tags; deployment and notification each have one entrypoint:

```bash
go build -trimpath -tags "bundle_dns_core,nethttpomithttp2"  -o lactssl-dns-core  ./cmd/dns-helper
go build -trimpath -tags "bundle_dns_heavy,nethttpomithttp2" -o lactssl-dns-heavy ./cmd/dns-helper
go build -trimpath -tags nethttpomithttp2 -o lactssl-deploy-all ./cmd/deploy-helper
go build -trimpath -tags nethttpomithttp2 -o lactssl-notify-all ./cmd/notify-helper
```

`manifest` outputs every item of that kind; after building, `pluginctl` verifies that the manifest matches the `providers` list in `plugins.json` exactly.

`plugins.json` maintains the merged bundle manifest (`providers` omitted means only part of the items are shown):

```json
{
  "schema": 2,
  "plugins": [
    {"kind": "dns", "id": "core", "version": "1.0.0", "tag": "bundle_dns_core", "bundle": true, "providers": ["cloudflare", "..."]},
    {"kind": "dns", "id": "heavy", "version": "1.0.0", "tag": "bundle_dns_heavy", "bundle": true, "providers": ["gcloud", "..."]},
    {"kind": "deploy", "id": "all", "version": "1.0.0", "tag": "bundle_deploy_all", "bundle": true},
    {"kind": "notify", "id": "all", "version": "1.0.0", "tag": "bundle_notify_all", "bundle": true}
  ]
}
```

Generate the manifest:

```bash
make gen
# or
go run scripts/pluginctl/main.go manifest
go run scripts/pluginctl/main.go gen
```

DNS provider wrappers live in `dns/providers/<id>`. Each wrapper exposes one lego provider and is ultimately linked into the same merged DNS helper. To add or update a provider, change `dns/providers`, bump the version in `plugins.json`, and republish the corresponding plugin.

## 4. Packaging

```bash
make plugin KIND=dns ID=core GOOS=linux GOARCH=amd64
```

Output:

```text
dist/
├── lactssl-dns-core_1.0.0_linux_amd64.tar.gz
└── lactssl-dns-core_1.0.0_linux_amd64.meta.json
```

Package contents:

```text
tar.gz
├── plugin.json
├── lactssl-dns-core
├── icons/                 # optional: provider icons covered by this package
│   ├── cloudflare.svg
│   └── alidns.svg
└── LICENSE
```

`plugin.json`:

```json
{
  "schema": 1,
  "kind": "dns",
  "id": "core",
  "version": "1.0.0",
  "protocol": "v1",
  "os": "linux",
  "arch": "amd64",
  "entrypoint": "lactssl-dns-core",
  "bundle": true,
  "providers": ["cloudflare", "..."],
  "icons": {
    "cloudflare": { "file": "icons/cloudflare.svg", "sha256": "…" }
  }
}
```

Icons live in the repository at `assets/icons/<kind>/<provider>.svg|png|webp`; `icons.json` can declare reuse for historical IDs. `build` collects icons according to `providers`; each icon must decode to no more than 64 KB and be svg, png, or webp. `sha256` is computed over the raw icon bytes and verified at extraction; missing or extra icons cause installation to fail.

`build` runs `manifest` once on the current platform to validate the item set and version; cross-compiled platforms skip execution. The `uncompressed_size` in the index is the size of the extracted helper and is also the disk usage shown in the plugin market. The root-level `icons` in the index inline these icons for display in the market before installation.

## 5. Index

```bash
make index BASE_URL=https://github.com/AXmishell/LACTSSL-Plugins/releases/download
```

Generated `index.json`:

```json
{
  "schema": 1,
  "channel": "stable",
  "protocol": "v1",
  "generated_at": "2026-11-15T10:00:00Z",
  "plugins": [
    {
      "kind": "dns",
      "id": "core",
      "name": "DNS plugin bundle (common)",
      "version": "1.0.0",
      "protocol": "v1",
      "bundle": true,
      "providers": ["cloudflare", "..."],
      "uncompressed_size": 60596384,
      "assets": {
        "linux/amd64": {
          "url": "https://.../plugin-assets-dns/lactssl-dns-core_1.0.0_linux_amd64.tar.gz",
          "sha256": "…",
          "size": 8019751
        }
      }
    }
  ]
}
```

- The index keeps only the latest version of each plugin.
- Every asset must include a 64-hex SHA-256; the host rejects downloads when the hash is missing or malformed.
- `min_host` is optional and sets the minimum host version.
- `assets` is keyed by `GOOS/GOARCH`.

### 5.1 Signing

```bash
go run scripts/pluginctl/main.go sign -key key.b64 -in index.json -out index.json.sig
```

`key.b64` is a base64-encoded Ed25519 private key (32-byte seed or 64-byte private key). After a public key is configured, the main program verifies `index.json.sig`; each asset is then verified by SHA-256.

## 6. Release

`.github/workflows/release.yml`:

- Builds a matrix from `plugins.json`, with one job per plugin.
- Initial platforms: linux amd64/arm64, darwin arm64, windows amd64.
- Publishes one Release per plugin, tagged `<kind>-<id>-v<version>`.
- Aggregates all `.meta.json` files into `index.json` and publishes it to the `plugin-index` Release.
- When `PLUGIN_INDEX_SIGNING_KEY` is configured, also publishes `index.json.sig`.

## 7. Field definitions

DNS field example:

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

stdin for DNS `present` / `cleanup`:

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

Deployment target fields:

- `fields`: authentication and connection information in target credentials.
- `deploy_fields`: parameters for the "Deploy certificate" node in a task workflow.
- Field `key` values must be unique across both types; supported field types are `text`, `password`, `textarea`, `number`, `select`, and `switch`.
- When a field sets `options_source`, the host calls the helper's `options` subcommand to load dynamic options; `multiple: true` allows selecting several values, which are still stored as a comma-separated string. Older hosts ignore both optional fields.
- `secret: true` or `type: "password"` masks the value and stores it encrypted.
- `name` / `description` / `label` / `placeholder` / `help` support `*_i18n`; language keys use BCP 47 (such as `zh-CN`, `en-US`), with a maximum of 32 languages per table.

Notification channel fields follow the same rules; a channel `id` must not conflict with the built-in email channel `mail`.

stdin for dynamic deployment field options (`options`):

```json
{
  "action": "options",
  "provider": "baota",
  "field": "sites",
  "source": "baota_sites",
  "query": "optional search keyword",
  "auth": {"panel_url": "https://panel.example.com:8888", "api_secret": "..."},
  "config": {"target": "docker"}
}
```

stdout returns `{"options":[{"label":"site name","value":"site name"}]}`. The helper should only return non-sensitive names; the host limits the count, removes duplicates, and truncates abnormal output.

For the stdin of deployment `deploy` and notification `send`, see [Deployment plugins](deploy-plugins.md) and [Notification plugins](notify-plugins.md).

## 8. Compatibility

- The protocol remains `v1`; only breaking changes bump it to `v2`.
- Each plugin uses independent SemVer; to upgrade, change the version in `plugins.json` and republish.
- The main program filters incompatible plugins by `protocol` and `min_host`.
- A merged bundle manifest can contain multiple items; the current release set includes DNS common, DNS large cloud providers, deployment, notification, and the full DNS refactor bundle.
- New fields should remain backward compatible; older main programs ignore unknown optional fields.

## 9. Development checklist

- [ ] `manifest` outputs valid JSON with protocol `v1`.
- [ ] IDs are unique and follow the lowercase letter / digit / underscore / hyphen rule.
- [ ] Field definitions are complete, and sensitive fields are marked `secret` or `password`.
- [ ] Exit codes and stdout/stderr conventions are correct.
- [ ] Memory budget, timeouts, and output size are within limits.
- [ ] Helper file permissions are correct and do not allow group or other users to write.
- [ ] Icon format and size meet the requirements.
- [ ] The index includes SHA-256 and a correct `min_host`.
- [ ] The signature can be verified.
- [ ] Run `manifest` and an actual invocation once on the target platform.
- [ ] Update documentation and change notes.

## Related documentation

- [Plugin governance](plugins.md)
- [DNS plugins](dns-plugins.md)
- [Deployment plugins](deploy-plugins.md)
- [Notification plugins](notify-plugins.md)
- [Development: source and build](development.md)
