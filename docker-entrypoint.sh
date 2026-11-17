#!/bin/sh
# 以 root 启动时先修正数据/配置目录归属，再降权到 lactssl 用户运行主程序。
set -eu

DATA_DIR="/app/data"
CONFIG_FILE="/app/config/config.yaml"
CONFIG_DIR="/app/config"

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR" "$CONFIG_DIR"
  chown -R lactssl:lactssl "$DATA_DIR" "$CONFIG_DIR"
  exec su-exec lactssl:lactssl /app/lactssl -config "$CONFIG_FILE" "$@"
fi

exec /app/lactssl -config "$CONFIG_FILE" "$@"
