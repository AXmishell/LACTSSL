# 外掛治理

LACTSSL 的 DNS、憑證部署與通知能力由外掛提供。官方實現位於 `internal/plugin/` 並編譯進主程式，`plugins/` 是外部外掛開發 SDK；上傳的第三方 helper 以一次性子程序執行，預設受沙箱限制。

## 外掛模型

- **內建外掛**：官方 DNS / 部署 / 通知實現隨主程式發布，不需要聯網安裝。
- **上傳外掛**：管理員可上傳單個 helper，用於臨時驗證或第三方擴充；預設停用，啟用後以子程序執行。
- **兩級啟停**：類別級（`plugins.enabled`、`deploy_plugins.enabled`、`notify_plugins.enabled`）和條目級（DNS 服務商、部署目標、通知渠道白名單）。
- **不再有外掛倉庫**：沒有索引、簽章索引、市場或自動更新；官方外掛更新隨主程式版本發布。

外掛協議保持 v1：

```text
manifest / present / cleanup / deploy / options / plan / send
```

內建實現直接在宿主進程內呼叫；上傳外掛透過 stdin/stdout JSON 與宿主通訊。

## 設定

```yaml
plugins:
  enabled: true
  dir: ""                 # 上傳 DNS helper 目錄，留空 <data_dir>/plugins/dns
  sandbox: strict         # strict / basic / off
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64

deploy_plugins:
  enabled: true           # 部署目標預設開啟；外掛接觸私鑰
  dir: ""
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64

notify_plugins:
  enabled: true
  dir: ""
  sandbox: strict
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64
```

`sandbox` **只作用於上傳/外部 helper**，內建外掛在宿主進程內直接呼叫。`strict` 在 Linux 使用 Landlock 檔案存取白名單、rlimit、`no_new_privs` 和獨立進程組，核心或容器不支援時自動降級為 `basic`（最小環境變數、私有暫存目錄、逾時/RSS/輸出限制）並記錄降級日誌。宿主仍以 root 執行且未設定 `LACTSSL_RUN_UID`/`LACTSSL_RUN_GID` 時，上傳外掛會被拒絕執行，因為 Landlock 無法阻止 root 透過 mount/ptrace 繞過；`off` 僅用於受控除錯。

## 內建外掛與記憶體

內建實現預設全部編譯進主程式；關閉類別或條目不會卸載程式碼，但會避免註冊對應實現和建構 provider。需要顯著減小二進位/記憶體時可使用建置標籤：`make backend-no-dns` 完全不編譯 DNS 內建實現（閒置 RSS 約 36 MB），官方發布預設全量（約 70 MB 閒置 RSS）。全量建置的主要記憶體來自 DNS 的 lego v5 程式碼頁，中繼資料僅約 1 MB；啟用全部 235 個服務商相對預設空列表只增加約 3–5 MB。DNS 內建實現透過全域建構鎖設定並還原 lego v5 環境變數，`present/cleanup` 可並行執行。

## 啟用與停用

### 類別

類別開關決定該類內建實現是否註冊。關閉後，該類內建條目不會出現在可用列表中，上傳的同類 helper 也不會執行，但已儲存的帳戶、任務和白名單保留。

### 條目

- DNS：預設不啟用任何服務商，需要在「外掛」頁手動啟用。
- 部署：類別與 25 個規範目標預設開啟，可按需停用。
- 通知：預設啟用 23 個渠道。

一次啟用超過 10 個條目會要求確認，避免誤開大量 provider。

## 上傳外掛與沙箱

上傳流程：選擇類別與 helper 檔案 → 在沙箱中執行 `manifest` 探測，校驗協議、ID、版本、摘要和平臺 → 寫入外掛目錄（新檔案預設停用）→ 管理員啟用檔案後再啟用條目。

限制：只接受單個可執行檔，最大 256 MB；拒絕符號連結和組/其他使用者可寫檔案；上傳外掛不能佔用內建條目 ID/別名；`strict` 沙箱只能讀取系統 CA/DNS 設定和自身檔案，只能寫入私有暫存目錄，不能讀取資料目錄、設定、資料庫或其他外掛。沙箱不是網路隔離，上傳外掛仍可存取對外網路，請只上傳可信來源。

## 更新、回滾與解除安裝

- 內建外掛隨主程式升級，不能單獨安裝/更新/解除安裝，升級後啟用狀態保留。
- 上傳外掛由管理員重新上傳，舊檔案會先備份，失敗時回滾；刪除上傳檔案會同時清理指紋、條目中繼資料和啟用白名單。

## 舊倉庫外掛遷移

升級前安裝過官方倉庫外掛的部署：官方外掛現在由內建實現提供，舊 helper 檔案應停用或刪除；帳戶、任務和啟用白名單保持不變；不屬於官方集合的自訂外掛保留為「舊版/上傳」外掛，預設按 `strict` 沙箱執行，但不再有更新來源，建議替換或刪除。

## 安全控制

- 內建外掛是主程式供應鏈的一部分，隨程式簽章/映像摘要發布；上傳外掛預設沙箱執行，生產不要設定 `sandbox: off`。
- 部署外掛接觸私鑰，預設開啟，只保留可信目標；外掛目錄 `0700`，helper `0755`，不允許符號連結和組/其他使用者寫入。
- 上傳 helper 的摘要會在發現、註冊和每次呼叫前複核；外掛管理需要 `plugins:read` / `plugins:write`，上傳、啟停寫入稽核日誌，strict 不可用時的降級寫入宿主啟動日誌。

## API

| 方法 | 路徑 | 說明 |
|---|---|---|
| GET | `/api/plugins` | 內建/上傳外掛狀態、條目與檔案 |
| POST | `/api/plugins/rescan` | 重新掃描上傳外掛 |
| POST | `/api/plugins/upload` | 上傳 helper |
| DELETE | `/api/plugins/files/:kind/:file` | 刪除上傳 helper |
| PUT | `/api/plugins/files/:kind/:file` | 檔案級啟停 |
| PUT | `/api/plugins/providers/:id` | DNS 條目啟停 |
| PUT | `/api/plugins/deployers/:id` | 部署條目啟停 |
| PUT | `/api/plugins/notify-channels/:id` | 通知條目啟停 |
| GET | `/api/plugins/icons` | 圖示 |

外掛倉庫相關 API 已移除。

## 排障

| 現象 | 原因 | 處理 |
|---|---|---|
| 內建條目不可用 | 類別關閉或條目未啟用 | 檢查類別設定與條目白名單 |
| 上傳後沒有條目 | 檔案預設停用 | 啟用檔案後再啟用條目 |
| 上傳外掛執行失敗或 Landlock 不可用 | strict 沙箱限制或核心/容器 seccomp 限制 | 檢查錯誤，必要時降級 `basic` 並稽核；Docker 需驗證 seccomp |
| ID 衝突或部署私鑰風險 | 與內建條目重名；部署類別啟用 | 換用其他 ID；只保留可信目標，停用不需要的條目 |
