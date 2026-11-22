# API 集成

LACTSSL 的控制台接口可同时通过浏览器登录会话和 API 密钥调用。API 密钥适合 CI/CD、监控巡检、CMDB 同步、证书分发等长期运行的程序；每个密钥可以单独配置权限、过期时间和启用状态，避免共享管理员密码。

> 账户安全、通行密钥、会话管理等敏感操作，以及备份下载/恢复、门禁入口修改和数据库切换，只允许浏览器登录会话调用；API 密钥不能完成交互式再认证。修改 SMTP/通知目标需要 `settings:write`，发送测试消息仍只允许浏览器会话。

## 1. 集成方式选择

| 场景 | 建议方式 | 原因 |
| --- | --- | --- |
| 人工签发、排障、改配置 | 浏览器控制台 | 可用完整审计和交互确认 |
| CI/CD 触发续期/部署 | API 密钥 + `tasks:run` / `deploy:execute` | 最小权限、可轮换 |
| 证书清单同步到 CMDB | API 密钥 + 只读权限 | 不暴露私钥 |
| 证书下载并分发 | API 密钥 + `certificates:key` + `deploy:execute` | 高敏感，需额外网络限制 |
| 监控巡检 | API 密钥 + `system:read` / `monitors:read` | 只读，短期过期 |
| 日志审计 | API 密钥 + `logs:read` / `system_logs:read` | 只读，导出到 SIEM |

API 密钥不是多用户 RBAC。需要多人分权时，应通过企业 IAM、堡垒机或上层平台实现审批和代理调用。

## 2. 认证

把密钥放在 `Authorization: Bearer` 或 `X-API-Key` 请求头中：

```bash
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```

```bash
curl -H "X-API-Key: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```

要点：

- 接口固定位于 `/api`；旧版 `/<entry>/api` 保留为兼容别名。
- 请求仍需通过登录会话或 API 密钥认证，不依赖门禁入口。
- API 密钥请求不依赖 Cookie，因此不需要 `X-LACTSSL-CSRF` 头。
- 所有请求建议通过 HTTPS；密钥不得出现在 URL 查询参数中。
- 响应头包含 `X-Request-ID`，排障时提供给供应商或运维。

## 3. API 密钥生命周期

### 3.1 创建

1. 使用管理员账户登录控制台。
2. 打开「API 密钥」页面，点击「创建密钥」。
3. 填写名称、过期时间，并按需勾选权限。
4. 页面会显示一次完整密钥；关闭弹窗后无法再次查看，只能轮换。

密钥格式为 86 位 Base64URL 随机字符串（64 字节熵），不含固定前缀。服务端只保存摘要，数据库泄露时无法直接还原明文密钥。

创建请求示例：

```bash
curl -X POST \
  -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI deployment",
    "scopes": ["tasks:read", "tasks:run", "deploy:execute"],
    "expires_at": "2027-01-01T00:00:00Z",
    "allowed_ips": ["203.0.113.10", "2001:db8::/48"],
    "allowed_dns_account_ids": [1, 3]
  }' \
  https://ssl.example.com/api/api-keys
```

`expires_at` 留空表示永不过期；生产不建议永不过期。`allowed_ips` 可选，留空表示不限制来源 IP；非空时只接受单个 IPv4/IPv6 或 CIDR，来自其他 IP 的请求会返回 `403`。`allowed_dns_account_ids` 可选，留空表示不限制 DNS 账户；非空时该密钥只能使用列表中的账户签发证书、运行任务和管理 DNS 配置。跨账户证书要求所有命中的账户都在列表内。创建和修改密钥属于高风险操作，浏览器会话依赖登录身份，API 密钥调用则依赖其 `api_keys:write` 权限。

### 3.2 IP 限制

