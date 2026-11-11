#!/bin/sh
# 以 root 启动时先修正数据/配置目录归属，再降权到 lactssl 用户运行主程序。
set -eu

DATA_DIR="${LACTSSL_DATA:-/app/data}"
CONFIG_FILE="${LACTSSL_CONFIG:-/app/config/config.yaml}"
CONFIG_DIR="$(dirname "$CONFIG_FILE")"

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR" "$CONFIG_DIR"
  chown -R lactssl:lactssl "$DATA_DIR" "$CONFIG_DIR"
  exec su-exec lactssl:lactssl /app/lactssl "$@"
fi

exec /app/lactssl "$@"
