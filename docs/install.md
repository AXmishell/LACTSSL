# 安装与启动

本文给出生产可用的安装步骤。第一次评估建议先看[快速上手与上线](getting-started.md)，确定场景和验收标准后再按本文部署。个人站点和小团队可以跳过评估，直接按第 2 节用 Docker 部署。

## 1. 环境要求

| 项目 | 要求 |
| --- | --- |
| 操作系统 | Linux amd64 / arm64；也支持 macOS 和 Windows 的发布包 |
| 容器 | Docker 及 Docker Compose v2（推荐） |
| 源码构建 | Go 1.27+、Node.js 22.12+、npm |
| 主机资源 | 参考[部署架构](architecture.md)；起步建议 2 vCPU / 1 GB，个人单域名场景 1 vCPU / 1 GB 也可运行 |
| 网络 | 管理入口经 HTTPS 反向代理；出站可访问 DNS API、CA、SMTP、部署目标 |
| 域名 | 一个管理域名和对应 TLS 证书；ACME 服务端可选独立域名 |

主程序不需要外部数据库、Redis 或消息队列。所有状态都在 `data/` 目录中。

!!! tip "个人 / 小团队"
    不需要走企业评估流程：按第 2 节 Docker 启动、第 4 节完成初始化、第 6.3 节安装插件即可。宝塔、1Panel 等面板建站需要安装对应的部署插件；自动续期依赖 DNS API，建议使用只授权所需域名的专用子账号。插件安装/更新会有瞬时内存峰值，不要把容器内存限制到刚好等于空闲占用。

## 2. 方式一：Docker Compose（推荐）

### 2.1 准备目录

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
```

仓库自带的 `docker-compose.yml` 默认使用 GHCR 镜像：

```yaml title="docker-compose.yml"
--8<-- "docker-compose.yml"
```

默认文件沿用 1Panel 的 `1panel-network` 外部网络。不使用 1Panel 时，删除服务下的 `networks` 和文件末尾的 `networks` 段，让 Compose 使用默认网络；也可以先创建该网络：

```bash
docker network create 1panel-network
```

### 2.2 启动

```bash
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```

!!! note "镜像与网络"
    GHCR 镜像从首个正式 Release 起可用；如果 `docker compose pull` 提示镜像不存在，请按 2.4 节在本地构建。Compose 默认把 8080 发布到宿主机所有网卡；公网主机建议用防火墙限制来源，或把 `ports` 改为 `127.0.0.1:8080:8080`，只允许本机反向代理访问。

默认挂载：

- `./config/config.yaml`：配置文件
- `./data`：SQLite、插件、日志和升级备份

容器内固定读取 `/app/config/config.yaml`，数据目录为 `/app/data`。入口脚本会以 root 修正挂载目录归属，然后降权到 `lactssl` 用户运行。Compose 默认启用只读根文件系统、`no-new-privileges`、capabilities 白名单和 `pids_limit`。

### 2.3 固定版本

不要长期使用 `latest`。直接修改 `docker-compose.yml` 中的镜像标签：

```yaml
image: ghcr.io/axmishell/lactssl:1.2.3
```

稳定标签包括 `<version>`、`<major>.<minor>`、`<major>` 和 `latest`；预发布版本只推送完整版本标签。

### 2.4 本地构建镜像

先在 `docker-compose.yml` 中取消 `build:` 段的注释，再修改源码并构建：

```bash
LACTSSL_VERSION=1.2.3 docker compose up -d --build
```

`LACTSSL_VERSION` 只注入程序版本号，不会改变本地镜像标签；需要区分镜像时请同时修改 `image`。

或手动构建：

```bash
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
docker run -d --name lactssl \
  -p 127.0.0.1:8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  --restart unless-stopped \
  lactssl:1.2.3
```

## 3. 方式二：二进制 + systemd

### 3.1 安装

```bash
sudo useradd --system --home /opt/lactssl --shell /usr/sbin/nologin lactssl
sudo mkdir -p /opt/lactssl/data
sudo cp lactssl /opt/lactssl/
sudo cp config.yaml.example /opt/lactssl/config.yaml
sudo chown -R lactssl:lactssl /opt/lactssl
sudo chmod 600 /opt/lactssl/config.yaml
sudo chmod 700 /opt/lactssl/data
```

### 3.2 systemd 单元

```ini title="/etc/systemd/system/lactssl.service"
[Unit]
Description=LACTSSL certificate manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lactssl
Group=lactssl
WorkingDirectory=/opt/lactssl
ExecStart=/opt/lactssl/lactssl -config /opt/lactssl/config.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/lactssl/data

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lactssl
sudo journalctl -u lactssl -f
```

如果日志写入 `/opt/lactssl/data/logs/`，上面的 `ReadWritePaths` 已覆盖；如果写到其他目录，需要额外放行。使用 80/443 等特权端口时，建议由反向代理转发，而不是给二进制提权。

### 3.3 一键部署脚本

仓库提供 `scripts/install.sh`，用于从 GitHub Release 下载 Linux amd64/arm64 二进制，创建运行用户与数据目录，安装 systemd 服务，并生成 Nginx 反向代理配置。默认不启用 TLS：二进制监听 `127.0.0.1:8080`，Nginx 监听 80 并转发到该端口。

```bash
sudo ./scripts/install.sh --port 8080 --domain ssl.example.com
```

也可以不克隆仓库，直接下载脚本执行：

```bash
curl -fsSL https://raw.githubusercontent.com/AXmishell/LACTSSL/main/scripts/install.sh \
  | sudo bash -s -- --port 8080 --domain ssl.example.com
