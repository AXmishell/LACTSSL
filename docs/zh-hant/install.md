# 安裝與啟動

本文給出生產可用的安裝步驟。第一次評估建議先看[快速上手與上線](getting-started.md)，確定場景和驗收標準後再按本文部署。個人站點和小團隊可以跳過評估，直接按第 2 節用 Docker 部署。

## 1. 環境要求

| 專案 | 要求 |
| --- | --- |
| 作業系統 | Linux amd64 / arm64；也支援 macOS 和 Windows 的釋出包 |
| 容器 | Docker 及 Docker Compose v2（推薦） |
| 原始碼構建 | Go 1.27+、Node.js 22.12+、npm |
| 主機資源 | 參考[部署架構](architecture.md)；起步建議 2 vCPU / 1 GB，個人單網域場景 1 vCPU / 1 GB 也可執行 |
| 網路 | 管理入口經 HTTPS 反向代理；出站可訪問 DNS API、CA、SMTP、部署目標 |
| 網域 | 一個管理網域和對應 TLS 證書；ACME 服務端可選獨立網域 |

主程式不需要外部資料庫、Redis 或訊息佇列。所有狀態都在 `data/` 目錄中。

!!! tip "個人 / 小團隊"
    不需要走企業評估流程：按第 2 節 Docker 啟動、第 4 節完成初始化、第 6.3 節安裝外掛即可。寶塔、1Panel 等面板建站需要安裝對應的部署外掛；自動續期依賴 DNS API，建議使用只授權所需網域的專用子帳號。外掛安裝/更新會有瞬時記憶體峰值，不要把容器記憶體限制到剛好等於空閒佔用。

## 2. 方式一：Docker Compose（推薦）

### 2.1 準備目錄

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
```

倉庫自帶的 `docker-compose.yml` 預設使用 GHCR 映象：

```yaml title="docker-compose.yml"
--8<-- "docker-compose.yml"
```

預設檔案沿用 1Panel 的 `1panel-network` 外部網路。不使用 1Panel 時，刪除服務下的 `networks` 和檔案末尾的 `networks` 段，讓 Compose 使用預設網路；也可以先建立該網路：

```bash
docker network create 1panel-network
```

### 2.2 啟動

```bash
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```

!!! note "映象與網路"
    GHCR 映象從首個正式 Release 起可用；如果 `docker compose pull` 提示映象不存在，請按 2.4 節在本地構建。Compose 預設把 8080 釋出到宿主機所有網路卡；公網主機建議用防火牆限制來源，或把 `ports` 改為 `127.0.0.1:8080:8080`，只允許本機反向代理訪問。

預設掛載：

- `./config/config.yaml`：配置檔案
- `./data`：SQLite、外掛、日誌和升級備份

容器內固定讀取 `/app/config/config.yaml`，資料目錄為 `/app/data`。入口腳本會以 root 修正掛載目錄歸屬，然後降權到 `lactssl` 使用者執行。Compose 預設啟用只讀根檔案系統、`no-new-privileges`、capabilities 白名單和 `pids_limit`。

### 2.3 固定版本

不要長期使用 `latest`。直接修改 `docker-compose.yml` 中的映象標籤：

```yaml
image: ghcr.io/axmishell/lactssl:1.2.3
```

穩定標籤包括 `<version>`、`<major>.<minor>`、`<major>` 和 `latest`；預釋出版本只推送完整版本標籤。

### 2.4 本地構建映象

Dockerfile 只複製外部構建好的 Linux 二進位檔，不在映象內編譯前端/後端；資料庫備份/恢復由 Go 原生實現，不需要安裝 `pg_dump`/`mysqldump`。先準備前端和當前架構的程式產物，再構建映象：

```bash
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
docker run -d --name lactssl \
  -p 127.0.0.1:8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  --restart unless-stopped \
  lactssl:1.2.3
