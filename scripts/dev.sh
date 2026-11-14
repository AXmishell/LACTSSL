#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/entry.sh"

CONFIG_PATH="${CONFIG:-config.yaml}"
if [ ! -f "$CONFIG_PATH" ]; then
  echo "config.yaml not found; creating it from config.yaml.example..." >&2
  cp config.yaml.example "$CONFIG_PATH"
  chmod 600 "$CONFIG_PATH"
fi

if [ ! -d web/node_modules ]; then
  echo "installing frontend dependencies..."
  (cd web && npm install)
fi

PORT=$(awk '/^[[:space:]]*port:/{print $2; exit}' "$CONFIG_PATH")
PORT="${PORT:-8080}"
ENTRY=$(resolve_entry "$CONFIG_PATH")
LAN_IPS=$(hostname -I 2>/dev/null || true)

echo "============================================================"
echo " LACTSSL dev mode / Vite HMR"
echo "------------------------------------------------------------"
if [ -n "$ENTRY" ]; then
  echo " Entrance  : http://<host>:5173/$ENTRY/"
else
  echo " Entrance  : 未找到安全入口（config.yaml 与数据库均为空）"
  echo "             首次启动后入口只保存在数据库，可设置 LACTSSL_ENTRY 覆盖"
fi
echo " Localhost : http://127.0.0.1:5173"
for ip in $LAN_IPS; do
  case "$ip" in
    127.*) continue ;;
  esac
  if [ -n "$ENTRY" ]; then
    echo " LAN       : http://$ip:5173/$ENTRY/"
  else
    echo " LAN       : http://$ip:5173"
  fi
done
echo "------------------------------------------------------------"
echo " Vite runs on :5173 and proxies /<entry>/api to Go on :$PORT."
echo " Open the Vite URL above during development; production serves from :$PORT."
echo "============================================================"

(cd web && LACTSSL_ENTRY="$ENTRY" npm run dev) &
VITE_PID=$!

cleanup() {
  kill "$VITE_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

CGO_ENABLED=0 go run ./cmd/server
