# DNS 外掛

DNS 外掛為公共 CA 的 DNS-01 驗證提供網域服務商 API 接入。重點是憑據最小權限、可輪換、可審計、可離線部署。

## 使用前提

- 已在「DNS 帳戶」中建立對應服務商的憑據。
- 已在「外掛」頁啟用 DNS 類別及具體服務商。
- 網域所在 Zone 對該憑據可見，且允許建立/刪除 TXT 記錄。
- 出站可訪問服務商 API；經代理時確認 Go HTTP 客戶端代理配置。

服務商預設全部停用，歷史 DNS 帳戶不會丟失；未啟用對應服務商時，簽發會給出明確錯誤。

## 外掛包選擇

官方 DNS 實現已內建，預設全量構建覆蓋約 235 個條目（228 個 lego v5 provider + 7 個相容 ID），無需安裝外掛包。所有服務商預設停用，請在「外掛」頁按需啟用。

需要更小的二進位制或記憶體時，可使用精簡構建：`make backend-dns-common` 只連結常用白名單，`make backend-dns-minimal` 僅保留 Cloudflare，`make backend-no-dns` 完全不編譯內建 DNS。精簡構建仍支援上傳 helper。

## 安裝與啟用

1. 開啟「外掛」頁，確認 DNS 類別已啟用。
2. 在 DNS 服務商列表中啟用需要的服務商；一次啟用超過 10 個條目會要求確認。
3. 在「DNS 帳戶」中建立憑據，選擇該服務商。

啟用狀態保存在 settings 表；關閉類別後條目不會註冊到執行時，但已有勾選會保留。

## 憑據治理

### 最小權限

- 使用專用子帳號或 API Token，不使用個人帳號、全域管理員金鑰。
- 只授予目標 Zone 的 DNS 讀寫權限，不授予支付、網域註冊、刪除 Zone 等權限。
- 測試與生產使用不同憑據和不同子帳號。

### 輪換

- 記錄憑據責任人、建立時間、權限範圍、輪換週期。
- 輪換時先在 LACTSSL 更新，再到服務商側吊銷舊憑據，並執行測試任務確認 TXT 建立/清理正常。
- 人員離職、金鑰洩露或服務商安全事件後立即輪換。

### 保管

- 憑據在寫入資料庫前使用 AES-256-GCM 加密，介面返回時脫敏；編輯帳戶時留空金鑰欄位表示保留原值。
- 不要把憑據寫入任務名、備註、工單或日誌；備份包含加密憑據，按機密檔案管理。

## 常見服務商

| 服務商 | 主要憑據 | 備註 |
| --- | --- | --- |
| 阿里雲 DNS（alidns）/ ESA（aliesa） | AccessKey ID / Secret | 可選 Region |
| Cloudflare | API Token，或 Email + Global API Key | 可選 API 上游地址 |
| DNSPod | API Token | |
| 騰訊雲（tencentcloud） | SecretId / SecretKey | 可選 Region |
| 華為雲（huaweicloud） | AccessKey ID / SecretAccessKey / Region | |
| 火山引擎 / 百度智慧雲 / 京東雲 | AccessKey / SecretKey（名稱略有差異） | 可選 Region |
| AWS Route 53 | AccessKey / SecretAccessKey | 可選 Region、Hosted Zone、私有區域 |
| Google Cloud DNS | 服務帳號 JSON | 可選專案 ID |
| Azure DNS | Tenant ID / Client ID / Client Secret / Subscription ID / 資源組 | |
| DigitalOcean / Vultr / Linode / Hetzner / Gandi / GoDaddy / Namecheap / NameSilo / Porkbun | API Token / API Key / 各服務商憑據 | |
| PowerDNS（pdns） | API 地址 / API Key | 可選 Server 名稱與 API 版本 |
| RFC 2136 動態更新 | DNS 伺服器 | 可選 TSIG Key / Secret / 演算法與 Zone 列表 |

- 內建實現覆蓋 lego v5 的全部服務商。
- `manual` 服務商需要人工新增 TXT 記錄，不納入自動流程。
- Cloudflare、DNSimple、PowerDNS、火山引擎等支援自訂 API 上游地址，可用於代理、自建服務或相容閘道器；完整列表以「DNS 帳戶」頁下拉框為準。

## 離線與手動安裝

內建服務商隨主程式分發，離線環境無需額外安裝。第三方 provider 可在「外掛」頁上傳單個 helper（最大 256 MB）：上傳時校驗協議、ID 與 SHA-256，預設拒絕覆蓋同名檔案，升級時舊檔案會先備份。手動上傳的多服務商 bundle 會標記為 `manual`，不參與自動更新。

## 驗證與排障

### 驗證

1. 建立 DNS 帳戶並儲存，再建立測試任務並選擇該帳戶和測試網域。
2. 執行任務，觀察 TXT 建立、CA 驗證與清理，檢查任務日誌和操作日誌。

### 常見問題

| 現象 | 原因 | 處理 |
| --- | --- | --- |
| 服務商下拉框為空 | DNS 類別或服務商未啟用 | 啟用類別與對應服務商 |
| 憑據儲存失敗 | 必填欄位缺失/格式錯誤 | 按表單和日誌提示修正 |
| TXT 建立失敗 | 權限/Zone/配額 | 檢查 API 權限、網域和配額 |
| TXT 清理失敗 | 權限或記錄已被刪除 | 手動清理殘留 TXT |
| 驗證超時 | DNS 傳播慢/權威 DNS 未更新 | 檢查 TTL、權威解析和 CAA |
| 自訂 API 地址失敗 | 地址不可達/證書不受信 | 檢查網路、TLS 和上游地址 |
| 外掛載入失敗 | 權限/摘要/協議 | 檢查目錄權限和安裝記錄 |

## Helper 協議 v1（整合開發）

本節面向外掛開發者；運維人員通常不需要了解。helper 是可執行檔案，支援四個子命令：

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

- 退出碼 `0` 表示成功，非 `0` 表示失敗；stderr 會進入任務日誌（敏感值會脫敏）；stdout 保持為空，`present`/`cleanup` 的輸出會被視為錯誤。
- `verify` 必須唯讀：只允許列 zone、查記錄；只有宣告 `verify-v1` 的服務商才會收到呼叫，未宣告者由 `mode=auto` 回退到 TXT 寫測試，顯式 `mode=read` 時返回不支援。
- `checks[].status` 取值：`ok`、`failed`、`skipped`、`unsupported`、`error`。
- helper 從 `LACTSSL_PLUGIN_MEMORY_LIMIT`（位元組）獲得記憶體預算；主程式週期檢查 `VmRSS`，超過 `plugins.memory_limit_mb` 時結束程序並記錄錯誤。
- 主程式只傳遞最小環境變數集合（PATH、HOME、代理、TLS 證書等），不會洩漏服務端金鑰。
- helper 檔案不能允許其他使用者寫入；`manifest.sha256` 非空時會校驗檔案摘要。`version` 是 helper 自身版本，不是 LACTSSL 構建版本。

## 安全建議

- 外掛目錄只允許服務執行使用者寫入，優先 `0700`；只安裝可信來源的 helper，並在 manifest 中填寫 `sha256`。
- 設定合理的 `memory_limit_mb` 與 `timeout_seconds`，避免異常外掛拖垮主機。
- 外掛以服務帳號執行，不要給 `lactssl` 使用者額外權限；生產固定版本並關閉自動更新。
