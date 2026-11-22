#!/usr/bin/env bash
# LACTSSL 一键部署脚本
#
# 功能：
#   1. 从 GitHub Release 下载对应架构的二进制包，并校验 SHA-256；
#   2. 安装为 systemd 服务，自动创建运行用户、数据目录和配置文件；
#   3. 默认只监听 127.0.0.1 的 HTTP 端口，由 Nginx 反向代理对外提供访问；
#   4. 重复执行可升级二进制，保留已有配置与数据。
#
# 用法示例：
#   sudo ./scripts/install.sh
#   sudo ./scripts/install.sh --port 9000 --domain ssl.example.com
#   sudo ./scripts/install.sh --version v1.2.3
#   sudo ./scripts/install.sh --domain ssl.example.com \
#     --tls-cert /etc/nginx/certs/ssl.example.com.pem \
#     --tls-key  /etc/nginx/certs/ssl.example.com.key
#   sudo ./scripts/install.sh --no-nginx --port 8080
set -Eeuo pipefail

APP="lactssl"
DEFAULT_REPO="AXmishell/LACTSSL"
GITHUB="https://github.com"

PREFIX="/opt/lactssl"
PORT="8080"
PORT_EXPLICIT=0
DOMAIN="_"
DOMAIN_EXPLICIT=0
VERSION="latest"
REPO="$DEFAULT_REPO"
ENABLE_NGINX=1
INSTALL_DEPS=1
SKIP_CHECKSUM=0
ALLOW_UNSIGNED=0
RELEASE_PUBLIC_KEY="${LACTSSL_RELEASE_PUBLIC_KEY:-}"
TLS_CERT=""
TLS_KEY=""
TLS_EXPLICIT=0
NO_TLS=0
SERVICE_NAME="lactssl"
NGINX_ROOT="/etc/nginx"
NGINX_CONF="$NGINX_ROOT/conf.d/lactssl.conf"
EXTRA_RW_PATHS=()
RW_EXPLICIT=0

ORIG_ARGS=("$@")
SCRIPT_PATH="$0"
TMP_DIR=""
BINARY_BACKUP=""
NGINX_BACKUP=""
NGINX_CONF_CREATED=0

if [ -t 1 ]; then
  C_RESET=$'\033[0m'
  C_BOLD=$'\033[1m'
  C_BLUE=$'\033[1;34m'
  C_GREEN=$'\033[1;32m'
  C_YELLOW=$'\033[1;33m'
  C_RED=$'\033[1;31m'
else
  C_RESET='' C_BOLD='' C_BLUE='' C_GREEN='' C_YELLOW='' C_RED=''
fi

log()  { printf '%s==>%s %s\n' "$C_BLUE" "$C_RESET" "$*"; }
ok()   { printf '%s✓%s %s\n' "$C_GREEN" "$C_RESET" "$*"; }
warn() { printf '%s!%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2; }

die() {
  printf '%s✗%s %s\n' "$C_RED" "$C_RESET" "$*" >&2
  exit 1
}

cleanup() {
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
LACTSSL 一键部署脚本

用法:
  sudo ./scripts/install.sh [选项]

选项:
  --port PORT           二进制监听端口（默认 8080，必须 >= 1024）
  --domain DOMAIN       Nginx server_name / 对外域名（默认 _）
  --version TAG         发布版本，例如 v1.2.3；默认 latest
  --repo OWNER/REPO     GitHub 仓库（默认 AXmishell/LACTSSL）
  --prefix DIR          安装目录（默认 /opt/lactssl）
  --no-nginx            不安装、不配置 Nginx，仅部署 systemd 服务
  --no-install-deps     缺少 Nginx 时不自动安装，直接报错
  --tls-cert FILE       Nginx TLS 证书路径（与 --tls-key 同时使用）
  --tls-key FILE        Nginx TLS 私钥路径（与 --tls-cert 同时使用）
  --no-tls              关闭已有 Nginx TLS 配置（默认本来就不启用 TLS）
  --read-write-path DIR 额外允许服务写入的目录（可重复，用于本机证书部署目标）
  --skip-checksum       跳过 SHA-256 校验（不推荐）
  --release-public-key KEY
                       发布签名公钥（base64 Ed25519；也可用 LACTSSL_RELEASE_PUBLIC_KEY）
  --allow-unsigned      未提供公钥时仍允许安装（不推荐，仅限受控环境）
  -h, --help            显示帮助

说明:
  - 默认不启用 TLS，Nginx 监听 80 端口并转发到 127.0.0.1:PORT。
  - 指定 --tls-cert/--tls-key 后，Nginx 监听 443 并把 80 重定向到 HTTPS。
  - 重复运行会升级二进制；已有 config.yaml 和 data/ 默认保留，
    指定 --port 时只更新 server.addr/host/port 等监听字段。
  - 需要 root 权限、systemd、curl 或 wget。
  - 可通过 http_proxy / https_proxy 环境变量让下载走代理。
  - 可选设置 GITHUB_TOKEN 提高 latest 版本查询的 API 限额。
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --port)
      [ "$#" -ge 2 ] || die "--port 需要参数"
      PORT="$2"
      PORT_EXPLICIT=1
      shift 2
      ;;
    --domain)
      [ "$#" -ge 2 ] || die "--domain 需要参数"
      DOMAIN="$2"
      DOMAIN_EXPLICIT=1
      shift 2
      ;;
    --version)
      [ "$#" -ge 2 ] || die "--version 需要参数"
      VERSION="$2"
      shift 2
      ;;
    --repo)
      [ "$#" -ge 2 ] || die "--repo 需要参数"
      REPO="$2"
      shift 2
      ;;
    --prefix)
      [ "$#" -ge 2 ] || die "--prefix 需要参数"
      PREFIX="$2"
      shift 2
      ;;
    --no-nginx)
      ENABLE_NGINX=0
      shift
      ;;
    --no-install-deps)
      INSTALL_DEPS=0
      shift
      ;;
    --tls-cert)
      [ "$#" -ge 2 ] || die "--tls-cert 需要参数"
      TLS_CERT="$2"
      TLS_EXPLICIT=1
      shift 2
      ;;
    --tls-key)
      [ "$#" -ge 2 ] || die "--tls-key 需要参数"
      TLS_KEY="$2"
      TLS_EXPLICIT=1
      shift 2
      ;;
    --no-tls)
      NO_TLS=1
      TLS_EXPLICIT=1
      shift
      ;;
    --read-write-path)
      [ "$#" -ge 2 ] || die "--read-write-path 需要参数"
      EXTRA_RW_PATHS+=("$2")
      RW_EXPLICIT=1
      shift 2
      ;;
    --skip-checksum)
      SKIP_CHECKSUM=1
      shift
      ;;
    --release-public-key)
      [ "$#" -ge 2 ] || die "--release-public-key 需要参数"
      RELEASE_PUBLIC_KEY="$2"
      shift 2
      ;;
    --allow-unsigned)
      ALLOW_UNSIGNED=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "未知参数: $1（使用 --help 查看帮助）"
      ;;
  esac
