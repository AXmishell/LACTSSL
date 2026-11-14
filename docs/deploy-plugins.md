# 部署插件

LACTSSL 的证书部署目标分为两类：

- **内置目标**：本地文件与 SSH/SFTP。这两类目标直接编译在主程序中，不依赖插件。
- **部署插件**：FTP/FTPS、Webhook、阿里云 CDN、七牛云、宝塔、1Panel、雷池 WAF 等目标由一次性 exec helper 提供，无需重新构建主程序。官方参考 helper 已包含这些目标。

!!! warning "部署插件会接收证书私钥"
    DNS 插件只接收域名 API 凭据，而部署插件必须拿到证书与私钥才能完成部署。
    请只安装可信来源的 helper，并确保插件目录仅服务运行用户可写。
    部署插件默认关闭，需要显式启用。

## 配置

```yaml
deploy_plugins:
  enabled: false          # 插件会接触私钥，默认关闭
  dir: ""                 # 留空表示 <data_dir>/plugins/deploy
  memory_limit_mb: 128    # 单个 helper 常驻内存上限
  timeout_seconds: 300    # 单次 deploy 超时，上传大文件时可调大
  max_output_kb: 64       # helper 输出上限
```

DNS 插件与部署插件分别使用 `<data_dir>/plugins` 下的两个子目录，避免两类 helper 混放：

- DNS 插件：`<data_dir>/plugins/dns`
- 部署插件：`<data_dir>/plugins/deploy`

也可以使用环境变量覆盖：

| 环境变量 | 说明 |
| --- | --- |
| `LACTSSL_DEPLOY_PLUGINS_ENABLED` | 是否启用部署插件 |
| `LACTSSL_DEPLOY_PLUGINS_DIR` | 插件目录 |
| `LACTSSL_DEPLOY_PLUGINS_MEMORY_MB` | 内存上限（MB），0 表示不限制 |
| `LACTSSL_DEPLOY_PLUGINS_TIMEOUT_SECONDS` | 单次执行超时（秒） |
| `LACTSSL_DEPLOY_PLUGINS_MAX_OUTPUT_KB` | 输出上限（KB） |

启动时自动扫描插件目录；安装新 helper 后可在「插件」页面点击「重扫插件」，或调用：

```bash
curl -X POST https://<host>/<entry>/api/plugins/rescan
```

## 上传插件

「插件」页的部署插件卡片同样提供「上传插件」，并且只有启用 `deploy_plugins.enabled` 后才可使用：

- 仅接受单个 helper 可执行文件，最大 256 MB；上传后先校验 `manifest`、部署目标 ID 与 `sha256`，通过后才写入部署插件目录并立即重扫。
- 默认拒绝覆盖同名文件；升级时勾选「允许覆盖同名插件」，旧文件会先备份，写入失败时自动恢复。
- 部署插件会接触证书私钥，上传前请确认来源可信；该操作始终要求二次验证。
- 与内置部署目标 ID 重复的插件仍会被发现，但重扫结果会标记为“已忽略”。
- 新增的非内置部署目标上传后默认停用，需要在部署目标列表中手动启用。

## 启用与停用部署目标

部署目标的启用状态由插件白名单管理：

- 首次启动（settings 中尚无白名单）时，迁移前内置的规范目标默认启用：`ftp`、`webhook`、`aliyun_cdn`、`qiniu`、`baota`、`1panel`、`safeline`；其对应旧 ID（如 `qiniu_cdn`、`qiniu_kodo`、`baota_site`、`baota_panel`、`1panel_site`、`1panel_panel`）也会保留在白名单中，用于兼容旧 helper 与历史数据。其他插件目标默认停用。
- 升级时如果白名单只有旧 ID，会自动补入合并后的规范 ID，避免目标因白名单缺失而消失。
- 启用状态保存在 settings 表的 `plugins.deploy.enabled_deployers`，只记录已启用的目标 ID，重启和重扫后保持。
- 只有已启用的目标才会出现在「目标凭据」和任务流程「部署证书」的类型列表中；停用后立即隐藏。
- 默认启用的目标同样可以停用；停用结果会持久化，不会因为 helper 重启或重扫而恢复。
- 「插件」页支持单个开关、全部启用和全部停用；操作后会立即重新探测插件目录并重建注册表。
- 本地文件与 SSH 不属于插件，始终可用，不能停用。

## 构建与安装参考 helper

源码运行时，推荐直接用 Makefile 构建并安装：

```bash
make deploy-helper-install
```

等价于构建 `bin/lactssl-deploy-helper` 并复制到默认插件目录：

```bash
make deploy-helper
mkdir -p data/plugins/deploy
cp bin/lactssl-deploy-helper data/plugins/deploy/deploy-helper
chmod 0755 data/plugins/deploy/deploy-helper
```

官方参考 helper 提供本地文件与 SSH 之外的全部目标：FTP/FTPS、Webhook、阿里云 CDN、七牛云、宝塔、1Panel 与雷池 WAF。发布包中会附带 `lactssl-deploy-helper`，也可用于理解与调试插件协议。

