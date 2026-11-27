---
hide:
  - navigation
  - toc
---

<section class="hero" markdown>
<div class="hero-badge">自託管 · 單實例 · 開源</div>

# LACTSSL

<p class="hero-sub">把證書簽發、自動續期、部署和到期監控收進一個自託管控制台。開發者用一臺小主機即可跑完全流程。</p>

[快速開始](getting-started.md){ .md-button .md-button--primary }
[安裝部署](install.md){ .md-button }
</section>

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-autorenew: 自動續期與部署
ACME 證書集中申請、續期、部署和到期監控，替代分散腳本與人工臺賬。
</div>

<div class="feature-card" markdown>
### :material-server-network: 私有 CA 與內部 ACME
生成或匯入根 / 中間 CA，並向 certbot、lego、acme.sh 提供標準 ACME v2 端點。
</div>

<div class="feature-card" markdown>
### :material-file-export-outline: 多目標交付
內建本地目錄與 SSH/SFTP；FTP、Webhook、雲 CDN、寶塔、1Panel、雷池 WAF 等透過部署外掛擴充。
</div>

<div class="feature-card" markdown>
### :material-monitor-eye: 站點到期監控
定期讀取外部站點證書，校驗證書鏈與主機名，異常和臨期自動通知。
</div>

<div class="feature-card" markdown>
### :material-shield-check-outline: 可審計、可自託管
單二進位制、SQLite、無外部依賴；門禁入口、TOTP/通行金鑰、欄位加密、操作日誌與加密備份。
</div>

<div class="feature-card" markdown>
### :material-bell-ring-outline: 通知與整合
郵件內建，常用通知渠道隨主程式提供；REST API 與按資源授權的 API 金鑰支援 CI/CD。
</div>

</div>

## 文件導覽

<div class="doc-grid" markdown>

<div class="doc-card" markdown>
[:material-rocket-launch-outline: 快速開始](getting-started.md)
<span>安裝、初始化，並簽發第一張證書。</span>
</div>

<div class="doc-card" markdown>
[:material-download-outline: 安裝與啟動](install.md)
<span>Docker、二進位制、反向代理與 HTTPS。</span>
</div>

<div class="doc-card" markdown>
[:material-format-list-checks: 產品能力](features.md)
<span>證書、任務、私有 CA、ACME 服務與監控。</span>
</div>

<div class="doc-card" markdown>
[:material-server-network: 部署架構](architecture.md)
<span>拓撲、埠、容量與可用性。</span>
</div>

<div class="doc-card" markdown>
[:material-shield-lock-outline: 安全基線](security.md)
<span>身份、加密、審計與上線檢查清單。</span>
</div>

<div class="doc-card" markdown>
[:material-cog-outline: 配置參考](configuration.md)
<span>完整配置欄位、反向代理與外掛配置。</span>
</div>

</div>

## 產品介面

[![LACTSSL 儀表盤](assets/images/dashboard.png){ .doc-screenshot .doc-screenshot--home }](assets/images/dashboard.png){ target="_blank" rel="noreferrer" }

<p class="home-note">LACTSSL 是單管理員、單實例的自託管工具，不提供多使用者 RBAC、SSO、叢集或 HSM。完整邊界見[產品能力](features.md)。</p>
