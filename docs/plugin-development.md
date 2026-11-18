# 扩展开发：插件协议

本文面向插件开发者、集成商和企业平台团队，说明 LACTSSL 插件协议、构建、打包、索引、签名和发布。**运维人员通常只需阅读[插件治理](plugins.md)和对应的 DNS/部署/通知插件页面。**

## 1. 仓库结构

插件源码、构建工具与仓库索引位于独立仓库 [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins)。主程序在宿主内实现协议 v1 的 manifest 解码，运行期从插件仓库下载按类型合并的插件包；两个仓库不再通过 `go.mod` 共享源码。

```text
protocol/        插件协议 v1 的线格式（插件侧实现，宿主另有本地副本）
sdk/helper/      helper 启动框架：内存预算、JSON、错误退出
catalog/         插件内部的服务商 / 部署目标元数据（不对外提供 Go API）
i18n/            插件侧文案
outbound/        SSRF 防护出站 HTTP 客户端
dns/providers/<id>        194 个 DNS 服务商包装
deploy/providers/<id>     部署目标实现
notify/providers/channels 通知渠道实现
cmd/dns-helper/           DNS 合并包入口（全部服务商）
cmd/deploy-helper/        部署合并包入口（全部目标）
cmd/notify-helper/        通知合并包入口（全部渠道）
plugins.json              合并包清单
scripts/pluginctl         生成、构建、打包、索引、签名
```

## 2. 协议 v1

插件是可执行文件，每次调用启动、执行完退出。协议保持 `v1`；破坏性变更才升 `v2`。

### 2.1 子命令

DNS：

```text
dns-plugin manifest
dns-plugin present   # stdin JSON
dns-plugin cleanup   # stdin JSON
```

部署：

```text
deploy-plugin manifest
deploy-plugin deploy
```

通知：

```text
notify-plugin manifest
notify-plugin send
```

### 2.2 约定

- 退出码 `0` 成功，非 `0` 失败；stderr 进入主程序日志。
- DNS `present/cleanup` 的 stdout 必须为空。
- 部署/通知的 stdout 可以为空，或输出一行 `{"ok": true, "message": "..."}`；非法 JSON 视为错误。
- 内存预算通过 `LACTSSL_PLUGIN_MEMORY_LIMIT`（字节）传入；建议启动时调用 `debug.SetMemoryLimit`。
- 主程序只传递最小环境变量集合（PATH、HOME、代理、TLS 证书等）。
- 主程序轮询 helper 的 `VmRSS`，超过 `memory_limit_mb` 时结束整个进程组。
- helper 文件必须可执行、不能允许组或其他用户写入；符号链接会被拒绝。
- `manifest.sha256` 可留空；仓库安装由索引 asset SHA-256 与安装记录保证完整性。

## 3. 合并包入口

DNS 常用/重型两个包共用一个入口、通过构建标签选择服务商；部署与通知各一个入口：

```bash
go build -trimpath -tags "bundle_dns_core,nethttpomithttp2"  -o lactssl-dns-core  ./cmd/dns-helper
go build -trimpath -tags "bundle_dns_heavy,nethttpomithttp2" -o lactssl-dns-heavy ./cmd/dns-helper
go build -trimpath -tags nethttpomithttp2 -o lactssl-deploy-all ./cmd/deploy-helper
go build -trimpath -tags nethttpomithttp2 -o lactssl-notify-all ./cmd/notify-helper
```

`manifest` 输出该类全部条目；`pluginctl` 在构建后会校验 manifest 与 `plugins.json` 中的 `providers` 列表完全一致。

`plugins.json` 维护合并包清单（`providers` 省略时只展示部分）：

```json
{
  "schema": 2,
  "plugins": [
    {"kind": "dns", "id": "core", "version": "1.0.0", "tag": "bundle_dns_core", "bundle": true, "providers": ["cloudflare", "..."]},
    {"kind": "dns", "id": "heavy", "version": "1.0.0", "tag": "bundle_dns_heavy", "bundle": true, "providers": ["gcloud", "..."]},
    {"kind": "deploy", "id": "all", "version": "1.0.0", "tag": "bundle_deploy_all", "bundle": true},
    {"kind": "notify", "id": "all", "version": "1.0.0", "tag": "bundle_notify_all", "bundle": true}
  ]
}
```

生成清单：

```bash
make gen
# 或
go run scripts/pluginctl/main.go manifest
go run scripts/pluginctl/main.go gen
```

DNS 服务商包装位于 `dns/providers/<id>`，每个包装暴露一个 lego 服务商，最终链接进同一个 DNS 合并 helper。新增或更新服务商时，修改 `dns/providers`、更新 `plugins.json` 中的版本并重新发布对应插件。

## 4. 打包

```bash
make plugin KIND=dns ID=core GOOS=linux GOARCH=amd64
```

产物：

```text
dist/
├── lactssl-dns-core_1.0.0_linux_amd64.tar.gz
└── lactssl-dns-core_1.0.0_linux_amd64.meta.json
```

包内容：

```text
tar.gz
├── plugin.json
├── lactssl-dns-core
├── icons/                 # 可选：该包覆盖的服务商图标
│   ├── cloudflare.svg
│   └── alidns.svg
└── LICENSE
```

`plugin.json`：

```json
{
  "schema": 1,
  "kind": "dns",
  "id": "core",
  "version": "1.0.0",
  "protocol": "v1",
  "os": "linux",
  "arch": "amd64",
  "entrypoint": "lactssl-dns-core",
  "bundle": true,
  "providers": ["cloudflare", "..."],
  "icons": {
    "cloudflare": { "file": "icons/cloudflare.svg", "sha256": "…" }
  }
}
```

