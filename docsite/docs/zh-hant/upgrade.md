# 升級與變更

版本升級是證書平臺風險最高的日常變更之一：既要保證資料庫遷移可回滾，也要避免在證書集中到期時引入不可用。本文給出升級前評估、遷移機制、程式升級、回滾和變更視窗清單。

## 變更原則

- **先備份，再升級**：`config.yaml` + 整個 `data/`，或使用介面加密備份；先預發驗證簽發、部署、通知和 API 四條鏈路。
- **固定版本、保留回滾點**：生產不使用不可追溯的 `latest`；保留資料庫遷移快照與舊二進位制/舊映象標籤。
- **避開高峰、可觀測**：不要與證書集中到期、批次部署重疊；升級後至少觀察一個排程週期和一次任務執行。

## 升級會做什麼

`internal/upgrade` 會在新版本首次啟動時自動執行歷史遺留遷移，也可以登入後訪問 `/upgrade` 進行預檢、手動遷移和資料庫最佳化。

### 自動遷移順序

1. 讀取並校驗 `config.yaml`。
2. 開啟配置的資料庫（預設 SQLite，也可為 PostgreSQL）並執行基礎建表。
3. 建立升級工具並讀取 `upgrade.schema_version`。
4. 如果版本落後，先備份資料庫與配置，再按順序執行遷移步驟。
5. 寫入新版本號，執行低成本最佳化，再繼續初始化帳戶、排程器與 HTTP 服務。

- 自動遷移由 `upgrade.auto` 控制，預設開啟；遷移步驟全部冪等，失敗時不會推進版本號，下次啟動會重試失敗步驟。遷移前備份位於 `data/upgrade-backups/<UTC 時間>/`，預設保留最近 5 份。

### 遷移內容

| 問題 | 舊版本表現 | 升級工具處理 |
| --- | --- | --- |
| 資料庫沒有格式版本 | 只能靠 `ensureColumn` 和零散遷移鍵推斷結構 | 寫入 `upgrade.schema_version`，按版本順序執行冪等步驟 |
| 敏感欄位明文 / 舊金鑰檔案 | 舊版 TOTP 金鑰、證書私鑰直接落庫；`data/jwt.secret` 與 `config.yaml` 雙份金鑰 | 使用 AES-256-GCM 加密（已是密文自動跳過），確認配置已儲存金鑰後把舊檔移入升級備份目錄並清理 |
| 全域 EAB 憑據 | `acme.eab_kid` / `acme.eab_hmac` 只適用於預設 CA | 遷移到預設 CA 的獨立設定，清理舊鍵 |
| 部署引數混在憑據中 | 路徑、站點、過載命令儲存在目標憑據 | 拆分到任務流程的部署節點，憑據只保留認證資訊；舊 ID 合併為規範 ID |
| 舊通知設定 | Gotify / Server醬 使用獨立設定鍵 | 轉換為通用通知渠道配置，再清理舊鍵 |
| 外掛遷移殘留 | 舊全量 helper、停用列表、缺失的啟用白名單 | 初始化 DNS/部署/通知白名單，保留內建條目的預設啟用狀態 |
| 資料庫膨脹 / WAL 過大 | 留下空閒頁，`-wal` 持續增長 | `VACUUM` 回收空間、`ANALYZE` 更新統計，`wal_checkpoint(TRUNCATE)` 摺疊並截斷 |
| 索引與日誌表 | 熱路徑全表掃描；系統日誌在 `system_logs`、操作日誌在 `logs` | 自動補充索引；複製系統日誌到 `logs(category=runtime)`，舊表保留用於回滾 |
| 配置結構陳舊 / 舊驗證模式 | 缺少 `plugins.repo`、`notify_plugins`、`log.system`、`upgrade` 等新段；存在 `password_passkey` 等歷史值 | 按當前版本補齊欄位並規範化，驗證模式歸一為 `passkey`，無效值回退 |

## 升級前檢查

- [ ] 閱讀目標版本變更說明和遷移說明，確認當前版本、資料格式版本和待遷移步驟；備份 `config.yaml` 與 `data/` 並驗證可讀，確認磁碟空間足夠（`VACUUM` 需要臨時空間）。
- [ ] 在預發環境驗證簽發、部署、通知、API，確認舊版本二進位制/映象標籤可用。
- [ ] 通知相關方並選擇低峰視窗，暫停批次任務，避免升級期間併發寫入。

## 升級方式

### Docker（推薦）

```bash
# 固定目標版本：先把 docker-compose.yml 中的 image 標籤改為 1.2.3
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```

