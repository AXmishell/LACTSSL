# 安装与启动

## 环境要求

- Go 1.27+
- Node.js 22+ 与 npm
- 使用 Docker 时：Docker 及 Docker Compose v2（可选）

## 源码构建

```bash
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```

`make build` 会先构建前端（产物复制到 `internal/webui/dist`），再编译嵌入了前端资源的 `bin/lactssl`。

`config.yaml` 不存在时，服务首次启动会自动生成一份带随机密钥的配置；手动复制示例文件可以保留完整注释。

## 首次访问

启动日志会打印安全入口地址，例如：

```text
http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
```

通过该地址访问管理页面，按提示创建管理员账户。之后可在设置中启用 TOTP 两步验证或修改安全入口。

## 开发模式

```bash
make dev
```

开发模式同时启动 Vite（`:5173`）和 Go 服务（默认 `:8080`），Vite 会把 `/<entry>/api` 代理到 Go。请访问终端输出的 Vite 地址。

这里的代理是单向的：只有 Vite 把接口请求转发给 Go，**Go 的 `8080` 不会反向代理 `5173`**。`8080` 由二进制程序直接托管嵌入的前端资源（`internal/webui/dist`），开发和生产都是如此；生产运行时没有 `5173`，也不依赖 Vite。

若尚未生成配置，可通过环境变量指定开发用安全入口：

```bash
LACTSSL_ENTRY=my-dev-entry make dev
```

## Docker

使用 Compose：

```bash
docker compose up -d --build
```

默认挂载：

- `./config/config.yaml`：配置文件
- `./data`：SQLite 数据库

也可以手动构建运行：

```bash
docker build -t lactssl .
docker run -d --name lactssl \
  -p 8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  lactssl
```

容器内默认使用 `LACTSSL_DATA=/app/data`、`LACTSSL_CONFIG=/app/config/config.yaml`。入口脚本会以 root 修正挂载目录归属，然后降权到 `lactssl` 用户运行主程序；Compose 已丢弃全部 capabilities 并按需保留 `CHOWN`、`SETUID` 等。经过反向代理时，记得设置 `LACTSSL_TRUSTED_PROXIES` 和 `security.public_url`，详见[安全](security.md)。

### 使用 GHCR 镜像

发布 Release 时会同时构建 `linux/amd64` 与 `linux/arm64` 镜像并推送到 GitHub Container Registry：

```bash
docker pull ghcr.io/<owner>/lactssl:latest
docker run -d --name lactssl \
  -p 8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  ghcr.io/<owner>/lactssl:latest
```

稳定版标签包括 `latest`、`<version>`、`<major>.<minor>`、`<major>`，例如 `1.0.1`、`1.0`、`1`；预发布版本（如 `1.0.1-rc.1`）只推送完整版本标签，不会覆盖 `latest`。

首次发布后，GHCR 包默认是私有的。如需匿名拉取，请在仓库右侧 **Packages** 打开对应包，进入 **Package settings → Change visibility** 改为 Public。

## 下一步

- [配置说明](configuration.md)
- [功能说明](features.md)
