# 擴充套件開發：外掛協議與貢獻指南

本文面向外掛開發者、整合商和企業平臺團隊，說明 LACTSSL 外掛協議、倉庫結構、開發流程、構建、打包、索引、簽名和釋出。**運維人員通常只需閱讀[外掛治理](plugins.md)和對應的 DNS/部署/通知外掛頁面。**

外掛原始碼、構建工具與倉庫索引位於獨立倉庫 [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins)。主程式只實現協議 v1 的 manifest 解碼，執行期從外掛索引下載按型別合併的外掛包；兩個倉庫不透過 `go.mod` 共享原始碼。

## 1. 架構與心智模型

外掛不是動態庫，而是**一次性子程序**：

```text
LACTSSL 宿主
  └─ exec lactssl-dns-all-native <subcommand>
       ├─ manifest          輸出外掛自描述 JSON
       ├─ present / cleanup stdin JSON → 寫/刪 TXT → 退出
       ├─ verify            只讀探測（可選）
       └─ 退出碼 0 成功；非 0 失敗，錯誤寫 stderr
```

四類 helper：

| Helper | 型別 | 子命令 |
|---|---|---|
| `cmd/dns-helper` | DNS（v4 / native） | `manifest` / `present` / `cleanup` / `verify` |
| `cmd/dns-helper-v5` | DNS（lego v5） | `manifest` / `present` / `cleanup` / `verify` |
| `cmd/deploy-helper` | 證書部署 | `manifest` / `deploy` / `options` / `plan` / `verify` |
| `cmd/notify-helper` | 通知 | `manifest` / `send` |

外掛倉庫採用**模組化單體**：

- 每個 provider 一個目錄；
- 後設資料、文案、圖示按 provider 拆分；
- 聚合檔案由 `make manifest` 生成，禁止手改；
- 多個貢獻者可以並行開發，減少單檔案衝突。

## 2. 協議 v1

協議保持 `v1`；只有破壞性變更才升 `v2`。新能力透過可選欄位與特性標記擴充套件，舊宿主會忽略無法識別的欄位。

### 2.1 子命令與輸出

DNS：

```text
dns-plugin manifest
dns-plugin present   # stdin JSON
dns-plugin cleanup   # stdin JSON
dns-plugin verify    # stdin JSON
```

部署：

```text
deploy-plugin manifest
deploy-plugin deploy
deploy-plugin options
deploy-plugin plan
deploy-plugin verify
```

通知：

```text
notify-plugin manifest
notify-plugin send
```

約定：

- 退出碼 `0` 成功，非 `0` 失敗；stderr 進入主程式日誌。
- DNS `present` / `cleanup` 的 stdout 必須為空。
- 部署 `deploy` 成功時 stdout 為空；`verify` / `options` / `plan` 輸出 JSON。
- 通知 `send` 成功輸出一行 `{"ok":true,"message":""}`；失敗也會輸出 `{"ok":false,...}` 並以非 0 退出。
- 請求與回應均使用 UTF-8 JSON，非 ASCII 不轉義。

### 2.2 環境變數

| 變數 | 說明 |
|---|---|
| `LACTSSL_PLUGIN_PROTOCOL` | 協議版本，當前 `v1` |
| `LACTSSL_PLUGIN_MEMORY_LIMIT` | 常駐記憶體預算（位元組），helper 應呼叫 `debug.SetMemoryLimit` |
| `LACTSSL_DNS_PLUGIN_FEATURES` | DNS 特性列表，逗號分隔 |
| `LACTSSL_DEPLOY_PLUGIN_FEATURES` | 部署特性列表，逗號分隔 |

當前特性：

| 特性 | 含義 |
|---|---|
| `merged-deployers-v1` | 部署目標只輸出合併後的規範 ID |
| `dynamic-options-v1` | 支援 `options` 子命令和 `options_source` / `multiple` |
| `verify-v1` | 支援 `verify` 只讀探測 |
| `plan-v1` | 支援 `plan` 預匹配 |

### 2.3 安全約定

- 私鑰、token 只透過 stdin 傳遞，不寫入 argv 或環境變數；
- 主程式只傳遞最小環境變數集合；
- helper 必須可執行，且不能允許組或其他使用者寫入；符號連結會被拒絕；
- 主程式輪詢 `VmRSS`，超過 `memory_limit_mb` 時結束整個程序組；
- `manifest.sha256` 可留空，倉庫安裝由索引 asset SHA-256 與安裝記錄保證完整性。

## 3. 倉庫結構

