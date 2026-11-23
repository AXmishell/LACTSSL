# LACTSSL 插件迁移实施方案

> **历史文档：v4 的 `dns/providers/`、`dns/native/`、`cmd/dns-helper` 及相关生成器已于 2026-11-22 移除；以下内容仅记录迁移过程，不再对应当前代码结构。**

> **状态：已实施（2026-11-15），并已于 2026-11-18 完成宿主/插件仓库的进一步分离。**
>
> - 插件实现、协议、元数据与 3 个 helper 已迁入本仓库。
> - 主仓库已删除 194 个 DNS 包装、部署/通知 provider、`dns_all` 全量版与三个 helper 入口。
> - 迁移前后三份 manifest 逐字段一致（`cmd/*-helper` 的测试会校验）。
> - 主仓库 `go.mod` 由 249 行降至 71 行，`go.sum` 由 2686 行降至 585 行。
> - 2026-11-18：主仓库不再通过 `go.mod` 依赖本仓库的 `protocol`/`catalog`/`i18n`，改为宿主内本地协议实现与 manifest 元数据；`catalog` 仅作为插件内部元数据。本文中“主仓库 → protocol/catalog”的依赖关系已不再适用。
>
> 下文保留原始方案与决策记录，便于回溯。

- 日期：2026-11-15
- 源仓库：`/root/LACTSSL`（`github.com/AXmishell/LACTSSL`）
- 目标仓库：`/root/LACTSSL-Plugins`（`github.com/AXmishell/LACTSSL-Plugins`）
- 目标：把 DNS / 部署 / 通知三类插件的**实现与官方 helper**迁出主仓库；主仓库只保留插件宿主（发现、校验、执行、启停、上传、UI/API）。

---

## 0. 结论摘要

1. **迁移范围**：约 1.0 万行 Go 代码
   - DNS：194 个 lego 服务商包装包 + 3 个构建 profile
   - 部署：5 个 provider 包，提供 7 个规范目标 + 6 个历史兼容 ID
   - 通知：23 个通知渠道
   - 3 个 helper 入口、3 套 manifest 类型、2 个代码生成器
2. **不迁移**：`internal/plugin/exec`、三个 `discover/exec/registry` 宿主包、插件上传/重扫/启停 API、前端插件页、内嵌 helper 安装逻辑。
3. **依赖方向**（必须保持单向）：
   - 主仓库 → `protocol`、`catalog`
   - 新仓库插件 → `protocol`、`sdk`、`catalog`
   - **禁止**新仓库 import 主仓库（主仓库 module path 是 `lactssl`，本身不可被远程拉取；且会形成环）。
4. **协议不变**：helper 协议保持 v1，CLI 子命令、stdin JSON、环境变量、manifest 字段全部不变；新增字段只能可选。
5. **已确认的两项决策**：
   - 元数据目录（catalog）**迁入 LACTSSL-Plugins**，主仓库通过 `go.mod` 依赖 import。
   - **本次直接移除 `dns_all` 全量版**：不再产出 `lactssl-full`，构建标签只保留 `embed` 与 `embed_helper`，官方 DNS 服务商统一由 helper 提供。
6. **推荐分 6 个阶段**，每阶段可独立合并、独立回滚；先搬「协议 + 元数据」，再搬实现，最后改构建发布。
7. **前置条件**：主仓库当前有 43 处未提交改动（含通知插件改造），必须先提交或在新 worktree 中执行迁移，避免把两批改动混在一起。

---

## 1. 现状盘点

### 1.1 三类插件

| 维度 | DNS 插件 | 部署插件 | 通知插件 |
|---|---|---|---|
| 官方实现 | 194 个 lego 服务商包装包 | 5 个包 / 7 个规范目标 | 23 个渠道 |
| helper 入口 | `cmd/dns-helper` | `cmd/deploy-helper` | `cmd/notify-helper` |
| 宿主包 | `internal/plugin/dns` | `internal/plugin/deploy` | `internal/plugin/notify` |
| 协议 | `manifest` / `present` / `cleanup` | `manifest` / `deploy` | `manifest` / `send` |
| 单次超时默认 | 60s | 300s | 30s |
| 默认安装 | 发布版内嵌并自动安装 | 手动安装，`deploy_plugins.enabled=false` | 手动安装，`notify_plugins.enabled=false` |
| 默认启用 | 迁移前内置的 35 家 | 7 个规范目标 + 旧 ID 白名单 | 23 个渠道（`notify_meta.go`） |
| 插件目录 | `<data>/plugins/dns` | `<data>/plugins/deploy` | `<data>/plugins/notify` |

### 1.2 关键文件：保留 vs 迁移