```

## 3. 方式二：二進位制 + systemd

### 3.1 安裝

```bash
sudo useradd --system --home /opt/lactssl --shell /usr/sbin/nologin lactssl
sudo mkdir -p /opt/lactssl/data
sudo cp lactssl /opt/lactssl/
sudo cp config.yaml.example /opt/lactssl/config.yaml
sudo chown -R lactssl:lactssl /opt/lactssl
sudo chmod 600 /opt/lactssl/config.yaml
sudo chmod 700 /opt/lactssl/data
```

!!! note "原生資料庫備份"
    PostgreSQL/MySQL 的邏輯備份與恢復由 LACTSSL 的 Go 程式碼直接完成，不再依賴 `pg_dump`、`pg_restore`、`mysqldump` 或 `mysql` 客戶端。SQLite 仍使用 `VACUUM INTO` 快照。

### 3.2 systemd 單元

```ini title="/etc/systemd/system/lactssl.service"
[Unit]
Description=LACTSSL certificate manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lactssl
Group=lactssl
WorkingDirectory=/opt/lactssl
ExecStart=/opt/lactssl/lactssl -config /opt/lactssl/config.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/lactssl/data

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lactssl
sudo journalctl -u lactssl -f
```

如果日誌寫入 `/opt/lactssl/data/logs/`，上面的 `ReadWritePaths` 已覆蓋；如果寫到其他目錄，需要額外放行。使用 80/443 等特權埠時，建議由反向代理轉發，而不是給二進位制提權。

### 3.3 一鍵部署腳本

倉庫提供 `scripts/install.sh`，用於從 GitHub Release 下載 Linux amd64/arm64 二進位制，建立執行使用者與資料目錄，安裝 systemd 服務，並生成 Nginx 反向代理配置。預設不啟用 TLS：二進位制監聽 `127.0.0.1:8080`，Nginx 監聽 80 並轉發到該埠。

```bash
sudo ./scripts/install.sh --port 8080 --domain ssl.example.com
```

也可以不克隆倉庫，直接下載腳本執行：

```bash
curl -fsSL https://raw.githubusercontent.com/AXmishell/LACTSSL/main/scripts/install.sh \
  | sudo bash -s -- --port 8080 --domain ssl.example.com
```

Linux 釋出壓縮包中也會附帶 `install.sh`，解壓後可直接執行。

常用引數：

| 引數 | 說明 |
| --- | --- |
| `--port PORT` | 二進位制監聽埠，預設 `8080`，必須 >= 1024 |
| `--domain DOMAIN` | Nginx `server_name`，例如 `ssl.example.com`；預設 `_` |
| `--version TAG` | 指定版本，例如 `v1.2.3`；預設最新穩定版 |
| `--no-nginx` | 只部署 systemd 服務，不安裝或修改 Nginx |
| `--tls-cert` / `--tls-key` | 可選，啟用 Nginx HTTPS 並配置 80 → 443 跳轉 |
| `--no-tls` | 關閉已有 Nginx TLS 配置（預設本來就不啟用 TLS） |
| `--read-write-path DIR` | 額外允許服務寫入的目錄，可重複；用於本機證書部署目標 |
| `--skip-checksum` | 跳過 SHA-256 校驗（不推薦） |

示例：啟用 HTTPS

```bash
sudo ./scripts/install.sh \
  --domain ssl.example.com \
  --port 8080 \
  --tls-cert /etc/nginx/certs/ssl.example.com.pem \
  --tls-key  /etc/nginx/certs/ssl.example.com.key
```

腳本會校驗 Release 中的 `checksums.txt`。重複執行時只替換二進位制並重啟服務，已有 `config.yaml` 和 `data/` 會保留；指定 `--port` 時會同步更新 `server.addr`、`server.host`、`server.port`。未指定 `--domain` / `--tls-cert` / `--tls-key` 時，會沿用已有 Nginx 配置中的網域與證書，避免升級時意外退回預設站點或關閉 TLS；如需關閉 TLS 請顯式使用 `--no-tls`。如果 Nginx 未安裝，腳本會嘗試透過系統的包管理器安裝；不希望自動安裝時可加 `--no-install-deps`，不配置 Nginx 時可加 `--no-nginx`。服務單元啟用了 `ProtectSystem=strict`，程式頁面中的自升級可能因目錄只讀而不可用；升級請重新執行本腳本。如需讓內建部署目標寫入本機 `/etc/nginx` 等目錄，可重複使用 `--read-write-path` 放行，並確保 `lactssl` 使用者對這些目錄有寫權限。

預設生成的關鍵配置：

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1", "::1"]
security:
  public_url: "http://ssl.example.com"
```

