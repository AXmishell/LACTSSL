# 擴充套件開發：原始碼與構建

本文面向需要二次開發、私有化構建或參與貢獻的研發/整合團隊。**運維和終端使用者不需要閱讀本文**；生產部署請看[安裝與啟動](install.md)，外掛使用請看[外掛治理](plugins.md)。

## 1. 倉庫結構

```text
cmd/server/          程式入口、啟動與門禁入口初始化
internal/api/        Gin 路由、處理器、會話與限流
internal/auth/       JWT 與密碼/TOTP 相關邏輯
internal/config/     config.yaml 載入與校驗
internal/db/         SQLite 開啟與遷移
internal/i18n/       後端國際化與語言資源
internal/logging/    程序日誌、級別、格式、檔案輪轉與非同步落庫
internal/model/      資料庫實體與任務流程
internal/acme/       內建 ACME 服務端協議
internal/service/    ACME 客戶端、部署、通知、監控、排程
internal/plugin/     外掛宿主：dns/deploy/notify、exec、install、meta、proto、repo
internal/store/      資料訪問層
internal/util/       加密、校驗等工具
internal/webui/      嵌入的前端構建產物
web/                 Vue 3 + Element Plus 前端
scripts/             開發腳本與 CI（TypeScript，由 Node 直接執行）
docs/                簡體中文文件
docs/en/             英文文件
docs/zh-hant/        繁體中文文件
docs/ja/             日語文件
docs/ru/             俄語文件
```

外掛實現、官方 helper、外掛協議與後設資料位於獨立倉庫 [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins)。主倉庫在宿主內實現協議 v1 的 manifest 解碼，兩個倉庫透過簽名索引與 `min_host` 做執行期相容，構建期不再互相依賴。

## 2. 本地開發

### 2.1 環境

- Go 1.27+
- Node.js 22.12+ 與 npm（Vite 8 要求）
- 可選：Docker / Docker Compose v2

### 2.2 常用命令

```bash
make dev          # 後臺啟動 Vite + Go 開發模式
make dev-down     # 停止開發程序
make dev-restart  # 重啟開發程序
make dev-status   # 檢視 PID / 埠 / 健康狀態
make dev-logs     # 跟蹤後端與 Vite 日誌
make build        # 生產構建，產物 bin/lactssl
make backend      # 僅構建後端，產物 bin/lactssl
make server-dev   # 開發模式後端，產物 bin/lactssl-dev（不嵌入前端）
make run          # 直接 go run ./cmd/server
make test         # go test ./...
make typecheck    # 前端 vue-tsc + scripts 的 tsc
make ci           # 完整本地 CI
make i18n-check   # 校驗翻譯鍵一致（預設含中英、繁體中文）
make i18n-check-all # 全量校驗所有語言（含俄語、日語）
make tidy         # go mod tidy + 前端依賴安裝
make clean        # 清理構建產物
make docs-build   # 構建中英文文件（strict）
make docs-serve   # 本地預覽雙語文件
```

### 2.3 開發模式埠

`make dev` 啟動：

- Vite：`:5173`，帶 HMR，前端原始碼。
- Go：預設 `:8080`，讀取 `config.yaml` 的 `server.addr` / `server.port`。

訪問 `http://<host>:5173/<entry>/`。Vite 會把 `/api` 和舊版 `/<entry>/api` 代理到 Go；Go 的 `8080` **不會**反向代理 `5173`，生產執行時沒有 Vite。

開發模式腳本：

```bash
./scripts/dev.sh start            # 後臺啟動 Go 後端 + Vite
./scripts/dev.sh status           # PID / 埠 / 健康狀態
./scripts/dev.sh logs panel       # 跟蹤後端日誌（logs / logs web / logs all）
./scripts/dev.sh restart --build  # 重新編譯 bin/lactssl-dev 後重啟
./scripts/dev.sh stop             # 停止
```

PID 檔案與日誌位於 `data/run/`：

```text
data/run/panel.pid  data/run/panel.log
data/run/web.pid    data/run/web.log
```

常用選項：

| 項 | 說明 |
| --- | --- |
| `--no-web` | 只啟動後端，不啟動 Vite |
| `--build` | 啟動/重啟前執行 `make server-dev` 重新編譯 |
| `WEB_PORT` / `DEV_HOST` | Vite 埠（預設 `5173`）與監聽地址（預設 `0.0.0.0`） |

後端埠與門禁入口統一從 `config.yaml` 讀取；配置檔案不存在時由 `config.yaml.example` 生成。

## 3. 構建與版本注入

- 前端構建產物先輸出到 `web/dist`，再複製到 `internal/webui/dist` 並以 `embed` 標籤嵌入二進位制。
- 版本號以 git tag 為唯一來源，透過 `scripts/version.sh` 呼叫 `git describe --tags --always --dirty` 推導（去掉 `v` 字首），並用 `-ldflags -X 'lactssl/internal/api.Version=...'` 注入。
- 構建時間與架構同理；未注入時版本顯示為 `dev`。
- 倉庫不維護 `VERSION` 檔案。
- 需要固定版本時可顯式覆蓋：

```bash
LACTSSL_VERSION=1.2.3 make build
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
```

## 4. 測試與 CI

```bash
make test
make typecheck
make i18n-check
make ci
```

`make ci` 等價於 `.github/workflows/ci.yml`，也可以拆分：

```bash
./scripts/ci.sh --backend-only
./scripts/ci.sh --frontend-only
./scripts/ci.sh --no-build
```

CI 還會執行 `gofmt` 檢查、`go vet`、帶 `-race` 的測試與覆蓋率統計，以及前端型別檢查和構建。

