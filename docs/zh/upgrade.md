# 升级与变更

版本升级是证书平台风险最高的日常变更之一：既要保证数据库迁移可回滚，也要避免在证书集中到期时引入不可用。本文给出升级前评估、迁移机制、程序升级、回滚和变更窗口清单。

## 1. 变更原则

- **先备份，再升级**：`config.yaml` + 整个 `data/`，或使用界面加密备份。
- **先预发，再生产**：至少验证签发、部署、通知和 API 四条链路。
- **固定版本**：生产不使用不可追溯的 `latest` 长期运行。
- **保留回滚点**：数据库迁移快照、旧二进制/旧镜像标签。
- **避开高峰**：不要与证书集中到期、批量部署或业务发布窗口重叠。
- **可观测**：升级后至少观察一个调度周期和一次任务执行。

## 2. 升级会做什么

LACTSSL 在长期迭代中积累了一批历史数据格式和兼容分支。`internal/upgrade` 会在新版本首次启动时自动执行历史遗留迁移，也可以登录后访问 `/upgrade` 进行预检、手动迁移和数据库优化。

### 2.1 自动迁移顺序

1. 读取并校验 `config.yaml`。
2. 打开配置的数据库（默认 SQLite，也可为 PostgreSQL 或 MySQL）并执行基础建表。
3. 创建升级工具并读取 `upgrade.schema_version`。
4. 如果版本落后，先备份数据库与配置，再按顺序执行迁移步骤。
5. 写入新版本号，执行低成本数据库优化。
6. 继续初始化账户、调度器与 HTTP 服务。

- 自动迁移由 `upgrade.auto` 控制，默认开启。
- 迁移步骤全部幂等，失败时不会推进版本号，下次启动会重试失败步骤。
- 迁移前备份位于 `data/upgrade-backups/<UTC 时间>/`，默认保留最近 5 份。

### 2.2 迁移内容

| 问题 | 旧版本表现 | 升级工具处理 |
| --- | --- | --- |
| 数据库没有格式版本 | 只能靠 `ensureColumn` 和零散迁移键推断结构 | 写入 `upgrade.schema_version`，按版本顺序执行幂等步骤 |
| 敏感字段明文 | 旧版 TOTP 密钥、证书私钥直接落库 | 使用 AES-256-GCM 加密，已是密文的记录自动跳过 |
| 旧版密钥文件 | `data/jwt.secret` 与 `config.yaml` 双份密钥 | 确认配置已保存密钥后移入升级备份目录并清理 |
| 全局 EAB 凭据 | `acme.eab_kid` / `acme.eab_hmac` 只适用于默认 CA | 迁移到默认 CA 的独立设置，清理旧键 |
| 部署参数混在凭据中 | 路径、站点、重载命令保存在目标凭据 | 拆分到任务流程的部署节点，凭据只保留认证信息 |
| 重复部署目标 | `qiniu_cdn`、`baota_site` 等旧 ID 分散 | 合并为规范 ID，并把网站/面板、CDN/Kodo 选择写入节点 |
| 旧通知设置 | Gotify / Server酱 使用独立设置键 | 转换为通用通知渠道配置，再清理旧键 |
| 插件迁移残留 | 旧全量 helper、停用列表、缺失的启用白名单 | 初始化 DNS/部署/通知白名单，保留内置条目的默认启用状态 |
| 旧验证模式 | `password_passkey` 等历史值 | 归一为 `passkey`，无效值回退 |
| 数据库膨胀 | 删除证书、日志、ACME 订单后留下空闲页 | `VACUUM` 回收空间，`ANALYZE` 更新统计 |
| WAL 过大 | 长期运行后 `-wal` 文件持续增长 | `wal_checkpoint(TRUNCATE)` 折叠并截断 |
| 缺少热路径索引 | 证书筛选、调度、日志清理全表扫描 | 自动补充索引，重复执行安全 |
| 日志表分离 | 系统日志保存在 `system_logs`，操作日志在 `logs` | 复制系统日志到 `logs(category=runtime)`，旧表保留用于回滚 |
| 配置结构陈旧 | 缺少 `notify_plugins`、`log.system`、`upgrade` 等新段 | 按当前版本补齐缺失字段，保留注释并规范化等价写法 |

