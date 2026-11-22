#!/usr/bin/env bash
# LACTSSL 本地开发脚本：后台运行 Go 后端与 Vite（热更新），并提供停止/重启/状态/日志。
#
# 用法：
#   ./scripts/dev.sh                  # 显示常用命令
#   ./scripts/dev.sh start [--build]  # 启动（必要时先编译）
#   ./scripts/dev.sh restart [--build]
#   ./scripts/dev.sh stop
#   ./scripts/dev.sh status
#   ./scripts/dev.sh logs [panel|web|all]
#
# 选项：
#   --no-web    只启动后端，不启动 Vite
#   --build     启动/重启前执行 make server-dev 重新编译
#   -h, --help  显示本帮助
#
# 环境变量：
#   WEB_PORT        Vite 端口（默认 5173）
#   DEV_HOST        Vite 监听地址（默认 0.0.0.0，局域网可访问）
#
# 后端端口与门禁入口统一从 config.yaml 读取。
# PID 文件与日志位于 data/run/。
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
source "$ROOT_DIR/scripts/entry.sh"

CONFIG="$ROOT_DIR/config.yaml"
PANEL_PORT=""
WEB_PORT_EXPLICIT=0
[ -n "${WEB_PORT:-}" ] && WEB_PORT_EXPLICIT=1
WEB_PORT="${WEB_PORT:-5173}"
DEV_HOST="${DEV_HOST:-0.0.0.0}"

APP_BIN="$ROOT_DIR/bin/lactssl-dev"
RUN_DIR="$ROOT_DIR/data/run"
PANEL_PID_FILE="$RUN_DIR/panel.pid"
WEB_PID_FILE="$RUN_DIR/web.pid"
PANEL_LOG="$RUN_DIR/panel.log"
WEB_LOG="$RUN_DIR/web.log"

COMMAND=""
LOG_TARGET=""
WITH_WEB=1
BUILD=0

if [ -t 1 ]; then
  C_RESET=$'\033[0m'
  C_TITLE=$'\033[1;35m'
  C_SECTION=$'\033[1;36m'
  C_CMD=$'\033[1;32m'
  C_OPT=$'\033[1;33m'
  C_ENV=$'\033[1;34m'
  C_LABEL=$'\033[1;37m'
  C_DESC=$'\033[2;37m'
  C_URL=$'\033[4;36m'
  C_INFO=$'\033[36m'; C_OK=$'\033[32m'; C_WARN=$'\033[33m'; C_ERR=$'\033[31m'
else
  C_RESET=""; C_TITLE=""; C_SECTION=""; C_CMD=""; C_OPT=""; C_ENV=""
  C_LABEL=""; C_DESC=""; C_URL=""
  C_INFO=""; C_OK=""; C_WARN=""; C_ERR=""
fi

help_command() { printf '   %s%-34s%s  %s# %s%s\n' "$C_CMD" "$1" "$C_RESET" "$C_DESC" "$2" "$C_RESET"; }
help_option() { printf '   %s%-12s%s  %s%s%s\n' "$C_OPT" "$1" "$C_RESET" "$C_DESC" "$2" "$C_RESET"; }
help_env() { printf '   %s%-14s%s  %s%s%s\n' "$C_ENV" "$1" "$C_RESET" "$C_DESC" "$2" "$C_RESET"; }

usage() {
  printf '\n %sLACTSSL 本地开发脚本%s\n\n' "$C_TITLE" "$C_RESET"
  printf ' %s常用命令%s\n' "$C_SECTION" "$C_RESET"
  help_command "./scripts/dev.sh start" "启动（后端 + Vite 热更新，后台运行）"
  help_command "./scripts/dev.sh restart" "重启前后端"
  help_command "./scripts/dev.sh restart --build" "重新编译 bin/lactssl-dev 后重启"
  help_command "./scripts/dev.sh stop" "停止"
  help_command "./scripts/dev.sh status" "查看 PID / 端口 / 健康状态"
  help_command "./scripts/dev.sh logs" "跟踪两边日志"
  help_command "./scripts/dev.sh logs panel" "只看后端"
  printf '\n %s选项%s\n' "$C_SECTION" "$C_RESET"
  help_option "--no-web" "只启动后端，不启动 Vite"
  help_option "--build" "启动/重启前执行 make server-dev 重新编译"
  help_option "-h, --help" "显示本帮助"
  printf '\n %s环境变量%s\n' "$C_SECTION" "$C_RESET"
  help_env "WEB_PORT" "Vite 端口（默认 5173）"
  help_env "DEV_HOST" "Vite 监听地址（默认 0.0.0.0，局域网可访问）"
  printf '\n %sPID 文件与日志：%sdata/run/%s\n\n' "$C_DESC" "$C_URL" "$C_RESET"
}

