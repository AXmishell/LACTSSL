#!/usr/bin/env bash
# 本地运行与 .github/workflows/ci.yml 等价的检查。
# 用法: ./scripts/ci.sh [--backend-only | --frontend-only | --no-build]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

RUN_BACKEND=1
RUN_FRONTEND=1
RUN_BUILD=1

usage() {
  cat <<'EOF'
用法: ./scripts/ci.sh [选项]

选项:
  --backend-only   只运行后端检查（gofmt / vet / test）
  --frontend-only  只运行前端检查（npm ci / typecheck / build）
  --no-build       跳过最终二进制构建
  -h, --help       显示帮助
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --backend-only)
      RUN_FRONTEND=0
      RUN_BUILD=0
      ;;
    --frontend-only)
      RUN_BACKEND=0
      RUN_BUILD=0
      ;;
    --no-build)
      RUN_BUILD=0
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

export GOFLAGS="${GOFLAGS:--mod=readonly}"
export NPM_CONFIG_AUDIT=false
export NPM_CONFIG_FUND=false

if [ -t 1 ]; then
  BOLD=$'\033[1m'
  BLUE=$'\033[1;34m'
  GREEN=$'\033[1;32m'
  RED=$'\033[1;31m'
  RESET=$'\033[0m'
else
  BOLD='' BLUE='' GREEN='' RED='' RESET=''
fi

step() {
  printf '\n%s==> %s%s\n' "$BLUE" "$*" "$RESET"
}

step_failed() {
  printf '\n%s✗ %s%s\n' "$RED" "$*" "$RESET" >&2
}

trap 'step_failed "CI 检查失败，请查看上方日志"' ERR

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少命令: $1" >&2
    exit 1
  fi
}

start_time=$SECONDS

if [ "$RUN_BACKEND" -eq 1 ]; then
  require go

  step "检查 gofmt"
  mapfile -t unformatted < <(
    find . -path ./web/node_modules -prune -o -name '*.go' -type f -print0 | xargs -0 -r gofmt -l
  )
  if [ "${#unformatted[@]}" -gt 0 ]; then
    echo "以下文件未通过 gofmt，请运行 gofmt -w 后重新提交："
    printf '  %s\n' "${unformatted[@]}"
    exit 1
  fi

  step "go vet"
  go vet ./...

  step "go test（含 race 检测）"
  test_flags=()
  if [ "$(go env CGO_ENABLED)" = "1" ] && command -v "$(go env CC)" >/dev/null 2>&1; then
    test_flags+=(-race)
  else
    echo "提示: 当前环境未启用 CGO 或缺少 C 编译器，跳过 -race（CI 中仍会执行）"
  fi
  go test "${test_flags[@]}" -covermode=atomic -coverprofile=coverage.out ./...

  step "覆盖率"
  go tool cover -func=coverage.out | tail -n 1

  if command -v govulncheck >/dev/null 2>&1; then
    step "govulncheck"
    govulncheck ./...
  else
    echo "提示: 未安装 govulncheck，跳过漏洞扫描（CI 中仍会执行）"
  fi
fi

if [ "$RUN_FRONTEND" -eq 1 ]; then
  require node
  require npm

  step "安装前端依赖 (npm ci)"
  (cd web && npm ci)

  step "前端依赖漏洞扫描 (npm audit)"
  (cd web && npm audit --audit-level=high)

  step "前端类型检查 (vue-tsc)"
  (cd web && npm run typecheck)

  step "脚本类型检查 (tsc)"
  (cd web && ./node_modules/.bin/tsc -p ../scripts/tsconfig.json)

  step "国际化资源检查"
  node scripts/check-i18n.ts

  step "前端构建 (vite build)"
  (cd web && npm run build)
fi

if [ "$RUN_BUILD" -eq 1 ]; then
  require go

  step "生成 DNS 插件包装包"
  go run scripts/gen_dns_plugins/main.go
  if command -v git >/dev/null 2>&1 && git rev-parse --git-dir >/dev/null 2>&1; then
    if ! git diff --exit-code -- internal/dnsplugin >/dev/null 2>&1; then
      echo "internal/dnsplugin 生成结果与仓库不一致，请运行 make gen-plugins 并提交" >&2
      exit 1
    fi
  fi

  step "构建二进制（前端资源嵌入）"
  rm -rf internal/webui/dist
  cp -a web/dist internal/webui/dist
  mkdir -p bin
  build_version="$(./scripts/version.sh)"
  build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  build_arch="$(go env GOOS)/$(go env GOARCH)"
  CGO_ENABLED=0 go build -trimpath -tags embed \
    -ldflags "-s -w -X 'lactssl/internal/api.Version=$build_version' -X 'lactssl/internal/api.BuildTime=$build_time' -X 'lactssl/internal/api.BuildArch=$build_arch'" \
    -o bin/lactssl ./cmd/server

  step "校验产物"
  test -x bin/lactssl
  ls -lh bin/lactssl

  step "构建全量版（dns_all）校验"
  CGO_ENABLED=0 go build -trimpath -tags "embed dns_all" -o /dev/null ./cmd/server

  step "构建 DNS helper 校验"
  CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/dns-helper
  CGO_ENABLED=0 go build -trimpath -tags dns_extra -o /dev/null ./cmd/dns-helper

  step "构建内嵌 DNS helper 的主程序校验"
  mkdir -p internal/dnsplugin/bundled
  CGO_ENABLED=0 go build -trimpath -tags dns_extra -o internal/dnsplugin/bundled/lactssl-dns-helper-extra ./cmd/dns-helper
  CGO_ENABLED=0 go build -trimpath -tags "embed embed_helper" -o /dev/null ./cmd/server
  rm -f internal/dnsplugin/bundled/lactssl-dns-helper-extra

  step "构建部署 helper 校验"
  CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/deploy-helper
fi

trap - ERR
printf '\n%s✓ 本地 CI 全部通过（耗时 %ss）%s\n' "$GREEN" "$((SECONDS - start_time))" "$RESET"
