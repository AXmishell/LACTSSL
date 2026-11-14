# 配置

## 配置文件

默认读取当前工作目录下的 `config.yaml`，可通过 `LACTSSL_CONFIG` 指定其他路径。

- 文件不存在时，首次启动会自动生成，并写入随机生成的 `jwt_secret` 与 `salt`。
- 修改配置后需要重启服务。
- 配置中包含密钥，建议保持 `0600` 权限。
- 优先级：**环境变量 > `config.yaml` > 内置默认值**。

完整带注释的示例见仓库根目录的 [`config.yaml.example`](https://github.com/AXmishell/LACTSSL/blob/main/config.yaml.example)。

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

log:
  level: info           # 进程日志级别：debug、info、warn、error
  format: text          # 输出格式：text 或 json
  file: ""              # 日志文件；留空仅输出 stdout
  also_stdout: true     # 写文件时是否同时输出 stdout
  max_size_mb: 20       # 单文件大小上限，超过后轮转
  max_backups: 5        # 保留历史文件数量，0 表示不限制
  max_age_days: 30      # 历史文件保留天数，0 表示不限制
  compress: true        # 是否压缩历史文件
  access_log: true      # 是否记录 HTTP 访问日志
  system:
    enabled: true       # 是否写入数据库供「系统日志」页面查看
    level: info         # 写入数据库的最低级别
    access: false       # 是否把 HTTP 访问日志也写入数据库
    retention_days: 7   # 保留天数
    max_rows: 20000     # 最大行数

security:
  entry: ""             # 安全入口，留空则首次启动随机生成
  public_url: ""        # 对外访问地址，如 "https://ssl.example.com"
  jwt_secret: ""        # JWT 签名密钥，留空自动生成并写回；手动配置至少 32 字符
  salt: ""              # 敏感字段加密盐，留空自动生成并写回；手动配置至少 32 字符

acme:
  enabled: true         # 是否启用内置 ACME 服务端，默认开启
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

### `log`

底层进程日志与界面中的操作日志相互独立：操作日志写入数据库并可在「日志」页面查看，底层日志用于启动、HTTP、调度、插件与 ACME 等运行期排障。

- `level`：最低输出级别，支持 `debug`、`info`、`warn`、`error`。`debug` 会附带源码位置，适合临时排障。
- `format`：`text` 适合直接阅读与 Docker 日志；`json` 适合日志采集系统。
- `file`：日志文件路径。留空时只输出到 stdout；相对路径基于服务进程的当前工作目录。目录不存在时会自动创建（`0700`），文件权限为 `0600`。
- `also_stdout`：写入文件时是否仍输出到 stdout。Docker 部署建议保持 `true`，既能在 `docker compose logs` 中查看，也能持久化到卷。
- `max_size_mb`：单文件大小上限，达到后按时间戳轮转，支持 `1-1024`。
- `max_backups`：保留的历史文件数量，`0` 表示不限制；超出后删除最旧文件。
- `max_age_days`：历史文件保留天数，`0` 表示不限制。
- `compress`：是否将轮转后的历史文件压缩为 `.gz`。
- `access_log`：是否记录 HTTP 访问日志。日志只包含方法、路由模板、状态码、耗时、客户端 IP 等字段，**不记录查询参数、请求体和认证头**；安全入口在路径中统一替换为 `:entry`。每个响应都会带 `X-Request-ID`，可用于与日志关联。

示例：把日志持久化到数据目录，并保留 7 天：

```yaml
log:
  level: info
  file: "data/logs/lactssl.log"
  also_stdout: true
  max_size_mb: 20
  max_backups: 10
  max_age_days: 7
  compress: true
  access_log: true
  system:
    enabled: true
    level: info
    access: false
    retention_days: 7
    max_rows: 20000
```

#### 系统日志

`log.system` 控制哪些进程日志写入数据库，并在界面「系统日志」页面展示。它与「操作日志」页面独立：

- `enabled`：是否写入数据库。关闭后不再产生新记录，页面仍可查看和清空历史记录。
- `level`：写入数据库的最低级别。不能低于 `log.level`，否则低级别日志不会生成；例如 `log.level: info` 时，`level: debug` 也不会记录 debug。
- `access`：是否把 HTTP 访问日志也写入数据库。默认 `false`，避免每个请求都写 SQLite；开启后访问量大时会快速占用数据库。
- `retention_days`：保留天数，范围 `1-365`。
- `max_rows`：最大行数，范围 `100-1000000`；超出后删除最旧记录。

写入采用有界异步队列，数据库繁忙时不会阻塞业务请求；队列满时会丢弃新日志。系统日志可能包含客户端 IP、域名和错误详情，请按运维数据管理。

### `security`

- `entry`：管理页面的安全入口路径段，手工设置要求 8-32 位字母、数字、下划线或中划线。留空时首次启动随机生成 20 位大小写字母与数字组合，并保存到数据库。
- `public_url`：对外访问地址，例如 `https://ssl.example.com`（不要带路径）。密码重置邮件中的链接基于该地址生成，避免 Host 头注入；未配置时仅允许本机回环请求按 Host 推导。也可用环境变量 `LACTSSL_PUBLIC_URL` 覆盖。
- `jwt_secret`：登录令牌签名密钥。留空时自动生成 32 字节随机值并写回配置文件；手动配置时至少 32 个字符，否则拒绝启动。
- `salt`：数据库中敏感字段（DNS/部署凭据、通知密钥、私有 CA 与证书私钥等）的加密盐。留空时自动生成并写回；手动配置时至少 32 个字符，否则拒绝启动。

### `acme`

- `enabled`：是否启用内置 ACME 服务端，默认开启。未创建任何端点时不会对外签发证书；如需完全关闭协议入口，可设为 `false`。
- `addr`：独立监听地址，例如 `:14000`。留空时 ACME 协议挂载到主端口的 `/acme` 前缀。
- `base_url`：客户端实际访问的 ACME 基础地址，用于生成 directory 和账户资源 URL。公网部署必须配置为固定地址，不要依赖请求 Host。
- `tls.cert` / `tls.key`：ACME 独立监听直接提供 TLS 时使用；反向代理终止 TLS 时留空。
- `mtls.enabled` / `mtls.client_ca`：要求客户端证书时启用，并指定客户端 CA 证书。启用 mTLS 时必须同时配置 ACME TLS 证书和私钥。

> **务必备份 `config.yaml`。** 丢失 `salt` 后，数据库中已加密的凭据无法解密；丢失或更换 `jwt_secret` 会使现有登录会话失效。服务每次启动时还会轮换 JWT 签名子密钥，因此重启后也需要重新登录。

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
| `LACTSSL_LOG_LEVEL` | 进程日志级别，覆盖 `log.level` |
| `LACTSSL_LOG_FORMAT` | 日志格式，覆盖 `log.format` |
| `LACTSSL_LOG_FILE` | 日志文件路径，覆盖 `log.file` |
| `LACTSSL_LOG_ALSO_STDOUT` | 写文件时是否同时输出 stdout |
| `LACTSSL_LOG_MAX_SIZE_MB` | 单文件大小上限（MB） |
| `LACTSSL_LOG_MAX_BACKUPS` | 保留历史文件数量，`0` 表示不限制 |
| `LACTSSL_LOG_MAX_AGE_DAYS` | 历史文件保留天数，`0` 表示不限制 |
| `LACTSSL_LOG_COMPRESS` | 是否压缩历史文件 |
| `LACTSSL_LOG_ACCESS` | 是否记录 HTTP 访问日志 |
| `LACTSSL_LOG_SYSTEM_ENABLED` | 是否把进程日志写入数据库 |
| `LACTSSL_LOG_SYSTEM_LEVEL` | 写入数据库的最低级别 |
| `LACTSSL_LOG_SYSTEM_ACCESS` | 是否把 HTTP 访问日志写入数据库 |
| `LACTSSL_LOG_SYSTEM_RETENTION_DAYS` | 系统日志保留天数 |
| `LACTSSL_LOG_SYSTEM_MAX_ROWS` | 系统日志最大行数 |
| `LACTSSL_LANG` | 命令行输出语言，如 `zh-CN`、`en-US` |

示例：

```bash
LACTSSL_PORT=9000 \
LACTSSL_DATA=/var/lib/lactssl \
LACTSSL_TRUSTED_PROXIES=127.0.0.1,10.0.0.0/8 \
./bin/lactssl
```

安全入口也可以在服务启动后通过「设置 → 安全入口」修改，无需重启。

## DNS 插件

`plugins` 配置控制 DNS exec helper，详见 [DNS 插件](dns-plugins.md)。默认主程序不内置 DNS 服务商；迁移前内置的 35 家服务商由 helper 提供并默认启用，其余服务商默认停用，可在「插件」页面中按需启用（状态保存在数据库，不写入配置文件）。

```yaml
plugins:
  enabled: true
  dir: ""                 # 留空表示 <data_dir>/plugins/dns
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64
```

## 部署插件

`deploy_plugins` 配置控制外部证书部署插件，详见 [部署插件](deploy-plugins.md)。
部署插件会接收证书私钥，默认关闭。DNS 插件与部署插件分别使用 `<data_dir>/plugins/dns` 和 `<data_dir>/plugins/deploy`，两个子目录互不混放。

```yaml
deploy_plugins:
  enabled: false
  dir: ""                 # 留空表示 <data_dir>/plugins/deploy
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64
```

## 相关文档

- [安全](security.md)
- [运维与备份](operations.md)
