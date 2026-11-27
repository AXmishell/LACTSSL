# DNS 插件

DNS 插件为公共 CA 的 DNS-01 验证提供域名服务商 API 接入。重点是**凭据最小权限、可轮换、可审计、可离线部署**。

## 使用前提

- 已在「DNS 账户」中创建对应服务商的凭据。
- 已在「插件」页启用 DNS 类别和具体服务商。
- 域名所在 Zone 对该凭据可见，且允许创建/删除 TXT 记录。
- 出站可访问服务商 API；如经代理，确认 Go HTTP 客户端代理配置。
- 一张证书可跨多个 DNS 账户：在「DNS 账户 → 网域绑定」为每个账户声明 Zone，任务选择「自动按域名绑定」后按最长后缀路由 TXT 写入与清理；显式选择单一账户表示该账户覆盖全部域名。

所有服务商默认停用；历史 DNS 账户不会丢失，服务商未启用时签发会给出明确错误。

## 插件包选择

官方 DNS 实现已内置，默认全量构建覆盖约 235 个条目（228 个 lego v5 provider + 7 个兼容 ID），无需安装插件包。所有服务商默认停用，请在「插件」页按需启用。

需要更小的二进制或内存时，可使用精简构建：`make backend-dns-common` 只链接常用白名单，`make backend-dns-minimal` 仅保留 Cloudflare，`make backend-no-dns` 完全不编译内置 DNS。精简构建仍支持上传 helper。

## 安装与启用

1. 打开「插件」页，确认 DNS 类别已启用。
2. 在 DNS 服务商列表中启用需要的服务商；一次启用超过 10 个条目会要求确认。
3. 在「DNS 账户」中创建凭据，选择该服务商。

启用状态保存在 settings 表；关闭类别后条目不会注册到运行时，但已有勾选会保留。上传第三方 helper 需要登录会话或 `plugins:write`。

## 凭据治理

### 最小权限

- 使用专用子账号或 API Token，不使用个人账号、全局管理员密钥。
- 只授予目标 Zone 的 DNS 读写权限；不授予账户级支付、域名注册、删除 Zone 等权限。
- 服务商支持按域名/项目授权时，限制到实际需要签发的域名。
- 测试与生产使用不同凭据和 DNS 子账号。

### 轮换

- 记录凭据责任人、创建时间、权限范围、轮换周期。
- 先在 LACTSSL 更新凭据，再在服务商侧吊销旧凭据。
- 轮换后运行测试任务，确认 TXT 创建/清理正常。
- 人员离职、密钥泄露或服务商安全事件后立即轮换。

### 保管

- 凭据写入数据库前使用 AES-256-GCM 加密；接口返回时脱敏。
- 编辑账户时留空密钥字段表示保留原值。
- 不要把凭据写入任务名、备注、工单或日志。
- 备份包包含加密凭据，按机密文件管理。

## 常见服务商

| 服务商 | 主要凭据 | 备注 |
| --- | --- | --- |
| 阿里云 DNS（alidns）/ ESA（aliesa） | AccessKey ID / Secret | 可选 Region |
| Cloudflare | API Token，或 Email + Global API Key | 可选 API 上游地址 |
| DNSPod | API Token | |
| 腾讯云（tencentcloud） | SecretId / SecretKey | 可选 Region |
| 华为云 / 百度智能云 / 京东云 | AccessKey ID / SecretAccessKey（京东云为 AccessKey Secret） | 华为云需 Region，京东云可选 Region |
| 火山引擎（volcengine） | AccessKey / SecretKey | 可选 Region、API 主机与协议 |
| AWS Route 53 | AccessKey / SecretAccessKey | 可选 Region、Hosted Zone、私有区域 |
| Google Cloud DNS | 服务账号 JSON | 可选项目 ID |
| Azure DNS | Tenant ID / Client ID / Client Secret / Subscription ID / 资源组 | |
| DigitalOcean / Vultr / Linode / Hetzner | API Token / API Key | |
| Gandi / GoDaddy / Namecheap / NameSilo / Porkbun | 各服务商 API 凭据 | |
| PowerDNS（pdns） | API 地址 / API Key | 可选 Server 名称与 API 版本 |
| RFC 2136 动态更新 | DNS 服务器 | 可选 TSIG Key / Secret / 算法与 Zone 列表 |

