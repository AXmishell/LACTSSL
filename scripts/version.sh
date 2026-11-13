#!/usr/bin/env bash
# 输出版本号：优先 LACTSSL_VERSION，其次 git tag（git describe），最后 dev。
# 结果去掉开头的 v，便于直接注入 internal/api.Version 与前端 __LACTSSL_VERSION__。
# 例如：在 v1.1.0 之后有 1 个提交且工作区有改动时输出 1.1.0-1-g7a22b1e-dirty。
set -euo pipefail

if [ -n "${LACTSSL_VERSION:-}" ]; then
  printf '%s\n' "${LACTSSL_VERSION#v}"
  exit 0
fi

if version="$(git describe --tags --always --dirty 2>/dev/null)"; then
  printf '%s\n' "${version#v}"
  exit 0
fi

printf 'dev\n'
