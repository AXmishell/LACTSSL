# インストールと起動

本文では本番利用可能なインストール手順を示します。初回評価ではまず[クイックスタートと本番導入](getting-started.md)を参照し、シナリオと受け入れ基準を決めてから本文に従ってデプロイしてください。個人サイトや小規模チームは評価をスキップし、第 2 節の Docker デプロイへ直接進んでかまいません。

## 1. 環境要件

| 項目 | 要件 |
| --- | --- |
| オペレーティングシステム | Linux amd64 / arm64。macOS と Windows のリリースパッケージにも対応 |
| コンテナ | Docker および Docker Compose v2（推奨） |
| ソースビルド | Go 1.27+、Node.js 22.12+、npm |
| ホストリソース | [デプロイアーキテクチャ](architecture.md)を参照。開始時は 2 vCPU / 1 GB を推奨。個人の単一ドメインなら 1 vCPU / 1 GB でも動作 |
| ネットワーク | 管理エントリは HTTPS リバースプロキシ経由。アウトバウンドで DNS API、CA、SMTP、デプロイ先に到達可能 |
| ドメイン | 管理ドメイン 1 つと対応する TLS 証明書。ACME サーバーは任意で専用ドメイン |

メインプログラムは外部データベース、Redis、メッセージキューを必要としません。すべての状態は `data/` ディレクトリにあります。

!!! tip "個人 / 小規模チーム"
    企業向け評価プロセスを踏む必要はありません。第 2 節の Docker 起動、第 4 節の初期化、第 6.3 節のプラグインインストールを行います。宝塔、1Panel などのパネル構築サイトでは対応するデプロイプラグインのインストールが必要です。自動更新は DNS API に依存するため、必要なドメインのみを認可した専用サブアカウントの使用を推奨します。プラグインのインストール/更新時には一時的なメモリピークが発生するため、コンテナのメモリ制限をアイドル時の使用量ちょうどに設定しないでください。

## 2. 方法 1：Docker Compose（推奨）

### 2.1 ディレクトリを準備する

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
```

リポジトリ同梱の `docker-compose.yml` はデフォルトで GHCR イメージを使用します：

```yaml title="docker-compose.yml"
--8<-- "docker-compose.yml"
```

デフォルトファイルは 1Panel の `1panel-network` 外部ネットワークをそのまま使用します。1Panel を使用しない場合は、サービス下の `networks` とファイル末尾の `networks` セクションを削除し、Compose がデフォルトネットワークを使用するようにします。先にそのネットワークを作成することもできます：

```bash
docker network create 1panel-network
```

### 2.2 起動

```bash
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```

!!! note "イメージとネットワーク"
    GHCR イメージは最初の正式 Release から利用可能です。`docker compose pull` でイメージが存在しないと表示された場合は、2.4 節に従ってローカルでビルドしてください。Compose はデフォルトで 8080 をホストのすべての NIC に公開します。パブリックネットワーク上のホストでは、ファイアウォールで送信元を制限するか、`ports` を `127.0.0.1:8080:8080` に変更して、ローカルのリバースプロキシからのみアクセスできるようにすることを推奨します。

デフォルトのマウント：

- `./config/config.yaml`：設定ファイル
- `./data`：SQLite、プラグイン、ログ、アップグレードバックアップ

コンテナ内では常に `/app/config/config.yaml` を読み取ります、データディレクトリは `/app/data` です。エントリスクリプトは root でマウントディレクトリの所有者を修正した後、`lactssl` ユーザーに権限を落として実行します。Compose はデフォルトで読み取り専用ルートファイルシステム、`no-new-privileges`、capabilities 許可リスト、`pids_limit` を有効にします。

### 2.3 バージョンを固定する

`latest` を長期使用しないでください。`docker-compose.yml` のイメージタグを直接変更します：

```yaml
image: ghcr.io/axmishell/lactssl:1.2.3
```

安定タグには `<version>`、`<major>.<minor>`、`<major>`、`latest` が含まれます。プレリリース版は完全なバージョンタグのみがプッシュされます。

### 2.4 ローカルでイメージをビルドする

Dockerfile は外部でビルドした Linux バイナリをコピーするだけで、データベースのバックアップ/復元は Go でネイティブに実装されているため `pg_dump`/`mysqldump` は不要です。先にフロントエンドと現在のアーキテクチャ向けのバイナリを用意してからイメージをビルドします：

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

## 3. 方法 2：バイナリ + systemd

### 3.1 インストール

```bash
sudo useradd --system --home /opt/lactssl --shell /usr/sbin/nologin lactssl
sudo mkdir -p /opt/lactssl/data
sudo cp lactssl /opt/lactssl/
sudo cp config.yaml.example /opt/lactssl/config.yaml
sudo chown -R lactssl:lactssl /opt/lactssl
sudo chmod 600 /opt/lactssl/config.yaml
sudo chmod 700 /opt/lactssl/data
```

!!! note "ネイティブデータベースバックアップ"
    PostgreSQL/MySQL の論理バックアップと復元は LACTSSL の Go コードで直接実装されており、`pg_dump`、`pg_restore`、`mysqldump`、`mysql` クライアントは不要です。SQLite は引き続き `VACUUM INTO` スナップショットを使用します。

### 3.2 systemd ユニット

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

ログを `/opt/lactssl/data/logs/` に書き込む場合、上記の `ReadWritePaths` でカバーされます。他のディレクトリに書き込む場合は追加で許可が必要です。80/443 などの特権ポートを使用する場合は、バイナリに権限を付与するのではなく、リバースプロキシで転送することを推奨します。

### 3.3 ワンクリックデプロイスクリプト

リポジトリは `scripts/install.sh` を提供し、GitHub Release から Linux amd64/arm64 バイナリをダウンロードし、実行ユーザーとデータディレクトリを作成し、systemd サービスをインストールし、Nginx リバースプロキシ設定を生成します。デフォルトでは TLS を有効にしません。バイナリは `127.0.0.1:8080` をリッスンし、Nginx は 80 でリッスンしてそのポートへ転送します。

```bash
sudo ./scripts/install.sh --port 8080 --domain ssl.example.com
```

リポジトリをクローンせず、スクリプトを直接ダウンロードして実行することもできます：

```bash
curl -fsSL https://raw.githubusercontent.com/AXmishell/LACTSSL/main/scripts/install.sh \
  | sudo bash -s -- --port 8080 --domain ssl.example.com