- 使用明確版本標籤，便於回滾；容器重建後會回到映象版本，不要在容器內自升級。官方 Compose 預設只讀根檔案系統，頁面會提示不支援自升級，這是預期行為。

### 二進位制

```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo chmod 0755 /opt/lactssl/lactssl
sudo systemctl start lactssl
sudo journalctl -u lactssl -f
```

- 升級會保留原檔案權限位，但不保留 `setcap` 等擴充套件屬性；使用特權埠或檔案 capability 的部署需在升級後重新設定。
- 直接 `exec` 新二進位制的自升級在 Linux/macOS/FreeBSD 上可用；Windows 需要服務管理器重新拉起。`dev`/原始碼構建，以及沒有 `checksums.txt` 或資產 SHA-256 的釋出包會被拒絕自升級，必須手動替換並自行校驗。

### 程式自升級（可選）

`/upgrade` 頁面上方提供「程式升級」：

- **檢查更新**：按 `upgrade.program.channel` 查詢 Release；`stable` 只接受正式版，`beta` 包含預釋出。
- **安裝更新**：下載 `lactssl_<version>_<os>_<arch>.tar.xz`（缺失時回退到 `.tar.gz`；Windows 為 `.zip`），使用 `checksums.txt` 強制校驗 SHA-256，解出 `lactssl` 後替換當前可執行檔案。
- **釋出簽名**：官方建置會內建公鑰，預設強制校驗 `checksums.txt.sig`，缺失或錯誤直接拒絕。自編譯版本未配置 `upgrade.program.public_key` 時預設拒絕線上升級，必須在受控環境顯式設定 `upgrade.program.allow_unsigned: true`。
- **備份與重啟**：替換前把舊二進位制複製到 `<data_dir>/upgrade-backups/program/`，預設保留最近 3 份；安裝完成後需重啟生效。會話保持預設開啟，設為 `security.session_persistence: false` 時重啟會清空會話。

限制：

- 只讀檔案系統、無釋出包的平臺、`dev` 構建顯示為不可升級；容器內升級不改變映象，重建後回到映象版本。GitHub API 未配置 token 時受匿名速率限制，可透過 `upgrade.program.token` 配置；需要出站代理時使用系統代理環境變數。

## 升級後驗證

1. 檢視啟動日誌中的“資料格式升級完成”，登入 `/upgrade` 確認步驟全部為“完成”或“跳過”。
2. 檢查版本、構建時間和架構，驗證管理員登入、TOTP/通行金鑰，以及證書列表、任務、部署憑據、通知渠道和外掛。
3. 手動執行一條測試任務，確認簽發、部署和通知；檢查 API 金鑰權限和呼叫，並觀察至少一個排程週期（30 秒）和一次自動續期。

## 回滾

### 資料回滾

升級工具在結構變更前生成快照：

```text
data/upgrade-backups/20260101-120000/
├── lactssl.db
└── config.yaml
```

步驟：

1. 停止服務。
2. 用備份中的 `lactssl.db` 覆蓋當前資料庫，並刪除 `lactssl.db-wal`、`lactssl.db-shm`。
3. 如需一併回滾配置，用備份中的 `config.yaml` 覆蓋當前配置。
4. 啟動舊版本程式。

- 舊版本程式可能無法讀取新版本寫入的資料格式；跨大版本回滾時應同時回滾二進位制與備份資料，且不要手動刪除 `upgrade.schema_version`。
- 自動遷移失敗時服務會拒絕啟動並列印失敗步驟；修復權限、磁碟或資料問題後直接重啟即可重試（步驟冪等，失敗版本號不會推進）。

### 程式回滾

程式升級備份位於：

```text
data/upgrade-backups/program/
├── 20260101-120000-lactssl-1.2.2
└── 20260102-090000-lactssl-1.2.3
```

步驟：

1. 停止服務。
2. 用備份檔案覆蓋當前二進位制，並恢復執行權限（`chmod 0755`）。
3. 如需回滾資料，按上一節操作；啟動服務後確認版本號與資料格式遷移狀態。

## 資料庫線上切換（SQLite ↔ PostgreSQL）

`/upgrade` 頁面提供資料庫後端切換。切換在服務程序內完成，不需要手工執行 SQL 或替換資料庫檔案。

### 前提

- 目標 PostgreSQL 建議 18，應用會檢查服務端版本；目標預設必須是空庫，非空時必須在頁面勾選“清空當前 schema”，該操作只刪除當前 schema 中的表。預設不允許第二個例項共享同一個 PostgreSQL 資料庫（`database.allow_multi_instance: false`），切換前也會檢查。
- 切換前會強制備份源庫：SQLite 使用 `VACUUM INTO`，PostgreSQL/MySQL 使用 Go 原生邏輯備份，位於 `data/db-convert/backups/<時間>/`。
- 服務需要由 systemd、Docker restart policy 或類似程序管理器託管，否則切換後需要手動啟動。