| 路径 | 处理 | 说明 |
|---|---|---|
| `internal/plugin/exec/*` | 保留 | 宿主侧子进程执行器（超时、RSS、环境白名单、脱敏） |
| `internal/plugin/dns/{registry,exec}.go` | 保留 | 宿主注册表与外部插件发现 |
| `internal/plugin/deploy/{registry,exec}.go` | 保留 | 同上 |
| `internal/plugin/notify/{registry,exec}.go` | 保留 | 同上 |
| `internal/plugin/dns/exec_manifest.go` | 改成类型别名 | 类型迁到 `protocol` |
| `internal/plugin/deploy/exec_manifest.go` | 改成类型别名 | 同上 |
| `internal/plugin/notify/exec_manifest.go` | 改成类型别名 | 同上 |
| `internal/plugin/dns/providers/**`（194 包） | 迁移 | 纯 lego 包装 |
| `internal/plugin/dns/builtin/profile_*.go` | 删除 | `dns_all` 全量版移除，主程序不再内置任何服务商 |
| `internal/plugin/dns/bundled/*` | 保留 | 仍负责把 helper 装进插件目录 |
| `internal/plugin/deploy/providers/**` | 迁移 | FTP/Webhook/阿里云 CDN/七牛/面板 |
| `internal/plugin/deploy/pluginutil/` | 迁移 | 部署侧工具 |
| `internal/plugin/notify/providers/channels/**` | 迁移 | 23 个渠道 |
| `internal/service/dns_providers_gen.go` | 迁移为 `catalog` | 159 条生成数据 |
| `internal/service/meta.go` 的 `DNSProviders`（35 条） | 迁移为 `catalog` | 手工维护数据 |
| `internal/service/deploy_plugin_targets.go` 的 `PluginDeployTypes`（13 条） | 迁移为 `catalog` | 含 6 个历史 ID 映射 |
| `internal/service/notify_meta.go` | 保留 | 宿主默认启用策略 |
| `internal/service/acme.go` 的 `EnvForProvider` / `builtinProviderEnv` / `applyProviderEnv` | 删除 | 环境变量映射迁入 `catalog`；主程序不再有内置服务商路径 |
| `internal/outbound/*` | 迁移 | 部署/通知插件共用；主仓库 `service/outbound_alias.go` 改为转发或保留副本 |
| `internal/i18n/*` | 保留 | 主仓库 UI 文案；插件侧只抽精简 shim |
| `internal/model.Certificate` | 拆分 | 部署插件只依赖 `protocol.Certificate` |
| `cmd/{dns,deploy,notify}-helper/` | 迁移 | 入口迁到新仓库 |
| `scripts/gen_dns_plugins/`、`scripts/gen_dns_providers/` | 迁移 | 代码生成器归属插件仓库 |
| `scripts/dns_plugins.json` | 删除 | `lite` profile 随 `dns_all` 一并移除 |

### 1.3 耦合点与处理

| 耦合 | 位置 | 风险 | 处理 |
|---|---|---|---|
| helper → `service` | `cmd/dns-helper`、`cmd/deploy-helper` | 没有 `service` 就无法生成 manifest、无法把表单字段映射为 lego 环境变量 | 元数据迁到 `catalog`；环境变量映射进 `catalog`/`sdk` |
| provider → `model.Certificate` | `pluginutil`、5 个部署 provider | 主仓库数据模型泄漏进插件 | `protocol.Certificate` 作为协议类型；部署侧本地最小结构 |
| provider → `i18n` | 86 处调用 | 插件必须带一套文案 | 新仓库 `i18n` 精简包 + 过滤后的 locale 片段（`zh-CN`/`en-US`） |
| provider → `outbound` | 4 个包 | SSRF 防护客户端 | 整包迁移到新仓库，主仓库保留自己的副本或改 import |
| 宿主 ↔ Manifest 结构 | 3 个 `exec_manifest.go` | 两份定义漂移 | 宿主改用 `protocol` 类型别名 |
| 宿主内置服务商路径 | `service/acme.go`、`dnsplugin.SourceBuiltin` | 移除 `dns_all` 后不再有内置实现，相关分支成为死代码 | 删除 `EnvForProvider`/`builtinProviderEnv`/`applyProviderEnv`，注册表类型保留供测试使用 |
| 发布 ↔ helper 源码 | `Makefile`、`Dockerfile`、`scripts/release.sh`、CI | 跨仓库构建 | `go build github.com/AXmishell/LACTSSL-Plugins/cmd/...`，版本由 `go.mod` 固定 |
| 内嵌 helper | `bundled/embed.go` + `bundled.Install` | 发布包仍要开箱即用 | 机制不变，产物改由新仓库构建 |

### 1.4 当前构建 / 发布链路

```text
make build
  ├─ web build
  └─ go build -tags embed ./cmd/server
       └─ embed_helper 构建标签
            └─ go:embed internal/plugin/dns/bundled/lactssl-dns-helper-extra
                 └─ 该文件由 Makefile/Dockerfile/release.sh 用 ./cmd/dns-helper 现场构建

make helper / helper-extra / helper-full
make deploy-helper / notify-helper
make gen-plugins
```