- 每个密钥可以单独配置来源 IP/CIDR 白名单，浏览器登录会话不受影响。
- 留空表示不限制；支持 `203.0.113.10`、`10.0.0.0/8`、`2001:db8::/48` 等写法。
- 认证时会使用反向代理还原后的真实客户端 IP。部署在代理后面时，必须在「设置 → 配置文件 → 可信代理」中配置实际代理地址；未配置可信代理时不会采信 `X-Forwarded-For` / `X-Real-IP`。
- 为避免误配，`0.0.0.0/0` 和 `::/0` 会被拒绝；需要不限制时直接留空。
- 通过 API 密钥管理其他密钥时，不能创建、修改或轮换 IP 白名单范围比自身更宽的密钥。
- 密钥列表会显示最近一次成功调用的来源 IP；IP 拒绝记录可在密钥行的「日志」中查看，默认只显示拒绝事件。
- 代理配置错误、动态出口 IP 或新旧版本混跑都可能让白名单失效或被意外绕过，启用前请先用「添加当前 IP」确认出口地址。
- 如果服务端在 CDN 或反向代理后且暂时无法还原真实 IP，可在创建/编辑弹窗中使用「WebRTC 探测」查看当前浏览器的公网出口 IP。该功能由浏览器连接公共 STUN 服务器，结果可能与 API 客户端的实际来源 IP 不同，只能作为诊断参考，不能替代可信代理配置。

### 3.3 轮换与停用

- 轮换后旧令牌立即失效。
- 停用或删除后，所有使用该密钥的调用返回 `401`。
- 通过 API 密钥管理其他密钥时，不能授予自身没有的权限、更宽的 IP 白名单或更宽的 DNS 账户集合，也不能修改/轮换/删除权限更高或范围更宽的密钥。

### 3.4 保管

- 存入企业密码管理器或密钥管理系统，不写入代码仓库、镜像或日志。
- 为每个系统单独发放，命名包含系统名和环境。
- 设置合理过期时间，到期前轮换。
- 怀疑泄露时立即轮换或删除，并检查操作日志中的异常调用。

## 4. 权限模型

权限按「资源:动作」划分。`*` 表示全部系统 API 能力，包含所有敏感操作。

| 权限 | 说明 | 敏感 |
| --- | --- | --- |
| `*` | 全部系统 API 能力 | 是 |
| `profile:read` | 查看当前密钥所属账户 | 否 |
| `system:read` | 查看版本、状态和元数据 | 否 |
| `dashboard:read` | 查看仪表盘统计 | 否 |
| `certificates:read` | 查看证书、下载证书链和证书分析 | 否 |
| `certificates:write` | 清理和删除证书 | 否 |
| `certificates:key` | 查看或下载证书私钥 | 是 |
| `certificates:request` | 提交客户端 CSR，由服务端向公共 CA 完成验证与签发，或使用私有 CA 签发（还需 `private_cas:write`） | 是 |
| `deploy:execute` | 手动部署证书，或创建/修改/运行包含部署节点的任务（写文件；命令执行另需 `deploy:command`） | 是 |
| `deploy:command` | 允许本地/SSH 部署执行 reload 命令（等同于以服务账号执行任意命令） | 是 |
| `tasks:read` | 查看任务列表与任务日志 | 否 |
| `tasks:write` | 创建、修改、删除和启停任务，清理任务执行日志（创建即执行还需 `tasks:run`；含部署节点还需 `deploy:execute`；私有 CA 还需 `private_cas:write`） | 否 |
| `tasks:run` | 手动运行任务或批量运行（含部署节点还需 `deploy:execute`；私有 CA 还需 `private_cas:write`） | 否 |
| `monitors:read` | 查看站点证书监控 | 否 |
| `monitors:write` | 创建、修改、删除和立即检查监控 | 否 |
| `dns_accounts:read` | 查看 DNS 服务商账户（密钥掩码） | 否 |
| `dns_accounts:write` | 创建、修改和删除 DNS 服务商账户 | 是 |
| `deploy_accounts:read` | 查看部署目标账户（密钥掩码） | 否 |
| `deploy_accounts:write` | 创建、修改和删除部署目标账户 | 是 |
| `deployments:read` | 查看证书部署历史 | 否 |
| `ca:read` | 查看 ACME CA、端点、账户和订单 | 否 |
| `ca:write` | 修改 ACME CA、端点、EAB 和清理诊断数据 | 是 |
| `private_cas:read` | 查看私有 CA 证书与证书链 | 否 |
| `private_cas:write` | 创建、导入和删除私有 CA | 是 |
| `private_cas:key` | 下载私有 CA 私钥 | 是 |
| `plugins:read` | 查看插件、插件仓库与启用状态 | 否 |
| `plugins:write` | 上传、安装、更新、卸载和启用插件 | 是 |
| `logs:read` | 查看和导出操作日志，查看/下载日志归档 | 否 |
| `logs:write` | 清空全部操作日志、删除日志归档（清空动作本身会留下审计记录） | 是 |
| `system_logs:read` | 查看和导出进程日志，查看/下载日志归档 | 否 |
| `system_logs:write` | 删除全部系统日志、删除日志归档 | 是 |
| `settings:read` | 查看系统设置与内部 CA 信任链 | 否 |
| `settings:write` | 修改常规系统设置（SMTP/通知目标；测试消息仍仅浏览器会话） | 是 |
| `backup:read` | 查看备份状态与大小 | 否 |
| `backup:download` | 导出加密备份（仅浏览器会话；仅保留历史密钥兼容） | 是 |
| `backup:restore` | 上传并恢复备份，会重启服务（仅浏览器会话；仅保留历史密钥兼容） | 是 |
| `upgrade:read` | 查看数据库迁移与程序升级状态 | 否 |
| `upgrade:write` | 运行迁移、优化数据库和替换程序 | 是 |
| `security:write` | 修改信任根与插件信任锚（门禁入口仅浏览器会话） | 是 |
| `api_keys:read` | 查看 API 密钥列表与权限 | 否 |
| `api_keys:write` | 创建、修改、轮换和删除 API 密钥 | 是 |