```

Linux 发布压缩包中也会附带 `install.sh`，解压后可直接运行。

常用参数：

| 参数 | 说明 |
| --- | --- |
| `--port PORT` | 二进制监听端口，默认 `8080`，必须 >= 1024 |
| `--domain DOMAIN` | Nginx `server_name`，例如 `ssl.example.com`；默认 `_` |
| `--version TAG` | 指定版本，例如 `v1.2.3`；默认最新稳定版 |
| `--no-nginx` | 只部署 systemd 服务，不安装或修改 Nginx |
| `--tls-cert` / `--tls-key` | 可选，启用 Nginx HTTPS 并配置 80 → 443 跳转 |
| `--no-tls` | 关闭已有 Nginx TLS 配置（默认本来就不启用 TLS） |
| `--read-write-path DIR` | 额外允许服务写入的目录，可重复；用于本机证书部署目标 |
| `--skip-checksum` | 跳过 SHA-256 校验（不推荐） |

示例：启用 HTTPS

```bash
sudo ./scripts/install.sh \
  --domain ssl.example.com \
  --port 8080 \
  --tls-cert /etc/nginx/certs/ssl.example.com.pem \
  --tls-key  /etc/nginx/certs/ssl.example.com.key
```

脚本会校验 Release 中的 `checksums.txt`。重复执行时只替换二进制并重启服务，已有 `config.yaml` 和 `data/` 会保留；指定 `--port` 时会同步更新 `server.addr`、`server.host`、`server.port`。未指定 `--domain` / `--tls-cert` / `--tls-key` 时，会沿用已有 Nginx 配置中的域名与证书，避免升级时意外退回默认站点或关闭 TLS；如需关闭 TLS 请显式使用 `--no-tls`。如果 Nginx 未安装，脚本会尝试通过系统的包管理器安装；不希望自动安装时可加 `--no-install-deps`，不配置 Nginx 时可加 `--no-nginx`。服务单元启用了 `ProtectSystem=strict`，程序页面中的自升级可能因目录只读而不可用；升级请重新运行本脚本。如需让内置部署目标写入本机 `/etc/nginx` 等目录，可重复使用 `--read-write-path` 放行，并确保 `lactssl` 用户对这些目录有写权限。

默认生成的关键配置：

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1", "::1"]
security:
  public_url: "http://ssl.example.com"
```

Nginx 配置写入 `/etc/nginx/conf.d/lactssl.conf`，包含 `client_max_body_size 256m` 和 600 秒读写超时。部署完成后按第 4 节通过启动日志中的门禁入口完成初始化。

### 3.4 从源码构建

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```

`make build` 先构建前端并复制到 `internal/webui/dist`，再编译嵌入了前端资源的 `bin/lactssl`。仅构建后端可使用 `make backend`，但需要自行准备前端产物。

## 4. 首次初始化

启动日志会打印门禁入口和一次性初始化令牌：

```text
门禁入口: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
初始化令牌（仅本次启动有效）: ...
```

1. 通过门禁入口打开页面。
2. 按提示创建管理员账户。
3. 如使用备份恢复，在 `/install` 的高级功能中导入，并携带初始化令牌。
4. 初始化成功后令牌立即失效。

初始化前可以在 `/install` 的「运行配置」中修改常用白名单字段：监听地址与可信反向代理、`public_url` 与会话保持、ACME（独立监听、对外地址、TLS/mTLS）、日志（轮转、访问日志、系统日志）、升级与程序自升级、DNS/部署/通知插件开关、插件仓库及资源限制。保存后需要重启服务生效。

`jwt_secret`、`salt`、门禁入口、数据目录、数据库与日志文件、插件目录、程序仓库与签名公钥等敏感或高风险字段不会在页面中暴露，请直接在服务器上编辑 `config.yaml`。门禁入口可在初始化并登录后通过「设置 → 门禁入口」修改。

> 门禁入口不是安全边界。生产必须使用 HTTPS、强密码、TOTP/通行密钥和网络访问控制。

## 5. 反向代理与 HTTPS

主程序只监听 HTTP，生产由 Nginx、Caddy、Traefik、负载均衡或 WAF 终止 TLS。

### 5.1 必填配置

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1", "10.0.0.0/8"]

security:
  public_url: "https://ssl.example.com"
```