Docker、CI、release 三处都在本仓库内直接编译 `./cmd/*-helper`，迁移后都要改为编译新模块的包。

---

## 2. 目标架构

### 2.1 仓库职责

**LACTSSL（宿主）**

- 插件目录扫描、manifest 探测、sha256 校验、权限校验
- 一次性子进程执行、超时、RSS 上限、输出截断、敏感信息脱敏
- 插件注册表、启用白名单、上传 / 重扫 API、前端插件页
- DNS / 部署 / 通知业务编排
- 内嵌官方 DNS helper，首次启动自动安装

**LACTSSL-Plugins（SDK + 官方插件）**

- 协议 v1 的 Go 类型与请求/响应结构
- helper 启动框架（内存上限、子命令分发、JSON 编解码、错误退出）
- 服务商 / 部署目标 / 通知渠道的静态元数据（catalog）
- 194 个 DNS 包装、5 个部署实现、23 个通知渠道
- 3 个 helper 可执行文件
- 代码生成器、测试、独立发布流水线

### 2.2 目录结构

```text
LACTSSL-Plugins/
├── go.mod                          # module github.com/AXmishell/LACTSSL-Plugins
├── go.sum
├── LICENSE                         # 与主仓库一致（MIT）
├── README.md
├── Makefile
├── protocol/                       # 无第三方依赖
│   ├── protocol.go                 # ProtocolVersion = "v1"、环境变量名
│   ├── dns.go                      # DNSManifest / DNSProvider / Field / Option / Condition / Request
│   ├── deploy.go                   # DeployManifest / Deployer / Request / Certificate
│   └── notify.go                   # NotifyManifest / Channel / Request
├── sdk/
│   ├── helper/
│   │   ├── helper.go               # Run(kind, handlers)：子命令分发、stdin/stdout、退出码
│   │   ├── memory.go               # LACTSSL_PLUGIN_MEMORY_LIMIT → debug.SetMemoryLimit
│   │   └── feature.go              # LACTSSL_DEPLOY_PLUGIN_FEATURES 解析
│   └── registry/                   # 通用 ID/别名注册表（三份逻辑合并）
├── catalog/
│   ├── catalog.go                  # Provider / Field / Option / Condition 纯数据类型
│   ├── dns.go                      # 194 家服务商元数据（159 生成 + 35 手工）
│   ├── deploy.go                   # 13 条部署目标元数据 + 旧 ID → 规范 ID 映射
│   ├── env.go                      # 表单字段 → lego 环境变量（含 Cloudflare 互斥规则）
│   └── locales/{zh-CN,en-US}.json  # 插件相关文案片段（meta.* / service.deploy.* / notifications.* / plugin.*）
├── i18n/                           # 精简实现：T / E / Default，不依赖 gin
├── outbound/                       # SSRF 防护 HTTP 客户端（从主仓库迁入）
├── dns/
│   ├── registry.go                 # 插件侧注册表
│   ├── provider.go                 # Provider 接口：ID / Aliases / New
│   ├── all/all.go                  # 空导入全部 194 个包装包
│   └── providers/<id>/plugin.go
├── deploy/
│   ├── registry.go
│   ├── deployer.go                 # Deployer 接口 + 本地 Certificate
│   ├── util/                       # 原 pluginutil
│   └── providers/{ftp,webhook,aliyuncdn,qiniu,panels}/
├── notify/
│   ├── registry.go
│   ├── channel.go                  # Channel 接口 + Definition
│   ├── util/
│   └── providers/channels/
├── cmd/
│   ├── dns-helper/main.go
│   ├── deploy-helper/main.go
│   └── notify-helper/main.go
├── scripts/
│   ├── gen_dns_providers/
│   ├── gen_dns_plugins/
│   └── check_catalog/
├── testdata/
│   ├── legacy-dns-manifest.json    # 迁移前基线
│   ├── legacy-deploy-manifest.json
│   └── legacy-notify-manifest.json
└── .github/workflows/{ci,release}.yml
```

### 2.3 依赖方向

```text
                 ┌──────────────────────────────┐
                 │  LACTSSL-Plugins             │
                 │                              │
  protocol ◄─────┼── sdk ◄── dns/deploy/notify  │
     ▲           │      ▲          ▲            │
     │           │      └── catalog┘            │
     │           │              ▲               │
     │           │         i18n/outbound        │
     │           └──────────────┬───────────────┘
     │                          │
     │                    cmd/*-helper
     │
┌────┴─────────────────────────────────────────┐
│  LACTSSL（宿主）                              │
│  internal/{dns,deploy,notify}plugin           │
│  internal/plugin/exec / api / service          │
└──────────────────────────────────────────────┘
```

