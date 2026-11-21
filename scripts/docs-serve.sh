#!/usr/bin/env bash
# 构建多语言文档站点并启动静态预览服务器（默认开启局域网访问）。
#
# `zensical serve` 一次只能构建一种配置，而且会清理 site 目录，
# 因此多语言预览需要先分别构建（英文到根目录、其他语言到各自前缀），
# 再用静态服务器托管合并后的 site。
#
# 用法：
#   ./scripts/docs-serve.sh [命令] [选项] [端口]
#
# 命令：
#   （省略）            后台值守运行：构建后常驻并监听变更（推荐）
#   start               后台常驻运行（等同于省略命令）
#   stop                停止后台服务
#   restart             重启后台服务
#
# 选项：
#   -f, --foreground    前台运行，Ctrl+C 停止（默认后台值守）
#   -p, --port PORT     监听端口（默认 8000，或环境变量 DOCS_PORT）
#   -H, --host HOST     监听地址（默认 0.0.0.0，即局域网可访问；
#                       只允许本机访问可设为 127.0.0.1，或环境变量 DOCS_HOST）
#   -l, --lang LANG     构建语言：all|en|zh|zh-hant|ja|ru（默认 all，或 DOCS_LANG）
#       --no-build      跳过构建，直接托管已有的 site 目录
#       --no-clean      构建前不清理旧的站点产物（增量构建，更快）
#   -w, --watch         监听文档与配置变更并自动重新构建（默认开启）
#       --no-watch      关闭文件监听（仅在需要固定产物时使用）
#   -o, --open          启动后尝试用浏览器打开预览地址（仅前台模式）
#   -h, --help          显示本帮助
#
# 示例：
#   ./scripts/docs-serve.sh                 # 后台值守：构建+监听，局域网可访问
#   ./scripts/docs-serve.sh -f              # 前台运行，Ctrl+C 停止
#   ./scripts/docs-serve.sh start           # 后台值守（显式写法）
#   ./scripts/docs-serve.sh restart         # 后台重启并继续监听变更
#   ./scripts/docs-serve.sh stop            # 停止后台服务
#   ./scripts/docs-serve.sh 8080            # 兼容旧用法：指定端口
#   ./scripts/docs-serve.sh -l ja --no-watch  # 只预览日语，关闭自动重建
#   ./scripts/docs-serve.sh --no-build      # 直接查看上次构建结果
#
# 环境变量：
#   DOCS_PORT / DOCS_HOST / DOCS_LANG   对应上面的默认值
#   ZENSICAL                            指定 zensical 可执行文件路径
#
# 依赖（缺失时脚本会提示安装命令）：
#   python3 -m venv .venv && .venv/bin/pip install -r requirements-docs.txt
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
SCRIPT_PATH="$ROOT/scripts/$(basename "${BASH_SOURCE[0]}")"

SITE_DIR="$ROOT/site"
# 注意: 不要放在 .cache/，那是 zensical/mkdocs 的缓存目录，构建时会被清理。
CACHE_DIR="$ROOT/.docs-serve"
STAMP_FILE="$CACHE_DIR/docs-serve.stamp"
PID_FILE="$CACHE_DIR/docs-serve.pid"
LOG_FILE="$CACHE_DIR/docs-serve.log"

PORT="${DOCS_PORT:-8000}"
HOST="${DOCS_HOST:-0.0.0.0}"
LANG_OPT="${DOCS_LANG:-all}"
ACTION="auto"
DO_BUILD=1
DO_CLEAN=1
WATCH=1
WATCH_SET=0
FOREGROUND=0
OPEN=0
PORT_FROM_ARG=0
WATCH_PID=""
SERVER_PID=""

# 站点语言及其构建产物路径（英文为默认站点，构建到根目录）。
SUPPORTED_LANGS=(en zh zh-hant ja ru)

lang_config() {
  case "$1" in
    en) printf 'mkdocs.yml' ;;
    *) printf 'mkdocs.%s.yml' "$1" ;;
  esac
}

lang_path() {
  case "$1" in
    en) printf '' ;;
    *) printf '%s/' "$1" ;;
  esac
}

