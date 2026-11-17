# 部署插件

部署插件把证书交付到 FTP、Webhook、云 CDN、面板和 WAF 等目标。它是**接触证书私钥的高风险组件**，企业应把部署插件纳入准入和变更管理。

## 1. 内置目标与插件目标

| 类型 | 目标 | 是否内置 | 说明 |
| --- | --- | --- | --- |
| 内置 | 本地目录 | 是 | 写本机路径，可选重载命令 |
| 内置 | SSH / SFTP | 是 | 密码或私钥认证，主机指纹校验 |
| 插件 | FTP / FTPS | 否 | 普通 FTP、显式 TLS、隐式 TLS |
| 插件 | Webhook | 否 | 自定义认证头，可跳过 TLS 校验（不建议） |
| 插件 | 阿里云 CDN | 否 | 更新域名证书 |
| 插件 | 七牛云 | 否 | CDN 加速域名或 Kodo 自定义源站域名 |
| 插件 | 宝塔面板 / 宝塔 WAF | 否 | 网站、面板或 WAF |
| 插件 | 1Panel | 否 | 网站或面板 |
| 插件 | 雷池 WAF | 否 | 按证书 ID 或域名匹配 |
| 插件 | 腾讯云 / 火山引擎 / AcePanel 等 | 否 | 以插件市场实际条目为准 |

> **部署插件会接收证书私钥。** 只安装可信来源的 helper，并确保插件目录仅服务运行用户可写。部署插件默认关闭，需要显式启用。

## 2. 凭据与部署参数

「目标凭据」只保存认证与连接信息，例如主机、端口、用户名、密码、面板地址和 API Token。证书路径、重载命令、部署域名、站点等参数在任务流程「部署证书」节点中填写。

这样设计的好处：

- 同一凭据可被多个任务复用。
- 凭据轮换不影响任务编排。
- 部署参数随任务版本留痕，便于审计和回滚。

## 3. 配置

```yaml
deploy_plugins:
  enabled: false          # 插件会接触私钥，默认关闭
  dir: ""                 # 留空表示 <data_dir>/plugins/deploy
  memory_limit_mb: 128    # 单个 helper 常驻内存上限
  timeout_seconds: 300    # 单次 deploy 超时，上传大文件时可调大
  max_output_kb: 64       # helper 输出上限
```

三类插件分别使用 `<data_dir>/plugins` 下的独立子目录：

- DNS：`<data_dir>/plugins/dns`
- 部署：`<data_dir>/plugins/deploy`
- 通知：`<data_dir>/plugins/notify`

修改配置后重启服务生效。启动时自动扫描；安装新 helper 后可在「插件」页点击「重扫插件」，或调用：

```bash
curl -X POST -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/plugins/rescan
```

## 4. 安装与启用

1. 在配置中设置 `deploy_plugins.enabled: true` 并重启。
2. 打开「插件」页，确认仓库地址并点击「刷新索引」。
3. 在「插件市场」中安装需要的部署目标。
4. 在部署目标列表中启用具体目标。

未安装或未启用插件时，目标凭据与任务流程的部署类型中只会出现本地文件与 SSH；历史凭据不会丢失，安装插件后可继续使用。

启用状态保存在 settings 表的 `plugins.deploy.enabled_deployers`，只记录已启用的目标 ID，重启和重扫后保持。合并目标在「目标凭据」中只显示一个厂商入口，具体产品在任务部署节点中通过「部署对象」选择。

## 5. 目标实践

### 5.1 本地目录

- 运行用户必须对目标目录有写权限。
- 建议使用原子写入：先写临时文件，再 `rename` 覆盖，避免读到半个文件。
- 重载命令示例：`nginx -s reload`、`systemctl reload nginx`、`systemctl reload postfix`。
- 重载命令失败不会回滚证书文件；需要在目标侧保留上一版。

### 5.2 SSH / SFTP

- 默认要求填写目标主机指纹（`SHA256:...`）。只有显式开启“首次连接自动信任主机密钥”时才采用 TOFU；后续指纹不一致会拒绝连接。
- 服务器更换密钥后需要清空旧指纹重新记录。
- 建议使用专用部署用户，只授予目标路径写权限和必要重载权限（sudoers 精确到命令）。
- 私钥认证优先于密码；私钥放在受控位置，设置口令并定期轮换。

### 5.3 FTP / FTPS

- 默认显式 TLS；只有明确选择 `none` 才使用明文 FTP。
- 证书私钥和登录密码会经过网络；生产优先 SFTP 或 FTPS。
- 限制目标 IP、端口和目录权限；不要使用共享账号。

### 5.4 Webhook

- 公网目标强制 HTTPS；回环、私网、`.local`、`.internal` 允许 HTTP。
- Webhook 会携带证书与私钥，接收方必须可信；建议使用独立 Token 和来源 IP 限制。
- 不建议开启“跳过 TLS 校验”；如必须，限定到内网自签目标。
- 接收方应做幂等和签名校验，避免重放。

### 5.5 云 CDN / 面板 / WAF

