# 扩展开发：插件协议与贡献指南

本文面向插件开发者、集成商和企业平台团队，说明 LACTSSL 插件协议、仓库结构、开发流程、构建、打包、索引、签名和发布。**运维人员通常只需阅读[插件治理](plugins.md)和对应的 DNS/部署/通知插件页面。**

官方 DNS/部署/通知实现位于本仓库 `internal/plugin/`，随主程序编译；`plugins/` 是外部插件开发 SDK（协议、helper 框架与通用工具）。上传插件仍使用协议 v1 的子进程通信。主程序不使用插件索引或外部插件仓库。

下文涉及的生成器、合并包与索引工具位于 `internal/plugin/`，命令默认在该目录执行；外部插件只需引用 `plugins/protocol` 与 `plugins/sdk`。

## 1. 架构与心智模型

官方插件在宿主进程内调用；上传插件仍是一次性子进程：

```text
LACTSSL 宿主
  └─ exec lactssl-dns-all-native <subcommand>
       ├─ manifest          输出插件自描述 JSON
       ├─ present / cleanup stdin JSON → 写/删 TXT → 退出
       ├─ verify            只读探测（可选）
       └─ 退出码 0 成功；非 0 失败，错误写 stderr
```

四类 helper：

| Helper | 类型 | 子命令 |
|---|---|---|
| `cmd/dns-helper` | DNS（v4 / native） | `manifest` / `present` / `cleanup` / `verify` |
| `cmd/dns-helper-v5` | DNS（lego v5） | `manifest` / `present` / `cleanup` / `verify` |
| `cmd/deploy-helper` | 证书部署 | `manifest` / `deploy` / `options` / `plan` / `verify` |
| `cmd/notify-helper` | 通知 | `manifest` / `send` |

插件仓库采用**模块化单体**：

- 每个 provider 一个目录；
- 元数据、文案、图标按 provider 拆分；
- 聚合文件由 `make manifest` 生成，禁止手改；
- 多个贡献者可以并行开发，减少单文件冲突。

## 2. 协议 v1

协议保持 `v1`；只有破坏性变更才升 `v2`。新能力通过可选字段与特性标记扩展，旧宿主会忽略无法识别的字段。

### 2.1 子命令与输出

DNS：

```text
dns-plugin manifest
dns-plugin present   # stdin JSON
dns-plugin cleanup   # stdin JSON
dns-plugin verify    # stdin JSON
```

部署：

```text
deploy-plugin manifest
deploy-plugin deploy
deploy-plugin options
deploy-plugin plan
deploy-plugin verify
```

通知：

```text
notify-plugin manifest
notify-plugin send
```

约定：

- 退出码 `0` 成功，非 `0` 失败；stderr 进入主程序日志。
- DNS `present` / `cleanup` 的 stdout 必须为空。
- 部署 `deploy` 成功时 stdout 为空；`verify` / `options` / `plan` 输出 JSON。
- 通知 `send` 成功输出一行 `{"ok":true,"message":""}`；失败也会输出 `{"ok":false,...}` 并以非 0 退出。
- 请求与响应均使用 UTF-8 JSON，非 ASCII 不转义。

### 2.2 环境变量

| 变量 | 说明 |
|---|---|
| `LACTSSL_PLUGIN_PROTOCOL` | 协议版本，当前 `v1` |
| `LACTSSL_PLUGIN_MEMORY_LIMIT` | 常驻内存预算（字节），helper 应调用 `debug.SetMemoryLimit` |
| `LACTSSL_DNS_PLUGIN_FEATURES` | DNS 特性列表，逗号分隔 |
| `LACTSSL_DEPLOY_PLUGIN_FEATURES` | 部署特性列表，逗号分隔 |

当前特性：

| 特性 | 含义 |
|---|---|
| `merged-deployers-v1` | 部署目标只输出合并后的规范 ID |
| `dynamic-options-v1` | 支持 `options` 子命令和 `options_source` / `multiple` |
| `verify-v1` | 支持 `verify` 只读探测 |
| `plan-v1` | 支持 `plan` 预匹配 |

### 2.3 安全约定

- 私钥、token 只通过 stdin 传递，不写入 argv 或环境变量；
- 主程序只传递最小环境变量集合；
- helper 必须可执行，且不能允许组或其他用户写入；符号链接会被拒绝；
- 主程序轮询 `VmRSS`，超过 `memory_limit_mb` 时结束整个进程组；
- `manifest.sha256` 可留空，仓库安装由索引 asset SHA-256 与安装记录保证完整性。

## 3. 仓库结构

