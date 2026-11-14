#!/bin/sh
# 以 root 启动时先修正数据/配置目录归属，再降权到 lactssl 用户运行主程序。
set -eu

DATA_DIR="${LACTSSL_DATA:-/app/data}"
CONFIG_FILE="${LACTSSL_CONFIG:-/app/config/config.yaml}"
CONFIG_DIR="$(dirname "$CONFIG_FILE")"

# 镜像内置官方部署插件；复制到数据目录后，启用 deploy_plugins 并重扫即可使用。
# 同名文件会被镜像版本覆盖，自定义 helper 请使用其他文件名。
install_deploy_helper() {
  [ -f /app/lactssl-deploy-helper ] || return 0
  mkdir -p "$DATA_DIR/plugins/deploy" 2>/dev/null || return 0
  cp /app/lactssl-deploy-helper "$DATA_DIR/plugins/deploy/deploy-helper" 2>/dev/null || return 0
  chmod 0755 "$DATA_DIR/plugins/deploy/deploy-helper" 2>/dev/null || true
}

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR" "$CONFIG_DIR"
  install_deploy_helper
  chown -R lactssl:lactssl "$DATA_DIR" "$CONFIG_DIR"
  exec su-exec lactssl:lactssl /app/lactssl "$@"
fi

install_deploy_helper
exec /app/lactssl "$@"