## 3. 升级前检查

- [ ] 阅读目标版本变更说明和迁移说明。
- [ ] 确认当前版本、数据格式版本和待迁移步骤。
- [ ] 备份 `config.yaml` 与 `data/`，并验证备份可读。
- [ ] 确认磁盘空间足够（`VACUUM` 需要临时空间）。
- [ ] 在预发环境验证签发、部署、通知、API。
- [ ] 确认旧版本二进制/镜像标签可用。
- [ ] 通知相关方，选择低峰窗口。
- [ ] 暂停批量任务，避免升级期间并发写入。

## 4. 升级方式

### 4.1 Docker（推荐）

```bash
# 固定目标版本：先把 docker-compose.yml 中的 image 标签改为 1.2.3
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```

- 使用明确版本标签，便于回滚。
- 不要在容器内使用自升级：重建容器后会回到镜像版本。
- 官方 Compose 默认只读根文件系统，程序目录不可写，页面会提示不支持自升级；这是预期行为。

### 4.2 二进制

```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo chmod 0755 /opt/lactssl/lactssl
sudo systemctl start lactssl
sudo journalctl -u lactssl -f
```

注意：

- 升级会保留原文件权限位，但不会保留 `setcap` 等扩展属性；使用特权端口或文件 capability 的部署需在升级后重新设置。
- 直接 `exec` 新二进制的自升级在 Linux/macOS/FreeBSD 上可用；Windows 需要服务管理器重新拉起。
- `dev`/源码构建，以及既没有 `checksums.txt` 也没有资产 SHA-256 的发布包会被拒绝自升级，必须手动替换并自行校验。

### 4.3 程序自升级（可选）

`/upgrade` 页面上方提供「程序升级」：

- **检查更新**：按 `upgrade.program.channel` 查询 Release；`stable` 只接受正式版，`beta` 包含预发布。
- **安装更新**：下载 `lactssl_<version>_<os>_<arch>.tar.xz`（缺失时回退到 `.tar.gz`；Windows 为 `.zip`），使用 `checksums.txt` 强制校验 SHA-256，解出 `lactssl` 后替换当前可执行文件。
- **发布签名**：官方发布构建会自动内置发布公钥，默认强制校验 `checksums.txt.sig`；缺失或错误签名直接拒绝。自编译版本未配置 `upgrade.program.public_key` 时默认拒绝在线升级，必须在受控环境显式设置 `upgrade.program.allow_unsigned: true` 才能安装未签名包。
- **备份与回滚**：替换前把旧二进制复制到 `<data_dir>/upgrade-backups/program/`，默认保留最近 3 份。
- **重启生效**：安装完成后页面提示重启；默认开启会话保持，重启后可继续使用原会话。如设为 `security.session_persistence: false`，重启会清空会话。

限制：

- 只读文件系统、无发布包的平台、`dev` 构建显示为不可升级。
- 容器内升级不改变镜像，重建后回到镜像版本。
- GitHub API 未配置 token 时受匿名速率限制；可通过 `upgrade.program.token` 配置。
- 需要出站代理时，通过系统代理环境变量配置（Go HTTP 客户端行为）。

## 5. 升级后验证

1. 查看启动日志中的“数据格式升级完成”。
2. 登录后打开 `/upgrade`，确认步骤全部为“完成”或“跳过”。
3. 检查版本、构建时间和架构。
4. 验证管理员登录、TOTP/通行密钥。
5. 检查证书列表、任务、部署凭据、通知渠道和插件。
6. 手动运行一条测试任务，确认签发、部署和通知。
7. 检查 API 密钥权限和调用。
8. 观察至少一个调度周期（30 秒）和一次自动续期。

## 6. 回滚

### 6.1 数据回滚

