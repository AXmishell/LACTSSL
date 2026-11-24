# 拡張開発：ソースとビルド

本文は、二次開発、プライベートビルド、コントリビューションを行う開発/連携チーム向けです。**運用担当者とエンドユーザーは本稿を読む必要はありません**。本番デプロイは[インストールと起動](install.md)、プラグインの使用は[プラグインガバナンス](plugins.md)を参照してください。

## 1. リポジトリ構造

```text
cmd/server/          プログラム入口、起動とセキュアエントリの初期化
internal/api/        Gin ルーティング、ハンドラー、セッション、レート制限
internal/auth/       JWT とパスワード/TOTP 関連ロジック
internal/config/     config.yaml の読み込みと検証
internal/db/         SQLite のオープンとマイグレーション
internal/i18n/       バックエンドの国際化と言語リソース
internal/logging/    プロセスログ、レベル、フォーマット、ファイルローテーション、非同期 DB 書き込み
internal/model/      データベースエンティティとタスクフロー
internal/acme/       組み込み ACME サーバープロトコル
internal/service/    ACME クライアント、デプロイ、通知、監視、スケジューリング
internal/plugin/     プラグインホスト（host/、runtime/、providers/、builtin/）と公式実装
plugins/             外部プラグイン SDK: プロトコル v1、helper フレームワーク、共通ユーティリティ
internal/store/      データアクセス層
internal/util/       暗号化、検証などのユーティリティ
internal/webui/      埋め込みフロントエンドビルド成果物
web/                 Vue 3 + Element Plus フロントエンド
scripts/             開発スクリプトと CI（TypeScript、Node で直接実行）
docs/                簡体字中国語ドキュメント
docs/en/             英語ドキュメント
docs/zh-hant/        繁体字中国語ドキュメント
docs/ja/             日本語ドキュメント
docs/ru/             ロシア語ドキュメント
```

公式 DNS/デプロイ/通知実装は本リポジトリの `internal/plugin/` にあり、メインモジュールと一緒にビルド・テストされます。`internal/plugin/providers` は独立した Go module（`lactssl/providers`）で、プラグイン SDK と上流 provider SDK のみに依存し、ホストはローカル `replace` で参照します。`plugins/` は外部プラグイン開発 SDK（プロトコル、helper フレームワーク、共通ユーティリティ）です。アップロードプラグインは引き続きプロトコル v1 のサブプロセスとして実行されます。メインプログラムは外部プラグインリポジトリや署名インデックスに依存しません。

## 2. ローカル開発

### 2.1 環境

- Go 1.27+
- Node.js 22.12+ と npm（Vite 8 の要件）
- 任意: Docker / Docker Compose v2

### 2.2 よく使うコマンド

```bash
make dev          # Vite + Go の開発モードをバックグラウンドで起動
make dev-down     # 開発プロセスを停止
make dev-restart  # 開発プロセスを再起動
make dev-status   # PID / ポート / ヘルス状態を表示
make dev-logs     # バックエンドと Vite のログを追跡
make build        # 本番ビルド、成果物 bin/lactssl
make backend      # バックエンドのみビルド、成果物 bin/lactssl
make server-dev   # 開発モードのバックエンド、成果物 bin/lactssl-dev（フロントエンドを埋め込まない）
make run          # 直接 go run ./cmd/server
make test         # go test ./...
make typecheck    # フロントエンドの vue-tsc + scripts の tsc
make ci           # 完全なローカル CI
make i18n-check   # 翻訳キーの一致を検証（デフォルトは中国語、英語、繁体字中国語）
make i18n-check-all # すべての言語を完全検証（ロシア語、日本語を含む）
make tidy         # go mod tidy + フロントエンド依存関係のインストール
make clean        # ビルド成果物をクリーンアップ
make docs-build   # 中国語・英語ドキュメントをビルド（strict）
make docs-serve   # バイリンガルドキュメントをローカルプレビュー
```

### 2.3 開発モードのポート

`make dev` が起動するもの:

- Vite: `:5173`、HMR 付き、フロントエンドソース。
- Go: デフォルト `:8080`、`config.yaml` の `server.addr` / `server.port` を読み取ります。

`http://<host>:5173/<entry>/` にアクセスします。Vite は `/api` と旧バージョンの `/<entry>/api` を Go へプロキシします。Go の `8080` は `5173` へ**リバースプロキシしません**。本番実行時に Vite はありません。

開発モードのスクリプト:

```bash
./scripts/dev.sh start            # Go バックエンド + Vite をバックグラウンドで起動
./scripts/dev.sh status           # PID / ポート / ヘルス状態
./scripts/dev.sh logs panel       # バックエンドログを追跡（logs / logs web / logs all）
./scripts/dev.sh restart --build  # bin/lactssl-dev を再コンパイルしてから再起動
./scripts/dev.sh stop             # 停止
```

