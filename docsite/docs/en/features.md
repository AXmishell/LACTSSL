# Features
This document describes LACTSSL capabilities, key parameters, and limitations to support development and integration. Detailed procedures are in [Quick start](getting-started.md), [Administration](administration.md), and [API integration](api.md).

## Capability map

| Capability domain | Highlights | Typical users |
| --- | --- | --- |
| Certificate assets | Public CA requests, automatic renewal, certificate details, and cleanup | Platform/Operations |
| Task orchestration | Standard: request → deploy (one or more) → notify; Target Flow Beta: read sites → group certificates → request → deploy | Platform/Operations |
| Certificate delivery | Local directory, SSH/SFTP, FTP, webhooks, cloud CDN, control panels | Platform/Operations, target systems |
| Internal PKI | Private CA generation/import, hierarchy, CRL/OCSP URLs | Security, infrastructure |
| ACME server | Standards-compliant ACME v2 endpoints for internal clients | Internal platforms, devices, test environments |
| Monitoring and alerting | External site certificate monitoring, task/deployment notifications | Operations, monitoring |
| Audit and recovery | Operation logs, system logs, encrypted backups | Security, audit, operations |
| Integration | API keys, REST API, automation | Integration development |

## Certificate issuance and renewal

### Validation methods
- Public CA requests use **DNS-01** validation only and support single-domain, multi-domain, and wildcard certificates.
- DNS-01 requires provider API credentials under "DNS Accounts"; tasks create and clean up TXT records automatically.
- Private CA issuance signs directly without ACME validation; the built-in ACME server supports HTTP-01, DNS-01, and TLS-ALPN-01 for internal clients.

### Built-in certificate authorities

| CA | Notes |
| --- | --- |
| Let's Encrypt | Default public CA; no EAB required |
| ZeroSSL | Requires EAB |
| Google Trust Services | Requires EAB |
| LiteSSL | Requires EAB |
| ZoTrus | Requires EAB; KID is the registered email. Uses manual DNS validation and does not support wildcards |
| Actalis | Requires EAB |
| SSL.com | Requires EAB; ECDSA/RSA endpoint selected by key type |

"Public CA" also lets you add **generic ACME upstreams**: configure the ACME directory URL, ACME v1/v2 protocol, and optional EAB. They appear in the task and default CA lists; custom directory URLs must use HTTPS (only local loopback may use HTTP). ACME v1 supports DNS-01 only and no EAB.

### Keys and renewal
- Private key types: `ecdsa` (default), `ecdsa384`, `rsa2048`, `rsa4096`.
- Automatic renewal triggers 30 days before expiry by default and can be adjusted per task; the scheduler checks due tasks every 30 seconds.
- The apply node can enable "Do not store certificate private key on the server": the key stays in memory during issuance and deployment, is never written to the database, and cannot be downloaded. Auto-renewal reissues and redeploys; standard flows require at least one enabled deploy node.
- If a task fails, review the task log; credential, domain, and CA issues produce explicit errors.
- The certificate page supports search, status/source filtering (task issuance, ACME server, CSR request), details, and security analysis.
- Downloading chains and viewing or downloading private keys require a login session; for API calls, the chain requires `certificates:read` and the private key additionally requires `certificates:key`.
- Bulk cleanup filters by status, age, issuance time, domain, issuer, source, and "keep the latest certificate for each task"; affected task certificate references are cleared.

### Default configuration
"Public CA" supports configuring the ACME account email, default CA, default private key type, default renewal days, and EAB credentials for each CA.

### Target Flow Beta
Target Flow Beta is for certificates that follow panel websites. It reads real websites and bound domains from deployment targets through the plugin `plan` capability, groups them by certificate policy, issues certificates, and deploys them back.

- The task page and create/edit dialog separate flows with "All / Standard / Target Flow Beta" tabs.
- Multiple target sources are supported; the website list comes from the target, not manual site names.
- Allocation strategies: smart recommendation, one certificate per site, merge by base domain, merge all selected sites, one certificate per target source, and custom groups, with site selection, include/exclude rules, and extra domains per group.
- Renewals re-read target sites: "All sites" includes newly added sites, while "Selected sites only" keeps the saved stable site keys. A Beta task may own multiple certificates linked through `task_certificate_links`; the task list shows site/group summaries.
- Bulk cleanup with "keep the latest certificate for each task" skips Beta task certificates to avoid deleting other groups.

