#!/bin/sh
# 以 root 启动时先修正数据/配置目录归属，再把运行用户交给主程序。
# 主程序在初始化阶段通过 setgid/setuid 降权，镜像内因此不再需要 su-exec。
set -eu

DATA_DIR="/app/data"
CONFIG_FILE="/app/config/config.yaml"
CONFIG_DIR="/app/config"
TMP_DIR="$DATA_DIR/tmp"

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR" "$CONFIG_DIR" "$TMP_DIR"
  chown -R 10001:10001 "$DATA_DIR" "$CONFIG_DIR"
  export LACTSSL_RUN_UID=10001
  export LACTSSL_RUN_GID=10001
fi

# 非 root 启动时数据卷应由管理员预先授权；tmp 目录创建失败不应阻塞启动。
mkdir -p "$TMP_DIR" 2>/dev/null || true
exec /app/lactssl -config "$CONFIG_FILE" "$@"