```

Linux リリースアーカイブにも `install.sh` が同梱されており、展開後に直接実行できます。

よく使うパラメータ：

| パラメータ | 説明 |
| --- | --- |
| `--port PORT` | バイナリのリッスンポート。デフォルト `8080`、1024 以上必須 |
| `--domain DOMAIN` | Nginx の `server_name`。例 `ssl.example.com`、デフォルト `_` |
| `--version TAG` | バージョンを指定。例 `v1.2.3`、デフォルトは最新安定版 |
| `--no-nginx` | systemd サービスのみをデプロイし、Nginx をインストールまたは変更しない |
| `--tls-cert` / `--tls-key` | 任意。Nginx HTTPS を有効にし、80 → 443 リダイレクトを設定 |
| `--no-tls` | 既存の Nginx TLS 設定を無効化（デフォルトでは元々 TLS を有効にしない） |
| `--read-write-path DIR` | サービスが書き込める追加ディレクトリ。複数指定可能。ローカル証明書デプロイ先に使用 |
| `--skip-checksum` | SHA-256 検証をスキップ（非推奨） |

例：HTTPS を有効にする

```bash
sudo ./scripts/install.sh \
  --domain ssl.example.com \
  --port 8080 \
  --tls-cert /etc/nginx/certs/ssl.example.com.pem \
  --tls-key  /etc/nginx/certs/ssl.example.com.key
```

スクリプトは Release 内の `checksums.txt` を検証します。再実行時はバイナリのみを置き換えてサービスを再起動し、既存の `config.yaml` と `data/` は保持されます。`--port` を指定すると `server.addr`、`server.host`、`server.port` が同期更新されます。`--domain` / `--tls-cert` / `--tls-key` を指定しない場合は、既存の Nginx 設定のドメインと証明書を引き継ぎ、アップグレード時に意図せずデフォルトサイトへ戻ったり TLS が無効になったりするのを防ぎます。TLS を無効にする場合は明示的に `--no-tls` を使用してください。Nginx がインストールされていない場合、スクリプトはシステムのパッケージマネージャーでインストールを試みます。自動インストールを望まない場合は `--no-install-deps`、Nginx を設定しない場合は `--no-nginx` を追加します。サービスユニットは `ProtectSystem=strict` を有効にしているため、プログラム画面での自己アップグレードはディレクトリが読み取り専用のため使用できない場合があります。アップグレードは本スクリプトを再実行してください。内蔵のデプロイ先から本機の `/etc/nginx` などのディレクトリへ書き込む必要がある場合は、`--read-write-path` を繰り返し使用して許可し、`lactssl` ユーザーがそれらのディレクトリへの書き込み権限を持つことを確認してください。

デフォルトで生成される主要設定：

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1", "::1"]
security:
  public_url: "http://ssl.example.com"
```

