# DNS プラグイン

DNS プラグインは、パブリック CA の DNS-01 検証用にドメインサービスプロバイダーの API 接続を提供します。認証情報の最小権限、ローテーション、監査、オフラインデプロイを重視してください。

## 利用前提

- 「DNS アカウント」で利用するプロバイダーの認証情報を作成しておきます。
- 「プラグイン」ページで DNS カテゴリを有効にし、必要なプロバイダーを有効化しておきます（既定はすべて無効）。
- 認証情報から対象 Zone が見え、TXT レコードの作成/削除が許可されていること。
- アウトバウンドでプロバイダー API に到達できること。プロキシ経由の場合は Go HTTP クライアントのプロキシ設定を確認します。

## プラグインパッケージの選択

公式 DNS 実装は本体に組み込まれており、既定のフルビルドは約 235 エントリ（228 の lego v5 provider + 7 の互換 ID）をカバーするため、プラグインパッケージのインストールは不要です。すべてのサービスプロバイダーは既定で無効のため、「プラグイン」ページで必要なものだけを有効化してください。

より小さなバイナリやメモリが必要な場合は簡易ビルドを使用します。`make backend-dns-common` は常用ホワイトリストのみをリンクし、`make backend-dns-minimal` は Cloudflare のみを残し、`make backend-no-dns` は組み込み DNS を完全にコンパイルしません。簡易ビルドでも helper のアップロードは可能です。

## インストールと有効化

1. 「プラグイン」ページを開き、DNS カテゴリが有効であることを確認します。
2. DNS サービスプロバイダー一覧で必要なプロバイダーを有効化します。一度に 10 を超えるエントリを有効化する場合は確認が求められます。
3. 「DNS アカウント」で認証情報を作成し、そのプロバイダーを選択します。

有効状態は settings テーブルに保存されます。カテゴリを閉じるとエントリはランタイムに登録されませんが、既存のチェックは保持されます。

## 認証情報のガバナンス

### 最小権限

- 専用のサブアカウントまたは API Token を使用し、対象 Zone の DNS 読み書き権限のみを付与してください。個人アカウント、グローバル管理者キー、支払い・ドメイン登録・Zone 削除の権限は付与しません。
- プロバイダーがドメイン/プロジェクト単位の認可に対応する場合は、発行対象のドメインに限定してください。テストと本番は別の認証情報を使用します。

### ローテーション

- 認証情報の責任者、作成日時、権限範囲、ローテーション周期を記録してください。
- ローテーション時は LACTSSL 側を先に更新し、その後プロバイダー側で古い認証情報を失効させ、テストタスクで TXT の作成/クリーンアップを確認します。退職、漏洩、プロバイダーのインシデント後は直ちにローテーションします。

### 保管

- 認証情報はデータベースへ書き込む前に AES-256-GCM で暗号化され、API レスポンスではマスクされます。アカウント編集時にキーフィールドを空にすると、元の値が保持されます。
- 認証情報をタスク名、備考、チケット、ログに書き込まないでください。バックアップには暗号化された認証情報が含まれるため、機密ファイルとして管理します。

## 一般的なプロバイダー

| プロバイダー | 主な認証情報 | 備考 |
| --- | --- | --- |
| Alibaba Cloud DNS（alidns）/ ESA（aliesa） | AccessKey ID / Secret | Region は任意 |
| Cloudflare | API Token、または Email + Global API Key | API アップストリームアドレスは任意 |
| DNSPod | API Token | |
| Tencent Cloud（tencentcloud） | SecretId / SecretKey | Region は任意 |
| Huawei Cloud（huaweicloud） | AccessKey ID / SecretAccessKey / Region | |
| Volcengine（volcengine） | AccessKey / SecretKey | Region、API ホストとプロトコルは任意 |
| Baidu AI Cloud（baiducloud）/ JD Cloud（jdcloud） | AccessKey ID / SecretAccessKey | Region は任意 |
| AWS Route 53 | AccessKey / SecretAccessKey | Region、Hosted Zone、プライベートゾーンは任意 |
| Google Cloud DNS | サービスアカウント JSON | プロジェクト ID は任意 |
| Azure DNS | Tenant ID / Client ID / Client Secret / Subscription ID / リソースグループ | |
| DigitalOcean / Vultr / Linode / Hetzner / Gandi / GoDaddy / Namecheap | API Token / API 認証情報 | |
| PowerDNS（pdns）/ RFC 2136 動的更新 | API アドレス / API Key、TSIG Key / Secret | Server 名、API バージョン、Zone リストは任意 |