升级工具在结构变更前生成快照：

```text
data/upgrade-backups/20260101-120000/
├── lactssl.db
└── config.yaml
```

步骤：

1. 停止服务。
2. 用备份中的 `lactssl.db` 覆盖当前数据库，并删除 `lactssl.db-wal`、`lactssl.db-shm`。
3. 如需一并回滚配置，用备份中的 `config.yaml` 覆盖当前配置。
4. 启动旧版本程序。

注意：

- 旧版本程序可能无法读取新版本写入的数据格式；跨大版本回滚时，应同时回滚二进制与备份数据。
- 不要手动删除 `upgrade.schema_version`，否则会重复执行迁移步骤。
- 如果自动迁移失败，服务会拒绝启动并打印失败步骤；修复权限、磁盘或数据问题后直接重启即可重试。

### 6.2 程序回滚

程序升级备份位于：

```text
data/upgrade-backups/program/
├── 20260101-120000-lactssl-1.2.2
└── 20260102-090000-lactssl-1.2.3
```

步骤：

1. 停止服务。
2. 用备份文件覆盖当前二进制，并恢复执行权限（`chmod 0755`）。
3. 如需回滚数据，按上一节操作。
4. 启动服务，确认版本号与数据格式迁移状态。

## 7. 数据库在线切换（SQLite ↔ PostgreSQL）

`/upgrade` 页面提供数据库后端切换。切换在服务进程内完成，不需要手工执行 SQL 或替换数据库文件。

### 7.1 前提

- 目标 PostgreSQL 建议 18，MySQL 要求 5.7+；应用会检查服务端版本。
- 目标 PostgreSQL/MySQL 默认必须是空库；非空时必须在页面勾选“清空当前 schema”，该操作只删除当前 schema 中的表。
- 默认不允许第二个实例共享同一个 PostgreSQL/MySQL 数据库（`database.allow_multi_instance: false`），切换前也会检查。
- 切换前会强制备份源库：SQLite 使用 `VACUUM INTO`，PostgreSQL/MySQL 使用 Go 原生逻辑备份，备份位于 `data/db-convert/backups/<时间>/`。
- 服务需要由 systemd、Docker restart policy 或类似进程管理器托管，否则切换后需要手动启动。

### 7.2 执行流程

1. 在 `/upgrade` 选择目标后端。PostgreSQL/MySQL 可填写主机、端口、用户名、密码、数据库名与 TLS 模式，也可切换到“原生 DSN”直接粘贴完整 DSN；SQLite 填写文件路径（必须位于 `data_dir` 内）。
2. 点击“测试连接”和“预检”，确认目标版本、空库状态、表与行数。
3. 输入账户密码/TOTP，并完成页面上的算术确认后开始切换（界面不再要求手动输入 `CONVERT`，接口仍使用该确认词）。
4. 切换期间进入维护模式，普通 API 暂停；应用先备份，再冻结写入、逐表复制、校验行数与逐表哈希、抽样解密敏感字段。
5. 校验通过后原子改写 `config.yaml`，写入 `data/db-convert/state.json`，自动重启。
6. 新进程打开目标库并执行启动自检；失败会自动恢复旧配置并回到源库。自检通过后会自动删除旧 SQLite 数据库文件（含 `-wal`/`-shm`/`-journal`），切换前备份继续保留。

写冻结时间取决于数据量；典型小库为秒级，日志/证书很多时为分钟级。复制和校验期间页面仍可轮询进度。

### 7.3 一键回滚

切换完成后，`/upgrade` 会显示“一键回滚”（同构迁移除外）。回滚不是简单切回旧配置，而是把**当前库的最新数据**反向切换回切换前的后端，再切换配置并重启，避免丢掉切换后的写入。

回滚同样会强制备份当前库、冻结写入并校验；需要完成页面上的算术确认并输入账户密码/TOTP（接口仍使用 `ROLLBACK` 作为确认词）。切换前配置备份保留在 `config.yaml.before-db-convert-<时间>`。切换自检通过后旧 SQLite 文件会被自动删除，但 `data/db-convert/backups/<时间>/` 中的切换前备份仍保留；回滚会基于当前 PostgreSQL/MySQL 数据重新生成 SQLite 库。

