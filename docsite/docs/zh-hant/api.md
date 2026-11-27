# API 整合
LACTSSL 的控制台介面可同時透過瀏覽器登入會話和 API 金鑰呼叫。API 金鑰適合 CI/CD、監控巡檢、CMDB 同步、證書分發等長期執行的程式，每個金鑰可單獨配置權限、過期時間和啟用狀態。

> 帳戶安全、通行金鑰、會話管理等敏感操作只允許瀏覽器登入會話呼叫；API 金鑰不能修改登入密碼、繫結通行金鑰或退出其他會話。

## 整合方式選擇
| 場景 | 建議方式 | 原因 |
| --- | --- | --- |
| 人工簽發、排障、改配置 | 瀏覽器控制台 | 可用完整審計和互動確認 |
| CI/CD 觸發續期/部署 | API 金鑰 + `tasks:run` / `deploy:execute` | 最小權限、可輪換 |
| 證書清單同步到 CMDB | API 金鑰 + 只讀權限 | 不暴露私鑰 |
| 證書下載並分發 | API 金鑰 + `certificates:key` + `deploy:execute` | 高敏感，需額外網路限制 |
| 監控巡檢 | API 金鑰 + `system:read` / `monitors:read` | 只讀，短期過期 |
| 日誌審計 | API 金鑰 + `logs:read` / `system_logs:read` | 只讀，匯出到 SIEM |

API 金鑰不是多使用者 RBAC。需要多人分權時，應在自有平臺或閘道中實現確認和代理呼叫。
## 認證
把金鑰放在 `Authorization: Bearer` 或 `X-API-Key` 請求頭中：

```bash
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```

```bash
curl -H "X-API-Key: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```
- 介面固定位於 `/api`；舊版 `/<entry>/api` 保留為相容別名。請求仍需登入會話或 API 金鑰認證，不依賴門禁入口。
- API 金鑰請求不依賴 Cookie，不需要 `X-LACTSSL-CSRF` 頭；所有請求建議使用 HTTPS，金鑰不得出現在 URL 查詢引數中。
- 回應頭包含 `X-Request-ID`，排障時提供給上游服務或運維。
## API 金鑰生命週期
### 建立
1. 使用管理員帳戶登入控制台。
2. 開啟「API 金鑰」頁面，點選「建立金鑰」。
3. 填寫名稱、過期時間，並按需勾選權限。
4. 頁面會顯示一次完整金鑰；關閉彈窗後無法再次檢視，只能輪換。

金鑰格式為 86 位 Base64URL 隨機字串（64 位元組熵），不含固定字首。服務端只儲存摘要，資料庫洩露時無法直接還原明文金鑰。

```bash
curl -X POST \
  -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI deployment",
    "scopes": ["tasks:read", "tasks:run", "deploy:execute"],
    "expires_at": "2027-01-01T00:00:00Z"
  }' \
  https://ssl.example.com/api/api-keys
```
`expires_at` 留空表示永不過期，生產不建議。建立和修改金鑰屬於高風險操作：瀏覽器會話依賴登入身份，API 金鑰呼叫則依賴 `api_keys:write` 權限。
### 輪換與停用
- 輪換後舊令牌立即失效；停用或刪除後，所有使用該金鑰的呼叫返回 `401`。
- 透過 API 金鑰管理其他金鑰時，不能授予自身沒有的權限，也不能修改/輪換/刪除權限高於自身的金鑰。
- 為每個系統單獨發放，設定合理過期時間，到期前輪換；懷疑洩露時立即輪換或刪除，並檢查操作日誌。
### 保管
- 存入密碼管理器或金鑰管理系統，不寫入程式碼倉庫、映象或日誌。
- 命名包含系統名和環境，避免多個系統共用同一把金鑰。
## 權限模型
權限按「資源:動作」劃分。`*` 表示全部系統 API 能力，包含所有敏感操作。

