# 部署外掛

部署外掛把證書交付到 FTP、Webhook、雲 CDN、面板和 WAF 等目標。它是**接觸證書私鑰的高風險元件**，企業應把部署外掛納入准入和變更管理。

## 1. 內建目標與外掛目標

| 型別 | 目標 | 是否內建 | 說明 |
| --- | --- | --- | --- |
| 內建 | 本地目錄 | 是 | 寫本機路徑，可選過載命令 |
| 內建 | SSH / SFTP | 是 | 密碼或私鑰認證，主機指紋校驗 |
| 外掛 | FTP / FTPS | 否 | 普通 FTP、顯式 TLS、隱式 TLS |
| 外掛 | Webhook | 否 | 自定義認證頭，可跳過 TLS 校驗（不建議） |
| 外掛 | 阿里雲 CDN | 否 | 更新網域證書 |
| 外掛 | 七牛雲 | 否 | CDN 加速網域或 Kodo 自定義源站網域 |
| 外掛 | 寶塔面板 / 寶塔 WAF | 否 | 網站、面板或 WAF |
| 外掛 | 1Panel | 否 | 網站或面板 |
| 外掛 | 雷池 WAF | 否 | 按證書 ID 或網域匹配 |
| 外掛 | 騰訊雲 / 火山引擎 / AcePanel 等 | 否 | 以外掛市場實際條目為準 |

> **部署外掛會接收證書私鑰。** 只安裝可信來源的 helper，並確保外掛目錄僅服務執行使用者可寫。部署外掛預設關閉，需要顯式啟用。

## 2. 憑據與部署引數

「目標憑據」只儲存認證與連線資訊，例如主機、埠、使用者名稱、密碼、面板地址和 API Token。證書路徑、過載命令、部署網域、站點等引數在任務流程「部署證書」節點中填寫。

這樣設計的好處：

- 同一憑據可被多個任務複用。
- 憑據輪換不影響任務編排。
- 部署引數隨任務版本留痕，便於審計和回滾。

## 3. 配置

```yaml
deploy_plugins:
  enabled: false          # 外掛會接觸私鑰，預設關閉
  dir: ""                 # 留空表示 <data_dir>/plugins/deploy
  memory_limit_mb: 128    # 單個 helper 常駐記憶體上限
  timeout_seconds: 300    # 單次 deploy 超時，上傳大檔案時可調大
  max_output_kb: 64       # helper 輸出上限
```

三類外掛分別使用 `<data_dir>/plugins` 下的獨立子目錄：

- DNS：`<data_dir>/plugins/dns`
- 部署：`<data_dir>/plugins/deploy`
- 通知：`<data_dir>/plugins/notify`

修改配置後重啟服務生效。啟動時自動掃描；安裝新 helper 後可在「外掛」頁點選「重掃外掛」，或呼叫：

```bash
curl -X POST -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/plugins/rescan
```

## 4. 安裝與啟用

1. 在配置中設定 `deploy_plugins.enabled: true` 並重啟。
2. 開啟「外掛」頁，確認倉庫地址並點選「重新整理索引」。
3. 在「外掛市場」中安裝需要的部署目標。
4. 先啟用外掛檔案，再在部署目標列表中啟用具體目標；新下載的檔案與條目預設停用。

未安裝或未啟用外掛時，目標憑據與任務流程的部署型別中只會出現本地檔案與 SSH；歷史憑據不會丟失，安裝外掛後可繼續使用。

啟用狀態儲存在 settings 表的 `plugins.deploy.enabled_deployers`，只記錄已啟用的目標 ID，重啟和重掃後保持。合併目標在「目標憑據」中只顯示一個廠商入口，具體產品在任務部署節點中透過「部署物件」選擇。

## 5. 目標實踐

### 5.1 本地目錄

- 執行使用者必須對目標目錄有寫權限。
- 建議使用原子寫入：先寫臨時檔案，再 `rename` 覆蓋，避免讀到半個檔案。
- 過載命令示例：`nginx -s reload`、`systemctl reload nginx`、`systemctl reload postfix`。
- 過載命令失敗不會回滾證書檔案；需要在目標側保留上一版。

### 5.2 SSH / SFTP

- 預設要求填寫目標主機指紋（`SHA256:...`）。只有顯式開啟“首次連線自動信任主機金鑰”時才採用 TOFU；後續指紋不一致會拒絕連線。
- 伺服器更換金鑰後需要清空舊指紋重新記錄。
- 建議使用專用部署使用者，只授予目標路徑寫權限和必要過載權限（sudoers 精確到命令）。
- 私鑰認證優先於密碼；私鑰放在受控位置，設定口令並定期輪換。

