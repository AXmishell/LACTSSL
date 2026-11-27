# API 連携

LACTSSL のコンソール API は、ブラウザのログインセッションと API キーの両方から呼び出せます。API キーは CI/CD、監視巡回、CMDB 同期、証明書配布など、長期稼働するプログラムに適しています。キーごとに権限、有効期限、有効状態を個別に設定できるため、管理者パスワードの共有を避けられます。

> アカウントセキュリティ、パスキー、セッション管理などの機密操作は、ブラウザのログインセッションからのみ呼び出せます。API キーではログインパスワードの変更、パスキーの登録、他のセッションからのサインアウトはできません。

## 連携方式の選択

| シナリオ | 推奨方式 | 理由 |
| --- | --- | --- |
| 手動発行、トラブルシューティング、設定変更 | ブラウザコンソール | 完全な監査と対話的な確認が可能 |
| CI/CD による更新/デプロイのトリガー | API キー + `tasks:run` / `deploy:execute` | 最小権限、ローテーション可能 |
| 証明書一覧の CMDB への同期 | API キー + 読み取り専用権限 | 秘密キーを公開しない |
| 証明書のダウンロードと配布 | API キー + `certificates:key` + `deploy:execute` | 高い機密性、追加のネットワーク制限が必要 |
| 監視巡回 | API キー + `system:read` / `monitors:read` | 読み取り専用、短い有効期限 |
| ログ監査 | API キー + `logs:read` / `system_logs:read` | 読み取り専用、SIEM へエクスポート |

API キーはマルチユーザー RBAC ではありません。複数人での権限分離が必要な場合は、自前のプラットフォームまたはゲートウェイで承認とプロキシ呼び出しを実装してください。

## 認証

キーを `Authorization: Bearer` または `X-API-Key` リクエストヘッダーに設定します:

```bash
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```

```bash
curl -H "X-API-Key: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```

要点:

- エンドポイントは常に `/api` にあります。旧バージョンの `/<entry>/api` は互換エイリアスとして残ります。
- リクエストは引き続きログインセッションまたは API キーによる認証が必要で、セキュアエントリには依存しません。
- API キーによるリクエストは Cookie に依存しないため、`X-LACTSSL-CSRF` ヘッダーは不要です。
- すべてのリクエストは HTTPS 経由を推奨し、キーを URL クエリパラメータに含めないでください。レスポンスヘッダーの `X-Request-ID` はトラブルシューティング時に提示してください。

## API キーのライフサイクル

### 作成

「API キー」ページで名前、有効期限、権限を指定して作成します。完全なキーは一度だけ表示され、ダイアログを閉じると再表示できず、ローテーションのみ可能です。キーは 86 文字の Base64URL ランダム文字列（64 バイトのエントロピー）で、固定プレフィックスはなく、サーバーはダイジェストのみを保存します。

```bash
curl -X POST \
  -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI deployment",
    "scopes": ["tasks:read", "tasks:run", "deploy:execute"],
    "expires_at": "2027-01-01T00:00:00Z"
  }' \
  https://ssl.example.com/api/api-keys
```

`expires_at` を空にすると無期限になります（本番では非推奨）。キーの作成と変更は高リスク操作で、ブラウザセッションではログイン ID に、API キー呼び出しでは `api_keys:write` 権限に依存します。

### ローテーションと無効化

ローテーション後は古いトークンが直ちに無効になり、無効化または削除後はそのキーを使うすべての呼び出しが `401` を返します。API キーで他のキーを管理する場合、自身が持たない権限は付与できず、自身より高い権限のキーを変更/ローテーション/削除することもできません。

### 保管

キーはパスワードマネージャーまたはキー管理システムへ保存し、コードリポジトリ、イメージ、ログには書き込まないでください。システムごとに個別発行して用途と環境を名前に含め、有効期限を設定して期限前にローテーションします。漏洩が疑われる場合は直ちに無効化し、操作ログを確認してください。

