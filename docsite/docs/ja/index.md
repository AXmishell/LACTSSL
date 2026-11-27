---
hide:
  - navigation
  - toc
---

<section class="hero" markdown>
<div class="hero-badge">セルフホスト · 単一インスタンス · オープンソース</div>

# LACTSSL

<p class="hero-sub">証明書の発行、自動更新、デプロイ、期限監視を 1 つのセルフホスト型コンソールに集約。開発者なら小型ホスト 1 台で全工程を運用できます。</p>

[クイックスタート](getting-started.md){ .md-button .md-button--primary }
[インストール](install.md){ .md-button }
</section>

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-autorenew: 自動更新とデプロイ
ACME 証明書の申請、更新、デプロイ、期限監視を一元化し、分散スクリプトや手作業の台帳を減らします。
</div>

<div class="feature-card" markdown>
### :material-server-network: プライベート CA と社内 ACME
ルート / 中間 CA を生成またはインポートし、certbot、lego、acme.sh に標準 ACME v2 エンドポイントを提供します。
</div>

<div class="feature-card" markdown>
### :material-file-export-outline: マルチターゲット配信
ローカルディレクトリと SSH/SFTP を内蔵。FTP、Webhook、クラウド CDN、宝塔、1Panel、雷池 WAF などはデプロイプラグインで拡張します。
</div>

<div class="feature-card" markdown>
### :material-monitor-eye: サイト証明書の期限監視
外部サイトの証明書を定期的に読み取り、チェーンとホスト名を検証。異常や期限間近を自動通知します。
</div>

<div class="feature-card" markdown>
### :material-shield-check-outline: 監査可能・セルフホスト
単一バイナリ、SQLite、外部依存なし。セキュアエントリ、TOTP/パスキー、フィールド暗号化、操作ログ、暗号化バックアップを備えます。
</div>

<div class="feature-card" markdown>
### :material-bell-ring-outline: 通知と連携
メールを内蔵し、主要な通知チャネルを同梱。REST API とリソース単位の API キーで CI/CD と連携できます。
</div>

</div>

## ドキュメント

<div class="doc-grid" markdown>

<div class="doc-card" markdown>
[:material-rocket-launch-outline: クイックスタート](getting-started.md)
<span>インストール、初期化、最初の証明書発行。</span>
</div>

<div class="doc-card" markdown>
[:material-download-outline: インストール](install.md)
<span>Docker、バイナリ、リバースプロキシ、HTTPS。</span>
</div>

<div class="doc-card" markdown>
[:material-format-list-checks: 製品機能](features.md)
<span>証明書、タスク、プライベート CA、ACME サービス、監視。</span>
</div>

<div class="doc-card" markdown>
[:material-server-network: デプロイ構成](architecture.md)
<span>トポロジー、ポート、容量、可用性。</span>
</div>

<div class="doc-card" markdown>
[:material-shield-lock-outline: セキュリティベースライン](security.md)
<span>アイデンティティ、暗号化、監査、本番チェックリスト。</span>
</div>

<div class="doc-card" markdown>
[:material-cog-outline: 設定リファレンス](configuration.md)
<span>全設定項目、リバースプロキシ、プラグイン設定。</span>
</div>

</div>

## 製品画面

[![LACTSSL ダッシュボード](assets/images/dashboard.png){ .doc-screenshot .doc-screenshot--home }](assets/images/dashboard.png){ target="_blank" rel="noreferrer" }

<p class="home-note">LACTSSL は単一管理者・単一インスタンスのセルフホストツールです。マルチユーザー RBAC、SSO、クラスタ、HSM は提供しません。詳しくは[製品機能](features.md)を参照してください。</p>