| 權限 | 說明 | 敏感 |
| --- | --- | --- |
| `*` | 全部系統 API 能力 | 是 |
| `profile:read` | 檢視當前金鑰所屬帳戶 | 否 |
| `system:read` | 檢視版本、狀態和後設資料 | 否 |
| `dashboard:read` | 檢視儀表盤統計 | 否 |
| `certificates:read` | 檢視證書、下載證書鏈和證書分析 | 否 |
| `certificates:write` | 清理和刪除證書 | 否 |
| `certificates:key` | 檢視或下載證書私鑰 | 是 |
| `certificates:request` | 提交客戶端 CSR，由服務端向公共 CA 完成驗證與簽發，或使用私有 CA 簽發（還需 `private_cas:write`） | 是 |
| `deploy:execute` | 手動部署證書，或建立/修改/執行包含部署節點的任務（寫檔案；命令執行另需 `deploy:command`） | 是 |
| `deploy:command` | 允許本地/SSH 部署執行 reload 命令（等同於以服務帳號執行任意命令） | 是 |
| `tasks:read` | 檢視任務列表與任務日誌 | 否 |
| `tasks:write` | 建立、修改、刪除和啟停任務，清理任務執行日誌（建立即執行還需 `tasks:run`；含部署節點還需 `deploy:execute`；私有 CA 還需 `private_cas:write`） | 否 |
| `tasks:run` | 手動執行任務或批次執行（含部署節點還需 `deploy:execute`；私有 CA 還需 `private_cas:write`） | 否 |
| `monitors:read` | 檢視站點證書監控 | 否 |
| `monitors:write` | 建立、修改、刪除和立即檢查監控 | 否 |
| `dns_accounts:read` | 檢視 DNS 服務商帳戶（金鑰掩碼） | 否 |
| `dns_accounts:write` | 建立、修改和刪除 DNS 服務商帳戶 | 是 |
| `deploy_accounts:read` | 檢視部署目標帳戶（金鑰掩碼） | 否 |
| `deploy_accounts:write` | 建立、修改和刪除部署目標帳戶 | 是 |
| `deployments:read` | 檢視證書部署歷史 | 否 |
| `ca:read` | 檢視 ACME CA、端點、帳戶和訂單 | 否 |
| `ca:write` | 修改 ACME CA、端點、EAB 和清理診斷資料 | 是 |
| `private_cas:read` | 檢視私有 CA 證書與證書鏈 | 否 |
| `private_cas:write` | 建立、匯入和刪除私有 CA | 是 |
| `private_cas:key` | 下載私有 CA 私鑰 | 是 |
| `plugins:read` | 檢視外掛與啟用狀態 | 否 |
| `plugins:write` | 上傳、啟停和刪除外掛 | 是 |
| `logs:read` | 檢視和匯出操作日誌，檢視/下載日誌歸檔 | 否 |
| `logs:write` | 清空全部操作日誌、刪除日誌歸檔（清空動作本身會留下審計記錄） | 是 |
| `system_logs:read` | 檢視和匯出程序日誌，檢視/下載日誌歸檔 | 否 |
| `system_logs:write` | 刪除全部系統日誌、刪除日誌歸檔 | 是 |
| `settings:read` | 檢視系統設定與內部 CA 信任鏈 | 否 |
| `settings:write` | 修改一般系統設定（SMTP/通知目標；測試訊息仍僅瀏覽器會話） | 是 |
| `backup:read` | 檢視備份狀態與大小 | 否 |
| `backup:download` | 匯出加密備份（僅瀏覽器會話；僅保留歷史金鑰相容） | 是 |
| `backup:restore` | 上傳並恢復備份，會重啟服務（僅瀏覽器會話；僅保留歷史金鑰相容） | 是 |
| `upgrade:read` | 檢視資料庫遷移與程式升級狀態 | 否 |
| `upgrade:write` | 執行遷移、最佳化資料庫和替換程式 | 是 |
| `security:write` | 修改信任根與外掛信任錨（門禁入口僅瀏覽器會話） | 是 |
| `api_keys:read` | 檢視 API 金鑰列表與權限 | 否 |
| `api_keys:write` | 建立、修改、輪換和刪除 API 金鑰 | 是 |
### 推薦角色對映
| 角色 | 權限 |
| --- | --- |
| 只讀巡檢 | `system:read`、`dashboard:read`、`certificates:read`、`tasks:read`、`monitors:read`、`deployments:read` |
| CI/CD 續期 | `tasks:read`、`tasks:run`；含部署節點時加 `deploy:execute` |
| 證書分發 | `certificates:read`、`certificates:key`、`deploy:execute` |
| 日誌審計 | `logs:read`、`system_logs:read` |
| 監控 | `system:read`、`monitors:read` |

