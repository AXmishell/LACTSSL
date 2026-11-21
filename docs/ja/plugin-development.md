# 拡張開発：プラグインプロトコルとコントリビューションガイド

本文はプラグイン開発者、インテグレーター、企業プラットフォームチーム向けで、LACTSSL のプラグインプロトコル、リポジトリ構造、開発フロー、ビルド、パッケージング、インデックス、署名、リリースについて説明します。**運用担当者は通常、[プラグインガバナンス](plugins.md)と対応する DNS/デプロイ/通知プラグインのページのみを読めば十分です。**

プラグインのソース、ビルドツール、リポジトリインデックスは独立リポジトリ [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins) にあります。メインプログラムはプロトコル v1 のマニフェストデコードのみを実装し、実行時にプラグインインデックスからタイプ別に統合されたプラグインパッケージをダウンロードします。2 つのリポジトリは `go.mod` でソースを共有しません。

## 1. アーキテクチャとメンタルモデル

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
| `cmd/dns-helper` | DNS（v4 / native） | `manifest` / `present` / `cleanup` / `verify` |
| `cmd/dns-helper-v5` | DNS（lego v5） | `manifest` / `present` / `cleanup` / `verify` |
| `cmd/deploy-helper` | 証明書デプロイ | `manifest` / `deploy` / `options` / `plan` / `verify` |
| `cmd/notify-helper` | 通知 | `manifest` / `send` |

プラグインリポジトリは**モジュラーモノリス**を採用しています:

- 各 provider に 1 つのディレクトリ。
- メタデータ、文言、アイコンは provider ごとに分割。
- 集約ファイルは `make manifest` で生成し、手動編集は禁止。
- 複数のコントリビューターが並行開発でき、単一ファイルの競合を減らせます。

## 2. プロトコル v1

プロトコルは `v1` のままです。破壊的変更がある場合のみ `v2` に上げます。新機能は任意フィールドと機能フラグで拡張し、古いホストは認識できないフィールドを無視します。

### 2.1 サブコマンドと出力

DNS:

```text
dns-plugin manifest
dns-plugin present   # stdin JSON
dns-plugin cleanup   # stdin JSON
dns-plugin verify    # stdin JSON
```

デプロイ:

```text
deploy-plugin manifest
deploy-plugin deploy
deploy-plugin options
deploy-plugin plan
deploy-plugin verify
```

通知:

```text
notify-plugin manifest
notify-plugin send
```

規約:

- 終了コード `0` は成功、非 `0` は失敗。stderr はメインプログラムのログに入ります。
- DNS の `present` / `cleanup` の stdout は空でなければなりません。
- デプロイの `deploy` は成功時に stdout が空です。`verify` / `options` / `plan` は JSON を出力します。
- 通知の `send` は成功時に 1 行の `{"ok":true,"message":""}` を出力します。失敗時も `{"ok":false,...}` を出力し、非 0 で終了します。
- リクエストとレスポンスは UTF-8 JSON を使用し、非 ASCII はエスケープしません。

### 2.2 環境変数

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
| `dynamic-options-v1` | `options` サブコマンドと `options_source` / `multiple` をサポート |
| `verify-v1` | `verify` の読み取り専用プローブをサポート |
| `plan-v1` | `plan` の事前マッチをサポート |

### 2.3 セキュリティ規約

- 秘密キー、token は stdin 経由でのみ渡し、argv や環境変数には書き込みません。
- メインプログラムは最小限の環境変数セットのみを渡します。
- helper は実行可能で、グループまたは他のユーザーに書き込みを許可してはなりません。シンボリックリンクは拒否されます。
- メインプログラムは `VmRSS` をポーリングし、`memory_limit_mb` を超えるとプロセスグループ全体を終了します。
- `manifest.sha256` は空でもかまいません。リポジトリインストールではインデックス asset の SHA-256 とインストール記録で完全性を保証します。

## 3. リポジトリ構造

