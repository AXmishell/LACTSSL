# 拡張開発：プラグインプロトコルとコントリビューションガイド

本文はプラグイン開発者、インテグレーター、プラットフォーム開発者向けで、LACTSSL のプラグインプロトコル、リポジトリ構造、開発フロー、ビルド、パッケージング、署名、リリースについて説明します。**利用とガバナンスは[プラグインガバナンス](plugins.md)と対応する DNS/デプロイ/通知プラグインのページを参照してください。**

公式 DNS/デプロイ/通知実装は `internal/plugin/` にあり、メインプログラムと一緒にコンパイルされます。`plugins/` は外部プラグイン開発 SDK（プロトコル、helper フレームワーク、共通ユーティリティ）で、アップロードプラグインは引き続きプロトコル v1 のサブプロセスを使用します。メインプログラムはプラグインインデックスや外部プラグインリポジトリを使用しません。生成器とバンドルツールは `internal/plugin/` にあり、コマンドはそのディレクトリで実行します。外部プラグインは `plugins/protocol` と `plugins/sdk` のみを参照します。

## アーキテクチャとメンタルモデル

プラグインは動的ライブラリではなく、**1 回限りの子プロセス**です:

```text
LACTSSL ホスト
  └─ exec lactssl-dns-all-native <subcommand>
       ├─ manifest          プラグインの自己記述 JSON を出力
       ├─ present / cleanup stdin JSON → TXT を書き込み/削除 → 終了
       ├─ verify            読み取り専用プローブ（任意）
       └─ 終了コード 0 は成功、非 0 は失敗。エラーは stderr へ
```

4 種類の helper:

| Helper | 種別 | サブコマンド |
|---|---|---|
| `tools/helpers/dns-helper` | DNS（v4 / native） | `manifest` / `present` / `cleanup` / `verify` |
| `tools/helpers/dns-helper-v5` | DNS（lego v5） | `manifest` / `present` / `cleanup` / `verify` |
| `tools/helpers/deploy-helper` | 証明書デプロイ | `manifest` / `deploy` / `options` / `plan` / `verify` |
| `tools/helpers/notify-helper` | 通知 | `manifest` / `send` |

プラグインコードベースは**モジュラーモノリス**を採用しています。各 provider に 1 つのディレクトリを持たせ、メタデータ、文言、アイコンを provider ごとに分割し、集約ファイルは `make manifest` で生成して手動編集を禁止します。これにより複数のコントリビューターが並行開発でき、単一ファイルの競合を減らせます。

## プロトコル v1

プロトコルは `v1` のままです。破壊的変更がある場合のみ `v2` に上げ、新機能は任意フィールドと機能フラグで拡張します。古いホストは認識できないフィールドを無視します。

### サブコマンドと出力

DNS は `manifest` / `present` / `cleanup` / `verify`、デプロイは `manifest` / `deploy` / `options` / `plan` / `verify`、通知は `manifest` / `send` をサポートします。stdin の JSON 形式と出力例は、[DNS プラグイン](dns-plugins.md)、[デプロイプラグイン](deploy-plugins.md)、[通知プラグイン](notify-plugins.md)の Helper プロトコルを参照してください。

規約:

- 終了コード `0` は成功、非 `0` は失敗です。stderr はメインプログラムのログに入ります。DNS の `present` / `cleanup` の stdout は空でなければなりません。デプロイの `deploy` は成功時に stdout が空で、`verify` / `options` / `plan` は JSON を出力します。通知の `send` は成功時に 1 行の `{"ok":true,"message":""}`、失敗時も `{"ok":false,...}` を出力して非 0 で終了します。
- リクエストとレスポンスは UTF-8 JSON を使用し、非 ASCII はエスケープしません。

### 環境変数

| 変数 | 説明 |
|---|---|
| `LACTSSL_PLUGIN_PROTOCOL` | プロトコルバージョン、現在 `v1` |
| `LACTSSL_PLUGIN_MEMORY_LIMIT` | 常駐メモリ予算（バイト）。helper は `debug.SetMemoryLimit` を呼び出すべき |
| `LACTSSL_DNS_PLUGIN_FEATURES` | DNS 機能リスト、カンマ区切り |
| `LACTSSL_DEPLOY_PLUGIN_FEATURES` | デプロイ機能リスト、カンマ区切り |

現在の機能:

| 機能 | 意味 |
|---|---|
| `merged-deployers-v1` | デプロイターゲットは統合後の正規 ID のみを出力 |
| `dynamic-options-v1` / `plan-v1` | `options` サブコマンドと `options_source` / `multiple`、`plan` の事前マッチをサポート |
| `verify-v1` | `verify` の読み取り専用プローブをサポート |

### セキュリティ規約

- 秘密キー、token は stdin 経由でのみ渡し、argv や環境変数には書き込みません。メインプログラムは最小限の環境変数セットのみを渡します。
- helper は実行可能で、グループまたは他のユーザーに書き込みを許可してはなりません。シンボリックリンクは拒否されます。
- メインプログラムは `VmRSS` をポーリングし、`memory_limit_mb` を超えるとプロセスグループ全体を終了します。`manifest.sha256` は空でもかまいません。リポジトリインストールではインデックス asset の SHA-256 とインストール記録で完全性を保証します。

## リポジトリ構造

```text
plugins/                     外部プラグイン SDK（独立 module）
  protocol/                  プロトコル v1 ワイヤ形式
  sdk/                       共通 HTTP、署名、DNS ツール、helper フレームワーク
  outbound/                  SSRF 対策済みアウトバウンド HTTP クライアント
  i18n/                      プラグイン側の共通文言
internal/plugin/
  host/
    dns/                     DNS ホストアダプター（インターフェース、レジストリ、探索）
    deploy/                  デプロイホストアダプター
    notify/                  通知ホストアダプター
  providers/                 独立 Go module（lactssl/providers）
    go.mod
    api/                     Native インターフェースとレジストリ（dns/deploy/notify）
    catalog/                 サービス提供者とデプロイ先のメタデータ
      dns/<id>.json          DNS provider ごとのメタデータ（手書き）
      deploy/<id>.json       デプロイターゲットごとのメタデータ（手書き）
      data.json              生成物：集約メタデータ
    dns/lego5/               lego v5 ラッパー、グループ、メタデータ（生成）
    deploy/<id>/             デプロイターゲット（手書き）
    notify/channels/         チャンネルごとに channel_<id>.go
  runtime/
    registry/                汎用 ID/別名/ソースレジストリ
    discovery/               プラグイン探索と共通ツール
    exec/                    ワンショット子プロセス実行
    sandbox/                 アップロードプラグインサンドボックス
    install/                 アトミックインストール/ロールバック
    metadata/                manifest スナップショットキャッシュ
    icon/                    アイコン MIME/data URI
  builtin/                   公式実装からホストレジストリへのアダプター
  assets/icons/              アイコン
  tools/helpers/             独立 helper / 署名ツール（互換/リリース）
  scripts/                   ジェネレーターとビルドツール
  plugins.json               生成物：バンドルマニフェスト
```

生成物:

```text
providers/catalog/data.json
plugins/i18n/locales/*.json
tools/helpers/deploy-helper/imports_gen.go
providers/dns/lego5/**
providers/dns/lego5/groups/**
plugins.json
```

ソースファイルを変更した後は次を実行します:

```bash
make manifest
```

このコマンドは `catalog`、`i18n`、`deployimports`、`gen-v5` を順に実行し、その後 `plugins.json` とグループを生成します。CI は生成物の漏れを検証し、同一リポジトリの PR では `Update generated files` bot が自動的に書き戻します。

## DNS provider の開発

公式 DNS provider は lego v5 を使用し、ラッパーファイル `internal/plugin/providers/dns/lego5/providers/<id>/plugin.go` は生成されます。生成ファイルは手動で編集しないでください。追加・更新手順：

1. `github.com/go-acme/lego/v5` に対象 provider があることを確認する；
2. `catalog/dns/<id>.json` にフィールドと環境変数のマッピングを追加する；
3. `plugins/i18n/providers/dns/<id>/` に `zh-CN.json` と `en-US.json` を追加し、`make gen-v5` でラッパー、グループ、メタデータを生成する；
4. `make metadata-check`、`make manifest`、`make test-providers` を実行する。

`catalog/dns/<id>.json` の例：

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

`env` は helper が lego v5 の環境変数として設定します。文言は `plugins/i18n/providers/dns/<id>/` の `zh-CN.json` と `en-US.json` に置き、共通フィールド文言は `plugins/i18n/core/<locale>.json` にあります。翻訳がない場合は既定言語、次に `label` / `value` にフォールバックします。

## デプロイターゲットの開発

場所: `providers/deploy/<id>/plugin.go`

