# LACTSSL 插件开发 SDK

本目录面向**外部插件开发者**，提供 LACTSSL 插件协议 v1 的线格式、helper 框架与常用工具。官方 DNS / 部署 / 通知实现已经迁移到主仓库 [`internal/plugin`](../internal/plugin)，随主程序编译，不再放在这里。

模块路径保持 `github.com/AXmishell/LACTSSL-Plugins`，便于已有外部 helper 继续引用。

## 目录

```text
protocol/              插件协议 v1 的线格式（零第三方依赖）
sdk/helper/            helper 进程入口：子命令、JSON 读写、内存/panic 处理、特性协商
sdk/dnsutil/           DNS-01 TXT 值、FQDN、zone 解析等工具
sdk/httpapi/           带超时/重试的 HTTP 请求辅助
sdk/validate/          字段校验与 URL/主机名检查
sdk/otp/               TOTP 等双因素辅助
sdk/{alibaba,azure,baidu,gcp,huawei,ovh,sigv4,tencentcloud,ucloud,volcengine,...}/
                       云厂商签名与 API 适配（官方实现与第三方均可复用）
outbound/              带 SSRF 防护的出站 HTTP 客户端
i18n/                  插件侧公共文案与语言枚举
```

官方实现的元数据、文案、图标、构建工具和生成器位于 [`internal/plugin`](../internal/plugin)。

## 最小 helper

一个上传插件是**单个可执行文件**，通过子命令与宿主交换 JSON：

```go
package main

import (
	"os"

	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/helper"
)

func main() {
	defer helper.Recover()
	helper.ApplyMemoryLimit()
	if len(os.Args) < 2 {
		helper.Fail("用法: my-plugin manifest|send")
	}
	switch os.Args[1] {
	case "manifest":
		helper.WriteJSON(protocol.NotifyManifest{ /* ... */ })
	case "send":
		// 从 stdin 读取 protocol.NotifyRequest，执行后写 stdout
	default:
		helper.Fail("未知子命令: " + os.Args[1])
	}
}
```

宿主会设置以下环境变量：

| 变量 | 含义 |
|---|---|
| `LACTSSL_PLUGIN_PROTOCOL` | 协议版本，当前为 `v1` |
| `LACTSSL_PLUGIN_MEMORY_LIMIT` | 常驻内存预算（字节） |
| `LACTSSL_DNS_PLUGIN_FEATURES` / `LACTSSL_DEPLOY_PLUGIN_FEATURES` | 宿主支持的 DNS/部署特性列表 |

`manifest` 必须输出合法 JSON；DNS 使用 `protocol.DNSManifest`，部署使用 `protocol.DeployManifest`，通知使用 `protocol.NotifyManifest`。字段、别名、多语言与 `show_if` 等约定见 [`protocol/`](protocol)。

## 开发与测试

```bash
cd plugins
go test ./...
go vet ./...
```

本模块不依赖第三方库，修改后不会影响主程序依赖图。宿主通过主仓库 `go.mod` 的本地 `replace` 引用本模块以获得协议与 SDK；发布物仍是单一主程序。

## 协议与接入

- 插件以一次性子进程运行，凭据通过 stdin JSON 传入，不写入磁盘。
- 上传插件默认在 Linux Landlock / rlimit 受限环境中执行；不支持 strict 沙箱的平台降级为 basic 资源限制。
- 完整字段说明、示例与宿主行为见主仓库文档：[扩展开发：插件协议与贡献指南](../docsite/docs/zh/plugin-development.md)。
- 安全问题请按 [SECURITY.md](SECURITY.md) 私下报告。