### 7.4 CLI

服务停止时也可以使用 CLI 执行同一套核心逻辑：

```bash
./lactssl-dbconvert   -config config.yaml   -source-driver sqlite -source data/lactssl.db   -target-driver postgres -target "postgres://user:pass@host:5432/lactssl?sslmode=verify-full"   -switch -yes
```

MySQL 目标使用同样的命令，把 `-target-driver` 改为 `mysql`，DSN 使用 `mysql://user:pass@host:3306/lactssl?tls=true`。MySQL 备份与恢复由 Go 原生实现，不再依赖 `mysqldump` / `mysql` 客户端。

`-switch` 会改写 `config.yaml`，但不会自动启动服务。目标库非空时默认拒绝；确认清空时加 `-force`。

### 7.5 限制

- 不提供双写/CDC 的零停机切换；写入会在复制与切换阶段暂停。
- 多实例共享 PostgreSQL/MySQL 时不能安全切换，v1 直接拒绝。
- 环境变量 `LACTSSL_DATABASE_DSN` 存在时，改写配置文件不会生效；切换前必须先移除该变量并重启。
- 切换前必须确保没有正在执行的批量部署/签发任务。

### 7.6 同构迁移（SQLite → SQLite / PostgreSQL → PostgreSQL / MySQL → MySQL）

默认不允许同构迁移。确实需要把数据库搬到另一个文件或 PostgreSQL 实例时，必须在页面勾选“同构迁移”确认；API 请求体传 `allow_same_driver: true`，CLI 使用 `-allow-same-driver`。同构迁移额外限制：

- 目标必须是空库/空文件；同构迁移不允许勾选“清空 schema”，避免误删源库。
- 目标不能是当前库本身：SQLite 会比较真实文件（含软链接、硬链接）；PostgreSQL 会比较实例启动时间和数据库名，MySQL 会比较服务端 UUID 与数据库名，同一实例的同一数据库都会被拒绝。
- PostgreSQL → PostgreSQL、MySQL → MySQL 切换成功后不会删除源数据库，需要管理员确认后手工清理。
- SQLite → SQLite 切换自检通过后会删除旧 SQLite 文件，切换前备份仍保留在 `data/db-convert/backups/`。
- 同构迁移不支持页面上的“一键回滚”，请使用切换前备份手动恢复；PostgreSQL 源库会保留，SQLite 旧文件可由备份恢复。
- 同构迁移与 `-force` 不能同时使用。

## 8. 手动迁移与数据库优化

登录后访问 `/upgrade`（侧边栏「系统 → 版本升级」）：

- **预检**：显示程序版本、数据格式版本、迁移步骤状态、数据库完整性、空闲页、配置变更与风险提示，不修改任何数据。
- **执行迁移并优化**：再次执行迁移步骤，随后优化配置文件和数据库。
- **仅优化数据库与配置**：不重复运行迁移，适合日常维护。
- **回收磁盘空间**：开启后执行 `VACUUM`，会短暂独占数据库，建议低峰期使用。

执行结果会写入系统日志和 `upgrade.last_run` 设置。

### 7.1 数据库优化内容

- `PRAGMA quick_check(1)`：快速完整性检查。
- 基础表结构使用 `PRAGMA user_version` 记录版本，结构已是最新时跳过建表与 `ensureColumn` 扫描。
- 补充热路径索引：证书状态与到期时间、任务启用状态与下次运行、监控检查、日志级别与时间、ACME 订单/挑战、部署记录。
- `PRAGMA optimize`：按查询模式更新统计。
- `ANALYZE`：完整分析表和索引（手动优化）。
- `PRAGMA wal_checkpoint(TRUNCATE)`：折叠并截断 WAL。
- `VACUUM`：回收删除数据留下的空闲页，降低文件体积。