## 権限モデル

権限は「リソース:アクション」で区分されます。`*` はすべてのシステム API 機能を表し、すべての機密操作を含みます。

| 権限 | 説明 | 機密 |
| --- | --- | --- |
| `*` | すべてのシステム API 機能 | はい |
| `profile:read` | 現在のキーが属するアカウントを表示 | いいえ |
| `system:read` | バージョン、状態、メタデータを表示 | いいえ |
| `dashboard:read` | ダッシュボード統計を表示 | いいえ |
| `certificates:read` | 証明書、証明書チェーンのダウンロード、証明書分析を表示 | いいえ |
| `certificates:write` | 証明書のクリーンアップと削除 | いいえ |
| `certificates:key` | 証明書の秘密キーを表示またはダウンロード | はい |
| `certificates:request` | クライアント CSR を送信し、サーバーがパブリック認証局で検証と発行を完了、または私有 CA で署名（`private_cas:write` も必要） | はい |
| `deploy:execute` | 証明書を手動でデプロイ、またはデプロイノードを含むタスクの作成/変更/実行（ファイル書き込み。コマンド実行には別途 `deploy:command` が必要） | はい |
| `deploy:command` | ローカル/SSH デプロイで reload コマンドの実行を許可（サービスアカウントで任意のコマンドを実行するのと同等） | はい |
| `tasks:read` | タスク一覧とタスクログを表示 | いいえ |
| `tasks:write` | タスクの作成、変更、削除、有効化/無効化、実行ログの消去（作成時に即実行するには `tasks:run` も必要。デプロイノードを含む場合は `deploy:execute` も必要。プライベート CA の場合は `private_cas:write` も必要） | いいえ |
| `tasks:run` | タスクの手動実行または一括実行（デプロイノードを含む場合は `deploy:execute` も必要。プライベート CA の場合は `private_cas:write` も必要） | いいえ |
| `monitors:read` | サイト証明書監視を表示 | いいえ |
| `monitors:write` | 監視の作成、変更、削除、即時チェック | いいえ |
| `dns_accounts:read` | DNS プロバイダーアカウントを表示（キーはマスク） | いいえ |
| `dns_accounts:write` | DNS プロバイダーアカウントの作成、変更、削除 | はい |
| `deploy_accounts:read` | デプロイ先アカウントを表示（キーはマスク） | いいえ |
| `deploy_accounts:write` | デプロイ先アカウントの作成、変更、削除 | はい |
| `deployments:read` | 証明書デプロイ履歴を表示 | いいえ |
| `ca:read` | ACME CA、エンドポイント、アカウント、オーダーを表示 | いいえ |
| `ca:write` | ACME CA、エンドポイント、EAB の変更と診断データのクリーンアップ | はい |
| `private_cas:read` | プライベート CA 証明書と証明書チェーンを表示 | いいえ |
| `private_cas:write` | プライベート CA の作成、インポート、削除 | はい |
| `private_cas:key` | プライベート CA の秘密キーをダウンロード | はい |
| `plugins:read` | プラグインと有効状態を表示 | いいえ |
| `plugins:write` | プラグインのアップロード、インストール、更新、アンインストール、有効化 | はい |
| `logs:read` | 操作ログの表示とエクスポート、ログアーカイブの表示/ダウンロード | いいえ |
| `logs:write` | すべての操作ログのクリア、ログアーカイブの削除（クリア操作自体は監査記録を残します） | はい |
| `system_logs:read` | プロセスログの表示とエクスポート、ログアーカイブの表示/ダウンロード | いいえ |
| `system_logs:write` | すべてのシステムログの削除、ログアーカイブの削除 | はい |
| `settings:read` | システム設定と内部 CA 信頼チェーンを表示 | いいえ |
| `settings:write` | 通常のシステム設定の変更（SMTP/通知ターゲット。テストメッセージは引き続きブラウザセッションのみ） | はい |
| `backup:read` | バックアップ状態とサイズを表示 | いいえ |
| `backup:download` | 暗号化バックアップをエクスポート（ブラウザセッションのみ。旧キー互換のため保持） | はい |
| `backup:restore` | バックアップをアップロードして復元（サービスが再起動します。ブラウザセッションのみ。旧キー互換のため保持） | はい |
| `upgrade:read` | データベースマイグレーションとプログラム更新状態を表示 | いいえ |
| `upgrade:write` | マイグレーションの実行、データベースの最適化、プログラムの置き換え | はい |
| `security:write` | 信頼ルートとプラグイン信頼アンカーの変更（入口変更はブラウザセッションのみ） | はい |
| `api_keys:read` | API キー一覧と権限を表示 | いいえ |
| `api_keys:write` | API キーの作成、変更、ローテーション、削除 | はい |