### 執行流程

1. 在 `/upgrade` 選擇目標後端。PostgreSQL 可填寫主機、埠、使用者名稱、密碼、資料庫名與 SSL 模式，也可切換到“原生 DSN”直接貼上完整 DSN；SQLite 填寫檔案路徑（必須位於 `data_dir` 內）。填寫後點選“測試連線”和“預檢”，確認目標版本、空庫狀態、表與行數。
2. 輸入帳戶密碼/TOTP，並完成頁面上的算術確認後開始切換（介面使用 `CONVERT` 確認詞）。
3. 切換期間進入維護模式，普通 API 暫停；應用先備份，再凍結寫入、逐表複製、校驗行數與逐表雜湊、抽樣解密敏感欄位。
4. 校驗透過後原子改寫 `config.yaml`，寫入 `data/db-convert/state.json`，自動重啟。
5. 新程序開啟目標庫並執行啟動自檢；失敗會自動恢復舊配置並回到源庫。自檢透過後會自動刪除舊 SQLite 資料庫檔案（含 `-wal`/`-shm`/`-journal`），切換前備份繼續保留。

凍結時間取決於資料量；典型小庫為秒級，日誌/證書很多時為分鐘級。複製和校驗期間頁面仍可輪詢進度。

### 一鍵回滾

切換完成後，`/upgrade` 會顯示“一鍵回滾”（同構遷移除外）。回滾不是簡單切回舊配置，而是把**當前庫的最新資料**反向切換回切換前的後端，再切換配置並重啟，避免丟掉切換後的寫入。

回滾同樣會強制備份當前庫、凍結寫入並校驗；需要完成頁面上的算術確認並輸入帳戶密碼/TOTP（介面使用 `ROLLBACK` 確認詞）。切換前配置備份保留在 `config.yaml.before-db-convert-<時間>`；切換自檢透過後舊 SQLite 檔案會被自動刪除，但 `data/db-convert/backups/<時間>/` 中的切換前備份仍保留。

### CLI

服務停止時也可以使用 CLI 執行同一套核心邏輯：

```bash
./lactssl-dbconvert   -config config.yaml   -source-driver sqlite -source data/lactssl.db   -target-driver postgres -target "postgres://user:pass@host:5432/lactssl?sslmode=verify-full"   -switch -yes
```

`-switch` 會改寫 `config.yaml`，但不會自動啟動服務。目標庫非空時預設拒絕；確認清空時加 `-force`。

### 限制

- 不提供雙寫/CDC 的零停機切換；寫入會在複製與切換階段暫停，切換前必須確保沒有正在執行的批次部署/簽發任務。
- 多例項共享 PostgreSQL 時不能安全切換，v1 直接拒絕；環境變數 `LACTSSL_DATABASE_DSN` 存在時，改寫配置檔案不會生效，切換前必須先移除該變數並重啟。

### 同構遷移（SQLite → SQLite / PostgreSQL → PostgreSQL）

預設不允許同構遷移。確實需要把資料庫搬到另一個檔案或 PostgreSQL 例項時，必須在頁面勾選“同構遷移”確認；API 請求體傳 `allow_same_driver: true`，CLI 使用 `-allow-same-driver`。額外限制：

- 目標必須是空庫/空檔案；同構遷移不允許勾選“清空 schema”，避免誤刪源庫。目標不能是當前庫本身：SQLite 會比較真實檔案（含軟連結、硬連結）；PostgreSQL 會比較例項啟動時間和資料庫名，同一例項的同一資料庫（包括不同 schema）都會被拒絕。
- PostgreSQL → PostgreSQL 切換成功後不會刪除源資料庫，需要管理員確認後手工清理；SQLite → SQLite 切換自檢透過後會刪除舊檔案，切換前備份仍保留在 `data/db-convert/backups/`。同構遷移不支援“一鍵回滾”，請用切換前備份手動恢復，且不能與 `-force` 同時使用。

## 手動遷移與資料庫最佳化

登入後訪問 `/upgrade`（側邊欄「系統 → 版本升級」）：

- **預檢**：顯示程式版本、資料格式版本、遷移步驟狀態、資料庫完整性、空閒頁、配置變更與風險提示，不修改任何資料。
- **執行遷移並最佳化**：再次執行遷移步驟，隨後最佳化配置檔案和資料庫。**僅最佳化資料庫與配置** 不重複執行遷移，適合日常維護。
- **回收磁碟空間**：開啟後執行 `VACUUM`，會短暫獨佔資料庫，建議低峰期使用。