### 4.1 推荐角色映射

| 角色 | 权限 |
| --- | --- |
| 只读巡检 | `system:read`、`dashboard:read`、`certificates:read`、`tasks:read`、`monitors:read`、`deployments:read` |
| CI/CD 续期 | `tasks:read`、`tasks:run`；含部署节点时加 `deploy:execute` |
| 证书分发 | `certificates:read`、`certificates:key`、`deploy:execute` |
| 日志审计 | `logs:read`、`system_logs:read` |
| 监控 | `system:read`、`monitors:read` |

私钥权限是附加权限：

- 下载证书私钥还需要 `certificates:read`。
- 下载私有 CA 私钥还需要 `private_cas:read`。
- 创建/运行含部署节点的任务必须显式拥有 `deploy:execute`；`certificates:write` 和 `tasks:*` 不隐含部署执行能力。
- 本地/SSH 部署中的 `reload_command` 还必须显式拥有 `deploy:command`；仅 `deploy:execute` 只能写入证书文件。
- 使用私有 CA 签发证书的任务需要 `private_cas:write`；`tasks:write` 不隐含私有 CA 签发能力。
- 创建 `enabled:true` 的任务会立即签发，除 `tasks:write` 外还需要 `tasks:run`。

## 5. 响应格式

成功：

```json
{
  "success": true,
  "data": {}
}
```

失败：

```json
{
  "success": false,
  "code": "api.error.api_key_scope_denied",
  "message": "API 密钥缺少所需权限：certificates:read",
  "params": { "scope": "certificates:read" }
}
```

常见状态码：

| 状态码 | 含义 |
| --- | --- |
| `400` | 参数错误、配置校验失败 |
| `401` | 未认证，密钥不存在、已停用、已过期或已删除 |
| `403` | 权限不足，或该接口只允许浏览器会话调用 |
| `404` | 资源不存在或路径错误 |
| `409` | 状态冲突（如正在运行、重复操作） |
| `429` | 请求过于频繁，按 `Retry-After` 重试 |
| `500` | 服务端错误，记录 `X-Request-ID` 后排查 |

## 6. 常用接口

### 6.1 查看状态与证书

```bash
# 进程状态（需要 system:read）
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/status

# 证书列表（需要 certificates:read）
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificates
```

列表接口直接返回数组；日志接口返回分页对象：

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/logs?page=1&size=50&level=error&source=task&q=demo"
```

`size` 最大 200；`q` 按原始存储内容和参数值匹配。清空操作日志使用 `DELETE /api/logs`，需要 `logs:write`。

清空日志时，如果启用了 `log.archive.enabled`，服务会先写入归档再删除，响应额外返回 `archived` 对象；归档失败会返回 500 且不删除日志。

日志归档接口：

```bash
# 列表：支持 page、size、category、reason、from、to、q
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives?page=1&size=20&category=operation"

