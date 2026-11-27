# 部署外掛

部署外掛把證書交付到 FTP、Webhook、雲 CDN、面板和 WAF 等目標。它是**接觸證書私鑰的高風險元件**，應納入審查和變更管理。

## 內建目標與外掛目標

| 目標 | 啟用方式 | 說明 |
| --- | --- | --- |
| 本地目錄 | 始終可用 | 寫本機路徑，可選過載命令 |
| SSH / SFTP | 始終可用 | 密碼或私鑰認證，主機指紋校驗 |
| FTP / FTPS | 需啟用 | 普通 FTP、顯式 TLS、隱式 TLS |
| Webhook | 需啟用 | 自訂認證頭，可跳過 TLS 校驗（不建議） |
| 阿里雲 CDN | 需啟用 | 更新網域證書 |
| 七牛雲 | 需啟用 | CDN 加速網域或 Kodo 自定義源站網域 |
| 寶塔面板 / 寶塔 WAF / 1Panel / 雷池 WAF | 需啟用 | 網站、面板或 WAF；雷池按證書 ID 或網域匹配 |
| 騰訊雲 / 火山引擎 / AcePanel 等 | 需啟用 | 以「外掛」頁實際條目為準 |

> **部署外掛會接收證書私鑰。** 只安裝可信來源的 helper，並確保外掛目錄僅服務執行使用者可寫。部署外掛與官方目標預設開啟，請停用不需要的目標。

## 憑據與部署引數

「目標憑據」只儲存認證與連線資訊，例如主機、埠、使用者名稱、密碼、面板地址和 API Token；證書路徑、過載命令、部署網域、站點等引數在任務流程「部署證書」節點中填寫。同一憑據可被多個任務複用，輪換不影響編排，部署引數隨任務版本留痕。

## 配置

```yaml
deploy_plugins:
  enabled: true           # 官方目標預設開啟；外掛會接觸私鑰
  dir: ""                 # 留空表示 <data_dir>/plugins/deploy
  memory_limit_mb: 128    # 單個 helper 常駐記憶體上限
  timeout_seconds: 300    # 單次 deploy 超時，上傳大檔案時可調大
  max_output_kb: 64       # helper 輸出上限
```

修改配置後重啟服務生效。啟動時自動掃描；安裝新 helper 後可在「外掛」頁點選「重掃外掛」，或呼叫：

```bash
curl -X POST -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/plugins/rescan
```

## 安裝與啟用

1. 確認配置中 `deploy_plugins.enabled: true`（預設已開啟）；修改後重啟。
2. 打開「外掛」頁的部署標籤，按需停用不需要的部署目標。

類別關閉時只顯示本地檔案與 SSH；歷史憑據不會丟失。啟用狀態保存在 settings 表，重啟和重掃後保持。狀態鍵為 `plugins.deploy.enabled_deployers`；合併目標在「目標憑據」中只顯示一個廠商入口，具體產品在任務部署節點選擇。

## 目標實踐

### 本地目錄

- 執行使用者必須對目標目錄有寫權限。
- 建議原子寫入：先寫臨時檔案，再 `rename` 覆蓋，避免讀到半個檔案。
- 過載命令示例：`nginx -s reload`、`systemctl reload nginx`、`systemctl reload postfix`；命令失敗不會回滾證書檔案，需在目標側保留上一版。

### SSH / SFTP

- 預設要求填寫目標主機指紋（`SHA256:...`）。只有顯式開啟“首次連線自動信任主機金鑰”時才採用 TOFU；後續指紋不一致會拒絕連線，更換金鑰後需清空舊指紋重新記錄。
- 建議使用專用部署使用者，只授予目標路徑寫權限和必要過載權限（sudoers 精確到命令）。
- 私鑰認證優先於密碼；私鑰放在受控位置，設定口令並定期輪換。

### FTP / FTPS

- 預設顯式 TLS；只有明確選擇 `none` 才使用明文 FTP。證書私鑰和登入密碼會經過網路，生產優先 SFTP 或 FTPS。
- 限制目標 IP、埠和目錄權限；不要使用共享帳號。

### Webhook

- 公網目標強制 HTTPS；迴環、私網、`.local`、`.internal` 允許 HTTP。
- Webhook 會攜帶證書與私鑰，接收方必須可信；建議使用獨立 Token 和來源 IP 限制，並做冪等和簽名校驗，避免重放。
- 不建議開啟“跳過 TLS 校驗”；如必須，限定到內網自籤目標。內網 CA 簽發的證書應透過「系統設定 → TLS 信任鏈」匯入 CA 公鑰，讓外掛在正常校驗下信任目標。

### 雲 CDN / 面板 / WAF

- 憑據使用專用子帳號，只授予證書管理相關權限。
- 部署引數（網域、站點、證書名稱）必須與目標系統一致；寶塔面板的網站與 Docker 網站名稱可透過面板 API 讀取後下拉選擇，外掛未實現動態選項或面板不可達時仍可手工輸入。
- 目標系統開啟 IP 白名單時放行 LACTSSL 出口；CDN/WAF 可能有證書快取，部署後驗證實際生效證書。

## 部署記錄與驗證