Nginx 配置寫入 `/etc/nginx/conf.d/lactssl.conf`，包含 `client_max_body_size 256m` 和 600 秒讀寫超時。部署完成後按第 4 節透過啟動日誌中的門禁入口完成初始化。

### 3.4 從原始碼構建

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```

`make build` 先構建前端並複製到 `internal/webui/dist`，再編譯嵌入了前端資源的 `bin/lactssl`。僅構建後端可使用 `make backend`，但需要自行準備前端產物。

## 4. 首次初始化

啟動日誌會列印門禁入口和一次性初始化令牌：

```text
門禁入口: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
初始化令牌（僅本次啟動有效）: ...
```

1. 透過門禁入口開啟頁面。
2. 按提示建立管理員帳戶。
3. 如使用備份恢復，在 `/install` 的高階功能中匯入，並攜帶初始化令牌。
4. 初始化成功後令牌立即失效。

初始化前可以在 `/install` 的「執行配置」中修改常用白名單欄位：監聽地址與可信反向代理、`public_url` 與會話保持、ACME（獨立監聽、對外地址、TLS/mTLS）、日誌（輪轉、訪問日誌、系統日誌）、升級與程式自升級、DNS/部署/通知外掛開關、外掛倉庫及資源限制。儲存後需要重啟服務生效。

`jwt_secret`、`salt`、門禁入口、資料目錄、資料庫與日誌檔案、外掛目錄、程式倉庫與簽名公鑰等敏感或高風險欄位不會在頁面中暴露，請直接在伺服器上編輯 `config.yaml`。門禁入口可在初始化並登入後透過「設定 → 門禁入口」修改。

> 門禁入口不是安全邊界。生產必須使用 HTTPS、強密碼、TOTP/通行金鑰和網路訪問控制。

## 5. 反向代理與 HTTPS

主程式只監聽 HTTP，生產由 Nginx、Caddy、Traefik、負載均衡或 WAF 終止 TLS。

### 5.1 必填配置

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

security:
  public_url: "https://ssl.example.com"
```

- `trusted_proxies` 必須包含代理地址，否則客戶端 IP 識別和限流會失真，`Secure` Cookie 判斷也可能錯誤。
- 只填寫實際代理的精確 IP；確需網段時使用最小 CIDR，不建議配置 `10.0.0.0/8` 這類大範圍。
- `public_url` 用於密碼重置等連結，不能依賴請求 Host。
- 如果 TLS 在代理終止，確認代理傳遞 `X-Forwarded-Proto: https`。

### 5.2 Nginx 示例

```nginx
server {
    listen 443 ssl;
    server_name ssl.example.com;

    ssl_certificate     /etc/nginx/certs/ssl.example.com.pem;
    ssl_certificate_key /etc/nginx/certs/ssl.example.com.key;
    ssl_protocols       TLSv1.2 TLSv1.3;

    client_max_body_size 256m;  # 備份恢復和外掛上傳

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        # 覆蓋模式：丟棄客戶端自帶的 XFF，只保留真實客戶端地址
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

這是推薦的**覆蓋模式**：LACTSSL 只信任這台 Nginx 即可。若改用 `$proxy_add_x_forwarded_for`（追加模式），單層部署仍然正確；多級代理時 `trusted_proxies` 必須包含鏈上所有會出現在 XFF 的代理，否則客戶端 IP 會停在上游代理。CDN、Docker/Kubernetes 和 L4 負載均衡的差異見[配置說明](configuration.md)第 11 節。

建議在代理層再加來源 IP 白名單、VPN、零信任訪問策略或 WAF。反向代理的訪問日誌可作為獨立審計來源，但不要記錄 Cookie 和 `Authorization` 頭。

### 5.3 ACME 服務端（可選）

如果需要向內部客戶端提供 ACME：

```yaml
acme:
  enabled: true
  addr: ":14000"
  base_url: "https://pki.example.com:14000"
  tls:
    cert: "/etc/lactssl/acme.crt"
    key: "/etc/lactssl/acme.key"