done

if [ "${EUID}" -ne 0 ]; then
  if [ -f "$SCRIPT_PATH" ] && command -v sudo >/dev/null 2>&1; then
    exec sudo -E bash "$SCRIPT_PATH" "${ORIG_ARGS[@]}"
  fi
  die "请使用 root 权限运行本脚本"
fi

validate_inputs() {
  case "$PORT" in
    ''|*[!0-9]*) die "--port 必须是数字: $PORT" ;;
  esac
  [ "${#PORT}" -le 5 ] || die "--port 必须在 1024-65535 之间: $PORT"
  PORT=$((10#$PORT))
  if [ "$PORT" -lt 1024 ] || [ "$PORT" -gt 65535 ]; then
    die "--port 必须在 1024-65535 之间（二进制以非特权用户运行，80/443 请交给 Nginx）"
  fi

  case "$PREFIX" in
    /) die "--prefix 不能是 /" ;;
    /*) ;;
    *) die "--prefix 必须是绝对路径: $PREFIX" ;;
  esac
  PREFIX="$(realpath -m "$PREFIX")"
  [ "$PREFIX" != "/" ] || die "--prefix 不能是 /"
  PREFIX="${PREFIX%/}"
  [ -n "$PREFIX" ] || die "--prefix 不能为空"
  case "$PREFIX" in
    /bin|/boot|/dev|/etc|/home|/lib|/lib64|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/var)
      die "--prefix 不能直接使用系统目录: $PREFIX"
      ;;
    /tmp|/tmp/*|/var/tmp|/var/tmp/*)
      die "--prefix 不能位于 /tmp 或 /var/tmp（systemd PrivateTmp 会导致路径不可见），请使用 /opt/lactssl 等持久目录"
      ;;
    /home|/home/*|/root|/root/*|/run/user|/run/user/*)
      die "--prefix 不能位于受 ProtectHome 保护的目录，请使用 /opt/lactssl 等目录"
      ;;
  esac

  if ! printf '%s' "$REPO" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'; then
    die "--repo 格式应为 owner/repo: $REPO"
  fi

  if [ "$DOMAIN" != "_" ] && ! printf '%s' "$DOMAIN" | grep -Eq '^[A-Za-z0-9*._-]+$'; then
    die "--domain 不合法: $DOMAIN"
  fi

  if [ "$NO_TLS" -eq 1 ] && { [ -n "$TLS_CERT" ] || [ -n "$TLS_KEY" ]; }; then
    die "--no-tls 不能与 --tls-cert/--tls-key 同时使用"
  fi
  if [ -n "$TLS_CERT" ] || [ -n "$TLS_KEY" ]; then
    [ -n "$TLS_CERT" ] && [ -n "$TLS_KEY" ] || die "--tls-cert 与 --tls-key 必须同时指定"
    [ "$ENABLE_NGINX" -eq 1 ] || die "--no-nginx 与 --tls-cert/--tls-key 不能同时使用"
    [ -r "$TLS_CERT" ] || die "TLS 证书不可读: $TLS_CERT"
    [ -r "$TLS_KEY" ] || die "TLS 私钥不可读: $TLS_KEY"
    if ! printf '%s%s' "$TLS_CERT" "$TLS_KEY" | grep -Eq '^[A-Za-z0-9._/+:@-]+$'; then
      die "TLS 证书/私钥路径包含不支持的字符"
    fi
  fi

  local i path
  if [ "${#EXTRA_RW_PATHS[@]}" -gt 0 ]; then
    for i in "${!EXTRA_RW_PATHS[@]}"; do
      path="${EXTRA_RW_PATHS[$i]}"
      case "$path" in
        /*) ;;
        *) die "--read-write-path 必须是绝对路径: $path" ;;
      esac
      path="$(realpath -m "$path")"
      if [ "$path" = "/" ] || ! printf '%s' "$path" | grep -Eq '^[A-Za-z0-9._/+:@-]+$'; then
        die "--read-write-path 不合法: $path"
      fi
      case "$path" in
        /tmp|/tmp/*|/var/tmp|/var/tmp/*|/home|/home/*|/root|/root/*|/run/user|/run/user/*)
          die "--read-write-path 不能位于 PrivateTmp/ProtectHome 范围: $path"
          ;;
      esac
      EXTRA_RW_PATHS[$i]="$path"
    done
  fi
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

check_platform() {
  need_cmd uname
  [ "$(uname -s)" = "Linux" ] || die "仅支持 Linux（当前: $(uname -s)）"
  [ -d /run/systemd/system ] || die "未检测到 systemd，本脚本仅支持由 systemd 管理服务的 Linux 发行版"

  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "不支持的 CPU 架构: $(uname -m)（发布包仅提供 amd64 / arm64）" ;;
  esac

  need_cmd awk
  need_cmd sed
  need_cmd od
  need_cmd find
  need_cmd realpath
  need_cmd tar
  need_cmd install
  need_cmd systemctl
  need_cmd useradd
  need_cmd getent
  need_cmd mktemp

  if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
    die "需要 curl 或 wget 用于下载发布包"
  fi
  if [ "$SKIP_CHECKSUM" -eq 0 ] && ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
    die "需要 sha256sum 或 shasum 校验发布包；如确有需要可加 --skip-checksum"
  fi
  if [ "$SKIP_CHECKSUM" -eq 0 ] && [ -n "$RELEASE_PUBLIC_KEY" ] && ! command -v openssl >/dev/null 2>&1; then
    die "需要 openssl 校验发布签名；如确有需要可加 --allow-unsigned"
  fi
  if [ "$ENABLE_NGINX" -eq 1 ] && [ "$INSTALL_DEPS" -eq 0 ] && ! command -v nginx >/dev/null 2>&1; then
    die "未安装 Nginx；请先安装，或去掉 --no-install-deps"
  fi
}

fetch_url() {
  # fetch_url URL OUTPUT_FILE
  local url="$1" out="$2" progress=(--progress-bar)
  if [ ! -t 2 ]; then
    progress=(--silent --show-error)
  fi
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 --retry-delay 2 --connect-timeout 15 --max-time 1800 \
      "${progress[@]}" -o "$out" "$url"
  else
    wget -O "$out" --tries=3 --timeout=30 "$url"
  fi
}

# verify_release_signature 校验 checksums.txt 的 Ed25519 分离签名。
# 公钥使用与 config.yaml upgrade.program.public_key 相同的 32 字节原始公钥
# base64 格式；同时兼容 44 字节 SPKI DER base64，便于从标准工具导出。
verify_release_signature() {
  local key="$1" checksums="$2" sig_url="$3" work="$4"
  if [ -z "$key" ]; then
    if [ "$ALLOW_UNSIGNED" -eq 1 ]; then
      warn "未提供发布签名公钥，已按 --allow-unsigned 跳过验签"
      return 0
    fi
    die "缺少发布签名公钥：请设置 LACTSSL_RELEASE_PUBLIC_KEY=<base64 Ed25519 公钥>，或显式加 --allow-unsigned（不推荐）"
  fi
  command -v openssl >/dev/null 2>&1 || die "需要 openssl 校验发布签名"
  log "下载并校验发布签名 checksums.txt.sig"
  fetch_url "$sig_url" "$work/checksums.txt.sig" || die "下载 checksums.txt.sig 失败"
  printf '%s' "$key" | tr -d '[:space:]' | openssl base64 -d -A > "$work/release-public.raw" 2>/dev/null || die "发布签名公钥不是合法 base64"
  local size
  size="$(wc -c < "$work/release-public.raw" | tr -d ' ')"
  if [ "$size" = "32" ]; then
    printf '\x30\x2a\x30\x05\x06\x03\x2b\x65\x70\x03\x21\x00' > "$work/release-public.der"
    cat "$work/release-public.raw" >> "$work/release-public.der"
  elif [ "$size" = "44" ]; then
    cp "$work/release-public.raw" "$work/release-public.der"
  else
    die "发布签名公钥长度不正确（期望 32 或 44 字节，实际 $size）"
  fi
  tr -d '[:space:]' < "$work/checksums.txt.sig" | openssl base64 -d -A > "$work/checksums.txt.sig.bin" 2>/dev/null || die "发布签名不是合法 base64"
  if ! openssl pkeyutl -verify -pubin -inkey "$work/release-public.der" -keyform DER \
      -rawin -in "$checksums" -sigfile "$work/checksums.txt.sig.bin" >/dev/null 2>&1; then
    die "发布签名校验失败，拒绝安装"
  fi
  ok "发布签名校验通过"
}

fetch_text() {
  # fetch_text URL [extra curl args...]
  local url="$1"
  shift
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 --max-time 60 "$@" "$url"
  else
    wget -qO- --tries=3 --timeout=30 "$url"
  fi
}

resolve_release_tag() {
  local requested="$1" final json tag=""

  if [ "$requested" != "latest" ]; then
    case "$requested" in
      v*|V*) TAG="$requested" ;;
      *) TAG="v$requested" ;;
    esac
    if ! printf '%s' "$TAG" | grep -Eq '^[vV]?[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$'; then
      die "版本号格式不正确: $requested（应形如 v1.2.3）"
    fi
    return
  fi

  # 优先使用 GitHub 的 /releases/latest 跳转，避免匿名 API 限流。
  if command -v curl >/dev/null 2>&1; then
    final="$(curl -fsSL --retry 2 --connect-timeout 15 --max-time 60 \
      -o /dev/null -w '%{url_effective}' "$GITHUB/$REPO/releases/latest" 2>/dev/null || true)"
    case "$final" in
      */tag/*)
        tag="${final##*/tag/}"
        tag="${tag%%\?*}"
        ;;
    esac
  fi

  if [ -z "$tag" ]; then
    local api="https://api.github.com/repos/$REPO/releases/latest"
    local args=(-H 'Accept: application/vnd.github+json')
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      args+=(-H "Authorization: Bearer $GITHUB_TOKEN")
    fi
    if command -v curl >/dev/null 2>&1; then
      json="$(fetch_text "$api" "${args[@]}" 2>/dev/null || true)"
    else
      json="$(fetch_text "$api" 2>/dev/null || true)"
    fi
    tag="$(printf '%s' "$json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  fi

  [ -n "$tag" ] || die "无法获取最新版本，请用 --version 指定版本，或检查网络/代理设置"
  if ! printf '%s' "$tag" | grep -Eq '^[vV]?[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$'; then
    die "无法解析发布版本: $tag"
  fi
  TAG="$tag"
}