- 每次部署生成部署記錄，可在證書詳情和日誌中檢視結果。
- 手動部署優先複用證書所屬任務的部署節點引數，也可以按目標單獨填寫本次引數。
- 本地/SSH 部署會執行過載命令；API 金鑰必須顯式擁有 `deploy:execute`。部署失敗不會自動回滾目標系統舊證書。

驗證清單：

- [ ] 目標路徑/站點正確，證書鏈完整（fullchain），私鑰與證書匹配。
- [ ] 服務已過載並實際載入新證書；多例項/多節點全部更新。
- [ ] CDN/WAF 快取已重新整理。
- [ ] 瀏覽器/客戶端顯示新證書序列號或指紋。

## 離線與手動安裝

- 在「外掛」頁上傳單個 helper，最大 256 MB；需要保持 `deploy_plugins.enabled: true`（預設開啟）才可使用。
- 上傳後先校驗 `manifest`、部署目標 ID 與 SHA-256，透過後才寫入部署外掛目錄並立即重掃。
- 預設拒絕覆蓋同名檔案；升級時勾選“允許覆蓋同名外掛”，舊檔案會先備份。與內建目標 ID 重複的外掛會被忽略。

## Helper 協議 v1（整合開發）

helper 是可執行檔案，支援以下子命令：

```bash
deploy-helper manifest   # 輸出 JSON 清單
deploy-helper deploy     # 從 stdin 讀取請求並執行部署
deploy-helper options    # 讀取動態下拉選項（可選）
deploy-helper verify     # 只讀驗證認證與目標作用域（可選）
```

`manifest` 要點：

- 頂層包含 `protocol: "v1"`、`name`、`version`、可選 `sha256` 和 `deployers[]`。
- 每個部署目標含 `id`、`name`、`capabilities`、`fields` 與 `deploy_fields`；`name`/`description`/`label` 可用 `*_i18n` 按 BCP 47 語言覆蓋，單個文案表最多 32 種語言。
- `fields` 是憑據欄位，`secret: true` 或 `type: "password"` 會脫敏並隨憑據加密；`deploy_fields` 是任務部署節點引數。
- 兩類欄位的 `key` 不能重複，型別支援 `text`、`password`、`textarea`、`number`、`select`、`switch`。
- 部署目標 `id` 只能包含小寫字母、數字、下劃線或連字元；與內建目標重複的外掛會被忽略。

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

`verify` 的 stdin / stdout：

```json
{"action": "verify", "provider": "example-panel", "auth": {"panel_url": "...", "api_token": "..."}, "config": {"sites": "example.com"}, "mode": "read", "locale": "zh-CN"}
```

```json
{"ok": false, "checks": [{"key": "auth", "status": "failed", "message": "invalid token"}]}
```

約定：

- 退出碼 `0` 表示成功，非 `0` 表示失敗；stderr 會進入部署記錄（敏感值脫敏）。
- stdout 可為空，或輸出一行 JSON：`{"ok": true, "message": "..."}`；`ok` 為 `false` 時按失敗處理，非法 JSON 視為錯誤。
- `verify` 必須唯讀：不得寫入證書、建立持久物件或執行 `reload_command`；只有 `mode=write` 的顯式寫入測試才允許建立並清理唯一臨時物件。
- 只有宣告 `verify-v1` 的部署目標才會收到 `verify` 呼叫，未宣告者顯示“外掛版本不支援驗證”；`checks[].status` 取值為 `ok`、`failed`、`skipped`、`unsupported`、`error`。
- helper 從 `LACTSSL_PLUGIN_MEMORY_LIMIT`（位元組）獲得記憶體預算；主程式週期檢查 `VmRSS`，超限時結束整個程序組並記錄錯誤。
- 主程式只傳遞最小環境變數集合；外掛檔案必須可執行、不能允許組或其他使用者寫入，`manifest.sha256` 非空時會校驗；主程式在每次部署前重新校驗摘要，替換 helper 後需要重掃。

## 安全建議

- 只啟用可信 helper；部署外掛可以讀取證書私鑰，等同於受信程式碼。
- 外掛目錄 `0700`，helper `0755`，屬主為服務執行使用者；生產在 manifest 中填寫 `sha256`，並在升級後更新摘要。
- 設定合理的 `memory_limit_mb` 與 `timeout_seconds`，避免異常外掛拖垮主機；不給 `lactssl` 使用者額外權限。
- 部署目標使用最小權限帳號，過載命令精確到具體服務，不讓外掛目錄位於共享可寫路徑。

## 排障

| 現象 | 原因 | 處理 |
| --- | --- | --- |
| 部署型別只有本地/SSH | 部署類別或目標未啟用 | 啟用 `deploy_plugins.enabled`，再啟用目標 |
| 外掛載入失敗 | 權限/摘要/協議/符號連結 | 修正權限或重新安裝 |
| SSH 指紋不匹配 | 伺服器換金鑰/配置錯誤 | 核對指紋，清空舊記錄重試 |
| 部署成功但目標仍使用舊證書 | 未過載/快取/多例項 | 配置過載命令，重新整理快取，檢查所有節點 |
| Webhook 被拒絕 | 公網 HTTP/證書不受信 | 使用 HTTPS 或內網地址，檢查 TLS |
| CDN/面板失敗或超時 | 憑據權限/引數/API 版本/網路 | 檢查目標側權限和引數，調大 `timeout_seconds` |

更多排障見[監控與排障](troubleshooting.md)。
