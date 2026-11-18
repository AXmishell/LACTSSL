#!/bin/sh
# 以 root 启动时先修正数据/配置目录归属，再降权到 lactssl 用户运行主程序。
set -eu

DATA_DIR="/app/data"
CONFIG_FILE="/app/config/config.yaml"
CONFIG_DIR="/app/config"
TMP_DIR="$DATA_DIR/tmp"

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR" "$CONFIG_DIR" "$TMP_DIR"
  chown -R lactssl:lactssl "$DATA_DIR" "$CONFIG_DIR"
  exec su-exec lactssl:lactssl /app/lactssl -config "$CONFIG_FILE" "$@"
fi

# 非 root 启动时数据卷应由管理员预先授权；tmp 目录创建失败不应阻塞启动。
mkdir -p "$TMP_DIR" 2>/dev/null || true
exec /app/lactssl -config "$CONFIG_FILE" "$@"