私鑰權限是附加權限：

- 下載證書私鑰還需要 `certificates:read`；下載私有 CA 私鑰還需要 `private_cas:read`。
- 建立/執行含部署節點的任務必須顯式擁有 `deploy:execute`；`certificates:write` 和 `tasks:*` 不隱含部署執行能力。
- 本地/SSH 部署中的 `reload_command` 還必須顯式擁有 `deploy:command`；僅 `deploy:execute` 只能寫入證書檔案。
- 使用私有 CA 簽發證書的任務需要 `private_cas:write`；建立 `enabled:true` 的任務會立即簽發，除 `tasks:write` 外還需要 `tasks:run`。
## 回應格式
成功：

```json
{
  "success": true,
  "data": {}
}
```
失敗：

```json
{
  "success": false,
  "code": "api.error.api_key_scope_denied",
  "message": "API 金鑰缺少所需權限：certificates:read",
  "params": { "scope": "certificates:read" }
}
```

| 狀態碼 | 含義 |
| --- | --- |
| `400` | 引數錯誤、配置校驗失敗 |
| `401` | 未認證，金鑰不存在、已停用、已過期或已刪除 |
| `403` | 權限不足，或該介面只允許瀏覽器會話呼叫 |
| `404` | 資源不存在或路徑錯誤 |
| `409` | 狀態衝突（如正在執行、重複操作） |
| `429` | 請求過於頻繁，按 `Retry-After` 重試 |
| `500` | 服務端錯誤，記錄 `X-Request-ID` 後排查 |
## 常用介面
### 檢視狀態與證書
```bash
# 程序狀態（需要 system:read）
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/status

# 證書列表（需要 certificates:read）
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificates
```
列表介面直接返回陣列；日誌介面返回分頁物件：

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/logs?page=1&size=50&level=error&source=task&q=demo"
```
`size` 最大 200；`q` 按原始儲存內容和引數值匹配。清空操作日誌使用 `DELETE /api/logs`，需要 `logs:write`。啟用 `log.archive.enabled` 時，服務會先寫入歸檔再刪除，回應額外返回 `archived` 物件；歸檔失敗會返回 500 且不刪除日誌。

日誌歸檔介面：

```bash
# 列表：支援 page、size、category、reason、from、to、q
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives?page=1&size=20&category=operation"

# 下載歸檔檔案
curl -fsS -OJ -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>/download"

# 刪除歸檔
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>"
```
歸檔介面接受 `logs:*` 或 `system_logs:*` 對應權限：檢視/下載需要任一讀權限，刪除需要任一寫權限。`category` 取值為 `operation` 或 `runtime`；`reason` 取值為 `manual_clear`、`retention` 或 `max_rows`。
### 目標流程預覽
目標流程 Beta 在儲存任務前可以讀取目標網站並預覽證書分組。該介面只讀，需要 `deploy:execute`：

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  https://ssl.example.com/api/tasks/target-flow/preview \
  -d '{
    "sources": [{"deploy_account_id": 3, "deploy_config": {"target": "site"}}],
    "selection": {"mode": "all"},
    "policy": {"strategy": "smart", "max_domains_per_cert": 100},
    "public_ca": true
  }'
```
建立任務時傳 `flow_mode: "target_beta"` 與 `target_flow`；`target_flow.policy.strategy` 支援 `smart`、`per_site`、`per_base_domain`、`single`、`per_source`、`custom`。一個 Beta 任務可能關聯多張證書，證書關聯記錄在 `task_certificate_links` 中。
### 執行任務
```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/run
```
如果任務包含啟用的部署節點，金鑰必須同時擁有 `deploy:execute`。預設會複用仍在有效期內且未進入續期視窗的證書，只重新執行部署節點；確需強制重新簽發時使用 `?force=1` 或傳送 `{"force":true}`，會消耗 CA 的證書配額。

查詢任務執行日誌（需要 `tasks:read`）：

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```
清理任務執行日誌（需要 `tasks:write`）：

```bash
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```
清理介面會刪除該任務的簽發與部署日誌，回傳刪除行數 `deleted`，並在操作日誌中保留一筆稽核記錄。
### 下載證書
```bash
# 完整鏈（需要 certificates:read）
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.fullchain.pem \
  "https://ssl.example.com/api/certificates/42/download?type=fullchain"