- 主仓库只 import `protocol` 和 `catalog`。
- 新仓库不 import 主仓库。
- `protocol` 不 import 任何第三方库，便于宿主低依赖接入。
- `catalog` 只依赖 `protocol`，不依赖 lego / net/http。

### 2.4 协议兼容性约束

以下内容**不得改变**：

- 子命令：`manifest` / `present` / `cleanup` / `deploy` / `send`
- manifest JSON 字段名与语义：`protocol`、`name`、`version`、`sha256`、`providers` / `deployers` / `channels`
- 请求 JSON：DNS 的 `action/provider/config/domain/token/key_auth`；部署的 `action/protocol/provider/auth/config/certificate`；通知的 `action/protocol/channel/config/title/body`
- 退出码约定：`0` 成功，非 `0` 失败；stderr 进入日志
- 环境变量：`LACTSSL_PLUGIN_MEMORY_LIMIT`、`LACTSSL_PLUGIN_PROTOCOL`、`LACTSSL_DEPLOY_PLUGIN_FEATURES`
- DNS 服务商 ID、别名、字段 key、字段 `Env` 名
- 部署目标 ID（含 6 个历史 ID）、别名、`fields` / `deploy_fields`
- 通知渠道 ID、别名、`label_key`

允许的**可选新增**（向后兼容）：

- `ManifestField.label_key` / `help_key` / `placeholder_key`
- `ManifestProvider.deprecated`（仅展示用途）
- manifest 顶层 `build`（构建信息，宿主忽略）

---

## 3. 关键设计决策

### D1：元数据目录（catalog）归属 —— 已确认迁入插件仓库

- 新仓库的 helper 和主仓库的 UI 使用同一份元数据，彻底消除漂移。
- 主仓库在 `internal/service` 增加 `catalog_bridge.go`，把 `catalog.Provider` 转换为现有 `ProviderDef`/`FieldDef`；`Localized()`、`IsSecret()` 等方法仍留在主仓库。
- `LabelKey` / `DescKey` 保持不变，主仓库现有 598 个 `meta.*` 文案无需改动。
- 新仓库带一份过滤后的 locale 片段，供 helper 生成 manifest 文案与运行时错误；主仓库 UI 仍用完整 locale。
- 加一个 `scripts/check_catalog` 校验：catalog 引用的每个 key 必须存在于主仓库 locale（可在主仓库 CI 运行）。
- 主仓库删除 `dns_providers_gen.go`、`meta.go` 中的 `DNSProviders`、`deploy_plugin_targets.go` 中的 `PluginDeployTypes`，只保留桥接与业务逻辑。

### D2：`dns_all` 全量版 —— 已确认本次移除

**目标状态：主程序不编译任何 DNS 服务商，官方服务商只通过 helper 提供。**

需要同步清理的内容：

| 位置 | 改动 |
|---|---|
| `internal/plugin/dns/providers/**` | 迁到新仓库 |
| `internal/plugin/dns/builtin/**` | 删除 |
| `internal/plugin/dns/registry.go` | 保留注册表与 `Source` 类型；注释改为「仅测试或未来扩展使用」 |
| `internal/service/acme.go` | 删除 `newDNSProvider` 的 builtin 分支、`EnvForProvider`、`builtinProviderEnv`、`applyProviderEnv` |
| `scripts/gen_dns_plugins`、`scripts/dns_plugins.json` | 删除（生成器迁到新仓库，不再有 profile 概念） |
| `Makefile` | 删除 `build-full`、`backend-full`、`helper-full`、`FULL_TAGS`、`gen-plugins` |
| `Dockerfile` | 删除 `TAGS` 构建参数中的 `dns_all` 分支，只保留 `embed embed_helper` |
| `docker-compose.yml` | 删除 `TAGS: "embed dns_all"` 注释 |
| `.github/workflows/{ci,release}.yml` | 删除 `dns_all` 构建与 `lactssl-full` 产物 |
| `scripts/{ci,release}.sh` | 同上 |
| `internal/service/acme_dns_test.go` | 删除基于 `dns_all` 的跳过逻辑，改为 helper 集成测试或直接删除 |
| `docs/{dns-plugins,development,features,install}.md` 及英文版 | 删除「完整版」章节，构建矩阵只保留 `embed` / `embed_helper` |
| `README.md` | 更新构建方式与产物列表 |

**用户影响与迁移说明：**

- `lactssl-full` 产物与 `-tags dns_all` 不再提供。依赖全量版的用户改用官方 DNS helper（发布包已内嵌并自动安装）。
- 发布包文件数由 5 个降为 4 个：`lactssl`、`lactssl-dns-helper-extra`、`lactssl-deploy-helper`、`lactssl-notify-helper`。
- Docker 镜像不再支持 `TAGS="embed dns_all"`，需在 release notes 中明确说明。
- 主仓库不再需要全部 lego 依赖；`go mod tidy` 后依赖图会明显缩小，`govulncheck` 告警面同步降低。

