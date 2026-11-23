# 贡献指南（外部插件 SDK）

本目录只维护插件协议与外部开发 SDK。官方 DNS / 部署 / 通知实现、元数据、生成器和发布工具位于 [`internal/plugin`](../internal/plugin)。

## 修改范围

- `protocol/`：协议 v1 线格式。只允许新增可选字段；破坏性变更必须升 `v2`。
- `sdk/`：helper 框架与通用工具。保持向后兼容，避免引入重量级依赖。
- `outbound/`：SSRF 防护规则。修改时必须补充测试。
- `i18n/`：插件侧公共文案。新增 key 同步各语言文件。

## 检查

```bash
cd plugins
go test ./...
go vet ./...
gofmt -l .   # 输出应为空
```

官方实现的开发流程见 [`internal/plugin/CONTRIBUTING.md`](../internal/plugin/CONTRIBUTING.md) 和主仓库 [插件开发文档](../docs/zh/plugin-development.md)。

## 安全

不要提交真实 token、私钥或账号信息；用户可控 URL 必须经过 `outbound`；错误信息不得包含 secret。漏洞请按 [SECURITY.md](SECURITY.md) 私下报告。