# ---------- YAML 文本编辑（只改指定键，保留其余注释与配置） ----------
yaml_set_scalar() {
  # yaml_set_scalar FILE SECTION KEY VALUE
  local file="$1" section="$2" key="$3" value="$4" tmp
  tmp="$(mktemp "${file}.tmp.XXXXXX")" || return 1
  if awk -v section="$section" -v key="$key" -v value="$value" '
    function top_key(line, s) {
      s = line
      sub(/:.*/, "", s)
      return s
    }
    BEGIN { in_section = 0; found = 0 }
    /^[^[:space:]#][^:]*:/ {
      in_section = (top_key($0) == section)
      print
      next
    }
    in_section && $0 ~ ("^[[:space:]]+" key "[[:space:]]*:") {
      match($0, /^[[:space:]]*/)
      indent = substr($0, 1, RLENGTH)
      print indent key ": " value
      found = 1
      next
    }
    { print }
    END { if (!found) exit 3 }
  ' "$file" > "$tmp"; then
    mv -f "$tmp" "$file"
  else
    rm -f "$tmp"
    return 1
  fi
}

yaml_set_block_or_flow_list() {
  # yaml_set_block_or_flow_list FILE SECTION KEY FLOW_VALUE
  # 替换标量列表，并删除紧随其后的块状列表项，避免留下孤立的 "- item"。
  local file="$1" section="$2" key="$3" value="$4" tmp
  tmp="$(mktemp "${file}.tmp.XXXXXX")" || return 1
  if awk -v section="$section" -v key="$key" -v value="$value" '
    function top_key(line, s) {
      s = line
      sub(/:.*/, "", s)
      return s
    }
    function indent_of(line, i, c, n) {
      n = 0
      for (i = 1; i <= length(line); i++) {
        c = substr(line, i, 1)
        if (c == " " || c == "\t") n++
        else break
      }
      return n
    }
    BEGIN { in_section = 0; skip = 0; key_indent = 0; found = 0 }
    /^[^[:space:]#][^:]*:/ {
      in_section = (top_key($0) == section)
      skip = 0
      print
      next
    }
    in_section && skip {
      if ($0 ~ /^[[:space:]]*$/) next
      if (indent_of($0) > key_indent) next
      skip = 0
    }
    in_section && $0 ~ ("^[[:space:]]*" key "[[:space:]]*:") {
      key_indent = indent_of($0)
      print substr($0, 1, key_indent) key ": " value
      found = 1
      skip = 1
      next
    }
    { print }
    END { if (!found) exit 3 }
  ' "$file" > "$tmp"; then
    mv -f "$tmp" "$file"
  else
    rm -f "$tmp"
    return 1
  fi
}

