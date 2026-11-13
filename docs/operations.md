# 运维

## 备份

需要备份两部分：

1. `config.yaml`（或 `LACTSSL_CONFIG` 指向的文件）
2. 整个 `data/` 目录

`data/` 中至少包含 `lactssl.db`；运行时还可能有 `lactssl.db-wal`、`lactssl.db-shm`。请一并保留。

### 界面备份与恢复

「系统设置 → 备份与恢复」提供在线操作：

- **下载备份**：生成 ZIP 备份包，包含 SQLite 数据库快照（`lactssl.db`）、当前配置文件（`config.yaml`）和版本信息（`manifest.json`）。下载前需要重新输入当前账户密码（启用 TOTP 时还需动态验证码）；备份加密密码与账户密码相互独立。备份内容使用 AES-256-GCM 加密，恢复时必须输入同一密码。密码遗失将无法恢复该备份。
- **恢复备份**：上传 ZIP 备份包或单独的 `.db` 数据库文件，输入当前密码确认；加密备份还需输入备份解密密码。服务会把恢复内容写入 `data/restore-pending/` 并自动重启。Docker 部署会由 `restart: unless-stopped` 自动拉起；直接运行二进制时需要自行重新启动进程。
- **安装页导入**：系统尚未初始化时，可在 `/install` 的「高级功能」中直接导入备份，跳过创建管理员账户。导入后可继续使用备份中的账户登录。

恢复后请使用备份中的管理员账户重新登录。若备份包内的 `config.yaml` 使用了不同的 `jwt_secret`，现有登录会话会失效；如果使用了不同的 `salt`，数据库中的加密凭据只能在配套配置文件下解密，因此建议始终使用完整 ZIP 备份包恢复。若备份中的安全入口与当前不同，恢复后请查看服务启动日志中的新入口地址。

备份包内含密钥与加密凭据，请按机密文件保管。

### 命令备份

推荐先停止服务再复制，确保数据库处于一致状态：

