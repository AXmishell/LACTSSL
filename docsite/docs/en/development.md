# Development: source and build

For engineering and integration teams customizing the product, producing private builds, or contributing. **Operations staff and end users do not need this page**; see [Installation and startup](install.md) for deployment and [Plugin governance](plugins.md) for plugin usage.

## Repository layout

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
internal/plugin/     plugin host (host/, runtime/, providers/, builtin/) and official implementations
plugins/             external plugin SDK: protocol v1, helper framework, shared utilities
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

Official DNS/deploy/notify implementations live in `internal/plugin/` and compile with the main module. `internal/plugin/providers` is a separate Go module (`lactssl/providers`) that depends only on the plugin SDK and upstream provider SDKs and is referenced through a local `replace`; `plugins/` is the external plugin SDK. Uploaded helpers run as protocol v1 subprocesses, and the host does not depend on an external plugin repository or signed index.

## Local development

### Prerequisites

- Go 1.27+
- Node.js 22.12+ and npm (required by Vite 8)
- Optional: Docker / Docker Compose v2

### Common commands

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

### Development mode ports

`make dev` starts Vite on `:5173` with HMR and Go on `:8080` (from `server.addr` / `server.port`). Open `http://<host>:5173/<entry>/`; Vite proxies `/api` and the legacy `/<entry>/api` to Go, and production has no Vite.

Options: `--no-web` starts only the backend, `--build` rebuilds with `make server-dev` before starting or restarting, and `WEB_PORT` / `DEV_HOST` set the Vite port (default `5173`) and bind address (default `0.0.0.0`).

```bash
./scripts/dev.sh start            # Start the Go backend + Vite in the background
./scripts/dev.sh status           # PID / ports / health status
./scripts/dev.sh logs panel       # Follow backend logs (logs / logs web / logs all)
./scripts/dev.sh restart --build  # Rebuild bin/lactssl-dev, then restart
./scripts/dev.sh stop             # Stop
```

PID files and logs live in `data/run/panel.pid`, `data/run/panel.log`, `data/run/web.pid`, and `data/run/web.log`. The backend port and secure entry are read from `config.yaml`, which is generated from `config.yaml.example` when missing.

## Build and version injection

- The frontend build goes to `web/dist`, is copied to `internal/webui/dist`, and is embedded with the `embed` tag.
- The version comes solely from git tags: `scripts/version.sh` runs `git describe --tags --always --dirty` (stripping `v`) and injects it with `-ldflags -X 'lactssl/internal/api.Version=...'`. Build time and architecture are handled the same way, and missing values display `dev`; there is no `VERSION` file.
- To pin a version explicitly:

```bash
LACTSSL_VERSION=1.2.3 make build
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
```

## Testing and CI

```bash
make test
make typecheck
make i18n-check
make ci
```

`make ci` is equivalent to `.github/workflows/ci.yml` and can be run in parts:

```bash
./scripts/ci.sh --backend-only
./scripts/ci.sh --frontend-only
./scripts/ci.sh --no-build
```

CI runs `gofmt` checks, `go vet`, tests with `-race` and coverage, and frontend type checking and builds.

## Internationalization

- Backend resources live in `internal/i18n/locales/`; frontend resources in `web/src/i18n/locales/`. When adding text, update `zh-CN`, `en-US`, and `zh-TW`, then run `make i18n-check`.
- `zh-TW` is bundled and checked by default, with missing strings falling back to `zh-CN`. Russian (`ru-RU`) and Japanese (`ja-JP`) are excluded from the default check and fall back `language → en-US → zh-CN`; run `make i18n-check-all` (or `I18N_CHECK_ALL=1`) for all languages.
- The check script is TypeScript (`scripts/check-i18n.ts`), run by Node's native type stripping and requiring Node 22.18+. Its type check is included in `make typecheck` via `scripts/tsconfig.json`.

## Documentation build

The documentation uses Zensical/MkDocs:

