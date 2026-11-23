#!/usr/bin/env bash
# 分析生产二进制的体积构成：段大小 + 按包聚合的符号大小。
#
# 用法: ./scripts/size-report.sh [--no-build]
#   --no-build  复用 bin/lactssl，不重新构建（仅做符号分析，需未 strip 的版本）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BUILD=1
if [ "${1:-}" = "--no-build" ]; then
  BUILD=0
fi

TAGS="nomsgpack validator_novalidatefn"
if [ -d internal/webui/dist ] && [ -n "$(ls -A internal/webui/dist 2>/dev/null)" ]; then
  TAGS="embed $TAGS"
else
  echo "提示: internal/webui/dist 不存在，按未嵌入前端分析（先运行 make web 可得到发布形态）。" >&2
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/lactssl-size.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

if [ "$BUILD" -eq 1 ]; then
  echo "==> 构建生产二进制（-s -w）"
  CGO_ENABLED=0 go build -trimpath -tags "$TAGS" -ldflags "-s -w" -o "$TMP/stripped" ./cmd/server

  echo "==> 构建带符号二进制用于分析"
  CGO_ENABLED=0 go build -trimpath -tags "$TAGS" -o "$TMP/sym" ./cmd/server
else
  cp bin/lactssl "$TMP/stripped"
  cp bin/lactssl "$TMP/sym"
fi

stripped_bytes=$(wc -c < "$TMP/stripped")
printf '\n==> 发布二进制: %s 字节 (%.2f MiB)\n' "$stripped_bytes" \
  "$(awk -v b="$stripped_bytes" 'BEGIN { printf "%.2f", b / 1048576 }')"

if command -v size >/dev/null 2>&1; then
  echo
  echo "==> 段大小"
  size -A "$TMP/sym" | awk 'NR == 1 { next } $1 == "Total" || $1 ~ /bss$/ { next } $2 ~ /^[0-9]+$/ && $1 !~ /^\.debug/ { printf "%12d  %s\n", $2, $1 }' | sort -rn | head -12
fi

echo
echo "==> 按包聚合（不含 BSS；仅统计代码/只读数据/已初始化数据）"
python3 - "$TMP/sym" "$stripped_bytes" <<'PY'
import collections
import subprocess
import sys

path, stripped = sys.argv[1], int(sys.argv[2])
out = subprocess.run(
    ["go", "tool", "nm", "-size", path],
    capture_output=True, text=True, check=True,
).stdout

per_pkg = collections.Counter()
accounted = 0
SKIP_PREFIX = ("type:", "go:", "runtime.gcbits", "runtime.pclntab", "runtime.epclntab",
               "runtime.text", "runtime.etext", "runtime.rodata", "runtime.erodata",
               "runtime.data", "runtime.edata", "runtime.bss", "runtime.ebss")

for line in out.splitlines():
    fields = line.split()
    if len(fields) < 4:
        continue
    try:
        size = int(fields[1])
    except ValueError:
        continue
    kind, sym = fields[2], fields[3]
    # BSS 不占文件体积；调试段不以 nm 符号形式出现。
    if kind in ("b", "B"):
        continue
    if sym.startswith(SKIP_PREFIX):
        continue
    accounted += size
    slash = sym.rfind("/")
    dot = sym.find(".", slash + 1 if slash >= 0 else 0)
    pkg = sym[:dot] if dot > 0 else sym
    per_pkg[pkg] += size

print(f"已归属符号合计: {accounted / 1048576:.2f} MiB (占发布二进制 {accounted * 100 / stripped:.1f}%)")
print(f"{'MiB':>8}  {'占比':>6}  包")
for pkg, size in per_pkg.most_common(25):
    print(f"{size / 1048576:8.2f}  {size * 100 / stripped:5.1f}%  {pkg}")
PY

echo
echo "提示: Go 的 .gopclntab 与 .go.type 约占发布体积的 1/3，属于运行时必需元数据，"
echo "      常规 -ldflags '-s -w' 无法移除；优化重点应放在减少链接进来的代码量。"