```text
protocol/                    协议 v1 线格式
sdk/                         共享 HTTP、签名、DNS 工具、helper 框架
outbound/                    SSRF 防护出站 HTTP 客户端
catalog/
  dns/<id>.json              单 DNS provider 元数据（手写）
  deploy/<id>.json           单部署目标元数据（手写）
  migrations.json            历史部署 ID 与迁移
  data.json                  生成物：聚合元数据
i18n/
  core/<locale>.json         公共文案
  providers/<kind>/<id>/     单 provider 文案
  locales/<locale>.json      生成物：聚合文案
dns/providers/<id>/          lego v4 包装（生成）
dns/native/providers/<id>/   native DNS 实现（手写）
dns/groups/                  生成物：DNS 分组
dnsv5/                       lego v5 包装、分组、元数据（生成）
deploy/providers/<id>/       部署目标（手写）
notify/providers/channels/   每渠道一个 channel_<id>.go
cmd/                         helper 入口
assets/icons/                图标
plugins.json                 生成物：合并包清单
scripts/                     生成器与构建工具
```

生成物：

```text
catalog/data.json
i18n/locales/*.json
cmd/dns-helper/group_*.go
cmd/deploy-helper/imports_gen.go
dns/groups/**
dnsv5/**
plugins.json
```

修改源文件后运行：

```bash
make manifest
```

该命令依次执行 `catalog`、`i18n`、`deployimports`、`gen-v5`，再生成 `plugins.json` 与分组。CI 会校验生成物是否遗漏；同仓库 PR 还会由 `Update generated files` bot 自动回写。

## 4. 开发 DNS provider

### 4.1 Native 实现

位置：`dns/native/providers/<id>/provider.go`

```go
package example

import (
    "github.com/AXmishell/LACTSSL-Plugins/dns"
    "github.com/AXmishell/LACTSSL-Plugins/sdk/dnsutil"
)

type plugin struct{}

func (plugin) ID() string        { return "example" }
func (plugin) Aliases() []string { return nil }
func (plugin) New(cfg map[string]string) (dns.NativeChallengeProvider, error) {
    return newProvider(cfg)
}

func init() {
    dns.RegisterNative(plugin{})
    // 只有实现 Verify 时才声明
    // dns.MarkNativeVerifiable("example")
}
```

`ChallengeProvider` 只有两个方法：

```go
Present(domain, token, keyAuth string) error
CleanUp(domain, token, keyAuth string) error
```

DNS-01 的硬性规则：

- 写入 DNS 的值是 `dnsutil.TXTValue(keyAuth)`，不是 `keyAuth` 原文；
- 域名用 `dnsutil.ChallengeFQDN(domain)`；
- zone 匹配用 `dnsutil.ZoneName`，相对名用 `dnsutil.RelativeName`；
- `CleanUp` 只删除本次 TXT 值，保留其他值；记录不存在时视为成功；
- TTL 默认值与 lego 保持一致（常见 60s）。

可选只读验证：

```go
func (p *provider) Verify(ctx context.Context, domain string) (string, error)
```

实现后记得 `dns.MarkNativeVerifiable("example")`，否则宿主不会调用 `verify`。

### 4.2 lego v4 包装

`dns/providers/<id>` 由生成器产出：

```bash
go run scripts/gen_dns_plugins/main.go
make manifest
```

不要手改生成文件。需要调整别名、字段或环境变量时，改生成器或 `catalog/dns/<id>.json`。

### 4.3 lego v5

```bash
make gen-v5
make manifest
```

生成器会重写 `dnsv5/providers`、`dnsv5/legacy`、`dnsv5/groups`、`dnsv5/meta`。v5 新增 provider 的字段来自上游 `.toml`；catalog 已有同名条目时沿用历史字段。

### 4.4 元数据与文案

`catalog/dns/<id>.json` 示例：

```json
{
  "value": "example",
  "label_key": "meta.dns.example.label",
  "desc_key": "meta.dns.example.desc",
  "fields": [
    {
      "key": "token",
      "label_key": "meta.field.token.label",
      "type": "password",
      "required": true,
      "env": "EXAMPLE_TOKEN"
    }
  ]
}
```

`env` 只对 lego 包装生效；native 实现直接读取 `cfg["token"]`。

文案：

```text
i18n/providers/dns/<id>/zh-CN.json
i18n/providers/dns/<id>/en-US.json
```

公共字段文案放在 `i18n/core/<locale>.json`。缺失翻译会回退到默认语言，再回退到 `label` / `value`。

## 5. 开发部署目标

位置：`deploy/providers/<id>/plugin.go`