lang_label() {
  case "$1" in
    en) printf '英文' ;;
    zh) printf '简体中文' ;;
    zh-hant) printf '繁体中文' ;;
    ja) printf '日语' ;;
    ru) printf '俄语' ;;
    *) printf '%s' "$1" ;;
  esac
}

usage() {
  cat <<'EOF'
构建多语言文档站点并启动静态预览服务器（默认开启局域网访问）。

`zensical serve` 一次只能构建一种配置，而且会清理 site 目录，
因此多语言预览需要先分别构建（英文到根目录、其他语言到各自前缀），
再用静态服务器托管合并后的 site。

用法：
  ./scripts/docs-serve.sh [命令] [选项] [端口]

命令：
  （省略）            后台值守运行：构建后常驻并监听变更（推荐）
  start               后台常驻运行（等同于省略命令）
  stop                停止后台服务
  restart             重启后台服务

选项：
  -f, --foreground    前台运行，Ctrl+C 停止（默认后台值守）
  -p, --port PORT     监听端口（默认 8000，或环境变量 DOCS_PORT）
  -H, --host HOST     监听地址（默认 0.0.0.0，即局域网可访问；
                      只允许本机访问可设为 127.0.0.1，或环境变量 DOCS_HOST）
  -l, --lang LANG     构建语言：all|en|zh|zh-hant|ja|ru（默认 all，或 DOCS_LANG）
      --no-build      跳过构建，直接托管已有的 site 目录
      --no-clean      构建前不清理旧的站点产物（增量构建，更快）
  -w, --watch         监听文档与配置变更并自动重新构建（默认开启）
      --no-watch      关闭文件监听（仅在需要固定产物时使用）
  -o, --open          启动后尝试用浏览器打开预览地址（仅前台模式）
  -h, --help          显示本帮助

示例：
  ./scripts/docs-serve.sh                 # 后台值守：构建+监听，局域网可访问
  ./scripts/docs-serve.sh -f              # 前台运行，Ctrl+C 停止
  ./scripts/docs-serve.sh start           # 后台值守（显式写法）
  ./scripts/docs-serve.sh restart         # 后台重启并继续监听变更
  ./scripts/docs-serve.sh stop            # 停止后台服务
  ./scripts/docs-serve.sh 8080            # 兼容旧用法：指定端口
  ./scripts/docs-serve.sh -l ja --no-watch  # 只预览日语，关闭自动重建
  ./scripts/docs-serve.sh --no-build      # 直接查看上次构建结果

环境变量：
  DOCS_PORT / DOCS_HOST / DOCS_LANG   对应上面的默认值
  ZENSICAL                            指定 zensical 可执行文件路径

依赖（缺失时脚本会提示安装命令）：
  python3 -m venv .venv && .venv/bin/pip install -r requirements-docs.txt
EOF
}

die() {
  echo "错误: $*" >&2
  exit 1
}

cleanup() {
  local pid
  for pid in "$WATCH_PID" "$SERVER_PID"; do
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  # 只清理属于本次进程的 PID 文件，避免误删其他实例的记录。
  if [ -f "$PID_FILE" ] && [ "$(cat "$PID_FILE" 2>/dev/null)" = "$$" ]; then
    rm -f "$PID_FILE"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# ---- 后台服务管理 ----------------------------------------------------------

running_pid() {
  local pid
  [ -s "$PID_FILE" ] || return 1
  pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  case "$pid" in
    ''|*[!0-9]*) return 1 ;;
  esac
  kill -0 "$pid" 2>/dev/null || return 1
  printf '%s' "$pid"
}

# 后台服务以 setsid 启动，因此可以按进程组整体结束（含 watch 子进程）。
kill_group() {
  local pid="$1" sig="$2" pgid
  pgid="$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ' || true)"
  if [ "$pgid" = "$pid" ]; then
    kill "-$sig" -- "-$pid" 2>/dev/null && return 0
  fi
  kill "-$sig" "$pid" 2>/dev/null
}

