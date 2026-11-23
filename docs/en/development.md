# Development: source and build

This page is intended for engineering and integration teams that need to customize the product, produce private builds, or contribute to the project. **Operations staff and end users do not need this page**; for production deployment see [Installation and startup](install.md), and for plugin usage see [Plugin governance](plugins.md).

## 1. Repository layout

```text
cmd/server/          Program entry point, startup, and secure entry initialization
internal/api/        Gin routes, handlers, sessions, and rate limiting
internal/auth/       JWT and password/TOTP logic
internal/config/     config.yaml loading and validation
internal/db/         SQLite opening and migrations
internal/i18n/       Backend internationalization and language resources
internal/logging/    Process logging, levels, formats, file rotation, and async persistence
internal/model/      Database entities and task workflows
internal/acme/       Built-in ACME server protocol
internal/service/    ACME client, deployment, notifications, monitoring, scheduling
internal/plugin/     plugin host: dns/deploy/notify, exec, install, meta, proto, repo
internal/store/      Data access layer
internal/util/       Encryption, validation, and other utilities
internal/webui/      Embedded frontend build output
web/                 Vue 3 + Element Plus frontend
scripts/             Development scripts and CI (TypeScript, run directly by Node)
docs/                Simplified Chinese documentation
docs/en/             English documentation
docs/zh-hant/        Traditional Chinese documentation
docs/ja/             Japanese documentation
docs/ru/             Russian documentation
```

Plugin implementations, official helpers, the plugin protocol, and plugin metadata live in the separate [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins) repository. The host implements protocol v1 manifest decoding locally; the repositories integrate at runtime through the signed index and `min_host`, not through a shared Go module.

## 2. Local development

### 2.1 Prerequisites

- Go 1.27+
- Node.js 22.12+ and npm (required by Vite 8)
- Optional: Docker / Docker Compose v2

### 2.2 Common commands

```bash
make dev          # Start Vite + Go development mode in the background
make dev-down     # Stop development processes
make dev-restart  # Restart development processes
make dev-status   # Show PID / ports / health status
make dev-logs     # Follow backend and Vite logs
make build        # Production build, output at bin/lactssl
make backend      # Backend only, output at bin/lactssl
make server-dev   # Development backend, output at bin/lactssl-dev (no embedded frontend)
make run          # Run go run ./cmd/server directly
make test         # go test ./...
make typecheck    # Frontend vue-tsc + tsc for scripts
make ci           # Full local CI
make i18n-check   # Verify translation keys (Chinese, English and Traditional Chinese by default)
make i18n-check-all # Check every language (including Russian and Japanese)
make tidy         # go mod tidy + install frontend dependencies
make clean        # Remove build artifacts
make docs-build   # Build Chinese and English documentation (strict)
make docs-serve   # Preview the bilingual documentation locally
```

### 2.3 Development mode ports

`make dev` starts:

- Vite: `:5173`, with HMR, serving frontend sources.
- Go: `:8080` by default, read from `server.addr` / `server.port` in `config.yaml`.

Open `http://<host>:5173/<entry>/`. Vite proxies `/api` and the legacy `/<entry>/api` to Go; Go on `8080` **does not** reverse proxy `5173`, and there is no Vite in production.

Development mode scripts:

```bash
./scripts/dev.sh start            # Start the Go backend + Vite in the background
./scripts/dev.sh status           # PID / ports / health status
./scripts/dev.sh logs panel       # Follow backend logs (logs / logs web / logs all)
./scripts/dev.sh restart --build  # Rebuild bin/lactssl-dev, then restart
./scripts/dev.sh stop             # Stop
```

PID files and logs live in `data/run/`:

```text
data/run/panel.pid  data/run/panel.log
data/run/web.pid    data/run/web.log
```

Common options:

| Item | Description |
| --- | --- |
| `--no-web` | Start the backend only, without Vite |
| `--build` | Run `make server-dev` to rebuild before starting or restarting |
| `WEB_PORT` / `DEV_HOST` | Vite port (default `5173`) and bind address (default `0.0.0.0`) |

The backend port and secure entry are both read from `config.yaml`; when the file does not exist, it is generated from `config.yaml.example`.

## 3. Build and version injection

- The frontend build output is written to `web/dist` first, then copied to `internal/webui/dist` and embedded into the binary with the `embed` tag.
- The version number comes solely from git tags. `scripts/version.sh` derives it with `git describe --tags --always --dirty` (stripping the `v` prefix) and injects it with `-ldflags -X 'lactssl/internal/api.Version=...'`.
- Build time and architecture are handled the same way; when they are not injected, the version displays as `dev`.
- The repository does not maintain a `VERSION` file.
- To pin a version explicitly:

```bash
LACTSSL_VERSION=1.2.3 make build
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
```

## 4. Testing and CI

```bash
make test
make typecheck
make i18n-check
make ci
```

`make ci` is equivalent to `.github/workflows/ci.yml` and can also be run in parts:

```bash
./scripts/ci.sh --backend-only
./scripts/ci.sh --frontend-only
./scripts/ci.sh --no-build
```

CI also runs `gofmt` checks, `go vet`, tests with `-race` and coverage reporting, plus frontend type checking and builds.

## 5. Internationalization