**收益：**

- 主仓库彻底与 194 家服务商实现解耦，依赖、编译时间、漏洞扫描面都下降。
- 只保留一条服务商加载路径（helper），减少「内置优先 / 插件重复被忽略」这类分支与文档解释成本。

### D3：helper 产物如何进入主仓库发布

**推荐：主仓库按固定模块版本现场构建。**

```bash
CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH \
  go build -trimpath -tags ... \
  -o out/lactssl-dns-helper-extra \
  github.com/AXmishell/LACTSSL-Plugins/cmd/dns-helper
```

优点：

- 版本由主仓库 `go.mod` 固定，构建可复现。
- 不需要跨仓库 checkout、token、artifact 下载。
- 主仓库 CI 的 `govulncheck` 能覆盖实际链接进来的代码。

备选：从新仓库 Release 下载带 checksum 的产物。优点是插件可独立发版、主仓库构建更快；缺点是发布顺序耦合、需要校验与重试。

新仓库本身仍然发布三个 helper 的独立产物，方便旧版本主程序手动安装。

### D4：版本与兼容策略

- 新仓库使用 SemVer tag（`v0.1.0` 起步）。
- 主仓库 `go.mod` 固定版本；升级 plugins 版本时单独提 PR。
- `protocol` 保持 `v1`；协议破坏性变更才升 `v2`，主仓库需同时支持。
- 维护兼容矩阵：`LACTSSL v1.2.x ↔ LACTSSL-Plugins v0.3.x / protocol v1`。
- helper 的 `version` 字段：DNS 继续取 lego 上游版本（与现状一致）；部署/通知取模块版本。

### D5：i18n / outbound / Certificate

- `i18n`：新仓库只保留 `T`/`E`/`Default`，不依赖 gin；locale 片段按前缀过滤：
  - `meta.*`（catalog）
  - `service.deploy.*`（部署错误）
  - `notifications.*`、`plugin.notify.*`（通知）
- `outbound`：整包迁入新仓库。主仓库仍有监控/通知等使用点，可先保留副本；后续改为独立共享包或接受短时重复。
- `Certificate`：`protocol.Certificate` 直接作为部署协议类型；新仓库 `deploy` 包定义最小本地结构，避免依赖 `internal/model`。
- `EnvForProvider`：迁入 `catalog.Env`。主仓库删除对应的 `builtinProviderEnv` / `applyProviderEnv`，只保留 `catalog` 一份映射规则。

---

## 4. 分阶段实施

> 每阶段结束都应满足：`go build ./...`、`go test ./...`、`scripts/ci.sh` 通过；涉及 helper 的阶段还需 manifest 基线 diff 为空。

### P0：准备与基线

1. 处理主仓库未提交改动：提交，或在 `git worktree` 中执行迁移。
2. 固化迁移前 helper manifest：

```bash
mkdir -p /tmp/lactssl-baseline
CGO_ENABLED=0 go build -tags dns_extra -o /tmp/lactssl-baseline/dns-helper ./cmd/dns-helper
CGO_ENABLED=0 go build -o /tmp/lactssl-baseline/deploy-helper ./cmd/deploy-helper
CGO_ENABLED=0 go build -o /tmp/lactssl-baseline/notify-helper ./cmd/notify-helper
/tmp/lactssl-baseline/dns-helper manifest    > /tmp/lactssl-baseline/dns.json
/tmp/lactssl-baseline/deploy-helper manifest > /tmp/lactssl-baseline/deploy.json
/tmp/lactssl-baseline/notify-helper manifest > /tmp/lactssl-baseline/notify.json
```

3. 把三份 JSON 放入新仓库 `testdata/legacy-*.json`。
4. 记录当前服务商 / 目标 / 渠道数量：194 / 13 / 23。

**验收**：基线文件可重复生成，数量一致。

### P1：protocol + sdk

1. 新建 `go.mod`：

```go
module github.com/AXmishell/LACTSSL-Plugins

go 1.27.4
```

2. 迁移 manifest / 请求结构：
   - `cmd/dns-helper/main.go` 的 `request`
   - `internal/plugin/dns/exec_manifest.go` 的 `Manifest*`
   - 部署、通知同理
3. 增加 `protocol.ProtocolVersion = "v1"` 与环境变量常量。
4. 实现 `sdk/helper`：内存上限、子命令分发、JSON 输出、错误退出。
5. 主仓库三个 `exec_manifest.go` 改为类型别名（保持现有调用点不变）：

```go
type Manifest = protocol.DNSManifest
type ManifestProvider = protocol.DNSProvider
type ManifestField = protocol.DNSField
```

6. 为 manifest JSON 加 golden test，字段顺序/omitempty 与基线一致。