stop_server() {
  local pid waited
  if ! pid="$(running_pid)"; then
    if [ -f "$PID_FILE" ]; then
      echo "==> 清理残留 PID 文件（进程已不存在）"
      rm -f "$PID_FILE"
    fi
    echo "==> 后台服务未运行"
    return 0
  fi

  echo "==> 停止后台服务（PID $pid）"
  kill_group "$pid" TERM || true

  waited=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge 50 ]; then
      echo "==> 进程未在 10 秒内退出，强制结束"
      kill_group "$pid" KILL || true
      break
    fi
    sleep 0.2
    waited=$((waited + 1))
  done

  rm -f "$PID_FILE"
  echo "==> 已停止"
}

# 端口上是否已是我们自己的预览服务（避免把其他进程误判为启动成功）。
port_ready() {
  local probe_host="$HOST"
  [ -s "$PID_FILE" ] || return 1
  case "$probe_host" in
    0.0.0.0|'') probe_host=127.0.0.1 ;;
    ::) probe_host=::1 ;;
  esac
  python3 - "$probe_host" "$PORT" <<'PY' >/dev/null 2>&1
import socket
import sys

host, port = sys.argv[1], int(sys.argv[2])
request = b"HEAD / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
with socket.create_connection((host, port), timeout=0.5) as sock:
    sock.sendall(request)
    data = sock.recv(4096).decode("latin-1", "replace")
# 预览服务器会注入这个响应头，用它区分其他占用端口的进程。
if "Cache-Control: no-store" not in data:
    sys.exit(1)
PY
}

start_server() {
  local pid waited
  if pid="$(running_pid)"; then
    echo "==> 后台服务已在运行（PID $pid），无需重复启动"
    print_endpoints
    echo "    日志: $LOG_FILE"
    if [ "$WATCH" = 1 ]; then
      echo "    注意: 已在运行的实例不会应用本次的端口、语言或监听参数，请先 stop 再 start"
    fi
    return 0
  fi

  if [ -f "$PID_FILE" ]; then
    echo "==> 清理残留 PID 文件（进程已不存在）"
    rm -f "$PID_FILE"
  fi

  mkdir -p "$CACHE_DIR"
  : >"$LOG_FILE"

  local forward_args=(-f -p "$PORT" -H "$HOST" -l "$LANG_OPT")
  [ "$DO_CLEAN" = 0 ] && forward_args+=(--no-clean)
  if [ "$WATCH" = 1 ]; then
    forward_args+=(--watch)
  else
    forward_args+=(--no-watch)
  fi
  [ "$DO_BUILD" = 0 ] && forward_args+=(--no-build)

  echo "==> 启动后台服务（日志: $LOG_FILE）"
  local launcher=()
  if command -v setsid >/dev/null 2>&1; then
    launcher=(setsid)
  fi
  "${launcher[@]+"${launcher[@]}"}" "$SCRIPT_PATH" "${forward_args[@]}" \
    </dev/null >>"$LOG_FILE" 2>&1 &
  pid=$!

  waited=0
  while [ "$waited" -lt 900 ]; do
    if port_ready; then
      echo "==> 后台服务已启动（PID $(cat "$PID_FILE" 2>/dev/null || echo "$pid")）"
      print_endpoints
      echo "    日志: $LOG_FILE"
      echo "    停止: $SCRIPT_PATH stop"
      return 0
    fi
    # setsid 可能 fork，$! 不一定是子进程本身，因此以 PID 文件为准；
    # 启动失败时子进程会通过 EXIT trap 删除 PID 文件。
    if [ "$waited" -ge 10 ] && [ ! -s "$PID_FILE" ] && ! kill -0 "$pid" 2>/dev/null; then
      break
    fi
    sleep 0.2
    waited=$((waited + 1))
  done

  echo "错误: 后台服务启动失败，最近日志：" >&2
  tail -n 20 "$LOG_FILE" >&2 || true
  rm -f "$PID_FILE"
  return 1
}

# ---- 参数解析 --------------------------------------------------------------