```go
type plugin struct{}

func (plugin) ID() string        { return "example" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(ctx context.Context, req deploy.NativeRequest) error { ... }

func init() { deploy.RegisterNative(plugin{}) }
```

任意機能:

| インターフェース | 用途 | 宣言 |
|---|---|---|
| `deploy.NativeVerifier` | 認証情報/ターゲットを読み取り専用で検証 | `deploy.MarkNativeVerifiable("example")` |
| `deploy.NativeOptionLister` | 動的ドロップダウンオプション | catalog フィールド `options_source` |
| `deploy.NativePlanner` | 一括デプロイ前にサイト/オブジェクトを読み取る | `deploy.MarkNativePlannable("example")` |

開発手順:

1. `providers/deploy/<id>/plugin.go` を作成します。
2. `catalog/deploy/<id>.json` を作成し、`fields` と `deploy_fields` を含めます。
3. `i18n/providers/deploy/<id>/zh-CN.json` と `en-US.json` を作成します。
4. `make manifest` を実行して `tools/helpers/deploy-helper/imports_gen.go` を生成し、`httptest` の単体テストを追加します。

セキュリティ規約:

- ユーザー制御の URL には `outbound.ValidateURL` / `outbound.ValidateWebhookURL` を使用し、アウトバウンドリクエストには `outbound.NewClient` を使用してください。`http.DefaultClient` を直接使用しないでください。
- `Verify` / `Plan` / `ListOptions` は証明書の書き込み、永続オブジェクトの作成、reload を行ってはなりません。エラーメッセージに token/secret を含めないでください。過去の ID は `catalog/migrations.json` の `deprecated_deploy_types` と `deploy_migrations` で正規ターゲットにマッピングされます。

## 通知チャンネルの開発

場所: `providers/notify/channels/channel_<id>.go`

```go
package channels

func init() {
    registerChannel(newChannel("example", []notify.NativeField{
        text("url", "notifications.fieldServerUrl", true),
        password("token", "notifications.fieldToken", true),
    }, sendExample))
}

func sendExample(cfg map[string]any, msg notify.NativeMessage) error { ... }
```

フィールドコンストラクター:

| 関数 | 用途 |
|---|---|
| `text` | テキスト |
| `password` | パスワード/キー |
| `number` | 数値 |
| `selectField` + `opt` / `textarea` | ドロップダウン、複数行テキスト |

各チャンネルは独立したファイルで、`init` により自動登録され、共有リストを変更する必要はありません。送信ロジックは `notifyJSON` / `notifyForm` / `notifyHTTP` と `outbound` を優先的に使用します。

## ビルド

```bash
# すべての統合パッケージ
make bundle

# DNS 単一バリアント
make bundle-lego
make bundle-native
make bundle-lego-v5
```

ビルドパラメータ:

```bash
CGO_ENABLED=0 go build -trimpath \
  -tags "bundle_dns_all_native,nethttpomithttp2,http2legacy" \
  -ldflags "-s -w -X main.buildVersion=<version>" \
  -o lactssl-dns-all-native ./tools/helpers/dns-helper
```

- `-trimpath` はローカルパスを除去し、`-s -w` はシンボルと DWARF を除去します。
- `bundle_dns_all_lego` / `bundle_dns_all_native` は DNS グループを選択します。
- `nethttpomithttp2,http2legacy` は Go 1.27 の `net/http` と新しい `x/net/http2` の互換性問題を回避します。lego v5 はさらに `urfave_cli_no_template,ozzo_validation_no_template,grpcnotrace,validator_novalidatefn` を使用してテンプレートコードを削減します。

## パッケージングとインデックス

単一パッケージのビルド:

```bash
make plugin KIND=dns ID=all-native GOOS=linux GOARCH=amd64
```

成果物:

```text
dist/
├── lactssl-dns-all-native_<version>_linux_amd64.tar.gz
└── lactssl-dns-all-native_<version>_linux_amd64.meta.json
```

パッケージ内容:

```text
plugin.json
lactssl-dns-all-native
LICENSE
icons/<provider>.<svg|png|webp>
```

`plugin.json` にはバージョン、プロトコル、bundle、providers、variant、conflicts、sdk、アイコン参照が含まれます。メインプログラムは署名付きプラグインインデックスを使用しません。

### 署名

リリースは `checksums.txt` と Ed25519 署名で検証し、アップロード helper は manifest の `sha256` とインストール記録で検証します。プラグインインデックスを生成・公開する必要はありません。