### 5.3 FTP / FTPS

- 預設顯式 TLS；只有明確選擇 `none` 才使用明文 FTP。
- 證書私鑰和登入密碼會經過網路；生產優先 SFTP 或 FTPS。
- 限制目標 IP、埠和目錄權限；不要使用共享帳號。

### 5.4 Webhook

- 公網目標強制 HTTPS；迴環、私網、`.local`、`.internal` 允許 HTTP。
- Webhook 會攜帶證書與私鑰，接收方必須可信；建議使用獨立 Token 和來源 IP 限制。
- 不建議開啟“跳過 TLS 校驗”；如必須，限定到內網自籤目標。內網 CA 簽發的證書應透過「系統設定 → TLS 信任鏈」匯入 CA 公鑰，讓部署外掛在正常校驗下信任目標。
- 接收方應做冪等和簽名校驗，避免重放。

### 5.5 雲 CDN / 面板 / WAF

- 憑據使用專用子帳號，只授予證書管理相關權限。
- 部署引數（網域、站點、證書名稱）必須與目標系統一致；寶塔面板的網站與 Docker 網站名稱可透過面板 API 讀取後下拉選擇。
- 外掛未實現動態選項或面板不可達時，仍可手工輸入名稱。
- 目標系統開啟 IP 白名單時，放行 LACTSSL 出口。
- CDN/WAF 可能有證書快取；部署後驗證實際生效證書。

## 6. 部署記錄與驗證

- 每次部署生成部署記錄，可在證書詳情和日誌中檢視結果。
- 手動部署優先複用證書所屬任務的部署節點引數，也可以按目標單獨填寫本次引數。
- 本地/SSH 部署會執行過載命令；API 金鑰必須顯式擁有 `deploy:execute`。
- 部署失敗不會自動回滾目標系統舊證書；需要在目標側保留上一版或使用原子釋出。

驗證清單：

- [ ] 目標路徑/站點正確。
- [ ] 證書鏈完整（fullchain）。
- [ ] 私鑰與證書匹配。
- [ ] 服務已過載並實際載入新證書。
- [ ] 多例項/多節點全部更新。
- [ ] CDN/WAF 快取已重新整理。
- [ ] 瀏覽器/客戶端顯示新證書序列號或指紋。

## 7. 離線與手動安裝

- 在「外掛」頁上傳單個 helper，最大 256 MB；只有啟用 `deploy_plugins.enabled` 後才可使用。
- 上傳後先校驗 `manifest`、部署目標 ID 與 SHA-256，透過後才寫入部署外掛目錄並立即重掃。
- 預設拒絕覆蓋同名檔案；升級時勾選“允許覆蓋同名外掛”，舊檔案會先備份。
- 與內建部署目標 ID 重複的外掛會被發現，但重掃結果標記為“已忽略”。

## 8. Helper 協議 v1（整合開發）

helper 是一個可執行檔案，支援以下子命令：

```bash
deploy-helper manifest   # 輸出 JSON 清單
deploy-helper deploy     # 從 stdin 讀取請求並執行部署
deploy-helper options    # 讀取動態下拉選項（可選）
deploy-helper verify     # 只讀驗證認證與目標作用域（可選）
```

`manifest` 輸出示例：

```json
{
  "protocol": "v1",
  "capabilities": ["verify-v1"],
  "name": "example deploy helper",
  "version": "1.0.0",
  "sha256": "可選的 helper 檔案校驗和",
  "deployers": [
    {
      "id": "example-panel",
      "name": "Example Panel",
      "name_i18n": {"zh-CN": "示例面板", "en-US": "Example Panel"},
      "capabilities": ["verify-v1"],
      "description": "把證書推送到 Example 面板",
      "description_i18n": {
        "zh-CN": "把證書推送到 Example 面板",
        "en-US": "Push certificates to an Example panel"
      },
      "aliases": ["example"],
      "fields": [
        {
          "key": "panel_url",
          "label": "面板地址",
          "label_i18n": {"zh-CN": "面板地址", "en-US": "Panel URL"},
          "type": "text",
          "required": true
        },
        {
          "key": "api_token",
          "label": "API Token",
          "label_i18n": {"zh-CN": "API Token", "en-US": "API Token"},
          "type": "password",
          "secret": true,
          "required": true
        }
      ],
      "deploy_fields": [
        {
          "key": "sites",
          "label": "站點",
          "label_i18n": {"zh-CN": "站點", "en-US": "Sites"},
          "type": "text",
          "required": true
        }
      ]
    }
  ]
}
```