### Client CSR requests
- The API accepts a client CSR and completes DNS-01 validation and public CA issuance; the server never generates or stores the private key.
- Requests run asynchronously with status polling, cancellation, retry, and per-request chain download.
- Only ACME v2 public CAs and DNS-01 are supported; wildcard certificates require DNS-01.
- A dedicated `certificates:request` API key scope is required, and `csr.allowed_domains` can restrict requestable domains.

## DNS providers
Official DNS implementations are built in, covering about 235 entries (lego v5 plus compatible IDs), and all providers are disabled by default; enable them on the **Plugins** page.

- Credentials are encrypted before being written to the database; leaving a secret field empty while editing keeps the existing value.
- The `manual` provider requires adding TXT records by hand and is not part of automated workflows.
- Some providers such as Cloudflare, DNSimple, PowerDNS, and Volcengine support custom API upstream URLs for proxies, self-hosted services, or compatible gateways.

The provider drop-down on "DNS Accounts" is the authoritative list; see [DNS plugins](dns-plugins.md) for credentials, the full provider list, and troubleshooting.

## Task orchestration
Tasks consist of nodes; the default flow is "Request → Deploy (one or more) → Notify".

| Node | Key parameters |
| --- | --- |
| Request | Domains, DNS account (auto by domain bindings across accounts), CA, private key type, automatic renewal, renewal lead days |
| Deploy | Target credentials, certificate/private key paths, site/domain/certificate name, reload command |
| Notify | Channels, trigger timing (always / success only / failure only) |

- Nodes can be enabled/disabled and reordered; multiple deploy nodes deliver the same certificate to multiple targets.
- "Auto by domain binding" lets one certificate cover domains from multiple DNS accounts, each writing and cleaning TXT records through its own account; selecting an explicit account means it covers every domain.
- Old tasks without an explicit workflow derive an equivalent one from existing domains and target credentials; upgrades migrate deployment paths, sites, and domains to task deploy nodes before removing them from credentials.
- The task list supports select-all and multi-select for batch execution, pausing, or starting automatic renewal. Batch execution skips running tasks, and batch start/stop changes only scheduling state.
- Before execution, a still-valid certificate outside its renewal window is reused and only deployment runs; use "Force re-issue" or API `?force=1` for a new certificate. When the CA rate-limits issuance, the task enters "Rate limited" and retries at the CA-provided recovery time instead of every 6 hours.

## Certificate deployment
"Target credentials" store only authentication and connection information; certificate paths, reload commands, domains, and sites are configured on task deploy nodes, so one credential can be reused by multiple tasks.

### Built-in targets

| Target | Credentials | Deployment parameters |
| --- | --- | --- |
| Local directory | None | Certificate/private key paths, optional reload command |
| SSH / SFTP | Host, port, username, password or private key, host fingerprint | Remote certificate/private key paths, optional remote reload command |

SSH requires a target host fingerprint (`SHA256:...`) by default. TOFU is used only when "auto-trust host key on first connection" is explicitly enabled; later mismatches reject the connection.

### Plugin targets
FTP/FTPS (plain, explicit TLS, and implicit TLS), Webhook (callback URL, custom authentication headers, optional TLS verification skip), Alibaba Cloud CDN, Qiniu Cloud, BaoTa panel, 1Panel, SafeLine WAF, and others are built in; select the product and parameters under "Deployment target".

- Third-party helpers can still be uploaded for additional targets; they receive certificates and private keys and are equivalent to trusted code.
- Local directories and SSH are always available, and existing credentials are preserved when targets are disabled.

### Internal CA trust chain
Under "Settings → TLS trust chain" you can import internal CA public keys (PEM, optionally a root CA plus subordinates). Afterwards:

- Deployment targets validate internal HTTPS services against system roots plus imported CAs, without disabling TLS verification; certificate monitors trust these CAs, and certificates from this application's private CAs are added automatically.
- Deployments, reload commands, and plugin subprocesses read the merged bundle through `SSL_CERT_FILE`, with `CURL_CA_BUNDLE` and `REQUESTS_CA_BUNDLE` also supported.
- The bundle is stored as `trusted-ca-bundle.pem` in the data directory and can be downloaded from the settings page.

Only import internal CAs you control; a wrong CA weakens TLS validation, and removing a CA makes dependent targets untrusted again. Importing a CA fixes trust only: an expired, not-yet-valid, or mismatched certificate still needs a certificate or address change.

### Deployment records
- Each deployment generates a record, viewable in certificate details and logs.
- Manual deployments prefer the certificate task's deploy node parameters but can also take parameters for that target only.
- Local/SSH deployments execute reload commands; API keys need `deploy:execute` to trigger them.
- A failed deployment does not roll back the previous certificate on the target; retain the previous version or use an atomic release mechanism.