# 私鑰（需要 certificates:read + certificates:key）
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.key \
  "https://ssl.example.com/api/certificates/42/download?type=key"
```
`type` 支援 `fullchain`（預設）、`cert`、`key`。下載介面返回 PEM 原文，不包在 JSON 中；請按檔案權限儲存，不要寫入日誌。
### 站點監控
```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors

curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors/7/check
```
### API 金鑰管理
| 介面 | 方法 | 權限 | 說明 |
| --- | --- | --- | --- |
| `/api-keys` | GET | `api_keys:read` | 列出金鑰，不返回明文令牌 |
| `/api-keys/scopes` | GET | `api_keys:read` | 獲取權限列表與本地化說明 |
| `/api-keys` | POST | `api_keys:write` | 建立金鑰，回應中僅此一次返回 `token` |
| `/api-keys/:id` | PUT | `api_keys:write` | 修改名稱、權限、啟用狀態和過期時間 |
| `/api-keys/:id/rotate` | POST | `api_keys:write` | 輪換金鑰，舊令牌立即失效 |
| `/api-keys/:id` | DELETE | `api_keys:write` | 刪除金鑰 |
### 使用客戶端 CSR 申請證書
服務端作為中間環節接收客戶端 CSR，使用已配置的公共 CA 和 DNS 帳戶完成 DNS-01 驗證並簽發證書，或使用管理員維護的私有 CA 直接簽名。服務端不會生成或儲存私鑰，證書簽發後私鑰仍由客戶端保管。

建立申請需要 `certificates:request` 權限；使用私有 CA 時還需要 `private_cas:write`：

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n",
    "ca": "letsencrypt",
    "challenge": "dns01",
    "dns_account_id": 3,
    "idempotency_key": "order-20261118-001",
    "wait_seconds": 0
  }' \
  https://ssl.example.com/api/certificate-requests
```
使用私有 CA 時，將 `ca` 設為 `private-ca:<id>`，或直接傳入 `private_ca_id`。私有 CA 申請不進行 DNS-01 驗證，不需要 `dns_account_id` 和 ACME 聯絡信箱，並允許申請 `app.internal` 等內網網域：

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n",
    "private_ca_id": 2,
    "idempotency_key": "internal-20261121-001"
  }' \
  https://ssl.example.com/api/certificate-requests
```
介面返回 `202 Accepted` 和申請 ID，申請非同步執行，可輪詢狀態：

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx
```
狀態為 `issued` 後可下載證書鏈，不提供私鑰：

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx/certificate?type=fullchain" \
  -o example.com.fullchain.crt
```
- 公共 CA 申請僅支援 DNS-01，萬用字元證書必須使用 DNS-01；私有 CA 申請由服務端直接簽名，不使用網域挑戰。
- `ca` 可以是「公共CA」頁面中的 ACME 上游，也可以是 `private-ca:<id>`；私有 CA 簽發還需要 `private_cas:write`，並會執行該 CA 的擴展金鑰用途和名稱約束檢查。
- 服務端會嚴格校驗 CSR 簽名、公鑰強度、SAN 規範和通用名稱；公共 CA 申請會拒絕內部網域，私有 CA 申請則允許。CSR 已由客戶端簽名，服務端無法替客戶端修正。
- `idempotency_key` 建議必傳；同一 CSR、CA、DNS 路由在同一呼叫方下只允許一個在途申請。
- 可透過 `csr.allowed_domains` 限制允許申請的網域，多個網域用逗號或空白分隔。私有 CA 申請在允許列表為空時不做額外限制，依賴 CA 自身的名稱約束。
- 證書列表中的 `key_available` 為 `false`，表示服務端沒有私鑰，不能下載私鑰或執行依賴私鑰的部署。

倉庫內提供了 Python 客戶端腳本 `scripts/csr_client.py`，它會在本地生成私鑰和 CSR，提交申請、輪詢狀態，並在下載後校驗證書與私鑰匹配：

```bash
export LACTSSL_URL="https://ssl.example.com"
export LACTSSL_TOKEN="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

python3 scripts/csr_client.py \
  --domains example.com,www.example.com \
  --dns-account-id 3 \
  --ca letsencrypt \
  --out-dir ./certs