- 凭据使用专用子账号，只授予证书管理相关权限。
- 部署参数（域名、站点、证书名称）必须与目标系统一致。
- 目标系统开启 IP 白名单时，放行 LACTSSL 出口。
- CDN/WAF 可能有证书缓存；部署后验证实际生效证书。

## 6. 部署记录与验证

- 每次部署生成部署记录，可在证书详情和日志中查看结果。
- 手动部署优先复用证书所属任务的部署节点参数，也可以按目标单独填写本次参数。
- 本地/SSH 部署会执行重载命令；API 密钥必须显式拥有 `deploy:execute`。
- 部署失败不会自动回滚目标系统旧证书；需要在目标侧保留上一版或使用原子发布。

验证清单：

- [ ] 目标路径/站点正确。
- [ ] 证书链完整（fullchain）。
- [ ] 私钥与证书匹配。
- [ ] 服务已重载并实际加载新证书。
- [ ] 多实例/多节点全部更新。
- [ ] CDN/WAF 缓存已刷新。
- [ ] 浏览器/客户端显示新证书序列号或指纹。

## 7. 离线与手动安装

- 在「插件」页上传单个 helper，最大 256 MB；只有启用 `deploy_plugins.enabled` 后才可使用。
- 上传后先校验 `manifest`、部署目标 ID 与 SHA-256，通过后才写入部署插件目录并立即重扫。
- 默认拒绝覆盖同名文件；升级时勾选“允许覆盖同名插件”，旧文件会先备份。
- 与内置部署目标 ID 重复的插件会被发现，但重扫结果标记为“已忽略”。

## 8. Helper 协议 v1（集成开发）

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
      "name_i18n": {"zh-CN": "示例面板", "en-US": "Example Panel"},
      "description": "把证书推送到 Example 面板",
      "description_i18n": {
        "zh-CN": "把证书推送到 Example 面板",
        "en-US": "Push certificates to an Example panel"
      },
      "aliases": ["example"],
      "fields": [
        {
          "key": "panel_url",
          "label": "面板地址",
          "label_i18n": {"zh-CN": "面板地址", "en-US": "Panel URL"},
          "type": "text",
          "required": true
        },
        {
          "key": "api_token",
          "label": "API Token",
          "label_i18n": {"zh-CN": "API Token", "en-US": "API Token"},
          "type": "password",
          "secret": true,
          "required": true
        }
      ],
      "deploy_fields": [
        {
          "key": "sites",
          "label": "站点",
          "label_i18n": {"zh-CN": "站点", "en-US": "Sites"},
          "type": "text",
          "required": true
        }
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
- `name` / `description` / `label` / `placeholder` / `help` 是默认语言文案；可选 `*_i18n` 表按语言覆盖。
- 语言键使用 BCP 47 形式，例如 `zh-CN`、`en-US`；单个文案表最多 32 种语言。

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
- stdout 可以为空，或输出一行 JSON：`{"ok": true, "message": "..."}`；`ok` 为 `false` 时按失败处理。
- 非法 JSON 会视为错误；调试日志请写入 stderr。
- helper 从 `LACTSSL_PLUGIN_MEMORY_LIMIT`（字节）获得内存预算。
- 主程序周期性检查 helper 的 `VmRSS`，超过 `memory_limit_mb` 时结束整个进程组并记录错误。
- 主程序只传递最小环境变量集合，不会泄漏服务端密钥。
- 插件文件必须可执行、不能允许组或其他用户写入；`manifest.sha256` 非空时会校验。
- 主程序记录扫描时的文件摘要，并在每次部署前重新校验；替换 helper 后需要重扫。

## 9. 安全建议

- 只启用可信 helper；部署插件可以读取证书私钥，等同于受信代码。
- 插件目录 `0700`，helper `0755`，属主为服务运行用户。
- 生产在 manifest 中填写 `sha256`，并在升级后更新摘要。
- 设置合理的 `memory_limit_mb` 与 `timeout_seconds`，避免异常插件拖垮主机。
- 不给 `lactssl` 用户额外权限，不让插件目录位于共享可写路径。
- 部署目标使用最小权限账号，重载命令精确到具体服务。

## 10. 排障

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 部署类型只有本地/SSH | 插件未启用/未安装 | 启用 `deploy_plugins.enabled`，安装并启用目标 |
| 插件加载失败 | 权限/摘要/协议/符号链接 | 修正权限或重新安装 |
| SSH 指纹不匹配 | 服务器换密钥/配置错误 | 核对指纹，清空旧记录重试 |
| 部署成功但业务仍旧证书 | 未重载/缓存/多实例 | 配置重载命令，刷新缓存，检查所有节点 |
| Webhook 被拒绝 | 公网 HTTP/证书不受信 | 使用 HTTPS 或内网地址，检查 TLS |
| CDN/面板失败 | 凭据权限/参数/API 版本 | 检查目标侧权限和参数 |
| 部署超时 | 目标慢/大文件 | 调大 `timeout_seconds`，检查网络 |

更多排障见[监控与排障](troubleshooting.md)。

## 相关文档

- [插件治理](plugins.md)
- [产品能力](features.md)
- [DNS 插件](dns-plugins.md)
- [通知插件](notify-plugins.md)