### 推奨ロールマッピング

ロール別の推奨権限は[管理ワークスペース](administration.md)の表を参照してください。秘密キー権限は追加権限です:

- 証明書の秘密キーのダウンロードには `certificates:read`、プライベート CA の秘密キーには `private_cas:read` も必要です。
- デプロイノードを含むタスクの作成/実行には `deploy:execute` を明示的に持つ必要があります。`certificates:write` と `tasks:*` はデプロイ実行能力を暗黙に含みません。
- ローカル/SSH デプロイの `reload_command` には `deploy:command` も必要です。`deploy:execute` のみでは証明書ファイルの書き込みしかできません。
- プライベート CA で発行するタスクには `private_cas:write` が必要で、`tasks:write` は発行能力を暗黙に含みません。`enabled:true` のタスク作成は即時発行のため `tasks:run` も必要です。

## レスポンス形式

```json
{
  "success": true,
  "data": {}
}
```

```json
{
  "success": false,
  "code": "api.error.api_key_scope_denied",
  "message": "API キーに必要な権限がありません: certificates:read",
  "params": { "scope": "certificates:read" }
}
```

一般的なステータスコード:

| ステータスコード | 意味 |
| --- | --- |
| `400` | パラメータエラー、設定検証の失敗 |
| `401` | 未認証。キーが存在しない、無効化済み、期限切れ、または削除済み |
| `403` | 権限不足、またはこのエンドポイントがブラウザセッションからの呼び出しのみを許可 |
| `404` | リソースが存在しない、またはパスが誤っている |
| `409` | 状態の競合（実行中、重複操作など） |
| `429` | リクエストが多すぎます。`Retry-After` に従って再試行してください |
| `500` | サーバーエラー。`X-Request-ID` を記録して調査してください |

## よく使うエンドポイント

### 状態と証明書の表示

```bash
# プロセス状態（system:read が必要）
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/status

# 証明書一覧（certificates:read が必要）
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificates
```

一覧エンドポイントは配列を直接返し、ログエンドポイントはページングオブジェクトを返します:

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/logs?page=1&size=50&level=error&source=task&q=demo"
```

`size` の最大は 200 で、`q` は保存された元の内容とパラメータ値でマッチします。操作ログのクリアには `DELETE /api/logs` と `logs:write` が必要です。`log.archive.enabled` が有効な場合、クリア時に先にアーカイブを書き込んでから削除し、レスポンスに `archived` を追加します。アーカイブに失敗した場合は 500 を返し、ログは削除されません。

```bash
# 一覧: page、size、category、reason、from、to、q をサポート
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives?page=1&size=20&category=operation"

# アーカイブファイルをダウンロード
curl -fsS -OJ -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>/download"