## バージョン戦略

- `all-lego` / `all-lego-v5` のバージョンは `go.mod` の lego バージョンに従います。`all-native`、`deploy/all`、`notify/all` のパッチ番号はコンテンツフィンガープリントにより自動インクリメントされます。
- 共通入力（`protocol/`、`sdk/`、`i18n/`、`outbound/`、`catalog/`、`go.mod`）の変更はすべての関連パッケージのバージョンアップをトリガーし、「同一バージョンで内容が異なる」状態を避けます。provider 自身のソース変更は、対応するタイプのパッケージにのみ影響します。リリースは tag を基準とし、tag 以外の手動リリースは内部バージョンを使用します。公開済み tag バージョンとの前後関係に注意してください。

## コラボレーションフロー

### ブランチと PR

```text
main
  ↑
短命ブランチ（1～3 日）
  ↑
PR → lint / generated / test / bundle / native → CODEOWNER review
  → merge queue → CI gate → マージ
  → after-merge bot が生成物を更新
```

要件:

- `main` への直接 push は禁止です。PR は必須で、`CI gate` を通過する必要があります。
- Merge Queue を使用し、単一 provider / 単一ターゲット / 単一チャンネルにつき 1 つの PR とします。生成物は bot が書き戻すため、コントリビューターは生成物の競合を手動で解決しないでください。

### CODEOWNERS

プラグインコードベースの `.github/CODEOWNERS` がデフォルト owner を定義します。チーム拡大後は DNS native、lego、deploy、notify、infra の area ごとに GitHub Team へ置き換えます。protocol / sdk / catalog schema / CI はコアメンテナーのレビューが必要です。

### 階層型 CI

| レイヤー | トリガー | 内容 |
|---|---|---|
| lint / generated | 各 PR / merge queue | gofmt、go.mod/go.sum、`make manifest` + 生成物検証 |
| test | Go の変更 | `go vet ./...`、`go test ./...` |
| bundle | DNS/deploy/notify の変更 | 5 つの helper のビルド、マニフェストの数/一貫性、パッケージング smoke |
| native / nightly | DNS/SDK の変更、毎日 | `check_native.sh`、`go test -race`、全体ビルド、govulncheck |

ブランチ保護では **CI gate** の 1 つのチェックのみを必須にすれば十分です。条件付き job がスキップされても gate は通過します。

### Merge Queue

`main` を保護し、Require status checks に `CI gate`、Require linear history、Merge Queue（最大同時実行 5、待機タイムアウト 5 分）、直接 push の禁止、「Require branches to be up to date before merging」を設定します。

## ローカルデバッグ

```bash
# 生成
make manifest

# native DNS マニフェスト
go run -tags bundle_dns_all_native ./tools/helpers/dns-helper manifest | jq '.providers | length'

# 単一 provider を表示
go run -tags bundle_dns_all_native ./tools/helpers/dns-helper manifest \
  | jq '.providers[] | select(.id=="linode")'

# デプロイ
LACTSSL_DEPLOY_PLUGIN_FEATURES=merged-deployers-v1,verify-v1,plan-v1,dynamic-options-v1 \
  go run ./tools/helpers/deploy-helper manifest | jq

# 通知
go run ./tools/helpers/notify-helper manifest | jq '.channels | length'
```

リクエスト例は[DNS プラグイン](dns-plugins.md)の `present` / `cleanup` の stdin を参照してください。

## テスト要件

- 単体テストは `httptest` を使用し、実際のネットワークへアクセスしません。DNS のカバレッジ: 書き込み値、クリーンアップが今回の値のみ削除、レコード不存在の冪等性、一致する zone がない場合。デプロイ: 成功、認証失敗、Verify/Plan が書き込まないこと。通知: 設定欠落、リクエストボディ、エラーステータスコード。
- マニフェストの数とフィールドは CI がアサートします。native パッケージは追加で `./scripts/check_native.sh` を実行します。

## コミット前チェック

```text
[ ] 自分の provider ディレクトリ + 対応する catalog/i18n のみを変更
[ ] init で登録され、ID/alias が一意
[ ] catalog フィールドの key とコードが一致
[ ] 中国語・英語の文言を両方補完
[ ] 単体テストは httptest を使用
[ ] make manifest
[ ] gofmt / go vet / go test
[ ] check_native（native）
[ ] 生成物をコミット済み、または bot の書き戻しを待機
```