PID ファイルとログは `data/run/` にあります:

```text
data/run/panel.pid  data/run/panel.log
data/run/web.pid    data/run/web.log
```

よく使うオプション:

| 項目 | 説明 |
| --- | --- |
| `--no-web` | バックエンドのみ起動し、Vite は起動しない |
| `--build` | 起動/再起動前に `make server-dev` を実行して再コンパイル |
| `WEB_PORT` / `DEV_HOST` | Vite ポート（デフォルト `5173`）とリッスンアドレス（デフォルト `0.0.0.0`） |

バックエンドポートとセキュアエントリは統一して `config.yaml` から読み取ります。設定ファイルが存在しない場合は `config.yaml.example` から生成されます。

## 3. ビルドとバージョン注入

- フロントエンドのビルド成果物はまず `web/dist` に出力され、次に `internal/webui/dist` へコピーされ、`embed` タグでバイナリに埋め込まれます。
- バージョン番号は git tag を唯一のソースとし、`scripts/version.sh` が `git describe --tags --always --dirty` を呼び出して導出し（`v` プレフィックスを除去）、`-ldflags -X 'lactssl/internal/api.Version=...'` で注入します。
- ビルド時刻とアーキテクチャも同様です。注入されていない場合、バージョンは `dev` と表示されます。
- リポジトリは `VERSION` ファイルを管理しません。
- バージョンを固定する必要がある場合は明示的に上書きできます:

```bash
LACTSSL_VERSION=1.2.3 make build
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
```

## 4. テストと CI

```bash
make test
make typecheck
make i18n-check
make ci
```

`make ci` は `.github/workflows/ci.yml` と同等で、分割して実行することもできます:

```bash
./scripts/ci.sh --backend-only
./scripts/ci.sh --frontend-only
./scripts/ci.sh --no-build
```

CI はさらに `gofmt` チェック、`go vet`、`-race` 付きテストとカバレッジ統計、フロントエンドの型チェックとビルドを実行します。

## 5. 国際化

- バックエンドの言語リソースは `internal/i18n/locales/` にあります。
- フロントエンドの言語リソースは `web/src/i18n/locales/` にあります。
- 新しい文言を追加する場合は `zh-CN`、`en-US`、`zh-TW` を同時に補完し、その後 `make i18n-check` を実行します。
- 現在は繁体字中国語 `zh-TW`、ロシア語 `ru-RU`、日本語 `ja-JP` が組み込まれています。`zh-TW` はデフォルトで検証対象となり、文言が不足する場合は `zh-CN` へ直接フォールバックします。ロシア語と日本語はデフォルトで検証対象外で、文言が不足する場合は `言語 → en-US → zh-CN` の順にフォールバックします。翻訳を同期した後は `make i18n-check-all`（または `I18N_CHECK_ALL=1`）ですべて検証できます。
- チェックスクリプトは TypeScript（`scripts/check-i18n.ts`）で、Node のネイティブ型消去により直接実行され、Node 22.18+ が必要です。
- スクリプト自身の型チェックは `make typecheck` に統合されています（設定は `scripts/tsconfig.json`）。

## 6. ドキュメントビルド

ドキュメントは Zensical/MkDocs を使用します:

```bash
make docs-build   # すべての言語をビルド、strict モード
make docs-serve   # バックグラウンドでプレビュー（ポート 8000、-l で言語指定、scripts/docs-serve.sh stop で停止）
```

設定・ソース・ビルド成果物はすべて `docsite/` にあります。Zensical は設定ファイルのあるディレクトリをプロジェクトルートとするため、`docs_dir` と `site_dir` はその中に置く必要があります。

- 英語設定: `docsite/mkdocs.yml`、出力 `docsite/site`（デフォルトサイト）。
- 簡体字中国語: `docsite/mkdocs.zh.yml` → `docsite/site/zh`。
- 繁体字中国語: `docsite/mkdocs.zh-hant.yml` → `docsite/site/zh-hant`。
- 日本語: `docsite/mkdocs.ja.yml` → `docsite/site/ja`。
- ロシア語: `docsite/mkdocs.ru.yml` → `docsite/site/ru`。
- `docs-internal/` は内部設計記録で、サイトには含まれません。
- 新しいページを追加する場合は、`docsite/docs/<言語>/` 配下に同期してミラーし、各言語設定の nav を更新する必要があります。

## 7. リリースフロー

リリースは tag を唯一のソースとします。GitHub Release は `Release` workflow をトリガーし、各プラットフォームの成果物を自動ビルドしてアップロードします:

```bash
gh release create v1.0.1 --generate-notes
```