# アーカイブを削除
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>"
```

アーカイブエンドポイントは `logs:*` または `system_logs:*` の対応する権限を受け付けます。表示/ダウンロードにはいずれかの読み取り権限、削除にはいずれかの書き込み権限が必要です。`category` の値は `operation` または `runtime`、`reason` の値は `manual_clear`、`retention`、`max_rows` です。

### ターゲットフローのプレビュー

ターゲットフロー Beta では、タスクを保存する前にターゲットサイトを読み取り、証明書のグループ化をプレビューできます。このエンドポイントは読み取り専用で、`deploy:execute` が必要です:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  https://ssl.example.com/api/tasks/target-flow/preview \
  -d '{
    "sources": [{"deploy_account_id": 3, "deploy_config": {"target": "site"}}],
    "selection": {"mode": "all"},
    "policy": {"strategy": "smart", "max_domains_per_cert": 100},
    "public_ca": true
  }'
```

タスク作成時に `flow_mode: "target_beta"` と `target_flow` を渡します。`target_flow.policy.strategy` は `smart`、`per_site`、`per_base_domain`、`single`、`per_source`、`custom` をサポートします。1 つの Beta タスクは複数の証明書に関連付けられる場合があり、証明書の関連付けは `task_certificate_links` に記録されます。

### タスクの実行

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/run
```

タスクに有効なデプロイノードが含まれる場合、キーは `deploy:execute` も持っている必要があります。

デフォルトでは、有効期限内で更新ウィンドウに入っていない証明書を再利用し、デプロイノードのみを再実行して、アップストリーム CA への再注文は行いません。強制的に再発行する必要がある場合は `?force=1` または `{"force":true}` を使用します。強制発行は CA の証明書クォータを消費します。

タスクの実行ログを取得します（`tasks:read` が必要）：

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```

タスクの実行ログを消去します（`tasks:write` が必要）：

```bash
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```

消去すると、そのタスクの発行ログとデプロイログが削除され、削除行数が `deleted` として返されます。操作ログには監査記録が残ります。

### 証明書のダウンロード

```bash
# 完全なチェーン（certificates:read が必要）
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.fullchain.pem \
  "https://ssl.example.com/api/certificates/42/download?type=fullchain"

# 秘密キー（certificates:read + certificates:key が必要）
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.key \
  "https://ssl.example.com/api/certificates/42/download?type=key"
```

`type` は `fullchain`（デフォルト）、`cert`、`key` をサポートします。ダウンロードエンドポイントは PEM の原文を返し、JSON には包みません。ファイル権限に従って保存し、ログに書き込まないでください。

### サイト監視

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors

curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors/7/check
```

### API キー管理

| エンドポイント | メソッド | 権限 | 説明 |
| --- | --- | --- | --- |
| `/api-keys` | GET | `api_keys:read` | キーを一覧表示。平文トークンは返しません |
| `/api-keys/scopes` | GET | `api_keys:read` | 権限一覧とローカライズされた説明を取得 |
| `/api-keys` | POST | `api_keys:write` | キーを作成。レスポンスでこの一度だけ `token` を返します |
| `/api-keys/:id` | PUT | `api_keys:write` | 名前、権限、有効状態、有効期限を変更 |
| `/api-keys/:id/rotate` | POST | `api_keys:write` | キーをローテーション。古いトークンは直ちに無効 |
| `/api-keys/:id` | DELETE | `api_keys:write` | キーを削除 |

### クライアント CSR による証明書の申請

サーバーは中間者としてクライアント CSR を受け取り、設定済みのパブリック認証局と DNS アカウントで DNS-01 検証を完了するか、管理者が管理するプライベート CA で直接署名して発行します。サーバーは秘密キーを生成も保存もせず、発行後もクライアントが保管します。作成には `certificates:request` 権限が必要で、プライベート CA を使用する場合は `private_cas:write` も必要です:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n",
    "ca": "letsencrypt",
    "challenge": "dns01",
    "dns_account_id": 3,
    "idempotency_key": "order-20261118-001",
    "wait_seconds": 0
  }' \
  https://ssl.example.com/api/certificate-requests
```