執行結果會寫入系統日誌和 `upgrade.last_run` 設定。

### 資料庫最佳化內容

- `PRAGMA quick_check(1)`：快速完整性檢查。
- 基礎表結構使用 `PRAGMA user_version` 記錄版本，結構已是最新時跳過建表與 `ensureColumn` 掃描。
- 補充熱路徑索引：證書狀態與到期時間、任務啟用狀態與下次執行、監控檢查、日誌級別與時間、ACME 訂單/挑戰、部署記錄；`PRAGMA optimize` 和 `ANALYZE` 更新統計，`PRAGMA wal_checkpoint(TRUNCATE)` 摺疊並截斷 WAL，`VACUUM` 回收空閒頁。

`VACUUM` 不會刪除應用資料，但會重寫資料庫檔案。執行前工具會先做資料庫快照，磁碟空間不足時可能失敗，失敗會作為警告返回，不影響服務繼續執行。

### 配置檔案最佳化內容

- 補齊當前版本的全部標準配置段與預設值，規範化 `server.addr`、`acme.addr` 等空白。
- 去除 `security.public_url`、`acme.base_url` 末尾斜槓；`log.level`、`log.format`、`log.system.level`、`plugins.repo.channel` 轉小寫；`server.trusted_proxies` 去重、去空白。
- 檢查 ACME 未配置固定地址、外掛倉庫未配置簽名公鑰、部署外掛接觸私鑰、金鑰過短等風險；寫入前按啟動載入邏輯完整校驗，失敗不覆蓋原檔案，並使用 YAML 節點編輯保留註釋和欄位順序。

## 配置項

```yaml
upgrade:
  auto: true              # 啟動時自動執行歷史遺留遷移
  auto_optimize: true     # 啟動時執行低成本最佳化；不執行 VACUUM
  vacuum: true            # 允許在 /upgrade 手動最佳化時執行 VACUUM
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    # 官方建置會自動內建公鑰；自編譯版本未配置時預設拒絕線上升級，僅受控環境可顯式允許未簽名包
    allow_unsigned: false
    token: ""
```

關閉 `upgrade.auto` 後，啟動日誌會提示存在待遷移資料，但服務仍會繼續啟動。此時應儘快登入 `/upgrade` 手動執行，避免新程式碼讀取舊格式資料；歷史系統日誌在日誌合併步驟完成前不會顯示在「日誌 → 系統日誌」頁籤。

## API

升級工具使用現有登入態，介面固定位於 `/api`：

| 介面 | 方法 | 權限 | 說明 |
| --- | --- | --- | --- |
| `/upgrade` | GET | `upgrade:read` | 預檢狀態 |
| `/upgrade/run` | POST | `upgrade:write` | 遷移、配置最佳化與資料庫最佳化 |
| `/upgrade/optimize` | POST | `upgrade:write` | 僅最佳化配置與資料庫 |
| `/upgrade/database` | GET | `upgrade:read` | 當前後端與切換進度 |
| `/upgrade/database/test` | POST | `upgrade:read` | 測試目標連線與空庫狀態 |
| `/upgrade/database/preflight` | POST | `upgrade:read` | 切換預檢 |
| `/upgrade/database/convert` | POST / GET | `upgrade:write` / `upgrade:read` | 啟動切換（需二次認證）/ 查詢切換進度 |
| `/upgrade/database/convert/cancel` | POST | `upgrade:write` | 取消未完成的切換 |
| `/upgrade/database/rollback` | POST | `upgrade:write` | 一鍵反向切換回舊後端 |
| `/upgrade/program` | GET | `upgrade:read` | 程式升級狀態 |
| `/upgrade/program/check` | POST | `upgrade:write` | 立即查詢 GitHub Release |
| `/upgrade/program/apply` | POST | `upgrade:write` | 下載並安裝指定版本 |
| `/upgrade/program/restart` | POST | `upgrade:write` | 安裝完成後重啟服務 |

請求體可包含 `vacuum`、`analyze`、`config`、`optimize` 等布林欄位。寫介面只需登入會話或具備相應權限的 API 金鑰。

## 變更視窗清單

- [ ] 已通知相關方，避開證書集中到期；已備份並驗證恢復密碼。
- [ ] 已確認磁碟空間（備份 + VACUUM 臨時空間），準備舊版本二進位制/映象標籤；已暫停批次任務和自動化觸發，完成升級和驗證，恢復後記錄變更單、版本、時間和結果。