```text
protocol/                    協議 v1 線格式
sdk/                         共享 HTTP、簽名、DNS 工具、helper 框架
outbound/                    SSRF 防護出站 HTTP 客戶端
catalog/
  dns/<id>.json              單 DNS provider 後設資料（手寫）
  deploy/<id>.json           單部署目標後設資料（手寫）
  migrations.json            歷史部署 ID 與遷移
  data.json                  生成物：聚合後設資料
i18n/
  core/<locale>.json         公共文案
  providers/<kind>/<id>/     單 provider 文案
  locales/<locale>.json      生成物：聚合文案
dns/providers/<id>/          lego v4 包裝（生成）
dns/native/providers/<id>/   native DNS 實現（手寫）
dns/groups/                  生成物：DNS 分組
dnsv5/                       lego v5 包裝、分組、後設資料（生成）
deploy/providers/<id>/       部署目標（手寫）
notify/providers/channels/   每渠道一個 channel_<id>.go
cmd/                         helper 入口
assets/icons/                圖示
plugins.json                 生成物：合併包清單
scripts/                     生成器與構建工具
```

生成物：

```text
catalog/data.json
i18n/locales/*.json
cmd/dns-helper/group_*.go
cmd/deploy-helper/imports_gen.go
dns/groups/**
dnsv5/**
plugins.json
```

修改原始檔後執行：

```bash
make manifest
```

該命令依次執行 `catalog`、`i18n`、`deployimports`、`gen-v5`，再生成 `plugins.json` 與分組。CI 會校驗生成物是否遺漏；同倉庫 PR 還會由 `Update generated files` bot 自動回寫。

## 4. 開發 DNS provider

### 4.1 Native 實現

位置：`dns/native/providers/<id>/provider.go`

```go
package example

import (
    "github.com/AXmishell/LACTSSL-Plugins/dns"
    "github.com/AXmishell/LACTSSL-Plugins/sdk/dnsutil"
)

type plugin struct{}

func (plugin) ID() string        { return "example" }
func (plugin) Aliases() []string { return nil }
func (plugin) New(cfg map[string]string) (dns.ChallengeProvider, error) {
    return newProvider(cfg)
}

func init() {
    dns.Register(plugin{})
    // 只有實現 Verify 時才宣告
    // dns.MarkVerifiable("example")
}
```

`ChallengeProvider` 只有兩個方法：

```go
Present(domain, token, keyAuth string) error
CleanUp(domain, token, keyAuth string) error
```

DNS-01 的硬性規則：

- 寫入 DNS 的值是 `dnsutil.TXTValue(keyAuth)`，不是 `keyAuth` 原文；
- 網域用 `dnsutil.ChallengeFQDN(domain)`；
- zone 匹配用 `dnsutil.ZoneName`，相對名用 `dnsutil.RelativeName`；
- `CleanUp` 只刪除本次 TXT 值，保留其他值；記錄不存在時視為成功；
- TTL 預設值與 lego 保持一致（常見 60s）。

可選只讀驗證：

```go
func (p *provider) Verify(ctx context.Context, domain string) (string, error)
```

實現後記得 `dns.MarkVerifiable("example")`，否則宿主不會呼叫 `verify`。

### 4.2 lego v4 包裝

`dns/providers/<id>` 由生成器產出：

```bash
go run scripts/gen_dns_plugins/main.go
make manifest
```

不要手改生成檔案。需要調整別名、欄位或環境變數時，改生成器或 `catalog/dns/<id>.json`。

### 4.3 lego v5

```bash
make gen-v5
make manifest
```

生成器會重寫 `dnsv5/providers`、`dnsv5/legacy`、`dnsv5/groups`、`dnsv5/meta`。v5 新增 provider 的欄位來自上游 `.toml`；catalog 已有同名條目時沿用歷史欄位。

### 4.4 後設資料與文案

`catalog/dns/<id>.json` 示例：

```json
{
  "value": "example",
  "label_key": "meta.dns.example.label",
  "desc_key": "meta.dns.example.desc",
  "fields": [
    {
      "key": "token",
      "label_key": "meta.field.token.label",
      "type": "password",
      "required": true,
      "env": "EXAMPLE_TOKEN"
    }
  ]
}
```

`env` 只對 lego 包裝生效；native 實現直接讀取 `cfg["token"]`。

文案：

```text
i18n/providers/dns/<id>/zh-CN.json
i18n/providers/dns/<id>/en-US.json
```

公共欄位文案放在 `i18n/core/<locale>.json`。缺失翻譯會回退到預設語言，再回退到 `label` / `value`。

## 5. 開發部署目標

位置：`deploy/providers/<id>/plugin.go`

```go
type plugin struct{}

func (plugin) ID() string        { return "example" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(ctx context.Context, req deploy.Request) error { ... }

func init() { deploy.Register(plugin{}) }
```

