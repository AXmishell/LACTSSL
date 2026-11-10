#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

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
ENTRY=$(awk '/^[[:space:]]*entry:/{gsub(/["'\'']/,"",$2); print $2; exit}' "$CONFIG_PATH")
ENTRY="${ENTRY#/}"
ENTRY="${ENTRY%/}"
LAN_IPS=$(hostname -I 2>/dev/null || true)

echo "============================================================"
echo " LACTSSL dev mode / Vite HMR"
echo "------------------------------------------------------------"
if [ -n "$ENTRY" ]; then
  echo " Entrance  : http://<host>:5173/$ENTRY/"
else
  echo " Entrance  : generated on first start; see the Go log"
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

(cd web && npm run dev) &
VITE_PID=$!

cleanup() {
  kill "$VITE_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

go run ./cmd/server