while [ "$#" -gt 0 ]; do
  case "$1" in
    start)
      ACTION="start"
      shift
      ;;
    stop)
      ACTION="stop"
      shift
      ;;
    restart)
      ACTION="restart"
      shift
      ;;
    -p|--port)
      [ "$#" -ge 2 ] || die "$1 缺少端口参数"
      PORT="$2"
      PORT_FROM_ARG=1
      shift 2
      ;;
    --port=*)
      PORT="${1#*=}"
      PORT_FROM_ARG=1
      shift
      ;;
    -H|--host)
      [ "$#" -ge 2 ] || die "$1 缺少监听地址参数"
      HOST="$2"
      shift 2
      ;;
    --host=*)
      HOST="${1#*=}"
      shift
      ;;
    -l|--lang)
      [ "$#" -ge 2 ] || die "$1 缺少语言参数"
      LANG_OPT="$2"
      shift 2
      ;;
    --lang=*)
      LANG_OPT="${1#*=}"
      shift
      ;;
    --no-build)
      DO_BUILD=0
      shift
      ;;
    --no-clean)
      DO_CLEAN=0
      shift
      ;;
    -w|--watch)
      WATCH=1
      WATCH_SET=1
      shift
      ;;
    --no-watch)
      WATCH=0
      WATCH_SET=1
      shift
      ;;
    -f|--foreground)
      FOREGROUND=1
      shift
      ;;
    -o|--open)
      OPEN=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    -*)
      usage >&2
      die "未知选项: $1"
      ;;
    *)
      # 兼容旧用法：第一个位置参数视作端口。
      if [ "$PORT_FROM_ARG" = 0 ] && [[ "$1" =~ ^[0-9]+$ ]]; then
        PORT="$1"
        PORT_FROM_ARG=1
        shift
      else
        usage >&2
        die "未知参数: $1"
      fi
      ;;
  esac
done

case "$LANG_OPT" in
  all) LANGS_TO_BUILD=("${SUPPORTED_LANGS[@]}") ;;
  *)
    valid_lang=0
    for l in "${SUPPORTED_LANGS[@]}"; do
      [ "$LANG_OPT" = "$l" ] && valid_lang=1
    done
    [ "$valid_lang" = 1 ] || die "无效语言: $LANG_OPT（可选 all、${SUPPORTED_LANGS[*]}）"
    LANGS_TO_BUILD=("$LANG_OPT")
    ;;
esac

case "$PORT" in
  ''|*[!0-9]*) die "无效端口: $PORT" ;;
esac
if [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
  die "端口超出范围: $PORT（1-65535）"
fi
[ -n "$HOST" ] || die "监听地址不能为空"
# 允许按 URL 习惯写成 [::1] 形式的 IPv6 地址。
case "$HOST" in
  \[*\]) HOST="${HOST#[}"; HOST="${HOST%]}" ;;
esac

# 默认后台值守，-f/--foreground 显式切回前台。
if [ "$FOREGROUND" = 1 ]; then
  case "$ACTION" in
    auto|start) ACTION=run ;;
    restart) ACTION="restart-fg" ;;
  esac
fi
[ "$ACTION" = auto ] && ACTION=start

# 监听默认开启；--no-build 时如果未显式指定 -w，则自动关闭监听。
if [ "$WATCH" = 1 ] && [ "$DO_BUILD" = 0 ]; then
  if [ "$WATCH_SET" = 1 ]; then
    die "--watch 与 --no-build 不能同时使用"
  fi
  WATCH=0
fi

# ---- 地址展示 --------------------------------------------------------------

# 列出本机可用于局域网访问的 IPv4 地址（过滤容器/网桥等虚拟网卡）。
lan_ips() {
  if command -v ip >/dev/null 2>&1; then
    ip -4 -o addr show scope global 2>/dev/null \
      | awk '$2 !~ /^(docker|br-|veth|virbr|cni|flannel|cali|tun|tap)/ { print $4 }' \
      | cut -d/ -f1 || true
  else
    hostname -I 2>/dev/null | tr ' ' '\n' | grep -v '^$' || true
  fi
}

DISPLAY_HOST="$HOST"
case "$HOST" in
  0.0.0.0) DISPLAY_HOST=localhost ;;
  ::) DISPLAY_HOST='[::1]' ;;
  *:*) DISPLAY_HOST="[$HOST]" ;;
