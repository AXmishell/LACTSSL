#!/usr/bin/env bash
# 解析开发环境使用的门禁入口，供 dev.sh source。
# 优先级：config.yaml 的 security.entry > 数据库中的持久化入口。
# 首次启动后入口只保存在数据库里，这里读取它，供 Vite 模拟页面门禁；
# 接口默认挂在裸 /api 下，旧版 /<entry>/api 仍作为兼容别名。

# entry_from_db 从 SQLite 的 settings 表读取 security.entry。
entry_from_db() {
  local config_path="$1"
  local data_dir db
  data_dir=$(awk '/^[[:space:]]*data_dir:/{gsub(/["'\'']/,"",$2); print $2; exit}' "$config_path")
  db=$(awk '/^[[:space:]]*path:/{gsub(/["'\'']/,"",$2); print $2; exit}' "$config_path")
  [ -n "$db" ] || db="${data_dir:-data}/lactssl.db"
  [ -f "$db" ] || return 0

  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "$db" "SELECT value FROM settings WHERE key='security.entry' LIMIT 1;" 2>/dev/null || true
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$db" <<'PY' 2>/dev/null || true
import sqlite3, sys
try:
    con = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
    row = con.execute("SELECT value FROM settings WHERE key='security.entry' LIMIT 1").fetchone()
    print(row[0] if row and row[0] else "")
except Exception:
    pass
PY
  fi
}

# resolve_entry 返回规范化后的入口（去掉首尾斜杠）；可能为空。
resolve_entry() {
  local config_path="$1"
  local entry
  entry=$(awk '/^[[:space:]]*entry:/{gsub(/["'\'']/,"",$2); print $2; exit}' "$config_path")
  entry="${entry#/}"
  entry="${entry%/}"
  if [ -z "$entry" ]; then
    entry=$(entry_from_db "$config_path")
  fi
  printf '%s' "$entry"
}