- Backend language resources live in `internal/i18n/locales/`.
- Frontend language resources live in `web/src/i18n/locales/`.
- When adding text, update `zh-CN`, `en-US` and `zh-TW`, then run `make i18n-check`.
- Traditional Chinese (`zh-TW`) is bundled and included in the default check; missing strings fall back directly to `zh-CN`. Russian (`ru-RU`) and Japanese (`ja-JP`) are excluded from the default check; missing strings fall back `language → en-US → zh-CN`. After syncing translations, run `make i18n-check-all` (or `I18N_CHECK_ALL=1`) for a full check of the remaining languages.
- The check script is TypeScript (`scripts/check-i18n.ts`), run directly by Node's native type stripping and requiring Node 22.18+.
- The script's own type check is included in `make typecheck` (configuration in `scripts/tsconfig.json`).

## 6. Documentation build

The documentation uses Zensical/MkDocs:

```bash
make docs-build   # Build every language in strict mode
make docs-serve   # Preview in the background (port 8000; -l picks a language; ./scripts/docs-serve.sh stop to stop)
```

- English: `mkdocs.yml` → `site` (default site).
- Simplified Chinese: `mkdocs.zh.yml` → `site/zh`.
- Traditional Chinese: `mkdocs.zh-hant.yml` → `site/zh-hant`.
- Japanese: `mkdocs.ja.yml` → `site/ja`.
- Russian: `mkdocs.ru.yml` → `site/ru`.
- `docs-internal/` holds internal design records and is not published to the site.
- New pages must be mirrored under `docs/<language>/` and added to each language's nav.

## 7. Release process

Releases are driven solely by tags. Publishing a GitHub Release triggers the `Release` workflow, which builds and uploads artifacts for each platform:

```bash
gh release create v1.0.1 --generate-notes
```

Artifacts include:

- `lactssl_<version>_linux_amd64.tar.gz`
- `lactssl_<version>_linux_arm64.tar.gz`
- `lactssl_<version>_darwin_amd64.tar.gz`
- `lactssl_<version>_darwin_arm64.tar.gz`
- `lactssl_<version>_windows_amd64.zip`
- `checksums.txt` (SHA-256)

Archives contain the binary, `README.md`, `LICENSE`, and `config.yaml.example`. The version comes directly from the tag (with the `v` prefix removed) and does not rely on local git derivation.

Images for `linux/amd64` and `linux/arm64` are also built and pushed to GHCR:

```text
ghcr.io/axmishell/lactssl:<version>
ghcr.io/axmishell/lactssl:<major>.<minor>
ghcr.io/axmishell/lactssl:<major>
ghcr.io/axmishell/lactssl:latest
```

Pre-releases only push the full `<version>` tag and do not update `latest`, `<major>.<minor>`, or `<major>`. After the first release, GHCR packages are private by default and must be changed to Public in the package settings.

Notes:

- The workflow only triggers when a Release is formally published; with `gh release create --draft`, the build starts only after you publish it.
- Before first use, `.github/workflows/release.yml` must already be included in the commit the tag points to.
- Files pre-uploaded with `gh release create <tag> <files>...` that share names with automatic artifacts are overwritten by the workflow with `--clobber`; do not pass files at creation time.
- Release assets take a few minutes to finish building; if you need to make the release public only after assets are ready, push the tag first and create the Release after CI completes (this requires a separate tag-push flow).
- Rebuild a specific tag with `gh workflow run release.yml -f tag=v1.0.1`.
- Release signing: after configuring `ED25519_PRIVATE_KEY_B64`, a detached signature is generated for `checksums.txt`; the release workflow and local release script derive the 32-byte public key and embed it into official binaries so online upgrades work out of the box. You can also set `LACTSSL_PROGRAM_PUBLIC_KEY` to choose the embedded public key.

Local release scripts:

```bash
make release TAG=v1.0.1
./scripts/release.sh v1.0.1 --no-docker
./scripts/release.sh v1.0.1 --multiarch
./scripts/release.sh v1.1.0-rc.1 --docker-only
./scripts/release.sh v1.0.1 --publish-only
./scripts/release.sh v1.0.1 --upload
```

`--multiarch` requires Docker Buildx; cross-architecture builds also require QEMU.

## 8. Plugin integration

Plugin sources, build tooling, and the repository index live in `LACTSSL-Plugins`. The host builds independently; when you need to change plugin implementations and verify end to end, temporarily create a workspace:

```bash
go work init . ../LACTSSL-Plugins
```

After changing a plugin implementation, run `make gen` and `make plugin KIND=dns GOOS=linux GOARCH=amd64` in the plugin repository to build the merged DNS bundle. Delete `go.work` after verification (the file is gitignored).

For plugin protocol, fields, and signing, see [Plugin development](plugin-development.md).

## 9. Contribution guidelines

- Do not commit `config.yaml`, `data/`, backups, logs, or any real credentials.
- When changing security-related logic, update the [Security baseline](security.md) and tests at the same time.
- When changing API, configuration, or plugin behavior, update both the Chinese and English documentation.
- New endpoints must include an API key permission policy and tests.
- Changes that involve data migration must provide idempotent steps, backup, and rollback instructions.
- Run `make ci` and `make docs-build` before committing.

## Related documentation

- [Plugin development](plugin-development.md)
- [Configuration reference](configuration.md)
- [Upgrade and change management](upgrade.md)
- [Security baseline](security.md)
