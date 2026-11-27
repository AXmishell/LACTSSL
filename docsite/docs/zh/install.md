# 安装与启动

本文给出生产可用的安装步骤。第一次部署建议先看[快速开始](getting-started.md)；可直接按 Docker Compose 方式部署。

## 环境要求

| 项目 | 要求 |
| --- | --- |
| 操作系统 | Linux amd64 / arm64；也支持 macOS 和 Windows 的发布包 |
| 容器 | Docker 及 Docker Compose v2（推荐） |
| 主机资源 | 参考[部署架构](architecture.md)；起步建议 2 vCPU / 1 GB，个人单域名场景 1 vCPU / 1 GB 也可运行 |
| 源码构建 | Go 1.27+、Node.js 22.12+、npm |
| 网络与域名 | 管理入口经 HTTPS 反向代理；出站可访问 DNS API、CA、SMTP、部署目标；需一个管理域名和对应证书，ACME 服务端可选独立域名 |

!!! tip "个人 / 小团队"
    主程序不需要外部数据库、Redis 或消息队列，状态都在 `data/` 目录中。按 Docker Compose 启动、完成初始化并启用内置插件即可。面板建站使用对应部署目标；自动续期依赖 DNS API，建议使用只授权所需域名的专用子账号。启用条目会有瞬时内存峰值，不要把容器内存限制到刚好等于空闲占用。

## 方式一：Docker Compose（推荐）

### 准备目录

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

### 启动

```bash
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```

!!! note "镜像与网络"
    GHCR 镜像从首个正式 Release 起可用；`docker compose pull` 提示镜像不存在时请按 2.4 节本地构建。Compose 默认只把 8080 发布到 `127.0.0.1`；如需局域网访问请显式修改 `ports`，并在前面部署 TLS 反向代理。镜像默认使用 `latest`，如需锁定版本请修改 `docker-compose.yml` 中的镜像 tag。

默认挂载 `./config/config.yaml`（配置）与 `./data`（SQLite、插件、日志和升级备份）。容器内固定读取 `/app/config/config.yaml`，数据目录为 `/app/data`；入口脚本以 root 修正挂载目录归属后降权到 `lactssl` 运行。Compose 默认启用只读根文件系统、`no-new-privileges`、capabilities 白名单和 `pids_limit`。

### 固定版本（可选）

默认使用 `latest`，生产环境建议锁定到具体版本，直接修改 `docker-compose.yml` 中的镜像标签：

```yaml
image: ghcr.io/axmishell/lactssl:1.2.3
```

稳定标签包括 `<version>`、`<major>.<minor>`、`<major>` 和 `latest`；预发布版本只推送完整版本标签。

### 本地构建镜像

Dockerfile 只复制外部构建好的 Linux 二进制，不在镜像内编译前端/后端；数据库备份/恢复由 Go 原生实现，不需要 `pg_dump`/`mysqldump`。先准备前端和当前架构的程序产物，再构建镜像：

```bash
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
docker run -d --name lactssl \
  -p 127.0.0.1:8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  --restart unless-stopped \
  lactssl:1.2.3
```

## 方式二：二进制 + systemd

### 安装

```bash
sudo useradd --system --home /opt/lactssl --shell /usr/sbin/nologin lactssl
sudo mkdir -p /opt/lactssl/data
sudo cp lactssl /opt/lactssl/
sudo cp config.yaml.example /opt/lactssl/config.yaml
sudo chown -R lactssl:lactssl /opt/lactssl
sudo chmod 600 /opt/lactssl/config.yaml
sudo chmod 700 /opt/lactssl/data
```

!!! note "原生数据库备份"
    PostgreSQL/MySQL 的逻辑备份与恢复由 LACTSSL 的 Go 代码直接完成，不依赖 `pg_dump`、`pg_restore`、`mysqldump` 或 `mysql` 客户端；SQLite 使用 `VACUUM INTO` 快照。

### systemd 单元

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