```text
protocol/                    プロトコル v1 のワイヤフォーマット
sdk/                         共有 HTTP、署名、DNS ユーティリティ、helper フレームワーク
outbound/                    SSRF 対策済みアウトバウンド HTTP クライアント
catalog/
  dns/<id>.json              単一 DNS provider のメタデータ（手書き）
  deploy/<id>.json           単一デプロイターゲットのメタデータ（手書き）
  migrations.json            過去のデプロイ ID とマイグレーション
  data.json                  生成物: 集約メタデータ
i18n/
  core/<locale>.json         共通文言
  providers/<kind>/<id>/     単一 provider の文言
  locales/<locale>.json      生成物: 集約文言
dns/providers/<id>/          lego v4 ラッパー（生成）
dns/native/providers/<id>/   native DNS 実装（手書き）
dns/groups/                  生成物: DNS グループ
dnsv5/                       lego v5 ラッパー、グループ、メタデータ（生成）
deploy/providers/<id>/       デプロイターゲット（手書き）
notify/providers/channels/   チャンネルごとに channel_<id>.go
cmd/                         helper エントリ
assets/icons/                アイコン
plugins.json                 生成物: 統合パッケージマニフェスト
scripts/                     ジェネレーターとビルドツール
```

生成物:

```text
catalog/data.json
i18n/locales/*.json
cmd/dns-helper/group_*.go
cmd/deploy-helper/imports_gen.go
dns/groups/**
dnsv5/**
plugins.json
```

ソースファイルを変更した後は次を実行します:

```bash
make manifest
```

このコマンドは `catalog`、`i18n`、`deployimports`、`gen-v5` を順に実行し、その後 `plugins.json` とグループを生成します。CI は生成物の漏れを検証します。同一リポジトリの PR では `Update generated files` bot が自動的に書き戻します。

## 4. DNS provider の開発

### 4.1 Native 実装

場所: `dns/native/providers/<id>/provider.go`

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
    // Verify を実装した場合のみ宣言
    // dns.MarkVerifiable("example")
}
```

`ChallengeProvider` には 2 つのメソッドのみがあります:

```go
Present(domain, token, keyAuth string) error
CleanUp(domain, token, keyAuth string) error
```

DNS-01 の必須ルール:

- DNS に書き込む値は `dnsutil.TXTValue(keyAuth)` であり、`keyAuth` の原文ではありません。
- ドメインは `dnsutil.ChallengeFQDN(domain)` を使用します。
- zone マッチには `dnsutil.ZoneName`、相対名には `dnsutil.RelativeName` を使用します。
- `CleanUp` は今回の TXT 値のみを削除し、他の値は保持します。レコードが存在しない場合は成功とみなします。
- TTL のデフォルトは lego と一致させます（一般に 60s）。

任意の読み取り専用検証:

```go
func (p *provider) Verify(ctx context.Context, domain string) (string, error)
```

実装後は `dns.MarkVerifiable("example")` を忘れないでください。そうしないとホストは `verify` を呼び出しません。

### 4.2 lego v4 ラッパー

`dns/providers/<id>` はジェネレーターが生成します:

```bash
go run scripts/gen_dns_plugins/main.go
make manifest
```

生成ファイルを手動で変更しないでください。エイリアス、フィールド、環境変数を調整する場合は、ジェネレーターまたは `catalog/dns/<id>.json` を変更します。

### 4.3 lego v5

```bash
make gen-v5
make manifest
```

ジェネレーターは `dnsv5/providers`、`dnsv5/legacy`、`dnsv5/groups`、`dnsv5/meta` を書き換えます。v5 で追加された provider のフィールドはアップストリームの `.toml` に由来します。catalog に同名エントリがある場合は既存のフィールドを引き継ぎます。

### 4.4 メタデータと文言

`catalog/dns/<id>.json` の例:

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

`env` は lego ラッパーにのみ有効です。native 実装は `cfg["token"]` を直接読み取ります。

文言:

```text
i18n/providers/dns/<id>/zh-CN.json
i18n/providers/dns/<id>/en-US.json
```

共通フィールドの文言は `i18n/core/<locale>.json` に置きます。翻訳が不足する場合はデフォルト言語へ、さらに `label` / `value` へフォールバックします。

## 5. デプロイターゲットの開発

場所: `deploy/providers/<id>/plugin.go`

```go
type plugin struct{}

