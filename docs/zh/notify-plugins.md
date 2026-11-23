# 通知插件

通知插件把证书签发、部署、监控和系统事件推送到企业 IM、邮件或 Webhook。B 端使用时重点关注**告警路由、渠道可用性、密钥轮换和噪音控制**。

## 1. 内置与插件渠道

| 类型 | 渠道 | 是否内置 | 说明 |
| --- | --- | --- | --- |
| 内置 | 邮件（SMTP） | 是 | 支持 SSL；默认隐式 TLS |
| 插件 | Gotify、ServerChan、ntfy | 否 | 自建/第三方推送 |
| 插件 | Telegram、Bark、PushDeer | 否 | 移动端推送 |
| 插件 | 钉钉、企业微信机器人/应用/智能机器人、飞书 | 否 | 企业 IM |
| 插件 | GoCQHTTP、iGot、PushPlus、WePlus、WxPusher、WxPusher SPT、OpenILink、WPush、Synology Chat、PushMe、ChronoCat | 否 | 其他渠道 |
| 插件 | 通用自定义 Webhook | 否 | 对接内部告警平台 |

未安装或未启用 helper 时，通知页面只会出现邮件；历史渠道配置不会丢失，安装 helper 后可继续使用。

## 2. 配置

```yaml
notify_plugins:
  enabled: true           # 默认开启
  dir: ""                 # 留空表示 <data_dir>/plugins/notify
  memory_limit_mb: 128    # 单个 helper 常驻内存上限
  timeout_seconds: 30     # 单次 send 超时
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

## 3. 安装与启用

1. 打开「插件」页，确认仓库地址并点击「刷新索引」。
2. 打开「插件」页的通知标签，启用需要的渠道。
3. 先启用插件文件，再在渠道列表中启用具体渠道；新下载的文件与条目默认停用。
4. 在「通知」页面配置渠道凭据并发送测试消息。

启用状态保存在 settings 表的 `plugins.notify.enabled_channels`，重启和重扫后保持。只有已启用的渠道才会出现在「通知」页面与任务通知节点中。

邮件不属于插件，始终可用，不能停用。

## 4. 企业告警设计

### 4.1 路由

| 事件 | 建议去向 | 说明 |
| --- | --- | --- |
| 证书签发失败 | 运维值班 | 需要立即处理，可能影响续期 |
| 部署失败 | 运维 + 目标系统负责人 | 检查目标系统和重载命令 |
| 证书临期 | 运维 + 业务负责人 | 30/14/7 天分级 |
| 站点监控异常 | 运维 | 区分网络、链、主机名问题 |
| 备份失败 | 平台 | 与证书告警分开 |
| 安全事件 | 安全 | 异常登录、插件校验失败等 |
| 测试消息 | 独立测试频道 | 不要进入生产值班群 |

### 4.2 降噪

- 为不同环境使用不同渠道和频道。
- 任务通知节点按“始终/仅成功/仅失败”选择，避免成功消息淹没失败。
- 同类事件设置聚合或静默窗口（由外部 IM/告警平台实现）。
- 监控项按业务重要性分级，避免对不稳定目标频繁告警。
- 定期清理失效渠道和重复配置。

### 4.3 可用性

- 至少配置两个独立渠道（如邮件 + 企业 IM），避免单点。
- 定期发送测试消息，确认 Token/Webhook 未过期。
- 关注渠道服务商的速率限制和内容审核。
- 通知失败会写入日志，建议把通知失败本身也纳入监控。

## 5. 渠道凭据治理

- 使用专用机器人/应用，不使用个人账号 Token。
- 只授予发送消息所需权限，不授予组织管理、通讯录读取等权限。
- 为不同环境/团队使用不同机器人。
- 记录 Token 责任人、创建时间、轮换周期。
- 凭据在写入数据库前加密；接口返回时脱敏。
- 不要把 Token、Webhook URL 写入任务名、备注或日志。
- 泄露时立即在渠道侧吊销并更新 LACTSSL 配置。

## 6. 邮件（SMTP）

- 默认使用隐式 TLS（`smtp.ssl=true`）。
- 关闭隐式 TLS 时，如果 SMTP 服务器不支持 STARTTLS，服务会拒绝发送，避免重置令牌等敏感邮件明文传输。
- 建议使用企业邮件网关、专用发件账号和 SPF/DKIM/DMARC。
- 恢复邮箱与通知发件账号应分开，避免互相影响。
- 测试邮件与生产通知使用不同收件人。

## 7. Helper 协议 v1（集成开发）

helper 是一个可执行文件，支持两个子命令：

```bash
notify-helper manifest   # 输出 JSON 清单
notify-helper send       # 从 stdin 读取请求并发送通知
```

`manifest` 输出示例：

```json
{
  "protocol": "v1",
  "name": "example notify helper",
  "version": "1.0.0",
  "sha256": "可选的 helper 文件校验和",
  "channels": [
    {
      "id": "example-push",
      "name": "Example Push",
      "description": "把通知推送到 Example 服务",
      "fields": [
        {"key": "url", "label": "服务地址", "type": "text", "required": true},
        {"key": "token", "label": "Token", "type": "password", "secret": true, "required": true}
      ]
    }
  ]
}
```

字段说明：

- `secret: true` 或 `type: "password"` 的字段会在接口返回时脱敏，并随渠道配置一起加密存储。
- 字段类型支持 `text`、`password`、`textarea`、`number`、`select`、`switch`。
- 渠道 `id` 只能包含小写字母、数字、下划线或连字符；与内置邮件 (`mail`) 重复的插件会被忽略。
- `label_key` 可选，官方前端会优先按 i18n key 本地化；第三方插件可直接提供 `label`。

`send` 的 stdin：

```json
{
  "action": "send",
  "protocol": "v1",
  "channel": "example-push",
  "config": {"url": "https://push.example.com", "token": "..."},
  "title": "证书签发成功: example.com",
  "body": "任务：example.com\n状态：成功"
}
```

约定：

- 退出码 `0` 表示成功，非 `0` 表示失败；stderr 会进入通知日志（敏感值会被脱敏）。
- stdout 可以为空，或输出一行 JSON：`{"ok": true, "message": "..."}`；`ok` 为 `false` 时按失败处理。
- 非法 JSON 会视为错误；调试日志请写入 stderr。
- helper 从 `LACTSSL_PLUGIN_MEMORY_LIMIT`（字节）获得内存预算。
- 主程序周期性检查 helper 的 `VmRSS`，超过 `memory_limit_mb` 时结束整个进程组并记录错误。
- 主程序只传递最小环境变量集合，不会泄漏服务端密钥。
- 插件文件必须可执行、不能允许组或其他用户写入；`manifest.sha256` 非空时会校验。
- 主程序记录扫描时的文件摘要，并在每次发送前重新校验；替换 helper 后需要重扫。

## 8. 安全建议

- 只启用可信 helper；通知插件可以读取渠道密钥，等同于受信代码。
- 插件目录 `0700`，helper `0755`，属主为服务运行用户。
- 生产在 manifest 中填写 `sha256`，并在升级后更新摘要。
- 设置合理的 `memory_limit_mb` 与 `timeout_seconds`，避免异常插件拖慢通知发送。
- 通知内容可能包含域名、错误详情和内部地址；避免发送到公开频道。
- Webhook 目标使用 HTTPS 和签名校验，防止伪造/重放。

## 9. 排障

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 通知页面只有邮件 | 通知插件未安装/未启用 | 安装并启用 notify 插件 |
| 渠道不在任务节点中 | 渠道未启用 | 在插件页和通知页启用 |
| 测试消息失败 | Token/URL/网络 | 检查渠道配置和出站策略 |
| 邮件发送失败 | SMTP TLS/认证/发件人 | 检查隐式 TLS、STARTTLS、SPF/DKIM |
| 通知延迟 | 渠道限流/网络 | 检查渠道状态和日志 |
| 通知内容含敏感信息 | 模板/日志脱敏不足 | 调整内容，避免发送凭据和私钥 |

更多排障见[监控与排障](troubleshooting.md)。

## 相关文档

- [插件治理](plugins.md)
- [产品能力](features.md)
- [DNS 插件](dns-plugins.md)
- [部署插件](deploy-plugins.md)