```go
type plugin struct{}

func (plugin) ID() string        { return "example" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(ctx context.Context, req deploy.NativeRequest) error { ... }

func init() { deploy.RegisterNative(plugin{}) }
```

可选能力：

| 接口 | 用途 | 声明 |
|---|---|---|
| `deploy.NativeVerifier` | 只读验证凭据/目标 | `deploy.MarkNativeVerifiable("example")` |
| `deploy.NativeOptionLister` | 动态下拉选项 | catalog 字段 `options_source` |
| `deploy.NativePlanner` | 批量部署前读取网站/对象 | `deploy.MarkNativePlannable("example")` |

开发步骤：

1. 创建 `deploy/providers/<id>/plugin.go`；
2. 创建 `catalog/deploy/<id>.json`，包含 `fields` 与 `deploy_fields`；
3. 创建 `i18n/providers/deploy/<id>/zh-CN.json` 与 `en-US.json`；
4. 运行 `make manifest` 生成 `cmd/deploy-helper/imports_gen.go`；
5. 添加 `httptest` 单测。

安全约定：

- 用户可控 URL 必须使用 `outbound.ValidateURL` / `outbound.ValidateWebhookURL`；
- 出站请求使用 `outbound.NewClient`，不要直接使用 `http.DefaultClient`；
- `Verify` / `Plan` / `ListOptions` 不得写证书、创建持久对象或 reload；
- 错误信息不包含 token/secret。

历史 ID 通过 `catalog/migrations.json` 的 `deprecated_deploy_types` 与 `deploy_migrations` 映射到规范目标。

## 6. 开发通知渠道

位置：`notify/providers/channels/channel_<id>.go`

```go
package channels

func init() {
    registerChannel(newChannel("example", []notify.NativeField{
        text("url", "notifications.fieldServerUrl", true),
        password("token", "notifications.fieldToken", true),
    }, sendExample))
}

func sendExample(cfg map[string]any, msg notify.NativeMessage) error { ... }
```

字段构造器：

| 函数 | 用途 |
|---|---|
| `text` | 文本 |
| `password` | 密码/密钥 |
| `number` | 数字 |
| `selectField` + `opt` | 下拉 |
| `textarea` | 多行文本 |

每个渠道独立文件，通过 `init` 自动注册，不需要修改共享列表。发送逻辑优先使用 `notifyJSON` / `notifyForm` / `notifyHTTP` 与 `outbound`。

## 7. 构建

```bash
# 全部合并包
make bundle

# DNS 单变体
make bundle-lego
make bundle-native
make bundle-lego-v5
```

构建参数：

```bash
CGO_ENABLED=0 go build -trimpath \
  -tags "bundle_dns_all_native,nethttpomithttp2,http2legacy" \
  -ldflags "-s -w -X main.buildVersion=<version>" \
  -o lactssl-dns-all-native ./cmd/dns-helper
```

- `-trimpath` 去掉本机路径；
- `-s -w` 去符号和 DWARF；
- `bundle_dns_all_lego` / `bundle_dns_all_native` 选择 DNS 分组；
- `nethttpomithttp2,http2legacy` 规避 Go 1.27 `net/http` 与新版 `x/net/http2` 的兼容问题；
- lego v5 额外使用 `urfave_cli_no_template,ozzo_validation_no_template,grpcnotrace,validator_novalidatefn` 裁掉模板代码。

## 8. 打包与索引

单包构建：

```bash
make plugin KIND=dns ID=all-native GOOS=linux GOARCH=amd64
```

产物：

```text
dist/
├── lactssl-dns-all-native_<version>_linux_amd64.tar.gz
└── lactssl-dns-all-native_<version>_linux_amd64.meta.json
```

包内容：

```text
plugin.json
lactssl-dns-all-native
LICENSE
icons/<provider>.<svg|png|webp>
```

`plugin.json` 包含版本、协议、bundle、providers、variant、conflicts、sdk 与图标引用。索引：

```bash
make index BASE_URL=https://github.com/AXmishell/LACTSSL-Plugins/releases/download
```

索引字段：

- `schema` / `channel` / `protocol` / `generated_at`；
- `plugins[]`：kind、id、version、bundle、providers、variant_of、conflicts_with、sdk、assets；
- `assets[GOOS/GOARCH]`：url、sha256、size、uncompressed_size；
- 根级 `icons[]`：内联 base64 图标，用于未安装时展示。

### 8.1 签名

```bash
go run scripts/pluginctl/main.go sign -key key.b64 -in index.json -out index.json.sig
```

`key.b64` 是 base64 编码的 Ed25519 私钥（32 字节种子或 64 字节私钥）。生产发布应强制配置 `PLUGIN_INDEX_SIGNING_KEY`，缺失时不应发布未签名索引。