## Site certificate monitoring
After an external site is added, the service periodically reads its certificate over TLS:

- Targets support `example.com`, `example.com:8443`, or a full URL; the default port is `443`.
- Checks run every 24 hours by default and can be adjusted per monitor.
- The service validates hostname, issuer, expiration, and status; certificates with 30 days or less remaining are marked expiring soon, while expired certificates, connection failures, untrusted or incomplete chains, and hostname mismatches are abnormal.
- A notification is sent when status changes from normal to expiring soon, expired, or abnormal.
- Monitors validate the system trust chain to prevent a man-in-the-middle from masking real problems; administrator-imported internal CAs and private CAs are trusted.

## Notifications

### Built-in channels
- **Email (SMTP)**: server, port, username, password, sender, and SSL support.
- Recipients default to the system account email and can be replaced with custom recipients; after saving, email goes only to the configured recipients, without appending the account email.
- Implicit TLS is used by default (`smtp.ssl=true`); when it is disabled and the server lacks STARTTLS, sending is refused to prevent sensitive email such as reset tokens from being sent in plaintext.

### Plugin channels
Gotify, ServerChan, ntfy, GoCQHTTP, PushDeer, Bark, Telegram, DingTalk, WeCom bot, WeCom app, WeCom intelligent bot, iGot, PushPlus, WePlus, WxPusher, WxPusher SPT, OpenILink, WPush, Synology Chat, Feishu, PushMe, ChronoCat, and custom generic webhooks.

- Channels other than email are built in, can be enabled individually, and can send test messages.
- A task notification node can select specific channels or use all enabled channels, with trigger timing always, success only, or failure only.

### Security alerts
- Security alerts are independent of the certificate notification switch; disabling certificate email does not blind them. Alerts cover password/email changes, TOTP enable/disable, passkey add/delete, password resets, API key create/update/delete/rotate, and notification setting changes.
- A **new device** sign-in is reported by default; the same browser is reminded at most once per quiet window (30 days by default), adjustable or changeable to "every sign-in".
- Failed sign-ins are aggregated per account: 5 failures within 10 minutes trigger an alert, at most once per hour; unknown emails are aggregated by source IP.
- Email goes only to configured recipients (up to 10). The account email is filled in by default but can be replaced; later account email changes do not silently change it, and changing the email notifies both old and new addresses to prevent account takeover.
- Email and enabled notification channels are supported. Plugin channels receive the sign-in IP, device, and account email, so select only trusted channels. Messages never contain passwords, TOTP codes, API keys, or full passkey identifiers.
- An hourly alert limit (default 10) applies only to high-frequency sign-in/failure alerts; further events are recorded under "Recent security events" and combined into one digest at most once per hour. Critical credential changes, new-device sign-ins, email changes, policy changes, and test alerts bypass the limit.
- "Recent security events" shows event time, type, per-target status, and errors, and exports JSON. Policy changes notify previous recipients and channels using the old SMTP snapshot; events are retained for 90 days by default.
- If SMTP is not configured and only email is selected, alerts cannot be delivered; the UI warns, and failed events keep the error and retry with backoff.

## Private CA

### Generation
- Private key types: ECDSA P-256 / P-384, RSA 2048 / 4096; set the validity period as needed.
- Generate a root CA, or select "Intermediate/Issuing CA" to have an existing private CA sign a subordinate CA.
- Configure Subject (ST, L, OU, Email, custom serial number), Extensions (path length, Key Usage, Extended Key Usage, SAN DNS/IP, Name Constraints), and Infrastructure (parent CA, CRL distribution point, OCSP responder URL, CA issuer URL, certificate policy OID, CPS URL).
- SKI, AKI, and CA basic constraints are generated automatically; with a parent CA, validity and path length converge to the parent's permitted range.

### Import
- Paste a PEM-format CA certificate and private key.
- The system validates CA basic constraints, certificate signing usage, and key matching; for an intermediate CA, select a parent CA and the system validates the signature and establishes the hierarchy.

### Download and deletion
- Download the CA certificate, private key, or full chain (intermediate CA + parent chain).
- List APIs do not return certificate or private key contents; downloading a private key requires `private_cas:key`.
- Deletion removes the CA record and server-stored private key; delete subordinate CAs first.

### Limitations
- Private CA private keys are encrypted in the database but are not HSM/KMS-managed.
- Root and intermediate/issuing CAs can both issue in "Certificate Request"; the full chain is stored and deployed with the certificate.
- Certificates default to 365-day validity and never exceed the CA's expiration.
- CRL/OCSP URLs are written into new certificates, but revocation lists and online status services must be provided by external PKI infrastructure; existing certificates do not gain CDP/AIA extensions automatically and must be reissued.

