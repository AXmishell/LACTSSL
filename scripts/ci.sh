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
  require python3

  step "CSR 客户端检查 (py_compile / i18n / unittest)"
  python3 -m py_compile scripts/csr_client.py scripts/check-csr-client-i18n.py scripts/tests/test_csr_client.py
  python3 scripts/check-csr-client-i18n.py
  python3 -m unittest discover -s scripts/tests -v

  step "检查 gofmt"
  # 与 GitHub CI 保持一致：只检查 git 跟踪的文件；
  # 同时纳入未跟踪但未被忽略的新文件，便于提交前发现问题。
  mapfile -t unformatted < <(
    {
      git ls-files -z '*.go'
      git ls-files -z --others --exclude-standard '*.go'
    } | xargs -0 -r gofmt -l
  )
  if [ "${#unformatted[@]}" -gt 0 ]; then
    echo "以下文件未通过 gofmt，请运行 gofmt -w 后重新提交："
    printf '  %s\n' "${unformatted[@]}"
    exit 1
  fi

  step "SQL 方言检查"
  ./scripts/check-sql-dialect.sh

  step "go vet"
  go vet ./...

  step "插件元数据一致性"
  (cd internal/plugin && go run ./scripts/metadatacheck/main.go)

  step "插件导入边界"
  if go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/plugin/host/... | grep -qE 'lactssl/providers/(dns|deploy|notify)'; then
    echo "host 层不得依赖 providers 实现（api/catalog 除外）" >&2
    go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/plugin/host/... | grep -E 'lactssl/providers/(dns|deploy|notify)' >&2
    exit 1
  fi
  if go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/plugin/runtime/... | grep -qE 'internal/plugin/(host|providers)'; then
    echo "runtime 层不得依赖 host/providers" >&2
    go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/plugin/runtime/... | grep -E 'internal/plugin/(host|providers)' >&2
    exit 1
  fi

  step "模块边界"
  if go list -deps -tags nobuiltin_dns ./cmd/server | grep -q 'go-acme/lego/v5'; then
    echo "nobuiltin_dns 构建仍依赖 lego v5" >&2
    exit 1
  fi
  if (cd internal/plugin/providers && go list -deps ./... | grep -q '^lactssl/internal'); then
    echo "providers module 不得依赖宿主 internal 包" >&2
    exit 1
  fi

  step "go test（含 race 检测）"
  test_flags=()
  if [ "$(go env CGO_ENABLED)" = "1" ] && command -v "$(go env CC)" >/dev/null 2>&1; then
    test_flags+=(-race)
  else
    echo "提示: 当前环境未启用 CGO 或缺少 C 编译器，跳过 -race（CI 中仍会执行）"
  fi
  go test "${test_flags[@]}" -covermode=atomic -coverprofile=coverage.out ./...

  step "插件裁剪构建"
  for tags in nobuiltin_dns dns_providers_common dns_providers_minimal; do
    CGO_ENABLED=0 go build -trimpath -tags "$tags nomsgpack validator_novalidatefn" -o /dev/null ./cmd/server
  done

  step "覆盖率"
  go tool cover -func=coverage.out | tail -n 1

  step "插件 module 检查（plugins/）"
  (cd plugins && go vet ./... && go test ./...)

  step "官方 provider module 检查（internal/plugin/providers/）"
  (cd internal/plugin/providers && go vet ./... && go test ./...)

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

  step "前端依赖漏洞扫描 (npm audit，带豁免清单)"
  (cd web && npm run audit)

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

  step "构建二进制（前端资源嵌入）"
  rm -rf internal/webui/dist
  cp -a web/dist internal/webui/dist
  go run ./scripts/compress-assets.go internal/webui/dist
  go run ./scripts/bundle-i18n.go

  step "校验嵌入语言包"
  go test -tags embed ./internal/i18n

  mkdir -p bin
  build_version="$(./scripts/version.sh)"
  build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  build_arch="$(go env GOOS)/$(go env GOARCH)"
  CGO_ENABLED=0 go build -trimpath -tags "embed nomsgpack validator_novalidatefn" \
    -ldflags "-s -w -X 'lactssl/internal/api.Version=$build_version' -X 'lactssl/internal/api.BuildTime=$build_time' -X 'lactssl/internal/api.BuildArch=$build_arch'" \
    -o bin/lactssl ./cmd/server

  step "校验产物"
  test -x bin/lactssl
  ls -lh bin/lactssl

  # validateFn 在启用 validator_novalidatefn 后会 panic；仓库约定不使用该标签。
  if grep -rn 'binding:"[^"]*validateFn' --include='*.go' internal cmd >/dev/null 2>&1; then
    echo "检测到 binding:\"validateFn\" 标签：与 validator_novalidatefn 构建标签冲突，请改用显式校验。" >&2
    exit 1
  fi

  # 体积预算用于守住 validator_novalidatefn 等瘦身成果；超出时请先看 make size。
  # 默认构建内置全部 lego v5 DNS provider，体积约 80 MiB；32 MiB 预算只适用于
  # `make backend-no-dns` 这类裁剪变体，可用 BINARY_SIZE_BUDGET_MIB 覆盖。
  size_bytes=$(wc -c < bin/lactssl)
  budget_mib=${BINARY_SIZE_BUDGET_MIB:-96}
  budget_bytes=$((budget_mib * 1024 * 1024))
  size_mib=$(awk -v b="$size_bytes" 'BEGIN { printf "%.2f", b / 1048576 }')
  if [ "$size_bytes" -gt "$budget_bytes" ]; then
    over_mib=$(awk -v s="$size_bytes" -v b="$budget_bytes" 'BEGIN { printf "%.2f", (s - b) / 1048576 }')
    echo "二进制体积 ${size_mib} MiB 超过 ${budget_mib} MiB 预算（超出 ${over_mib} MiB）；请运行 make size 定位增长来源。" >&2
    exit 1
  fi
  echo "二进制体积 ${size_mib} MiB（预算 ${budget_mib} MiB）"

fi

trap - ERR
printf '\n%s✓ 本地 CI 全部通过（耗时 %ss）%s\n' "$GREEN" "$((SECONDS - start_time))" "$RESET"
