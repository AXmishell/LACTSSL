# 配置参考

LACTSSL 的应用配置只来自 `config.yaml` 和内置默认值，不读取应用级环境变量。修改配置后需要重启服务生效；部分运行时设置（门禁入口、插件启用状态）保存在数据库中，优先级高于配置文件。

## 1. 配置文件模型

默认读取当前工作目录下的 `config.yaml`；也可在启动时用 `-config` 指定其他路径。

- 文件不存在时，首次启动会自动生成，并写入随机生成的 `jwt_secret` 与 `salt`。
- 包含密钥，建议保持 `0600` 权限。
- 应用配置只来自 `config.yaml` 与内置默认值；系统代理等 Go HTTP 客户端行为仍可通过环境变量配置。
- 配置变更后重启生效；门禁入口可在界面修改，立即生效。
- 完整带注释的示例见仓库根目录 [`config.yaml.example`](https://github.com/AXmishell/LACTSSL/blob/main/config.yaml.example)。

### 1.1 最小生产配置

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

data_dir: "data"

security:
  public_url: "https://ssl.example.com"
  session_persistence: true

log:
  level: info
  format: json
  file: "data/logs/lactssl.log"
  also_stdout: true
  max_size_mb: 50
  max_backups: 10
  max_age_days: 30
  compress: true
  access_log: true
  system:
    enabled: true
    level: info
    access: false
    retention_days: 30
    max_rows: 200000
  archive:
    enabled: true
    compress: true
    retention_days: 0
    max_total_mb: 0
    quota_policy: fail
```

### 1.2 配置优先级

| 配置 | 来源 | 说明 |
| --- | --- | --- |
| 监听地址、日志、密钥、ACME | `config.yaml` | 修改后重启生效 |
| 门禁入口 | 数据库，`config.yaml` 可覆盖 | 界面修改立即生效；`security.entry` 非空时启动覆盖 |
| 插件启用状态 | 数据库白名单 | 重启和重扫后保持 |
| 系统语言、通知渠道等 | 数据库 | 界面修改 |

### 1.3 Web 配置文件入口

登录后可在「系统设置 → 配置文件」中修改 `config.yaml` 的白名单字段，包括监听地址、客户端 IP 模式、PROXY protocol、可信反向代理、`security.public_url`、会话保持、限流阈值、ACME、日志与日志归档、升级与插件运行参数。

- 限流页签展示合并内置默认值后的生效规则；保存时会写回 `config.yaml`，重启后生效。
- 限流规则支持“恢复默认”：保存后会从 `config.yaml` 删除对应的 `security.rate_limit.rules.<name>` 键，回退到内置默认值。
- `upgrade.program.allow_unsigned` 可在表单中开启，但开启后会跳过发布包签名校验，仅建议在受控内网使用。
- `jwt_secret`、`salt`、`data_dir`、`database.path`、日志文件、插件目录与仓库信任根等高风险字段仍只能在服务器上编辑。
- 保存前会自动在 `<data_dir>/config-backups/` 创建配置快照；也可手动创建。快照只包含 `config.yaml`，与包含数据库的加密全量备份不同。
- 恢复快照只回写白名单字段，当前的 `jwt_secret`、`salt`、`data_dir`、`database.path` 保持不变，避免破坏数据库解密。
- 保存后需重启服务生效。修改监听端口时会先做端口预检；若新地址不可用，服务可能无法自动恢复，请保留 SSH 或控制台访问。
- 「可信反向代理」支持自动检测：检测只使用当前请求的 TCP 对端地址，`X-Forwarded-For` 等转发头仅作展示，不会被自动加入可信列表。检测结果会区分已保存与当前生效的可信列表，并显示客户端 IP 来源、跳过的可信项和转发头解析告警。反代场景建议同时配置 `security.public_url`。

## 2. 完整字段示例

```yaml
server:
  addr: ""              # 完整监听地址，如 ":8080"；非空时优先于 host + port
  host: ""              # 监听网卡，留空表示所有网卡
  port: 8080            # 监听端口
  trusted_proxies: []   # 可信反向代理 CIDR
  client_ip_mode: "auto"  # auto 或 cloudflare
  proxy_protocol: false   # 接受可信代理发送的 PROXY protocol v1/v2
  tls:                    # 主监听 TLS；反向代理终止 TLS 时留空
    cert: ""
    key: ""
    proxy_client_ca: ""   # 可选：mTLS 代理客户端 CA

data_dir: "data"        # 数据目录，存放插件、日志与升级备份

database:
  driver: "sqlite"      # sqlite（默认）、postgres 或 mysql（MySQL 5.7+）
  path: ""              # SQLite 路径；留空表示 <data_dir>/lactssl.db
  dsn: ""               # PostgreSQL/MySQL DSN；driver 非 sqlite 时必填
  max_open_conns: 10    # PostgreSQL/MySQL 连接池
  max_idle_conns: 5
  conn_max_lifetime_minutes: 30
  allow_multi_instance: false  # 是否允许多实例共享同一个数据库

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
    enabled: true       # 是否写入数据库供「日志 → 系统日志」页签查看
    level: info         # 写入数据库的最低级别
    access: false       # 是否把 HTTP 访问日志也写入数据库
    retention_days: 7   # 保留天数
    max_rows: 20000     # 最大行数
  archive:
    enabled: true       # 删除/清空日志前先归档；归档失败会阻止删除
    dir: ""             # 留空表示 <data_dir>/archives/logs
    compress: true      # 是否 gzip 压缩
    retention_days: 0   # 归档文件保留天数，0 表示永久保留
    max_total_mb: 0     # 归档目录容量上限（MB），0 表示不限制
    quota_policy: fail  # fail 或 prune_oldest

security:
  entry: ""             # 门禁入口，留空则首次启动随机生成
  public_url: ""        # 对外访问地址，如 "https://ssl.example.com"
  session_persistence: true  # 为 false 时进程重启后清空登录会话
  jwt_secret: ""        # JWT 签名密钥，留空自动生成并写回；手动配置至少 32 字符
  salt: ""              # 敏感字段加密盐，留空自动生成并写回；手动配置至少 32 字符
  rate_limit:
    max_keys: 10000     # 内存限流器最多保留的 key 数
    rules:
      # max 为窗口内允许次数（0 表示不限制），window_seconds 为窗口秒数。
      # 未列出的规则使用内置默认值；完整规则列表见 config.yaml.example。
      login_ip: {max: 10, window_seconds: 300}
      login_account: {max: 20, window_seconds: 900}
      api_ip: {max: 600, window_seconds: 60}
      api_key_ip_denied: {max: 10, window_seconds: 600}
      api_key_dns_denied: {max: 10, window_seconds: 600}
      acme_request: {max: 1200, window_seconds: 60}
      acme_order: {max: 300, window_seconds: 3600}

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

plugins:
  enabled: true
  dir: ""                 # 留空表示 <data_dir>/plugins/dns
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64

deploy_plugins:
  enabled: false
  dir: ""                 # 留空表示 <data_dir>/plugins/deploy
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64

notify_plugins:
  enabled: true
  dir: ""                 # 留空表示 <data_dir>/plugins/notify
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64

upgrade:
  auto: true
  auto_optimize: true
  vacuum: true
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    # 官方发布构建会自动内置公钥；自编译版本留空时默认拒绝在线升级，仅受控环境可显式允许未签名包
    allow_unsigned: false
    token: ""
```

## 3. `server`

- `addr`：完整监听地址，例如 `:8080`、`127.0.0.1:8080`。
- `host`、`port`：未设置 `addr` 时组合为监听地址。该端口由 Go 二进制直接提供页面与接口，前端资源已嵌入二进制，不会反向代理 Vite 的 `5173`；`5173` 仅在 `make dev` 时存在。
- `trusted_proxies`：可信反向代理的 IP/CIDR 列表。留空表示不信任任何代理头，适合直连部署；反向代理终止 TLS 时必须填写，否则 `Secure` Cookie 与客户端 IP 识别会不正确。

生产建议：

- `host` 设为 `127.0.0.1` 或内网地址，由反向代理访问。
- `trusted_proxies` 只包含实际代理地址，不要配置 `0.0.0.0/0`。
- 如果代理和 LACTSSL 不在同一主机，使用内网 CIDR。

## 4. `data_dir` 与数据库后端

`data_dir` 用于插件、日志和升级备份。数据库默认是 SQLite，文件位于 `<data_dir>/lactssl.db`；也可以显式使用 PostgreSQL 18 或 MySQL 5.7+（含 MariaDB 10.2+）：

```yaml
database:
  driver: "postgres"
  dsn: "postgres://user:password@127.0.0.1:5432/lactssl?sslmode=verify-full"
```

```yaml
database:
  driver: "mysql"
  dsn: "mysql://user:password@127.0.0.1:3306/lactssl?tls=true"
```

- `driver` 为 `postgres` 或 `mysql` 时 `path` 被忽略，`dsn` 必填；也可以用环境变量 `LACTSSL_DATABASE_DSN` 覆盖，避免密码写入配置文件。MySQL 还需同时设置 `LACTSSL_DATABASE_DRIVER=mysql`（未设置时默认按 PostgreSQL 解析）。
- MySQL DSN 支持 `mysql://user:pass@host:3306/db?tls=...` 和 go-sql-driver 原生格式 `user:pass@tcp(host:3306)/db`；程序会强制 `parseTime=true`、UTC 时间与 `utf8mb4`。
- 默认 `allow_multi_instance: false`，应用会持有 PostgreSQL advisory lock 或 MySQL `GET_LOCK`；第二个实例连接同一个数据库会启动失败。
- SQLite 仍固定单连接，适合单机小规模部署；需要外部备份、集中运维时再选 PostgreSQL 或 MySQL。
- 切换后端不需要手工改库：登录后到 `/upgrade` 的“迁移数据库”卡片执行，详见 [upgrade.md](upgrade.md#7-数据库在线切换sqlite--postgresql)。

数据目录用于存放数据库、插件、日志和升级备份。目录会以 `0700` 权限创建。`database.path` 留空时使用 `<data_dir>/lactssl.db`。

- 数据目录不要放在共享可写路径或公开目录。
- 不要把数据库放在 NFS/SMB 等多写共享存储上；SQLite 单写，锁语义不稳定。
- 备份范围是 `config.yaml` + 整个 `data/`。

## 5. `log`

底层进程日志与操作日志用途不同：操作日志记录“谁在何时做了什么”，显示在「日志 → 操作日志」页签；进程日志用于运行期排障，可异步写入数据库并显示在「日志 → 系统日志」页签，同时输出到 stdout/文件。

- `level`：最低输出级别，支持 `debug`、`info`、`warn`、`error`。`debug` 会附带源码位置，适合临时排障。
- `format`：`text` 适合直接阅读与 Docker 日志；`json` 适合日志采集系统。
- `file`：日志文件路径。留空时只输出到 stdout；相对路径基于服务进程的当前工作目录。目录不存在时会自动创建（`0700`），文件权限为 `0600`。
- `also_stdout`：写入文件时是否仍输出到 stdout。Docker 部署建议保持 `true`。
- `max_size_mb`：单文件大小上限，达到后按时间戳轮转，支持 `1–1024`。
- `max_backups`：保留的历史文件数量，`0` 表示不限制；超出后删除最旧文件。
- `max_age_days`：历史文件保留天数，`0` 表示不限制。
- `compress`：是否将轮转后的历史文件压缩为 `.gz`。
- `access_log`：是否记录 HTTP 访问日志。日志只包含方法、路由模板、状态码、耗时、客户端 IP 等字段，不记录查询参数、请求体和认证头；门禁入口在路径中统一替换为 `:entry`。每个响应都会带 `X-Request-ID`。
- `system.enabled`：是否写入数据库供「日志 → 系统日志」页签查看。
- `system.level`：写入数据库的最低级别。不能低于 `log.level`，否则低级别日志不会生成。
- `system.access`：是否把 HTTP 访问日志也写入数据库。默认 `false`，避免每个请求都写 SQLite。
- `system.retention_days`：保留天数，范围 `1–365`。
- `system.max_rows`：最大行数，范围 `100–1000000`；超出后删除最旧记录。
- `archive.enabled`：是否在删除/清空数据库日志前先写入归档。默认开启；归档失败会阻止删除，不会静默丢弃日志。
- `archive.dir`：归档根目录，留空表示 `<data_dir>/archives/logs`。目录以 `0700` 创建，文件权限为 `0600`。
- `archive.compress`：是否 gzip 压缩；关闭后保存为 `.ndjson`。
- `archive.retention_days`：归档文件保留天数，范围 `0–3650`；默认 `0` 表示永久保留。
- `archive.max_total_mb`：归档目录容量上限（MB），`0` 表示不限制。超限策略由 `quota_policy` 决定。
- `archive.quota_policy`：`fail`（默认，拒绝继续归档并阻止删除）或 `prune_oldest`（删除最旧归档）。`prune_oldest` 会销毁审计材料，必须显式配置。

归档文件由 `*.ndjson.gz`（或 `.ndjson`）与 `*.manifest.json` 组成，清单包含行数、ID 范围、时间范围、触发原因、操作者、SHA-256 和文件大小。归档目录不进入默认 ZIP 备份，需单独备份。

写入采用有界异步队列，数据库繁忙时不会阻塞业务请求；队列满时会丢弃新日志。系统日志可能包含客户端 IP、域名和错误详情，请按运维数据管理。

生产建议：日志文件放独立磁盘或挂载卷；`format: json` 接入日志平台；`access_log: true` 便于审计；`system.access` 保持 `false`，除非需要短期排查。

## 6. `security`

- `entry`：管理页面的门禁入口路径段，手工设置要求 8–32 位字母、数字、下划线或中划线。留空时首次启动随机生成 20 位大小写字母与数字组合，并保存到数据库。
- `public_url`：对外访问地址，例如 `https://ssl.example.com`（不要带路径）。密码重置邮件中的链接基于该地址生成，避免 Host 头注入；未配置时仅允许本机回环请求按 Host 推导。
- `session_persistence`：会话保持，默认 `true`。为 `true` 时服务启动不再清空 `sessions` 表，JWT 也改用固定的 `jwt_secret` 签名，因此后台进程重启后浏览器仍可继续使用原有登录会话；为 `false` 时每次启动仍会强制所有旧登录失效。
- `jwt_secret`：登录令牌签名密钥。留空时自动生成 32 字节随机值并写回配置文件；手动配置时至少 32 个字符，否则拒绝启动。
- `salt`：数据库中敏感字段（DNS/部署凭据、通知密钥、私有 CA 与证书私钥等）的加密盐。留空时自动生成并写回；手动配置时至少 32 个字符，否则拒绝启动。
- `rate_limit.max_keys`：内存令牌桶最多保留的 key 数，默认 `10000`。每个客户端 IP、账户或操作会占用一个 key；超出后按最近未使用顺序淘汰。
- `rate_limit.rules`：按规则名覆盖限流阈值，每项包含 `max`（窗口内允许次数）与 `window_seconds`（窗口秒数）。未配置的规则使用内置默认值；省略 `window_seconds` 时沿用默认窗口；`max: 0` 表示关闭该规则。未知规则名会导致启动失败，便于发现拼写错误。

常用规则及默认值：

| 规则 | 默认值 | 说明 |
| --- | --- | --- |
| `login_ip` / `login_account` / `login_totp` | 10/5 分钟、20/15 分钟、5/10 分钟 | 登录 IP、账户失败与 TOTP 校验 |
| `forgot_ip` / `forgot_account` | 5/10 分钟、3/小时 | 忘记密码 |
| `passkey_login_begin` / `passkey_login_fail` | 20/5 分钟、10/5 分钟 | Passkey 登录 |
| `reauth` | 10/10 分钟 | 敏感操作再认证失败 |
| `api_ip` / `api_anonymous` / `entry` | 600/分钟、120/分钟、60/分钟 | `/api` 总量、匿名请求与门禁探测 |
| `api_key_ip_denied` | 10/10 分钟 | API 密钥来源 IP 被拒时的审计日志限频 |
| `api_key_dns_denied` | 10/10 分钟 | API 密钥使用未授权 DNS 账户时的审计日志限频 |
| `csr_create` | 120/小时 | ACME 客户端 CSR 提交 |
| `acme_request` / `acme_account` / `acme_pki` | 1200/分钟、30/小时、600/分钟 | ACME 协议入口、建账户、CRL/OCSP |
| `acme_order` | 300/小时 | 未在 ACME 端点单独配置时的订单上限 |

> 限流阈值修改后需重启服务生效。ACME 端点自身的「每小时订单数」在管理界面配置，优先级高于 `acme_order`。

> **务必备份 `config.yaml`。** 丢失 `salt` 后，数据库中已加密的凭据无法解密；丢失或更换 `jwt_secret` 会使现有登录会话失效。默认开启会话保持，重启不会清空服务端会话；设置 `security.session_persistence: false` 可恢复“每次启动清空会话并轮换 JWT 签名子密钥”的严格模式。

安全建议：

- `entry` 保持随机长字符串，不要使用 `admin`、`ssl`、`panel` 等可猜测值。
- `public_url` 必须是固定 HTTPS 地址，不能依赖请求 Host。
- `session_persistence` 根据企业会话策略选择；默认开启，进程重启不强制重新登录，需接受更长的会话生命周期。
- `jwt_secret` 和 `salt` 用密码管理器或配置管理系统生成/保管，不要提交到版本库。
- 更换 `salt` 会导致已有加密凭据不可用，必须配套迁移方案，禁止直接改值。

## 7. `acme`

- `enabled`：是否启用内置 ACME 服务端，默认开启。未创建任何端点时不会对外签发证书；如需完全关闭协议入口，可设为 `false`。
- `addr`：独立监听地址，例如 `:14000`。留空时 ACME 协议挂载到主端口的 `/acme` 前缀。
- `base_url`：客户端实际访问的 ACME 基础地址，用于生成 directory、账户资源 URL 以及证书内的 CRL/OCSP 地址。公网部署必须配置为固定地址，不要依赖请求 Host；未配置时会回退到 `security.public_url`，两者都为空时按请求 Host 推导并输出启动警告。
- `tls.cert` / `tls.key`：ACME 独立监听直接提供 TLS 时使用；反向代理终止 TLS 时留空。
- `mtls.enabled` / `mtls.client_ca`：要求客户端证书时启用，并指定客户端 CA 证书。启用 mTLS 时必须同时配置 ACME TLS 证书和私钥。

安全建议：

- 公网部署使用独立端口，并限制来源。
- 每个端点必须配置域名白名单；生产开启 EAB。
- `base_url` 必须是固定公网地址，避免 Host 头注入。
- 不需要 ACME 服务时设置 `enabled: false`，减少攻击面。

## 8. `plugins`、`deploy_plugins`、`notify_plugins`

三类插件分别使用 `<data_dir>/plugins/dns`、`<data_dir>/plugins/deploy` 和 `<data_dir>/plugins/notify`，子目录互不混放。

### 8.1 DNS 插件

```yaml
plugins:
  enabled: true
  dir: ""                 # 留空表示 <data_dir>/plugins/dns
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64
```

上传插件默认使用 `strict` 沙箱。Linux 下使用 Landlock 文件白名单、rlimit 与 `no_new_privs`；内核或容器不支持时自动降级为 `basic`。生产环境不要配置 `off`。

### 8.2 部署插件

部署插件会接收证书私钥，默认关闭：

```yaml
deploy_plugins:
  enabled: false
  dir: ""
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64
```

### 8.3 通知插件

主程序只内置邮件通知，通知插件默认开启：

```yaml
notify_plugins:
  enabled: true
  dir: ""
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64
```

插件目录权限 `0700`，helper `0755`，属主为服务运行用户。生产建议配置 `public_key` 并关闭 `auto_update`。详细治理见[插件治理](plugins.md)。

## 9. `upgrade`

`upgrade` 控制启动时自动迁移与 `/upgrade` 页面的优化行为：

```yaml
upgrade:
  auto: true              # 启动时自动执行历史遗留迁移
  auto_optimize: true     # 启动时补充索引、更新统计、折叠 WAL；不执行 VACUUM
  vacuum: true            # 允许在 /upgrade 手动优化时执行 VACUUM
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable       # stable 或 beta
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    token: ""
```

- 程序升级会从 GitHub Release 下载对应平台发布包，校验 `checksums.txt` 中的 SHA-256 后原子替换当前二进制，并把旧版本备份到 `<data_dir>/upgrade-backups/program/`。
- 只读文件系统、无发布包的平台或 `dev` 构建会显示为不可升级。
- 容器内升级不会改变镜像，重建容器后仍会回到镜像版本；生产建议用镜像标签升级。
- `public_key` 配置后会强制校验 `checksums.txt.sig`；`token` 用于提高 GitHub API 速率上限。

## 10. 命令行参数

| 参数 | 说明 |
| --- | --- |
| `-config` | 配置文件路径，默认当前工作目录下的 `config.yaml` |

```bash
./bin/lactssl -config /etc/lactssl/config.yaml
```

门禁入口也可以在服务启动后通过「设置 → 门禁入口」修改，无需重启。

## 11. 反向代理要点

### 11.1 信任边界

- 只有 `server.trusted_proxies` 中列出的 TCP 对端地址才被视为可信代理；留空表示不信任任何代理头，适合直连部署。
- `X-Forwarded-For`、`X-Real-IP`、`Forwarded`、`CF-Connecting-IP` 在代理被信任前都只是客户端可伪造的声明。设置页的「自动检测」只提供线索，不会自动把地址加入可信列表。
- 不要配置 `0.0.0.0/0`、`::/0` 或过大的内网网段；可信列表中的任何主机都能代表客户端伪造来源 IP。

### 11.2 覆盖模式（推荐）

让最靠近 LACTSSL 的代理用真实客户端地址覆盖转发头：

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-Proto $scheme;
```

此时 `trusted_proxies` 只需要填写这一层代理地址，解析一次即可得到客户端 IP。多级场景可由边缘代理先还原真实客户端，再由最近一跳覆盖；不要让中间代理把上游代理地址覆盖成客户端 IP。

### 11.3 追加模式

Nginx 的 `$proxy_add_x_forwarded_for` 会保留客户端自带的 XFF，并追加当前连接地址。LACTSSL 从右向左跳过可信项：

- 单层代理：只信任 Nginx 即可。
- 多级代理：必须把链上所有会出现在 XFF 中的代理加入 `trusted_proxies`。只信任最近一跳时，结果会停在上游代理 IP。
- 绝不能把最左侧的客户端地址加入可信列表。

例如 `client → CDN → Nginx → LACTSSL`，追加后的 XFF 是 `client, CDN 边缘 IP`；只信任 Nginx 会得到 CDN 边缘 IP，必须同时信任 CDN 的官方网段，或改由 Nginx 覆盖 XFF。

### 11.4 CDN 与云负载均衡

- `CF-Connecting-IP` 只有在 TCP 对端属于 Cloudflare 官方网段时才可信；仅看到该头就采信会被直连客户端伪造。
- 推荐在自建 Nginx 上按官方网段启用 `real_ip`，再用 `$remote_addr` 覆盖 XFF，LACTSSL 只信任这台 Nginx。
- CDN 边缘 IP 会变化，不要逐个加入可信列表；使用官方网段，或在 CDN 后增加一层你控制的反向代理。
- L4/TCP 负载均衡没有 HTTP 头可解析，只能看到 LB 地址；需要 PROXY protocol 或在其后增加一层能写 HTTP 头的代理。

### 11.5 Docker 与 Kubernetes

- 容器内看到的 peer 常是网桥网关或 Service 转发地址，不一定是真实客户端。
- 优先填写代理容器/节点的精确 IP；IP 动态时使用专用网络并只放行该网络中的代理地址或最小 CIDR。
- 不要因为“是私网”就直接信任 `172.16.0.0/12`；同网段其他容器可以伪造转发头。
- 确认 Ingress Controller 是否会清洗 XFF；不要假设所有 Ingress 的行为一致。

### 11.6 应用与验证

1. 将代理地址加入 `trusted_proxies`，配置 `security.public_url`。
2. 代理终止 TLS 时传递 `X-Forwarded-Proto: https`。
3. 备份恢复和插件上传需要较大的请求体上限（例如 Nginx `client_max_body_size 256m`），代理读超时至少覆盖备份恢复、插件下载和部署操作（例如 600 秒）。
4. 保存配置后重启服务，在「设置 → 配置文件 → 自动检测」确认 `peer_ip`、`applied_trusted_proxies`、`effective_client_ip`、`client_ip_source` 符合预期；保存未重启时 `configured_trusted_proxies` 与 `applied_trusted_proxies` 可能不同。
5. 直连伪造测试：从不受信地址发送 `X-Forwarded-For: 203.0.113.9`，服务端仍应使用 TCP 对端地址。访问日志中的 `ip_source`、`ip_skipped`、`ip_warnings` 可用于排障。

### 11.7 高级代理身份

解析优先级为：可信代理身份验证 → Cloudflare 专用头（可选）→ `X-Forwarded-For` → `X-Real-IP` → RFC 7239 `Forwarded` → TCP 对端。任何模式都不会让非可信来源的转发头生效。

- **RFC 7239 `Forwarded`**：在前面的头不可用时自动解析，支持 `for=203.0.113.9`、`for=203.0.113.9:4711`、`for="[2001:db8::1]:4711"`；`unknown`、`_hidden` 等混淆值视为不可用。
- **Cloudflare 模式**：
  ```yaml
  server:
    client_ip_mode: cloudflare
    trusted_proxies:
      - "173.245.48.0/20"
      - "103.21.244.0/22"
      # ... Cloudflare 官方网段
  ```
  只有 peer 属于 `trusted_proxies` 时才采信 `CF-Connecting-IP`。不要把该模式与只做本机覆盖的 Nginx 混用；源站应只允许 Cloudflare 回源。
- **PROXY protocol**：
  ```yaml
  server:
    proxy_protocol: true
  ```
  接受 v1/v2 头，且只信任 `trusted_proxies` 中的对端发送的头；非可信来源发送 PROXY 头会直接断开。没有 PROXY 头的普通 HTTP 仍可正常访问。L4 负载均衡需要开启并发送 PROXY protocol。
- **mTLS 代理身份**：
  ```yaml
  server:
    tls:
      cert: "/etc/lactssl/server.crt"
      key: "/etc/lactssl/server.key"
      proxy_client_ca: "/etc/lactssl/proxy-ca.pem"
  ```
  主监听改为 HTTPS；启用后应把 `security.public_url` 设置为 `https://` 地址。持有 `proxy_client_ca` 签发客户端证书的连接会被视为可信代理，不再依赖 IP。未携带客户端证书的浏览器仍可正常访问。该模式只适用于代理直接以 TLS 连接 LACTSSL 的部署；如果 TLS 在代理终止，应继续使用 IP/CIDR 信任或 PROXY protocol。请使用专用于代理的 CA，不要复用浏览器客户端 CA。

完整示例见[安装与启动](install.md)。

## 12. 配置校验与故障处理

常见启动失败原因：

| 错误 | 原因 | 处理 |
| --- | --- | --- |
| `jwt_secret` 太短 | 手动配置少于 32 字符 | 改为至少 32 字符或留空自动生成 |
| `salt` 太短 | 手动配置少于 32 字符 | 改为至少 32 字符或留空自动生成 |
| 监听失败 | 端口被占用或无权限 | 检查端口和运行用户 |
| mTLS 配置不完整 | 启用 mTLS 但缺少 TLS 证书/私钥 | 同时配置 `acme.tls.cert/key` |
| 配置校验失败 | 恢复包或手工编辑非法 | 按日志提示修正；恢复包会先校验再应用 |
| 修改后不生效 | 配置需要重启 | 重启服务；门禁入口/插件页设置例外 |

配置恢复和迁移由升级工具处理，详见[升级与变更](upgrade.md)。

## 相关文档

- [安装与启动](install.md)
- [部署架构](architecture.md)
- [安全基线](security.md)
- [运维手册](operations.md)