# 私有 CA 申請（私鑰仍只在本機產生）
python3 scripts/csr_client.py \
  --domains app.internal,api.internal \
  --private-ca-id 2 \
  --out-dir ./certs
```
也可以使用互動式嚮導逐項填寫（不帶引數執行也會進入嚮導）：

```bash
python3 scripts/csr_client.py --interactive
```
續期、查詢與批次操作：

```bash
# 續期：用既有私鑰產生新 CSR（私鑰不複製、不修改）
python3 scripts/csr_client.py \
  --key ./certs/example.com.key \
  --domains example.com,www.example.com \
  --out-dir ./certs \
  --force

# 或直接從上次的狀態檔案續期（自動重用私鑰、CA、DNS 帳戶與輸出名）
python3 scripts/csr_client.py --renew-from ./certs/example.com.request.json

# 查詢 / 取消申請（--json 便於指令碼處理）
python3 scripts/csr_client.py --status csr_xxxxxxxx --json
python3 scripts/csr_client.py --cancel csr_xxxxxxxx --yes
python3 scripts/csr_client.py --list --json

# 批次申請：順序執行，失敗時預設停止
python3 scripts/csr_client.py --manifest ./certs.json --json
```
批次清單格式：

```json
{
  "defaults": {"ca": "letsencrypt", "dns_account_id": 3, "out_dir": "./certs"},
  "certificates": [
    {"domains": ["example.com", "www.example.com"]},
    {"domains": ["api.example.com"], "name": "api", "key_type": "rsa2048"}
  ]
}
```
腳本只上傳 CSR，私鑰始終儲存在客戶端；輸出包括 `.key`、`.csr`、`.crt`、`.fullchain.crt` 和 `.request.json`。本機產生的私鑰與 CSR 先以 `.new` 暫存，憑證下載並校驗通過後才原子替換最終檔案，申請失敗不會破壞上一套可用憑證。

建立申請後會立即寫入 `.request.json`，斷網或使用 `--quiet` 時可用 `--request-id csr_xxxxxxxx` 續傳。網路異常預設重試 3 次（`--retries`），服務端標記可重試時自動呼叫 `/retry` 1 次（`--auto-retry`），並遵循 429 的 `Retry-After`。進度訊息寫入 stderr，`--json` 時 stdout 只有最終 JSON；金鑰可透過 `LACTSSL_TOKEN_FILE` 或 `--token-file` 讀取，避免出現在命令列引數中。客戶端介面支援 `zh-CN`、`zh-TW`、`en-US`、`ja-JP`、`ru-RU`，可透過 `--lang` 或 `LACTSSL_LANG` 切換。結束碼：0 成功、1 一般失敗、2 引數/設定、3 認證/權限、4 網路、5 服務端申請失敗、6 本機校驗失敗、130 使用者取消。
### 驗證憑據可用性
在新增或修改 DNS 帳戶、目標憑據後，可主動驗證憑據是否真實可用。驗證介面使用 `dns_accounts:write` / `deploy_accounts:write` 權限；瀏覽器會話不再需要透過當前密碼或 TOTP 再認證。驗證結果僅作提示，不會阻止儲存或執行。未傳 `domain` 時，已儲存的 DNS 帳戶優先使用其已啟用的網域綁定；沒有綁定時回傳 `mode: "scope"`，僅校驗憑據設定與呼叫權限。

| 方法 | 路徑 | 說明 |
| --- | --- | --- |
| POST | `/api/dns-accounts/:id/verify` | 驗證已儲存 DNS 帳戶；可傳 `domain`、`mode` |
| POST | `/api/dns-accounts/verify` | 驗證未儲存的 DNS 配置；必須顯式提交金鑰 |
| POST | `/api/deploy-accounts/:id/verify` | 驗證已儲存目標憑據；可傳 `deploy_config` |
| POST | `/api/deploy-accounts/verify` | 驗證未儲存的目標憑據配置；必須顯式提交金鑰 |
| POST | `/api/deploy-accounts/:id/options` | 使用已儲存憑據讀取部署欄位的動態選項，例如寶塔網站列表 |
| POST | `/api/deploy-accounts/:id/match` | 依憑證網域比對目標網站；回傳 `field`、`multiple`、`values` |

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"domain":"example.com","mode":"auto"}' \
  https://ssl.example.com/api/dns-accounts/3/verify
```
動態選項介面只適用於 manifest 中宣告瞭 `options_source` 的部署欄位，API 金鑰需要 `deploy_accounts:write` 權限。請求體傳入欄位名和當前部署引數，憑據由服務端從目標憑據中讀取：

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"field":"sites","config":{"target":"docker"}}' \
  https://ssl.example.com/api/deploy-accounts/3/options
