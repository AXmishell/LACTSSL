# インストールと起動

本番利用可能なインストール手順です。初回デプロイでは[クイックスタート](getting-started.md)を先に読んでください。Docker Compose の手順へ直接進んでもかまいません。メインプログラムは外部データベース、Redis、メッセージキューを必要とせず、すべての状態は `data/` にあります。

## 環境要件

Linux amd64 / arm64（macOS と Windows のリリースパッケージもあり）、Docker + Docker Compose v2 推奨。ソースビルドは Go 1.27+、Node.js 22.12+、npm が必要です。ホストリソースの目安は[デプロイアーキテクチャ](architecture.md)を参照し、開始時は 2 vCPU / 1 GB、個人の単一ドメインなら 1 vCPU / 1 GB でも動作します。管理エントリは HTTPS リバースプロキシ経由で公開し、アウトバウンドで DNS API、CA、SMTP、デプロイ先へ到達できること。管理ドメイン 1 つと TLS 証明書が必要で、ACME サーバーは任意の専用ドメインを使用します。

!!! tip "クイックデプロイ"
    Docker Compose で起動し、初期化を完了して内蔵プラグインを有効化します。パネル構築サイトでは対応するデプロイプラグインが必要です。DNS API は必要なドメインのみを認可した専用サブアカウントを使用し、コンテナのメモリ制限をアイドル時の使用量ちょうどに設定しないでください。

## 方法 1：Docker Compose（推奨）

### ディレクトリを準備する

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
```

```yaml title="docker-compose.yml"
--8<-- "docker-compose.yml"
```

デフォルトは 1Panel の `1panel-network` 外部ネットワークを使用します。使用しない場合はサービス下と末尾の `networks` を削除するか、先に作成します：

```bash
docker network create 1panel-network
```

### 起動

```bash
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```

!!! note "イメージとネットワーク"
    GHCR イメージは最初の正式 Release から利用可能です。`docker compose pull` で存在しない場合は 2.4 節に従ってローカルでビルドしてください。Compose は 8080 をすべての NIC に公開するため、パブリックホストではファイアウォールで制限するか、`ports` を `127.0.0.1:8080:8080` に変更してください。マウントは `./config/config.yaml`（設定）と `./data`（SQLite、プラグイン、ログ、アップグレードバックアップ）で、コンテナ内は `/app/config/config.yaml` と `/app/data` です。エントリスクリプトが所有者を修正してから `lactssl` ユーザーで実行し、Compose は読み取り専用ルート、`no-new-privileges`、capabilities 許可リスト、`pids_limit` を有効にします。

### バージョンを固定する（任意）

デフォルトでは `latest` を使用します。本番環境では、`docker-compose.yml` のイメージタグを直接変更して具体的なバージョンに固定することを推奨します。安定タグは `<version>`、`<major>.<minor>`、`<major>`、`latest` で、プレリリースは完全なバージョンタグのみ提供されます：

```yaml
image: ghcr.io/axmishell/lactssl:1.2.3
```

### ローカルでイメージをビルドする

Dockerfile は外部でビルドした Linux バイナリをコピーするため、先にフロントエンドと現在のアーキテクチャ向けバイナリを用意します：

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

## 方法 2：バイナリ + systemd

### インストール

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
    PostgreSQL/MySQL の論理バックアップと復元は Go コードで実装されており、`pg_dump`、`pg_restore`、`mysqldump`、`mysql` クライアントは不要です。SQLite は `VACUUM INTO` スナップショットを使用します。

### systemd ユニット

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

ログを `/opt/lactssl/data/logs/` に書く場合は `ReadWritePaths` でカバーされます。他のディレクトリには追加許可が必要です。80/443 などの特権ポートは、バイナリに権限を付与せずリバースプロキシで転送してください。

### ワンクリックデプロイスクリプト

`scripts/install.sh` は GitHub Release から Linux amd64/arm64 バイナリを取得し、ユーザー、データディレクトリ、systemd サービス、Nginx 設定を作成します。デフォルトでは TLS を有効にせず、バイナリは `127.0.0.1:8080`、Nginx は 80 で待ち受けます。

```bash
sudo ./scripts/install.sh --port 8080 --domain ssl.example.com
```

クローンせず、スクリプトを直接ダウンロードして実行することもできます（Linux リリースアーカイブにも `install.sh` が同梱されています）：

```bash
curl -fsSL https://raw.githubusercontent.com/AXmishell/LACTSSL/main/scripts/install.sh \
  | sudo bash -s -- --port 8080 --domain ssl.example.com