# 下载归档文件
curl -fsS -OJ -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>/download"

# 删除归档
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>"
```

归档接口接受 `logs:*` 或 `system_logs:*` 对应权限：查看/下载需要任一读权限，删除需要任一写权限。`category` 取值为 `operation` 或 `runtime`；`reason` 取值为 `manual_clear`、`retention` 或 `max_rows`。

### 6.2 目标流程预览

目标流程 Beta 在保存任务前可以读取目标网站并预览证书分组。该接口只读，需要 `deploy:execute`：

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  https://ssl.example.com/api/tasks/target-flow/preview \
  -d '{
    "sources": [{"deploy_account_id": 3, "deploy_config": {"target": "site"}}],
    "selection": {"mode": "all"},
    "policy": {"strategy": "smart", "max_domains_per_cert": 100},
    "public_ca": true
  }'
```

创建任务时传 `flow_mode: "target_beta"` 与 `target_flow`；`target_flow.policy.strategy` 支持 `smart`、`per_site`、`per_base_domain`、`single`、`per_source`、`custom`。一个 Beta 任务可能关联多张证书，证书关联记录在 `task_certificate_links` 中。

普通任务的 `dns_account_id` 也可以省略或设为 `0`，由服务端按域名绑定解析；跨账户时任务的 `dns_account_id` 为 `0`，`dns_routes` 保存每个域名实际使用的账户快照。

### 6.3 运行任务

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/run
```

如果任务包含启用的部署节点，密钥必须同时拥有 `deploy:execute`。

默认会复用仍在有效期内且未进入续期窗口的证书，只重新执行部署节点，不重复向上游 CA 下单。确需强制重新签发时使用 `?force=1` 或发送 `{"force":true}`；强制签发会消耗 CA 的证书配额。

查询任务执行日志（需要 `tasks:read`）：

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```

清理任务执行日志（需要 `tasks:write`）：

```bash
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```

清理接口会删除该任务的签发与部署日志，返回删除行数 `deleted`，并在操作日志中保留一条审计记录。

### 6.4 下载证书

```bash
# 完整链（需要 certificates:read）
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.fullchain.pem \
  "https://ssl.example.com/api/certificates/42/download?type=fullchain"

# 私钥（需要 certificates:read + certificates:key）
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.key \
  "https://ssl.example.com/api/certificates/42/download?type=key"
```

`type` 支持 `fullchain`（默认）、`cert`、`key`。下载接口返回 PEM 原文，不包在 JSON 中；请按文件权限保存，不要写入日志。

### 6.5 站点监控

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors

curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors/7/check
```

### 6.6 API 密钥管理

| 接口 | 方法 | 权限 | 说明 |
| --- | --- | --- | --- |
| `/api-keys` | GET | `api_keys:read` | 列出密钥，不返回明文令牌 |
| `/api-keys/scopes` | GET | `api_keys:read` | 获取权限列表与本地化说明 |
| `/api-keys` | POST | `api_keys:write` | 创建密钥，响应中仅此一次返回 `token` |
| `/api-keys/:id` | PUT | `api_keys:write` | 修改名称、权限、启用状态和过期时间 |
| `/api-keys/:id/rotate` | POST | `api_keys:write` | 轮换密钥，旧令牌立即失效 |
| `/api-keys/:id` | DELETE | `api_keys:write` | 删除密钥 |

### 6.7 使用客户端 CSR 申请证书

服务端作为中间环节接收客户端 CSR，使用已配置的公共 CA 和 DNS 账户完成 DNS-01 验证并签发证书，或使用管理员维护的私有 CA 直接签名。服务端不会生成或保存私钥，证书签发后私钥仍由客户端保管。

创建申请需要 `certificates:request` 权限；使用私有 CA 时还需要 `private_cas:write`：

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n",
    "ca": "letsencrypt",
    "challenge": "dns01",
    "dns_account_id": 3,
    "idempotency_key": "order-20261118-001",
    "wait_seconds": 0
  }' \
  https://ssl.example.com/api/certificate-requests
```

