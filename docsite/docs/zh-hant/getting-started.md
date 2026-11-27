# 快速開始

用 Docker 啟動例項、完成初始化，並簽發第一張證書。通常 30 分鐘內可以跑通；生產環境加固見[安全基線](security.md)。

## 準備

- 一臺能執行 Docker 的 Linux 主機（VPS、NAS 或小主機）
- 一個網域和 DNS API 憑據
- 一個部署目標（本地目錄、SSH 或面板）

## 啟動例項

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
docker compose up -d
docker compose logs -f lactssl
```

倉庫預設使用 `1panel-network`；不使用 1Panel 時刪除 `docker-compose.yml` 中兩處 `networks` 段，或先執行 `docker network create 1panel-network`。GHCR 映象從首個正式 Release 起可用，未釋出時按[安裝與啟動](install.md)本地構建。

## 初始化

啟動日誌會列印門禁入口和一次性初始化令牌：

```text
門禁入口: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
初始化令牌（僅本次啟動有效）: ...
```

透過門禁入口建立管理員帳戶，啟用 TOTP 或通行金鑰，並設定恢復郵箱。初始化完成後令牌立即失效；忘記入口時可在 `config.yaml` 設定 `security.entry` 並重啟覆蓋。

> 門禁入口不是安全邊界。生產環境必須使用 HTTPS、強密碼、TOTP/通行金鑰和網路存取控制。

## 啟用外掛

官方 DNS、部署和通知外掛已內建，無需安裝：

1. 開啟「外掛」頁，確認對應類別已啟用。
2. 啟用所需的 DNS 服務商、部署目標或通知通道。
3. 第三方擴充按需上傳 helper，見[外掛治理](plugins.md)。

DNS 服務商預設全部停用，部署類別預設開啟。

## 簽發第一張證書

先用私有 CA 跑通鏈路，不依賴公網 DNS：

1. 在「私有 CA」生成根 CA（ECDSA P-256）。
2. 在「證書申請」建立任務，簽發機構選擇該私有 CA，填寫測試網域。
3. 新增「部署證書」節點，選擇「本地目錄」，填寫證書/私鑰路徑和可選過載指令。
4. 新增「通知」節點，選擇已啟用的通道，觸發時機選「始終」。
5. 手動執行任務，確認證書、部署記錄和通知都成功。

再補一條公網鏈路：建立 DNS 帳戶 → 選擇公共 CA → 用測試網域走 DNS-01 → 部署到目標 → 觀察一次自動續期。

## 驗證

| 動作 | 驗證方式 |
| --- | --- |
| 備份與恢復 | 下載加密備份，在隔離例項匯入 |
| 日誌 | 篩選並匯出操作日誌與系統日誌 |
| API | 用唯讀金鑰呼叫 `/api/certificates` |
| 通知 | 傳送測試通知 |
| 監控 | 新增一個外部站點並讀取證書 |

## 生產上線

用 1–2 個真實網域、一個部署目標和一個內部 CA 場景驗證鏈路。進入生產前完成[安全基線](security.md)的上線檢查清單，並按[運維手冊](operations.md)落實備份、監控與變更流程。

常見失敗原因：

- 只把門禁入口當安全邊界。
- 使用個人 DNS 帳號做 API 憑據。
- 證書更新後未驗證過載指令。
- 只備份資料庫，不備份 `config.yaml`；丟失 `salt` 後加密憑據無法解密。