```

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

```bash
sudo ./scripts/install.sh \
  --domain ssl.example.com \
  --port 8080 \
  --tls-cert /etc/nginx/certs/ssl.example.com.pem \
  --tls-key  /etc/nginx/certs/ssl.example.com.key
```

スクリプトは `checksums.txt` を検証します。再実行時はバイナリのみを置き換え、既存の `config.yaml` と `data/` は保持します。`--port` は `server.addr`、`server.host`、`server.port` を同期更新します。ドメイン/証明書を指定しない場合は既存の Nginx 設定を引き継ぎ、意図せずデフォルトサイトへ戻ったり TLS が無効になったりするのを防ぎます。TLS を無効にする場合は `--no-tls` を明示してください。Nginx 未導入時はパッケージマネージャーで導入を試み、自動導入を避ける場合は `--no-install-deps`、Nginx を設定しない場合は `--no-nginx` を追加します。`ProtectSystem=strict` のため画面からの自己アップグレードは使えない場合があり、アップグレードは本スクリプトを再実行してください。`/etc/nginx` などへ書き込む場合は `--read-write-path` を繰り返し指定し、`lactssl` ユーザーの書き込み権限を確認してください。生成される主要設定は次のとおりです。

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1", "::1"]
security:
  public_url: "http://ssl.example.com"
```

Nginx 設定は `/etc/nginx/conf.d/lactssl.conf` に書き込まれ、`client_max_body_size 256m` と 600 秒のタイムアウトを含みます。デプロイ後は起動ログのセキュアエントリから初期化します。

### ソースからビルドする

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```

`make build` はフロントエンドをビルドして `internal/webui/dist` にコピーし、埋め込んだ `bin/lactssl` を生成します。バックエンドのみなら `make backend` を使いますが、フロントエンド成果物は別途用意してください。

## 初回初期化

起動ログにセキュアエントリとワンタイム初期化トークンが出力されます：

```text
セキュアエントリ: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
初期化トークン（今回の起動でのみ有効）: ...
```

1. セキュアエントリからページを開き、案内に従って管理者アカウントを作成します。
2. バックアップから復元する場合は `/install` の詳細機能でインポートし、初期化トークンを添付します。初期化成功後、トークンは直ちに無効になります。初期化前に `/install` の「実行設定」で、リッスンアドレスと信頼するリバースプロキシ、`public_url` とセッション維持、ACME（専用リスン、外部アドレス、TLS/mTLS）、ログ、アップグレード、DNS/デプロイ/通知プラグインのスイッチ、リポジトリとリソース制限を変更できます。保存後はサービスの再起動が必要です。`jwt_secret`、`salt`、セキュアエントリ、データディレクトリ、データベースとログ、プラグインディレクトリ、署名公開鍵はページに表示されず、`config.yaml` を直接編集します。セキュアエントリはログイン後に「設定 → セキュアエントリ」から変更できます。

> セキュアエントリはセキュリティ境界ではありません。本番では HTTPS、強力なパスワード、TOTP/パスキー、ネットワークアクセス制御を併用してください。

## リバースプロキシと HTTPS

メインプログラムは HTTP のみをリッスンし、TLS は Nginx、Caddy、Traefik、ロードバランサー、WAF が終端します。

### 必須設定

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

security:
  public_url: "https://ssl.example.com"
```

- `trusted_proxies` にプロキシアドレスを含めないと、クライアント IP の識別とレート制限、`Secure` Cookie の判定が誤ります。正確な IP を指定し、範囲が必要でも最小 CIDR を使って `10.0.0.0/8` のような広い範囲は避けます。`public_url` はパスワードリセットなどのリンクに使われ、Host に依存できません。TLS 終端時はプロキシが `X-Forwarded-Proto: https` を渡すようにします。

