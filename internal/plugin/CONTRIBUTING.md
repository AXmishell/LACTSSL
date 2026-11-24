# 官方插件实现贡献指南

本目录是 LACTSSL 官方 DNS / 部署 / 通知插件实现，随主程序编译。外部插件 SDK 位于 [`plugins/`](../../plugins)。

协议说明、宿主行为与上传插件接入见主仓库 [插件开发文档](../../docsite/docs/zh/plugin-development.md)。

## 开发环境

```bash
cd internal/plugin
go test ./...
go vet ./...
gofmt -l .   # 输出应为空
```

官方实现已内置，日常开发不需要构建或安装合并包。仅在需要独立 helper 或发布索引时使用本目录的 `Makefile`。

## 目录约定

```text
host/dns/                   DNS 宿主适配：接口、注册表、exec 发现、跨进程 recordID 状态
host/deploy/                部署宿主适配：接口、注册表、exec 发现
host/notify/                通知宿主适配：接口、注册表、exec 发现
providers/                  独立 Go module（lactssl/providers）
providers/api/              Native 接口与注册表（dns/deploy/notify）
providers/catalog/          服务商与部署目标元数据
providers/catalog/data.json 生成物：聚合元数据，禁止手改
providers/dns/lego5/        lego v5 包装、分组、元数据（生成）
providers/deploy/<id>/      部署目标实现
providers/notify/channels/  通知渠道实现
runtime/                    通用宿主运行期：registry、discovery、exec、sandbox、install、metadata、icon
builtin/                    官方实现到宿主注册表的适配与 import 清单
assets/icons/               图标
tools/helpers/              独立 helper 与 signer（兼容/发布用）
scripts/                    生成器与构建工具
plugins.json                生成物：合并包清单
```

分层约束：

- `providers/` 是独立 module，不得 import 宿主 `internal` 包；
- `host/` 可以 import `providers/api` 与 `providers/catalog`，但不得 import 具体实现；
- `runtime/` 不得 import `host/` 或 `providers/`；
- 只有 `builtin/`、`tools/helpers/`、`scripts/` 可以 import `providers/dns|deploy|notify`。

**生成物禁止手改。** 修改源文件后运行：

```bash
cd internal/plugin
make manifest
```

## 新增 DNS 服务商

1. 确认 `github.com/go-acme/lego/v5` 已包含目标 provider；
2. 运行 `make gen-v5`，生成 `providers/dns/lego5/providers`、`providers/dns/lego5/legacy`、`providers/dns/lego5/groups`、`providers/dns/lego5/meta`；
3. 在 `catalog/dns/<id>.json` 补充字段元数据；
4. 在 `plugins/i18n/providers/dns/<id>/` 补 `zh-CN.json` 与 `en-US.json`；
5. 运行 `make metadata-check`、`make manifest`、`go test ./...` 与 `make test-providers`。

## 新增部署目标

1. 创建 `providers/deploy/<id>/plugin.go`，实现 `host/deploy.NativeDeployer`；
2. 按需实现 `host/deploy.NativeVerifier` / `host/deploy.NativePlanner` / `host/deploy.NativeOptionLister`；
3. 创建 `catalog/deploy/<id>.json`，包含 `fields` 与 `deploy_fields`；
4. 在 `plugins/i18n/providers/deploy/<id>/` 补文案；
5. 运行 `make deployimports`（或 `make manifest`）；
6. 添加 `httptest` 单测，覆盖成功、鉴权失败与只读验证。

用户可控 URL 必须使用 `outbound.ValidateURL` / `outbound.NewClient`。`Verify` / `Plan` / `ListOptions` 不得写证书或修改目标系统。

## 新增通知渠道

在 `providers/notify/channels/` 下新增 `channel_<id>.go`，通过 `init` 注册；字段构造器与发送辅助见同目录现有实现。用户可控 URL 同样必须经过 `outbound`。

## 安全

不要提交真实 token、私钥或账号信息；错误信息不得包含 secret。漏洞请按 [SECURITY.md](SECURITY.md) 私下报告。