プライベート CA を使用する場合は、`ca` に `private-ca:<id>` を指定するか `private_ca_id` を渡します。プライベート CA 申請では DNS-01 検証、`dns_account_id`、ACME 連絡先メールは不要で、`app.internal` などの内部ドメインも申請できます:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n",
    "private_ca_id": 2,
    "idempotency_key": "internal-20261121-001"
  }' \
  https://ssl.example.com/api/certificate-requests
```

エンドポイントは `202 Accepted` と申請 ID を返し、申請は非同期で実行されるため状態をポーリングできます:

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx
```

状態が `issued` になったら証明書チェーンをダウンロードできます（秘密キーは提供されません）:

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx/certificate?type=fullchain" \
  -o example.com.fullchain.crt
```

要点:

- パブリック CA 申請は DNS-01 のみで、ワイルドカードも DNS-01 が必要です。プライベート CA 申請はサーバーが直接署名し、ドメイン検証を行いません。
- `ca` には ACME アップストリームまたは `private-ca:<id>` を指定します。プライベート CA の発行には `private_cas:write` も必要で、CA の ExtKeyUsage と NameConstraints が適用されます。
- サーバーは CSR 署名、公開キーの強度、SAN 仕様、コモンネームを検証します。内部ドメインはパブリック CA では拒否され、プライベート CA では許可されます。CSR はクライアント署名済みのため、サーバーが代わりに修正することはできません。
- `idempotency_key` を渡すと、同じ CSR、CA、DNS ルートの組み合わせで進行中の申請を 1 つに制限できます。`csr.allowed_domains` は申請可能なドメインをカンマまたは空白区切りで制限し、空の場合は CA の NameConstraints に従います。
- `key_available` が `false` の証明書はサーバーに秘密キーがなく、秘密キーのダウンロードや秘密キーに依存するデプロイは実行できません。

リポジトリにはそのまま使用できる Python クライアントスクリプト `scripts/csr_client.py` が用意されています。ローカルで秘密キーと CSR を生成し、申請を送信して状態をポーリングし、ダウンロード後に証明書と秘密キーの一致を検証します:

```bash
export LACTSSL_URL="https://ssl.example.com"
export LACTSSL_TOKEN="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

python3 scripts/csr_client.py \
  --domains example.com,www.example.com \
  --dns-account-id 3 \
  --ca letsencrypt \
  --out-dir ./certs

# プライベート CA 申請（秘密キーは引き続きローカルで生成）
python3 scripts/csr_client.py \
  --domains app.internal,api.internal \
  --private-ca-id 2 \
  --out-dir ./certs
```

対話式ウィザードで項目ごとに入力することもでき、パラメータを覚える必要はありません（引数なしでターミナルから実行すると自動的にウィザードに入ります）:

```bash
python3 scripts/csr_client.py --interactive
```

```bash
# 更新: 既存の秘密キーから新しい CSR を生成（キーはコピーも変更もされません）
python3 scripts/csr_client.py \
  --key ./certs/example.com.key \
  --domains example.com,www.example.com \
  --out-dir ./certs \
  --force

# または前回の状態ファイルから直接更新（キー、CA、DNS アカウント、出力名を再利用）
python3 scripts/csr_client.py --renew-from ./certs/example.com.request.json

# 状態確認 / キャンセル（--json はスクリプト向け）
python3 scripts/csr_client.py --status csr_xxxxxxxx --json
python3 scripts/csr_client.py --cancel csr_xxxxxxxx --yes
python3 scripts/csr_client.py --list --json