Nginx 設定は `/etc/nginx/conf.d/lactssl.conf` に書き込まれ、`client_max_body_size 256m` と 600 秒の読み書きタイムアウトを含みます。デプロイ完了後、第 4 節に従って起動ログのセキュアエントリから初期化を完了します。

### 3.4 ソースからビルドする

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```

`make build` はまずフロントエンドをビルドして `internal/webui/dist` にコピーし、その後フロントエンドリソースを埋め込んだ `bin/lactssl` をコンパイルします。バックエンドのみをビルドする場合は `make backend` を使用できますが、フロントエンドの成果物は自分で用意する必要があります。

## 4. 初回初期化

起動ログにはセキュアエントリとワンタイム初期化トークンが出力されます：

```text
セキュアエントリ: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
初期化トークン（今回の起動でのみ有効）: ...
```

1. セキュアエントリからページを開きます。
2. 案内に従って管理者アカウントを作成します。
3. バックアップから復元する場合は、`/install` の詳細機能でインポートし、初期化トークンを添付します。
4. 初期化成功後、トークンは直ちに無効になります。

初期化前に `/install` の「実行設定」でよく使う許可リスト項目を変更できます：リッスンアドレスと信頼するリバースプロキシ、`public_url` とセッション維持、ACME（専用リスン、外部アドレス、TLS/mTLS）、ログ（ローテーション、アクセスログ、システムログ）、アップグレードとプログラムの自己アップグレード、DNS/デプロイ/通知プラグインのスイッチ、プラグインリポジトリとリソース制限。保存後はサービスの再起動が必要です。

`jwt_secret`、`salt`、セキュアエントリ、データディレクトリ、データベースとログファイル、プラグインディレクトリ、プログラムリポジトリと署名公開鍵などの機密または高リスク項目はページに表示されません。サーバー上で直接 `config.yaml` を編集してください。セキュアエントリは初期化してログインした後、「設定 → セキュアエントリ」から変更できます。

> セキュアエントリはセキュリティ境界ではありません。本番では HTTPS、強力なパスワード、TOTP/パスキー、ネットワークアクセス制御を必ず使用してください。

## 5. リバースプロキシと HTTPS

メインプログラムは HTTP のみをリッスンし、本番では Nginx、Caddy、Traefik、ロードバランサー、WAF が TLS を終端します。

### 5.1 必須設定

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

security:
  public_url: "https://ssl.example.com"
```

- `trusted_proxies` にはプロキシアドレスを含める必要があります。そうしないとクライアント IP の識別とレート制限が不正確になり、`Secure` Cookie の判定も誤る可能性があります。
- 実際のプロキシの正確な IP を指定してください。範囲が必要な場合は最小の CIDR を使い、`10.0.0.0/8` のような広い範囲は避けます。
- `public_url` はパスワードリセットなどのリンクに使用され、リクエストの Host に依存できません。
- TLS をプロキシで終端する場合、プロキシが `X-Forwarded-Proto: https` を渡すことを確認します。

### 5.2 Nginx の例

```nginx
server {
    listen 443 ssl;
    server_name ssl.example.com;

    ssl_certificate     /etc/nginx/certs/ssl.example.com.pem;
    ssl_certificate_key /etc/nginx/certs/ssl.example.com.key;
    ssl_protocols       TLSv1.2 TLSv1.3;

    client_max_body_size 256m;  # バックアップ復元とプラグインアップロード

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        # 上書きモード: クライアントが送った XFF を破棄し、実際のクライアントのみを保持
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

これが推奨の**上書きモード**です。LACTSSL はこの Nginx だけを信頼すれば十分です。`$proxy_add_x_forwarded_for`（追記モード）に変更しても単一プロキシなら正しく動作しますが、多段プロキシでは XFF に現れ得るすべてのプロキシを `trusted_proxies` に含める必要があります。含めない場合、クライアント IP は上流プロキシで止まります。CDN、Docker/Kubernetes、L4 ロードバランサーの違いは[設定](configuration.md)の第 11 節を参照してください。

プロキシ層でさらに送信元 IP 許可リスト、VPN、ゼロトラストアクセス戦略、WAF を追加することを推奨します。リバースプロキシのアクセスログは独立した監査ソースとして使用できますが、Cookie と `Authorization` ヘッダーは記録しないでください。

### 5.3 ACME サーバー（任意）

内部クライアントに ACME を提供する場合：

```yaml
acme:
  enabled: true
  addr: ":14000"
  base_url: "https://pki.example.com:14000"
  tls:
    cert: "/etc/lactssl/acme.crt"
    key: "/etc/lactssl/acme.key"
