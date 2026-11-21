# DNS 外掛

DNS 外掛為公共 CA 的 DNS-01 驗證提供網域服務商 API 接入。企業選型時重點不是“支援多少家”，而是**憑據最小權限、可輪換、可審計、可離線部署**。

## 1. 使用前提

- 已在「DNS 帳戶」中建立對應服務商的憑據。
- 已在「外掛」頁安裝並啟用 DNS 外掛及具體服務商。
- 網域所在 Zone 對該憑據可見，且允許建立/刪除 TXT 記錄。
- 出站可訪問服務商 API；如經代理，確認 Go HTTP 客戶端代理配置。

未安裝任何 DNS 外掛時，DNS 服務商列表為空。歷史 DNS 帳戶不會丟失，但服務商不可用時簽發會給出明確錯誤。

## 2. 外掛包選擇

官方倉庫 [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins) 為全部約 194 家服務商提供三個 DNS 包：

| 包 | 覆蓋 | 解壓後 | 適用 |
| --- | --- | ---: | --- |
| DNS 全量·重構 | 194 個服務商 | 約 18 MiB | 大多數企業；21 家使用倉庫 native 實現，其餘使用 lego v4 |
| DNS 全量·lego | 194 個服務商 | 約 58 MiB | 需要完整 lego v4 能力（例如 rfc2136 GSS-TSIG） |
| DNS 全量·lego v5 | 235 個條目（228 個 v5 provider + 7 個相容佔位） | 約 37 MiB | 跟進 lego v5 與新增 provider |

三個包互斥，不能同時安裝。

- 只讀驗證（`verify-v1`）僅在「DNS 全量·重構」包中由原生實現提供；使用其他包時，`auto` 模式會自動回退到 TXT 寫測試。
- 每個包共享一份 Go 執行時，避免逐服務商打包的磁碟和程序開銷。
- 主程式釋出包不內建任何服務商；按需安裝。
- 服務商圖示隨外掛包和倉庫索引下發，安裝後快取在 `<data_dir>/plugin-icons/`。

## 3. 安裝與啟用

1. 開啟「外掛」頁，確認倉庫地址，點選「重新整理索引」。
2. 開啟「外掛市場」，篩選 DNS，安裝需要的包。
3. 先啟用外掛檔案，再在 DNS 服務商列表中啟用具體服務商；新下載的檔案與條目預設停用。
4. 在「DNS 帳戶」中建立憑據，選擇該服務商。

合併包不會被行首開關一次性全開；一次啟用超過 10 個條目會要求確認。檔案級停用後，其中的條目不會註冊到執行時，但已有勾選會保留。

安裝、更新、解除安裝只需登入會話或 `plugins:write`。外掛包會校驗索引簽名（如配置公鑰）、asset SHA-256、`plugin.json` 與 manifest 中的服務商 ID，任一不符都會拒絕安裝並回滾。

## 4. 憑據治理

### 4.1 最小權限

- 使用專用子帳號或 API Token，不使用個人帳號、全域性管理員金鑰。
- 只授予目標 Zone 的 DNS 讀寫權限；不要授予帳戶級支付、網域註冊、刪除 Zone 等權限。
- 如果服務商支援按網域/專案授權，限制到實際需要簽發的網域。
- 為測試、生產使用不同憑據和不同 DNS 子帳號。

### 4.2 輪換

- 記錄憑據責任人、建立時間、權限範圍、輪換週期。
- 輪換時先在 LACTSSL 更新憑據，再在服務商側吊銷舊憑據。
- 輪換後執行一條測試任務，確認 TXT 建立/清理正常。
- 人員離職、金鑰洩露或服務商安全事件後立即輪換。

### 4.3 保管

- 憑據在寫入資料庫前使用 AES-256-GCM 加密；介面返回時脫敏。
- 編輯帳戶時留空金鑰欄位表示保留原值。
- 不要把憑據寫入任務名、備註、工單或日誌。
- 備份包包含加密憑據，按機密檔案管理。

## 5. 常見服務商

| 服務商 | 主要憑據 | 備註 |
| --- | --- | --- |
| 阿里雲 DNS（alidns） | AccessKey ID / Secret | 可選 Region |
| 阿里雲 ESA（aliesa） | AccessKey ID / Secret | 可選 Region |
| Cloudflare | API Token，或 Email + Global API Key | 可選 API 上游地址 |
| DNSPod | API Token | |
| 騰訊雲（tencentcloud） | SecretId / SecretKey | 可選 Region |
| 華為雲（huaweicloud） | AccessKey ID / SecretAccessKey / Region | |
| 火山引擎（volcengine） | AccessKey / SecretKey | 可選 Region、API 主機與協議 |
| 百度智慧雲（baiducloud） | AccessKey ID / SecretAccessKey | |
| 京東雲（jdcloud） | AccessKey ID / AccessKey Secret | 可選 Region |
| AWS Route 53 | AccessKey / SecretAccessKey | 可選 Region、Hosted Zone、私有區域 |
| Google Cloud DNS | 服務帳號 JSON | 可選專案 ID |
| Azure DNS | Tenant ID / Client ID / Client Secret / Subscription ID / 資源組 | |
| DigitalOcean / Vultr / Linode / Hetzner | API Token / API Key | |
| Gandi / GoDaddy / Namecheap / NameSilo / Porkbun | 各服務商 API 憑據 | |
| PowerDNS（pdns） | API 地址 / API Key | 可選 Server 名稱與 API 版本 |
| RFC 2136 動態更新 | DNS 伺服器 | 可選 TSIG Key / Secret / 演算法與 Zone 列表 |

