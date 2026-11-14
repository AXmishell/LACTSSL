# Development

## Common commands

```bash
make dev          # Vite + Go development mode
make rebuild-run  # Rebuild the frontend and backend binary, then start Vite + Go
make build        # Production build, output at bin/lactssl (no built-in DNS providers)
make build-full   # Full build, output at bin/lactssl-full (all DNS providers)
make backend      # Backend only, output at bin/lactssl (CGO_ENABLED=0 static build)
make helper       # Build the DNS exec helper (all 194 providers)
make helper-extra # Build the plugin DNS exec helper (all 194 providers)
make helper-full  # Build the full DNS exec helper (all 194 providers)
make deploy-helper # Build the deployment plugin (FTP, Webhook, CDN, panels, etc.)
make deploy-helper-install # Build and install into data/plugins/deploy; rescan plugins to activate
make run          # Run go run ./cmd/server directly
make test         # go test ./...
make typecheck    # Frontend vue-tsc + tsc for scripts
make ci           # Full local CI
make i18n-check   # Verify that Chinese and English translation keys match
make tidy         # go mod tidy + install frontend dependencies
make clean        # Remove build artifacts
```

Release builds embed `lactssl-dns-helper-extra` into the lite `lactssl` binary (via the `embed_helper` tag) and install it into the plugin directory on first start. A regular `make build` does not embed the helper, which keeps development builds small.

`make ci` is equivalent to `.github/workflows/ci.yml` and can also be split with arguments:

```bash
./scripts/ci.sh --backend-only
./scripts/ci.sh --frontend-only
./scripts/ci.sh --no-build
```

`make release` is equivalent to `.github/workflows/release.yml` and is a dry run by default: it cross-compiles and packages according to the release matrix into `dist/`, generates `checksums.txt`, but does not push images or upload a Release:

```bash
make release TAG=v1.0.1       # Specify a tag
./scripts/release.sh v1.0.1 --no-docker
./scripts/release.sh v1.0.1 --upload   # Upload to a Release with the same name using gh (overwrites assets with the same name)
```

## Rebuild and run

When you need to "rebuild and restart" after changing code:

```bash
make rebuild-run
```

This is equivalent to `./scripts/rebuild-run.sh`, which performs the following in order:

1. Build the frontend into `web/dist`
2. Compile `bin/lactssl-dev` in development mode (without `-tags embed`, reading `web/dist` at runtime)
3. Stop old processes occupying the configured port (default `8080`) and `5173`
4. Start Vite (`5173`) and the Go binary (`8080`)

Open `http://<host>:5173/<entry>/`. The secret entry is resolved from `LACTSSL_ENTRY`, then `security.entry` in `config.yaml`, and finally the persisted entry in the database; you can still override it explicitly:

```bash
LACTSSL_ENTRY=xxxxxxxx make rebuild-run
```

The script runs in the foreground; `Ctrl+C` stops both Vite and Go. Use `make dev` when only changing the frontend; Vite hot reload means no rebuild is needed.

## Directory layout

```text
cmd/server/          Program entry point, startup, and secret entry initialization
internal/api/        Gin routes, handlers, sessions, and rate limiting
internal/auth/       JWT and password/TOTP logic
internal/config/     config.yaml and environment variable loading
internal/db/         SQLite opening and migrations
internal/i18n/       Backend internationalization and language resources
internal/logging/    Process logging, levels, formats, and file rotation
internal/systemlog/  Async persistence of process logs for the System Logs page
internal/model/      Database entities and task workflows
internal/acme/       Built-in ACME server protocol (directory, nonce, accounts, JWS, validation)
internal/service/    ACME client, deployment, notifications, monitoring, scheduling
internal/store/      Data access layer
internal/util/       Encryption, validation, and other utilities
internal/webui/      Embedded frontend build output
web/                 Vue 3 + Element Plus frontend
scripts/             Development scripts and CI (TypeScript, run directly by Node)
```

## Build and version injection

- Frontend build output goes to `web/dist` first, then is copied to `internal/webui/dist` and embedded into the binary with the `embed` tag.
- Versions come solely from git tags: `scripts/version.sh` runs `git describe --tags --always --dirty` (stripping the `v` prefix), and the result is injected with `-ldflags -X 'lactssl/internal/api.Version=...'`; build time and architecture are handled the same way. The repository no longer keeps a `VERSION` file.
- Releases are driven by tags; see [Operations](operations.md#versioning). Use `LACTSSL_VERSION` to override the version for local builds.
- When not injected, the version is shown as `dev`.

## Frontend development

`make dev` starts Vite (`:5173`) and Go (`:8080`). The Vite development server emulates secret entry validation; the entry is resolved from `LACTSSL_ENTRY`, then `security.entry` in `config.yaml`, and finally the persisted entry in the database.

During development, open the Vite address; `/<entry>/api` requests are proxied to the Go service.

Note that the proxy is one-way: **8080 does not reverse proxy 5173**:

| Port | Who listens | Frontend asset source | Relationship with the other port |
| --- | --- | --- | --- |
| `5173` | Vite development server (development mode only) | `web/` source with HMR | Proxies `/<entry>/api` to `8080` |
| `8080` | Go binary (same in development and production) | Frontend output embedded in the binary from `internal/webui/dist` | Does not proxy `5173` and does not depend on Vite |

## Internationalization

Backend language resources live in `internal/i18n/locales/`, and frontend resources in `web/src/i18n/locales/`. When adding text, update both `zh-CN` and `en-US`, then run:

```bash
make i18n-check
```

The check script is TypeScript (`scripts/check-i18n.ts`), run directly by Node's native type stripping and requiring Node 22.18 or later; the script's own type check is included in `make typecheck` (see `scripts/tsconfig.json`).

## Testing

```bash
make test
```

CI also runs `gofmt` checks, `go vet`, tests with `-race`, coverage reporting, and frontend type checking and builds.