## 5. 國際化

- 後端語言資源位於 `internal/i18n/locales/`。
- 前端語言資源位於 `web/src/i18n/locales/`。
- 新增文案時需同時補充 `zh-CN`、`en-US` 與 `zh-TW`，然後執行 `make i18n-check`。
- 當前已內建繁體中文 `zh-TW`、俄語 `ru-RU` 與日語 `ja-JP`：`zh-TW` 預設參與校驗，缺少文案時直接回退 `zh-CN`；俄語、日語預設不參與校驗，缺少文案時按 `語言 → en-US → zh-CN` 回退，同步翻譯後可用 `make i18n-check-all`（或 `I18N_CHECK_ALL=1`）做全量校驗。
- 檢查腳本是 TypeScript（`scripts/check-i18n.ts`），由 Node 原生型別擦除直接執行，需要 Node 22.18+。
- 腳本自身的型別檢查已併入 `make typecheck`（配置見 `scripts/tsconfig.json`）。

## 6. 文件構建

文件使用 Zensical/MkDocs：

```bash
make docs-build   # 構建全部語言，strict 模式
make docs-serve   # 後台常駐預覽（預設埠 8000；-l 指定語言；scripts/docs-serve.sh stop 停止）
```

- 英文配置：`mkdocs.yml`，輸出 `site`（預設站點）。
- 簡體中文：`mkdocs.zh.yml` → `site/zh`。
- 繁體中文：`mkdocs.zh-hant.yml` → `site/zh-hant`。
- 日語：`mkdocs.ja.yml` → `site/ja`。
- 俄語：`mkdocs.ru.yml` → `site/ru`。
- `docs-internal/` 是內部設計記錄，不進入站點。
- 新增頁面需要在 `docs/<語言>/` 下同步映象，並更新各語言配置的 nav。

## 7. 釋出流程

釋出以 tag 為唯一來源。GitHub Release 會觸發 `Release` workflow，自動構建並上傳各平臺產物：

```bash
gh release create v1.0.1 --generate-notes
```

產物包括：

- `lactssl_<version>_linux_amd64.tar.gz`
- `lactssl_<version>_linux_arm64.tar.gz`
- `lactssl_<version>_darwin_amd64.tar.gz`
- `lactssl_<version>_darwin_arm64.tar.gz`
- `lactssl_<version>_windows_amd64.zip`
- `checksums.txt`（SHA-256）

歸檔內包含二進位制、`README.md`、`LICENSE` 和 `config.yaml.example`。版本號直接取自 tag（去掉 `v` 字首），不依賴本地 git 推導。

同時構建 `linux/amd64`、`linux/arm64` 映象並推送到 GHCR：

```text
ghcr.io/axmishell/lactssl:<version>
ghcr.io/axmishell/lactssl:<major>.<minor>
ghcr.io/axmishell/lactssl:<major>
ghcr.io/axmishell/lactssl:latest
```

預釋出版本只推送完整 `<version>` 標籤，不更新 `latest`、`<major>.<minor>` 和 `<major>`。首次釋出後 GHCR 包預設私有，需要到包設定中改為 Public。

注意事項：

- workflow 只在 Release 正式釋出時觸發；`gh release create --draft` 在點選發布後才會開始構建。
- 首次使用前，`.github/workflows/release.yml` 必須已包含在 tag 指向的提交中。
- 用 `gh release create <tag> <files>...` 預上傳的同名檔案會被 workflow 以 `--clobber` 覆蓋；建議不要在建立時傳檔案。
- Release 釋出後資產需要幾分鐘構建完成；如需“資產就緒後再公開”，應先推送 tag，由 CI 構建完成後再建立 Release（需要另配 tag push 流程）。
- 重新構建指定 tag：`gh workflow run release.yml -f tag=v1.0.1`。
- 釋出簽名：配置 `ED25519_PRIVATE_KEY_B64` 後為 `checksums.txt` 生成分離簽名。

本地釋出腳本：

```bash
make release TAG=v1.0.1
./scripts/release.sh v1.0.1 --no-docker
./scripts/release.sh v1.0.1 --multiarch
./scripts/release.sh v1.1.0-rc.1 --docker-only
./scripts/release.sh v1.0.1 --publish-only
./scripts/release.sh v1.0.1 --upload
```

`--multiarch` 需要 Docker Buildx；跨架構構建還需 QEMU。

## 8. 外掛聯調

外掛原始碼、構建工具和索引位於 `LACTSSL-Plugins`。兩個倉庫不再共享 Go 模組，主倉庫可以獨立構建；需要同時修改外掛實現並做端到端驗證時，可臨時建立 `go.work`：

```bash
go work init . ../LACTSSL-Plugins
```

修改外掛實現後，在外掛倉庫執行 `make gen` 與 `make plugin KIND=dns GOOS=linux GOARCH=amd64` 構建合併包；聯調結束後刪除 `go.work`（該檔案已被 gitignore）。

外掛協議、欄位和簽名見[外掛開發](plugin-development.md)。

## 9. 貢獻注意事項

- 不要提交 `config.yaml`、`data/`、備份、日誌或任何真實憑據。
- 修改安全相關邏輯時同步更新[安全基線](security.md)和測試。
- 修改 API/配置/外掛行為時同步更新中英文文件。
- 新增介面必須補充 API 金鑰權限策略和測試。
- 涉及資料遷移時提供冪等步驟、備份和回滾說明。
- 提交前執行 `make ci` 和 `make docs-build`。

## 相關文件

- [外掛開發](plugin-development.md)
- [配置參考](configuration.md)
- [升級與變更](upgrade.md)
- [安全基線](security.md)