图标来自仓库 `assets/icons/<kind>/<provider>.svg|png|webp`，`icons.json` 可声明历史 ID 的复用关系。`build` 会按 `providers` 收集图标；单张解码后不超过 64 KB，只允许 svg / png / webp。`sha256` 对图标原始字节计算，解包时校验，缺失或多余的图标都会导致安装失败。

`build` 会在当前平台运行一次 `manifest`，校验条目集合与版本；交叉编译平台跳过执行。索引中的 `uncompressed_size` 是解压后 helper 的大小，也是插件市场展示的磁盘占用。索引根级的 `icons` 内联这些图标，用于未安装时的市场展示。

## 5. 索引

```bash
make index BASE_URL=https://github.com/AXmishell/LACTSSL-Plugins/releases/download
```

生成的 `index.json`：

```json
{
  "schema": 1,
  "channel": "stable",
  "protocol": "v1",
  "generated_at": "2026-11-15T10:00:00Z",
  "plugins": [
    {
      "kind": "dns",
      "id": "core",
      "name": "DNS 插件包（常用）",
      "version": "1.0.0",
      "protocol": "v1",
      "bundle": true,
      "providers": ["cloudflare", "..."],
      "uncompressed_size": 60596384,
      "assets": {
        "linux/amd64": {
          "url": "https://.../plugin-assets-dns/lactssl-dns-core_1.0.0_linux_amd64.tar.gz",
          "sha256": "…",
          "size": 8019751
        }
      }
    }
  ]
}
```

- 索引只保留每个插件的最新版本。
- 每个资产必须包含 64 位十六进制 SHA-256；宿主缺失或格式错误时直接拒绝下载。
- `min_host` 可选，用于限制最低宿主版本。
- `assets` 以 `GOOS/GOARCH` 为键。

### 5.1 签名

```bash
go run scripts/pluginctl/main.go sign -key key.b64 -in index.json -out index.json.sig
```

`key.b64` 是 base64 编码的 Ed25519 私钥（32 字节种子或 64 字节私钥）。主程序配置公钥后，会校验 `index.json.sig`；asset 再校验 SHA-256。

## 6. 发布

`.github/workflows/release.yml`：

- 按 `plugins.json` 生成矩阵，每个插件一个 job。
- 首期平台：linux amd64/arm64、darwin arm64、windows amd64。
- 每个插件发布一个 Release，tag 为 `<kind>-<id>-v<version>`。
- 汇总全部 `.meta.json` 生成 `index.json`，发布到 `plugin-index` Release。
- 配置 `PLUGIN_INDEX_SIGNING_KEY` 后同时发布 `index.json.sig`。

## 7. 字段定义

DNS 字段示例：

```json
{
  "protocol": "v1",
  "name": "example dns helper",
  "version": "1.0.0",
  "sha256": "可选的 helper 文件校验和",
  "providers": [
    {
      "id": "example-dns",
      "name": "Example DNS",
      "description": "示例服务商",
      "aliases": ["example"],
      "fields": [
        {"key": "api_token", "label": "API Token", "type": "password", "required": true}
      ]
    }
  ]
}
```

DNS `present` / `cleanup` 的 stdin：

```json
{
  "action": "present",
  "provider": "example-dns",
  "config": {"api_token": "..."},
  "domain": "example.com",
  "token": "acme-token",
  "key_auth": "key-authorization"
}
```

部署目标字段：

- `fields`：目标凭据中的认证与连接信息。
- `deploy_fields`：任务流程「部署证书」节点中的参数。
- 两类字段的 `key` 不能重复；字段类型支持 `text`、`password`、`textarea`、`number`、`select`、`switch`。
- `secret: true` 或 `type: "password"` 会脱敏并加密存储。
- `name` / `description` / `label` / `placeholder` / `help` 支持 `*_i18n`，语言键使用 BCP 47（如 `zh-CN`、`en-US`），单表最多 32 种语言。

通知渠道字段同理；渠道 `id` 不能与内置邮件 `mail` 重复。

部署 `deploy` 的 stdin 与通知 `send` 的 stdin 见[部署插件](deploy-plugins.md)和[通知插件](notify-plugins.md)。

## 8. 兼容性

- 协议保持 `v1`；破坏性变更才升 `v2`。
- 每个插件独立 SemVer；升级时修改 `plugins.json` 中的版本并重新发布。
- 主程序按 `protocol` 与 `min_host` 过滤不兼容插件。
- 合并包 manifest 可以包含多个条目；当前发布物包括 DNS 常用、DNS 大型云厂商、部署、通知，以及 DNS 全量·重构包。
- 新增字段应保持向后兼容；旧版主程序会忽略未知可选字段。

## 9. 开发检查清单

- [ ] `manifest` 输出合法 JSON，协议为 `v1`。
- [ ] ID 唯一，符合小写字母/数字/下划线/连字符规则。
- [ ] 字段定义完整，敏感字段标记 `secret` 或 `password`。
- [ ] 退出码、stdout/stderr 约定正确。
- [ ] 内存预算、超时、输出大小在限制内。
- [ ] helper 文件权限正确，不允许组/其他用户写入。
- [ ] 图标格式和大小符合要求。
- [ ] 索引包含 SHA-256，`min_host` 正确。
- [ ] 签名可验证。
- [ ] 在目标平台运行一次 `manifest` 和实际调用。
- [ ] 更新文档和变更说明。

## 相关文档

- [插件治理](plugins.md)
- [DNS 插件](dns-plugins.md)
- [部署插件](deploy-plugins.md)
- [通知插件](notify-plugins.md)
- [扩展开发：源码与构建](development.md)