```

- `addr` 留空時 ACME 掛在主埠的 `/acme` 字首；生產建議獨立埠。
- `base_url` 必須是客戶端實際訪問地址，公網部署不要依賴請求 Host。
- EAB、網域白名單、mTLS 和來源限制見[安全基線](security.md)。

## 6. 安裝後的最小配置

### 6.1 帳戶安全

- 立即啟用 TOTP 或新增通行金鑰。
- 設定受控的恢復郵箱；郵箱本身必須啟用多因素認證。
- 明確管理員帳戶由崗位持有，離職/轉崗時完成交接。
- 在「設定 → 會話」中檢查並撤銷未知會話。

### 6.2 資料與備份

```bash
# 先停止寫入再複製，或使用介面上的加密備份
tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

生產建議使用介面「系統設定 → 備份與恢復」生成加密 ZIP，並把備份存到獨立介質。備份密碼與 `config.yaml` 分開保管。

### 6.3 外掛

主程式不內建任何 DNS / 部署 / 通知外掛：

1. 進入「外掛」頁，確認倉庫地址，點選「重新整理索引」。
2. 在「外掛市場」安裝所需外掛。
3. 在對應列表中啟用服務商、部署目標或通知渠道。
4. 生產環境建議使用內部映象並固定版本，見[外掛治理](plugins.md)。

未安裝外掛時：

- DNS 服務商列表為空，無法建立 DNS 帳戶。
- 部署目標只有本地目錄和 SSH。
- 通知只有郵件。

### 6.4 驗證

```bash
# 程序與埠
curl -I http://127.0.0.1:8080/          # 未帶門禁 Cookie 應為 404，屬預期
ss -lntp | grep 8080

# 使用 API 金鑰做認證檢查
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/status
```

然後在介面完成：

- 建立 DNS 帳戶並測試。
- 建立一條測試任務並執行。
- 傳送測試通知。
- 新增一個站點監控。
- 下載並解密一次備份。

## 7. 升級與解除安裝

升級前必須備份，升級步驟見[升級與變更](upgrade.md)。

Docker：

```bash
docker compose pull
docker compose up -d
```

二進位制：

```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo systemctl start lactssl
```

解除安裝前先匯出備份。刪除 `data/` 會永久丟失資料庫、外掛和加密憑據；刪除 `config.yaml` 會丟失 `salt` 和 `jwt_secret`。

## 8. 常見安裝問題

| 現象 | 原因 | 處理 |
| --- | --- | --- |
| `docker compose up` 報外部網路不存在 | 預設依賴 `1panel-network` | 刪除兩處 `networks` 使用預設網路，或先建立該網路 |
| 訪問根路徑返回 404 | 未透過門禁入口 | 使用啟動日誌中的入口地址 |
| 登入後 Cookie 異常/限流不準 | 未配置可信代理 | 配置 `trusted_proxies` 並傳遞轉發頭 |
| 密碼重置連結指向錯誤網域 | 未配置 `public_url` | 設定固定對外地址 |
| 上傳備份/外掛失敗 | 代理請求體限制 | 調整 `client_max_body_size` |
| 通行金鑰不可用 | 非 HTTPS 或使用 IP 訪問 | 使用 HTTPS 網域或 localhost |
| 容器重啟後回到舊版本 | 在容器內自升級 | 透過映象標籤升級 |

更多排障見[監控與排障](troubleshooting.md)。