- 内置实现覆盖 lego v5 的全部服务商（约 190 家）及少量兼容 ID。
- `manual` 服务商需要人工添加 TXT 记录，不纳入自动流程。
- Cloudflare、DNSimple、PowerDNS、火山引擎等支持自定义 API 上游地址，可用于代理、自建服务或兼容网关；完整列表以「DNS 账户」页下拉框为准。

## 离线与手动安装

内置服务商随主程序分发，离线环境无需额外安装。第三方 provider 可在「插件」页上传单个 helper（最大 256 MB）：上传时校验协议、ID 与 SHA-256，默认拒绝覆盖同名文件，升级时旧文件会先备份。手动上传的多服务商 bundle 会标记为 `manual`，不参与自动更新。

## 验证与排障

### 验证

1. 创建 DNS 账户并保存。
2. 创建测试任务，选择该账户和测试域名。
3. 运行任务，观察 TXT 创建、CA 验证与清理，检查任务日志和操作日志，并按需删除测试证书。

### 常见问题

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 服务商下拉框为空 | DNS 类别或服务商未启用 | 在「插件」页启用类别和服务商 |
| 凭据保存失败 | 必填字段缺失/格式错误 | 按表单和日志提示修正 |
| TXT 创建失败 | 权限/Zone/配额 | 检查 API 权限、域名和配额 |
| TXT 清理失败 | 权限或记录已被删除 | 手动清理残留 TXT |
| 验证超时 | DNS 传播慢/权威 DNS 未更新 | 检查 TTL、权威解析和 CAA |
| 自定义 API 地址失败 | 地址不可达/证书不受信 | 检查网络、TLS 和上游地址 |
| 插件加载失败 | 权限/摘要/协议 | 检查目录权限和安装记录 |

## Helper 协议 v1（集成开发）

helper 是一个可执行文件，支持四个子命令（运维人员通常不需要了解）：

```bash
dns-helper manifest   # 输出 JSON 清单
dns-helper present    # 从 stdin 读取请求，添加 TXT 记录
dns-helper cleanup    # 从 stdin 读取请求，删除 TXT 记录
dns-helper verify     # 从 stdin 读取请求，执行只读探测（可选）
```

`manifest` 输出示例：

```json
{
  "protocol": "v1",
  "capabilities": ["verify-v1"],
  "name": "example dns helper",
  "version": "1.0.0",
  "sha256": "可选的 helper 文件校验和",
  "providers": [
    {
      "id": "example-dns",
      "name": "Example DNS",
      "description": "示例服务商",
      "aliases": ["example"],
      "capabilities": ["verify-v1"],
      "fields": [
        {"key": "api_token", "label": "API Token", "type": "password", "required": true}
      ]
    }
  ]
}
```

`present` / `cleanup` 的 stdin：

```json
{
  "action": "present",
  "provider": "example-dns",
  "config": {"api_token": "..."},
  "domain": "example.com",
  "token": "acme-token",
  "key_auth": "key-authorization"
}
```

约定：

- 退出码 `0` 表示成功，非 `0` 表示失败；stderr 会进入任务日志（敏感值会被脱敏）。stdout 必须为空，`present/cleanup` 的输出会被视为错误。
- `verify` 必须只读：只允许列 zone、查记录，不得创建、修改或删除任何记录。只有声明 `verify-v1` 的服务商才会收到调用；未声明者由主程序回退到 `present/cleanup` 写测试（`mode=auto`），显式 `mode=read` 时返回不支持。
- `checks[].status` 取值：`ok`、`failed`、`skipped`、`unsupported`、`error`。
- helper 从 `LACTSSL_PLUGIN_MEMORY_LIMIT`（字节）获得内存预算；主程序周期性检查 `VmRSS`，超过 `plugins.memory_limit_mb` 时结束进程。
- 主程序只传递最小环境变量集合（PATH、HOME、代理、TLS 证书等），不会泄漏服务端密钥。
- helper 文件不能允许其他用户写入；`manifest.sha256` 非空时会校验文件摘要。
- `version` 表示 helper/服务商实现自身版本，不是 LACTSSL 构建版本。

## 安全建议

- 插件目录只允许服务运行用户写入，优先 `0700`；插件以服务账号运行，不要给 `lactssl` 用户额外权限。只安装可信来源的 helper，并在 manifest 中填写 `sha256`。
- 设置合理的 `memory_limit_mb` 与 `timeout_seconds`，避免异常插件拖垮主机；生产使用内部镜像，固定版本，关闭自动更新。