while [ $# -gt 0 ]; do
  case "$1" in
    start|stop|restart|status|logs) COMMAND="$1" ;;
    panel|web|all)
      [ "$COMMAND" = "logs" ] || { echo "dev: unexpected argument: $1" >&2; usage >&2; exit 2; }
      LOG_TARGET="$1"
      ;;
    --no-web) WITH_WEB=0 ;;
    --build) BUILD=1 ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "dev: unknown option: $1" >&2; usage >&2; exit 2 ;;
    *) echo "dev: unknown command: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if [ -z "$COMMAND" ]; then
  usage
  exit 0
fi

log() { printf '%s==> %s%s\n' "$C_INFO" "$*" "$C_RESET"; }
ok() { printf '%s  ✓ %s%s\n' "$C_OK" "$*" "$C_RESET"; }
warn() { printf '%s  ! %s%s\n' "$C_WARN" "$*" "$C_RESET"; }
die() { printf '%s  ✗ %s%s\n' "$C_ERR" "$*" "$C_RESET" >&2; exit 1; }

detect_lan_ip() {
  if command -v ip >/dev/null 2>&1; then
    ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -n1
    return 0
  fi
  hostname -I 2>/dev/null | awk '{print $1}'
}

LAN_IP="${LAN_IP:-$(detect_lan_ip)}"
[ -n "$LAN_IP" ] || LAN_IP="127.0.0.1"

mkdir -p "$RUN_DIR"

# config_value 读取简单 YAML 键的首个值（去掉引号）。
config_value() {
  local key="$1"
  [ -f "$CONFIG" ] || return 0
  awk -v key="$key" '$1 == key":" {gsub(/["'\'']/,"",$2); print $2; exit}' "$CONFIG"
}

# config_port 优先读取 server.addr 中的端口，其次读取 server.port。
config_port() {
  local addr
  addr="$(config_value addr)"
  if [ -n "$addr" ]; then
    case "$addr" in
      *:*)
        local p="${addr##*:}"
        case "$p" in
          ''|*[!0-9]*) ;;
          *) printf '%s\n' "$p"; return 0 ;;
        esac
        ;;
    esac
  fi
  config_value port
}

resolve_ports() {
  # 优先沿用上次启动记录的端口，避免自定义端口启动后用默认端口 status/stop。
  if [ -z "$PANEL_PORT" ] && running_ours "$PANEL_PID_FILE" "panel" && [ -s "$RUN_DIR/panel.port" ]; then
    PANEL_PORT="$(cat "$RUN_DIR/panel.port")"
  fi
  if [ -z "$PANEL_PORT" ]; then
    PANEL_PORT="$(config_port)"
  fi
  PANEL_PORT="${PANEL_PORT:-8080}"
  if [ "$WEB_PORT_EXPLICIT" = "0" ] && running_ours "$WEB_PID_FILE" "web" && [ -s "$RUN_DIR/web.port" ]; then
    WEB_PORT="$(cat "$RUN_DIR/web.port")"
  fi
}

ENTRY=""
[ -f "$CONFIG" ] && ENTRY="$(resolve_entry "$CONFIG" || true)"