esac
BASE_URL="http://${DISPLAY_HOST}:${PORT}"

# 输出预览地址（前台启动与后台启动共用）。
print_endpoints() {
  local ip ipv4 l
  echo
  echo "==> 文档预览地址："
  for l in "${LANGS_TO_BUILD[@]}"; do
    echo "    本机$(lang_label "$l"): ${BASE_URL}/$(lang_path "$l")"
  done

  case "$HOST" in
    0.0.0.0|::|'')
      for ip in $(lan_ips); do
        case "$ip" in
          *:*) ipv4="[$ip]" ;;
          *) ipv4="$ip" ;;
        esac
        for l in "${LANGS_TO_BUILD[@]}"; do
          echo "    局域网$(lang_label "$l"): http://${ipv4}:${PORT}/$(lang_path "$l")"
        done
      done
      ;;
    127.0.0.1|::1)
      echo "    （仅本机可访问；局域网访问请去掉 -H 127.0.0.1 或改用 -H 0.0.0.0）"
      ;;
  esac
}

# ---- 后台命令 --------------------------------------------------------------

run_start() {
  if [ "$OPEN" = 1 ]; then
    echo "提示: 后台模式下忽略 --open，请手动访问下面的地址"
  fi
  start_server
}

case "$ACTION" in
  stop)
    stop_server
    exit 0
    ;;
  start)
    run_start
    exit $?
    ;;
  restart)
    stop_server
    run_start
    exit $?
    ;;
  restart-fg)
    # 先停掉后台实例，随后走前台流程。
    stop_server
    ACTION=run
    ;;
esac

# ---- 依赖检查 --------------------------------------------------------------

command -v python3 >/dev/null 2>&1 || die "缺少 python3，无法启动静态预览服务器"

ZENSICAL_CMD=()
if [ "$DO_BUILD" = 1 ]; then
  if [ -n "${ZENSICAL:-}" ]; then
    if command -v "$ZENSICAL" >/dev/null 2>&1 || [ -x "$ZENSICAL" ]; then
      ZENSICAL_CMD=("$ZENSICAL")
    else
      die "ZENSICAL 指向的文件不可执行: $ZENSICAL"
    fi
  else
    for candidate in zensical .venv/bin/zensical venv/bin/zensical; do
      if command -v "$candidate" >/dev/null 2>&1; then
        ZENSICAL_CMD=("$candidate")
        break
      fi
    done
    if [ "${#ZENSICAL_CMD[@]}" -eq 0 ] && python3 -c 'import zensical' >/dev/null 2>&1; then
      ZENSICAL_CMD=(python3 -m zensical)
    fi
  fi

  if [ "${#ZENSICAL_CMD[@]}" -eq 0 ]; then
    cat >&2 <<'EOF'
错误: 未找到 zensical 命令。

安装文档依赖（推荐使用虚拟环境）：
  python3 -m venv .venv
  .venv/bin/pip install -r requirements-docs.txt

也可以直接指定可执行文件：
  ZENSICAL=/path/to/zensical ./scripts/docs-serve.sh
EOF
    exit 1
  fi
fi

# 记录前台实例的 PID，使 `stop` 也能停止前台启动的服务。
mkdir -p "$CACHE_DIR"
echo "$$" >"$PID_FILE"

# ---- 构建 ------------------------------------------------------------------

build_once() {
  local base_clean=() l cfg first=1
  [ "$DO_CLEAN" = 1 ] && base_clean+=(--clean)

  for l in "${LANGS_TO_BUILD[@]}"; do
    cfg="$(lang_config "$l")"
    echo "--> $(lang_label "$l") ($cfg)"
    if [ "$first" = 1 ]; then
      # 只在第一次构建时清理，避免后续语言删除前面语言的产物。
      "${ZENSICAL_CMD[@]}" build -f "$cfg" --strict ${base_clean[@]+"${base_clean[@]}"}
      first=0
    else
      "${ZENSICAL_CMD[@]}" build -f "$cfg" --strict
    fi
  done
}