# バッチ: 順次実行し、既定では最初の失敗で停止
python3 scripts/csr_client.py --manifest ./certs.json --json
```

```json
{
  "defaults": {"ca": "letsencrypt", "dns_account_id": 3, "out_dir": "./certs"},
  "certificates": [
    {"domains": ["example.com", "www.example.com"]},
    {"domains": ["api.example.com"], "name": "api", "key_type": "rsa2048"}
  ]
}
```

スクリプトは CSR のみをアップロードし、秘密キーは常にクライアント側に保存されます。出力には `.key`、`.csr`、`.crt`、`.fullchain.crt`、`.request.json` が含まれます。ローカルで生成したキーと CSR はまず `.new` として一時保存され、証明書のダウンロードと検証が成功した後にのみ最終ファイルをアトミックに置き換えるため、申請が失敗しても以前の有効な証明書一式を壊しません。申請作成直後に `.request.json` を書き込むため、通信が切れた場合や `--quiet` 使用時でも `--request-id csr_xxxxxxxx` で再開でき、前回の `.key.new`/`.key` も自動的に見つけます。一時的なネットワークエラーは既定で 3 回再試行し（`--retries`）、サーバーが再試行可能と判断した場合は `/retry` を 1 回自動実行し（`--auto-retry`）、HTTP 429 の `Retry-After` に従います。進捗は stderr に出力され、`--json` のとき stdout には最終 JSON のみが入ります。トークンは `LACTSSL_TOKEN_FILE` または `--token-file` で読み込め、コマンドライン引数に残りません。クライアント UI は `--lang` または `LACTSSL_LANG` で `zh-CN`、`zh-TW`、`en-US`、`ja-JP`、`ru-RU` を切り替えられ、API エラーも同じ言語で要求されます。終了コード: 0 成功、1 一般失敗、2 引数/設定、3 認証/権限、4 ネットワーク、5 サーバー側の申請失敗、6 ローカル検証失敗、130 ユーザーによるキャンセル。

### 認証情報の可用性検証

DNS アカウントやターゲット認証情報を追加または変更した後、認証情報が実際に使用可能かを能動的に検証できます。検証エンドポイントは `dns_accounts:write` / `deploy_accounts:write` 権限を使用し、ブラウザセッションではパスワードまたは TOTP の再認証は不要です。検証結果は参考情報で、保存や実行を妨げません。`domain` を省略した場合、保存済み DNS アカウントは有効なドメイン紐付けを優先し、紐付けがない場合は `mode: "scope"` を返して設定と呼び出し権限のみを確認します。

| メソッド | パス | 説明 |
| --- | --- | --- |
| POST | `/api/dns-accounts/:id/verify` | 保存済み DNS アカウントを検証。`domain`、`mode` を渡せる |
| POST | `/api/dns-accounts/verify` | 未保存の DNS 設定を検証。キーを明示的に送信する必要がある |
| POST | `/api/deploy-accounts/:id/verify` | 保存済みターゲット認証情報を検証。`deploy_config` を渡せる |
| POST | `/api/deploy-accounts/verify` | 未保存のターゲット認証情報設定を検証。キーを明示的に送信する必要がある |
| POST | `/api/deploy-accounts/:id/options` | 保存済み認証情報を使用してデプロイフィールドの動的オプションを読み取る（例: BaoTa のサイト一覧） |
| POST | `/api/deploy-accounts/:id/match` | 証明書ドメインに一致するターゲットサイトを照合し、`field`、`multiple`、`values` を返す |

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"domain":"example.com","mode":"auto"}' \
  https://ssl.example.com/api/dns-accounts/3/verify
```

動的オプションエンドポイントは、マニフェストで `options_source` を宣言したデプロイフィールドにのみ適用されます。API キーには `deploy_accounts:write` 権限が必要です。リクエストボディにはフィールド名と現在のデプロイパラメータを渡し、認証情報はサーバーがターゲット認証情報から読み取ります:

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"field":"sites","config":{"target":"docker"}}' \
  https://ssl.example.com/api/deploy-accounts/3/options