使用私有 CA 时，将 `ca` 设置为 `private-ca:<id>`，或直接传入 `private_ca_id`。私有 CA 申请不进行 DNS-01 验证，不需要 `dns_account_id` 和 ACME 联系邮箱，并允许申请 `app.internal` 等内网域名：

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n",
    "private_ca_id": 2,
    "idempotency_key": "internal-20261121-001"
  }' \
  https://ssl.example.com/api/certificate-requests
```

公共 CA 申请的 `dns_account_id` 可以省略。管理员在「DNS 账户 → 域名绑定」中声明域名后，服务端会按最长后缀匹配自动选择账户，并在响应的 `resolved_by` / `resolved_zone` 中返回解析来源。不同域名可以命中不同账户：响应中的 `dns_routes` 会逐域名返回实际使用的账户，签发时也按该快照分别写入 TXT。只有未绑定域名，或同一个域名同时命中多个同等级账户时才会拒绝，避免把 TXT 写入错误的服务商。显式传入 `dns_account_id` 时仍表示该账户覆盖 CSR 的全部域名。

接口返回 `202 Accepted` 和申请 ID。申请是异步执行的，可轮询状态：

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx
```

状态为 `issued` 后可下载证书链，不提供私钥：

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx/certificate?type=fullchain" \
  -o example.com.fullchain.crt
```

要点：

- 公共 CA 申请仅支持 DNS-01，通配符证书必须使用 DNS-01；私有 CA 申请由服务端直接签名，不使用域名挑战。
- `ca` 可以是「公共CA」页面中的 ACME 上游，也可以是 `private-ca:<id>`。私有 CA 签发还需要 `private_cas:write`，并会执行该 CA 的扩展密钥用途和名称约束检查。
- 服务端会严格校验 CSR 签名、公钥强度、SAN 规范和通用名称；公共 CA 申请会拒绝内部域名，私有 CA 申请则允许。CSR 已由客户端签名，服务端无法替客户端修正。
- `idempotency_key` 建议必传；同一 CSR、CA、DNS 路由在同一调用方下只允许一个在途申请。
- 可通过设置项 `csr.allowed_domains` 限制允许申请的域名，多个域名用逗号或空白分隔。私有 CA 申请在允许列表为空时不做额外限制，依赖 CA 自身的名称约束；配置允许列表后，两种 CA 都按列表校验。
- 证书列表中的 `key_available` 为 `false`，表示服务端没有私钥，不能下载私钥或执行依赖私钥的部署。

域名绑定与解析预览接口：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/dns-accounts/:id/zones` | 查看账户已声明的域名绑定 |
| PUT | `/api/dns-accounts/:id/zones` | 原子替换账户的域名绑定，同一域名只能属于一个启用账户 |
| POST | `/api/dns-accounts/:id/zones/probe` | 用只读验证探测域名所属 zone 并回填绑定，需要 `dns_accounts:write` |
| POST | `/api/dns-accounts/resolve` | 只读预览每个域名会解析到哪个账户，不发起出站请求 |

`/api/dns-accounts/resolve` 示例：

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"domains":["a.example.com","b.other.net"]}' \
  https://ssl.example.com/api/dns-accounts/resolve
```

响应中的 `routes` 与 `account_ids` 会列出每个域名的账户。`account_id` 只在全部域名落到同一账户时返回该账户，否则为 `0`。

仓库内提供了可直接使用的 Python 客户端脚本 `scripts/csr_client.py`，它会在本地生成私钥和 CSR，提交申请、轮询状态，并在下载后校验证书与私钥匹配：

```bash
export LACTSSL_URL="https://ssl.example.com"
export LACTSSL_TOKEN="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

python3 scripts/csr_client.py \
  --domains example.com,www.example.com \
  --dns-account-id 3 \
  --ca letsencrypt \
  --out-dir ./certs

# 私有 CA 申请（私钥仍只在本地生成）
python3 scripts/csr_client.py \
  --domains app.internal,api.internal \
  --private-ca-id 2 \
  --out-dir ./certs
