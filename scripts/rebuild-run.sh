#!/usr/bin/env bash
# 重新构建并运行开发环境：先构建前端，再编译未嵌入前端的 Go 二进制，
# 最后同时启动 Vite（:5173，带 HMR）与 Go 服务（配置中的 port，默认 :8080）。
# 用法: ./scripts/rebuild-run.sh
# 安全入口优先取 LACTSSL_ENTRY，其次 config.yaml，最后数据库中的持久化入口。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/entry.sh"

CONFIG_PATH="${CONFIG:-config.yaml}"
if [ ! -f "$CONFIG_PATH" ]; then
  echo "config.yaml not found; creating it from config.yaml.example..." >&2
  cp config.yaml.example "$CONFIG_PATH"
  chmod 600 "$CONFIG_PATH"
fi

if [ ! -d web/node_modules ]; then
  echo "installing frontend dependencies..." >&2
  (cd web && npm install)
fi

PORT=$(awk '/^[[:space:]]*port:/{print $2; exit}' "$CONFIG_PATH")
PORT="${PORT:-8080}"
ENTRY=$(resolve_entry "$CONFIG_PATH")

# 结束占用端口的旧进程，便于反复“重新构建运行”。
kill_port() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -ti "tcp:$port" 2>/dev/null | while read -r pid; do
      [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
    done
  elif command -v fuser >/dev/null 2>&1; then
    fuser -k "$port/tcp" 2>/dev/null || true
  fi
}

echo "==> 构建前端 web/dist" >&2
(cd web && npm run build)

VERSION="$(./scripts/version.sh)"
if [ "$VERSION" != "dev" ]; then
  VERSION="${VERSION}-dev"
fi
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_ARCH=$(go env GOOS)/$(go env GOARCH)

echo "==> 编译开发模式二进制 bin/lactssl-dev（不嵌入前端）" >&2
mkdir -p bin
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X 'lactssl/internal/api.Version=${VERSION}' -X 'lactssl/internal/api.BuildTime=${BUILD_TIME}' -X 'lactssl/internal/api.BuildArch=${BUILD_ARCH}'" \
  -o bin/lactssl-dev ./cmd/server

echo "==> 停止占用 :$PORT 与 :5173 的旧进程" >&2
kill_port "$PORT"
kill_port 5173
sleep 1

echo "==> 启动 Vite(:5173) 与 Go(:$PORT)，访问 http://<host>:5173/${ENTRY:-<entry>}/" >&2
(cd web && LACTSSL_ENTRY="$ENTRY" npm run dev) &
VITE_PID=$!

cleanup() {
  kill "$VITE_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

./bin/lactssl-dev