### Nginx の例

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

これが推奨の上書きモードで、LACTSSL はこの Nginx だけを信頼すれば十分です。`$proxy_add_x_forwarded_for`（追記モード）でも単一プロキシなら動作しますが、多段プロキシでは XFF に現れ得るすべてのプロキシを `trusted_proxies` に含める必要があります。CDN、Docker/Kubernetes、L4 ロードバランサーの違いは[設定](configuration.md)のリバースプロキシの項目を参照してください。プロキシ層では送信元 IP 制限、VPN、ゼロトラスト、WAF の追加を推奨します。アクセスログは独立した監査ソースになりますが、Cookie と `Authorization` ヘッダーは記録しないでください。

### ACME サーバー（任意）

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

`addr` を空にすると ACME はメインポートの `/acme` プレフィックスにマウントされます（本番は専用ポートを推奨）。`base_url` はクライアントが実際にアクセスする固定アドレスにし、パブリックネットワークへのデプロイでは Host に依存しないでください。EAB、ドメイン許可リスト、mTLS、送信元制限は[セキュリティベースライン](security.md)を参照してください。

## インストール後の最小設定

### アカウントセキュリティ

- 直ちに TOTP を有効化するかパスキーを追加し、管理された復旧メールアドレスを設定します（メール自体でも多要素認証を有効にする）。管理者アカウントは職位が保有して退職/異動時に引き継ぎ、「設定 → セッション」で不明なセッションを取り消します。

### データとバックアップ

```bash
# 書き込みを停止してからコピーするか、画面上の暗号化バックアップを使用する
tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```

本番では「システム設定 → バックアップと復元」で暗号化 ZIP を生成し、独立した媒体へ保存してください。バックアップパスワードと `config.yaml` は別々に保管します。

### プラグイン

公式 DNS、デプロイ、通知実装は内蔵されており、ネットワークからのインストールやアップロードは不要です：

1. 「プラグイン」ページで対応するカテゴリが有効になっていることを確認します。
2. 一覧で必要な DNS プロバイダー、デプロイ先、通知チャネルを有効にします。

DNS プロバイダーは既定ですべて無効、デプロイカテゴリは既定でオン、通知はメールが内蔵され常用チャネルが既定で有効です。サードパーティの helper は引き続きアップロードでき、詳細は[プラグインガバナンス](plugins.md)を参照してください。

### 検証

```bash
# プロセスとポート
curl -I http://127.0.0.1:8080/          # セキュアエントリ Cookie なしでは 404 になる（想定どおり）
ss -lntp | grep 8080

# API キーで認証チェック
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/status
```

## アップグレードとアンインストール

アップグレード前に必ずバックアップし、手順は[アップグレードと変更](upgrade.md)を参照してください。Docker の場合は次のとおりです。

```bash
docker compose pull
docker compose up -d
```

バイナリの場合は次のとおりです。

```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo systemctl start lactssl
```

アンインストール前にバックアップをエクスポートしてください。`data/` を削除するとデータベース、プラグイン、暗号化資格情報が、`config.yaml` を削除すると `salt` と `jwt_secret` が失われます。

## インストール時のよくある問題

| 現象 | 原因 | 対応 |
| --- | --- | --- |
| `docker compose up` で外部ネットワークが存在しないとエラー | デフォルトで `1panel-network` に依存 | 2 か所の `networks` を削除するか、先にネットワークを作成 |
| ルートパスへのアクセスが 404、ログイン後に Cookie が異常 | セキュアエントリ経由でない、または信頼するプロキシが未設定 | 起動ログのエントリアドレスを使用し、`trusted_proxies` を設定 |
| バックアップ/プラグインのアップロードに失敗、パスキーが使えない | プロキシのボディ制限、HTTPS でない、IP でアクセスしている | `client_max_body_size` を調整し、HTTPS ドメインまたは localhost を使用 |

その他のトラブルシューティングは[監視とトラブルシューティング](troubleshooting.md)を参照してください。