```

`{"options":[{"label":"サイト名","value":"サイト名"}]}` を返します。プラグインが利用できない、またはパネルに到達できない場合は明確なエラーを返し、UI は手動入力機能を維持します。

レスポンス構造には項目別のチェックが含まれ、認証失敗、ドメインがその認証情報に属さない、ディレクトリが書き込み不可などの原因を区別できます:

```json
{
  "success": true,
  "data": {
    "ok": false,
    "mode": "write",
    "checks": [
      {"key": "init", "status": "ok"},
      {"key": "domain", "status": "ok", "message": "example.com"},
      {"key": "write", "status": "failed", "message": "..."}
    ],
    "message": "...",
    "verified_at": "2026-11-18T12:00:00Z"
  }
}
```

- DNS 検証はデフォルトで `auto` モードです。プラグインが `verify-v1` を宣言していれば、読み取り専用で zone を列挙してドメインの所属を確認し、未対応ならランダムな `_acme-challenge` TXT を書き込んで直後に削除する書き込みテストへフォールバックします。`mode=read` は読み取り専用を強制し、未対応時は `unsupported` を返します。
- 保存済みアカウントは DB 内の設定のみを使用し、未保存の設定ではキーを明示的に送信します（保存済みキーは再利用しない）。endpoint の変更で認証情報が他のサーバーへ送信されるのを防ぎます。
- ターゲット検証は組み込みの `local`/`ssh` に対応し、接続と認証後に一時ファイルを作成して削除します（`reload_command` は実行しません）。その他のデプロイプラグインは `verify` 実装まで `unsupported` を返します。
- `deploy_config` を渡すと特定デプロイノードのスコープ（サイト、ディレクトリ、バケット、ドメイン）を検証でき、「追加した権限が使用可能か」の確認に適します。

## CI/CD の例

### 証明書更新後の配布

```bash
#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${LACTSSL_URL:?}"
TOKEN="${LACTSSL_TOKEN:?}"

# タスクをトリガー（タスク ID は CMDB または設定管理から渡される）
curl -fsS -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  "${BASE_URL}/api/tasks/${TASK_ID}/run"

# タスクの完了を待ってから、証明書 ID でダウンロードして配布
curl -fL \
  -H "Authorization: Bearer ${TOKEN}" \
  -o /run/secrets/example.com.fullchain.pem \
  "${BASE_URL}/api/certificates/${CERT_ID}/download?type=fullchain"
```

- API には冪等キーがありません。`run` を繰り返すとフローが再実行されますが、証明書が有効な間は発行をスキップしてデプロイのみ実行するため、スクリプト側で重複排除してください。
- タスク実行は非同期です。完了を待つにはタスク一覧をポーリングするか通知を購読し、POST の返却時点を完了とみなさないでください。
- 秘密キーのダウンロードには追加権限が必要です。ほとんどの配布では証明書チェーンのみで十分で、秘密キーはデプロイノードが直接書き込むため、CI 経由で中継しないでください。

### 巡回

```bash
#!/usr/bin/env bash
set -euo pipefail

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/status" >/dev/null

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/certificates" |
  jq -e '[.data[] | select(.not_after < (now + 14*86400 | todate))] | length == 0'
```

## エラー処理と再試行

- `429` は `Retry-After` に従ってバックオフし、直ちに再試行しないでください。`5xx` は指数バックオフを使い、`X-Request-ID` を記録します。
- `401` は再試行せず、キーの期限切れ、無効化、ローテーション済みでないか確認してください。
- `403` は権限とエンドポイントの制限（ブラウザセッションのみ許可するものがある）を確認します。
- `400`/`409` は自動再試行せず、パラメータまたは状態を修正してください。発行/デプロイを伴うリクエストは副作用があるため、再試行前にタスク/デプロイの状態を確認します。

## セキュリティ推奨事項

- 自動化タスクごとに個別のキーを作成し、必要な権限のみを付与してください。読み取り専用タスクに `*` を付与しないでください。
- 適切な有効期限を設定して定期ローテーションし、ログ、スクリーンショット、公開リポジトリに完全なキーを露出しないでください。
- 漏洩が疑われる場合は直ちに無効化または削除してください（ローテーションで旧キーは即座に無効になります）。
- ネットワーク層で呼び出し元を制限し、API 呼び出しログと操作ログを関連付けて、呼び出し元、時刻、`X-Request-ID` を保持してください。