欄位說明：

- `fields` 是目標憑據中的認證與連線資訊；`secret: true` 或 `type: "password"` 會在介面返回時脫敏，並隨憑據一起加密儲存。
- `deploy_fields` 是任務流程「部署證書」節點中的引數。
- 兩類欄位的 `key` 不能重複；欄位型別支援 `text`、`password`、`textarea`、`number`、`select`、`switch`。
- 部署目標 `id` 只能包含小寫字母、數字、下劃線或連字元；與內建目標重複的外掛會被忽略。
- `name` / `description` / `label` / `placeholder` / `help` 是預設語言文案；可選 `*_i18n` 表按語言覆蓋。
- 語言鍵使用 BCP 47 形式，例如 `zh-CN`、`en-US`；單個文案表最多 32 種語言。

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

- 退出碼 `0` 表示成功，非 `0` 表示失敗；stderr 會進入部署記錄（敏感值會被脫敏）。
- stdout 可以為空，或輸出一行 JSON：`{"ok": true, "message": "..."}`；`ok` 為 `false` 時按失敗處理。
- `verify` 必須是隻讀操作：不得寫入證書、建立持久物件或執行 `reload_command`；只有 `mode=write` 的顯式寫入測試才允許建立並清理唯一臨時物件。
- 只有宣告 `verify-v1` 能力的部署目標才會收到 `verify` 呼叫；未宣告的目標由主程式顯示“外掛版本不支援驗證”。
- `checks` 用於區分連線、認證、作用域與權限問題；`status` 取值：`ok`、`failed`、`skipped`、`unsupported`、`error`。
- 非法 JSON 會視為錯誤；除錯日誌請寫入 stderr。
- helper 從 `LACTSSL_PLUGIN_MEMORY_LIMIT`（位元組）獲得記憶體預算。
- 主程式週期性檢查 helper 的 `VmRSS`，超過 `memory_limit_mb` 時結束整個程序組並記錄錯誤。
- 主程式只傳遞最小環境變數集合，不會洩漏服務端金鑰。
- 外掛檔案必須可執行、不能允許組或其他使用者寫入；`manifest.sha256` 非空時會校驗。
- 主程式記錄掃描時的檔案摘要，並在每次部署前重新校驗；替換 helper 後需要重掃。

## 9. 安全建議

- 只啟用可信 helper；部署外掛可以讀取證書私鑰，等同於受信程式碼。
- 外掛目錄 `0700`，helper `0755`，屬主為服務執行使用者。
- 生產在 manifest 中填寫 `sha256`，並在升級後更新摘要。
- 設定合理的 `memory_limit_mb` 與 `timeout_seconds`，避免異常外掛拖垮主機。
- 不給 `lactssl` 使用者額外權限，不讓外掛目錄位於共享可寫路徑。
- 部署目標使用最小權限帳號，過載命令精確到具體服務。

## 10. 排障

| 現象 | 原因 | 處理 |
| --- | --- | --- |
| 部署型別只有本地/SSH | 外掛未啟用/未安裝 | 啟用 `deploy_plugins.enabled`，安裝並啟用目標 |
| 外掛載入失敗 | 權限/摘要/協議/符號連結 | 修正權限或重新安裝 |
| SSH 指紋不匹配 | 伺服器換金鑰/配置錯誤 | 核對指紋，清空舊記錄重試 |
| 部署成功但業務仍舊證書 | 未過載/快取/多例項 | 配置過載命令，重新整理快取，檢查所有節點 |
| Webhook 被拒絕 | 公網 HTTP/證書不受信 | 使用 HTTPS 或內網地址，檢查 TLS |
| CDN/面板失敗 | 憑據權限/引數/API 版本 | 檢查目標側權限和引數 |
| 部署超時 | 目標慢/大檔案 | 調大 `timeout_seconds`，檢查網路 |

更多排障見[監控與排障](troubleshooting.md)。

## 相關文件

- [外掛治理](plugins.md)
- [產品能力](features.md)
- [DNS 外掛](dns-plugins.md)
- [通知外掛](notify-plugins.md)
