# 插件治理

LACTSSL 的 DNS、证书部署与通知能力由插件提供。官方插件实现位于 `internal/plugin/` 并编译进主程序；`plugins/` 是外部插件开发 SDK。上传的第三方 helper 仍以一次性子进程运行，默认受沙箱限制。

## 1. 插件模型

- **内置插件**：官方 DNS / 部署 / 通知实现随主程序发布，不需要联网安装。
- **上传插件**：管理员可以上传单个 helper，用于临时验证或第三方扩展；默认停用，启用后以子进程执行。
- **两级启停**：
  - 类别级：`plugins.enabled`、`deploy_plugins.enabled`、`notify_plugins.enabled`；
  - 条目级：DNS 服务商、部署目标、通知渠道白名单。
- **不再有插件仓库**：没有索引、签名索引、市场、自动更新；官方插件更新随主程序版本发布。

插件协议保持 v1：

```text
manifest / present / cleanup / deploy / options / plan / send
```

内置实现直接在宿主进程内调用；上传插件通过 stdin/stdout JSON 与宿主通信。

## 2. 配置

```yaml
plugins:
  enabled: true
  dir: ""                 # 上传 DNS helper 目录，留空 <data_dir>/plugins/dns
  sandbox: strict         # strict / basic / off
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64

deploy_plugins:
  enabled: false          # 部署插件接触私钥，默认关闭
  dir: ""
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64

notify_plugins:
  enabled: true
  dir: ""
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64
```

`sandbox` **只作用于上传/外部 helper**；内置插件在宿主进程内直接调用，不经过沙箱。`strict` 在 Linux 使用 Landlock 文件访问白名单、rlimit、`no_new_privs` 和独立进程组；内核或容器不支持时自动降级为 `basic`（最小环境变量、私有临时目录、超时/RSS/输出限制），并记录一次降级日志。若宿主进程仍以 root 运行且未设置 `LACTSSL_RUN_UID`/`LACTSSL_RUN_GID`，上传插件会被拒绝执行：Landlock 无法阻止 root 通过 mount/ptrace 等方式绕过。`off` 仅用于受控调试。

## 3. 内置插件与内存

- 内置实现默认全部编译进主程序；关闭类别或条目不会卸载代码，但会避免注册对应实现和构造 provider。
- 需要显著减小二进制/内存时，可使用构建标签：
  - `make backend-no-dns`：完全不编译 DNS 内置实现，闲置 RSS 约 36 MB；
  - 官方发布默认全量（约 70 MB 闲置 RSS）。
- 全量构建的主要内存来自 DNS 的 lego v5 代码页，而不是插件元数据（元数据约 1 MB）；启用全部 235 个服务商相对默认空列表只增加约 3–5 MB。
- DNS 内置实现通过全局构造锁设置并恢复 lego v5 环境变量；`present/cleanup` 可并发执行。

## 4. 启用与停用

### 4.1 类别

类别开关决定该类内置实现是否注册。关闭后：

- 该类内置条目不会出现在可用列表中；
- 上传的同类 helper 也不会执行（避免“类别关闭但插件仍可用”的认知不一致）；
- 已保存的账户、任务和白名单保留。

### 4.2 条目

- DNS：默认不启用任何服务商；安装后需要在「插件」页手动启用。
- 部署：类别默认关闭；启用类别后默认启用 25 个规范目标。
- 通知：默认启用 23 个渠道。

一次启用超过 10 个条目会要求确认，避免误开大量 provider。

## 5. 上传插件与沙箱

上传流程：

1. 选择 DNS / 部署 / 通知类别与 helper 文件；
2. 在沙箱中执行 `manifest` 探测，校验协议、ID、版本、摘要和平台；
3. 写入插件目录；新文件默认停用；
4. 管理员启用文件后，再按条目启用。

限制：

- 只接受单个可执行文件，最大 256 MB；
- 拒绝符号链接、组/其他用户可写文件；
- 上传插件不能占用内置条目 ID/别名；
- 默认 `strict` 沙箱：只能读取系统 CA/DNS 配置和自身文件，只能写入私有临时目录；不能读取数据目录、配置、数据库或其他插件；
- 沙箱不是网络隔离：上传插件仍可访问出站网络，请只上传可信来源。

## 6. 更新、回滚与卸载

- 内置插件随主程序升级，不能单独安装/更新/卸载；升级后启用状态保留。
- 上传插件由管理员重新上传；旧文件会先备份，失败时回滚。
- 删除上传文件会同时清理指纹、条目元数据和启用白名单。

## 7. 旧仓库插件迁移

升级前安装过官方仓库插件的部署：

1. 官方插件现在由内置实现提供，旧 helper 文件应停用或删除；
2. 账户、任务和启用白名单保持不变；
3. 不属于官方集合的自定义插件保留为“旧版/上传”插件，默认按 `strict` 沙箱执行；
4. 旧插件不再有更新来源，建议替换或删除。

## 8. 安全控制

- 内置插件是主程序供应链的一部分，随程序签名/镜像摘要发布。
- 上传插件默认沙箱执行；生产环境不要配置 `sandbox: off`。
- 部署插件接触私钥，默认关闭，只启用可信来源。
- 插件目录 `0700`，helper `0755`，不允许符号链接和组/其他用户写入。
- 上传 helper 的摘要会在发现、注册和每次调用前复核。
- 插件管理需要 `plugins:read` / `plugins:write`；上传、启停均写入审计日志，strict 不可用时的降级会写入宿主启动日志。

## 9. API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/plugins` | 内置/上传插件状态、条目与文件 |
| POST | `/api/plugins/rescan` | 重新扫描上传插件 |
| POST | `/api/plugins/upload` | 上传 helper |
| DELETE | `/api/plugins/files/:kind/:file` | 删除上传 helper |
| PUT | `/api/plugins/files/:kind/:file` | 文件级启停 |
| PUT | `/api/plugins/providers/:id` | DNS 条目启停 |
| PUT | `/api/plugins/deployers/:id` | 部署条目启停 |
| PUT | `/api/plugins/notify-channels/:id` | 通知条目启停 |
| GET | `/api/plugins/icons` | 图标 |

插件仓库相关 API 已移除。

## 10. 排障

| 现象 | 原因 | 处理 |
|---|---|---|
| 内置条目不可用 | 类别关闭或条目未启用 | 检查类别配置与条目白名单 |
| 上传后没有条目 | 文件默认停用 | 启用文件后再启用条目 |
| 上传插件执行失败 | strict 沙箱阻止了合法文件/路径 | 检查错误；必要时显式降级 `basic` 并审计 |
| Landlock 不可用 | 内核/容器 seccomp 限制 | 使用较新内核或配置 `basic`；Docker 需验证 seccomp |
| ID 冲突 | 上传插件与内置条目重名 | 内置 ID 永久保留，换用其他 ID |
| 部署私钥风险 | 部署插件类别启用 | 只启用可信目标，默认保持关闭 |

## 相关文档

- [DNS 插件](dns-plugins.md)
- [部署插件](deploy-plugins.md)
- [通知插件](notify-plugins.md)
- [安全基线](security.md)
