# DNS プラグイン

DNS プラグインは、パブリック CA の DNS-01 検証用にドメインサービスプロバイダーの API 接続を提供します。企業が選定する際に重要なのは「何社をサポートするか」ではなく、**認証情報の最小権限、ローテーション可能性、監査可能性、オフラインデプロイ可能性**です。

## 1. 利用前提

- 「DNS アカウント」で対応プロバイダーの認証情報を作成済みであること。
- 「プラグイン」ページで DNS プラグインと具体的なプロバイダーをインストールし、有効化済みであること。
- ドメインが属する Zone がその認証情報から見え、TXT レコードの作成/削除が許可されていること。
- アウトバウンドでプロバイダー API にアクセスできること。プロキシ経由の場合は、Go HTTP クライアントのプロキシ設定を確認してください。

DNS プラグインが 1 つもインストールされていない場合、DNS プロバイダー一覧は空です。過去の DNS アカウントは失われませんが、プロバイダーが利用できない場合は発行時に明確なエラーが表示されます。

## 2. プラグインパッケージの選択

公式リポジトリ [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins) は、全約 194 社のプロバイダー向けに 3 つの DNS パッケージを提供しています:

| パッケージ | 対象 | 展開後 | 用途 |
| --- | --- | ---: | --- |
| DNS 全量・再構築 | 194 プロバイダー | 約 18 MiB | ほとんどの企業。21 社はリポジトリの native 実装、残りは lego v4 を使用 |
| DNS 全量・lego | 194 プロバイダー | 約 58 MiB | 完全な lego v4 機能が必要な場合（例: rfc2136 GSS-TSIG） |
| DNS 全量・lego v5 | 235 エントリ（228 の v5 provider + 7 の互換プレースホルダー） | 約 37 MiB | lego v5 と新規 provider を追跡 |

3 つのパッケージは排他関係にあり、同時にインストールできません。

- 読み取り専用検証（`verify-v1`）は「DNS 全量・再構築」パッケージでのみ native 実装により提供されます。他のパッケージを使用する場合、`auto` モードは自動的に TXT 書き込みテストへフォールバックします。
- 各パッケージは 1 つの Go ランタイムを共有し、プロバイダーごとにパッケージ化する場合のディスクとプロセスのオーバーヘッドを避けます。
- メインプログラムのリリースパッケージにはプロバイダーが含まれません。必要に応じてインストールします。
- プロバイダーのアイコンはプラグインパッケージとリポジトリインデックスで配信され、インストール後は `<data_dir>/plugin-icons/` にキャッシュされます。

## 3. インストールと有効化

1. 「プラグイン」ページを開き、リポジトリ URL を確認して「インデックスを更新」をクリックします。
2. 「プラグインマーケット」を開き、DNS で絞り込み、必要なパッケージをインストールします。
3. まずプラグインファイルを有効化し、次に DNS プロバイダー一覧で具体的なプロバイダーを有効化します。新しくダウンロードしたファイルとエントリはデフォルトで無効です。
4. 「DNS アカウント」で認証情報を作成し、そのプロバイダーを選択します。

統合パッケージは行頭のスイッチで一度にすべて有効化されるわけではありません。一度に 10 を超えるエントリを有効化する場合は確認が求められます。ファイルレベルで無効化すると、その中のエントリはランタイムに登録されませんが、既存のチェックは保持されます。

インストール、更新、アンインストールにはログインセッションまたは `plugins:write` のみが必要です。プラグインパッケージはインデックス署名（公開キーが設定されている場合）、asset の SHA-256、`plugin.json` とマニフェスト内のプロバイダー ID を検証し、いずれかが一致しない場合はインストールを拒否してロールバックします。

## 4. 認証情報のガバナンス

### 4.1 最小権限

- 専用のサブアカウントまたは API Token を使用し、個人アカウントやグローバル管理者キーを使用しないでください。
- 対象 Zone の DNS 読み書き権限のみを付与してください。アカウントレベルの支払い、ドメイン登録、Zone 削除などの権限は付与しないでください。
- プロバイダーがドメイン/プロジェクト単位の認可をサポートしている場合は、実際に発行が必要なドメインに制限してください。
- テストと本番では異なる認証情報と異なる DNS サブアカウントを使用してください。

### 4.2 ローテーション

- 認証情報の責任者、作成日時、権限範囲、ローテーション周期を記録してください。
- ローテーション時は、まず LACTSSL で認証情報を更新し、その後プロバイダー側で古い認証情報を失効させてください。
- ローテーション後にテストタスクを実行し、TXT の作成/クリーンアップが正常であることを確認してください。
- 担当者の退職、キー漏洩、プロバイダーのセキュリティインシデント後は直ちにローテーションしてください。