yaml_set_top_scalar() {
  # yaml_set_top_scalar FILE KEY VALUE
  local file="$1" key="$2" value="$3" tmp
  tmp="$(mktemp "${file}.tmp.XXXXXX")" || return 1
  if awk -v key="$key" -v value="$value" '
    BEGIN { found = 0 }
    /^[^[:space:]#][^:]*:/ {
      line = $0
      sub(/:.*/, "", line)
      if (line == key) {
        print key ": " value
        found = 1
        next
      }
    }
    { print }
    END { if (!found) exit 3 }
  ' "$file" > "$tmp"; then
    mv -f "$tmp" "$file"
  else
    rm -f "$tmp"
    return 1
  fi
}

yaml_get_scalar() {
  # yaml_get_scalar FILE SECTION KEY
  local file="$1" section="$2" key="$3"
  awk -v section="$section" -v key="$key" '
    function top_key(line, s) {
      s = line
      sub(/:.*/, "", s)
      return s
    }
    BEGIN { in_section = 0 }
    /^[^[:space:]#][^:]*:/ {
      in_section = (top_key($0) == section)
      next
    }
    in_section && $0 ~ ("^[[:space:]]*" key "[[:space:]]*:") {
      line = $0
      sub("^[[:space:]]*" key "[[:space:]]*:[[:space:]]*", "", line)
      sub(/[[:space:]]+#.*$/, "", line)
      if (line ~ /^".*"$/) line = substr(line, 2, length(line) - 2)
      else if (line ~ /^\047.*\047$/) line = substr(line, 2, length(line) - 2)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
      print line
      exit
    }
  ' "$file"
}

yaml_get_top_scalar() {
  # yaml_get_top_scalar FILE KEY
  local file="$1" key="$2"
  awk -v key="$key" '
    /^[^[:space:]#][^:]*:/ {
      line = $0
      sub(/:.*/, "", line)
      if (line == key) {
        rest = $0
        sub("^" key "[[:space:]]*:[[:space:]]*", "", rest)
        sub(/[[:space:]]+#.*$/, "", rest)
        if (rest ~ /^".*"$/) rest = substr(rest, 2, length(rest) - 2)
        else if (rest ~ /^\047.*\047$/) rest = substr(rest, 2, length(rest) - 2)
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", rest)
        print rest
        exit
      }
    }
  ' "$file"
}

gen_secret() {
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

write_fresh_config() {
  # write_fresh_config CONFIG EXAMPLE PUBLIC_URL
  local config="$1" example="$2" public_url="$3"
  local jwt salt

  if [ -n "$example" ] && [ -f "$example" ]; then
    cp "$example" "$config"
  else
    cat > "$config" <<'EOF'
# LACTSSL 配置文件
server:
  addr: ""
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: []
data_dir: "data"
security:
  entry: ""
  public_url: ""
  session_persistence: false
  jwt_secret: ""
  salt: ""
EOF
  fi

  jwt="$(gen_secret)"
  salt="$(gen_secret)"
  [ "${#jwt}" -ge 32 ] || die "生成 jwt_secret 失败"
  [ "${#salt}" -ge 32 ] || die "生成 salt 失败"

  yaml_set_scalar "$config" server addr '""' || die "配置文件缺少 server.addr，无法写入监听配置"
  yaml_set_scalar "$config" server host '"127.0.0.1"' || die "配置文件缺少 server.host，无法写入监听配置"
  yaml_set_scalar "$config" server port "$PORT" || die "配置文件缺少 server.port，无法写入监听配置"
  yaml_set_block_or_flow_list "$config" server trusted_proxies '["127.0.0.1", "::1"]' \
    || die "配置文件缺少 server.trusted_proxies，无法写入反向代理配置"
  yaml_set_top_scalar "$config" data_dir "\"$DATA_DIR\"" || die "配置文件缺少 data_dir"
  yaml_set_scalar "$config" security jwt_secret "\"$jwt\"" || die "配置文件缺少 security.jwt_secret"
  yaml_set_scalar "$config" security salt "\"$salt\"" || die "配置文件缺少 security.salt"
  if [ -n "$public_url" ]; then
    yaml_set_scalar "$config" security public_url "\"$public_url\"" \
      || die "配置文件缺少 security.public_url"
  fi
}

update_config_port() {
  local config="$1"
  local backup="$config.bak.$(date +%Y%m%d%H%M%S)"
  cp -a "$config" "$backup"
  log "已备份原配置: $backup"
  yaml_set_scalar "$config" server addr '""' || die "更新 server.addr 失败"
  yaml_set_scalar "$config" server host '"127.0.0.1"' || die "更新 server.host 失败"
  yaml_set_scalar "$config" server port "$PORT" || die "更新 server.port 失败"
  if ! grep -A5 -E '^[[:space:]]*trusted_proxies[[:space:]]*:' "$config" 2>/dev/null | grep '127\.0\.0\.1' >/dev/null; then
    warn "请确认 config.yaml 的 server.trusted_proxies 包含 Nginx 地址（本机为 127.0.0.1）"
  fi
}

parse_backend_addr() {
  # 根据 config.yaml 计算 Nginx 应转发到的地址。
  local config="$1" addr host port
  addr="$(yaml_get_scalar "$config" server addr || true)"
  if [ -n "$addr" ]; then
    case "$addr" in
      :*)
        BACKEND_HOST="127.0.0.1"
        BACKEND_PORT="${addr#:}"
        ;;
      \[*\]:*)
        BACKEND_HOST="${addr%]:*}"
        BACKEND_HOST="${BACKEND_HOST#[}"
        BACKEND_PORT="${addr##*:}"
        ;;
      *:*)
        BACKEND_HOST="${addr%:*}"
        BACKEND_PORT="${addr##*:}"
        ;;
      *)
        die "无法解析 server.addr: $addr"
        ;;
    esac
  else
    host="$(yaml_get_scalar "$config" server host || true)"
    port="$(yaml_get_scalar "$config" server port || true)"
    [ -n "$port" ] || port="8080"
    if [ "$port" = "0" ]; then
      port="8080"
    fi
    BACKEND_HOST="$host"
    BACKEND_PORT="$port"
  fi

  case "$BACKEND_HOST" in
    ''|0.0.0.0|localhost) BACKEND_HOST="127.0.0.1" ;;
    ::|'[::]') BACKEND_HOST="::1" ;;
  esac
  case "$BACKEND_PORT" in
    ''|*[!0-9]*) die "配置文件中的监听端口不合法: $BACKEND_PORT" ;;
  esac
  [ "${#BACKEND_PORT}" -le 5 ] || die "配置文件中的监听端口不合法: $BACKEND_PORT"
  BACKEND_PORT=$((10#$BACKEND_PORT))
  if [ "$BACKEND_PORT" -lt 1 ] || [ "$BACKEND_PORT" -gt 65535 ]; then
    die "配置文件中的监听端口不合法: $BACKEND_PORT"
  fi

  case "$BACKEND_HOST" in
    *:*) BACKEND_ADDR="[$BACKEND_HOST]:$BACKEND_PORT" ;;
    *)   BACKEND_ADDR="$BACKEND_HOST:$BACKEND_PORT" ;;
  esac
  if ! printf '%s' "$BACKEND_HOST" | grep -Eq '^[A-Za-z0-9._:-]+$'; then
    die "配置文件中的监听地址不合法: $BACKEND_HOST"
  fi
}

