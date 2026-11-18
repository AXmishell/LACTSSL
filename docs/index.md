---
hide:
  - navigation
  - toc
---

<section class="hero" markdown>
<div class="hero-badge">面向企业团队与个人 / 小团队 · 自托管 · 单租户</div>

# LACTSSL

<p class="hero-sub">把公网证书签发、证书部署、到期监控和内部 PKI 收敛到一个可审计的自托管控制台。企业用它统一多业务线的证书治理，个人开发者、站长和小团队用一台小主机就能跑完全自动续期与部署。</p>

[快速上手](getting-started.md){ .md-button .md-button--primary }
[部署架构](architecture.md){ .md-button }
[GitHub 仓库](https://github.com/AXmishell/LACTSSL){ .md-button target="_blank" rel="noreferrer" }
</section>

[![LACTSSL 仪表盘](assets/images/dashboard.png){ .doc-screenshot .doc-screenshot--home }](assets/images/dashboard.png){ target="_blank" rel="noreferrer" }

## 为什么选择 LACTSSL

同一套产品，既服务企业多业务线，也服务个人站点、小团队和家庭实验室。

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-certificate-outline: 证书到期风险收敛
集中管理 ACME 证书的申请、续期、部署和到期监控，减少“证书过期导致业务中断”的分散脚本与人工台账。企业可覆盖多业务线，个人站点只需接一个域名。
</div>

<div class="feature-card" markdown>
### :material-server-network: 内网 PKI 与 ACME 服务
用私有 CA 为内部服务签发证书，并可把标准 ACME v2 端点开放给 certbot、lego、acme.sh 等客户端；家庭实验室和开发环境也能有自己的本地 CA。
</div>

<div class="feature-card" markdown>
### :material-file-export-outline: 多目标交付
内置本地目录与 SSH/SFTP；FTP、Webhook、主流云 CDN、宝塔、1Panel、雷池 WAF 等目标通过部署插件按需扩展，从云主机到面板建站都能覆盖。
</div>

<div class="feature-card" markdown>
### :material-shield-check-outline: 可审计、可自托管
单二进制、SQLite、无外部依赖；门禁入口、TOTP/通行密钥、字段加密、操作日志与加密备份，数据不出企业边界，也不出你自己的服务器。资源占用低，一台小主机即可长期运行。
</div>

</div>

## 适用场景

| 场景 | 适用对象 | 典型问题 | LACTSSL 的做法 |
| --- | --- | --- | --- |
| 个人站点、博客与副业项目 | 个人 / 小团队 | 证书到期提醒靠记忆，续期脚本散落在各台机器上 | 一个任务搞定申请、续期和部署，到期前自动执行并留痕 |
| 家庭实验室与内网服务 | 个人 / 小团队 | 自签证书浏览器告警多，客户端信任和轮换麻烦 | 私有 CA 分层签发，并提供标准 ACME 端点 |
| 多域名、多业务线证书治理 | 企业 | 证书分散在个人账号、脚本和表格中，续期失败无人发现 | 统一任务编排、自动续期、部署记录与到期看板 |
| 内部服务 TLS | 企业 | 内网服务使用自签证书，客户端信任和轮换困难 | 私有 CA 分层签发，并提供标准 ACME 端点 |
| 证书交付自动化 | 两者 | 证书签发后仍需人工上传到面板、CDN 或服务器 | 任务内编排本地/SSH/插件部署，支持重载命令 |
| 安全与审计要求 | 企业 | 无法回答“谁在何时签发了什么、部署到哪里” | 操作日志、系统日志、API 密钥分权与加密备份 |

## 选型边界

B 端和 C 端选型都应先确认产品边界。以下能力是当前版本的真实范围：

| 能力 | 当前版本 | 对个人 / 小团队的影响 |
| --- | --- | --- |
| 部署形态 | 自托管单实例，单租户 | 一台小主机、VPS 或 NAS 即可运行 |
| 管理账户 | 单个管理员账户，不提供多用户、角色或组织架构 | 个人或小团队共用即可满足 |
| 机器身份 | API 密钥按“资源:动作”授权，可设置过期时间与启停 | 可给 CI/CD 或脚本单独发一把受限密钥 |
| 身份联邦 | 不提供 LDAP / OIDC / SAML / SCIM；不能接入企业 SSO | 不依赖企业账号体系，本地登录即可 |
| 高可用 | 不提供内置集群或双活；SQLite 单写，生产建议单活 + 备份/快速恢复 | 个人场景按定期备份即可 |
| 密钥托管 | 私钥加密存储在本地数据库；不提供 HSM / KMS 托管 | 无需额外密钥服务 |
| 监控集成 | 提供操作日志、系统日志和认证 API；不提供 Prometheus 指标端点 | 依赖内置通知即可 |
| 合规认证 | 项目本身不附带等保、ISO 27001 等认证证书；可提供控制项与审计证据 | 与个人使用无关 |
| 支持模式 | 项目采用 MIT 许可证；商业支持、SLA 和托管服务需另行约定 | 社区文档与 issue 支持 |

> 如果企业要求多用户 RBAC、SSO、多租户或双活集群，需要在上层平台或组织流程中补齐，不能把 LACTSSL 当作完整 IAM/PKI 平台使用。

## 参考架构

```text
                    企业用户 / 个人用户 / 自动化系统
                           │ HTTPS
                    ┌──────▼──────┐
                    │ 反向代理/WAF │   TLS 终止、访问控制、审计
                    └──────┬──────┘
                           │ HTTP (内网)
                    ┌──────▼──────────────────────────┐
                    │ LACTSSL 单实例                   │
                    │ 管理控制台 + API + 调度器         │
                    │ 私有 CA + ACME 服务（可选）       │
                    │ SQLite + 本地加密密钥             │
                    └──┬──────────┬──────────┬────────┘
                       │          │          │
                 DNS API     部署目标     通知渠道
               （公网 CA）  （SSH/面板/CDN） （SMTP/Webhook）
```

详细拓扑、端口、容量和可用性设计见[部署架构](architecture.md)。

## 按角色阅读

| 角色 | 关注点 | 建议阅读 |
| --- | --- | --- |
| 个人开发者 / 站长 / 小团队 | 装起来、接域名、自动续期和部署 | [安装与启动](install.md)、[DNS 插件](dns-plugins.md)、[部署插件](deploy-plugins.md) |
| 业务负责人 / 选型评估 | 价值、边界、试点周期、验收标准 | [快速上手与上线](getting-started.md)、[产品能力](features.md) |
| 平台 / 运维负责人 | 部署拓扑、备份恢复、升级、告警 | [部署架构](architecture.md)、[运维手册](operations.md) |
| 安全 / 合规 | 身份、加密、审计、供应链、控制项 | [安全基线](security.md)、[管理工作台](administration.md) |
| 集成 / 自动化开发 | API 密钥、权限、CI/CD、错误处理 | [API 集成](api.md)、[扩展开发](plugin-development.md) |

## 推荐上线路径

**个人 / 小团队**：Docker 起一个实例 → 装 DNS 与部署插件 → 建一个域名任务 → 验证续期和通知，通常半天内完成。不需要走完整的企业试点流程。

**企业团队**：

1. **评估（0.5–1 天）**：用 Docker 启动试点实例，跑通一张测试证书和一次本地部署。
2. **试点（1–2 周）**：接入 1–2 个真实域名或一个内部 CA 场景，验证 DNS 凭据、部署目标和通知链路。
3. **生产（按变更窗口）**：确定反向代理、备份、监控和 API 密钥责任人，完成安全基线后切换。
4. **运营（持续）**：按月度检查到期、备份、日志和插件版本，按季度做恢复演练。

具体步骤和验收表见[快速上手与上线](getting-started.md)。

## 能力总览

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-autorenew: ACME 自动化
DNS-01 验证，支持单域名、多域名和通配符；到期前自动续期，失败可重试并留痕。
</div>

<div class="feature-card" markdown>
### :material-sitemap-outline: 任务编排
申请 → 部署（可多个）→ 通知，节点可启停和排序，同一凭据可被多个任务复用。
</div>

<div class="feature-card" markdown>
### :material-lock-outline: 私有 CA
生成或导入根 CA / 中间 CA，配置路径长度、Key Usage、Name Constraints、CRL/OCSP 地址等 PKI 参数。
</div>

<div class="feature-card" markdown>
### :material-api: 标准 ACME 服务端
按端点绑定私有 CA，支持 EAB、域名白名单、HTTP-01/DNS-01/TLS-ALPN-01、mTLS 与订单限流。
</div>

<div class="feature-card" markdown>
### :material-monitor-eye: 站点证书监控
定期读取外部站点证书，校验证书链与主机名，异常和临期自动通知。
</div>

<div class="feature-card" markdown>
### :material-bell-ring-outline: 通知与审计
邮件内置，20+ 渠道由插件提供；操作日志和系统日志可按条件筛选并导出 CSV/JSON。
</div>

</div>

## 文档导航

<div class="doc-grid" markdown>

<div class="doc-card" markdown>
[:material-rocket-launch-outline: 快速上手与上线](getting-started.md)
<span>个人快速上手，以及企业试点、验收、生产切换与责任分工。</span>
</div>

<div class="doc-card" markdown>
[:material-server-network: 部署架构](architecture.md)
<span>拓扑、端口、容量、可用性与网络边界。</span>
</div>

<div class="doc-card" markdown>
[:material-shield-lock-outline: 安全基线](security.md)
<span>身份、加密、审计、出站防护和控制项映射。</span>
</div>

<div class="doc-card" markdown>
[:material-wrench-outline: 运维手册](operations.md)
<span>备份恢复、监控、变更、故障响应和演练。</span>
</div>

<div class="doc-card" markdown>
[:material-code-braces: API 集成](api.md)
<span>密钥治理、权限矩阵和自动化示例。</span>
</div>

<div class="doc-card" markdown>
[:material-lifebuoy: 监控与排障](troubleshooting.md)
<span>常见故障、诊断路径和升级信息收集。</span>
</div>

</div>

## 技术栈

| 层级 | 实现 |
| --- | --- |
| 后端 | Go + Gin + SQLite |
| 前端 | Vue 3 + Vite + Element Plus（构建后嵌入二进制） |
| ACME | lego / RFC 8555 |
| 存储 | SQLite，敏感字段 AES-256-GCM 加密 |
| 部署 | 单二进制，无外部数据库和消息队列 |