```

- `addr` を空にすると、ACME はメインポートの `/acme` プレフィックスにマウントされます。本番では専用ポートを推奨します。
- `base_url` はクライアントが実際にアクセスするアドレスである必要があり、パブリックネットワークへのデプロイではリクエストの Host に依存しないでください。
- EAB、ドメイン許可リスト、mTLS、送信元制限は[セキュリティベースライン](security.md)を参照してください。

## 6. インストール後の最小設定

### 6.1 アカウントセキュリティ

- 直ちに TOTP を有効化するか、パスキーを追加します。
- 管理された復旧メールアドレスを設定します。メール自体で多要素認証を有効にする必要があります。
- 管理者アカウントは職位が保有することを明確にし、退職/異動時に引き継ぎを完了します。
- 「設定 → セッション」で不明なセッションを確認し、取り消します。

### 6.2 データとバックアップ

```bash
# 書き込みを停止してからコピーするか、画面上の暗号化バックアップを使用する
tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

本番では画面の「システム設定 → バックアップと復元」で暗号化 ZIP を生成し、バックアップを独立した媒体に保存することを推奨します。バックアップパスワードと `config.yaml` は別々に保管してください。

### 6.3 プラグイン

メインプログラムには DNS / デプロイ / 通知プラグインが一切内蔵されていません：

1. 「プラグイン」ページに進み、リポジトリのアドレスを確認し、「インデックスを更新」をクリックします。
2. 「プラグインマーケット」で必要なプラグインをインストールします。
3. 対応するリストでプロバイダー、デプロイ先、通知チャネルを有効にします。
4. 本番環境では社内ミラーを使用しバージョンを固定することを推奨します。詳細は[プラグインガバナンス](plugins.md)を参照してください。

プラグイン未インストール時：

- DNS プロバイダー一覧が空で、DNS アカウントを作成できません。
- デプロイ先はローカルディレクトリと SSH のみです。
- 通知はメールのみです。

### 6.4 検証

```bash
# プロセスとポート
curl -I http://127.0.0.1:8080/          # セキュアエントリ Cookie なしでは 404 になる（想定どおり）
ss -lntp | grep 8080

# API キーで認証チェック
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/status
```

その後、画面で以下を完了します：

- DNS アカウントを作成してテストする。
- テストタスクを 1 つ作成して実行する。
- テスト通知を送信する。
- サイト監視を 1 つ追加する。
- バックアップを 1 回ダウンロードして復号する。

## 7. アップグレードとアンインストール

アップグレード前に必ずバックアップしてください。手順は[アップグレードと変更](upgrade.md)を参照してください。

Docker：

```bash
docker compose pull
docker compose up -d
```

バイナリ：

```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo systemctl start lactssl
```

アンインストール前にバックアップをエクスポートしてください。`data/` を削除するとデータベース、プラグイン、暗号化資格情報が永久に失われます。`config.yaml` を削除すると `salt` と `jwt_secret` が失われます。

## 8. インストール時のよくある問題

| 現象 | 原因 | 対応 |
| --- | --- | --- |
| `docker compose up` で外部ネットワークが存在しないとエラー | デフォルトで `1panel-network` に依存 | 2 か所の `networks` を削除してデフォルトネットワークを使用するか、先にそのネットワークを作成 |
| ルートパスへのアクセスが 404 | セキュアエントリ経由でない | 起動ログのエントリアドレスを使用 |
| ログイン後に Cookie が異常/レート制限が不正確 | 信頼するプロキシが未設定 | `trusted_proxies` を設定し、転送ヘッダーを渡す |
| パスワードリセットリンクが誤ったドメインを指す | `public_url` が未設定 | 固定の外部アドレスを設定 |
| バックアップ/プラグインのアップロードに失敗 | プロキシのリクエストボディ制限 | `client_max_body_size` を調整 |
| パスキーが使用できない | HTTPS でない、または IP でアクセスしている | HTTPS ドメインまたは localhost を使用 |
| コンテナ再起動後に旧バージョンへ戻る | コンテナ内で自己アップグレード | イメージタグでアップグレード |

その他のトラブルシューティングは[監視とトラブルシューティング](troubleshooting.md)を参照してください。