```
返回 `{"options":[{"label":"站點名稱","value":"站點名稱"}]}`。外掛不可用或面板不可達時返回明確錯誤，介面會保留手工輸入能力。返回結構包含分項檢查，便於區分認證失敗、網域不屬於該憑據、目錄不可寫等原因：

```json
{
  "success": true,
  "data": {
    "ok": false,
    "mode": "write",
    "checks": [
      {"key": "init", "status": "ok"},
      {"key": "domain", "status": "ok", "message": "example.com"},
      {"key": "write", "status": "failed", "message": "..."}
    ],
    "message": "...",
    "verified_at": "2026-11-18T12:00:00Z"
  }
}
```
行為說明：

- DNS 驗證預設使用 `auto` 模式：外掛宣告 `verify-v1` 時優先只讀列出 zone 並檢查網域歸屬；未宣告或返回不支援時回退到寫測試（寫入隨機 `_acme-challenge` TXT 後立即刪除）。`mode=read` 強制只讀，外掛不支援時返回 `unsupported`。
- 已儲存帳戶只使用資料庫中的配置；未儲存配置必須顯式提交金鑰，不會複用已儲存金鑰，避免透過修改 endpoint 把憑據發往其他伺服器。
- 目標憑據驗證支援內建 `local`/`ssh`：連線並認證，在目標目錄建立並刪除臨時檔案，不執行 `reload_command`；其他部署 helper 在實現 `verify` 能力前返回 `unsupported`。
- 傳入 `deploy_config` 可驗證具體部署節點的作用域（站點、目錄、桶、網域），這是“新增權限是否可用”的推薦用法。
## CI/CD 示例
### 證書續期後分發
```bash
#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${LACTSSL_URL:?}"
TOKEN="${LACTSSL_TOKEN:?}"

# 觸發任務（任務 ID 由 CMDB 或配置管理下發）
curl -fsS -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  "${BASE_URL}/api/tasks/${TASK_ID}/run"

# 等待任務完成後，按證書 ID 下載並分發
curl -fL \
  -H "Authorization: Bearer ${TOKEN}" \
  -o /run/secrets/example.com.fullchain.pem \
  "${BASE_URL}/api/certificates/${CERT_ID}/download?type=fullchain"
```
- API 沒有冪等鍵；重複呼叫 `run` 會再次執行任務流程，但證書仍有效時會跳過簽發並只執行部署，腳本仍應自行去重。
- 任務執行是非同步的；需要等待完成時輪詢任務列表或訂閱通知，不要假設 POST 返回即完成。
- 下載私鑰需要額外權限；大多數分發場景只需證書鏈，私鑰由部署節點直接寫入目標，不建議經 CI 中轉。
### 巡檢
```bash
#!/usr/bin/env bash
set -euo pipefail

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/status" >/dev/null

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/certificates" |
  jq -e '[.data[] | select(.not_after < (now + 14*86400 | todate))] | length == 0'
```
（示例使用 `jq`；實際欄位名以介面返回為準。）
## 錯誤處理與重試
- 對 `429` 按 `Retry-After` 退避；對 `5xx` 使用指數退避並記錄 `X-Request-ID`。
- 對 `401` 不要重試，檢查金鑰是否過期、停用或輪換；對 `403` 檢查權限和介面限制。
- 對 `400`/`409` 不要自動重試，先修正引數或狀態。
- 涉及簽發/部署的請求可能產生外部副作用，重試前先查詢任務/部署狀態。
## 安全建議
- 為每個自動化任務建立獨立金鑰，只授予真正需要的權限；只讀任務不要授予 `*`。
- 設定合理過期時間並定期輪換；不在日誌、截圖或公開倉庫中暴露完整金鑰。
- 懷疑洩露時立即停用或刪除金鑰，並在網路層限制呼叫來源。
- 將 API 呼叫日誌與操作日誌關聯，保留呼叫方、時間和 `X-Request-ID`。
