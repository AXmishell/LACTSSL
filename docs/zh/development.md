# 扩展开发：源码与构建

本文面向需要二次开发、私有化构建或参与贡献的研发/集成团队。**运维和最终用户不需要阅读本文**；生产部署请看[安装与启动](install.md)，插件使用请看[插件治理](plugins.md)。

## 1. 仓库结构

```text
cmd/server/          程序入口、启动与门禁入口初始化
internal/api/        Gin 路由、处理器、会话与限流
internal/auth/       JWT 与密码/TOTP 相关逻辑
internal/config/     config.yaml 加载与校验
internal/db/         SQLite 打开与迁移
internal/i18n/       后端国际化与语言资源
internal/logging/    进程日志、级别、格式、文件轮转与异步落库
internal/model/      数据库实体与任务流程
internal/acme/       内置 ACME 服务端协议
internal/service/    ACME 客户端、部署、通知、监控、调度
internal/plugin/     插件宿主：dns/deploy/notify、exec、install、meta、proto、repo
internal/store/      数据访问层
internal/util/       加密、校验等工具
internal/webui/      嵌入的前端构建产物
web/                 Vue 3 + Element Plus 前端
scripts/             开发脚本与 CI（TypeScript，由 Node 直接运行）
docs/                简体中文文档
docs/en/             英文文档
docs/zh-hant/        繁体中文文档
docs/ja/             日语文档
docs/ru/             俄语文档
```

插件实现、官方 helper、插件协议与元数据位于独立仓库 [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins)。主仓库在宿主内实现协议 v1 的 manifest 解码，两个仓库通过签名索引与 `min_host` 做运行期兼容，构建期不再互相依赖。

## 2. 本地开发

### 2.1 环境

- Go 1.27+
- Node.js 22.12+ 与 npm（Vite 8 要求）
- 可选：Docker / Docker Compose v2

### 2.2 常用命令

```bash
make dev          # 后台启动 Vite + Go 开发模式
make dev-down     # 停止开发进程
make dev-restart  # 重启开发进程
make dev-status   # 查看 PID / 端口 / 健康状态
make dev-logs     # 跟踪后端与 Vite 日志
make build        # 生产构建，产物 bin/lactssl
make backend      # 仅构建后端，产物 bin/lactssl
make server-dev   # 开发模式后端，产物 bin/lactssl-dev（不嵌入前端）
make run          # 直接 go run ./cmd/server
make test         # go test ./...
make typecheck    # 前端 vue-tsc + scripts 的 tsc
make ci           # 完整本地 CI
make i18n-check   # 校验翻译键一致（默认含中英、繁体中文）
make i18n-check-all # 全量校验所有语言（含俄语、日语）
make tidy         # go mod tidy + 前端依赖安装
make clean        # 清理构建产物
make docs-build   # 构建中英文文档（strict）
make docs-serve   # 本地预览双语文档
```

### 2.3 开发模式端口

`make dev` 启动：

- Vite：`:5173`，带 HMR，前端源码。
- Go：默认 `:8080`，读取 `config.yaml` 的 `server.addr` / `server.port`。

访问 `http://<host>:5173/<entry>/`。Vite 会把 `/api` 和旧版 `/<entry>/api` 代理到 Go；Go 的 `8080` **不会**反向代理 `5173`，生产运行时没有 Vite。

开发模式脚本：

```bash
./scripts/dev.sh start            # 后台启动 Go 后端 + Vite
./scripts/dev.sh status           # PID / 端口 / 健康状态
./scripts/dev.sh logs panel       # 跟踪后端日志（logs / logs web / logs all）
./scripts/dev.sh restart --build  # 重新编译 bin/lactssl-dev 后重启
./scripts/dev.sh stop             # 停止
```

PID 文件与日志位于 `data/run/`：

```text
data/run/panel.pid  data/run/panel.log
data/run/web.pid    data/run/web.log
```

常用选项：

| 项 | 说明 |
| --- | --- |
| `--no-web` | 只启动后端，不启动 Vite |
| `--build` | 启动/重启前执行 `make server-dev` 重新编译 |
| `WEB_PORT` / `DEV_HOST` | Vite 端口（默认 `5173`）与监听地址（默认 `0.0.0.0`） |

后端端口与门禁入口统一从 `config.yaml` 读取；配置文件不存在时由 `config.yaml.example` 生成。

## 3. 构建与版本注入

- 前端构建产物先输出到 `web/dist`，再复制到 `internal/webui/dist` 并以 `embed` 标签嵌入二进制。
- 版本号以 git tag 为唯一来源，通过 `scripts/version.sh` 调用 `git describe --tags --always --dirty` 推导（去掉 `v` 前缀），并用 `-ldflags -X 'lactssl/internal/api.Version=...'` 注入。
- 构建时间与架构同理；未注入时版本显示为 `dev`。
- 仓库不维护 `VERSION` 文件。
- 需要固定版本时可显式覆盖：

```bash
LACTSSL_VERSION=1.2.3 make build
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
```

## 4. 测试与 CI

```bash
make test
make typecheck
make i18n-check
make ci
```

`make ci` 等价于 `.github/workflows/ci.yml`，也可以拆分：

```bash
./scripts/ci.sh --backend-only
./scripts/ci.sh --frontend-only
./scripts/ci.sh --no-build
```

CI 还会运行 `gofmt` 检查、`go vet`、带 `-race` 的测试与覆盖率统计，以及前端类型检查和构建。

## 5. 国际化