```

也可以使用交互式向导逐项填写，无需记忆参数（不带任何参数在终端中运行也会自动进入向导）：

```bash
python3 scripts/csr_client.py --interactive
```

续期、查询与批量操作：

```bash
# 续期：用现有私钥生成新 CSR（私钥不复制、不修改）
python3 scripts/csr_client.py \
  --key ./certs/example.com.key \
  --domains example.com,www.example.com \
  --out-dir ./certs \
  --force

# 或直接从上次的状态文件续期（自动复用私钥、CA、DNS 账户与输出名）
python3 scripts/csr_client.py --renew-from ./certs/example.com.request.json

# 查询 / 取消申请（--json 便于脚本处理）
python3 scripts/csr_client.py --status csr_xxxxxxxx --json
python3 scripts/csr_client.py --cancel csr_xxxxxxxx --yes
python3 scripts/csr_client.py --list --json

# 批量申请：顺序执行，失败时默认停止
python3 scripts/csr_client.py --manifest ./certs.json --json
```

批量清单格式：

```json
{
  "defaults": {"ca": "letsencrypt", "dns_account_id": 3, "out_dir": "./certs"},
  "certificates": [
    {"domains": ["example.com", "www.example.com"]},
    {"domains": ["api.example.com"], "name": "api", "key_type": "rsa2048"}
  ]
}
```

脚本只上传 CSR，私钥始终保存在客户端；输出包括 `.key`、`.csr`、`.crt`、`.fullchain.crt` 和 `.request.json`。本地生成的私钥和 CSR 先以 `.new` 暂存，证书下载并校验通过后才原子替换最终文件，因此申请失败不会破坏上一套可用证书。创建申请后会立即写入 `.request.json`，即使断网或使用 `--quiet`，也可以用 `--request-id csr_xxxxxxxx` 续传；续传时会自动查找上次留下的 `.key.new`/`.key`。网络异常默认重试 3 次（`--retries`），服务端标记可重试时默认自动调用 `/retry` 1 次（`--auto-retry`），并遵循 429 的 `Retry-After`。进度日志写入 stderr，`--json` 时 stdout 只有最终 JSON；令牌可通过 `LACTSSL_TOKEN_FILE` 或 `--token-file` 读取，避免出现在命令行参数中。客户端界面支持 `zh-CN`、`zh-TW`、`en-US`、`ja-JP`、`ru-RU`，可通过 `--lang` 或 `LACTSSL_LANG` 切换，API 错误也会按同一语言返回。退出码：0 成功、1 通用失败、2 参数/配置、3 认证/权限、4 网络、5 服务端申请失败、6 本地校验失败、130 用户取消。

### 6.8 验证凭据可用性

在新增或修改 DNS 账户、目标凭据后，可主动验证凭据是否真实可用。验证接口使用 `dns_accounts:write` / `deploy_accounts:write` 权限；浏览器会话不再需要输入当前密码或 TOTP 再认证。验证结果仅作提示，不会阻止保存或运行。

未传 `domain` 时，已保存的 DNS 账户优先使用其已启用的域名绑定；没有绑定时返回 `mode: "scope"`，仅校验凭据配置与调用权限。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/dns-accounts/:id/verify` | 验证已保存 DNS 账户；可传 `domain`、`mode` |
| POST | `/api/dns-accounts/verify` | 验证未保存的 DNS 配置；必须显式提交密钥 |
| POST | `/api/deploy-accounts/:id/verify` | 验证已保存目标凭据；可传 `deploy_config` |
| POST | `/api/deploy-accounts/verify` | 验证未保存的目标凭据配置；必须显式提交密钥 |
| POST | `/api/deploy-accounts/:id/options` | 使用已保存凭据读取部署字段的动态选项，例如宝塔网站列表 |

请求示例：

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"domain":"example.com","mode":"auto"}' \
  https://ssl.example.com/api/dns-accounts/3/verify
```

动态选项接口只适用于 manifest 中声明了 `options_source` 的部署字段，API 密钥需要 `deploy_accounts:write` 权限。请求体传入字段名和当前部署参数，凭据由服务端从目标凭据中读取：

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"field":"sites","config":{"target":"docker"}}' \
  https://ssl.example.com/api/deploy-accounts/3/options
```