### 4.3 保管

- 認証情報はデータベースへ書き込む前に AES-256-GCM で暗号化され、API レスポンスではマスクされます。
- アカウント編集時にキーフィールドを空にすると、元の値を保持することを意味します。
- 認証情報をタスク名、備考、チケット、ログに書き込まないでください。
- バックアップパッケージには暗号化された認証情報が含まれるため、機密ファイルとして管理してください。

## 5. 一般的なプロバイダー

| プロバイダー | 主な認証情報 | 備考 |
| --- | --- | --- |
| Alibaba Cloud DNS（alidns） | AccessKey ID / Secret | Region は任意 |
| Alibaba Cloud ESA（aliesa） | AccessKey ID / Secret | Region は任意 |
| Cloudflare | API Token、または Email + Global API Key | API アップストリームアドレスは任意 |
| DNSPod | API Token | |
| Tencent Cloud（tencentcloud） | SecretId / SecretKey | Region は任意 |
| Huawei Cloud（huaweicloud） | AccessKey ID / SecretAccessKey / Region | |
| Volcengine（volcengine） | AccessKey / SecretKey | Region、API ホストとプロトコルは任意 |
| Baidu AI Cloud（baiducloud） | AccessKey ID / SecretAccessKey | |
| JD Cloud（jdcloud） | AccessKey ID / AccessKey Secret | Region は任意 |
| AWS Route 53 | AccessKey / SecretAccessKey | Region、Hosted Zone、プライベートゾーンは任意 |
| Google Cloud DNS | サービスアカウント JSON | プロジェクト ID は任意 |
| Azure DNS | Tenant ID / Client ID / Client Secret / Subscription ID / リソースグループ | |
| DigitalOcean / Vultr / Linode / Hetzner | API Token / API Key | |
| Gandi / GoDaddy / Namecheap / NameSilo / Porkbun | 各プロバイダーの API 認証情報 | |
| PowerDNS（pdns） | API アドレス / API Key | Server 名と API バージョンは任意 |
| RFC 2136 動的更新 | DNS サーバー | TSIG Key / Secret / アルゴリズムと Zone リストは任意 |

- 公式 DNS プラグインをインストールすると、lego のすべてのプロバイダー（約 190 社）をサポートします。
- `manual` プロバイダーは TXT レコードを手動で追加する必要があり、自動フローには含まれません。
- Cloudflare、DNSimple、PowerDNS、Volcengine などはカスタム API アップストリームアドレスをサポートし、プロキシ、セルフホストサービス、互換ゲートウェイに使用できます。
- 完全なプロバイダー一覧は「DNS アカウント」ページのドロップダウンを基準とします。

## 6. オフラインと手動インストール

- 「プラグイン」ページで単一の helper 実行ファイルをアップロードできます。最大 256 MB です。
- アップロード後、一時ファイルとして `manifest` を検査し、プロトコル、プロバイダー ID、SHA-256 を検証します。通過後にのみプラグインディレクトリへ書き込み、再スキャンします。
- デフォルトでは同名ファイルの上書きを拒否します。アップグレード時は「同名プラグインの上書きを許可」をチェックすると、旧ファイルは先にバックアップされます。
- または、`plugins.repo.url` を内部ミラーインデックスに向けます:

```yaml
plugins:
  repo:
    url: "file:///srv/lactssl-plugins/index.json"
```

手動アップロードしたマルチプロバイダー bundle は引き続きロードできますが、`manual` とマークされ、公式リポジトリのアップグレード対象外になります。

## 7. 検証とトラブルシューティング

### 7.1 検証

1. DNS アカウントを作成し、保存に成功します。
2. テストタスクを作成し、そのアカウントとテストドメインを選択します。
3. タスクを実行し、TXT 作成、CA 検証、TXT クリーンアップを観察します。
4. タスクログと操作ログを確認します。
5. テスト証明書を削除するか、必要に応じて保持します。

### 7.2 よくある問題