可選能力：

| 介面 | 用途 | 宣告 |
|---|---|---|
| `deploy.Verifier` | 只讀驗證憑據/目標 | `deploy.MarkVerifiable("example")` |
| `deploy.OptionLister` | 動態下拉選項 | catalog 欄位 `options_source` |
| `deploy.Planner` | 批次部署前讀取網站/物件 | `deploy.MarkPlannable("example")` |

開發步驟：

1. 建立 `deploy/providers/<id>/plugin.go`；
2. 建立 `catalog/deploy/<id>.json`，包含 `fields` 與 `deploy_fields`；
3. 建立 `i18n/providers/deploy/<id>/zh-CN.json` 與 `en-US.json`；
4. 執行 `make manifest` 生成 `cmd/deploy-helper/imports_gen.go`；
5. 新增 `httptest` 單測。

安全約定：

- 使用者可控 URL 必須使用 `outbound.ValidateURL` / `outbound.ValidateWebhookURL`；
- 出站請求使用 `outbound.NewClient`，不要直接使用 `http.DefaultClient`；
- `Verify` / `Plan` / `ListOptions` 不得寫證書、建立持久物件或 reload；
- 錯誤資訊不包含 token/secret。

歷史 ID 透過 `catalog/migrations.json` 的 `deprecated_deploy_types` 與 `deploy_migrations` 對映到規範目標。

## 6. 開發通知渠道

位置：`notify/providers/channels/channel_<id>.go`

```go
package channels

func init() {
    registerChannel(newChannel("example", []notify.Field{
        text("url", "notifications.fieldServerUrl", true),
        password("token", "notifications.fieldToken", true),
    }, sendExample))
}

func sendExample(cfg map[string]any, msg notify.Message) error { ... }
```

欄位構造器：

| 函式 | 用途 |
|---|---|
| `text` | 文字 |
| `password` | 密碼/金鑰 |
| `number` | 數字 |
| `selectField` + `opt` | 下拉 |
| `textarea` | 多行文字 |

每個渠道獨立檔案，透過 `init` 自動註冊，不需要修改共享列表。傳送邏輯優先使用 `notifyJSON` / `notifyForm` / `notifyHTTP` 與 `outbound`。

## 7. 構建

```bash
# 全部合併包
make bundle

# DNS 單變體
make bundle-lego
make bundle-native
make bundle-lego-v5
```

構建引數：

```bash
CGO_ENABLED=0 go build -trimpath \
  -tags "bundle_dns_all_native,nethttpomithttp2,http2legacy" \
  -ldflags "-s -w -X main.buildVersion=<version>" \
  -o lactssl-dns-all-native ./cmd/dns-helper
```

- `-trimpath` 去掉本機路徑；
- `-s -w` 去符號和 DWARF；
- `bundle_dns_all_lego` / `bundle_dns_all_native` 選擇 DNS 分組；
- `nethttpomithttp2,http2legacy` 規避 Go 1.27 `net/http` 與新版 `x/net/http2` 的相容問題；
- lego v5 額外使用 `urfave_cli_no_template,ozzo_validation_no_template,grpcnotrace,validator_novalidatefn` 裁掉模板程式碼。

## 8. 打包與索引

單包構建：

```bash
make plugin KIND=dns ID=all-native GOOS=linux GOARCH=amd64
```

產物：

```text
dist/
├── lactssl-dns-all-native_<version>_linux_amd64.tar.gz
└── lactssl-dns-all-native_<version>_linux_amd64.meta.json
```

包內容：

```text
plugin.json
lactssl-dns-all-native
LICENSE
icons/<provider>.<svg|png|webp>
```

`plugin.json` 包含版本、協議、bundle、providers、variant、conflicts、sdk 與圖示引用。索引：

```bash
make index BASE_URL=https://github.com/AXmishell/LACTSSL-Plugins/releases/download
```

索引欄位：

- `schema` / `channel` / `protocol` / `generated_at`；
- `plugins[]`：kind、id、version、bundle、providers、variant_of、conflicts_with、sdk、assets；
- `assets[GOOS/GOARCH]`：url、sha256、size、uncompressed_size；
- 根級 `icons[]`：內聯 base64 圖示，用於未安裝時展示。

### 8.1 簽名

```bash
go run scripts/pluginctl/main.go sign -key key.b64 -in index.json -out index.json.sig
```

`key.b64` 是 base64 編碼的 Ed25519 私鑰（32 位元組種子或 64 位元組私鑰）。生產釋出應強制配置 `PLUGIN_INDEX_SIGNING_KEY`，缺失時不應釋出未簽名索引。

## 9. 版本策略