返回 `{"options":[{"label":"站点名称","value":"站点名称"}]}`。插件不可用或面板不可达时返回明确错误，界面会保留手工输入能力。

返回结构包含分项检查，便于区分认证失败、域名不属于该凭据、目录不可写等原因：

```json
{
  "success": true,
  "data": {
    "ok": false,
    "mode": "write",
    "checks": [
      {"key": "init", "status": "ok"},
      {"key": "domain", "status": "ok", "message": "example.com"},
      {"key": "write", "status": "failed", "message": "..."}
    ],
    "message": "...",
    "verified_at": "2026-11-18T12:00:00Z"
  }
}
```

行为说明：

- DNS 验证默认使用 `auto` 模式：插件声明 `verify-v1` 时优先只读列出 zone 并检查域名归属；未声明或返回不支持时回退到写测试（写入随机 `_acme-challenge` TXT 后立即删除）。`mode=read` 强制只读，插件不支持时返回 `unsupported`。
- 已保存账户只使用数据库中的配置；未保存配置必须显式提交密钥，不会复用已保存密钥，避免通过修改 endpoint 把凭据发往其他服务器。
- 目标凭据验证支持内置 `local`/`ssh`：连接并认证，在目标目录创建并删除临时文件，不执行 `reload_command`。其他部署插件在实现 `verify` 能力前返回 `unsupported`。
- 传入 `deploy_config` 可验证具体部署节点的作用域（站点、目录、桶、域名），这是“新增权限是否可用”的推荐用法。

## 7. CI/CD 示例

### 7.1 证书续期后分发

```bash
#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${LACTSSL_URL:?}"
TOKEN="${LACTSSL_TOKEN:?}"

# 触发任务（任务 ID 由 CMDB 或配置管理下发）
curl -fsS -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  "${BASE_URL}/api/tasks/${TASK_ID}/run"

# 等待任务完成后，按证书 ID 下载并分发
curl -fL \
  -H "Authorization: Bearer ${TOKEN}" \
  -o /run/secrets/example.com.fullchain.pem \
  "${BASE_URL}/api/certificates/${CERT_ID}/download?type=fullchain"
```

注意：

- API 没有幂等键；重复调用 `run` 会再次执行任务流程，但证书仍有效时会跳过签发并只执行部署，脚本仍应自行去重。
- 任务运行是异步的；需要等待完成时轮询任务列表或订阅通知，不要假设 POST 返回即完成。
- 下载私钥需要额外权限；大多数分发场景只需证书链和私钥由部署节点直接写入目标，不建议把私钥经 CI 中转。

### 7.2 巡检

```bash
#!/usr/bin/env bash
set -euo pipefail

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/status" >/dev/null

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/certificates" |
  jq -e '[.data[] | select(.not_after < (now + 14*86400 | todate))] | length == 0'
```

（示例使用 `jq`；实际字段名以接口返回为准。）

## 8. 错误处理与重试

- 对 `429` 按 `Retry-After` 退避，不要立即重试。
- 对 `5xx` 使用指数退避，并记录 `X-Request-ID`。
- 对 `401` 不要重试，检查密钥是否过期、停用或轮换。
- 对 `403` 检查权限和接口限制（部分接口只允许浏览器会话）。
- 对 `400`/`409` 不要自动重试，先修正参数或状态。
- 涉及签发/部署的请求可能产生外部副作用，重试前先查询任务/部署状态。

## 9. 安全建议

- 为每个自动化任务创建独立密钥，不要共用。
- 只授予任务真正需要的权限；只读任务不要授予 `*`。
- 设置合理过期时间，并定期轮换。
- 不在日志、截图或公开仓库中暴露完整密钥。
- 怀疑泄露时立即停用或删除密钥；轮换会使旧密钥立即失效。
- 在网络层限制调用来源，避免密钥泄露后被任意地址使用。
- 将 API 调用日志与操作日志关联，保留调用方、时间和 `X-Request-ID`。

## 相关文档

- [管理工作台](administration.md)：密钥治理和职责分离。
- [安全基线](security.md)：认证、审计和网络控制。
- [监控与排障](troubleshooting.md)：429、401、任务失败等问题。