日志写入 `/opt/lactssl/data/logs/` 时上面的 `ReadWritePaths` 已覆盖，其他目录需额外放行。使用 80/443 等特权端口时，建议由反向代理转发，不要给二进制提权。

### 一键部署脚本

`scripts/install.sh` 从 GitHub Release 下载 Linux amd64/arm64 二进制，创建运行用户与数据目录，安装 systemd 服务并生成 Nginx 反向代理配置。默认不启用 TLS：二进制监听 `127.0.0.1:8080`，Nginx 监听 80 并转发到该端口。

```bash
sudo ./scripts/install.sh --port 8080 --domain ssl.example.com
```

也可以不克隆仓库，直接下载脚本执行（Linux 发布压缩包内也附带 `install.sh`）：

```bash
curl -fsSL https://raw.githubusercontent.com/AXmishell/LACTSSL/main/scripts/install.sh \
  | sudo bash -s -- --port 8080 --domain ssl.example.com
```

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
| `--release-public-key KEY` | base64 Ed25519 发布公钥，校验 `checksums.txt.sig`（也可用 `LACTSSL_RELEASE_PUBLIC_KEY`） |
| `--allow-unsigned` | 未提供签名公钥时仍允许安装（不推荐，仅限受控环境） |

```bash
sudo ./scripts/install.sh \
  --domain ssl.example.com \
  --port 8080 \
  --tls-cert /etc/nginx/certs/ssl.example.com.pem \
  --tls-key  /etc/nginx/certs/ssl.example.com.key
```

脚本会校验 Release 中的 `checksums.txt`，默认校验 `checksums.txt.sig` 的 Ed25519 签名：通过 `LACTSSL_RELEASE_PUBLIC_KEY`（与 `upgrade.program.public_key` 相同）或 `--release-public-key` 设置 base64 公钥，受控环境可加 `--allow-unsigned` 退回仅 SHA-256 模式。重复执行只替换二进制并重启，保留已有 `config.yaml` 和 `data/`；指定 `--port` 会同步更新 `server.addr`、`server.host`、`server.port`。未指定 `--domain` / `--tls-cert` / `--tls-key` 时沿用已有 Nginx 配置中的域名与证书，关闭 TLS 需显式使用 `--no-tls`。Nginx 未安装时脚本会尝试通过包管理器安装（`--no-install-deps` 可关闭），`--no-nginx` 表示不配置 Nginx。服务单元启用 `ProtectSystem=strict`，页面内自升级可能因目录只读不可用，升级请重新运行脚本；需写入本机 `/etc/nginx` 等目录时重复使用 `--read-write-path`，并确保 `lactssl` 用户有写权限。默认生成的关键配置：

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1", "::1"]
security:
  public_url: "http://ssl.example.com"
```

Nginx 配置写入 `/etc/nginx/conf.d/lactssl.conf`，包含 `client_max_body_size 256m` 和 600 秒读写超时。部署完成后通过门禁入口完成初始化。

### 从源码构建

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```

`make build` 先构建前端并复制到 `internal/webui/dist`，再编译嵌入了前端资源的 `bin/lactssl`；仅构建后端可用 `make backend`，但需自行准备前端产物。

## 首次初始化

启动日志会打印门禁入口和一次性初始化令牌：

```text
门禁入口: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
初始化令牌（仅本次启动有效）: ...
```

1. 通过门禁入口打开页面，按提示创建管理员账户；初始化成功后令牌立即失效。
2. 如使用备份恢复，在 `/install` 的高级功能中导入并携带初始化令牌。

初始化前可以在 `/install` 的「运行配置」中修改常用白名单字段：监听地址与可信反向代理、`public_url` 与会话保持、ACME（独立监听、对外地址、TLS/mTLS）、日志（轮转、访问日志、系统日志）、升级与程序自升级、DNS/部署/通知插件开关、沙箱与资源限制；保存后需要重启服务生效。`jwt_secret`、`salt`、门禁入口、数据目录、数据库与日志文件、插件目录、程序仓库与签名公钥等敏感或高风险字段不会在页面中暴露，请直接在服务器上编辑 `config.yaml`；门禁入口可在初始化并登录后通过「设置 → 门禁入口」修改。

