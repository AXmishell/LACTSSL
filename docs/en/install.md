# Installation

## Requirements

- Go 1.27+
- Node.js 22+ and npm
- For Docker: Docker and Docker Compose v2 (optional)

## Build from source

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```

`make build` first builds the frontend (copying the output to `internal/webui/dist`), then compiles `bin/lactssl` with the frontend assets embedded.

FreeBSD can also be built directly from source. If you only need a prebuilt binary, download `lactssl_<version>_freebsd_amd64.tar.gz` or `lactssl_<version>_freebsd_arm64.tar.gz` from a release.

If `config.yaml` does not exist, the service generates a configuration file with random keys on first startup. Copying the example file manually preserves all the comments.

## First access

The startup log prints the secret entry address, for example:

```text
http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
```

Open the admin page through that address and create an administrator account when prompted. You can later enable TOTP two-factor authentication or change the secret entry in Settings.

## Development mode

```bash
make dev
```

Development mode starts Vite (`:5173`) and the Go service (default `:8080`) together; Vite proxies `/<entry>/api` to Go. Open the Vite address printed in the terminal.

The proxy is one-way: only Vite forwards API requests to Go. **Go on `8080` does not reverse proxy `5173`.** Port `8080` serves the embedded frontend assets (`internal/webui/dist`) directly from the binary, in both development and production; production has no `5173` and does not depend on Vite.

If no configuration has been generated yet, you can set a development secret entry through an environment variable:

```bash
LACTSSL_ENTRY=my-dev-entry make dev
```

## Docker

Using Compose, the complete file is as follows (identical to `docker-compose.yml` in the repository root):

```yaml title="docker-compose.yml"
--8<-- "docker-compose.yml"
```

Start it (pulls `ghcr.io/axmishell/lactssl:latest` by default):

```bash
docker compose pull
docker compose up -d
```

Default mounts:

- `./config/config.yaml`: configuration file
- `./data`: SQLite database

You can also build and run it manually (for local source changes):

```bash
docker build -t lactssl .
docker run -d --name lactssl \
  -p 8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  lactssl
```

Inside the container, `LACTSSL_DATA=/app/data` and `LACTSSL_CONFIG=/app/config/config.yaml` are used by default. The entrypoint script runs as root to fix ownership of the mounted directories, then drops privileges to the `lactssl` user to run the main program; Compose drops all capabilities and keeps only what is needed, such as `CHOWN` and `SETUID`. Behind a reverse proxy, remember to set `LACTSSL_TRUSTED_PROXIES` and `security.public_url`; see [Security](security.md) for details.

### Using the GHCR image

Releases also build `linux/amd64` and `linux/arm64` images and push them to the GitHub Container Registry:

```bash
docker pull ghcr.io/axmishell/lactssl:latest
docker run -d --name lactssl \
  -p 8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  ghcr.io/axmishell/lactssl:latest
```

Stable tags include `latest`, `<version>`, `<major>.<minor>`, and `<major>`, for example `1.0.1`, `1.0`, and `1`; pre-releases (such as `1.0.1-rc.1`) only push the full version tag and do not overwrite `latest`.

## Next steps

- [Configuration](configuration.md)
- [Features](features.md)