check_output() {
  local l
  for l in "${LANGS_TO_BUILD[@]}"; do
    [ -f "$SITE_DIR/$(lang_path "$l")index.html" ] \
      || die "未生成$(lang_label "$l")站点: $SITE_DIR/$(lang_path "$l")index.html"
  done
}

if [ "$DO_BUILD" = 1 ]; then
  echo "==> 构建文档（语言: ${LANG_OPT}，严格模式）"
  build_once
  check_output
  echo "==> 构建完成"
else
  check_output
  echo "==> 跳过构建，使用已有站点: $SITE_DIR"
fi

# ---- 监听变更 --------------------------------------------------------------

if [ "$WATCH" = 1 ]; then
  mkdir -p "$(dirname "$STAMP_FILE")"
  touch "$STAMP_FILE"

  watch_loop() {
    while :; do
      sleep 1
      if find docs mkdocs.yml mkdocs.*.yml -newer "$STAMP_FILE" -print -quit 2>/dev/null | grep -q .; then
        # 先更新时间戳，避免构建过程中反复触发。
        touch "$STAMP_FILE"
        echo
        echo "==> 检测到文档变更，重新构建 $(date '+%H:%M:%S')"
        if build_once; then
          echo "==> 重新构建完成"
        else
          echo "警告: 构建失败，继续监听（修复后会自动重试）" >&2
        fi
        touch "$STAMP_FILE"
      fi
    done
  }

  watch_loop &
  WATCH_PID=$!
  echo "==> 已开启文件监听（docs/、mkdocs*.yml）"
fi

# ---- 端口检查 --------------------------------------------------------------

if ! python3 - "$HOST" "$PORT" <<'PY'
import socket
import sys

host, port = sys.argv[1], int(sys.argv[2])
sock = socket.socket(socket.AF_INET6 if ":" in host else socket.AF_INET, socket.SOCK_STREAM)
try:
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind((host, port))
except OSError as exc:
    print(f"无法监听 {host}:{port}: {exc}", file=sys.stderr)
    sys.exit(1)
finally:
    sock.close()
PY
then
  cat >&2 <<EOF
提示: 端口 ${PORT} 可能已被其他进程占用，可查看占用情况：
  ss -ltnp | grep ':${PORT}'
或换个端口启动，例如：
  ${SCRIPT_PATH} -p $((PORT + 1))
EOF
  die "端口不可用: ${HOST}:${PORT}"
fi

# ---- 启动预览服务器 --------------------------------------------------------

print_endpoints
case "$ACTION" in
  run) echo "    按 Ctrl+C 停止" ;;
esac
echo

if [ "$OPEN" = 1 ]; then
  OPEN_URL="$BASE_URL/$(lang_path "$LANG_OPT")"
  [ "$LANG_OPT" = "all" ] && OPEN_URL="$BASE_URL/"
  if command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$OPEN_URL" >/dev/null 2>&1 || true
  elif command -v open >/dev/null 2>&1; then
    open "$OPEN_URL" >/dev/null 2>&1 || true
  else
    echo "提示: 未找到浏览器打开命令，请手动访问 $OPEN_URL"
  fi
fi

# 使用内联处理器关闭缓存，避免重新构建后浏览器仍显示旧的 CSS/JS。
# 服务器放到后台运行，便于退出信号到来时统一回收。
python3 - "$HOST" "$PORT" "$SITE_DIR" <<'PY' &
import functools
import http.server
import socket
import sys

host, port, directory = sys.argv[1], int(sys.argv[2]), sys.argv[3]


class Server(http.server.ThreadingHTTPServer):
    address_family = socket.AF_INET6 if ":" in host else socket.AF_INET


class Handler(http.server.SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header("Cache-Control", "no-store, no-cache, must-revalidate")
        self.send_header("Pragma", "no-cache")
        self.send_header("Expires", "0")
        super().end_headers()

    def log_message(self, fmt, *args):
        # 只输出错误请求，避免每次刷新刷屏。
        if args and str(args[1]).startswith(("4", "5")):
            super().log_message(fmt, *args)


handler = functools.partial(Handler, directory=directory)
with Server((host, port), handler) as httpd:
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass
PY

SERVER_PID=$!
wait "$SERVER_PID"