- 安裝官方 DNS 外掛後支援 lego 的全部服務商（約 190 家）。
- `manual` 服務商需要人工新增 TXT 記錄，不納入自動流程。
- Cloudflare、DNSimple、PowerDNS、火山引擎等支援自定義 API 上游地址，可用於代理、自建服務或相容閘道器。
- 完整服務商列表以「DNS 帳戶」頁的下拉框為準。

## 6. 離線與手動安裝

- 在「外掛」頁上傳單個 helper 可執行檔案，最大 256 MB。
- 上傳後先以臨時檔案探測 `manifest`，校驗協議、服務商 ID 和 SHA-256，透過後才寫入外掛目錄並重掃。
- 預設拒絕覆蓋同名檔案；升級時勾選“允許覆蓋同名外掛”，舊檔案會先備份。
- 或把 `plugins.repo.url` 指向內部映象索引：

```yaml
plugins:
  repo:
    url: "file:///srv/lactssl-plugins/index.json"
```

手動上傳的多服務商 bundle 仍可載入，但會標記為 `manual`，不參與官方倉庫升級。

## 7. 驗證與排障

### 7.1 驗證

1. 建立 DNS 帳戶，儲存成功。
2. 建立測試任務，選擇該帳戶和測試網域。
3. 執行任務，觀察 TXT 建立、CA 驗證、TXT 清理。
4. 檢查任務日誌和操作日誌。
5. 刪除測試證書或保留按需。

### 7.2 常見問題

| 現象 | 原因 | 處理 |
| --- | --- | --- |
| 服務商下拉框為空 | 未安裝/未啟用外掛 | 安裝 DNS 包並啟用服務商 |
| 憑據儲存失敗 | 必填欄位缺失/格式錯誤 | 按表單和日誌提示修正 |
| TXT 建立失敗 | 權限/Zone/配額 | 檢查 API 權限、網域和配額 |
| TXT 清理失敗 | 權限或記錄已被刪除 | 手動清理殘留 TXT |
| 驗證超時 | DNS 傳播慢/權威 DNS 未更新 | 檢查 TTL、權威解析和 CAA |
| 自定義 API 地址失敗 | 地址不可達/證書不受信 | 檢查網路、TLS 和上游地址 |
| 外掛載入失敗 | 權限/摘要/協議 | 檢查目錄權限和安裝記錄 |

## 8. Helper 協議 v1（整合開發）

本節面向外掛開發者；運維人員通常不需要了解。

helper 是一個可執行檔案，支援四個子命令：

```bash
dns-helper manifest   # 輸出 JSON 清單
dns-helper present    # 從 stdin 讀取請求，新增 TXT 記錄
dns-helper cleanup    # 從 stdin 讀取請求，刪除 TXT 記錄
dns-helper verify     # 從 stdin 讀取請求，執行只讀探測（可選）
```

`manifest` 輸出示例：

```json
{
  "protocol": "v1",
  "capabilities": ["verify-v1"],
  "name": "example dns helper",
  "version": "1.0.0",
  "sha256": "可選的 helper 檔案校驗和",
  "providers": [
    {
      "id": "example-dns",
      "name": "Example DNS",
      "description": "示例服務商",
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

`verify` 的 stdin / stdout：

```json
{"action": "verify", "provider": "example-dns", "config": {"api_token": "..."}, "domain": "example.com", "mode": "read", "locale": "zh-CN"}
```

```json
{"ok": true, "checks": [{"key": "zone", "status": "ok", "message": "example.com"}]}
```

約定：

- 退出碼 `0` 表示成功，非 `0` 表示失敗；stderr 會進入任務日誌（敏感值會被脫敏）。
- stdout 保持為空；`present/cleanup` 的輸出會被視為錯誤。
- `verify` 必須是隻讀操作：只允許列 zone / 查記錄，不得建立、修改或刪除任何記錄。
- 只有宣告 `verify-v1` 能力的服務商才會收到 `verify` 呼叫；未宣告的服務商由主程式回退到 `present/cleanup` 寫測試（`mode=auto`），顯式 `mode=read` 時返回不支援。
- `checks[].status` 取值：`ok`、`failed`、`skipped`、`unsupported`、`error`。
- helper 從 `LACTSSL_PLUGIN_MEMORY_LIMIT`（位元組）獲得記憶體預算。
- 主程式週期性檢查 helper 的 `VmRSS`，超過 `plugins.memory_limit_mb` 時結束程序並記錄錯誤。
- 主程式只傳遞最小環境變數集合（PATH、HOME、代理、TLS 證書等），不會洩漏服務端金鑰。
- helper 檔案不能允許其他使用者寫入；`manifest.sha256` 非空時會校驗檔案摘要。
- `version` 表示 helper/服務商實現自身版本，不是 LACTSSL 構建版本。

## 9. 安全建議

- 外掛目錄只允許服務執行使用者寫入，優先 `0700`。
- 只安裝可信來源的 helper，並在 manifest 中填寫 `sha256`。
- 設定合理的 `memory_limit_mb` 與 `timeout_seconds`，避免異常外掛拖垮主機。
- 外掛以服務帳號執行，不要給 `lactssl` 使用者額外權限。
- 生產使用內部映象，固定版本，關閉自動更新。

## 相關文件

- [外掛治理](plugins.md)
- [產品能力](features.md)
- [部署外掛](deploy-plugins.md)
- [通知外掛](notify-plugins.md)