```bash
# 在项目目录中
docker compose stop          # Docker 部署
# 或停止直接运行的进程

tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

备份包内含密钥与加密凭据，请按机密文件保管。

## 恢复

1. 停止服务。
2. 将备份中的 `config.yaml` 与 `data/` 放回原位置。
3. 确认权限：配置文件 `0600`，数据目录 `0700`。
4. 启动服务。

如果只恢复了数据库而丢失了 `config.yaml` 中的 `salt`，已加密的 DNS/部署/通知凭据将无法解密，需要重新填写。

## 版本号

版本号以 git tag 为唯一来源，仓库不再维护 `VERSION` 文件。

本地构建（`make build`、`./scripts/ci.sh`、`./scripts/rebuild-run.sh`）由 `scripts/version.sh` 通过 `git describe --tags --always --dirty` 推导版本号并去掉开头的 `v`：

- 最新 tag 为 `v1.0.1` 且之后有 3 个提交时，版本为 `1.0.1-3-gabc1234`；
- 工作区有未提交改动时追加 `-dirty`，例如 `1.0.1-3-gabc1234-dirty`；
- 仓库没有任何 tag 时退化为提交短哈希；git 不可用时（如 Docker 构建上下文）显示 `dev`。

构建脚本通过链接参数 `-ldflags -X 'lactssl/internal/api.Version=...'` 注入版本，构建完成后可在「设置 → 关于」查看构建版本、构建时间与架构。直接使用 `go run ./cmd/server` 时版本显示为 `dev`。

需要固定版本时可显式覆盖：`LACTSSL_VERSION=1.2.3 make build`。Docker 构建上下文不包含 `.git`，本地构建镜像时需要传入版本：

```bash
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
```

Compose 默认使用 `ghcr.io/axmishell/lactssl:latest`；如需固定镜像版本，可直接修改 `docker-compose.yml` 中的 `image`，例如 `ghcr.io/axmishell/lactssl:1.2.3`。

### 创建 Release 自动构建

发布 GitHub Release 会触发 `Release` workflow，自动构建并上传各平台产物：

```bash
gh release create v1.0.1 --generate-notes
```

workflow 会从 tag 检出源码，构建前端并嵌入二进制，产出：

- `lactssl_<version>_linux_amd64.tar.gz`
- `lactssl_<version>_linux_arm64.tar.gz`
- `lactssl_<version>_darwin_amd64.tar.gz`
- `lactssl_<version>_darwin_arm64.tar.gz`
- `lactssl_<version>_freebsd_amd64.tar.gz`
- `lactssl_<version>_freebsd_arm64.tar.gz`
- `lactssl_<version>_windows_amd64.zip`
- `checksums.txt`（SHA-256）

归档内包含二进制、`README.md`、`LICENSE` 和 `config.yaml.example`。版本号直接取自 tag（去掉 `v` 前缀），不依赖本地 git 推导，因此从任意检出的源码构建都能得到正确版本。

同时会构建 `linux/amd64`、`linux/arm64` 的 Docker 镜像并推送到 GHCR：

```text
ghcr.io/axmishell/lactssl:<version>
ghcr.io/axmishell/lactssl:<major>.<minor>
ghcr.io/axmishell/lactssl:<major>
ghcr.io/axmishell/lactssl:latest
```

预发布版本只推送完整的 `<version>` 标签，不会更新 `latest`、`<major>.<minor>` 和 `<major>`。首次发布后 GHCR 包默认私有，需要到包设置中手动改为 Public。

注意事项：

- workflow 只在 Release 正式发布时触发；使用 `gh release create --draft` 时，点击发布后才会开始构建。
- 首次使用前，`.github/workflows/release.yml` 必须已经包含在 tag 指向的提交中；release 事件使用 tag 上的 workflow 文件。
- 用 `gh release create <tag> <files>...` 预上传的文件如果与自动产物同名，会被 workflow 以 `--clobber` 覆盖；建议不要在创建时传文件。
- Release 发布后资产需要几分钟才构建完成；如需“资产就绪后再公开”，应先推送 tag，由 CI 构建完成后再创建 Release（需要另配 tag push 流程）。
- 重新构建指定 tag：`gh workflow run release.yml -f tag=v1.0.1`，或在 Actions 页面手动运行。
- 如果 Release 由 workflow 内部的 `GITHUB_TOKEN` 创建，不会再触发 `Release` workflow；本流程使用人工 `gh release create`，不受影响。

## 升级

源码部署：

```bash
git pull
make build
# 停止旧进程后启动 ./bin/lactssl
```

Docker 部署：

```bash
docker compose pull
docker compose up -d
```

升级前建议先备份 `config.yaml` 与 `data/`。数据库结构会在启动时自动迁移。

## 日志

- 界面「日志」页面：任务执行、证书签发、部署、通知、监控与设置变更。
- 进程输出：启动信息、安全入口、HTTP 错误与调度器状态。
- Docker：`docker compose logs -f lactssl`。

## 常见问题

**忘记安全入口**
查看启动日志中打印的入口地址，或查看数据库中 `security.entry` 设置。也可以在启动时用 `LACTSSL_ENTRY` 覆盖为新入口。

**反向代理后 Cookie 异常 / 限流不准**
配置 `trusted_proxies`，并确认代理传递了 `X-Forwarded-For` 与 `X-Forwarded-Proto`，见[安全](security.md)。

**证书续期后服务未加载新证书**
在任务流程的部署节点中配置重载命令，例如 `nginx -s reload` 或 `systemctl reload nginx`。目标凭据只保存认证与连接信息，不再保存路径和重载命令。

**数据库文件变大**
服务每 6 小时执行一次维护清理；可在日志页面确认调度器是否正常运行。历史证书可在「证书管理」中通过「一键清理」按状态、过期/撤销时长、签发时间、域名、颁发机构、来源等条件筛选删除，并可选择保留每个任务最新证书；清理后相关任务的证书引用会被清空。ACME 诊断记录可在「ACME 诊断」中按端点、状态和未使用时长清理，失效订单的授权与挑战会级联删除；删除账户会一并删除其历史订单，并使对应客户端需要重新注册。