- lego の全プロバイダー（約 190 社）を利用できます。`manual` プロバイダーは TXT を手動で追加する必要があり、自動フローには含まれません。
- Cloudflare、DNSimple、PowerDNS、Volcengine などはカスタム API アップストリームアドレスに対応し、プロキシ、セルフホスト、互換ゲートウェイに使用できます。完全な一覧は「DNS アカウント」ページのドロップダウンを基準とします。

## オフラインと手動インストール

組み込みプロバイダーは本体とともに配布されるため、オフライン環境でも追加インストールは不要です。サードパーティの provider は「プラグイン」ページで単一の helper（最大 256 MB）をアップロードできます。アップロード時にはプロトコル、ID、SHA-256 を検証し、既定では同名ファイルの上書きを拒否します。アップグレード時は旧ファイルを先にバックアップします。手動でアップロードしたマルチプロバイダー bundle は `manual` とマークされ、自動更新の対象外になります。

## 検証とトラブルシューティング

### 検証

1. DNS アカウントを作成し、保存できることを確認します。
2. テストタスクを作成し、そのアカウントとテストドメインを選択します。
3. タスクを実行し、TXT 作成、CA 検証、TXT クリーンアップをタスクログと操作ログで確認します。テスト証明書は削除するか、必要に応じて保持します。

### よくある問題

| 現象 | 原因 | 対処 |
| --- | --- | --- |
| プロバイダーのドロップダウンが空 | プロバイダーが未有効化 | 「プラグイン」ページで対象プロバイダーを有効化 |
| 認証情報の保存に失敗 | 必須フィールドの欠落/形式エラー | フォームとログのヒントに従って修正 |
| TXT の作成に失敗 | 権限/Zone/クォータ | API 権限、ドメイン、クォータを確認 |
| TXT のクリーンアップに失敗 | 権限、またはレコードが既に削除済み | 残留 TXT を手動でクリーンアップ |
| 検証タイムアウト | DNS 伝播が遅い/権威 DNS が未更新 | TTL、権威解決、CAA を確認 |
| カスタム API アドレスの失敗 | アドレスに到達できない/証明書が信頼されていない | ネットワーク、TLS、アップストリームアドレスを確認 |
| プラグインのロードに失敗 | 権限/ダイジェスト/プロトコル | ディレクトリ権限とインストール記録を確認 |

## Helper プロトコル v1（連携開発）

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

- 終了コード `0` は成功、非 `0` は失敗です。stderr はタスクログに入り（機密値はマスクされます）、DNS の `present` / `cleanup` では stdout を空にします。
- `verify` は読み取り専用操作でなければなりません。zone の列挙とレコードの照会のみを許可し、作成、変更、削除はできません。
- `verify-v1` を宣言したプロバイダーのみが `verify` 呼び出しを受け取ります。未宣言の場合、`mode=auto` は `present`/`cleanup` の書き込みテストへフォールバックし、`mode=read` は未サポートを返します。
- `checks[].status` の値は `ok`、`failed`、`skipped`、`unsupported`、`error` です。
- helper は `LACTSSL_PLUGIN_MEMORY_LIMIT`（バイト）からメモリ予算を取得します。メインプログラムは `VmRSS` を定期的に確認し、`plugins.memory_limit_mb` を超えるとプロセスを終了します。
- メインプログラムは最小限の環境変数セットのみを渡します。helper ファイルは他のユーザーに書き込みを許可せず、`manifest.sha256` が空でない場合はダイジェストを検証します。`version` は helper/プロバイダー実装自体のバージョンで、LACTSSL のビルドバージョンではありません。

## セキュリティ推奨事項

- プラグインディレクトリはサービスの実行ユーザーのみが書き込み可能にし、`0700` を推奨します。
- 信頼できるソースの helper のみをインストールし、マニフェストに `sha256` を記入してください。
- 適切な `memory_limit_mb` と `timeout_seconds` を設定し、異常なプラグインがホストを停止させないようにしてください。プラグインはサービスアカウントで実行し、`lactssl` ユーザーに追加権限を与えず、本番ではバージョンを固定して自動更新を無効にします。
