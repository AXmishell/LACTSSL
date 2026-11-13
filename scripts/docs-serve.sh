#!/usr/bin/env bash
# 构建中英文文档站点并启动静态预览服务器。
#
# `zensical serve` 一次只能构建一种配置，而且会清理 site 目录，
# 因此双语预览需要先分别构建，再用静态服务器托管合并后的 site。
# 用法: ./scripts/docs-serve.sh [端口]，默认 8000。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PORT="${1:-${DOCS_PORT:-8000}}"

zensical build --strict --clean
zensical build -f mkdocs.zh.yml --strict

echo "Serving docs at http://localhost:${PORT}/ (English) and http://localhost:${PORT}/zh/ (Chinese)"
exec python3 -m http.server "$PORT" --directory site
