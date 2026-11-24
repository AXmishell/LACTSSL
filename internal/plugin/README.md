# 官方插件实现

本目录是随主程序编译的官方 DNS / 部署 / 通知插件实现。外部插件开发 SDK 位于 [`plugins/`](../../plugins)。

## 目录分层

```text
host/          三类插件的宿主适配层：接口、注册表、exec 发现
providers/     独立 Go module（lactssl/providers）：api/、catalog/、dns/、deploy/、notify/
runtime/       通用宿主运行期：registry、discovery、exec、sandbox、install、metadata、icon
builtin/       官方实现到宿主注册表的适配与 import 清单
assets/icons/  图标数据
tools/helpers/ 独立 helper 与 signer（兼容/发布用）
scripts/       生成器与构建工具
```

依赖方向：

```text
builtin ──► host ──► runtime
   │          │
   │          └──► providers/api（Native 接口与注册表）
   └──────► providers/dns|deploy|notify（官方实现）
```

- `providers` 是独立 module，不得 import 宿主 `internal` 包；
- `host/` 可以 import `providers/api` 与 `providers/catalog`，但不得 import 具体实现；
- `runtime/` 不得 import `host/` 或 `providers/`；
- 只有 `builtin/`、`tools/helpers/`、`scripts/` 可以 import `providers/dns|deploy|notify`。

## 常用命令

```bash
make -C internal/plugin metadata-check   # 元数据/i18n/图标/plugins.json 一致性
make -C internal/plugin gen-v5           # 重新生成 lego v5 包装、分组、元数据
make -C internal/plugin manifest         # 生成聚合元数据与 plugins.json
make test-providers                      # 独立 providers module 测试
```

从仓库根目录：

```bash
make backend               # 全量 DNS provider
make backend-dns-common    # 常用 DNS provider 精简构建
make backend-dns-minimal   # 仅 cloudflare
make backend-no-dns        # 不内置 DNS，仅上传 helper
```

## 新增 provider

见 [CONTRIBUTING.md](CONTRIBUTING.md)。
