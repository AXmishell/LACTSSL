# 設定リファレンス

LACTSSL のアプリケーション設定は `config.yaml` と組み込みの既定値のみに由来し、アプリケーションレベルの環境変数は読み取りません。設定変更後はサービスの再起動が必要です。一部の実行時設定（セキュアエントリ、プラグインの有効状態）はデータベースに保存され、設定ファイルより優先されます。

## 設定ファイルのモデル

既定ではカレントワーキングディレクトリの `config.yaml` を読み取り、起動時に `-config` で別のパスを指定できます。

- ファイルが存在しない場合、初回起動時に自動生成され、ランダムな `jwt_secret` と `salt` が書き込まれます。キーを含むため `0600` 権限を維持してください。
- 設定変更は再起動後に反映されます。セキュアエントリは画面で変更でき、即座に反映されます。システムプロキシなどの Go HTTP クライアントの挙動は、引き続き環境変数で設定できます。コメント付きの完全な例はリポジトリルートの [`config.yaml.example`](https://github.com/AXmishell/LACTSSL/blob/main/config.yaml.example) を参照してください。

### 最小限の本番設定

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

data_dir: "data"

security:
  public_url: "https://ssl.example.com"
  session_persistence: true

log:
  level: info
  format: json
  file: "data/logs/lactssl.log"
  also_stdout: true
  max_size_mb: 50
  max_backups: 10
  max_age_days: 30
  compress: true
  access_log: true
  system:
    enabled: true
    level: info
    access: false
    retention_days: 30
    max_rows: 200000
  archive:
    enabled: true
    compress: true
    retention_days: 0
    max_total_mb: 0
    quota_policy: fail
```

### 設定の優先順位

| 設定 | 由来 | 説明 |
| --- | --- | --- |
| リッスンアドレス、ログ、キー、ACME | `config.yaml` | 変更後は再起動で反映 |
| セキュアエントリ | データベース。`config.yaml` で上書き可能 | 画面での変更は即時反映。`security.entry` が空でない場合は起動時に上書き |
| プラグインの有効状態 | データベースのホワイトリスト | 再起動と再スキャン後も保持 |
| システム言語、通知チャネルなど | データベース | 画面で変更 |

### Web からの設定ファイル編集

ログイン後、「システム設定 → 設定ファイル」で `config.yaml` のホワイトリスト対象フィールドを変更できます。リッスンアドレス、信頼できるリバースプロキシ、`security.public_url`、セッション保持、レート制限のしきい値、ACME、ログ、アップグレード、プラグインの実行パラメータが含まれます。レート制限タブには組み込みの既定値と統合した後の有効なルールが表示され、保存時には `config.yaml` へ書き戻されて再起動後に反映されます。

- `jwt_secret`、`salt`、`data_dir`、`database.path`、ログファイル、プラグインディレクトリなどの高リスクなフィールドは、引き続きサーバー上でのみ編集できます。
- 保存前に `<data_dir>/config-backups/` へ設定スナップショットが自動作成されます。スナップショットには `config.yaml` のみが含まれ、データベースを含む暗号化された全体バックアップとは異なります。リストアではホワイトリスト対象のフィールドのみを書き戻し、現在の `jwt_secret`、`salt`、`data_dir`、`database.path` は変更しません。保存後は再起動が必要です。リッスンポートを変更する場合は事前にポートのチェックが行われますが、新しいアドレスが利用できない場合に備えて SSH またはコンソールからのアクセスを確保してください。「信頼できるリバースプロキシ」の自動検出は現在のリクエストの TCP 対向アドレスのみを使用し、`X-Forwarded-For` などの転送ヘッダーは表示のみで信頼リストへ自動追加されません。リバースプロキシ構成では `security.public_url` も併せて設定してください。

## 完全なフィールド例

```yaml
server:
  addr: ""              # 完全なリッスンアドレス。例: ":8080"。空でない場合は host + port より優先
  host: ""              # リッスンする NIC。空はすべての NIC
  port: 8080            # リッスンポート
  trusted_proxies: []   # 信頼できるリバースプロキシの CIDR

data_dir: "data"        # データディレクトリ。プラグイン、ログ、アップグレードバックアップを格納

database:
  driver: "sqlite"      # sqlite（既定）または postgres
  path: ""              # SQLite のパス。空は <data_dir>/lactssl.db
  dsn: ""               # PostgreSQL の DSN。driver=postgres の場合は必須
  max_open_conns: 10    # PostgreSQL のコネクションプール
  max_idle_conns: 5
  conn_max_lifetime_minutes: 30
  allow_multi_instance: false  # 複数インスタンスで同じ PostgreSQL データベースの共有を許可するか

log:
  level: info           # プロセスログのレベル: debug、info、warn、error
  format: text          # 出力形式: text または json
  file: ""              # ログファイル。空の場合は stdout のみ
  also_stdout: true     # ファイルへ書き込む際に stdout にも出力するか
  max_size_mb: 20       # 1 ファイルのサイズ上限。超過後にローテーション
  max_backups: 5        # 保持する履歴ファイル数。0 は無制限
  max_age_days: 30      # 履歴ファイルの保持日数。0 は無制限
  compress: true        # 履歴ファイルを圧縮するか
  access_log: true      # HTTP アクセスログを記録するか
  system:
    enabled: true       # 「ログ → システムログ」タブで参照するためデータベースへ書き込むか
    level: info         # データベースへ書き込む最低レベル
    access: false       # HTTP アクセスログもデータベースへ書き込むか
    retention_days: 7   # 保持日数
    max_rows: 20000     # 最大行数
  archive:
    enabled: true       # ログの削除/クリア前にアーカイブする。アーカイブ失敗時は削除を阻止
    dir: ""             # 空は <data_dir>/archives/logs
    compress: true      # gzip 圧縮するか
    retention_days: 0   # アーカイブファイルの保持日数。0 は永久保持
    max_total_mb: 0     # アーカイブディレクトリの容量上限（MB）。0 は無制限
    quota_policy: fail  # fail または prune_oldest

security:
  entry: ""             # セキュアエントリ。空の場合は初回起動時にランダム生成
  public_url: ""        # 外部アクセスアドレス。例: "https://ssl.example.com"
  session_persistence: true  # false の場合はプロセス再起動時にログインセッションをクリア
  jwt_secret: ""        # JWT 署名キー。空の場合は自動生成して書き戻す。手動設定は 32 文字以上
  salt: ""              # 機密フィールドの暗号化ソルト。空の場合は自動生成して書き戻す。手動設定は 32 文字以上
  rate_limit:
    max_keys: 10000     # メモリ内レートリミッターが保持する key の最大数
    rules:
      # max はウィンドウ内で許可する回数（0 は無制限）、window_seconds はウィンドウの秒数。
      # 列挙されていないルールは組み込みの既定値を使用。完全なルール一覧は config.yaml.example を参照。
      login_ip: {max: 10, window_seconds: 300}
      login_account: {max: 20, window_seconds: 900}
      api_ip: {max: 600, window_seconds: 60}
      acme_request: {max: 1200, window_seconds: 60}
      acme_order: {max: 300, window_seconds: 3600}

acme:
  enabled: true         # 組み込み ACME サーバーを有効にするか。既定で有効
  addr: ""              # 独立したリッスンアドレス。例: ":14000"。空の場合はメインポートの /acme にマウント
  base_url: ""          # 外部アドレス。例: "https://pki.example.com:14000"
  tls:
    cert: ""            # ACME 独立リッスンの TLS 証明書
    key: ""             # ACME 独立リッスンの TLS 秘密鍵
  mtls:
    enabled: false      # クライアント証明書を要求するか
    client_ca: ""       # クライアント CA 証明書のパス

plugins:
  enabled: true
  dir: ""                 # 空は <data_dir>/plugins/dns
  memory_limit_mb: 128
  timeout_seconds: 60
  max_output_kb: 64

deploy_plugins:
  enabled: true
  dir: ""                 # 空は <data_dir>/plugins/deploy
  memory_limit_mb: 128
  timeout_seconds: 300
  max_output_kb: 64

notify_plugins:
  enabled: true
  dir: ""                 # 空は <data_dir>/plugins/notify
  memory_limit_mb: 128
  timeout_seconds: 30
  max_output_kb: 64

upgrade:
  auto: true
  auto_optimize: true
  vacuum: true
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    # 公式ビルドはリリース公開鍵を自動的に埋め込みます。未設定の自前ビルドはオンラインアップグレードを拒否し、管理された環境のみ未署名パッケージを明示的に許可できます
    allow_unsigned: false
    token: ""
```

## `server`

- `addr`: 完全なリッスンアドレスです（例: `:8080`、`127.0.0.1:8080`）。`host`、`port` は `addr` が未設定の場合に組み合わせて使用します。このポートは Go バイナリが直接ページと API を提供します。フロントエンドのリソースはバイナリに埋め込まれており、Vite の `5173` をリバースプロキシすることはありません。`5173` は `make dev` のときのみ存在します。
- `trusted_proxies`: 信頼できるリバースプロキシの IP/CIDR リストです。空の場合はプロキシヘッダーを一切信頼せず、直接接続のデプロイに適しています。リバースプロキシで TLS を終端する場合は必ず設定してください。そうでないと `Secure` Cookie とクライアント IP の識別が正しく行われません。本番では `host` を `127.0.0.1` または内部ネットワークのアドレスに設定してリバースプロキシからアクセスし、実際のプロキシアドレスのみを含めて `0.0.0.0/0` は設定しないでください。プロキシと LACTSSL が同じホストにない場合は、内部ネットワークの CIDR を使用します。

## `data_dir` とデータベースバックエンド

`data_dir` はプラグイン、ログ、アップグレードバックアップに使用します。データベースは既定で SQLite で、ファイルは `<data_dir>/lactssl.db` に置かれます。PostgreSQL 18 を明示的に使用することもできます。

```yaml
database:
  driver: "postgres"
  dsn: "postgres://user:password@127.0.0.1:5432/lactssl?sslmode=verify-full"
```

- `driver=postgres` の場合、`path` は無視され、`dsn` が必須です。環境変数 `LACTSSL_DATABASE_DSN` で上書きして、パスワードを設定ファイルに書かないようにすることもできます。既定では `allow_multi_instance: false` で、アプリケーションは PostgreSQL の advisory lock を保持し、2 つ目のインスタンスが同じデータベースへ接続すると起動に失敗します。
- SQLite は単一接続に固定され、単一ホストの小規模なデプロイに適しています。外部バックアップや集中運用が必要な場合は PostgreSQL を選択してください。
- バックエンドの切り替えにデータベースの手動変更は不要です。ログイン後に `/upgrade` の「データベース移行」カードから実行します。詳細は [upgrade.md](upgrade.md#データベースのオンライン切り替えsqlite--postgresql) を参照してください。

データディレクトリは `0700` 権限で作成され、`database.path` が空の場合は `<data_dir>/lactssl.db` を使用します。共有書き込み可能なパスや公開ディレクトリ、NFS/SMB などのマルチライト共有ストレージに置かないでください。SQLite は単一書き込みで、ロックのセマンティクスが不安定です。バックアップ範囲は `config.yaml` + `data/` 全体です。

## `log`

プロセスログは実行時のトラブルシューティング用で、非同期にデータベースへ書き込んで「ログ → システムログ」タブに表示できるほか、stdout/ファイルへも出力します。操作ログは「誰がいつ何をしたか」を記録し、「ログ → 操作ログ」タブに表示されます。

- `level`、`format`、`file`、`also_stdout`、`max_size_mb`、`max_backups`、`max_age_days`、`compress`: 最低出力レベル（`debug`、`info`、`warn`、`error`）、出力形式（`text` または `json`）、ログファイルのパス、stdout への同時出力、1 ファイルのサイズ上限（`1〜1024`）、履歴ファイル数（`0` は無制限）、保持日数（`0` は無制限）、`.gz` 圧縮の有無。`debug` はソースの位置を付加し、`json` はログ収集システムに適しています。ファイルが空の場合は stdout のみで、相対パスはプロセスのカレントワーキングディレクトリを基準にします。ディレクトリは `0700` で自動作成され、ファイルの権限は `0600` です。Docker では `also_stdout: true` を維持してください。
- `access_log`: HTTP アクセスログを記録するか。メソッド、ルートテンプレート、ステータスコード、所要時間、クライアント IP などのフィールドのみを含み、クエリパラメータ、リクエストボディ、認証ヘッダーは記録しません。セキュアエントリはパス内で一律 `:entry` に置き換えられ、各レスポンスには `X-Request-ID` が付きます。
- `system.enabled`、`system.level`、`system.access`、`system.retention_days`、`system.max_rows`: データベースへの書き込み可否、最低レベル（`log.level` より低くできない）、アクセスログの書き込み（既定 `false`）、保持日数（`1〜365`）、最大行数（`100〜1000000`）。行数超過時は最も古いレコードを削除します。
- `archive.enabled`、`archive.dir`、`archive.compress`、`archive.retention_days`、`archive.max_total_mb`、`archive.quota_policy`: 削除/クリア前のアーカイブ（既定で有効）、ルートディレクトリ（空は `<data_dir>/archives/logs`）、gzip 圧縮（無効時は `.ndjson`）、保持日数（`0〜3650`、`0` は永久）、容量上限（`0` は無制限）、上限超過時の `fail`（既定。アーカイブを拒否し削除を阻止）または `prune_oldest`（最も古いアーカイブを削除）。アーカイブに失敗すると削除を阻止し、ログを黙って破棄しません。`prune_oldest` は監査資料を破棄するため明示的に設定します。

アーカイブファイルは `*.ndjson.gz`（または `.ndjson`）と `*.manifest.json` で構成され、マニフェストには行数、ID の範囲、時間範囲、トリガー理由、操作者、SHA-256、ファイルサイズが含まれます。アーカイブディレクトリは既定の ZIP バックアップに含まれないため、別途バックアップが必要です。書き込みは有界の非同期キューを採用しており、データベースがビジーでもアプリケーションリクエストをブロックしませんが、キューが満杯になると新しいログは破棄されます。システムログにはクライアント IP、ドメイン、エラーの詳細が含まれる場合があるため、運用データとして管理してください。本番の推奨事項: ログファイルは独立したディスクやマウントボリュームに置き、`format: json` でログ基盤へ連携します。`access_log: true` は監査に便利です。`system.access` は短期的な調査が必要な場合を除いて `false` を維持します。

## `security`

- `entry` / `public_url`: 管理ページのセキュアエントリのパスセグメントと外部アクセスアドレスです。`entry` は手動設定では 8〜32 文字の英字、数字、アンダースコア、ハイフンが要件で、空の場合は初回起動時に 20 文字の大文字・小文字・数字の組み合わせをランダム生成してデータベースへ保存します。`public_url`（例: `https://ssl.example.com`、パスを含めない）はパスワードリセットメールのリンク生成に使用され、Host ヘッダーインジェクションを防ぎます。未設定の場合はローカルループバックからのリクエストのみ Host から推測できます。
- `session_persistence`: セッション保持で、既定は `true` です。`true` の場合、起動時に `sessions` テーブルをクリアせず、JWT も固定の `jwt_secret` で署名するため、再起動後もブラウザは元のログインセッションを使用できます。`false` の場合は起動のたびにすべての古いログインが無効になります。
- `jwt_secret` / `salt`: ログイントークンの署名キーと、データベース内の機密フィールド（DNS/デプロイ認証情報、通知キー、プライベート CA と証明書の秘密鍵など）の暗号化ソルトです。空の場合はそれぞれ 32 バイトのランダム値を自動生成して設定ファイルへ書き戻します。手動設定では 32 文字以上が必要で、そうでない場合は起動を拒否します。
- `rate_limit.max_keys` / `rate_limit.rules`: メモリ内トークンバケットが保持する key の最大数（既定 `10000`）と、ルール名ごとのしきい値の上書きです。key はクライアント IP、アカウント、操作ごとに 1 つ消費し、超過時には最も長く未使用のものから追い出します。各ルールは `max`（ウィンドウ内で許可する回数）と `window_seconds`（ウィンドウの秒数）を含み、未設定のルールは組み込みの既定値を使用します。`window_seconds` を省略した場合は既定のウィンドウを引き継ぎ、`max: 0` はそのルールを無効化します。未知のルール名は起動失敗となるためスペルミスを発見できます。

よく使うルールと既定値:

| ルール | 既定値 | 説明 |
| --- | --- | --- |
| `login_ip` / `login_account` / `login_totp` | 10/5 分、20/15 分、5/10 分 | ログイン IP、アカウントの失敗、TOTP 検証 |
| `forgot_ip` / `forgot_account` | 5/10 分、3/時間 | パスワードを忘れた場合 |
| `passkey_login_begin` / `passkey_login_fail` | 20/5 分、10/5 分 | パスキーログイン |
| `reauth` | 10/10 分 | 機密操作の再認証失敗 |
| `api_ip` / `api_anonymous` / `entry` | 600/分、120/分、60/分 | `/api` の総量、匿名リクエスト、エントリの探索 |
| `csr_create` | 120/時間 | ACME クライアントの CSR 送信 |
| `acme_request` / `acme_account` / `acme_pki` | 1200/分、30/時間、600/分 | ACME プロトコルエントリ、アカウント作成、CRL/OCSP |
| `acme_order` | 300/時間 | ACME エンドポイントで個別に設定していない場合のオーダー上限 |

> レート制限のしきい値は変更後にサービスの再起動が必要です。ACME エンドポイント自体の「1 時間あたりのオーダー数」は管理画面で設定し、`acme_order` より優先されます。

> **必ず `config.yaml` をバックアップしてください。** `salt` を失うと、データベース内の暗号化された認証情報を復号できません。`jwt_secret` を失うか変更すると、既存のログインセッションが無効になります。既定ではセッション保持が有効なため、再起動してもサーバー側のセッションはクリアされません。`security.session_persistence: false` を設定すると、起動のたびにセッションをクリアして JWT 署名サブキーをローテーションする厳格モードに戻ります。

セキュリティの推奨事項:

- `entry` はランダムな長い文字列を維持し、`admin`、`ssl`、`panel` などの推測可能な値を使用しないでください。`public_url` は固定の HTTPS アドレスである必要があり、リクエストの Host に依存できません。`session_persistence` はデプロイ方針に応じて選択し、既定で有効な場合はプロセス再起動で再ログインは強制されませんが、より長いセッションライフサイクルを受け入れる必要があります。
- `jwt_secret` と `salt` はパスワードマネージャーや設定管理システムで生成/保管し、バージョン管理へコミットしないでください。`salt` を変更すると既存の暗号化された認証情報が使用できなくなるため、移行計画を伴う必要があり、値を直接変更することは禁止です。

## `acme`

- `enabled`: 組み込み ACME サーバーを有効にするかで、既定で有効です。エンドポイントを 1 つも作成していない場合は外部へ証明書を発行しません。プロトコルエントリを完全に無効にする場合は `false` に設定します。
- `addr`: 独立したリッスンアドレスです（例: `:14000`）。空の場合は ACME プロトコルがメインポートの `/acme` プレフィックスにマウントされます。`base_url` はクライアントが実際にアクセスする ACME のベースアドレスで、directory、アカウントリソースの URL、証明書内の CRL/OCSP アドレスの生成に使用します。パブリックなデプロイでは固定アドレスを設定する必要があり、リクエストの Host に依存できません。未設定の場合は `security.public_url` へフォールバックし、両方とも空の場合はリクエストの Host から推測して起動時の警告を出力します。
- `tls.cert` / `tls.key` / `mtls.enabled` / `mtls.client_ca`: ACME の独立リスンが直接 TLS を提供する場合の証明書と秘密鍵、クライアント証明書を要求する場合の有効化とクライアント CA 証明書です。リバースプロキシで TLS を終端する場合は `tls` を空のままにします。mTLS を有効にする場合は、ACME の TLS 証明書と秘密鍵も同時に設定する必要があります。

セキュリティの推奨事項: パブリックなデプロイでは独立したポートを使用して送信元を制限し、各エンドポイントにはドメインホワイトリストを設定して本番では EAB を有効にします。`base_url` は固定のパブリックアドレスである必要があり、Host ヘッダーインジェクションを避けます。ACME サービスが不要な場合は `enabled: false` に設定して攻撃面を減らします。

## `plugins`、`deploy_plugins`、`notify_plugins`

3 種類のプラグインはそれぞれ `<data_dir>/plugins/dns`、`<data_dir>/plugins/deploy`、`<data_dir>/plugins/notify` を使用し、サブディレクトリは混在しません。公式の DNS、デプロイ、通知実装は本体に組み込まれており、必要に応じてカテゴリと項目を有効化します。

### DNS プラグイン

DNS の設定例は「2. 完全なフィールド例」の `plugins` セクションを参照してください。`plugins.dir` は `<data_dir>/plugins/dns`、`memory_limit_mb`、`timeout_seconds`、`max_output_kb` で実行時の上限を制御します。

### デプロイプラグイン

デプロイプラグインは証明書の秘密鍵を受け取りますが、カテゴリと公式ターゲットは既定で有効です。不要なターゲットは無効化し、設定例は §2 の `deploy_plugins` を参照してください。

### 通知プラグイン

通知プラグインは既定で有効で、メールは常に利用できます。設定例は §2 の `notify_plugins` を参照してください。プラグインディレクトリの権限は `0700`、helper は `0755`、所有者はサービス実行ユーザーです。本番では `public_key` を設定し、`auto_update` を無効にすることを推奨します。詳細なガバナンスは[プラグインガバナンス](plugins.md)を参照してください。

## `upgrade`

`upgrade` は起動時の自動移行と `/upgrade` ページの最適化の挙動を制御します。

```yaml
upgrade:
  auto: true              # 起動時に履歴移行を自動実行
  auto_optimize: true     # 起動時にインデックスを補完し、統計を更新し、WAL を折り畳む。VACUUM は実行しない
  vacuum: true            # /upgrade での手動最適化時に VACUUM の実行を許可
  program:
    enabled: true
    repo: "AXmishell/LACTSSL"
    channel: stable       # stable または beta
    check_interval_hours: 6
    max_download_mb: 128
    public_key: ""
    token: ""
```

- プログラムのアップグレードは GitHub Release から該当プラットフォームのリリースパッケージをダウンロードし、`checksums.txt` 内の SHA-256 を検証してから現在のバイナリをアトミックに置き換え、旧バージョンを `<data_dir>/upgrade-backups/program/` へバックアップします。
- 読み取り専用ファイルシステム、リリースパッケージのないプラットフォーム、または `dev` ビルドはアップグレード不可と表示されます。コンテナ内のアップグレードはイメージを変更しないため、本番ではイメージタグによるアップグレードを推奨します。`public_key` を設定すると `checksums.txt.sig` の検証が必須になり、`token` は GitHub API のレート上限を引き上げるために使用します。

## コマンドライン引数

| 引数 | 説明 |
| --- | --- |
| `-config` | 設定ファイルのパス。既定はカレントワーキングディレクトリの `config.yaml` |

```bash
./bin/lactssl -config /etc/lactssl/config.yaml
```

セキュアエントリはサービス起動後に「設定 → セキュアエントリ」で変更することもでき、再起動は不要です。

## リバースプロキシの要点

### 信頼境界

- `server.trusted_proxies` に列挙した TCP 対向アドレスのみを信頼済みプロキシとします。空の場合、すべての転送ヘッダーを信頼せず、直接接続のデプロイに適しています。
- `X-Forwarded-For`、`X-Real-IP`、`Forwarded`、`CF-Connecting-IP` は、プロキシが信頼されるまではクライアントが偽造できる主張にすぎません。設定画面の「自動検出」は手がかりを表示するだけで、自動的に信頼リストへ追加しません。`0.0.0.0/0`、`::/0`、広すぎる内部網を設定しないでください。信頼範囲内の任意のホストがクライアントの送信元 IP を偽装できます。

### 上書きモード（推奨）

LACTSSL に最も近いプロキシで、転送ヘッダーを実際のクライアントアドレスに置き換えます:

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-Proto $scheme;
```

この場合、`trusted_proxies` には直近のプロキシアドレスだけを設定すれば、1 回の解決でクライアント IP を取得できます。多段構成では、エッジ側で実際のクライアントを復元したうえで直近ホップが上書きし、中間プロキシが上流プロキシのアドレスで XFF を上書きしないようにします。

### 追記モード

Nginx の `$proxy_add_x_forwarded_for` は、クライアントが送った XFF を保持し、現在の接続アドレスを追記します。LACTSSL は右から左へ信頼済み項目をスキップします:

- 単一プロキシ: Nginx のアドレスだけを信頼すれば十分です。
- 多段プロキシ: XFF に現れ得るすべてのプロキシを `trusted_proxies` に追加する必要があります。直近ホップだけを信頼すると、上流プロキシの IP で止まります。最も左側のクライアントアドレスを信頼リストへ追加してはいけません。例えば `client → CDN → Nginx → LACTSSL` の追記モードでは XFF は `client, CDN エッジ IP` になるため、Nginx だけを信頼すると CDN エッジ IP が返ります。CDN の公式範囲も信頼するか、Nginx で XFF を上書きしてください。

### CDN とクラウドロードバランサー

- `CF-Connecting-IP` は、TCP 対向が Cloudflare の公式範囲に属する場合のみ信頼してください。ヘッダーの存在だけで信頼すると、直接接続したクライアントに偽装されます。推奨: 自前の Nginx で公式範囲を使って `real_ip` を有効にし、`$remote_addr` で XFF を上書きします。LACTSSL はその Nginx だけを信頼します。CDN のエッジ IP は変動するため個別に追加せず、公式範囲を使うか CDN の背後に自前のプロキシを追加します。L4/TCP ロードバランサーには HTTP ヘッダーがなく、LB のアドレスしか見えません。PROXY protocol を使うか、その後ろに HTTP ヘッダーを書けるプロキシを置きます。

### Docker と Kubernetes

- コンテナ内の peer は、多くの場合ブリッジゲートウェイや Service の転送アドレスであり、実際のクライアントではありません。プロキシコンテナ/ノードの正確な IP を優先し、IP が動的な場合は専用ネットワーク内のプロキシアドレスまたは最小 CIDR のみを信頼します。プライベートだからという理由で `172.16.0.0/12` を信頼しないでください。同じネットワーク内の他のコンテナが転送ヘッダーを偽装できます。Ingress Controller が XFF を洗浄するか確認してください（すべての Ingress が同じ動作をするとは限りません）。

### 適用と確認

1. プロキシアドレスを `trusted_proxies` に追加し、`security.public_url` を設定します。プロキシで TLS を終端する場合は `X-Forwarded-Proto: https` を渡します。
2. バックアップのリストアとプラグインのアップロードには大きなボディ上限が必要です（例: Nginx `client_max_body_size 256m`）。プロキシの読み取りタイムアウトは、リストア、プラグインのダウンロード、デプロイをカバーしてください（例: 600 秒）。
3. 保存後にサービスを再起動し、「設定 → 設定ファイル → 自動検出」で `peer_ip`、`applied_trusted_proxies`、`effective_client_ip`、`client_ip_source` を確認します。再起動前は `configured_trusted_proxies` と `applied_trusted_proxies` が異なる場合があります。偽装テストとして、信頼されていないアドレスから `X-Forwarded-For: 203.0.113.9` を送っても TCP 対向アドレスが使われることを確認します。アクセスログの `ip_source`、`ip_skipped`、`ip_warnings` が調査に役立ちます。

### 高度なプロキシ識別

解決順序は、信頼済みプロキシ識別 → Cloudflare 専用ヘッダー（任意）→ `X-Forwarded-For` → `X-Real-IP` → RFC 7239 `Forwarded` → TCP 対向アドレスです。どのモードでも、信頼されていない送信元の転送ヘッダーは採用しません。

- **RFC 7239 `Forwarded`**: 前のヘッダーが使えない場合に自動解析します。`for=203.0.113.9`、`for=203.0.113.9:4711`、`for="[2001:db8::1]:4711"` に対応し、`unknown` や `_hidden` などの難読化値は拒否します。
- **Cloudflare モード**:
  ```yaml
  server:
    client_ip_mode: cloudflare
    trusted_proxies:
      - "173.245.48.0/20"
      - "103.21.244.0/22"
      # ... Cloudflare の公式範囲
  ```
  `CF-Connecting-IP` は peer が `trusted_proxies` に含まれる場合のみ採用します。ヘッダーを上書きするだけのローカル Nginx と併用せず、オリジンは Cloudflare からのみ到達可能にしてください。
- **PROXY protocol / mTLS プロキシ識別**:
  ```yaml
  server:
    proxy_protocol: true
  ```
  `trusted_proxies` 内の peer から送られた v1/v2 ヘッダーのみ受け付けます。信頼されていない送信元の PROXY ヘッダーは接続を切断します。PROXY ヘッダーのない通常の HTTP も引き続き利用できます。L4 ロードバランサー側で送信設定が必要です。mTLS では次のようにメインリスナーを HTTPS にし、`proxy_client_ca` が発行したクライアント証明書を提示する接続を IP に関係なく信頼済みプロキシとして扱います。クライアント証明書を持たないブラウザも利用できます。このモードはプロキシが LACTSSL に直接 TLS 接続する構成専用のため、プロキシで TLS を終端する場合は IP/CIDR 信頼または PROXY protocol を使用し、ブラウザ用 CA ではなくプロキシ専用 CA を使用します。
  ```yaml
  server:
    tls:
      cert: "/etc/lactssl/server.crt"
      key: "/etc/lactssl/server.key"
      proxy_client_ca: "/etc/lactssl/proxy-ca.pem"
  ```

完全な例は[インストールと起動](install.md)を参照してください。

## 設定の検証とトラブルシューティング

よくある起動失敗の原因:

| エラー | 原因 | 対処 |
| --- | --- | --- |
| `jwt_secret` / `salt` が短すぎる | 手動設定が 32 文字未満 | 32 文字以上に変更するか、空にして自動生成 |
| リッスン失敗 | ポートが使用中または権限がない | ポートと実行ユーザーを確認 |
| mTLS 設定が不完全 / 設定の検証失敗 | mTLS を有効にしたが TLS 証明書/秘密鍵がない。リストアパッケージまたは手動編集が不正 | `acme.tls.cert/key` も同時に設定。ログの案内に従って修正し、リストアパッケージは適用前に検証される |
| 変更が反映されない | 設定には再起動が必要 | サービスを再起動。セキュアエントリ/プラグインページの設定は例外 |

設定のリストアと移行はアップグレードツールが処理します。詳細は[アップグレードと変更](upgrade.md)を参照してください。