**验收**：主仓库编译通过；用旧 helper 生成的 manifest 与 `protocol` 序列化结果 diff 为空。

### P2：catalog

1. 把 `scripts/gen_dns_providers` 迁到新仓库，输出 `catalog/dns.go`。
2. 把主仓库手工维护的 35 条 `DNSProviders` 移入 `catalog/dns.go`，保持原有顺序。
3. 把 `PluginDeployTypes`、`DeprecatedDeployTypeCanonical` 移入 `catalog/deploy.go`。
4. 增加 `catalog/env.go`，迁入 `builtinProviderEnv` 与 Cloudflare 互斥逻辑。
5. 主仓库新增桥接：

```go
// internal/service/catalog_bridge.go
func init() {
    for _, p := range catalog.DNSProviders() {
        DNSProviders = append(DNSProviders, fromCatalogProvider(p))
    }
    PluginDeployTypes = fromCatalogDeployers(catalog.Deployers())
}
```

6. 删除主仓库 `dns_providers_gen.go` 与 `meta.go` 中已迁移的列表。

**验收**：
- 主仓库 `DNSProviders` / `PluginDeployTypes` 的顺序、字段、`Env` 与迁移前逐项一致（写一个对比测试）。
- 前端服务商列表、凭据表单无变化。

### P3：DNS 插件迁移

1. 复制 194 个 `internal/plugin/dns/providers/<id>` 到 `dns/providers/<id>`，import 改为新仓库 `dns` 包。
2. 新仓库实现插件侧注册表（`dns.Register/List/Lookup`），接口只保留 `ID/Aliases/New`。
3. `dns/all/all.go` 空导入全部包装包。
4. 迁移 `scripts/gen_dns_plugins`，只生成包装包与 `all` 清单；删除 profile、`lite` 配置和 `dns_extra` / `dns_all` 标签（helper 始终包含全部）。
5. 迁移 `cmd/dns-helper/main.go`：
   - manifest 使用 `catalog` + `i18n`
   - `present/cleanup` 使用 `catalog.Env`
   - 保留 `serviceVersion()` 读取 lego 版本的逻辑
6. 主仓库：
   - 删除 `internal/plugin/dns/providers/**`
   - 删除 `internal/plugin/dns/builtin/**`
   - 删除 `scripts/gen_dns_plugins`、`scripts/dns_plugins.json`
   - 删除 `service/acme.go` 中 builtin 分支与 `EnvForProvider` / `builtinProviderEnv` / `applyProviderEnv`
   - 删除 `Makefile` 的 `gen-plugins`
7. 对比 `dns-helper manifest` 与基线：provider 数量、ID、别名、字段、Env 必须完全一致。

**验收**：
- DNS helper 与旧版 manifest 仅 `version/name` 可有差异。
- 主仓库中 `grep -r "dns_all\|dns_extra\|builtin\."` 只应命中 `bundled`、文档历史说明或测试辅助代码。
- `go build ./cmd/server` 通过，且 `go mod tidy` 后不再包含只被全量版使用的 lego 服务商依赖。

### P4：部署 / 通知插件迁移

1. 复制 `deployplugin/providers/**` → `deploy/providers/**`。
2. `pluginutil` → `deploy/util`；`model.Certificate` → 本地最小证书结构。
3. 迁移 `outbound` 与精简 `i18n`。
4. 迁移 `notifyplugin/providers/channels/**` → `notify/providers/channels/**`。
5. 迁移两个 helper 入口：
   - 保留 `LACTSSL_DEPLOY_PLUGIN_FEATURES=merged-deployers-v1` 分支，兼容旧主程序。
   - 通知 helper 保留 stdout `{"ok":...}` 输出。
6. 删除主仓库对应 provider 包。

**验收**：三份 manifest 与基线一致；部署/通知单测全部迁入新仓库并通过。

### P5：主仓库接线

1. `go.mod` 增加依赖；开发期用 `go.work`（已 gitignore）或临时 `replace`，合并前删除 `replace`：

```go
require github.com/AXmishell/LACTSSL-Plugins v0.1.0
```

2. 删除 `cmd/{dns,deploy,notify}-helper`。
3. 构建脚本改为模块路径：

```make
PLUGINS_MODULE := github.com/AXmishell/LACTSSL-Plugins
HELPER_TAGS :=

helper:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
	  -o bin/$(APP)-dns-helper $(PLUGINS_MODULE)/cmd/dns-helper

deploy-helper:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
	  -o bin/$(APP)-deploy-helper $(PLUGINS_MODULE)/cmd/deploy-helper

notify-helper:
	mkdir -p bin
	$(BUILD_ENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
	  -o bin/$(APP)-notify-helper $(PLUGINS_MODULE)/cmd/notify-helper
```

