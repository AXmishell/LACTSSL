# 通知外掛

通知外掛把證書簽發、部署、監控和系統事件推送到 IM、郵件或 Webhook。重點是告警路由、渠道可用性、金鑰輪換和噪音控制。

## 內建與外掛渠道

| 渠道 | 說明 |
| --- | --- |
| 郵件（SMTP） | 支援 SSL；預設隱式 TLS |
| Gotify、ServerChan、ntfy、Telegram、Bark、PushDeer | 自建/第三方推送、移動端推送 |
| 釘釘、企業微信機器人/應用/智慧機器人、飛書 | IM |
| GoCQHTTP、iGot、PushPlus、WePlus、WxPusher、WxPusher SPT、OpenILink、WPush、Synology Chat、PushMe、ChronoCat、通用自訂 Webhook | 其他渠道與內部告警平臺 |

官方渠道隨主程式內建，預設啟用常用渠道；只有已啟用的渠道會出現在「通知」頁面與任務節點。郵件始終可用。

## 配置

```yaml
notify_plugins:
  enabled: true           # 預設開啟
  dir: ""                 # 留空表示 <data_dir>/plugins/notify
  memory_limit_mb: 128    # 單個 helper 常駐記憶體上限
  timeout_seconds: 30     # 單次 send 超時
  max_output_kb: 64       # helper 輸出上限
```

修改配置後重啟生效；啟動時自動掃描，也可在「外掛」頁點選「重掃外掛」或呼叫：

```bash
curl -X POST -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/plugins/rescan
```

## 安裝與啟用

1. 開啟「外掛」頁的通知標籤，啟用需要的渠道。
2. 在「通知」頁面配置渠道憑據並傳送測試訊息。

只有已啟用的渠道才會出現在「通知」頁面與任務通知節點中；郵件始終可用，不能停用。

## 告警設計

### 路由

| 事件 | 建議去向 | 說明 |
| --- | --- | --- |
| 證書簽發失敗 | 運維告警 | 需要立即處理，可能影響續期 |
| 部署失敗 | 運維 + 目標系統負責人 | 檢查目標系統和過載命令 |
| 證書臨期 / 站點監控異常 | 運維 + 目標系統負責人 | 30/14/7 天分級；區分網路、鏈、主機名問題 |
| 備份失敗 / 安全事件 | 平臺 / 安全 | 與證書告警分開 |

### 降噪

- 為不同環境使用不同渠道和頻道，任務通知節點按“始終/僅成功/僅失敗”選擇；測試訊息使用獨立測試頻道，不要進入生產告警群。
- 同類事件設定聚合或靜默視窗（由外部 IM/告警平臺實現）；監控項按重要性分級，並定期清理失效渠道和重複配置。

### 可用性

- 至少配置兩個獨立渠道（如郵件 + IM），避免單點。
- 定期傳送測試訊息，確認 Token/Webhook 未過期；關注渠道速率限制和內容稽核。通知失敗會寫入日誌，建議把通知失敗本身也納入監控。

## 渠道憑據治理

- 使用專用機器人/應用，不使用個人帳號 Token；只授予傳送訊息所需權限，不同環境/團隊使用不同機器人，記錄 Token 責任人、建立時間、輪換週期。
- 憑據在寫入資料庫前加密，介面返回時脫敏；不要把 Token、Webhook URL 寫入任務名、備註或日誌；洩露時立即在渠道側吊銷並更新 LACTSSL 配置。

## 郵件（SMTP）

- 預設使用隱式 TLS（`smtp.ssl=true`）。關閉後若伺服器不支援 STARTTLS，服務會拒絕傳送，避免敏感郵件明文傳輸。
- 建議使用 SMTP 服務、專用發件帳號和 SPF/DKIM/DMARC；恢復郵箱與通知發件帳號、測試郵件與生產通知的收件人應分開。

## Helper 協議 v1（整合開發）

helper 是可執行檔案，支援兩個子命令：

```bash
notify-helper manifest   # 輸出 JSON 清單
notify-helper send       # 從 stdin 讀取請求併傳送通知
```

`manifest` 輸出示例：

```json
{
  "protocol": "v1",
  "name": "example notify helper",
  "version": "1.0.0",
  "sha256": "可選的 helper 檔案校驗和",
  "channels": [
    {
      "id": "example-push",
      "name": "Example Push",
      "description": "把通知推送到 Example 服務",
      "fields": [
        {"key": "url", "label": "服務地址", "type": "text", "required": true},
        {"key": "token", "label": "Token", "type": "password", "secret": true, "required": true}
      ]
    }
  ]
}
```

欄位說明：

- `secret: true` 或 `type: "password"` 的欄位會在介面返回時脫敏，並隨渠道配置一起加密儲存；型別支援 `text`、`password`、`textarea`、`number`、`select`、`switch`。渠道 `id` 只能包含小寫字母、數字、下劃線或連字元；與內建郵件 (`mail`) 重複的外掛會被忽略。`label_key` 可選，第三方外掛也可直接提供 `label`。

`send` 的 stdin：

```json
{
  "action": "send",
  "protocol": "v1",
  "channel": "example-push",
  "config": {"url": "https://push.example.com", "token": "..."},
  "title": "證書簽發成功: example.com",
  "body": "任務：example.com\n狀態：成功"
}
```

約定：

- 退出碼 `0` 表示成功，非 `0` 表示失敗；stderr 進入通知日誌（敏感值脫敏）。
- stdout 可為空或輸出一行 JSON：`{"ok": true, "message": "..."}`；`ok` 為 `false` 時按失敗處理。
- 非法 JSON 視為錯誤，除錯日誌寫入 stderr；helper 從 `LACTSSL_PLUGIN_MEMORY_LIMIT`（位元組）獲得記憶體預算，主程式週期檢查 `VmRSS`，超限時結束程序組並記錄錯誤。
- 主程式只傳遞最小環境變數集合，不會洩漏服務端金鑰；外掛檔案必須可執行、不能允許組或其他使用者寫入，`manifest.sha256` 非空時會校驗。替換 helper 後需要重掃。

## 安全建議

- 只啟用可信 helper；通知外掛可以讀取渠道金鑰，等同於受信程式碼。外掛目錄 `0700`，helper `0755`，屬主為服務執行使用者；生產在 manifest 中填寫 `sha256` 並在升級後更新。
- 設定合理的 `memory_limit_mb` 與 `timeout_seconds`，避免異常外掛拖慢通知傳送；通知內容可能包含網域、錯誤詳情和內部地址，避免傳送到公開頻道，Webhook 目標使用 HTTPS 和簽名校驗。

## 排障

| 現象 | 原因 | 處理 |
| --- | --- | --- |
| 通知頁面只有郵件 / 渠道不在節點中 | 渠道未啟用 | 在「外掛」頁與通知頁啟用渠道 |
| 測試訊息失敗或延遲 | Token/URL/網路/限流 | 檢查渠道配置、出站策略和渠道狀態 |
| 郵件傳送失敗 | SMTP TLS/認證/發件人 | 檢查隱式 TLS、STARTTLS、SPF/DKIM |
| 通知延遲 | 渠道限流/網路 | 檢查渠道狀態和日誌 |
| 通知內容含敏感資訊 | 模板/日誌脫敏不足 | 調整內容，避免傳送憑據和私鑰 |

更多排障見[監控與排障](troubleshooting.md)。