## ACME server
The built-in ACME v2 server is enabled by default and exposes private CAs to clients such as certbot, lego, and acme.sh. It does not issue certificates until an endpoint is created and enabled.

### Endpoints
- Each endpoint is bound to one private CA, with a random URL slug such as `/acme/internal/directory`.
- Supports an EAB toggle, domain allowlists, and HTTP-01 / DNS-01 / TLS-ALPN-01 toggles.
- Can listen on a dedicated port with direct TLS and optional mTLS; certificate validity, order rate limits, and an HTTP-01 internal CIDR allowlist are configurable.

### Protocol capabilities
- Accounts store only public keys, never client private keys, and support account key rollover (`keyChange`).
- Supports ARI (RFC 9773) renewal suggestions: a renewal window about one third of the lifetime before expiry (at most 30 days); revoked or expired certificates return a past window to prompt immediate renewal. The ARI `replaces` field lets a new order declare the old certificate, with ownership, identifier overlap, and duplicate-replacement checks.
- Supports DNS and IP identifiers; IP identifiers allow only HTTP-01 / TLS-ALPN-01 and require an IP CIDR allowlist.
- Newly issued certificates include CRL Distribution Point and OCSP AIA extensions, with status at `/pki/{ca}/crl` and `/pki/{ca}/ocsp`. Private CAs must have the `cRLSign` usage, validated when an endpoint is enabled.

### Diagnostics and cleanup
- "ACME Diagnostics" shows account, order, authorization, challenge, and certificate status.
- Cleanup of invalid orders and long-unused accounts is supported by endpoint, status, and age; deleting an account removes its orders but not issued certificates.
- CRL/OCSP is organized per CA and should remain reachable after an ACME enrollment endpoint is disabled.

## Dashboard and logs

### Dashboard
The UI supports Simplified Chinese and English and can be switched in settings. The home page summarizes total certificates and expiring/expired counts, task and monitor status, deployment success rate, and expiry distribution and issuance trends.

### Logs: Operation
- Shares the Misc → Logs page with system logs via tabs; records task execution, certificate issuance, deployments, notifications, monitors, and settings changes.
- Filters by source, level, keyword, and time range, with optional auto refresh; keywords match raw stored content and parameter values (such as task names and domains), not localized template text.
- Exports CSV or JSON with the same filters as the list; CSV includes a UTF-8 BOM for Excel/WPS.
- Can be cleared with the separate sensitive scope `logs:write` and UI confirmation; when archiving is enabled, logs are written to `data/archives/logs/` first, and the clear action is written to the audit log.
- Export is limited to the most recent 90 days and follows the browser time zone by default, falling back to the server `TZ`.

### Logs: System
- Shares the Misc → Logs page via a tab; filtered from process logs and written asynchronously to the database.
- Filters by level, component, keyword, and time range; exports CSV/JSON and clears with the same archiving behavior, writing a clearing entry to the operation log.
- Records `info` and above by default, retaining 7 days or 20,000 rows; HTTP access logs are not written to the database by default and can be adjusted in `log.system`.

### Logs: Archive
- A third tab on the Misc → Logs page. Manual clears, the 90-day operation-log cleanup, and system-log retention/max-row cleanup create an archive before deletion.
- An archive consists of `*.ndjson.gz` and `*.manifest.json`, preserving raw fields, row count, ID/time ranges, trigger reason, operator, SHA-256, and file size.
- Filter by category, reason, keyword, and time range; download or delete archives. Archives are kept forever by default; `log.archive.retention_days`, `max_total_mb`, and `quota_policy` control cleanup.
- Archive failure blocks deletion: manual clears return an error and automatic cleanup retries on the next maintenance run.
- The archive directory is not included in the default ZIP backup and must be copied separately; administrators with data-directory access can delete archives, so this is not tamper-proof auditing.

## Capability boundaries

| Boundary | Description |
| --- | --- |
| Public certificate validation | DNS-01 only; HTTP-01 is not supported for public CA requests |
| Multi-user | Single administrator account; no multi-user support or RBAC |
| Identity federation | No LDAP/OIDC/SAML/SCIM |
| High availability | No built-in clustering or active-active; SQLite uses a single writer |
| Private key custody | Local encrypted storage; no HSM/KMS |
| Monitoring output | No Prometheus metrics endpoint; logs and an authenticated API are provided |
| Deployment rollback | No automatic rollback of previous certificates on targets; versioning must be retained on the target side |