ensure_user() {
  local shell
  shell="/bin/false"
  for candidate in /usr/sbin/nologin /sbin/nologin /usr/bin/nologin; do
    if [ -x "$candidate" ]; then
      shell="$candidate"
      break
    fi
  done

  if ! getent group "$APP" >/dev/null 2>&1; then
    need_cmd groupadd
    groupadd --system "$APP"
  fi
  if ! id -u "$APP" >/dev/null 2>&1; then
    useradd --system --gid "$APP" --home-dir "$PREFIX" --no-create-home --shell "$shell" "$APP"
  fi
}

install_nginx_package() {
  log "未检测到 Nginx，尝试自动安装"
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y nginx
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y nginx
  elif command -v yum >/dev/null 2>&1; then
    yum install -y nginx
  elif command -v zypper >/dev/null 2>&1; then
    zypper --non-interactive install nginx
  else
    die "未找到受支持的包管理器，请手工安装 Nginx 后重试"
  fi
}

nginx_has_default_server() {
  # nginx_has_default_server PORT
  local port="$1" file files=()
  if [ -f "$NGINX_ROOT/nginx.conf" ]; then
    files+=("$NGINX_ROOT/nginx.conf")
  fi
  while IFS= read -r file; do
    files+=("$file")
  done < <(find "$NGINX_ROOT/conf.d" "$NGINX_ROOT/sites-enabled" -maxdepth 1 \( -type f -o -type l \) 2>/dev/null)

  if [ "${#files[@]}" -gt 0 ]; then
    for file in "${files[@]}"; do
      case "$file" in
        "$NGINX_CONF"|*.bak.*|*.tmp.*)
          continue
          ;;
      esac
      if grep -E '^[[:space:]]*listen[[:space:]]' "$file" 2>/dev/null \
        | grep 'default_server' >/dev/null \
        && grep -E '^[[:space:]]*listen[[:space:]]' "$file" 2>/dev/null \
        | grep -E "(^|[[:space:]:])${port}([[:space:]]|;|$)" >/dev/null; then
        return 0
      fi
    done
  fi
  return 1
}
write_nginx_config() {
  local tmp="$NGINX_CONF.tmp.$$" def80="" def443=""
  mkdir -p "$(dirname "$NGINX_CONF")"

  if [ "$DOMAIN" = "_" ]; then
    if ! nginx_has_default_server 80; then
      def80=" default_server"
    else
      warn "检测到已有 Nginx 默认站点，服务器 IP 访问可能不会进入 LACTSSL；建议使用 --domain 指定域名"
    fi
    if [ -n "$TLS_CERT" ] && ! nginx_has_default_server 443; then
      def443=" default_server"
    fi
  fi

  if [ -n "$TLS_CERT" ]; then
    cat > "$tmp" <<EOF
# Managed by LACTSSL install.sh
# 上游: http://$BACKEND_ADDR
server {
    listen 80$def80;
    server_name $DOMAIN;
    return 301 https://\$host\$request_uri;
}

server {
    listen 443 ssl$def443;
    server_name $DOMAIN;

    ssl_certificate     $TLS_CERT;
    ssl_certificate_key $TLS_KEY;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 1d;

    client_max_body_size 256m;
    client_body_timeout  600s;
    send_timeout         600s;

    location / {
        proxy_pass http://$BACKEND_ADDR;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-Host \$host;
        proxy_set_header X-Request-ID \$request_id;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
EOF
  else
    cat > "$tmp" <<EOF
# Managed by LACTSSL install.sh
# 上游: http://$BACKEND_ADDR
server {
    listen 80$def80;
    server_name $DOMAIN;

    client_max_body_size 256m;
    client_body_timeout  600s;
    send_timeout         600s;

    location / {
        proxy_pass http://$BACKEND_ADDR;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-Host \$host;
        proxy_set_header X-Request-ID \$request_id;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
EOF
  fi

  chmod 0644 "$tmp"
  chown root:root "$tmp" 2>/dev/null || true

  if [ -f "$NGINX_CONF" ]; then
    NGINX_BACKUP="$NGINX_CONF.bak.$(date +%Y%m%d%H%M%S)"
    cp -a "$NGINX_CONF" "$NGINX_BACKUP"
    if ! grep -q 'Managed by LACTSSL install.sh' "$NGINX_CONF"; then
      warn "检测到非脚本管理的 Nginx 配置，已备份到 $NGINX_BACKUP"
    fi
  else
    NGINX_CONF_CREATED=1
  fi

  mv -f "$tmp" "$NGINX_CONF"
}

restore_nginx_config() {
  if [ -n "$NGINX_BACKUP" ] && [ -f "$NGINX_BACKUP" ]; then
    mv -f "$NGINX_BACKUP" "$NGINX_CONF"
  elif [ "$NGINX_CONF_CREATED" -eq 1 ]; then
    rm -f "$NGINX_CONF"
  fi
}

setup_nginx() {
  [ "$ENABLE_NGINX" -eq 1 ] || {
    log "已跳过 Nginx 配置（--no-nginx）"
    return
  }

  if ! command -v nginx >/dev/null 2>&1; then
    if [ "$INSTALL_DEPS" -eq 1 ]; then
      install_nginx_package
    else
      die "未安装 Nginx；请手工安装，或去掉 --no-install-deps"
    fi
  fi

  write_nginx_config

  if ! nginx -t; then
    restore_nginx_config
    die "Nginx 配置校验失败；已恢复原配置。应用本身应仍在 http://127.0.0.1:$BACKEND_PORT 运行"
  fi

  systemctl enable nginx >/dev/null 2>&1 || true
  if systemctl is-active --quiet nginx; then
    if ! systemctl reload nginx; then
      restore_nginx_config
      journalctl -u nginx --no-pager -n 40 || true
      die "Nginx 加载失败；已恢复原配置。应用本身仍在 http://127.0.0.1:$BACKEND_PORT 运行，请检查 80 端口占用或 Nginx 日志"
    fi
  else
    if ! systemctl start nginx; then
      restore_nginx_config
      journalctl -u nginx --no-pager -n 40 || true
      die "Nginx 启动失败；已恢复原配置。应用本身仍在 http://127.0.0.1:$BACKEND_PORT 运行"
    fi
  fi
  if ! systemctl is-active --quiet nginx; then
    journalctl -u nginx --no-pager -n 40 || true
    die "Nginx 未能保持运行；应用本身仍在 http://127.0.0.1:$BACKEND_PORT 运行"
  fi
  ok "Nginx 已配置并加载: $NGINX_CONF"
  if command -v nginx >/dev/null 2>&1 && ! nginx -T 2>/dev/null | grep 'Managed by LACTSSL install.sh' >/dev/null; then
    warn "Nginx 主配置可能未包含 $NGINX_CONF，请检查 nginx.conf 中的 include 指令"
  fi
  if command -v getenforce >/dev/null 2>&1 && [ "$(getenforce 2>/dev/null || true)" = "Enforcing" ]; then
    warn "SELinux 处于 Enforcing；若 Nginx 返回 502，请执行: setsebool -P httpd_can_network_connect on"
  fi
}

wait_for_service() {
  local i code
  for i in {1..120}; do
    if ! systemctl is-active --quiet "$SERVICE_NAME"; then
      sleep 1
      continue
    fi
    if command -v curl >/dev/null 2>&1; then
      code="$(curl -s --noproxy '*' -o /dev/null --max-time 2 -w '%{http_code}' "http://$BACKEND_ADDR/" 2>/dev/null || true)"
      if [ -n "$code" ] && [ "$code" != "000" ]; then
        return 0
      fi
    elif (exec 3<>"/dev/tcp/$BACKEND_HOST/$BACKEND_PORT") 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

assert_port_free() {
  local i
  for i in {1..10}; do
    if command -v ss >/dev/null 2>&1; then
      if ! ss -ltn 2>/dev/null | grep -E "[:.]$BACKEND_PORT[[:space:]]" >/dev/null; then
        return 0
      fi
    elif ! (exec 3<>"/dev/tcp/$BACKEND_HOST/$BACKEND_PORT") 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

rollback_binary() {
  if [ -n "$BINARY_BACKUP" ] && [ -f "$BINARY_BACKUP" ]; then
    warn "尝试回滚到旧二进制"
    install -m 0755 -o root -g root "$BINARY_BACKUP" "$PREFIX/$APP"
    if ! systemctl restart "$SERVICE_NAME" >/dev/null 2>&1; then
      warn "旧二进制也未能启动，已停止服务，请检查上方日志"
      systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
    fi
  else
    warn "首次安装失败，停止并禁用服务，避免反复重启"
    systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
    systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
}

print_summary() {
  local access_url="http://127.0.0.1:$BACKEND_PORT"
  local entry_path=""
  local rerun="$SCRIPT_PATH"

  case "$SCRIPT_PATH" in
    bash|sh|/bin/bash|/bin/sh|/usr/bin/bash|/usr/bin/sh) rerun="./scripts/install.sh" ;;
  esac

  if [ "$ENABLE_NGINX" -eq 1 ]; then
    if [ -n "$TLS_CERT" ]; then
      case "$DOMAIN" in
        _|*'*'*) access_url="https://<服务器地址>" ;;
        *) access_url="https://$DOMAIN" ;;
      esac
    else
      case "$DOMAIN" in
        _|*'*'*) access_url="http://<服务器地址>" ;;
        *) access_url="http://$DOMAIN" ;;
      esac
    fi
  fi

  entry_path="$(journalctl -u "$SERVICE_NAME" --no-pager -n 200 2>/dev/null \
    | grep -E '门禁入口: https?://|Secure entry: https?://' \
    | tail -n 1 | sed -E 's#^.*(门禁入口|Secure entry): https?://[^/]+##' || true)"
  if [ -n "$entry_path" ]; then
    access_url="${access_url%/}$entry_path"
  fi

  local public_url
  public_url="$(yaml_get_scalar "$CONFIG" security public_url 2>/dev/null || true)"

  printf '\n%s%s 部署完成%s\n' "$C_BOLD" "$C_GREEN" "$C_RESET"
  printf '  版本      : %s\n' "$TAG"
  printf '  安装目录  : %s\n' "$PREFIX"
  printf '  配置文件  : %s/config.yaml\n' "$PREFIX"
  printf '  数据目录  : %s\n' "$DATA_DIR"
  printf '  后端监听  : http://%s\n' "$BACKEND_ADDR"
  printf '  访问地址  : %s\n' "$access_url"
  if [ "$ENABLE_NGINX" -eq 1 ]; then
    printf '  Nginx 配置: %s\n' "$NGINX_CONF"
  fi
  printf '\n  查看状态  : systemctl status %s\n' "$SERVICE_NAME"
  printf '  查看日志  : journalctl -u %s -f\n' "$SERVICE_NAME"
  printf '  再次运行  : %s --version latest\n' "$rerun"

  printf '\n  首次启动日志（包含门禁入口和一次性初始化令牌）：\n'
  journalctl -u "$SERVICE_NAME" --no-pager -n 20 2>/dev/null | sed 's/^/    /' || true

  if [ "$DOMAIN" = "_" ] && [ "$ENABLE_NGINX" -eq 1 ]; then
    warn "未指定 --domain。Nginx 使用默认 server_name，建议设置域名或手工修改 $NGINX_CONF"
  fi
  if [ -z "$TLS_CERT" ] && [ "$ENABLE_NGINX" -eq 1 ]; then
    warn "当前未启用 TLS。生产环境请使用 --tls-cert/--tls-key，或在外层负载均衡/WAF 终止 TLS"
  fi
  if [ -z "$public_url" ]; then
    if [ -n "$TLS_CERT" ] && [ "$DOMAIN" != "_" ] && [[ "$DOMAIN" != *'*'* ]]; then
      warn "config.yaml 的 security.public_url 为空，建议设置为 https://$DOMAIN"
    elif [ "$DOMAIN" != "_" ] && [[ "$DOMAIN" != *'*'* ]]; then
      warn "config.yaml 的 security.public_url 为空，建议设置为 http://$DOMAIN（密码重置等链接需要）"
    else
      warn "config.yaml 的 security.public_url 为空；请设置为实际对外访问地址"
    fi
  elif [ -n "$TLS_CERT" ] && [[ "$public_url" == http://* ]]; then
    warn "security.public_url 为 $public_url，但 Nginx 已启用 TLS；建议改为 https://"
  elif [ "$ENABLE_NGINX" -eq 1 ] && [ -z "$TLS_CERT" ] && [[ "$public_url" == https://* ]]; then
    warn "security.public_url 为 $public_url，但当前 Nginx 未启用 TLS；请确认外层是否终止 TLS"
  fi
  if [ "$ENABLE_NGINX" -eq 1 ]; then
    if [ -n "$TLS_CERT" ]; then
      warn "如启用了 firewalld/ufw/云安全组，请放行 80/443 端口"
    else
      warn "如启用了 firewalld/ufw/云安全组，请放行 80 端口"
    fi
  else
    warn "已跳过 Nginx；应用默认仅监听 127.0.0.1，如需远程直连请修改 config.yaml 的 server.host"
  fi
}

load_saved_nginx_settings() {
  # 重复执行且未显式指定 --domain/--tls-* 时，沿用已有 Nginx 配置，避免升级时
  # 意外退回默认 server_name 或关闭 TLS。
  [ "$ENABLE_NGINX" -eq 1 ] || return 0
  [ -f "$NGINX_CONF" ] || return 0
  grep 'Managed by LACTSSL install.sh' "$NGINX_CONF" >/dev/null 2>&1 || return 0

  local domain cert key
  if [ "$DOMAIN_EXPLICIT" -eq 0 ]; then
    domain="$(awk '
      $1 == "server_name" {
        value = $2
        sub(/;.*$/, "", value)
        gsub(/"/, "", value)
        print value
        exit
      }
    ' "$NGINX_CONF")"
    if [ -n "$domain" ]; then
      DOMAIN="$domain"
      log "沿用已有 Nginx 域名: $DOMAIN"
    fi
  fi

  if [ "$TLS_EXPLICIT" -eq 0 ]; then
    cert="$(awk '
      $1 == "ssl_certificate" {
        value = $2
        sub(/;.*$/, "", value)
        gsub(/"/, "", value)
        print value
        exit
      }
    ' "$NGINX_CONF")"
    key="$(awk '
      $1 == "ssl_certificate_key" {
        value = $2
        sub(/;.*$/, "", value)
        gsub(/"/, "", value)
        print value
        exit
      }
    ' "$NGINX_CONF")"
    if [ -n "$cert" ] && [ -n "$key" ]; then
      TLS_CERT="$cert"
      TLS_KEY="$key"
      log "沿用已有 Nginx TLS 证书: $TLS_CERT"
    elif [ -n "$cert" ] || [ -n "$key" ]; then
      die "现有 Nginx 配置中的 TLS 证书/私钥不完整，请手工检查 $NGINX_CONF"
    fi
  fi
}

main() {
  check_platform
  log "LACTSSL 一键部署"
  load_saved_nginx_settings
  validate_inputs

  log "仓库: $REPO  版本: $VERSION  架构: linux/$ARCH"
  log "安装目录: $PREFIX  后端端口: $PORT  Nginx: $([ "$ENABLE_NGINX" -eq 1 ] && echo 启用 || echo 跳过)"

  resolve_release_tag "$VERSION"
  RELEASE_VERSION="${TAG#[vV]}"
  ASSET="${APP}_${RELEASE_VERSION}_linux_${ARCH}.tar.gz"
  ASSET_URL="$GITHUB/$REPO/releases/download/$TAG/$ASSET"
  CHECKSUMS_URL="$GITHUB/$REPO/releases/download/$TAG/checksums.txt"

  TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/lactssl-install.XXXXXX")"
  log "下载发布包: $ASSET"
  fetch_url "$ASSET_URL" "$TMP_DIR/$ASSET" || die "下载失败: $ASSET_URL"

  if [ "$SKIP_CHECKSUM" -eq 1 ]; then
    warn "已跳过 SHA-256 校验（--skip-checksum）"
  else
    log "下载并校验 checksums.txt"
    fetch_url "$CHECKSUMS_URL" "$TMP_DIR/checksums.txt" || die "下载 checksums.txt 失败"
    awk -v asset="$ASSET" '{ f = $2; sub(/^\*/, "", f); sub(/\r$/, "", f); if (f == asset) print $1 "  " asset }' "$TMP_DIR/checksums.txt" > "$TMP_DIR/checksum.line"
    [ -s "$TMP_DIR/checksum.line" ] || die "checksums.txt 中找不到 $ASSET"
    if command -v sha256sum >/dev/null 2>&1; then
      (cd "$TMP_DIR" && sha256sum -c checksum.line) || die "SHA-256 校验失败: $ASSET"
    else
      (cd "$TMP_DIR" && shasum -a 256 -c checksum.line) || die "SHA-256 校验失败: $ASSET"
    fi
    verify_release_signature "$RELEASE_PUBLIC_KEY" "$TMP_DIR/checksums.txt" "${CHECKSUMS_URL}.sig" "$TMP_DIR"
  fi

  if tar -tzf "$TMP_DIR/$ASSET" | grep -E '(^/|(^|/)\.\.(/|$))' >/dev/null; then
    die "发布包包含不安全路径，拒绝解压"
  fi
  mkdir -p "$TMP_DIR/extract"
  tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR/extract"
  if [ -L "$TMP_DIR/extract/$APP" ]; then
    die "发布包中的 $APP 不能是符号链接"
  fi
  [ -f "$TMP_DIR/extract/$APP" ] || die "发布包中找不到 $APP 二进制"
  [ -f "$TMP_DIR/extract/config.yaml.example" ] || warn "发布包中没有 config.yaml.example，将使用内置最小配置"

  ensure_user
  mkdir -p "$PREFIX"
  chmod 0755 "$PREFIX"
  chown root:root "$PREFIX"

  CONFIG="$PREFIX/config.yaml"
  local example="$TMP_DIR/extract/config.yaml.example"
  local public_url=""
  local existing_public_url=""

  if [ "$DOMAIN" != "_" ] && ! printf '%s' "$DOMAIN" | grep -q '\*'; then
    if [ -n "$TLS_CERT" ]; then
      public_url="https://$DOMAIN"
    else
      public_url="http://$DOMAIN"
    fi
  fi

  if [ ! -f "$CONFIG" ]; then
    log "生成新配置文件: $CONFIG"
    DATA_DIR="$PREFIX/data"
    mkdir -p "$DATA_DIR"
    write_fresh_config "$CONFIG" "$example" "$public_url"
    ok "配置文件已生成"
  else
    log "检测到已有配置，保留并复用: $CONFIG"
    if [ "$PORT_EXPLICIT" -eq 1 ]; then
      log "根据 --port 更新监听端口为 $PORT"
      update_config_port "$CONFIG"
    fi
    existing_public_url="$(yaml_get_scalar "$CONFIG" security public_url || true)"
    if [ -z "$existing_public_url" ] && [ -n "$public_url" ]; then
      if yaml_set_scalar "$CONFIG" security public_url "\"$public_url\""; then
        ok "已补充 security.public_url = $public_url"
      else
        warn "未能补充 security.public_url，请手工设置"
      fi
    elif [ -n "$TLS_CERT" ] && [ "$existing_public_url" = "http://$DOMAIN" ]; then
      if yaml_set_scalar "$CONFIG" security public_url "\"https://$DOMAIN\""; then
        ok "已将 security.public_url 更新为 https://$DOMAIN"
      else
        warn "未能更新 security.public_url，请手工改为 https://$DOMAIN"
      fi
    fi
    DATA_DIR="$(yaml_get_top_scalar "$CONFIG" data_dir || true)"
    [ -n "$DATA_DIR" ] || DATA_DIR="data"
    case "$DATA_DIR" in
      /*) ;;
      *) DATA_DIR="$PREFIX/$DATA_DIR" ;;
    esac
    mkdir -p "$DATA_DIR"
  fi

  DATA_DIR="$(realpath -m "$DATA_DIR")"
  if ! printf '%s' "$DATA_DIR" | grep -Eq '^[A-Za-z0-9._/+:@-]+$'; then
    die "数据目录包含不支持的字符: $DATA_DIR"
  fi
  case "$DATA_DIR" in
    /|/bin|/boot|/dev|/etc|/home|/lib|/lib64|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/var)
      die "数据目录不能直接使用系统目录: $DATA_DIR"
      ;;
    /tmp|/tmp/*|/var/tmp|/var/tmp/*)
      die "数据目录不能位于 /tmp 或 /var/tmp（systemd PrivateTmp 会导致路径不可见）: $DATA_DIR"
      ;;
    /home|/home/*|/root|/root/*|/run/user|/run/user/*)
      die "数据目录不能位于受 ProtectHome 保护的目录: $DATA_DIR"
      ;;
  esac

  # 未显式指定 --read-write-path 时，沿用已有服务单元中的额外可写目录。
  if [ "$RW_EXPLICIT" -eq 0 ] && [ -f "/etc/systemd/system/$SERVICE_NAME.service" ]; then
    local rw_line saved_rw existing_rw already restored_rw
    local -a saved_rw_list=()
    rw_line="$(grep -m1 '^ReadWritePaths=' "/etc/systemd/system/$SERVICE_NAME.service" 2>/dev/null || true)"
    restored_rw=0
    if [ -n "$rw_line" ]; then
      read -r -a saved_rw_list <<< "${rw_line#ReadWritePaths=}"
      for saved_rw in "${saved_rw_list[@]}"; do
        saved_rw="${saved_rw#-}"
        [ -n "$saved_rw" ] || continue
        saved_rw="$(realpath -m "$saved_rw")"
        [ "$saved_rw" = "$DATA_DIR" ] && continue
        [ "$saved_rw" = "$CONFIG" ] && continue
        already=0
        if [ "${#EXTRA_RW_PATHS[@]}" -gt 0 ]; then
          for existing_rw in "${EXTRA_RW_PATHS[@]}"; do
            if [ "$existing_rw" = "$saved_rw" ]; then
              already=1
              break
            fi
          done
        fi
        if [ "$already" -eq 0 ]; then
          EXTRA_RW_PATHS+=("$saved_rw")
          restored_rw=$((restored_rw + 1))
        fi
      done
    fi
    if [ "$restored_rw" -gt 0 ]; then
      log "沿用已有 systemd 额外可写目录（$restored_rw 个）"
    fi
  fi

  chown "$APP:$APP" "$CONFIG" "$DATA_DIR"
  chmod 0600 "$CONFIG"
  chmod 0700 "$DATA_DIR"

  parse_backend_addr "$CONFIG"
  if [ "$PORT_EXPLICIT" -eq 1 ] && [ "$BACKEND_PORT" != "$PORT" ]; then
    die "配置中的端口 $BACKEND_PORT 与 --port $PORT 不一致"
  fi

  mkdir -p "$PREFIX/backups"
  chmod 0700 "$PREFIX/backups"
  if [ -f "$PREFIX/$APP" ]; then
    BINARY_BACKUP="$PREFIX/backups/$APP.$(date +%Y%m%d%H%M%S)"
    cp -a "$PREFIX/$APP" "$BINARY_BACKUP"
    log "已备份旧二进制: $BINARY_BACKUP"
  fi

  log "停止旧服务（如存在）"
  systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true

  if ! assert_port_free; then
    die "端口 $BACKEND_PORT 已被其他进程占用；请停止占用进程或用 --port 指定其他端口"
  fi

  log "安装二进制到 $PREFIX/$APP"
  install -m 0755 -o root -g root "$TMP_DIR/extract/$APP" "$PREFIX/$APP.new"
  mv -f "$PREFIX/$APP.new" "$PREFIX/$APP"
  chown root:root "$PREFIX/$APP"

  local rw_paths=("$DATA_DIR" "$CONFIG")
  local rw
  if [ "${#EXTRA_RW_PATHS[@]}" -gt 0 ]; then
    for rw in "${EXTRA_RW_PATHS[@]}"; do
      rw_paths+=("-$rw")
    done
  fi

  if [ -f "/etc/systemd/system/$SERVICE_NAME.service" ]; then
    cp -a "/etc/systemd/system/$SERVICE_NAME.service" \
      "/etc/systemd/system/$SERVICE_NAME.service.bak.$(date +%Y%m%d%H%M%S)"
  fi

  cat > "/etc/systemd/system/$SERVICE_NAME.service" <<EOF
[Unit]
Description=LACTSSL certificate manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$APP
Group=$APP
WorkingDirectory=$PREFIX
ExecStart=$PREFIX/$APP -config $CONFIG
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${rw_paths[*]}

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "/etc/systemd/system/$SERVICE_NAME.service"

  log "启动 systemd 服务: $SERVICE_NAME"
  systemctl daemon-reload || die "systemd daemon-reload 失败"
  systemctl enable "$SERVICE_NAME" >/dev/null || warn "systemctl enable $SERVICE_NAME 失败，请检查服务单元"
  if ! systemctl restart "$SERVICE_NAME"; then
    journalctl -u "$SERVICE_NAME" --no-pager -n 80 || true
    rollback_binary
    die "服务启动失败，请检查上方日志"
  fi

  if ! wait_for_service; then
    journalctl -u "$SERVICE_NAME" --no-pager -n 80 || true
    rollback_binary
    die "服务启动失败或端口 $BACKEND_PORT 未就绪，请检查上方日志"
  fi
  ok "服务已启动: http://$BACKEND_ADDR"

  setup_nginx

  if [ -n "$BINARY_BACKUP" ]; then
    ls -1t "$PREFIX/backups/$APP".* 2>/dev/null | tail -n +4 | xargs -r rm -f --
  fi

  print_summary
}

main "$@"