4. 同步修改发布链路：
   - `Dockerfile`：3 处 `go build ./cmd/*-helper` 改为模块路径；删除 `TAGS` 中的 `dns_all` 分支
   - `.github/workflows/ci.yml`：删除 `dns_all` 全量构建校验
   - `.github/workflows/release.yml`：删除 `lactssl-full` 构建、`test -f` 与打包
   - `scripts/ci.sh`：删除全量版构建步骤与 `gen_dns_plugins` 校验
   - `scripts/release.sh`：删除 `lactssl-full` 构建、打包与产物校验
   - `Makefile`：删除 `build-full` / `backend-full` / `helper-full` / `FULL_TAGS` / `gen-plugins`
   - `docker-compose.yml`：删除 `TAGS: "embed dns_all"` 注释
5. `bundled/embed.go` 与 `bundled.Install` 不动；只是被嵌入的产物来源变化。
6. 文档更新：
   - `docs/dns-plugins.md`、`docs/deploy-plugins.md`、`docs/notify-plugins.md`
   - `docs/development.md`（构建矩阵、插件开发流程指向新仓库）
   - `docs/features.md`、`docs/install.md`、`docs/configuration.md`
   - `README.md` 的仓库结构与产物列表
   - 中英文同步；release notes 增加「移除 lactssl-full」的迁移说明

**验收**：
- `make build`、`make helper*`、`make deploy-helper`、`make notify-helper` 全部通过。
- `make build-full` 已不存在，仓库中不再有 `lactssl-full` / `dns_all` 的有效引用。
- `docker build` 通过。
- 主仓库 `go test ./...` 通过。
- 发布 dry-run 产物为 4 个二进制 + checksums。

### P6：新仓库 CI / 发布

1. `ci.yml`：
   - `go vet ./...`
   - `go test ./...`
   - 构建 3 个 helper（linux/amd64）
   - manifest golden 对比
   - `go build -tags ...` 交叉编译冒烟
2. `release.yml`：
   - tag 触发
   - 7 个平台矩阵：linux/darwin/freebsd（amd64/arm64）+ windows/amd64
   - 产物：
     - `lactssl-dns-helper-extra`
     - `lactssl-deploy-helper`
     - `lactssl-notify-helper`
   - `checksums.txt`
   - 可选：同时发布 `lactssl-dns-helper` 别名，兼容旧脚本
3. 主仓库发布：
   - `go.mod` 固定 plugins 版本
   - release 构建 helper 后打包 **4 个二进制**：`lactssl`、`lactssl-dns-helper-extra`、`lactssl-deploy-helper`、`lactssl-notify-helper`
   - 增加「plugins 版本」写入 release notes / build info 的步骤
4. 可选：主仓库 nightly 任务用最新 plugins 版本跑三份 manifest 兼容性测试。

**验收**：新仓库打 tag 后能产出全平台 helper；主仓库 release dry-run 产物齐全。

---

## 5. 兼容性与回归测试

### 5.1 必须的自动化检查

| 检查 | 位置 | 内容 |
|---|---|---|
| manifest 基线 diff | 新仓库 | 旧 helper 的 manifest 与新 helper 逐字段对比，忽略 `version`/`name` |
| catalog 计数 | 新仓库 | DNS 194、部署 13、通知 23 |
| ID/别名唯一性 | 新仓库 | 复用现有校验逻辑，额外跑全量重复检测 |
| 字段 Env 对比 | 主仓库 | 迁移前后 `DNSProviders[].Fields[].Env` 完全一致 |
| 默认启用列表 | 主仓库 | 35 个 DNS ID、7 个部署目标、23 个通知渠道均可解析 |
| 历史部署 ID | 主仓库 | `qiniu_cdn`/`qiniu_kodo`/`baota_*`/`1panel_*` 仍能映射到规范 ID；自动迁移时使用 `catalog.DeployMigrations()` 同步写入 `target` / `domain_type` |
| 协议请求兼容 | 新仓库 | 用基线请求 JSON 调用 present/deploy/send（mock 下游） |
| 上传 / 重扫 | 主仓库 | 用新 helper 走 `POST /api/plugins/upload` 与 `rescan` |
| 内嵌安装 | 主仓库 | `embed_helper` 构建后首次启动能安装 helper |
| 全量版移除 | 主仓库 | 仓库中不再有 `lactssl-full` / `dns_all` / `FULL_TAGS` / `build-full` 的有效引用；`-tags dns_all` 不影响构建结果 |

### 5.2 手工验收清单

