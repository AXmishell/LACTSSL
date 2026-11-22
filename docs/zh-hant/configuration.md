# 配置參考

LACTSSL 的應用配置只來自 `config.yaml` 和內建預設值，不讀取應用級環境變數。修改配置後需要重啟服務生效；部分執行時設定（門禁入口、外掛倉庫、外掛啟用狀態）儲存在資料庫中，優先順序高於配置檔案。

## 1. 配置檔案模型

預設讀取當前工作目錄下的 `config.yaml`；也可在啟動時用 `-config` 指定其他路徑。

- 檔案不存在時，首次啟動會自動生成，並寫入隨機生成的 `jwt_secret` 與 `salt`。
- 包含金鑰，建議保持 `0600` 權限。
- 應用配置只來自 `config.yaml` 與內建預設值；系統代理等 Go HTTP 客戶端行為仍可透過環境變數配置。
- 配置變更後重啟生效；門禁入口可在介面修改，立即生效。
- 完整帶註釋的示例見倉庫根目錄 [`config.yaml.example`](https://github.com/AXmishell/LACTSSL/blob/main/config.yaml.example)。

### 1.1 最小生產配置

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

data_dir: "data"

security:
  public_url: "https://ssl.example.com"
  session_persistence: true

log:
  level: info
  format: json
  file: "data/logs/lactssl.log"
  also_stdout: true
  max_size_mb: 50
  max_backups: 10
  max_age_days: 30
  compress: true
  access_log: true
  system:
    enabled: true
    level: info
    access: false
    retention_days: 30
    max_rows: 200000
  archive:
    enabled: true
    compress: true
    retention_days: 0
    max_total_mb: 0
    quota_policy: fail
```

### 1.2 配置優先順序

| 配置 | 來源 | 說明 |
| --- | --- | --- |
| 監聽地址、日誌、金鑰、ACME | `config.yaml` | 修改後重啟生效 |
| 門禁入口 | 資料庫，`config.yaml` 可覆蓋 | 介面修改立即生效；`security.entry` 非空時啟動覆蓋 |
| 外掛倉庫地址/渠道/自動更新/公鑰 | 資料庫優先，`config.yaml` 提供預設值 | 外掛頁儲存不寫回配置檔案 |
| 外掛啟用狀態 | 資料庫白名單 | 重啟和重掃後保持 |
| 系統語言、通知渠道等 | 資料庫 | 介面修改 |

### 1.3 Web 配置檔案入口

登入後可在「系統設定 → 配置檔案」中修改 `config.yaml` 的白名單欄位，包括監聽地址、可信反向代理、`security.public_url`、會話保持、限流閾值、ACME、日誌、升級與外掛執行引數。

- 限流頁籤展示合併內建預設值後的生效規則；儲存時會寫回 `config.yaml`，重啟後生效。
- `jwt_secret`、`salt`、`data_dir`、`database.path`、日誌檔案、外掛目錄與倉庫信任根等高風險欄位仍只能在伺服器上編輯。
- 儲存前會自動在 `<data_dir>/config-backups/` 建立配置快照；也可手動建立。快照只包含 `config.yaml`，與包含資料庫的加密全量備份不同。
- 恢復快照只回寫白名單欄位，當前的 `jwt_secret`、`salt`、`data_dir`、`database.path` 保持不變，避免破壞資料庫解密。
- 儲存後需重啟服務生效。修改監聽埠時會先做埠預檢；若新地址不可用，服務可能無法自動恢復，請保留 SSH 或控制台訪問。
- 「可信反向代理」支援自動檢測：檢測只使用當前請求的 TCP 對端地址，`X-Forwarded-For` 等轉發頭僅作展示，不會被自動加入可信列表。檢測結果會區分已儲存與當前生效的可信列表，並顯示客戶端 IP 來源、跳過的可信項和轉發頭解析告警。反代場景建議同時配置 `security.public_url`。

## 2. 完整欄位示例

```yaml
server:
  addr: ""              # 完整監聽地址，如 ":8080"；非空時優先於 host + port
  host: ""              # 監聽網路卡，留空表示所有網路卡
  port: 8080            # 監聽埠
  trusted_proxies: []   # 可信反向代理 CIDR

data_dir: "data"        # 資料目錄，存放外掛、日誌與升級備份

database:
  driver: "sqlite"      # sqlite（預設）或 postgres
  path: ""              # SQLite 路徑；留空表示 <data_dir>/lactssl.db
  dsn: ""               # PostgreSQL DSN；driver=postgres 時必填
  max_open_conns: 10    # PostgreSQL 連線池
  max_idle_conns: 5
  conn_max_lifetime_minutes: 30
  allow_multi_instance: false  # 是否允許多例項共享同一個 PostgreSQL 資料庫

log:
  level: info           # 程序日誌級別：debug、info、warn、error
  format: text          # 輸出格式：text 或 json
  file: ""              # 日誌檔案；留空僅輸出 stdout
  also_stdout: true     # 寫檔案時是否同時輸出 stdout
  max_size_mb: 20       # 單檔案大小上限，超過後輪轉
  max_backups: 5        # 保留歷史檔案數量，0 表示不限制
  max_age_days: 30      # 歷史檔案保留天數，0 表示不限制
  compress: true        # 是否壓縮歷史檔案
  access_log: true      # 是否記錄 HTTP 訪問日誌
  system:
    enabled: true       # 是否寫入資料庫供「日誌 → 系統日誌」頁籤檢視
    level: info         # 寫入資料庫的最低階別
    access: false       # 是否把 HTTP 訪問日誌也寫入資料庫
    retention_days: 7   # 保留天數
    max_rows: 20000     # 最大行數
  archive:
    enabled: true       # 刪除/清空日誌前先歸檔；歸檔失敗會阻止刪除
    dir: ""             # 留空表示 <data_dir>/archives/logs
    compress: true      # 是否 gzip 壓縮
    retention_days: 0   # 歸檔檔案保留天數，0 表示永久保留
    max_total_mb: 0     # 歸檔目錄容量上限（MB），0 表示不限制
    quota_policy: fail  # fail 或 prune_oldest

security:
  entry: ""             # 門禁入口，留空則首次啟動隨機生成
  public_url: ""        # 對外訪問地址，如 "https://ssl.example.com"
  session_persistence: true  # 為 false 時程序重啟後清除登入會話
  jwt_secret: ""        # JWT 簽名金鑰，留空自動生成並寫回；手動配置至少 32 字元
  salt: ""              # 敏感欄位加密鹽，留空自動生成並寫回；手動配置至少 32 字元
  rate_limit:
    max_keys: 10000     # 記憶體限流器最多保留的 key 數
    rules:
      # max 為視窗內允許次數（0 表示不限制），window_seconds 為視窗秒數。
      # 未列出的規則使用內建預設值；完整規則列表見 config.yaml.example。
      login_ip: {max: 10, window_seconds: 300}
      login_account: {max: 20, window_seconds: 900}
      api_ip: {max: 600, window_seconds: 60}
      acme_request: {max: 1200, window_seconds: 60}
      acme_order: {max: 300, window_seconds: 3600}

acme:
  enabled: true         # 是否啟用內建 ACME 服務端，預設開啟
  addr: ""              # 獨立監聽地址，如 ":14000"；留空掛載到主埠 /acme
  base_url: ""          # 對外地址，如 "https://pki.example.com:14000"
  tls:
    cert: ""            # ACME 獨立監聽 TLS 證書
    key: ""             # ACME 獨立監聽 TLS 私鑰
  mtls:
    enabled: false      # 是否要求客戶端證書
    client_ca: ""       # 客戶端 CA 證書路徑

plugins:
  enabled: true
  dir: ""                 # 留空表示 <data_dir>/plugins/dns
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64
  repo:
    enabled: true
    url: "https://github.com/AXmishell/LACTSSL-Plugins/releases/download/plugin-index/index.json"
    channel: stable
    auto_update: false
    public_key: "t7Q4vd0B1zh3mRf0t6Q6qlBViL7cKhoipgpGWx5WSss="
    # 拉取索引/下載外掛的顯式代理；留空沿用 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY
    proxy: "socks5://127.0.0.1:1080"
    timeout_seconds: 30
    max_download_mb: 256
    check_interval_hours: 24

deploy_plugins:
  enabled: false
  dir: ""                 # 留空表示 <data_dir>/plugins/deploy
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64

notify_plugins:
  enabled: true
  dir: ""                 # 留空表示 <data_dir>/plugins/notify
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64

upgrade:
  auto: true
  auto_optimize: true
  vacuum: true
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    # 官方釋出建置會自動內建公鑰；自編譯版本留空時預設拒絕線上升級，僅受控環境可顯式允許未簽名包
    allow_unsigned: false
    token: ""
```

## 3. `server`

- `addr`：完整監聽地址，例如 `:8080`、`127.0.0.1:8080`。
- `host`、`port`：未設定 `addr` 時組合為監聽地址。該埠由 Go 二進位制直接提供頁面與介面，前端資源已嵌入二進位制，不會反向代理 Vite 的 `5173`；`5173` 僅在 `make dev` 時存在。
- `trusted_proxies`：可信反向代理的 IP/CIDR 列表。留空表示不信任任何代理頭，適合直連部署；反向代理終止 TLS 時必須填寫，否則 `Secure` Cookie 與客戶端 IP 識別會不正確。

生產建議：

- `host` 設為 `127.0.0.1` 或內網地址，由反向代理訪問。
- `trusted_proxies` 只包含實際代理地址，不要配置 `0.0.0.0/0`。
- 如果代理和 LACTSSL 不在同一主機，使用內網 CIDR。

## 4. `data_dir` 與資料庫後端

`data_dir` 用於外掛、日誌和升級備份。資料庫預設是 SQLite，檔案位於 `<data_dir>/lactssl.db`；也可以顯式使用 PostgreSQL 18：

```yaml
database:
  driver: "postgres"
  dsn: "postgres://user:password@127.0.0.1:5432/lactssl?sslmode=verify-full"
```

- `driver=postgres` 時 `path` 被忽略，`dsn` 必填；也可以用環境變數 `LACTSSL_DATABASE_DSN` 覆蓋，避免密碼寫入配置檔案。
- 預設 `allow_multi_instance: false`，應用會持有 PostgreSQL advisory lock；第二個例項連線同一個資料庫會啟動失敗。
- SQLite 仍固定單連線，適合單機小規模部署；需要外部備份、集中運維時再選 PostgreSQL。
- 切換後端不需要手工改庫：登入後到 `/upgrade` 的“切換資料庫”卡片執行，詳見 [upgrade.md](upgrade.md#7-資料庫線上切換sqlite--postgresql)。

資料目錄用於存放資料庫、外掛、日誌和升級備份。目錄會以 `0700` 權限建立。`database.path` 留空時使用 `<data_dir>/lactssl.db`。

- 資料目錄不要放在共享可寫路徑或公開目錄。
- 不要把資料庫放在 NFS/SMB 等多寫共享儲存上；SQLite 單寫，鎖語義不穩定。
- 備份範圍是 `config.yaml` + 整個 `data/`。

## 5. `log`

底層程序日誌與操作日誌用途不同：操作日誌記錄“誰在何時做了什麼”，顯示在「日誌 → 操作日誌」頁籤；程序日誌用於執行期排障，可非同步寫入資料庫並顯示在「日誌 → 系統日誌」頁籤，同時輸出到 stdout/檔案。

- `level`：最低輸出級別，支援 `debug`、`info`、`warn`、`error`。`debug` 會附帶原始碼位置，適合臨時排障。
- `format`：`text` 適合直接閱讀與 Docker 日誌；`json` 適合日誌採集系統。
- `file`：日誌檔案路徑。留空時只輸出到 stdout；相對路徑基於服務程序的當前工作目錄。目錄不存在時會自動建立（`0700`），檔案權限為 `0600`。
- `also_stdout`：寫入檔案時是否仍輸出到 stdout。Docker 部署建議保持 `true`。
- `max_size_mb`：單檔案大小上限，達到後按時間戳輪轉，支援 `1–1024`。
- `max_backups`：保留的歷史檔案數量，`0` 表示不限制；超出後刪除最舊檔案。
- `max_age_days`：歷史檔案保留天數，`0` 表示不限制。
- `compress`：是否將輪轉後的歷史檔案壓縮為 `.gz`。
- `access_log`：是否記錄 HTTP 訪問日誌。日誌只包含方法、路由模板、狀態碼、耗時、客戶端 IP 等欄位，不記錄查詢引數、請求體和認證頭；門禁入口在路徑中統一替換為 `:entry`。每個回應都會帶 `X-Request-ID`。
- `system.enabled`：是否寫入資料庫供「日誌 → 系統日誌」頁籤檢視。
- `system.level`：寫入資料庫的最低階別。不能低於 `log.level`，否則低階別日誌不會生成。
- `system.access`：是否把 HTTP 訪問日誌也寫入資料庫。預設 `false`，避免每個請求都寫 SQLite。
- `system.retention_days`：保留天數，範圍 `1–365`。
- `system.max_rows`：最大行數，範圍 `100–1000000`；超出後刪除最舊記錄。
- `archive.enabled`：是否在刪除/清空資料庫日誌前先寫入歸檔。預設開啟；歸檔失敗會阻止刪除，不會靜默丟棄日誌。
- `archive.dir`：歸檔根目錄，留空表示 `<data_dir>/archives/logs`。目錄以 `0700` 建立，檔案權限為 `0600`。
- `archive.compress`：是否 gzip 壓縮；關閉後儲存為 `.ndjson`。
- `archive.retention_days`：歸檔檔案保留天數，範圍 `0–3650`；預設 `0` 表示永久保留。
- `archive.max_total_mb`：歸檔目錄容量上限（MB），`0` 表示不限制。超限策略由 `quota_policy` 決定。
- `archive.quota_policy`：`fail`（預設，拒絕繼續歸檔並阻止刪除）或 `prune_oldest`（刪除最舊歸檔）。`prune_oldest` 會銷毀審計材料，必須顯式配置。

歸檔檔案由 `*.ndjson.gz`（或 `.ndjson`）與 `*.manifest.json` 組成，清單包含行數、ID 範圍、時間範圍、觸發原因、操作者、SHA-256 和檔案大小。歸檔目錄不進入預設 ZIP 備份，需單獨備份。

寫入採用有界非同步佇列，資料庫繁忙時不會阻塞業務請求；佇列滿時會丟棄新日誌。系統日誌可能包含客戶端 IP、網域和錯誤詳情，請按運維資料管理。

生產建議：日誌檔案放獨立磁碟或掛載卷；`format: json` 接入日誌平臺；`access_log: true` 便於審計；`system.access` 保持 `false`，除非需要短期排查。

## 6. `security`

- `entry`：管理頁面的門禁入口路徑段，手工設定要求 8–32 位字母、數字、下劃線或中劃線。留空時首次啟動隨機生成 20 位大小寫字母與數字組合，並儲存到資料庫。
- `public_url`：對外訪問地址，例如 `https://ssl.example.com`（不要帶路徑）。密碼重置郵件中的連結基於該地址生成，避免 Host 頭注入；未配置時僅允許本機迴環請求按 Host 推導。
- `session_persistence`：會話保持，預設 `true`。為 `true` 時服務啟動不再清空 `sessions` 表，JWT 也改用固定的 `jwt_secret` 簽名，因此後臺程序重啟後瀏覽器仍可繼續使用原有登入會話；為 `false` 時每次啟動仍會強制所有舊登入失效。
- `jwt_secret`：登入令牌簽名金鑰。留空時自動生成 32 位元組隨機值並寫回配置檔案；手動配置時至少 32 個字元，否則拒絕啟動。
- `salt`：資料庫中敏感欄位（DNS/部署憑據、通知金鑰、私有 CA 與證書私鑰等）的加密鹽。留空時自動生成並寫回；手動配置時至少 32 個字元，否則拒絕啟動。
- `rate_limit.max_keys`：記憶體令牌桶最多保留的 key 數，預設 `10000`。每個客戶端 IP、帳戶或操作會佔用一個 key；超出後按最近未使用順序淘汰。
- `rate_limit.rules`：按規則名覆蓋限流閾值，每項包含 `max`（視窗內允許次數）與 `window_seconds`（視窗秒數）。未配置的規則使用內建預設值；省略 `window_seconds` 時沿用預設視窗；`max: 0` 表示關閉該規則。未知規則名會導致啟動失敗，便於發現拼寫錯誤。

常用規則及預設值：

| 規則 | 預設值 | 說明 |
| --- | --- | --- |
| `login_ip` / `login_account` / `login_totp` | 10/5 分鐘、20/15 分鐘、5/10 分鐘 | 登入 IP、帳戶失敗與 TOTP 校驗 |
| `forgot_ip` / `forgot_account` | 5/10 分鐘、3/小時 | 忘記密碼 |
| `passkey_login_begin` / `passkey_login_fail` | 20/5 分鐘、10/5 分鐘 | Passkey 登入 |
| `reauth` | 10/10 分鐘 | 敏感操作再認證失敗 |
| `api_ip` / `api_anonymous` / `entry` | 600/分鐘、120/分鐘、60/分鐘 | `/api` 總量、匿名請求與門禁探測 |
| `csr_create` | 120/小時 | ACME 客戶端 CSR 提交 |
| `acme_request` / `acme_account` / `acme_pki` | 1200/分鐘、30/小時、600/分鐘 | ACME 協議入口、建帳戶、CRL/OCSP |
| `acme_order` | 300/小時 | 未在 ACME 端點單獨配置時的訂單上限 |

> 限流閾值修改後需重啟服務生效。ACME 端點自身的「每小時訂單數」在管理介面配置，優先順序高於 `acme_order`。

> **務必備份 `config.yaml`。** 丟失 `salt` 後，資料庫中已加密的憑據無法解密；丟失或更換 `jwt_secret` 會使現有登入會話失效。預設開啟會話保持，重啟不會清空服務端會話；設定 `security.session_persistence: false` 可恢復「每次啟動清空會話並輪換 JWT 簽名子金鑰」的嚴格模式。

安全建議：

- `entry` 保持隨機長字串，不要使用 `admin`、`ssl`、`panel` 等可猜測值。
- `public_url` 必須是固定 HTTPS 地址，不能依賴請求 Host。
- `session_persistence` 根據企業會話策略選擇；預設開啟，程序重啟不強制重新登入，需接受更長的會話生命週期。
- `jwt_secret` 和 `salt` 用密碼管理器或配置管理系統生成/保管，不要提交到版本庫。
- 更換 `salt` 會導致已有加密憑據不可用，必須配套遷移方案，禁止直接改值。

## 7. `acme`

- `enabled`：是否啟用內建 ACME 服務端，預設開啟。未建立任何端點時不會對外簽發證書；如需完全關閉協議入口，可設為 `false`。
- `addr`：獨立監聽地址，例如 `:14000`。留空時 ACME 協議掛載到主埠的 `/acme` 字首。
- `base_url`：客戶端實際訪問的 ACME 基礎地址，用於生成 directory、帳戶資源 URL 以及證書內的 CRL/OCSP 地址。公網部署必須配置為固定地址，不要依賴請求 Host；未配置時會回退到 `security.public_url`，兩者都為空時按請求 Host 推導並輸出啟動警告。
- `tls.cert` / `tls.key`：ACME 獨立監聽直接提供 TLS 時使用；反向代理終止 TLS 時留空。
- `mtls.enabled` / `mtls.client_ca`：要求客戶端證書時啟用，並指定客戶端 CA 證書。啟用 mTLS 時必須同時配置 ACME TLS 證書和私鑰。

安全建議：

- 公網部署使用獨立埠，並限制來源。
- 每個端點必須配置網域白名單；生產開啟 EAB。
- `base_url` 必須是固定公網地址，避免 Host 頭注入。
- 不需要 ACME 服務時設定 `enabled: false`，減少攻擊面。

## 8. `plugins`、`deploy_plugins`、`notify_plugins`

三類外掛分別使用 `<data_dir>/plugins/dns`、`<data_dir>/plugins/deploy` 和 `<data_dir>/plugins/notify`，子目錄互不混放。

### 8.1 DNS 外掛

```yaml
plugins:
  enabled: true
  dir: ""                 # 留空表示 <data_dir>/plugins/dns
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64
  repo:
    enabled: true
    url: "https://github.com/AXmishell/LACTSSL-Plugins/releases/download/plugin-index/index.json"
    channel: stable
    auto_update: false
    public_key: "t7Q4vd0B1zh3mRf0t6Q6qlBViL7cKhoipgpGWx5WSss="
    # 拉取索引/下載外掛的顯式代理；留空沿用 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY
    proxy: "socks5://127.0.0.1:1080"
    timeout_seconds: 30
    max_download_mb: 256
    check_interval_hours: 24
```

### 8.2 部署外掛

部署外掛會接收證書私鑰，預設關閉：

```yaml
deploy_plugins:
  enabled: false
  dir: ""
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64
```

### 8.3 通知外掛

主程式只內建郵件通知，通知外掛預設開啟：

```yaml
notify_plugins:
  enabled: true
  dir: ""
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64
```

外掛目錄權限 `0700`，helper `0755`，屬主為服務執行使用者。生產建議配置 `public_key` 並關閉 `auto_update`。詳細治理見[外掛治理](plugins.md)。

## 9. `upgrade`

`upgrade` 控制啟動時自動遷移與 `/upgrade` 頁面的最佳化行為：

```yaml
upgrade:
  auto: true              # 啟動時自動執行歷史遺留遷移
  auto_optimize: true     # 啟動時補充索引、更新統計、摺疊 WAL；不執行 VACUUM
  vacuum: true            # 允許在 /upgrade 手動最佳化時執行 VACUUM
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable       # stable 或 beta
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    token: ""
```

- 程式升級會從 GitHub Release 下載對應平臺釋出包，校驗 `checksums.txt` 中的 SHA-256 後原子替換當前二進位制，並把舊版本備份到 `<data_dir>/upgrade-backups/program/`。
- 只讀檔案系統、無釋出包的平臺或 `dev` 構建會顯示為不可升級。
- 容器內升級不會改變映象，重建容器後仍會回到映象版本；生產建議用映象標籤升級。
- `public_key` 配置後會強制校驗 `checksums.txt.sig`；`token` 用於提高 GitHub API 速率上限。

## 10. 命令列引數

| 引數 | 說明 |
| --- | --- |
| `-config` | 配置檔案路徑，預設當前工作目錄下的 `config.yaml` |

```bash
./bin/lactssl -config /etc/lactssl/config.yaml
```

門禁入口也可以在服務啟動後透過「設定 → 門禁入口」修改，無需重啟。

## 11. 反向代理要點

### 11.1 信任邊界

- 只有 `server.trusted_proxies` 中列出的 TCP 對端地址才被視為可信代理；留空表示不信任任何代理頭，適合直連部署。
- `X-Forwarded-For`、`X-Real-IP`、`Forwarded`、`CF-Connecting-IP` 在代理被信任前都只是客戶端可偽造的聲明。設定頁的「自動檢測」只提供線索，不會自動把地址加入可信列表。
- 不要配置 `0.0.0.0/0`、`::/0` 或過大的內網網段；可信列表中的任何主機都能代表客戶端偽造來源 IP。

### 11.2 覆蓋模式（推薦）

讓最靠近 LACTSSL 的代理用真實客戶端地址覆蓋轉發頭：

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-Proto $scheme;
```

此時 `trusted_proxies` 只需要填寫這一層代理地址，解析一次即可得到客戶端 IP。多級場景可由邊緣代理先還原真實客戶端，再由最近一跳覆蓋；不要讓中間代理把上游代理地址覆蓋成客戶端 IP。

### 11.3 追加模式

Nginx 的 `$proxy_add_x_forwarded_for` 會保留客戶端自帶的 XFF，並追加當前連線地址。LACTSSL 從右向左跳過可信項：

- 單層代理：只信任 Nginx 即可。
- 多級代理：必須把鏈上所有會出現在 XFF 中的代理加入 `trusted_proxies`。只信任最近一跳時，結果會停在上游代理 IP。
- 絕不能把最左側的客戶端地址加入可信列表。

例如 `client → CDN → Nginx → LACTSSL`，追加後的 XFF 是 `client, CDN 邊緣 IP`；只信任 Nginx 會得到 CDN 邊緣 IP，必須同時信任 CDN 的官方網段，或改由 Nginx 覆蓋 XFF。

### 11.4 CDN 與雲端負載均衡

- `CF-Connecting-IP` 只有在 TCP 對端屬於 Cloudflare 官方網段時才可信；僅看到該頭就採信會被直連客戶端偽造。
- 推薦在自建 Nginx 上按官方網段啟用 `real_ip`，再用 `$remote_addr` 覆蓋 XFF，LACTSSL 只信任這台 Nginx。
- CDN 邊緣 IP 會變化，不要逐個加入可信列表；使用官方網段，或在 CDN 後增加一層你控制的反向代理。
- L4/TCP 負載均衡沒有 HTTP 頭可解析，只能看到 LB 地址；需要 PROXY protocol 或在其後增加一層能寫 HTTP 頭的代理。

### 11.5 Docker 與 Kubernetes

- 容器內看到的 peer 常是網橋閘道或 Service 轉發地址，不一定是真實客戶端。
- 優先填寫代理容器/節點的精確 IP；IP 動態時使用專用網路並只放行該網路中的代理地址或最小 CIDR。
- 不要因為「是私網」就直接信任 `172.16.0.0/12`；同網段其他容器可以偽造轉發頭。
- 確認 Ingress Controller 是否會清洗 XFF；不要假設所有 Ingress 的行為一致。

### 11.6 套用與驗證

1. 將代理地址加入 `trusted_proxies`，配置 `security.public_url`。
2. 代理終止 TLS 時傳遞 `X-Forwarded-Proto: https`。
3. 備份恢復和外掛上傳需要較大的請求體上限（例如 Nginx `client_max_body_size 256m`），代理讀超時至少覆蓋備份恢復、外掛下載和部署操作（例如 600 秒）。
4. 儲存配置後重啟服務，在「設定 → 配置檔案 → 自動檢測」確認 `peer_ip`、`applied_trusted_proxies`、`effective_client_ip`、`client_ip_source` 符合預期；儲存未重啟時 `configured_trusted_proxies` 與 `applied_trusted_proxies` 可能不同。
5. 直連偽造測試：從不受信地址發送 `X-Forwarded-For: 203.0.113.9`，服務端仍應使用 TCP 對端地址。存取日誌中的 `ip_source`、`ip_skipped`、`ip_warnings` 可用於排障。

### 11.7 進階代理身份

解析優先級為：可信代理身份驗證 → Cloudflare 專用頭（可選）→ `X-Forwarded-For` → `X-Real-IP` → RFC 7239 `Forwarded` → TCP 對端。任何模式都不會讓非可信來源的轉發頭生效。

- **RFC 7239 `Forwarded`**：在前面的頭不可用時自動解析，支援 `for=203.0.113.9`、`for=203.0.113.9:4711`、`for="[2001:db8::1]:4711"`；`unknown`、`_hidden` 等混淆值視為不可用。
- **Cloudflare 模式**：
  ```yaml
  server:
    client_ip_mode: cloudflare
    trusted_proxies:
      - "173.245.48.0/20"
      - "103.21.244.0/22"
      # ... Cloudflare 官方網段
  ```
  只有 peer 屬於 `trusted_proxies` 時才採信 `CF-Connecting-IP`。不要把該模式與只做本機覆蓋的 Nginx 混用；源站應只允許 Cloudflare 回源。
- **PROXY protocol**：
  ```yaml
  server:
    proxy_protocol: true
  ```
  接受 v1/v2 頭，且只信任 `trusted_proxies` 中的對端發送的頭；非可信來源發送 PROXY 頭會直接斷開。沒有 PROXY 頭的普通 HTTP 仍可正常訪問。L4 負載均衡需要開啟並發送 PROXY protocol。
- **mTLS 代理身份**：
  ```yaml
  server:
    tls:
      cert: "/etc/lactssl/server.crt"
      key: "/etc/lactssl/server.key"
      proxy_client_ca: "/etc/lactssl/proxy-ca.pem"
  ```
  主監聽改為 HTTPS；持有 `proxy_client_ca` 簽發客戶端證書的連線會被視為可信代理，不再依賴 IP。未攜帶客戶端證書的瀏覽器仍可正常訪問。該模式只適用於代理直接以 TLS 連線 LACTSSL 的部署；如果 TLS 在代理終止，應繼續使用 IP/CIDR 信任或 PROXY protocol。請使用專用於代理的 CA，不要復用瀏覽器客戶端 CA。

完整示例見[安裝與啟動](install.md)。

## 12. 配置校驗與故障處理

常見啟動失敗原因：

| 錯誤 | 原因 | 處理 |
| --- | --- | --- |
| `jwt_secret` 太短 | 手動配置少於 32 字元 | 改為至少 32 字元或留空自動生成 |
| `salt` 太短 | 手動配置少於 32 字元 | 改為至少 32 字元或留空自動生成 |
| 監聽失敗 | 埠被佔用或無權限 | 檢查埠和執行使用者 |
| mTLS 配置不完整 | 啟用 mTLS 但缺少 TLS 證書/私鑰 | 同時配置 `acme.tls.cert/key` |
| 配置校驗失敗 | 恢復包或手工編輯非法 | 按日誌提示修正；恢復包會先校驗再應用 |
| 修改後不生效 | 配置需要重啟 | 重啟服務；門禁入口/外掛頁設定例外 |

配置恢復和遷移由升級工具處理，詳見[升級與變更](upgrade.md)。

## 相關文件

- [安裝與啟動](install.md)
- [部署架構](architecture.md)
- [安全基線](security.md)
- [運維手冊](operations.md)