```bash
make docs-build   # Build every language in strict mode
make docs-serve   # Preview in the background (port 8000; -l picks a language; ./scripts/docs-serve.sh stop to stop)
```

Configs, sources, and output live in `docsite/`; Zensical treats the directory containing the config as the project root, so `docs_dir` and `site_dir` stay inside it. English uses `docsite/mkdocs.yml` → `docsite/site`; Simplified Chinese, Traditional Chinese, Japanese, and Russian use `docsite/mkdocs.zh.yml`, `mkdocs.zh-hant.yml`, `mkdocs.ja.yml`, and `mkdocs.ru.yml` under `docsite/site/<lang>`. `docs-internal/` is not published, and new pages must be mirrored under `docsite/docs/<language>/` and added to each language's nav.

## Release process

Releases are driven solely by tags; publishing a GitHub Release triggers the `Release` workflow:

```bash
gh release create v1.0.1 --generate-notes
```

Artifacts include `lactssl_<version>_<os>_<arch>.tar.xz` and `.tar.gz` for Linux and macOS amd64/arm64, `lactssl_<version>_windows_amd64.zip`, and `checksums.txt` (SHA-256). Archives contain the binary, `README.md`, `LICENSE`, and `config.yaml.example`; the version comes from the tag without the `v`. Both `.tar.xz` (preferred) and `.tar.gz` (for older auto-upgrade clients) are published on non-Windows platforms.

Images for `linux/amd64` and `linux/arm64` are pushed to GHCR:

```text
ghcr.io/axmishell/lactssl:<version>
ghcr.io/axmishell/lactssl:<major>.<minor>
ghcr.io/axmishell/lactssl:<major>
ghcr.io/axmishell/lactssl:latest
```

Pre-releases only push `<version>` and do not update `latest`, `<major>.<minor>`, or `<major>`. GHCR packages are private by default after the first release and must be changed to Public in package settings.

Notes:

- The workflow triggers only when a Release is published; with `gh release create --draft`, the build starts on publish. `.github/workflows/release.yml` must already exist in the tagged commit.
- Files pre-uploaded with `gh release create <tag> <files>...` that share names with automatic artifacts are overwritten with `--clobber`; do not pass files at creation time. Assets take a few minutes to build, and a tag can be rebuilt with `gh workflow run release.yml -f tag=v1.0.1`.
- Release signing: after configuring `ED25519_PRIVATE_KEY_B64`, a detached signature is generated for `checksums.txt`; the workflow and local release script derive the 32-byte public key and embed it so online upgrades work out of the box. `LACTSSL_PROGRAM_PUBLIC_KEY` overrides the embedded key.

Local release scripts:

```bash
make release TAG=v1.0.1
./scripts/release.sh v1.0.1 --no-docker
./scripts/release.sh v1.0.1 --multiarch
./scripts/release.sh v1.1.0-rc.1 --docker-only
./scripts/release.sh v1.0.1 --publish-only
./scripts/release.sh v1.0.1 --upload
```

`--multiarch` requires Docker Buildx. QEMU is not required: the image only copies host-cross-compiled binaries and runs no target-architecture commands during the build.

## Plugin integration

Official implementations live in `internal/plugin/`; `plugins/` is the external SDK, referenced by the host `go.mod` through a local `replace`. After changing an official implementation, build from the repository root with `make backend` and `make plugins-test`. Official implementations are built in with no merged bundle to build or install; uploaded plugins remain single helper executables. For protocol, fields, and signing, see [Plugin development](plugin-development.md).

## Contribution guidelines

- Never commit `config.yaml`, `data/`, backups, logs, or real credentials.
- Security-related changes must update the [Security baseline](security.md) and tests; API, configuration, or plugin changes must update both Chinese and English documentation, and new endpoints need an API key permission policy and tests.
- Data migrations need idempotent steps, backup, and rollback instructions. Run `make ci` and `make docs-build` before committing.