running() {
  local file=$1 pid
  [ -s "$file" ] || return 1
  pid="$(cat "$file" 2>/dev/null || true)"
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

running_ours() {
  local file=$1 kind=$2 pid
  running "$file" || return 1
  pid="$(cat "$file")"
  cmd_matches "$pid" "$kind"
}

listener_pid() {
  local port=$1
  if command -v ss >/dev/null 2>&1; then
    ss -ltnp "sport = :$port" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n1
    return 0
  fi
  if command -v lsof >/dev/null 2>&1; then
    lsof -ti "tcp:$port" 2>/dev/null | head -n1
  fi
}

cmd_matches() {
  local pid=$1 kind=$2 cmdline=""
  if [ -r "/proc/$pid/cmdline" ]; then
    cmdline="$(tr '\0' ' ' <"/proc/$pid/cmdline")"
  else
    cmdline="$(ps -o command= -p "$pid" 2>/dev/null || true)"
  fi
  [ -n "$cmdline" ] || return 1
  case "$kind" in
    panel) printf '%s' "$cmdline" | grep -qF -- "$APP_BIN" ;;
    web) printf '%s' "$cmdline" | grep -qF -- "$ROOT_DIR/web/node_modules" ;;
    *) return 1 ;;
  esac
}

port_owner() {
  local port=$1 kind=$2 pid
  pid="$(listener_pid "$port")"
  [ -n "$pid" ] || return 1
  cmd_matches "$pid" "$kind" || return 1
  printf '%s\n' "$pid"
}