| 現象 | 原因 | 対処 |
| --- | --- | --- |
| プロバイダーのドロップダウンが空 | プラグインが未インストール/未有効化 | DNS パッケージをインストールし、プロバイダーを有効化 |
| 認証情報の保存に失敗 | 必須フィールドの欠落/形式エラー | フォームとログのヒントに従って修正 |
| TXT の作成に失敗 | 権限/Zone/クォータ | API 権限、ドメイン、クォータを確認 |
| TXT のクリーンアップに失敗 | 権限、またはレコードが既に削除済み | 残留 TXT を手動でクリーンアップ |
| 検証タイムアウト | DNS 伝播が遅い/権威 DNS が未更新 | TTL、権威解決、CAA を確認 |
| カスタム API アドレスの失敗 | アドレスに到達できない/証明書が信頼されていない | ネットワーク、TLS、アップストリームアドレスを確認 |
| プラグインのロードに失敗 | 権限/ダイジェスト/プロトコル | ディレクトリ権限とインストール記録を確認 |

## 8. Helper プロトコル v1（連携開発）

このセクションはプラグイン開発者向けです。運用担当者は通常、理解する必要はありません。

helper は実行ファイルで、4 つのサブコマンドをサポートします:

```bash
dns-helper manifest   # JSON マニフェストを出力
dns-helper present    # stdin からリクエストを読み取り、TXT レコードを追加
dns-helper cleanup    # stdin からリクエストを読み取り、TXT レコードを削除
dns-helper verify     # stdin からリクエストを読み取り、読み取り専用プローブを実行（任意）
```

`manifest` 出力例:

```json
{
  "protocol": "v1",
  "capabilities": ["verify-v1"],
  "name": "example dns helper",
  "version": "1.0.0",
  "sha256": "省略可能な helper ファイルのチェックサム",
  "providers": [
    {
      "id": "example-dns",
      "name": "Example DNS",
      "description": "サンプルプロバイダー",
      "aliases": ["example"],
      "capabilities": ["verify-v1"],
      "fields": [
        {"key": "api_token", "label": "API Token", "type": "password", "required": true}
      ]
    }
  ]
}
```

`present` / `cleanup` の stdin:

```json
{
  "action": "present",
  "provider": "example-dns",
  "config": {"api_token": "..."},
  "domain": "example.com",
  "token": "acme-token",
  "key_auth": "key-authorization"
}
```

`verify` の stdin / stdout:

```json
{"action": "verify", "provider": "example-dns", "config": {"api_token": "..."}, "domain": "example.com", "mode": "read", "locale": "zh-CN"}
```

```json
{"ok": true, "checks": [{"key": "zone", "status": "ok", "message": "example.com"}]}
```

規約:

- 終了コード `0` は成功、非 `0` は失敗を表します。stderr はタスクログに入ります（機密値はマスクされます）。
- stdout は空のままにします。`present/cleanup` の出力はエラーとみなされます。
- `verify` は読み取り専用操作でなければなりません。zone の列挙とレコードの照会のみが許可され、レコードの作成、変更、削除はできません。
- `verify-v1` 機能を宣言したプロバイダーのみが `verify` 呼び出しを受け取ります。宣言していないプロバイダーは、メインプログラムが `present/cleanup` の書き込みテストにフォールバックします（`mode=auto`）。明示的な `mode=read` の場合は未サポートを返します。
- `checks[].status` の値: `ok`、`failed`、`skipped`、`unsupported`、`error`。
- helper は `LACTSSL_PLUGIN_MEMORY_LIMIT`（バイト）からメモリ予算を取得します。
- メインプログラムは helper の `VmRSS` を定期的に確認し、`plugins.memory_limit_mb` を超えるとプロセスを終了してエラーを記録します。
- メインプログラムは最小限の環境変数セット（PATH、HOME、プロキシ、TLS 証明書など）のみを渡し、サーバー側のキーを漏洩しません。
- helper ファイルは他のユーザーに書き込みを許可してはなりません。`manifest.sha256` が空でない場合はファイルダイジェストを検証します。
- `version` は helper/プロバイダー実装自体のバージョンを表し、LACTSSL のビルドバージョンではありません。

## 9. セキュリティ推奨事項

- プラグインディレクトリはサービスの実行ユーザーのみが書き込み可能にし、`0700` を推奨します。
- 信頼できるソースの helper のみをインストールし、マニフェストに `sha256` を記入してください。
- 適切な `memory_limit_mb` と `timeout_seconds` を設定し、異常なプラグインがホストを停止させないようにしてください。
- プラグインはサービスアカウントで実行し、`lactssl` ユーザーに追加権限を与えないでください。
- 本番環境では内部ミラーを使用し、バージョンを固定し、自動更新を無効にしてください。

## 関連ドキュメント

- [プラグインガバナンス](plugins.md)
- [機能一覧](features.md)
- [デプロイプラグイン](deploy-plugins.md)
- [通知プラグイン](notify-plugins.md)
