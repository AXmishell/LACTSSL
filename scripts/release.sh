#!/usr/bin/env bash
# 本地运行与 .github/workflows/release.yml 等价的发布流程。
# 默认 dry-run：构建全部平台的产物、生成 checksums.txt，但不推送镜像、不上传 GitHub Release。
# 用法: ./scripts/release.sh [tag] [选项]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

RUN_WEB=1
RUN_BUILD=1
RUN_DOCKER=1
RUN_PUBLISH=1
MULTIARCH=0
UPLOAD=0
TAG_ARG=""

usage() {
  cat <<'EOF'
用法: ./scripts/release.sh [tag] [选项]

参数:
  tag               要模拟发布的 tag，例如 v1.0.1；省略时读取 VERSION 并加 v 前缀

选项:
  --no-web          跳过前端构建（复用已有 web/dist）
  --no-build        跳过交叉编译、打包与发布（可配合 --docker-only）
  --no-docker       跳过 Docker 镜像
  --docker-only     只做 Docker 镜像，跳过前端与打包（用于验证镜像标签/多架构）
  --publish-only    只跑 publish，复用已有 dist/（用于验证上传资产列表）
  --multiarch       用 buildx 构建 linux/amd64 + linux/arm64，导出 OCI 镜像到 dist/（不推送）
  --upload          构建完成后用 gh 把 dist/* 上传到同名 GitHub Release（会覆盖同名资产）
  -h, --help        显示帮助

说明:
  - 与 release.yml 的 jobs 一一对应：meta → web → build → checksums → docker → publish
  - 产物写入 ./dist，可直接检查包内容与校验和
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-web) RUN_WEB=0 ;;
    --no-build)
      RUN_BUILD=0
      RUN_DOCKER=0
      RUN_PUBLISH=0
      ;;
    --docker-only)
      RUN_WEB=0
      RUN_BUILD=0
      RUN_DOCKER=1
      RUN_PUBLISH=0
      ;;
    --publish-only)
      RUN_WEB=0
      RUN_BUILD=0
      RUN_DOCKER=0
      RUN_PUBLISH=1
      ;;
    --no-docker) RUN_DOCKER=0 ;;
    --multiarch) MULTIARCH=1 ;;
    --upload) UPLOAD=1 ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      echo "未知参数: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      if [ -n "$TAG_ARG" ]; then
        echo "只能指定一个 tag" >&2
        exit 2
      fi
      TAG_ARG="$1"
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
  YELLOW=$'\033[1;33m'
  RED=$'\033[1;31m'
  RESET=$'\033[0m'
else
  BOLD='' BLUE='' GREEN='' YELLOW='' RED='' RESET=''
fi

step() {
  printf '\n%s==> %s%s\n' "$BLUE" "$*" "$RESET"
}

step_failed() {
  printf '\n%s✗ %s%s\n' "$RED" "$*" "$RESET" >&2
}

trap 'step_failed "本地 release 流程失败，请查看上方日志"' ERR

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少命令: $1" >&2
    exit 1
  fi
}

start_time=$SECONDS
DIST="$ROOT/dist"
STAGE_ROOT="$ROOT/.release-work"

# ---------- meta: 解析并校验 tag ----------
step "meta: 解析 tag"
tag="$TAG_ARG"
if [ -z "$tag" ]; then
  require go
  tag="v$(tr -d '[:space:]' < VERSION)"
fi

version="${tag#v}"
if ! printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'; then
  echo "::error::tag '$tag' 不是合法的语义化版本，期望格式为 vX.Y.Z（可带 -rc.1 等后缀）" >&2
  exit 1
fi

prerelease=false
if printf '%s' "$version" | grep -q -- '-'; then
  prerelease=true
fi

remote_url="$(git config --get remote.origin.url 2>/dev/null || true)"
repo="${remote_url%.git}"
repo="${repo#*github.com[:/]}"
if [ -z "$repo" ] || [ "$repo" = "$remote_url" ]; then
  repo="owner/repo"
fi
image="ghcr.io/${repo,,}"

echo "本次发布: tag=$tag, version=$version, prerelease=$prerelease, image=$image"
if [ -n "$(git status --porcelain 2>/dev/null || true)" ]; then
  printf '%s提示: 工作区有未提交改动。CI 中 checkout 的是 tag，本地构建的是当前工作区，产物可能包含未提交代码。%s\n' "$YELLOW" "$RESET"
fi

rm -rf "$STAGE_ROOT"
mkdir -p "$DIST" "$STAGE_ROOT"

# ---------- web: 前端构建 ----------
if [ "$RUN_WEB" -eq 1 ]; then
  require node
  require npm
  step "web: npm ci"
  (cd web && npm ci)

  step "web: npm run build"
  (cd web && npm run build)
else
  step "web: 跳过（--no-web）"
fi

if [ "$RUN_BUILD" -eq 1 ] && [ ! -f web/dist/index.html ]; then
  echo "web/dist 不存在，无法继续；请去掉 --no-web 或先构建前端" >&2
  exit 1
fi

# ---------- build + package: 交叉编译矩阵 ----------
TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "freebsd amd64"
  "freebsd arm64"
  "windows amd64"
)

make_zip() {
  # zip 语义等价于 (cd stage && zip -q -r ../dist/<name>.zip .)
  local src="$1" out="$2"
  if command -v zip >/dev/null 2>&1; then
    (cd "$src" && zip -q -r "$out" .)
  else
    python3 - "$src" "$out" <<'PY'
import os, sys, zipfile
src, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for base, _, files in os.walk(src):
        for name in sorted(files):
            full = os.path.join(base, name)
            z.write(full, os.path.relpath(full, src))
PY
  fi
}

make_tgz() {
  # 语义等价于 tar -czf dist/<name>.tar.gz -C stage .
  local src="$1" out="$2"
  tar -czf "$out" -C "$src" .
}

if [ "$RUN_BUILD" -eq 1 ]; then
  require go

  step "build: 清理旧的 dist/ 与打包临时目录"
  rm -rf "$DIST" "$STAGE_ROOT"
  mkdir -p "$DIST" "$STAGE_ROOT"

  step "build: 准备嵌入用的前端资源 (internal/webui/dist)"
  rm -rf internal/webui/dist
  cp -a web/dist internal/webui/dist

  for target in "${TARGETS[@]}"; do
    read -r goos goarch <<<"$target"
    ext=""
    [ "$goos" = "windows" ] && ext=".exe"

    step "build: $goos/$goarch"
    build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    mkdir -p "out/$goos-$goarch"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -tags embed \
      -ldflags "-s -w -X 'lactssl/internal/api.Version=${version}' -X 'lactssl/internal/api.BuildTime=${build_time}' -X 'lactssl/internal/api.BuildArch=${goos}/${goarch}'" \
      -o "out/$goos-$goarch/lactssl${ext}" ./cmd/server

    test -f "out/$goos-$goarch/lactssl${ext}"
    ls -lh "out/$goos-$goarch/lactssl${ext}"

    step "package: $goos/$goarch"
    name="lactssl_${version}_${goos}_${goarch}"
    stage="$STAGE_ROOT/$name"
    mkdir -p "$stage"
    cp README.md LICENSE config.yaml.example "$stage/"

    if [ "$goos" = "windows" ]; then
      cp "out/$goos-$goarch/lactssl.exe" "$stage/"
      make_zip "$stage" "$DIST/${name}.zip"
    else
      cp "out/$goos-$goarch/lactssl" "$stage/"
      make_tgz "$stage" "$DIST/${name}.tar.gz"
    fi
  done

  # ---------- checksums ----------
  step "checksums: 生成 checksums.txt"
  (cd "$DIST" && shopt -s nullglob && sha256sum *.tar.gz *.zip > checksums.txt && cat checksums.txt)

  step "校验产物"
  ls -lh "$DIST"
else
  step "build: 跳过（--no-build）"
fi

# ---------- docker ----------
if [ "$RUN_DOCKER" -eq 1 ]; then
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    # 与 metadata-action 的 semver 规则一致：预发布只打精确版本，稳定版追加 minor/major/latest。
    core="${version%%-*}"
    core="${core%%+*}"
    major="${core%%.*}"
    minor="$(printf '%s' "${core#*.}" | cut -d. -f1)"
    image_tags=("${image}:${version}")
    if [ "$prerelease" = "false" ]; then
      image_tags+=("${image}:${major}.${minor}")
      if [ "${major}" != "0" ]; then
        image_tags+=("${image}:${major}")
      fi
      image_tags+=("${image}:latest")
    fi

    tag_args=()
    for t in "${image_tags[@]}"; do
      tag_args+=(-t "$t")
    done

    # 构建机需要代理才能访问 Go/npm 仓库时，把宿主的代理环境透传给构建。
    proxy_args=()
    for v in HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
      if [ -n "${!v:-}" ]; then
        proxy_args+=(--build-arg "$v=${!v}")
      fi
    done

    if [ "$MULTIARCH" -eq 1 ]; then
      require docker
      step "docker: buildx 多架构构建 linux/amd64 + linux/arm64（构建到 dist/，不推送）"
      docker buildx build \
        --platform linux/amd64,linux/arm64 \
        --build-arg "VERSION=${version}" \
        "${proxy_args[@]}" \
        "${tag_args[@]}" \
        --output "type=oci,dest=${DIST}/lactssl_${version}_image.tar" \
        .
      step "docker: 校验多架构 manifest"
      python3 - "$DIST/lactssl_${version}_image.tar" <<'PY'
import json, sys, tarfile

def read_blob(t, digest):
    return json.load(t.extractfile(f"blobs/sha256/{digest.split(':', 1)[1]}"))

def platforms_of(t, entries):
    found = set()
    for entry in entries:
        node = entry
        if entry.get("mediaType", "").endswith("image.index.v1+json"):
            node = read_blob(t, entry["digest"])
        for manifest in node.get("manifests", [entry]):
            platform = manifest.get("platform") or {}
            if platform.get("os") not in (None, "unknown"):
                found.add(f"{platform['os']}/{platform.get('architecture')}")
    return found

with tarfile.open(sys.argv[1]) as t:
    index = json.load(t.extractfile("index.json"))
    platforms = sorted(platforms_of(t, index.get("manifests", [])))

tags = sorted({
    m.get("annotations", {}).get("org.opencontainers.image.ref.name")
    for m in index.get("manifests", [])
} - {None})
print("tags:", ", ".join(tags))
print("platforms:", ", ".join(platforms))
missing = {"linux/amd64", "linux/arm64"} - set(platforms)
if missing:
    sys.exit(f"缺少平台: {sorted(missing)}")
PY
      ls -lh "${DIST}/lactssl_${version}_image.tar"
    else
      step "docker: 本地单架构构建（不推送；加 --multiarch 可验证双架构）"
      docker build \
        --build-arg "VERSION=${version}" \
        "${proxy_args[@]}" \
        "${tag_args[@]}" \
        .
    fi

    printf '镜像标签：\n'
    printf '  %s\n' "${image_tags[@]}"
    printf '提示: CI 会把上述标签推送到 GHCR（%s）\n' "$image"
  else
    step "docker: 本机没有可用的 docker，跳过（CI 中会推送 ${image}）"
  fi
else
  step "docker: 跳过（--no-docker / --no-build）"
fi

# ---------- publish ----------
if [ "$RUN_PUBLISH" -eq 1 ]; then
  step "publish: GitHub Release 资产"
  shopt -s nullglob
  assets=("$DIST"/*.tar.gz "$DIST"/*.zip "$DIST/checksums.txt")
  if [ "${#assets[@]}" -eq 0 ]; then
    if [ "$UPLOAD" -eq 1 ]; then
      echo "::error::dist/ 中没有可分发的资产，请先执行构建" >&2
      exit 1
    fi
    echo "dist/ 中没有可分发的资产，跳过 publish"
  elif [ "$UPLOAD" -eq 1 ]; then
    require gh
    if ! gh release view "$tag" >/dev/null 2>&1; then
      echo "::error::找不到 tag '$tag' 对应的 release，请先创建 release" >&2
      exit 1
    fi
    gh release upload "$tag" "${assets[@]}" --clobber
    gh release view "$tag" --json url,assets \
      --jq '"release: " + .url, "assets:", (.assets[] | " - " + .name + " (" + (.size | tostring) + " bytes)")'
  else
    echo "dry-run：以下资产将在 CI 中上传到 release $tag"
    printf '%s\n' "${assets[@]##*/}"
    echo "如需真正上传，重新运行并加 --upload"
  fi
else
  step "publish: 跳过（--no-build / --docker-only）"
fi

trap - ERR
printf '\n%s✓ 本地 release 流程完成（tag=%s，耗时 %ss）%s\n' "$GREEN" "$tag" "$((SECONDS - start_time))" "$RESET"