wait_http() {
  local url=$1 tries=${2:-60} code
  for _ in $(seq 1 "$tries"); do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$url" 2>/dev/null || true)"
    if [ -n "$code" ] && [ "$code" != "000" ]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

http_code() {
  curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$1" 2>/dev/null || true
}

panel_health_url() {
  if [ -n "$ENTRY" ]; then
    printf 'http://127.0.0.1:%s/%s/api/setup/status' "$PANEL_PORT" "$ENTRY"
  else
    printf 'http://127.0.0.1:%s/' "$PANEL_PORT"
  fi
}

web_health_url() {
  if [ -n "$ENTRY" ]; then
    printf 'http://127.0.0.1:%s/%s/' "$WEB_PORT" "$ENTRY"
  else
    printf 'http://127.0.0.1:%s/' "$WEB_PORT"
  fi
}

ensure_config() {
  [ -f "$CONFIG" ] && return 0
  [ -f config.yaml.example ] || die "config.yaml.example not found"
  log "creating $CONFIG from config.yaml.example"
  mkdir -p "$(dirname "$CONFIG")"
  cp config.yaml.example "$CONFIG"
  chmod 600 "$CONFIG"
  ok "created $CONFIG"
  resolve_ports
  ENTRY="$(resolve_entry "$CONFIG" || true)"
}

build_panel() {
  command -v go >/dev/null 2>&1 || die "go is required to build the panel"
  log "building bin/lactssl-dev"
  make server-dev
  ok "built bin/lactssl-dev"
}

ensure_web_deps() {
  command -v npm >/dev/null 2>&1 || die "npm is required for the web dev server (use --no-web)"
  [ -x web/node_modules/.bin/vite ] && return 0
  log "installing web dependencies"
  if [ -f web/package-lock.json ]; then
    ( cd web && npm ci --no-audit --no-fund )
  else
    ( cd web && npm install --no-audit --no-fund )
  fi
  ok "web dependencies installed"
}

start_panel() {
  if running_ours "$PANEL_PID_FILE" "panel"; then
    ok "panel already running (pid $(cat "$PANEL_PID_FILE"))"
    return 0
  fi
  local adopted
  if adopted="$(port_owner "$PANEL_PORT" "panel" || true)" && [ -n "$adopted" ]; then
    printf '%s\n' "$adopted" >"$PANEL_PID_FILE"
    printf '%s\n' "$PANEL_PORT" >"$RUN_DIR/panel.port"
    ok "panel already running (pid $adopted, adopted from port $PANEL_PORT)"
    return 0
  fi
  [ -x "$APP_BIN" ] || die "bin/lactssl-dev not found; run with --build first"
  rm -f "$PANEL_PID_FILE"
  printf '\n===== %s starting panel =====\n' "$(date '+%F %T')" >>"$PANEL_LOG"
  (
    cd "$ROOT_DIR"
    exec env LACTSSL_DEV_ORIGIN=1 "$APP_BIN" -config "$CONFIG"
  ) >>"$PANEL_LOG" 2>&1 &
  printf '%s\n' "$!" >"$PANEL_PID_FILE"
  if wait_http "$(panel_health_url)"; then
    printf '%s\n' "$PANEL_PORT" >"$RUN_DIR/panel.port"
    ok "panel listening on :$PANEL_PORT (pid $(cat "$PANEL_PID_FILE"))"
  else
    warn "panel did not become healthy; last log lines:"
    tail -n 20 "$PANEL_LOG" >&2 || true
    return 1
  fi
}

start_web() {
  [ "$WITH_WEB" = "1" ] || return 0
  if running_ours "$WEB_PID_FILE" "web"; then
    ok "web dev server already running (pid $(cat "$WEB_PID_FILE"))"
    return 0
  fi
  local adopted
  if adopted="$(port_owner "$WEB_PORT" "web" || true)" && [ -n "$adopted" ]; then
    printf '%s\n' "$adopted" >"$WEB_PID_FILE"
    printf '%s\n' "$WEB_PORT" >"$RUN_DIR/web.port"
    ok "web dev server already running (pid $adopted, adopted from port $WEB_PORT)"
    return 0
  fi
  rm -f "$WEB_PID_FILE"
  printf '\n===== %s starting vite =====\n' "$(date '+%F %T')" >>"$WEB_LOG"
  (
    cd "$ROOT_DIR/web"
    exec "$ROOT_DIR/web/node_modules/.bin/vite" --host "$DEV_HOST" --port "$WEB_PORT" --strictPort
  ) >>"$WEB_LOG" 2>&1 &
  printf '%s\n' "$!" >"$WEB_PID_FILE"
  if wait_http "$(web_health_url)"; then
    printf '%s\n' "$WEB_PORT" >"$RUN_DIR/web.port"
    ok "web dev server listening on $DEV_HOST:$WEB_PORT (pid $(cat "$WEB_PID_FILE"))"
  else
    warn "web dev server did not start; last log lines:"
    tail -n 20 "$WEB_LOG" >&2 || true
    return 1
  fi
}

stop_one() {
  local name=$1 pidfile=$2 port=$3 kind=$4 pid=""
  if [ -s "$pidfile" ]; then
    pid="$(cat "$pidfile")"
  fi
  if [ -n "$pid" ] && ! cmd_matches "$pid" "$kind"; then
    pid=""
  fi
  if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
    pid="$(port_owner "$port" "$kind" || true)"
  fi
  if [ -z "$pid" ]; then
    rm -f "$pidfile" "${pidfile%.pid}.port"
    ok "$name not running"
    return 0
  fi
  log "stopping $name (pid $pid)"
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 50); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.2
  done
  if kill -0 "$pid" 2>/dev/null; then
    warn "$name did not exit; sending SIGKILL"
    kill -9 "$pid" 2>/dev/null || true
  fi
  rm -f "$pidfile" "${pidfile%.pid}.port"
  ok "$name stopped"
}

cmd_stop() {
  stop_one "web dev server" "$WEB_PID_FILE" "$WEB_PORT" "web"
  stop_one "panel" "$PANEL_PID_FILE" "$PANEL_PORT" "panel"
}

print_urls() {
  local panel_url="http://$LAN_IP:$PANEL_PORT/login"
  if [ -n "$ENTRY" ]; then
    panel_url="http://$LAN_IP:$PANEL_PORT/$ENTRY/"
  fi
  printf '  %sPanel:%s  %s%s%s\n' "$C_LABEL" "$C_RESET" "$C_URL" "$panel_url" "$C_RESET"
  if [ "$WITH_WEB" = "1" ]; then
    if [ -n "$ENTRY" ]; then
      printf '  %sWeb:%s    %shttp://%s:%s/%s/  %s(hot reload)%s\n' \
        "$C_LABEL" "$C_RESET" "$C_URL" "$LAN_IP" "$WEB_PORT" "$ENTRY" "$C_DESC" "$C_RESET"
    else
      printf '  %sWeb:%s    %shttp://%s:%s/  %s(hot reload, 未找到门禁入口)%s\n' \
        "$C_LABEL" "$C_RESET" "$C_URL" "$LAN_IP" "$WEB_PORT" "$C_DESC" "$C_RESET"
    fi
  fi
  printf '  %sLogs:%s   %s%s%s\n' "$C_LABEL" "$C_RESET" "$C_DESC" "$RUN_DIR" "$C_RESET"
}