安装 helper 后还需要：

1. 在配置中设置 `deploy_plugins.enabled: true`；
2. 在「插件」页点击「重扫插件」，或调用 `POST /api/plugins/rescan`。

未启用或未安装 helper 时，目标凭据与任务流程的部署类型中只会出现本地文件与 SSH；历史凭据不会丢失，安装 helper 后可继续使用。

!!! note "Docker 镜像"
    官方镜像已附带 `/app/lactssl-deploy-helper`，启动时会自动复制到 `<data_dir>/plugins/deploy/deploy-helper`。同名文件会被镜像版本覆盖，自定义 helper 请使用其他文件名。

!!! note "内置目标不会被插件覆盖"
    本地文件与 SSH 始终由主程序实现。插件声明这两个 ID 时会被标记为“已忽略”。

## Helper 协议 v1

helper 是一个可执行文件，支持两个子命令：

```bash
deploy-helper manifest   # 输出 JSON 清单
deploy-helper deploy     # 从 stdin 读取请求并执行部署
```

`manifest` 输出示例：

```json
{
  "protocol": "v1",
  "name": "example deploy helper",
  "version": "1.0.0",
  "sha256": "可选的 helper 文件校验和",
  "deployers": [
    {
      "id": "example-panel",
      "name": "Example Panel",
      "description": "把证书推送到 Example 面板",
      "aliases": ["example"],
      "fields": [
        {"key": "panel_url", "label": "面板地址", "type": "text", "required": true},
        {"key": "api_token", "label": "API Token", "type": "password", "secret": true, "required": true}
      ],
      "deploy_fields": [
        {"key": "sites", "label": "站点", "type": "text", "required": true}
      ]
    }
  ]
}
```

字段说明：

- `fields` 是目标凭据中的认证与连接信息；`secret: true` 或 `type: "password"` 会在接口返回时脱敏，并随凭据一起加密存储。
- `deploy_fields` 是任务流程「部署证书」节点中的参数。
- 两类字段的 `key` 不能重复；字段类型支持 `text`、`password`、`textarea`、`number`、`select`、`switch`。
- 部署目标 `id` 只能包含小写字母、数字、下划线或连字符；与内置目标重复的插件会被忽略。

`deploy` 的 stdin：

```json
{
  "action": "deploy",
  "protocol": "v1",
  "provider": "example-panel",
  "auth": {"panel_url": "https://panel.example.com", "api_token": "..."},
  "config": {"sites": "example.com,www.example.com"},
  "certificate": {
    "domain": "example.com",
    "domains": ["example.com", "www.example.com"],
    "issuer": "Let's Encrypt",
    "serial": "...",
    "not_before": "2026-01-01T00:00:00Z",
    "not_after": "2026-04-01T00:00:00Z",
    "cert_pem": "-----BEGIN CERTIFICATE-----...",
    "fullchain_pem": "-----BEGIN CERTIFICATE-----...",
    "key_pem": "-----BEGIN PRIVATE KEY-----..."
  }
}
```

约定：

- 退出码 `0` 表示成功，非 `0` 表示失败；stderr 会进入部署记录（敏感值会被脱敏）。
- stdout 可以为空，或输出一行 JSON：`{"ok": true, "message": "..."}`。
  `ok` 为 `false` 时按失败处理；非法 JSON 会视为错误，因此调试日志请写入 stderr。
- helper 从 `LACTSSL_PLUGIN_MEMORY_LIMIT`（字节）获得内存预算，建议启动时调用 `debug.SetMemoryLimit`。
- 主程序会周期性检查 helper 的 `VmRSS`，超过 `memory_limit_mb` 时结束整个进程组并记录错误。
- 主程序只传递最小环境变量集合（PATH、HOME、代理、TLS 证书等），不会泄漏服务端密钥。
- 插件文件必须可执行、不能允许组或其他用户写入；`manifest.sha256` 非空时会校验文件摘要。
- 主程序会记录扫描时的文件摘要，并在每次部署前重新校验；替换 helper 后需要重扫插件目录，否则部署会直接拒绝。

## 安全建议

- 只启用可信 helper；部署插件可以读取证书私钥，等同于受信代码。
- 插件目录权限设为 `0700`，helper 权限设为 `0755`，属主为服务运行用户。
- 生产环境优先在 manifest 中填写 `sha256`，并在插件升级后更新摘要。
- 设置合理的 `memory_limit_mb` 与 `timeout_seconds`，避免异常插件拖垮主机。
- 插件以服务账号运行，不要给 `lactssl` 用户额外权限，也不要让插件目录位于共享可写路径。

## 排障

「插件」页面会显示已加载的 helper、部署目标、跳过项与错误。常见原因：

- 文件没有执行权限，或允许组/其他用户写入。
- manifest 协议不是 `v1`，或字段定义不合法。
- `sha256` 与文件摘要不一致。
- 部署目标 ID 与内置目标重复，被标记为“已忽略”。

插件未加载时，历史凭据仍会保留；凭据页面会提示插件缺失，且不会把配置写回数据库。
