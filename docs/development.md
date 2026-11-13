# 开发

## 常用命令

```bash
make dev          # Vite + Go 开发模式
make rebuild-run  # 重新构建前端与后端二进制，并启动 Vite + Go
make build        # 生产构建，产物 bin/lactssl
make run          # 直接 go run ./cmd/server
make test         # go test ./...
make typecheck    # 前端 vue-tsc + scripts 的 tsc
make ci           # 完整本地 CI
make i18n-check   # 校验中英文翻译键一致
make tidy         # go mod tidy + 前端依赖安装
make clean        # 清理构建产物
```

`make ci` 等价于 `.github/workflows/ci.yml`，也可以用参数拆分：

```bash
./scripts/ci.sh --backend-only
./scripts/ci.sh --frontend-only
./scripts/ci.sh --no-build
```

`make release` 等价于 `.github/workflows/release.yml`，默认 dry-run：按发布矩阵交叉编译并打包到 `dist/`，生成 `checksums.txt`，本地构建 Docker 镜像（不推送），不上传 Release：

```bash
make release TAG=v1.0.1                # 指定 tag
./scripts/release.sh v1.0.1 --no-docker
./scripts/release.sh v1.0.1 --multiarch   # buildx 构建 amd64+arm64，导出 OCI 镜像并校验平台
./scripts/release.sh v1.1.0-rc.1 --docker-only   # 只验证镜像标签（预发布只打精确版本）
./scripts/release.sh v1.0.1 --publish-only       # 只检查将要上传的资产列表
./scripts/release.sh v1.0.1 --upload             # 用 gh 上传到同名 Release（覆盖同名资产）
```

`--multiarch` 需要 Docker Buildx；跨架构运行构建阶段时还需 QEMU（`docker run --privileged --rm tonistiigi/binfmt --install arm64`）。

## 重新构建运行

改完代码需要“重新构建 + 重启”时：

```bash
make rebuild-run
```

等价于 `./scripts/rebuild-run.sh`，依次执行：

1. 构建前端到 `web/dist`
2. 以开发模式编译 `bin/lactssl-dev`（不带 `-tags embed`，运行时读取 `web/dist`）
3. 结束占用配置端口（默认 `8080`）与 `5173` 的旧进程
4. 启动 Vite（`5173`）与 Go 二进制（`8080`）

访问 `http://<host>:5173/<entry>/`。安全入口默认读取 `config.yaml` 的 `security.entry`；入口只保存在数据库时，用环境变量覆盖：

```bash
LACTSSL_ENTRY=xxxxxxxx make rebuild-run
```

脚本以前台方式运行，`Ctrl+C` 会同时结束 Vite 与 Go。只改前端时用 `make dev` 即可，Vite 热更新无需重复构建。

## 目录结构

```text
cmd/server/          程序入口、启动与安全入口初始化
internal/api/        Gin 路由、处理器、会话与限流
internal/auth/       JWT 与密码/TOTP 相关逻辑
internal/config/     config.yaml 与环境变量加载
internal/db/         SQLite 打开与迁移
internal/i18n/       后端国际化与语言资源
internal/model/      数据库实体与任务流程
internal/acme/       内置 ACME 服务端协议（directory、nonce、账户、JWS、验证）
internal/service/    ACME 客户端、部署、通知、监控、调度
internal/store/      数据访问层
internal/util/       加密、校验等工具
internal/webui/      嵌入的前端构建产物
web/                 Vue 3 + Element Plus 前端
scripts/             开发脚本与 CI（TypeScript，由 Node 直接运行）
```

## 构建与版本注入

- 前端构建产物先输出到 `web/dist`，再复制到 `internal/webui/dist` 并以 `embed` 标签嵌入二进制。
- 版本号以 git tag 为唯一来源，通过 `scripts/version.sh` 调用 `git describe --tags --always --dirty` 推导（去掉 `v` 前缀），并用 `-ldflags -X 'lactssl/internal/api.Version=...'` 注入；构建时间与架构同理。仓库不再维护 `VERSION` 文件。
- 发布流程以 tag 为准，详见[运维](operations.md#版本号)；用 `LACTSSL_VERSION` 可覆盖本地构建的版本号。
- 未注入时版本显示为 `dev`。

## 前端开发

`make dev` 会启动 Vite（`:5173`）和 Go（`:8080`）。Vite 开发服务器会模拟安全入口校验，并读取 `config.yaml` 中的 `security.entry`；也可以使用 `LACTSSL_ENTRY` 覆盖。

开发时请访问 Vite 地址，`/<entry>/api` 请求会被代理到 Go 服务。

注意代理方向是单向的，**8080 不会反向代理 5173**：

| 端口 | 谁在监听 | 前端资源来源 | 与另一端口的关系 |
| --- | --- | --- | --- |
| `5173` | Vite 开发服务器（仅开发模式） | `web/` 源码，带 HMR | 把 `/<entry>/api` 代理到 `8080` |
| `8080` | Go 二进制（开发与生产相同） | `internal/webui/dist` 中嵌入二进制的前端产物 | 不代理 `5173`，也不依赖 Vite |

## 国际化

后端语言资源位于 `internal/i18n/locales/`，前端位于 `web/src/i18n/locales/`。新增文案时需同时补充 `zh-CN` 与 `en-US`，然后运行：

```bash
make i18n-check
```

检查脚本是 TypeScript（`scripts/check-i18n.ts`），由 Node 原生类型擦除直接运行，需要 Node 22.18 及以上；脚本自身的类型检查已并入 `make typecheck`（配置见 `scripts/tsconfig.json`）。

## 测试

```bash
make test
```

CI 还会运行 `gofmt` 检查、`go vet`、带 `-race` 的测试与覆盖率统计，以及前端类型检查和构建。
