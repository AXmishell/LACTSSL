# 官方插件 provider module

本目录是独立 Go module `lactssl/providers`，只依赖插件 SDK 与上游 provider SDK，不依赖宿主 `lactssl/internal` 包。

```text
api/          Native 接口与注册表（dns/deploy/notify）
catalog/      官方 provider 元数据与生成的 data.json
dns/lego5/    lego v5 DNS provider 包装、分组、元数据
deploy/       部署目标实现
notify/       通知渠道实现
```

宿主通过根 `go.mod` 的本地 `replace` 引用：

```text
replace lactssl/providers => ./internal/plugin/providers
```

常用命令：

```bash
cd internal/plugin/providers
go test ./...
go vet ./...
```

生成物由 `internal/plugin/Makefile` 的 `make manifest` / `make gen-v5` 维护，不要手改生成文件。
