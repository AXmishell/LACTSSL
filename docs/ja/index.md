---
hide:
  - navigation
  - toc
---

<section class="hero" markdown>
<div class="hero-badge">企業チーム・個人 / 小規模チーム向け · セルフホスト · シングルテナント</div>

# LACTSSL

<p class="hero-sub">パブリック証明書の発行、証明書のデプロイ、有効期限の監視、社内 PKI を、監査可能なセルフホスト型コンソールに集約します。企業は複数の事業ラインにまたがる証明書ガバナンスに、個人開発者・サイト運営者・小規模チームは 1 台の小型ホストで完全自動の更新とデプロイに利用できます。</p>

[クイックスタート](getting-started.md){ .md-button .md-button--primary }
[デプロイアーキテクチャ](architecture.md){ .md-button }
[GitHub リポジトリ](https://github.com/AXmishell/LACTSSL){ .md-button target="_blank" rel="noreferrer" }
</section>

[![LACTSSL ダッシュボード](assets/images/dashboard.png){ .doc-screenshot .doc-screenshot--home }](assets/images/dashboard.png){ target="_blank" rel="noreferrer" }

## LACTSSL を選ぶ理由

同じ製品が、企業の複数事業ラインにも、個人サイト・小規模チーム・ホームラボにも対応します。

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-certificate-outline: 証明書の期限切れリスクを集約
ACME 証明書の申請、更新、デプロイ、期限監視を一元管理し、「証明書の期限切れによる業務停止」を招く分散スクリプトや手作業の台帳を減らします。企業では複数の事業ラインをカバーでき、個人サイトならドメイン 1 つを接続するだけで済みます。
</div>

<div class="feature-card" markdown>
### :material-server-network: 社内 PKI と ACME サービス
プライベート CA で内部サービス向けの証明書を発行し、標準 ACME v2 エンドポイントを certbot、lego、acme.sh などのクライアントに公開できます。ホームラボや開発環境にも独自のローカル CA を持てます。
</div>

<div class="feature-card" markdown>
### :material-file-export-outline: マルチターゲット配信
ローカルディレクトリと SSH/SFTP を内蔵。FTP、Webhook、主要クラウド CDN、宝塔、1Panel、雷池 WAF などのターゲットはデプロイプラグインで必要に応じて拡張でき、クラウドホストからパネル構築サイトまでカバーします。
</div>

<div class="feature-card" markdown>
### :material-shield-check-outline: 監査可能・セルフホスト
単一バイナリ、SQLite、外部依存なし。セキュアエントリ、TOTP/パスキー、フィールド暗号化、操作ログ、暗号化バックアップにより、データは企業の境界からも、自分のサーバーからも出ません。リソース消費が少なく、小型ホスト 1 台で長期運用できます。
</div>

</div>

## 適用シナリオ

| シナリオ | 対象 | 典型的な課題 | LACTSSL のアプローチ |
| --- | --- | --- | --- |
| 個人サイト、ブログ、副業プロジェクト | 個人 / 小規模チーム | 証明書の期限通知を記憶に頼り、更新スクリプトが各マシンに散在している | 1 つのタスクで申請、更新、デプロイを完結。期限前に自動実行し、記録を残す |
| ホームラボと社内サービス | 個人 / 小規模チーム | 自己署名証明書によるブラウザ警告が多く、クライアントの信頼とローテーションが煩雑 | プライベート CA による階層署名と、標準 ACME エンドポイントを提供 |
| 複数ドメイン・複数事業ラインの証明書ガバナンス | 企業 | 証明書が個人アカウント、スクリプト、表計算に分散し、更新失敗に誰も気づかない | タスクの一元編成、自動更新、デプロイ記録、期限ダッシュボード |
| 内部サービスの TLS | 企業 | 社内サービスが自己署名証明書を使用し、クライアントの信頼とローテーションが困難 | プライベート CA による階層署名と、標準 ACME エンドポイントを提供 |
| 証明書配信の自動化 | 両方 | 証明書の発行後も、パネル、CDN、サーバーへの手動アップロードが必要 | タスク内でローカル/SSH/プラグインのデプロイを編成し、リロードコマンドに対応 |
| セキュリティと監査の要件 | 企業 | 「誰がいつ何を発行し、どこへデプロイしたか」に答えられない | 操作ログ、システムログ、API キーの権限分離、暗号化バックアップ |

## 選定の境界

企業・個人のどちらも、選定前に製品の境界を確認すべきです。以下は現行バージョンの実際の範囲です。

| 項目 | 現行バージョン | 個人 / 小規模チームへの影響 |
| --- | --- | --- |
| デプロイ形態 | セルフホストの単一インスタンス、シングルテナント | 小型ホスト、VPS、NAS で運用可能 |
| 管理アカウント | 単一の管理者アカウント。複数ユーザー、ロール、組織構造は提供しない | 個人または小規模チームの共用で十分 |
| マシン ID | API キーを「リソース:アクション」で認可。有効期限と有効/無効を設定可能 | CI/CD やスクリプトに制限付きキーを個別に発行できる |
| アイデンティティフェデレーション | LDAP / OIDC / SAML / SCIM は非対応。企業 SSO には接続不可 | 企業のアカウント体系に依存せず、ローカルログインで完結 |
| 高可用性 | 組み込みクラスタや Active-Active は非対応。SQLite は単一書き込みのため、本番は単一アクティブ + バックアップ/迅速復旧を推奨 | 個人利用なら定期バックアップで十分 |
| キー管理 | 秘密鍵はローカルデータベースに暗号化保存。HSM / KMS による管理は非対応 | 追加のキーサービス不要 |
| 監視連携 | 操作ログ、システムログ、認証 API を提供。Prometheus メトリクスエンドポイントは非対応 | 内蔵通知で対応可能 |
| コンプライアンス認証 | プロジェクト自体は等保（等級保護）、ISO 27001 などの認証を付帯しない。統制項目と監査証跡は提供可能 | 個人利用には無関係 |
| サポートモデル | MIT ライセンス。商用サポート、SLA、マネージドサービスは別途契約 | コミュニティドキュメントと issue によるサポート |

> 企業がマルチユーザー RBAC、SSO、マルチテナント、Active-Active クラスタを求める場合は、上位プラットフォームや組織プロセスで補う必要があり、LACTSSL を完全な IAM/PKI プラットフォームとして使うことはできません。

## 参考アーキテクチャ

```text
                企業ユーザー / 個人ユーザー / 自動化システム
                           │ HTTPS
                  ┌────────────▼────────────┐
                  │ リバースプロキシ / WAF   │   TLS 終端、アクセス制御、監査
                  └────────────┬────────────┘
                           │ HTTP（社内ネットワーク）
                  ┌──────────▼──────────────────────┐
                  │ LACTSSL 単一インスタンス        │
                  │ 管理コンソール+API+スケジューラ │
                  │ プライベート CA + ACME（任意）  │
                  │ SQLite + ローカル暗号化キー     │
                  └──┬────────────┬───────────┬─────┘
                     │            │           │
                 DNS API      デプロイ先    通知チャネル
              （パブリック CA）（SSH/パネル/CDN）（SMTP/Webhook）
```

詳細なトポロジー、ポート、容量、可用性設計は[デプロイアーキテクチャ](architecture.md)を参照してください。

## 役割別の読み方

| ロール | 関心事 | 推奨ドキュメント |
| --- | --- | --- |
| 個人開発者 / サイト運営者 / 小規模チーム | 導入、ドメイン接続、自動更新とデプロイ | [インストールと起動](install.md)、[DNS プラグイン](dns-plugins.md)、[デプロイプラグイン](deploy-plugins.md) |
| 事業責任者 / 選定評価 | 価値、境界、パイロット期間、受け入れ基準 | [クイックスタートと本番導入](getting-started.md)、[製品機能](features.md) |
| プラットフォーム / 運用責任者 | デプロイトポロジー、バックアップ/復旧、アップグレード、アラート | [デプロイアーキテクチャ](architecture.md)、[運用マニュアル](operations.md) |
| セキュリティ / コンプライアンス | アイデンティティ、暗号化、監査、サプライチェーン、統制項目 | [セキュリティベースライン](security.md)、[管理ワークスペース](administration.md) |
| 連携 / 自動化開発 | API キー、権限、CI/CD、エラー処理 | [API 連携](api.md)、[拡張開発](plugin-development.md) |

## 推奨される導入パス

**個人 / 小規模チーム**：Docker でインスタンスを 1 つ起動 → DNS プラグインとデプロイプラグインを導入 → ドメインタスクを 1 つ作成 → 更新と通知を検証、通常は半日以内に完了します。企業向けの完全なパイロットプロセスを踏む必要はありません。

**企業チーム**：

1. **評価（0.5〜1 日）**：Docker でパイロットインスタンスを起動し、テスト証明書 1 枚とローカルデプロイ 1 回を通します。
2. **パイロット（1〜2 週間）**：実際のドメイン 1〜2 件、または社内 CA のシナリオ 1 件を接続し、DNS 資格情報、デプロイ先、通知経路を検証します。
3. **本番（変更ウィンドウに合わせて）**：リバースプロキシ、バックアップ、監視、API キーの責任者を定め、セキュリティベースラインを完了してから切り替えます。
4. **運用（継続）**：毎月、期限、バックアップ、ログ、プラグインバージョンを確認し、四半期ごとに復旧訓練を行います。

具体的な手順と受け入れ表は[クイックスタートと本番導入](getting-started.md)を参照してください。

## 機能概要

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-autorenew: ACME 自動化
DNS-01 検証。単一ドメイン、複数ドメイン、ワイルドカードに対応。期限前に自動更新し、失敗時は再試行して記録を残します。
</div>

<div class="feature-card" markdown>
### :material-sitemap-outline: タスク編成
申請 → デプロイ（複数可）→ 通知。ノードは有効/無効の切り替えと並べ替えができ、同じ資格情報を複数のタスクで再利用できます。
</div>

<div class="feature-card" markdown>
### :material-lock-outline: プライベート CA
ルート CA / 中間 CA の生成またはインポート。パス長、Key Usage、Name Constraints、CRL/OCSP アドレスなどの PKI パラメータを設定できます。
</div>

<div class="feature-card" markdown>
### :material-api: 標準 ACME サーバー
エンドポイントごとにプライベート CA をバインド。EAB、ドメイン許可リスト、HTTP-01/DNS-01/TLS-ALPN-01、mTLS、オーダーのレート制限に対応します。
</div>

<div class="feature-card" markdown>
### :material-monitor-eye: サイト証明書監視
外部サイトの証明書を定期的に読み取り、証明書チェーンとホスト名を検証。異常や期限間近を自動通知します。
</div>

<div class="feature-card" markdown>
### :material-bell-ring-outline: 通知と監査
メールは内蔵、20 以上のチャネルはプラグインで提供。操作ログとシステムログは条件で絞り込み、CSV/JSON でエクスポートできます。
</div>

</div>

## ドキュメントナビゲーション

<div class="doc-grid" markdown>

<div class="doc-card" markdown>
[:material-rocket-launch-outline: クイックスタートと本番導入](getting-started.md)
<span>個人のクイックスタート、および企業のパイロット、受け入れ、本番切り替え、責任分担。</span>
</div>

<div class="doc-card" markdown>
[:material-server-network: デプロイアーキテクチャ](architecture.md)
<span>トポロジー、ポート、容量、可用性、ネットワーク境界。</span>
</div>

<div class="doc-card" markdown>
[:material-shield-lock-outline: セキュリティベースライン](security.md)
<span>アイデンティティ、暗号化、監査、アウトバウンド保護、統制項目マッピング。</span>
</div>

<div class="doc-card" markdown>
[:material-wrench-outline: 運用マニュアル](operations.md)
<span>バックアップ/復旧、監視、変更、障害対応、訓練。</span>
</div>

<div class="doc-card" markdown>
[:material-code-braces: API 連携](api.md)
<span>キーガバナンス、権限マトリクス、自動化の例。</span>
</div>

<div class="doc-card" markdown>
[:material-lifebuoy: 監視とトラブルシューティング](troubleshooting.md)
<span>よくある障害、診断手順、エスカレーション情報の収集。</span>
</div>

</div>

## 技術スタック

| レイヤー | 実装 |
| --- | --- |
| バックエンド | Go + Gin + SQLite |
| フロントエンド | Vue 3 + Vite + Element Plus（ビルド後にバイナリへ埋め込み） |
| ACME | lego / RFC 8555 |
| ストレージ | SQLite、機密フィールドは AES-256-GCM で暗号化 |
| デプロイ | 単一バイナリ、外部データベースとメッセージキューは不要 |