> 门禁入口不是安全边界。生产必须使用 HTTPS、强密码、TOTP/通行密钥和网络访问控制。

## 反向代理与 HTTPS

主程序只监听 HTTP，生产由 Nginx、Caddy、Traefik、负载均衡或 WAF 终止 TLS。

### 必填配置

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

security:
  public_url: "https://ssl.example.com"
```

- `trusted_proxies` 必须包含代理地址，否则客户端 IP 识别、限流和 `Secure` Cookie 判断会失真；只填写精确 IP，确需网段时使用最小 CIDR，不建议 `10.0.0.0/8` 这类大范围。
- `public_url` 用于密码重置等链接，不能依赖请求 Host；TLS 在代理终止时，确认代理传递 `X-Forwarded-Proto: https`，并在代理层配置来源 IP 白名单、VPN 或 WAF。

### Nginx 示例

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
        # 覆盖模式：丢弃客户端自带的 XFF，只保留真实客户端地址
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

这是推荐的**覆盖模式**：LACTSSL 只信任这台 Nginx 即可。若改用 `$proxy_add_x_forwarded_for`（追加模式），单层部署仍然正确；多级代理时 `trusted_proxies` 必须包含链上所有会出现在 XFF 的代理，否则客户端 IP 会停在上游代理。CDN、Docker/Kubernetes 和 L4 负载均衡的差异见[配置说明](configuration.md)的反向代理要点。建议在代理层再加来源 IP 白名单、VPN、零信任访问策略或 WAF。反向代理访问日志可作为独立审计来源，但不要记录 Cookie 和 `Authorization` 头。

### ACME 服务端（可选）

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

- `addr` 留空时 ACME 挂在主端口的 `/acme` 前缀，生产建议独立端口；`base_url` 必须是客户端实际访问地址，公网部署不要依赖请求 Host。
- EAB、域名白名单、mTLS 和来源限制见[安全基线](security.md)。

## 安装后的最小配置

### 账户安全

- 立即启用 TOTP 或添加通行密钥，并设置受控的恢复邮箱（邮箱本身必须启用多因素认证）。
- 明确管理员账户由固定负责人持有，变更时完成交接；在「设置 → 会话」中检查并撤销未知会话。

### 数据与备份

```bash
# 先停止写入再复制，或使用界面上的加密备份
tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

生产建议使用界面「系统设置 → 备份与恢复」生成加密 ZIP，并把备份存到独立介质；备份密码与 `config.yaml` 分开保管。

### 插件

官方 DNS、部署和通知实现已内置，不需要联网安装或上传：

1. 在「插件」页确认对应类别已启用。
2. 在列表中启用所需 DNS 服务商、部署目标或通知渠道。

DNS 服务商默认全部停用；部署类别默认开启；通知内置邮件并默认启用常用渠道。第三方 helper 仍可上传，见[插件治理](plugins.md)。

### 验证

```bash
# 进程与端口
curl -I http://127.0.0.1:8080/          # 未带门禁 Cookie 应为 404，属预期
ss -lntp | grep 8080

# 使用 API 密钥做认证检查
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/status
```

然后在界面完成：创建 DNS 账户并测试、创建一条测试任务并运行、发送测试通知、添加一个站点监控、下载并解密一次备份。

## 升级与卸载

升级前必须备份，升级步骤见[升级与变更](upgrade.md)。Docker：

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

## 常见安装问题

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| `docker compose up` 报外部网络不存在 | 默认依赖 `1panel-network` | 删除两处 `networks` 使用默认网络，或先创建该网络 |
| 访问根路径返回 404 | 未通过门禁入口 | 使用启动日志中的入口地址 |
| 上传备份/插件失败 | 代理请求体限制 | 调整 `client_max_body_size` |

Cookie 异常、密码重置链接、通行密钥不可用、容器重启回到旧版本等问题见[监控与排障](troubleshooting.md)。
