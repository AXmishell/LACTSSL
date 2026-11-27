---
hide:
  - navigation
  - toc
---

<section class="hero" markdown>
<div class="hero-badge">自托管 · 单实例 · 开源</div>

# LACTSSL

<p class="hero-sub">把证书签发、自动续期、部署和到期监控收进一个自托管控制台。开发者用一台小主机即可跑完全流程。</p>

[快速开始](getting-started.md){ .md-button .md-button--primary }
[安装部署](install.md){ .md-button }
</section>

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-autorenew: 自动续期与部署
ACME 证书集中申请、续期、部署和到期监控，替代分散脚本与人工台账。
</div>

<div class="feature-card" markdown>
### :material-server-network: 私有 CA 与内部 ACME
生成或导入根 / 中间 CA，并向 certbot、lego、acme.sh 提供标准 ACME v2 端点。
</div>

<div class="feature-card" markdown>
### :material-file-export-outline: 多目标交付
内置本地目录与 SSH/SFTP；FTP、Webhook、云 CDN、宝塔、1Panel、雷池 WAF 等通过部署插件扩展。
</div>

<div class="feature-card" markdown>
### :material-monitor-eye: 站点到期监控
定期读取外部站点证书，校验证书链与主机名，异常和临期自动通知。
</div>

<div class="feature-card" markdown>
### :material-shield-check-outline: 可审计、可自托管
单二进制、SQLite、无外部依赖；门禁入口、TOTP/通行密钥、字段加密、操作日志与加密备份。
</div>

<div class="feature-card" markdown>
### :material-bell-ring-outline: 通知与集成
邮件内置，常用通知渠道随主程序提供；REST API 与按资源授权的 API 密钥支持 CI/CD。
</div>

</div>

## 文档导航

<div class="doc-grid" markdown>

<div class="doc-card" markdown>
[:material-rocket-launch-outline: 快速开始](getting-started.md)
<span>安装、初始化，并签发第一张证书。</span>
</div>

<div class="doc-card" markdown>
[:material-download-outline: 安装与启动](install.md)
<span>Docker、二进制、反向代理与 HTTPS。</span>
</div>

<div class="doc-card" markdown>
[:material-format-list-checks: 产品能力](features.md)
<span>证书、任务、私有 CA、ACME 服务与监控。</span>
</div>

<div class="doc-card" markdown>
[:material-server-network: 部署架构](architecture.md)
<span>拓扑、端口、容量与可用性。</span>
</div>

<div class="doc-card" markdown>
[:material-shield-lock-outline: 安全基线](security.md)
<span>身份、加密、审计与上线检查清单。</span>
</div>

<div class="doc-card" markdown>
[:material-cog-outline: 配置参考](configuration.md)
<span>完整配置字段、反向代理与插件配置。</span>
</div>

</div>

## 产品界面

[![LACTSSL 仪表盘](assets/images/dashboard.png){ .doc-screenshot .doc-screenshot--home }](assets/images/dashboard.png){ target="_blank" rel="noreferrer" }

<p class="home-note">LACTSSL 是单管理员、单实例的自托管工具，不提供多用户 RBAC、SSO、集群或 HSM。完整边界见[产品能力](features.md)。</p>