- `all-lego` / `all-lego-v5` 版本跟隨 `go.mod` 中的 lego 版本；
- `all-native`、`deploy/all`、`notify/all` 的補丁號由內容指紋自動遞增；
- 公共輸入（`protocol/`、`sdk/`、`i18n/`、`outbound/`、`catalog/`、`go.mod`）變化會觸發所有相關包升版，避免“同版本不同內容”；
- provider 自身原始碼變化隻影響對應型別的包；
- 釋出以 tag 為準，非 tag 手動釋出使用內部版本，需注意與已釋出 tag 版本的先後關係。

## 10. 協作流程

### 10.1 分支與 PR

```text
main
  ↑
短分支（1～3 天）
  ↑
PR → lint / generated / test / bundle / native → CODEOWNER review
  → merge queue → CI gate → 合併
  → after-merge bot 重新整理生成物
```

要求：

- 禁止直接 push `main`；
- 必須 PR，必須透過 `CI gate`；
- 使用 Merge Queue；
- 單 provider / 單目標 / 單渠道一個 PR；
- 生成物由 bot 回寫，貢獻者不要手工解決生成物衝突。

### 10.2 CODEOWNERS

外掛倉庫 `.github/CODEOWNERS` 定義預設 owner。團隊擴大後按 area 替換為 GitHub Team：

- DNS native、lego、deploy、notify、infra；
- protocol / sdk / catalog schema / CI 需要核心維護者複核。

### 10.3 分層 CI

| 層級 | 觸發 | 內容 |
|---|---|---|
| lint | 每個 PR / merge queue | gofmt、go.mod/go.sum |
| generated | 每個 PR / merge queue | `make manifest` + 生成物校驗 |
| test | Go 變更 | `go vet ./...`、`go test ./...` |
| bundle | DNS/deploy/notify 變更 | 構建 5 個 helper、manifest 數量/一致性、打包 smoke |
| native | DNS/SDK 變更 | `check_native.sh` |
| nightly | 每天 | `go test -race`、全量構建、govulncheck |

分支保護只需要求 **CI gate** 一個檢查；條件 job 被跳過時 gate 仍會透過。

### 10.4 Merge Queue

倉庫設定：

- 保護 `main`；
- Require status checks：`CI gate`；
- Require linear history；
- 開啟 Merge Queue（最大併發 5，等待超時 5 分鐘）；
- 禁止直接 push；
- 啟用 “Require branches to be up to date before merging”。

## 11. 本地除錯

```bash
# 生成
make manifest

# native DNS manifest
go run -tags bundle_dns_all_native ./cmd/dns-helper manifest | jq '.providers | length'

# 檢視單個 provider
go run -tags bundle_dns_all_native ./cmd/dns-helper manifest \
  | jq '.providers[] | select(.id=="linode")'

# 部署
LACTSSL_DEPLOY_PLUGIN_FEATURES=merged-deployers-v1,verify-v1,plan-v1,dynamic-options-v1 \
  go run ./cmd/deploy-helper manifest | jq

# 通知
go run ./cmd/notify-helper manifest | jq '.channels | length'
```

請求示例：

```bash
echo '{
  "action": "present",
  "provider": "linode",
  "config": {"token": "..."},
  "domain": "example.com",
  "token": "acme-token",
  "key_auth": "acme-token.thumbprint"
}' | go run -tags bundle_dns_all_native ./cmd/dns-helper present
```

## 12. 測試要求

- 單測使用 `httptest`，不訪問真實網路；
- DNS 覆蓋：寫入值、清理只刪本次值、記錄不存在冪等、無匹配 zone；
- 部署覆蓋：成功、鑑權失敗、Verify/Plan 不寫入；
- 通知覆蓋：配置缺失、請求體、錯誤狀態碼；
- manifest 數量與欄位由 CI 斷言；
- native 包額外執行 `./scripts/check_native.sh`。

## 13. 提交前檢查

```text
[ ] 只改自己的 provider 目錄 + 對應 catalog/i18n
[ ] init 註冊且 ID/alias 唯一
[ ] catalog 欄位 key 與程式碼一致
[ ] 中英文文案都補齊
[ ] 單測使用 httptest
[ ] make manifest
[ ] gofmt / go vet / go test
[ ] check_native（native）
[ ] 生成物已提交或等待 bot 回寫
```

## 14. 相關文件

- [外掛治理](plugins.md)
- [DNS 外掛](dns-plugins.md)
- [部署外掛](deploy-plugins.md)
- [通知外掛](notify-plugins.md)
- [外掛倉庫貢獻指南](https://github.com/AXmishell/LACTSSL-Plugins/blob/main/CONTRIBUTING.md)