- 后端语言资源位于 `internal/i18n/locales/`。
- 前端语言资源位于 `web/src/i18n/locales/`。
- 新增文案时需同时补充 `zh-CN`、`en-US` 与 `zh-TW`，然后运行 `make i18n-check`。
- 当前已内置繁体中文 `zh-TW`、俄语 `ru-RU` 与日语 `ja-JP`：`zh-TW` 默认参与校验，缺少文案时直接回退 `zh-CN`；俄语、日语默认不参与校验，缺少文案时按 `语言 → en-US → zh-CN` 回退，同步翻译后可用 `make i18n-check-all`（或 `I18N_CHECK_ALL=1`）做全量校验。
- 检查脚本是 TypeScript（`scripts/check-i18n.ts`），由 Node 原生类型擦除直接运行，需要 Node 22.18+。
- 脚本自身的类型检查已并入 `make typecheck`（配置见 `scripts/tsconfig.json`）。

## 6. 文档构建

文档使用 Zensical/MkDocs：

```bash
make docs-build   # 构建全部语言，strict 模式
make docs-serve   # 后台值守预览（默认端口 8000；-l 指定语言；scripts/docs-serve.sh stop 停止）
```

- 英文配置：`mkdocs.yml`，输出 `site`（默认站点）。
- 简体中文：`mkdocs.zh.yml` → `site/zh`。
- 繁体中文：`mkdocs.zh-hant.yml` → `site/zh-hant`。
- 日语：`mkdocs.ja.yml` → `site/ja`。
- 俄语：`mkdocs.ru.yml` → `site/ru`。
- `docs-internal/` 是内部设计记录，不进入站点。
- 新增页面需要在 `docs/<语言>/` 下同步镜像，并更新各语言配置的 nav。

## 7. 发布流程

发布以 tag 为唯一来源。GitHub Release 会触发 `Release` workflow，自动构建并上传各平台产物：

```bash
gh release create v1.0.1 --generate-notes
```

产物包括：

- `lactssl_<version>_linux_amd64.tar.gz`
- `lactssl_<version>_linux_arm64.tar.gz`
- `lactssl_<version>_darwin_amd64.tar.gz`
- `lactssl_<version>_darwin_arm64.tar.gz`
- `lactssl_<version>_windows_amd64.zip`
- `checksums.txt`（SHA-256）

归档内包含二进制、`README.md`、`LICENSE` 和 `config.yaml.example`。版本号直接取自 tag（去掉 `v` 前缀），不依赖本地 git 推导。

同时构建 `linux/amd64`、`linux/arm64` 镜像并推送到 GHCR：

```text
ghcr.io/axmishell/lactssl:<version>
ghcr.io/axmishell/lactssl:<major>.<minor>
ghcr.io/axmishell/lactssl:<major>
ghcr.io/axmishell/lactssl:latest
```

预发布版本只推送完整 `<version>` 标签，不更新 `latest`、`<major>.<minor>` 和 `<major>`。首次发布后 GHCR 包默认私有，需要到包设置中改为 Public。

注意事项：

- workflow 只在 Release 正式发布时触发；`gh release create --draft` 在点击发布后才会开始构建。
- 首次使用前，`.github/workflows/release.yml` 必须已包含在 tag 指向的提交中。
- 用 `gh release create <tag> <files>...` 预上传的同名文件会被 workflow 以 `--clobber` 覆盖；建议不要在创建时传文件。
- Release 发布后资产需要几分钟构建完成；如需“资产就绪后再公开”，应先推送 tag，由 CI 构建完成后再创建 Release（需要另配 tag push 流程）。
- 重新构建指定 tag：`gh workflow run release.yml -f tag=v1.0.1`。
- 发布签名：配置 `ED25519_PRIVATE_KEY_B64` 后为 `checksums.txt` 生成分离签名；release workflow 与本地发布脚本会从私钥派生 32 字节公钥并嵌入官方二进制，使在线升级开箱即用。也可用 `LACTSSL_PROGRAM_PUBLIC_KEY` 直接指定要嵌入的公钥。

本地发布脚本：

```bash
make release TAG=v1.0.1
./scripts/release.sh v1.0.1 --no-docker
./scripts/release.sh v1.0.1 --multiarch
./scripts/release.sh v1.1.0-rc.1 --docker-only
./scripts/release.sh v1.0.1 --publish-only
./scripts/release.sh v1.0.1 --upload
```

`--multiarch` 需要 Docker Buildx；跨架构构建还需 QEMU。

## 8. 插件联调

插件源码、构建工具和索引位于 `LACTSSL-Plugins`。两个仓库不再共享 Go 模块，主仓库可以独立构建；需要同时修改插件实现并做端到端验证时，可临时创建 `go.work`：

```bash
go work init . ../LACTSSL-Plugins
```

修改插件实现后，在插件仓库执行 `make gen` 与 `make plugin KIND=dns GOOS=linux GOARCH=amd64` 构建合并包；联调结束后删除 `go.work`（该文件已被 gitignore）。

插件协议、字段和签名见[插件开发](plugin-development.md)。

## 9. 贡献注意事项

- 不要提交 `config.yaml`、`data/`、备份、日志或任何真实凭据。
- 修改安全相关逻辑时同步更新[安全基线](security.md)和测试。
- 修改 API/配置/插件行为时同步更新中英文文档。
- 新增接口必须补充 API 密钥权限策略和测试。
- 涉及数据迁移时提供幂等步骤、备份和回滚说明。
- 提交前运行 `make ci` 和 `make docs-build`。

## 相关文档

- [插件开发](plugin-development.md)
- [配置参考](configuration.md)
- [升级与变更](upgrade.md)
- [安全基线](security.md)