- [ ] 全新数据目录启动，DNS helper 自动安装，35 家默认启用。
- [ ] 插件页显示 helper 数量、版本、错误信息正常。
- [ ] 上传同名 helper（允许/拒绝覆盖）行为不变。
- [ ] 停用某服务商后重启、重扫仍保持停用。
- [ ] 部署插件默认关闭；启用后 FTP/Webhook/七牛/面板目标出现。
- [ ] 通知插件安装后可发送测试通知。
- [ ] 历史 DNS 账户、部署凭据、通知配置在迁移后仍可读取。
- [ ] 从 `lactssl-full` 升级的用户按 release notes 切换到官方 helper 后，历史账户与任务正常。
- [ ] Windows 产物可执行（`.exe`、文件权限分支）。
- [ ] Docker 镜像内 helper 路径与版本正确。

---

## 6. 风险与回滚

| 风险 | 影响 | 缓解 |
|---|---|---|
| 未提交改动与迁移混在一起 | 难以 review / 回滚 | 先提交通知插件改造，或使用独立 worktree |
| 元数据漂移 | 表单字段、Env 名不一致导致签发失败 | D1 单一数据源 + 逐项对比测试 |
| 历史部署 ID 丢失 | 旧凭据不可用 | 保留 `DeprecatedDeployTypeCanonical` 与旧条目 |
| 移除 `lactssl-full` 影响存量用户 | 依赖全量版的部署升级后只能走 helper | release notes 明确说明；官方 DNS helper 随包内嵌并自动安装；保留上一版本产物可回退 |
| 移除内置路径后签发回归 | 服务商不可用时错误信息不清晰 | 保留 `dns_provider_unavailable` 错误；补 helper 缺失场景测试 |
| 模块路径大小写 / 代理 | CI 拉取失败 | 使用与远程一致的 `github.com/AXmishell/...`；CI 配置 GOPROXY |
| `go mod tidy` 引入多余依赖 | 主仓库依赖膨胀 | 只 import `protocol`/`catalog`；不再 import `dns/all` |
| helper 版本与宿主不匹配 | manifest 解析失败 | 协议 v1 冻结；发布说明标注兼容版本 |
| 跨平台权限检查 | Windows 加载失败 | 保持现有 `PathName()` 与权限分支；Windows 产物实测 |
| 私钥暴露面 | 安全审计 | 部署插件仍默认关闭；不改变 stdin 传参与环境白名单 |

**回滚策略**

- 迁移期间主仓库保留旧包，直到 P5 验收通过后再删除。
- 新仓库是增量仓库，回滚只需把主仓库 `go.mod` 固定回上一版或 revert P5 提交。
- 发布产物保留上一版本的 helper 资产，用户可手动替换。

---

## 7. 工作量估算

| 阶段 | 内容 | 预估 |
|---|---|---|
| P0 | 基线、worktree、脚本 | 0.5 人日 |
| P1 | protocol + sdk | 1 人日 |
| P2 | catalog 抽取 + 主仓库桥接 | 2–3 人日 |
| P3 | DNS 194 包 + helper + 删除内置/全量版路径 | 1.5–2 人日 |
| P4 | 部署/通知 + i18n/outbound/Certificate | 2 人日 |
| P5 | 主仓库接线 + 构建/发布脚本 + 文档（含全量版移除） | 2 人日 |
| P6 | 新仓库 CI/发布 + 联调 | 1 人日 |
| **合计** | | **约 10–11.5 人日** |

> 移除 `dns_all` 会额外涉及发布矩阵、Docker、CI、文档四处清理，但省去了适配器与长期双路径维护。

---

## 8. 决策与实施结果

已确认并落地：

- catalog **迁入新仓库**，主仓库通过 `go.mod` import。
- **已移除** `dns_all` 全量版，只保留 helper。
- helper 产物由主仓库按 `go.mod` 固定的模块版本现场构建；CI/Docker 在构建阶段 checkout 对应 tag 并临时 `replace`。
- i18n 文案：本仓库保留 `meta.*` / `notifications.*` / `service.deploy.*` / `service.notify.*` 片段；主仓库 UI 使用完整 locale。
- 新仓库首发版本：`v0.1.0`。

实施结果：

| 项目 | 结果 |
|---|---|
| DNS manifest | 194 家，与迁移前逐字段一致 |
| 部署 manifest | 旧主程序 10 个 / 新主程序 7 个，均与迁移前一致 |
| 通知 manifest | 23 个渠道，逐字段一致 |
| 主仓库依赖 | `go.mod` 71 行、`go.sum` 585 行 |
| 主仓库测试 | `go test ./...` 通过 |
| 新仓库测试 | `go test ./...` 通过（含 manifest 基线对比） |
| 宿主发现 | 用新 helper 实测 `Discover`：DNS/部署/通知均成功加载，无错误 |

剩余人工步骤：

1. 提交并推送本仓库，打 tag `v0.1.0`。
2. 在主仓库执行 `go mod tidy`，把 `LACTSSL-Plugins v0.1.0` 的校验和写入 `go.sum`。
3. 删除本地 `go.work`（已被主仓库 gitignore）。
4. 在主仓库提交迁移改动；建议与通知插件改造分开提交。
