#!/usr/bin/env bash
# 检查业务层是否直接使用了方言相关 SQL。方言差异必须收敛在 internal/db。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

pattern='\binstr\(|substr\(created_at|DATE_FORMAT\(|to_char\(|PRAGMA|sqlite_master|AUTOINCREMENT|LastInsertId'
if matches=$(grep -rnE "$pattern" internal/store internal/api internal/service --include='*.go' | grep -v '_test.go'); then
  echo "以下业务代码使用了方言相关 SQL，请改用 internal/db 提供的方言封装：" >&2
  printf '%s\n' "$matches" >&2
  exit 1
fi

# MySQL 5.7 不允许把目标表放进 UPDATE/DELETE 的子查询。
if matches=$(grep -rnE 'DELETE FROM [a-z_]+.*IN \(SELECT|UPDATE [a-z_]+.*IN \(SELECT' internal/store internal/api internal/service --include='*.go' | grep -v '_test.go'); then
  echo "检测到修改语句内嵌 SELECT 子查询，MySQL 5.7 可能报 1093；请先查 ID 再分批修改：" >&2
  printf '%s\n' "$matches" >&2
  exit 1
fi

echo "SQL 方言检查通过"