## 9. 版本策略

- `all-lego` / `all-lego-v5` 版本跟随 `go.mod` 中的 lego 版本；
- `all-native`、`deploy/all`、`notify/all` 的补丁号由内容指纹自动递增；
- 公共输入（`protocol/`、`sdk/`、`i18n/`、`outbound/`、`catalog/`、`go.mod`）变化会触发所有相关包升版，避免“同版本不同内容”；
- provider 自身源码变化只影响对应类型的包；
- 发布以 tag 为准，非 tag 手动发布使用内部版本，需注意与已发布 tag 版本的先后关系。

## 10. 协作流程

### 10.1 分支与 PR

```text
main
  ↑
短分支（1～3 天）
  ↑
PR → lint / generated / test / bundle / native → CODEOWNER review
  → merge queue → CI gate → 合并
  → after-merge bot 刷新生成物
```

要求：

- 禁止直接 push `main`；
- 必须 PR，必须通过 `CI gate`；
- 使用 Merge Queue；
- 单 provider / 单目标 / 单渠道一个 PR；
- 生成物由 bot 回写，贡献者不要手工解决生成物冲突。

### 10.2 CODEOWNERS

插件仓库 `.github/CODEOWNERS` 定义默认 owner。团队扩大后按 area 替换为 GitHub Team：

- DNS native、lego、deploy、notify、infra；
- protocol / sdk / catalog schema / CI 需要核心维护者复核。

### 10.3 分层 CI

| 层级 | 触发 | 内容 |
|---|---|---|
| lint | 每个 PR / merge queue | gofmt、go.mod/go.sum |
| generated | 每个 PR / merge queue | `make manifest` + 生成物校验 |
| test | Go 变更 | `go vet ./...`、`go test ./...` |
| bundle | DNS/deploy/notify 变更 | 构建 5 个 helper、manifest 数量/一致性、打包 smoke |
| native | DNS/SDK 变更 | `check_native.sh` |
| nightly | 每天 | `go test -race`、全量构建、govulncheck |

分支保护只需要求 **CI gate** 一个检查；条件 job 被跳过时 gate 仍会通过。

### 10.4 Merge Queue

仓库设置：

- 保护 `main`；
- Require status checks：`CI gate`；
- Require linear history；
- 开启 Merge Queue（最大并发 5，等待超时 5 分钟）；
- 禁止直接 push；
- 启用 “Require branches to be up to date before merging”。

## 11. 本地调试

```bash
# 生成
make manifest

# native DNS manifest
go run -tags bundle_dns_all_native ./cmd/dns-helper manifest | jq '.providers | length'

# 查看单个 provider
go run -tags bundle_dns_all_native ./cmd/dns-helper manifest \
  | jq '.providers[] | select(.id=="linode")'

# 部署
LACTSSL_DEPLOY_PLUGIN_FEATURES=merged-deployers-v1,verify-v1,plan-v1,dynamic-options-v1 \
  go run ./cmd/deploy-helper manifest | jq

# 通知
go run ./cmd/notify-helper manifest | jq '.channels | length'
```

请求示例：

```bash
echo '{
  "action": "present",
  "provider": "linode",
  "config": {"token": "..."},
  "domain": "example.com",
  "token": "acme-token",
  "key_auth": "acme-token.thumbprint"
}' | go run -tags bundle_dns_all_native ./cmd/dns-helper present
```

## 12. 测试要求

- 单测使用 `httptest`，不访问真实网络；
- DNS 覆盖：写入值、清理只删本次值、记录不存在幂等、无匹配 zone；
- 部署覆盖：成功、鉴权失败、Verify/Plan 不写入；
- 通知覆盖：配置缺失、请求体、错误状态码；
- manifest 数量与字段由 CI 断言；
- native 包额外运行 `./scripts/check_native.sh`。

## 13. 提交前检查

```text
[ ] 只改自己的 provider 目录 + 对应 catalog/i18n
[ ] init 注册且 ID/alias 唯一
[ ] catalog 字段 key 与代码一致
[ ] 中英文文案都补齐
[ ] 单测使用 httptest
[ ] make manifest
[ ] gofmt / go vet / go test
[ ] check_native（native）
[ ] 生成物已提交或等待 bot 回写
```

## 14. 相关文档

- [插件治理](plugins.md)
- [DNS 插件](dns-plugins.md)
- [部署插件](deploy-plugins.md)
- [通知插件](notify-plugins.md)
- [插件仓库贡献指南](https://github.com/AXmishell/LACTSSL-Plugins/blob/main/CONTRIBUTING.md)