cmd_start() {
  command -v curl >/dev/null 2>&1 || die "curl is required"
  ensure_config
  if [ "$BUILD" = "1" ]; then
    if running "$PANEL_PID_FILE" || [ -n "$(port_owner "$PANEL_PORT" "panel" || true)" ]; then
      stop_one "panel" "$PANEL_PID_FILE" "$PANEL_PORT" "panel"
    fi
  fi
  if [ "$BUILD" = "1" ] || [ ! -x "$APP_BIN" ]; then
    build_panel
  fi
  [ "$WITH_WEB" = "1" ] && ensure_web_deps
  start_panel
  start_web
  echo
  print_urls
}

cmd_status() {
  local panel_pid="" web_pid="" code
  if running_ours "$PANEL_PID_FILE" "panel"; then
    panel_pid="$(cat "$PANEL_PID_FILE")"
  else
    panel_pid="$(port_owner "$PANEL_PORT" "panel" || true)"
  fi
  if running_ours "$WEB_PID_FILE" "web"; then
    web_pid="$(cat "$WEB_PID_FILE")"
  else
    web_pid="$(port_owner "$WEB_PORT" "web" || true)"
  fi

  log "panel"
  if [ -n "$panel_pid" ]; then
    code="$(http_code "$(panel_health_url)")"
    ok "running (pid $panel_pid), :$PANEL_PORT http=$code"
    if [ -n "$ENTRY" ]; then
      printf '       %shttp://%s:%s/%s/%s\n' "$C_URL" "$LAN_IP" "$PANEL_PORT" "$ENTRY" "$C_RESET"
    else
      printf '       %shttp://%s:%s/login%s\n' "$C_URL" "$LAN_IP" "$PANEL_PORT" "$C_RESET"
    fi
  else
    warn "not running"
  fi

  log "web dev server"
  if [ -n "$web_pid" ]; then
    code="$(http_code "$(web_health_url)")"
    ok "running (pid $web_pid), :$WEB_PORT http=$code"
    if [ -n "$ENTRY" ]; then
      printf '       %shttp://%s:%s/%s/%s  %s(hot reload)%s\n' \
        "$C_URL" "$LAN_IP" "$WEB_PORT" "$ENTRY" "$C_RESET" "$C_DESC" "$C_RESET"
    fi
  else
    warn "not running"
  fi

  printf '\n%sLogs:%s %s%s%s\n' "$C_LABEL" "$C_RESET" "$C_DESC" "$RUN_DIR" "$C_RESET"
}

cmd_logs() {
  local which=${1:-all} files=()
  case "$which" in
    panel) [ -f "$PANEL_LOG" ] && files+=("$PANEL_LOG") ;;
    web) [ -f "$WEB_LOG" ] && files+=("$WEB_LOG") ;;
    all) [ -f "$PANEL_LOG" ] && files+=("$PANEL_LOG"); [ -f "$WEB_LOG" ] && files+=("$WEB_LOG") ;;
    *) die "logs: expected panel, web or all" ;;
  esac
  [ ${#files[@]} -gt 0 ] || die "no log files yet; start the stack first"
  exec tail -n 80 -f "${files[@]}"
}

resolve_ports

case "$COMMAND" in
  start) cmd_start ;;
  stop) cmd_stop ;;
  restart) cmd_stop; echo; cmd_start ;;
  status) cmd_status ;;
  logs) cmd_logs "${LOG_TARGET:-all}" ;;
  *) die "unknown command: $COMMAND" ;;
esac
