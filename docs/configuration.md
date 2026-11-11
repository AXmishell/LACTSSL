# 配置

## 配置文件

默认读取当前工作目录下的 `config.yaml`，可通过 `LACTSSL_CONFIG` 指定其他路径。

- 文件不存在时，首次启动会自动生成，并写入随机生成的 `jwt_secret` 与 `salt`。
- 修改配置后需要重启服务。
- 配置中包含密钥，建议保持 `0600` 权限。
- 优先级：**环境变量 > `config.yaml` > 内置默认值**。

完整带注释的示例见仓库根目录的 [`config.yaml.example`](../config.yaml.example)。

## 字段说明

```yaml
server:
  addr: ""              # 完整监听地址，如 ":8080"；非空时优先于 host + port
  host: ""              # 监听网卡，留空表示所有网卡
  port: 8080            # 监听端口
  trusted_proxies: []   # 可信反向代理 CIDR

data_dir: "data"        # 数据目录，存放 SQLite 数据库

database:
  path: ""              # 留空表示 <data_dir>/lactssl.db

security:
  entry: ""             # 安全入口，留空则首次启动随机生成
  public_url: ""        # 对外访问地址，如 "https://ssl.example.com"
  jwt_secret: ""        # JWT 签名密钥，留空自动生成并写回
  salt: ""              # 敏感字段加密盐，留空自动生成并写回

acme:
  enabled: false        # 是否启用内置 ACME 服务端
  addr: ""              # 独立监听地址，如 ":14000"；留空挂载到主端口 /acme
  base_url: ""          # 对外地址，如 "https://pki.example.com:14000"
  tls:
    cert: ""            # ACME 独立监听 TLS 证书
    key: ""             # ACME 独立监听 TLS 私钥
  mtls:
    enabled: false      # 是否要求客户端证书
    client_ca: ""       # 客户端 CA 证书路径
```

### `server`

- `addr`：完整监听地址，例如 `:8080`、`127.0.0.1:8080`。
- `host`、`port`：未设置 `addr` 时组合为监听地址。该端口由 Go 二进制直接提供页面与接口，前端资源已嵌入二进制，**不会反向代理 Vite 的 `5173`**；`5173` 仅在 `make dev` 时存在。
- `trusted_proxies`：可信反向代理的 IP/CIDR 列表。留空表示不信任任何代理头，适合直连部署；反向代理终止 TLS 时必须填写，否则 `Secure` Cookie 与客户端 IP 识别会不正确。

### `data_dir` 与 `database.path`

数据目录用于存放数据库。目录会以 `0700` 权限创建。`database.path` 留空时使用 `<data_dir>/lactssl.db`。

### `security`

- `entry`：管理页面的安全入口路径段。留空时首次启动随机生成 20 位大小写字母与数字组合，并保存到数据库。
- `public_url`：对外访问地址，例如 `https://ssl.example.com`（不要带路径）。密码重置邮件中的链接基于该地址生成，避免 Host 头注入；未配置时仅允许本机回环请求按 Host 推导。也可用环境变量 `LACTSSL_PUBLIC_URL` 覆盖。
- `jwt_secret`：登录令牌签名密钥。留空时自动生成 32 字节随机值并写回配置文件。
- `salt`：数据库中敏感字段（DNS/部署凭据、通知密钥等）的加密盐。留空时自动生成并写回。

### `acme`

- `enabled`：是否启用内置 ACME 服务端。默认关闭。
- `addr`：独立监听地址，例如 `:14000`。留空时 ACME 协议挂载到主端口的 `/acme` 前缀。
- `base_url`：客户端实际访问的 ACME 基础地址，用于生成 directory 和账户资源 URL。公网部署必须配置为固定地址，不要依赖请求 Host。
- `tls.cert` / `tls.key`：ACME 独立监听直接提供 TLS 时使用；反向代理终止 TLS 时留空。
- `mtls.enabled` / `mtls.client_ca`：要求客户端证书时启用，并指定客户端 CA 证书。启用 mTLS 时必须同时配置 ACME TLS 证书和私钥。

> **务必备份 `config.yaml`。** 丢失 `salt` 后，数据库中已加密的凭据无法解密；丢失 `jwt_secret` 会使现有登录会话失效。

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `LACTSSL_CONFIG` | 配置文件路径，默认 `config.yaml` |
| `LACTSSL_ADDR` | 完整监听地址，覆盖 `server.addr` |
| `LACTSSL_PORT` | 监听端口，覆盖 `server.port` |
| `LACTSSL_DATA` | 数据目录，覆盖 `data_dir` |
| `LACTSSL_ENTRY` | 安全入口，覆盖 `security.entry` 并写回数据库 |
| `LACTSSL_PUBLIC_URL` | 对外访问地址，覆盖 `security.public_url` |
| `LACTSSL_TRUSTED_PROXIES` | 逗号分隔的可信代理列表，覆盖 `server.trusted_proxies` |
| `LACTSSL_ACME_ENABLED` | 是否启用 ACME 服务端，覆盖 `acme.enabled` |
| `LACTSSL_ACME_ADDR` | ACME 独立监听地址，覆盖 `acme.addr` |
| `LACTSSL_ACME_BASE_URL` | ACME 对外地址，覆盖 `acme.base_url` |
| `LACTSSL_ACME_TLS_CERT` | ACME TLS 证书路径 |
| `LACTSSL_ACME_TLS_KEY` | ACME TLS 私钥路径 |
| `LACTSSL_ACME_MTLS_ENABLED` | 是否要求 ACME 客户端证书 |
| `LACTSSL_ACME_MTLS_CLIENT_CA` | ACME 客户端 CA 证书路径 |
| `LACTSSL_LANG` | 命令行输出语言，如 `zh-CN`、`en-US` |

示例：

```bash
LACTSSL_PORT=9000 \
LACTSSL_DATA=/var/lib/lactssl \
LACTSSL_TRUSTED_PROXIES=127.0.0.1,10.0.0.0/8 \
./bin/lactssl
```

安全入口也可以在服务启动后通过「设置 → 安全入口」修改，无需重启。

## 相关文档

- [安全](security.md)
- [运维与备份](operations.md)
