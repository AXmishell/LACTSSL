---
hide:
  - navigation
  - toc
---

<section class="hero" markdown>
<div class="hero-badge">面向企業團隊與個人 / 小團隊 · 自託管 · 單租戶</div>

# LACTSSL

<p class="hero-sub">把公網證書簽發、證書部署、到期監控和內部 PKI 收斂到一個可審計的自託管控制台。企業用它統一多業務線的證書治理，個人開發者、站長和小團隊用一臺小主機就能跑完全自動續期與部署。</p>

[快速上手](getting-started.md){ .md-button .md-button--primary }
[部署架構](architecture.md){ .md-button }
[GitHub 倉庫](https://github.com/AXmishell/LACTSSL){ .md-button target="_blank" rel="noreferrer" }
</section>

[![LACTSSL 儀表盤](assets/images/dashboard.png){ .doc-screenshot .doc-screenshot--home }](assets/images/dashboard.png){ target="_blank" rel="noreferrer" }

## 為什麼選擇 LACTSSL

同一套產品，既服務企業多業務線，也服務個人站點、小團隊和家庭實驗室。

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-certificate-outline: 證書到期風險收斂
集中管理 ACME 證書的申請、續期、部署和到期監控，減少“證書過期導致業務中斷”的分散腳本與人工臺賬。企業可覆蓋多業務線，個人站點只需接一個網域。
</div>

<div class="feature-card" markdown>
### :material-server-network: 內網 PKI 與 ACME 服務
用私有 CA 為內部服務簽發證書，並可把標準 ACME v2 端點開放給 certbot、lego、acme.sh 等客戶端；家庭實驗室和開發環境也能有自己的本地 CA。
</div>

<div class="feature-card" markdown>
### :material-file-export-outline: 多目標交付
內建本地目錄與 SSH/SFTP；FTP、Webhook、主流雲 CDN、寶塔、1Panel、雷池 WAF 等目標透過部署外掛按需擴充套件，從雲主機到面板建站都能覆蓋。
</div>

<div class="feature-card" markdown>
### :material-shield-check-outline: 可審計、可自託管
單二進位制、SQLite、無外部依賴；門禁入口、TOTP/通行金鑰、欄位加密、操作日誌與加密備份，資料不出企業邊界，也不出你自己的伺服器。資源佔用低，一臺小主機即可長期執行。
</div>

</div>

## 適用場景

| 場景 | 適用物件 | 典型問題 | LACTSSL 的做法 |
| --- | --- | --- | --- |
| 個人站點、部落格與副業專案 | 個人 / 小團隊 | 證書到期提醒靠記憶，續期腳本散落在各臺機器上 | 一個任務搞定申請、續期和部署，到期前自動執行並留痕 |
| 家庭實驗室與內網服務 | 個人 / 小團隊 | 自簽證書瀏覽器告警多，客戶端信任和輪換麻煩 | 私有 CA 分層簽發，並提供標準 ACME 端點 |
| 多網域、多業務線證書治理 | 企業 | 證書分散在個人帳號、腳本和表格中，續期失敗無人發現 | 統一任務編排、自動續期、部署記錄與到期看板 |
| 內部服務 TLS | 企業 | 內網服務使用自簽證書，客戶端信任和輪換困難 | 私有 CA 分層簽發，並提供標準 ACME 端點 |
| 證書交付自動化 | 兩者 | 證書簽發後仍需人工上傳到面板、CDN 或伺服器 | 任務內編排本地/SSH/外掛部署，支援過載命令 |
| 安全與審計要求 | 企業 | 無法回答“誰在何時簽發了什麼、部署到哪裡” | 操作日誌、系統日誌、API 金鑰分權與加密備份 |

## 選型邊界

B 端和 C 端選型都應先確認產品邊界。以下能力是當前版本的真實範圍：

| 能力 | 當前版本 | 對個人 / 小團隊的影響 |
| --- | --- | --- |
| 部署形態 | 自託管單例項，單租戶 | 一臺小主機、VPS 或 NAS 即可執行 |
| 管理帳戶 | 單個管理員帳戶，不提供多使用者、角色或組織架構 | 個人或小團隊共用即可滿足 |
| 機器身份 | API 金鑰按“資源:動作”授權，可設定過期時間與啟停 | 可給 CI/CD 或腳本單獨發一把受限金鑰 |
| 身份聯邦 | 不提供 LDAP / OIDC / SAML / SCIM；不能接入企業 SSO | 不依賴企業帳號體系，本地登入即可 |
| 高可用 | 不提供內建叢集或雙活；SQLite 單寫，生產建議單活 + 備份/快速恢復 | 個人場景按定期備份即可 |
| 金鑰託管 | 私鑰加密儲存在本地資料庫；不提供 HSM / KMS 託管 | 無需額外金鑰服務 |
| 監控整合 | 提供操作日誌、系統日誌和認證 API；不提供 Prometheus 指標端點 | 依賴內建通知即可 |
| 合規認證 | 專案本身不附帶等保、ISO 27001 等認證證書；可提供控制項與審計證據 | 與個人使用無關 |
| 支援模式 | 專案採用 MIT 許可證；商業支援、SLA 和託管服務需另行約定 | 社群文件與 issue 支援 |

> 如果企業要求多使用者 RBAC、SSO、多租戶或雙活叢集，需要在上層平臺或組織流程中補齊，不能把 LACTSSL 當作完整 IAM/PKI 平臺使用。

## 參考架構

```text
                    企業使用者 / 個人使用者 / 自動化系統
                           │ HTTPS
                    ┌──────▼──────┐
                    │ 反向代理/WAF │   TLS 終止、訪問控制、審計
                    └──────┬──────┘
                           │ HTTP (內網)
                    ┌──────▼──────────────────────────┐
                    │ LACTSSL 單例項                   │
                    │ 管理控制台 + API + 排程器         │
                    │ 私有 CA + ACME 服務（可選）       │
                    │ SQLite + 本地加密金鑰             │
                    └──┬──────────┬──────────┬────────┘
                       │          │          │
                 DNS API     部署目標     通知渠道
               （公網 CA）  （SSH/面板/CDN） （SMTP/Webhook）
```

詳細拓撲、埠、容量和可用性設計見[部署架構](architecture.md)。

## 按角色閱讀

| 角色 | 關注點 | 建議閱讀 |
| --- | --- | --- |
| 個人開發者 / 站長 / 小團隊 | 裝起來、接網域、自動續期和部署 | [安裝與啟動](install.md)、[DNS 外掛](dns-plugins.md)、[部署外掛](deploy-plugins.md) |
| 業務負責人 / 選型評估 | 價值、邊界、試點週期、驗收標準 | [快速上手與上線](getting-started.md)、[產品能力](features.md) |
| 平臺 / 運維負責人 | 部署拓撲、備份恢復、升級、告警 | [部署架構](architecture.md)、[運維手冊](operations.md) |
| 安全 / 合規 | 身份、加密、審計、供應鏈、控制項 | [安全基線](security.md)、[管理工作臺](administration.md) |
| 整合 / 自動化開發 | API 金鑰、權限、CI/CD、錯誤處理 | [API 整合](api.md)、[擴充套件開發](plugin-development.md) |

## 推薦上線路徑

**個人 / 小團隊**：Docker 起一個例項 → 裝 DNS 與部署外掛 → 建一個網域任務 → 驗證續期和通知，通常半天內完成。不需要走完整的企業試點流程。

**企業團隊**：

1. **評估（0.5–1 天）**：用 Docker 啟動試點例項，跑通一張測試證書和一次本地部署。
2. **試點（1–2 周）**：接入 1–2 個真實網域或一個內部 CA 場景，驗證 DNS 憑據、部署目標和通知鏈路。
3. **生產（按變更視窗）**：確定反向代理、備份、監控和 API 金鑰責任人，完成安全基線後切換。
4. **運營（持續）**：按月度檢查到期、備份、日誌和外掛版本，按季度做恢復演練。

具體步驟和驗收表見[快速上手與上線](getting-started.md)。

## 能力總覽

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-autorenew: ACME 自動化
DNS-01 驗證，支援單網域、多網域和萬用字元；到期前自動續期，失敗可重試並留痕。
</div>

<div class="feature-card" markdown>
### :material-sitemap-outline: 任務編排
申請 → 部署（可多個）→ 通知，節點可啟停和排序，同一憑據可被多個任務複用。
</div>

<div class="feature-card" markdown>
### :material-lock-outline: 私有 CA
生成或匯入根 CA / 中間 CA，配置路徑長度、Key Usage、Name Constraints、CRL/OCSP 地址等 PKI 引數。
</div>

<div class="feature-card" markdown>
### :material-api: 標準 ACME 服務端
按端點繫結私有 CA，支援 EAB、網域白名單、HTTP-01/DNS-01/TLS-ALPN-01、mTLS 與訂單限流。
</div>

<div class="feature-card" markdown>
### :material-monitor-eye: 站點證書監控
定期讀取外部站點證書，校驗證書鏈與主機名，異常和臨期自動通知。
</div>

<div class="feature-card" markdown>
### :material-bell-ring-outline: 通知與審計
郵件內建，20+ 渠道由外掛提供；操作日誌和系統日誌可按條件篩選並匯出 CSV/JSON。
</div>

</div>

## 文件導航

<div class="doc-grid" markdown>

<div class="doc-card" markdown>
[:material-rocket-launch-outline: 快速上手與上線](getting-started.md)
<span>個人快速上手，以及企業試點、驗收、生產切換與責任分工。</span>
</div>

<div class="doc-card" markdown>
[:material-server-network: 部署架構](architecture.md)
<span>拓撲、埠、容量、可用性與網路邊界。</span>
</div>

<div class="doc-card" markdown>
[:material-shield-lock-outline: 安全基線](security.md)
<span>身份、加密、審計、出站防護和控制項對映。</span>
</div>

<div class="doc-card" markdown>
[:material-wrench-outline: 運維手冊](operations.md)
<span>備份恢復、監控、變更、故障回應和演練。</span>
</div>

<div class="doc-card" markdown>
[:material-code-braces: API 整合](api.md)
<span>金鑰治理、權限矩陣和自動化示例。</span>
</div>

<div class="doc-card" markdown>
[:material-lifebuoy: 監控與排障](troubleshooting.md)
<span>常見故障、診斷路徑和升級資訊收集。</span>
</div>

</div>

## 技術棧

| 層級 | 實現 |
| --- | --- |
| 後端 | Go + Gin + SQLite |
| 前端 | Vue 3 + Vite + Element Plus（構建後嵌入二進位制） |
| ACME | lego / RFC 8555 |
| 儲存 | SQLite，敏感欄位 AES-256-GCM 加密 |
| 部署 | 單二進位制，無外部資料庫和訊息佇列 |