`VACUUM` 不会删除业务数据，但会重写数据库文件。执行前工具会先做数据库快照，磁盘空间不足时可能失败，失败会作为警告返回，不影响服务继续运行。

### 7.2 配置文件优化内容

- 补齐当前版本的全部标准配置段与默认值。
- 规范化 `server.addr`、`acme.addr` 等空白。
- 去除 `security.public_url`、`acme.base_url` 末尾斜杠。
- `log.level`、`log.format`、`log.system.level` 转小写。
- `server.trusted_proxies` 去重、去空白。
- 检查 ACME 未配置固定地址、部署插件接触私钥、上传插件沙箱关闭、密钥过短等风险。
- 写入前按启动加载逻辑完整校验，校验失败不会覆盖原文件。
- 使用 YAML 节点编辑，保留注释和字段顺序。

## 9. 配置项

```yaml
upgrade:
  auto: true              # 启动时自动执行历史遗留迁移
  auto_optimize: true     # 启动时执行低成本优化；不执行 VACUUM
  vacuum: true            # 允许在 /upgrade 手动优化时执行 VACUUM
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    # 官方构建会自动内置公钥；自编译版本未配置时默认拒绝在线升级，仅受控环境可显式允许未签名包
    allow_unsigned: false
    token: ""
```

关闭 `upgrade.auto` 后，启动日志会提示存在待迁移数据，但服务仍会继续启动。此时应尽快登录 `/upgrade` 手动执行，避免新代码读取旧格式数据；历史系统日志在日志合并步骤完成前不会显示在「日志 → 系统日志」页签。

## 10. API

升级工具使用现有登录态，接口固定位于 `/api`：

| 接口 | 方法 | 权限 | 说明 |
| --- | --- | --- | --- |
| `/upgrade` | GET | `upgrade:read` | 预检状态 |
| `/upgrade/run` | POST | `upgrade:write` | 迁移、配置优化与数据库优化 |
| `/upgrade/optimize` | POST | `upgrade:write` | 仅优化配置与数据库 |
| `/upgrade/database` | GET | `upgrade:read` | 当前后端与切换进度 |
| `/upgrade/database/test` | POST | `upgrade:read` | 测试目标连接与空库状态 |
| `/upgrade/database/preflight` | POST | `upgrade:read` | 切换预检 |
| `/upgrade/database/convert` | POST | `upgrade:write` | 启动切换（需二次认证） |
| `/upgrade/database/convert` | GET | `upgrade:read` | 查询切换进度 |
| `/upgrade/database/convert/cancel` | POST | `upgrade:write` | 取消未完成的切换 |
| `/upgrade/database/rollback` | POST | `upgrade:write` | 一键反向切换回旧后端 |
| `/upgrade/program` | GET | `upgrade:read` | 程序升级状态 |
| `/upgrade/program/check` | POST | `upgrade:write` | 立即查询 GitHub Release |
| `/upgrade/program/apply` | POST | `upgrade:write` | 下载并安装指定版本 |
| `/upgrade/program/restart` | POST | `upgrade:write` | 安装完成后重启服务 |

请求体可包含 `vacuum`、`analyze`、`config`、`optimize` 等布尔字段。写接口只需登录会话或具备相应权限的 API 密钥。

## 11. 变更窗口清单

- [ ] 已通知相关方，避开证书集中到期。
- [ ] 已备份并验证恢复密码。
- [ ] 已确认磁盘空间（备份 + VACUUM 临时空间）。
- [ ] 已准备旧版本二进制/镜像标签。
- [ ] 已暂停批量任务和自动化触发。
- [ ] 已完成升级和验证。
- [ ] 已恢复批量任务和自动化。
- [ ] 已记录变更单、版本、时间和结果。

## 相关文档

- [运维手册](operations.md)：备份、监控和故障响应。
- [配置参考](configuration.md)：`upgrade` 段字段。
- [监控与排障](troubleshooting.md)：启动失败、迁移失败和会话失效。