成果物:

- `lactssl_<version>_linux_amd64.tar.xz`
- `lactssl_<version>_linux_amd64.tar.gz`
- `lactssl_<version>_linux_arm64.tar.xz`
- `lactssl_<version>_linux_arm64.tar.gz`
- `lactssl_<version>_darwin_amd64.tar.xz`
- `lactssl_<version>_darwin_amd64.tar.gz`
- `lactssl_<version>_darwin_arm64.tar.xz`
- `lactssl_<version>_darwin_arm64.tar.gz`
- `lactssl_<version>_windows_amd64.zip`
- `checksums.txt`（SHA-256）

アーカイブにはバイナリ、`README.md`、`LICENSE`、`config.yaml.example` が含まれます。バージョン番号は tag から直接取得し（`v` プレフィックスを除去）、ローカル git の導出に依存しません。Windows 以外では `.tar.xz` と `.tar.gz` の両方を公開します。`.tar.xz` はダウンロードサイズが小さく、`.tar.gz` は旧バージョンの自動アップグレードクライアントとの互換用です。

同時に `linux/amd64`、`linux/arm64` イメージをビルドし、GHCR へプッシュします:

```text
ghcr.io/axmishell/lactssl:<version>
ghcr.io/axmishell/lactssl:<major>.<minor>
ghcr.io/axmishell/lactssl:<major>
ghcr.io/axmishell/lactssl:latest
```

プレリリース版は完全な `<version>` タグのみをプッシュし、`latest`、`<major>.<minor>`、`<major>` は更新しません。初回リリース後、GHCR パッケージはデフォルトでプライベートなので、パッケージ設定で Public に変更する必要があります。

注意事項:

- workflow は Release の正式公開時のみトリガーされます。`gh release create --draft` は公開をクリックした後にビルドを開始します。
- 初回使用前に、`.github/workflows/release.yml` が tag の指すコミットに含まれている必要があります。
- `gh release create <tag> <files>...` で事前アップロードした同名ファイルは、workflow が `--clobber` で上書きします。作成時にファイルを渡さないことを推奨します。
- Release 公開後、アセットのビルド完了には数分かかります。「アセット準備後に公開」したい場合は、先に tag をプッシュし、CI のビルド完了後に Release を作成してください（別途 tag push フローが必要です）。
- 指定 tag を再ビルド: `gh workflow run release.yml -f tag=v1.0.1`。
- リリース署名: `ED25519_PRIVATE_KEY_B64` を設定すると、`checksums.txt` の分離署名を生成します。

ローカルリリーススクリプト:

```bash
make release TAG=v1.0.1
./scripts/release.sh v1.0.1 --no-docker
./scripts/release.sh v1.0.1 --multiarch
./scripts/release.sh v1.1.0-rc.1 --docker-only
./scripts/release.sh v1.0.1 --publish-only
./scripts/release.sh v1.0.1 --upload
```

`--multiarch` には Docker Buildx が必要です。イメージはホストでクロスコンパイルしたバイナリをコピーするだけで、ビルド中にターゲットアーキテクチャのコマンドを実行しないため、QEMU は不要です。

## 8. プラグイン連携デバッグ

公式実装は `internal/plugin/` にあり、`plugins/` は外部プラグイン SDK のみを含みます。SDK はメインリポジトリの `go.mod` からローカル `replace` で参照されます。公式実装を変更した後は、リポジトリルートからビルドしてください:

```bash
go work init . ../LACTSSL-Plugins
```

プラグイン実装を変更した後、プラグインリポジトリで `make gen` と `make plugin KIND=dns GOOS=linux GOARCH=amd64` を実行して統合パッケージをビルドします。連携デバッグ終了後は `go.work` を削除してください（このファイルは gitignore 済みです）。

プラグインプロトコル、フィールド、署名は[プラグイン開発](plugin-development.md)を参照してください。

## 9. コントリビューション時の注意

- `config.yaml`、`data/`、バックアップ、ログ、または実際の認証情報をコミットしないでください。
- セキュリティ関連ロジックを変更する場合は、[セキュリティ基準](security.md)とテストを同時に更新してください。
- API/設定/プラグインの動作を変更する場合は、中国語・英語ドキュメントを同時に更新してください。
- 新しいエンドポイントを追加する場合は、API キー権限ポリシーとテストを補完してください。
- データマイグレーションを伴う場合は、冪等な手順、バックアップ、ロールバックの説明を提供してください。
- コミット前に `make ci` と `make docs-build` を実行してください。

## 関連ドキュメント

- [プラグイン開発](plugin-development.md)
- [設定リファレンス](configuration.md)
- [アップグレードと変更](upgrade.md)
- [セキュリティ基準](security.md)