func (plugin) ID() string        { return "example" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(ctx context.Context, req deploy.Request) error { ... }

func init() { deploy.Register(plugin{}) }
```

任意機能:

| インターフェース | 用途 | 宣言 |
|---|---|---|
| `deploy.Verifier` | 認証情報/ターゲットを読み取り専用で検証 | `deploy.MarkVerifiable("example")` |
| `deploy.OptionLister` | 動的ドロップダウンオプション | catalog フィールド `options_source` |
| `deploy.Planner` | 一括デプロイ前にサイト/オブジェクトを読み取る | `deploy.MarkPlannable("example")` |

開発手順:

1. `deploy/providers/<id>/plugin.go` を作成します。
2. `catalog/deploy/<id>.json` を作成し、`fields` と `deploy_fields` を含めます。
3. `i18n/providers/deploy/<id>/zh-CN.json` と `en-US.json` を作成します。
4. `make manifest` を実行して `cmd/deploy-helper/imports_gen.go` を生成します。
5. `httptest` の単体テストを追加します。

セキュリティ規約:

- ユーザー制御の URL には `outbound.ValidateURL` / `outbound.ValidateWebhookURL` を使用する必要があります。
- アウトバウンドリクエストには `outbound.NewClient` を使用し、`http.DefaultClient` を直接使用しないでください。
- `Verify` / `Plan` / `ListOptions` は証明書の書き込み、永続オブジェクトの作成、reload を行ってはなりません。
- エラーメッセージに token/secret を含めないでください。

過去の ID は `catalog/migrations.json` の `deprecated_deploy_types` と `deploy_migrations` で正規ターゲットにマッピングされます。

## 6. 通知チャンネルの開発

場所: `notify/providers/channels/channel_<id>.go`

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

フィールドコンストラクター:

| 関数 | 用途 |
|---|---|
| `text` | テキスト |
| `password` | パスワード/キー |
| `number` | 数値 |
| `selectField` + `opt` | ドロップダウン |
| `textarea` | 複数行テキスト |

各チャンネルは独立したファイルで、`init` により自動登録され、共有リストを変更する必要はありません。送信ロジックは `notifyJSON` / `notifyForm` / `notifyHTTP` と `outbound` を優先的に使用します。

## 7. ビルド

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
  -o lactssl-dns-all-native ./cmd/dns-helper
```

- `-trimpath` はローカルパスを除去します。
- `-s -w` はシンボルと DWARF を除去します。
- `bundle_dns_all_lego` / `bundle_dns_all_native` は DNS グループを選択します。
- `nethttpomithttp2,http2legacy` は Go 1.27 の `net/http` と新しい `x/net/http2` の互換性問題を回避します。
- lego v5 はさらに `urfave_cli_no_template,ozzo_validation_no_template,grpcnotrace,validator_novalidatefn` を使用してテンプレートコードを削減します。

## 8. パッケージングとインデックス

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

`plugin.json` にはバージョン、プロトコル、bundle、providers、variant、conflicts、sdk、アイコン参照が含まれます。インデックス:

```bash
make index BASE_URL=https://github.com/AXmishell/LACTSSL-Plugins/releases/download
```

インデックスフィールド:

- `schema` / `channel` / `protocol` / `generated_at`。
- `plugins[]`: kind、id、version、bundle、providers、variant_of、conflicts_with、sdk、assets。
- `assets[GOOS/GOARCH]`: url、sha256、size、uncompressed_size。
- ルートレベルの `icons[]`: インライン base64 アイコン。未インストール時の表示に使用。

### 8.1 署名

```bash
go run scripts/pluginctl/main.go sign -key key.b64 -in index.json -out index.json.sig
```

`key.b64` は base64 エンコードされた Ed25519 秘密キー（32 バイトのシードまたは 64 バイトの秘密キー）です。本番リリースでは `PLUGIN_INDEX_SIGNING_KEY` の設定を必須にすべきで、欠落時に未署名インデックスを公開すべきではありません。

## 9. バージョン戦略

- `all-lego` / `all-lego-v5` のバージョンは `go.mod` の lego バージョンに従います。
- `all-native`、`deploy/all`、`notify/all` のパッチ番号はコンテンツフィンガープリントにより自動インクリメントされます。
- 共通入力（`protocol/`、`sdk/`、`i18n/`、`outbound/`、`catalog/`、`go.mod`）の変更は、すべての関連パッケージのバージョンアップをトリガーし、「同一バージョンで内容が異なる」状態を避けます。
- provider 自身のソース変更は、対応するタイプのパッケージにのみ影響します。
- リリースは tag を基準とし、tag 以外の手動リリースは内部バージョンを使用します。公開済み tag バージョンとの前後関係に注意が必要です。

## 10. コラボレーションフロー

### 10.1 ブランチと PR

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

- `main` への直接 push は禁止。
- PR は必須で、`CI gate` を通過する必要があります。
- Merge Queue を使用します。
- 単一 provider / 単一ターゲット / 単一チャンネルにつき 1 つの PR とします。
- 生成物は bot が書き戻すため、コントリビューターは生成物の競合を手動で解決しないでください。

### 10.2 CODEOWNERS

プラグインリポジトリの `.github/CODEOWNERS` がデフォルト owner を定義します。チーム拡大後は area ごとに GitHub Team へ置き換えます:

- DNS native、lego、deploy、notify、infra。
- protocol / sdk / catalog schema / CI はコアメンテナーのレビューが必要です。

### 10.3 階層型 CI

| レイヤー | トリガー | 内容 |
|---|---|---|
| lint | 各 PR / merge queue | gofmt、go.mod/go.sum |
| generated | 各 PR / merge queue | `make manifest` + 生成物検証 |
| test | Go の変更 | `go vet ./...`、`go test ./...` |
| bundle | DNS/deploy/notify の変更 | 5 つの helper のビルド、マニフェストの数/一貫性、パッケージング smoke |
| native | DNS/SDK の変更 | `check_native.sh` |
| nightly | 毎日 | `go test -race`、全体ビルド、govulncheck |

ブランチ保護では **CI gate** の 1 つのチェックのみを必須にすれば十分です。条件付き job がスキップされても gate は通過します。

### 10.4 Merge Queue

リポジトリ設定:

- `main` を保護。
- Require status checks: `CI gate`。
- Require linear history。
- Merge Queue を有効化（最大同時実行 5、待機タイムアウト 5 分）。
- 直接 push を禁止。
- 「Require branches to be up to date before merging」を有効化。

## 11. ローカルデバッグ

```bash
# 生成
make manifest

# native DNS マニフェスト
go run -tags bundle_dns_all_native ./cmd/dns-helper manifest | jq '.providers | length'

# 単一 provider を表示
go run -tags bundle_dns_all_native ./cmd/dns-helper manifest \
  | jq '.providers[] | select(.id=="linode")'

# デプロイ
LACTSSL_DEPLOY_PLUGIN_FEATURES=merged-deployers-v1,verify-v1,plan-v1,dynamic-options-v1 \
  go run ./cmd/deploy-helper manifest | jq

# 通知
go run ./cmd/notify-helper manifest | jq '.channels | length'
```

リクエスト例:

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

## 12. テスト要件

- 単体テストは `httptest` を使用し、実際のネットワークへアクセスしません。
- DNS のカバレッジ: 書き込み値、クリーンアップが今回の値のみ削除、レコード不存在の冪等性、一致する zone がない場合。
- デプロイのカバレッジ: 成功、認証失敗、Verify/Plan が書き込まないこと。
- 通知のカバレッジ: 設定欠落、リクエストボディ、エラーステータスコード。
- マニフェストの数とフィールドは CI がアサートします。
- native パッケージは追加で `./scripts/check_native.sh` を実行します。

## 13. コミット前チェック

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

## 14. 関連ドキュメント

- [プラグインガバナンス](plugins.md)
- [DNS プラグイン](dns-plugins.md)
- [デプロイプラグイン](deploy-plugins.md)
- [通知プラグイン](notify-plugins.md)
- [プラグインリポジトリのコントリビューションガイド](https://github.com/AXmishell/LACTSSL-Plugins/blob/main/CONTRIBUTING.md)