- `trusted_proxies` 必须包含代理地址，否则客户端 IP 识别和限流会失真，`Secure` Cookie 判断也可能错误。
- `public_url` 用于密码重置等链接，不能依赖请求 Host。
- 如果 TLS 在代理终止，确认代理传递 `X-Forwarded-Proto: https`。

### 5.2 Nginx 示例

```nginx
server {
    listen 443 ssl;
    server_name ssl.example.com;

    ssl_certificate     /etc/nginx/certs/ssl.example.com.pem;
    ssl_certificate_key /etc/nginx/certs/ssl.example.com.key;
    ssl_protocols       TLSv1.2 TLSv1.3;

    client_max_body_size 256m;  # 备份恢复和插件上传

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

建议在代理层再加来源 IP 白名单、VPN、零信任访问策略或 WAF。反向代理的访问日志可作为独立审计来源，但不要记录 Cookie 和 `Authorization` 头。

### 5.3 ACME 服务端（可选）

如果需要向内部客户端提供 ACME：

```yaml
acme:
  enabled: true
  addr: ":14000"
  base_url: "https://pki.example.com:14000"
  tls:
    cert: "/etc/lactssl/acme.crt"
    key: "/etc/lactssl/acme.key"
```

- `addr` 留空时 ACME 挂在主端口的 `/acme` 前缀；生产建议独立端口。
- `base_url` 必须是客户端实际访问地址，公网部署不要依赖请求 Host。
- EAB、域名白名单、mTLS 和来源限制见[安全基线](security.md)。

## 6. 安装后的最小配置

### 6.1 账户安全

- 立即启用 TOTP 或添加通行密钥。
- 设置受控的恢复邮箱；邮箱本身必须启用多因素认证。
- 明确管理员账户由岗位持有，离职/转岗时完成交接。
- 在「设置 → 会话」中检查并撤销未知会话。

### 6.2 数据与备份

```bash
# 先停止写入再复制，或使用界面上的加密备份
tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

生产建议使用界面「系统设置 → 备份与恢复」生成加密 ZIP，并把备份存到独立介质。备份密码与 `config.yaml` 分开保管。

### 6.3 插件

主程序不内置任何 DNS / 部署 / 通知插件：

1. 进入「插件」页，确认仓库地址，点击「刷新索引」。
2. 在「插件市场」安装所需插件。
3. 在对应列表中启用服务商、部署目标或通知渠道。
4. 生产环境建议使用内部镜像并固定版本，见[插件治理](plugins.md)。

未安装插件时：

- DNS 服务商列表为空，无法创建 DNS 账户。
- 部署目标只有本地目录和 SSH。
- 通知只有邮件。

### 6.4 验证

```bash
# 进程与端口
curl -I http://127.0.0.1:8080/          # 未带门禁 Cookie 应为 404，属预期
ss -lntp | grep 8080

# 使用 API 密钥做认证检查
curl -H "Authorization: Bearer lactssl_xxx" \
  https://ssl.example.com/api/status
```

然后在界面完成：

- 创建 DNS 账户并测试。
- 创建一条测试任务并运行。
- 发送测试通知。
- 添加一个站点监控。
- 下载并解密一次备份。

## 7. 升级与卸载

升级前必须备份，升级步骤见[升级与变更](upgrade.md)。

Docker：

```bash
docker compose pull
docker compose up -d
```

二进制：

```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo systemctl start lactssl
```

卸载前先导出备份。删除 `data/` 会永久丢失数据库、插件和加密凭据；删除 `config.yaml` 会丢失 `salt` 和 `jwt_secret`。

## 8. 常见安装问题

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| `docker compose up` 报外部网络不存在 | 默认依赖 `1panel-network` | 删除两处 `networks` 使用默认网络，或先创建该网络 |
| 访问根路径返回 404 | 未通过门禁入口 | 使用启动日志中的入口地址 |
| 登录后 Cookie 异常/限流不准 | 未配置可信代理 | 配置 `trusted_proxies` 并传递转发头 |
| 密码重置链接指向错误域名 | 未配置 `public_url` | 设置固定对外地址 |
| 上传备份/插件失败 | 代理请求体限制 | 调整 `client_max_body_size` |
| 通行密钥不可用 | 非 HTTPS 或使用 IP 访问 | 使用 HTTPS 域名或 localhost |
| 容器重启后回到旧版本 | 在容器内自升级 | 通过镜像标签升级 |

更多排障见[监控与排障](troubleshooting.md)。
