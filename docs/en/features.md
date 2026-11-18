# Features

This document describes LACTSSL capabilities by business domain, including key parameters and limitations, to support solution design and acceptance. Detailed procedures are covered in [Getting started and rollout](getting-started.md), [Administration](administration.md), and [API integration](api.md).

## Capability map

| Capability domain | Highlights | Typical users |
| --- | --- | --- |
| Certificate assets | Public CA requests, automatic renewal, certificate details, and cleanup | Platform/Operations |
| Task orchestration | Request → Deploy (one or more) → Notify | Platform/Operations |
| Certificate delivery | Local directory, SSH/SFTP, FTP, webhooks, cloud CDN, control panels | Platform/Operations, business systems |
| Internal PKI | Private CA generation/import, hierarchy, CRL/OCSP URLs | Security, infrastructure |
| ACME server | Standards-compliant ACME v2 endpoints for internal clients | Internal platforms, devices, test environments |
| Monitoring and alerting | External site certificate monitoring, task/deployment notifications | Operations, on-call |
| Audit and recovery | Operation logs, system logs, encrypted backups | Security, audit, operations |
| Integration | API keys, REST API, automation | Integration development |

## 1. Certificate issuance and renewal

### 1.1 Validation methods

- Public CA requests use **DNS-01** validation only and support single-domain, multi-domain, and wildcard certificates.
- DNS-01 requires provider API credentials configured under "DNS Accounts"; tasks create and clean up TXT records automatically at runtime.
- Private CA issuance does not use ACME validation; the system signs certificates directly.
- The built-in ACME server supports HTTP-01, DNS-01, and TLS-ALPN-01 for internal clients.

### 1.2 Built-in certificate authorities

| CA | Notes |
| --- | --- |
| Let's Encrypt | Default public CA; no EAB required |
| ZeroSSL | Requires EAB |
| Google Trust Services | Requires EAB |
| Buypass | Public CA |
| LiteSSL | Requires EAB |

"Authoritative CA" also lets you add multiple **generic ACME upstreams**:

- Configure the ACME directory URL, ACME v1/v2 protocol version, and optional EAB.
- Generic upstreams appear in the task and default CA selection lists.
- Custom directory URLs must use HTTPS; only local loopback may use HTTP.
- ACME v1 supports DNS-01 only and does not support EAB.

### 1.3 Keys and renewal

- Private key types: `ecdsa` (default), `ecdsa384`, `rsa2048`, `rsa4096`.
- Automatic renewal triggers 30 days before expiry by default and can be adjusted per task.
- The background scheduler checks for due tasks every 30 seconds.
- If a task fails, review the task log for the cause; credential, domain, and CA issues produce explicit errors.
- The certificate management page supports search, status filtering, details, and security analysis.
- Downloading certificate chains and viewing or downloading private keys require a login session; for API calls, downloading the chain requires `certificates:read`, while viewing or downloading the private key additionally requires `certificates:key`.
- Bulk cleanup can filter by status (expired, revoked, or all), age, issuance time, domain, issuer, source, and "keep the latest certificate for each task"; after cleanup, certificate references in affected tasks are cleared.

### 1.4 Default configuration

"Authoritative CA" supports configuring:

- ACME account email.
- Default CA.
- Default private key type.
- Default renewal days.
- EAB credentials for each CA.

### 1.5 Client CSR requests

- The API accepts a client CSR and acts as an intermediary to complete DNS-01 validation and issuance with an authoritative CA.
- The server never generates or stores the private key; the certificate binds the public key from the client CSR, and the client keeps the private key.
- Requests run asynchronously and support status polling, cancellation, retry, and per-request certificate chain download.
- Only ACME v2 authoritative CAs and DNS-01 are supported; wildcard certificates require DNS-01.
- A dedicated `certificates:request` API key scope is required, and `csr.allowed_domains` can restrict requestable domains.

## 2. DNS providers

DNS providers are supplied by DNS plugins. The main program does not include any provider; the provider list appears only after a plugin is installed and enabled.

Common providers and credentials:

| Provider | Main credentials |
| --- | --- |
| Alibaba Cloud DNS (alidns) | AccessKey ID / Secret, optional Region |
| Alibaba Cloud ESA (aliesa) | AccessKey ID / Secret, optional Region |
| Cloudflare | API Token, or Email + Global API Key, optional API upstream URL |
| DNSPod | API Token |
| Tencent Cloud (tencentcloud) | SecretId / SecretKey, optional Region |
| Huawei Cloud (huaweicloud) | AccessKey ID / SecretAccessKey / Region |
| Volcengine (volcengine) | AccessKey / SecretKey, optional Region, API host, and scheme |
| AWS Route 53 | AccessKey / SecretAccessKey, optional Region, Hosted Zone, private zone |
| Google Cloud DNS | Service account JSON, optional project ID |
| Azure DNS | Tenant ID / Client ID / Client Secret / Subscription ID / resource group |
| West.cn (westcn) | Username / Password |
| PowerDNS (pdns) | API URL / API Key, optional server name and API version |
| RFC 2136 dynamic update | DNS server, optional TSIG key / secret / algorithm and zone list |

- After the official DNS plugins are installed, all lego providers are supported (about 190).
- The `manual` provider requires adding TXT records by hand and is not part of automated workflows.
- Credentials are encrypted before being written to the database; leaving a secret field empty while editing keeps the existing value.
- Cloudflare, DNSimple, PowerDNS, Volcengine, and others support custom API upstream URLs for proxies, self-hosted services, or compatible gateways.
- The provider drop-down on the "DNS Accounts" page is the authoritative full list.

For plugin installation, offline mirrors, and supply chain governance, see [Plugin governance](plugins.md) and [DNS plugins](dns-plugins.md).

## 3. Task orchestration

Tasks consist of nodes; the default flow is "Request → Deploy (one or more) → Notify".

| Node | Key parameters |
| --- | --- |
| Request | Domains, DNS account, CA, private key type, automatic renewal, renewal lead days |
| Deploy | Target credentials, certificate/private key paths, site/domain/certificate name, reload command |
| Notify | Channels, trigger timing (always / success only / failure only) |

Key points:

- Nodes can be enabled/disabled and reordered; a task can contain multiple deploy nodes to deliver the same certificate to multiple targets.
- When an old task has no explicit workflow, the system derives an equivalent workflow from its existing domains, target credentials, and other fields.
- During upgrades, deployment paths, sites, and domains from old credentials are migrated to task deploy nodes before the deployment parameters are removed from credentials.
- The task list supports header select-all and multi-select for batch execution, pausing, or starting automatic renewal.
- Batch execution skips tasks that are already running; batch start/stop only changes scheduling state and does not interrupt an issuance already in progress.

## 4. Certificate deployment

"Target credentials" store only authentication and connection information; certificate paths, reload commands, deployment domains, sites, and similar parameters are configured on task deploy nodes. As a result, one credential can be reused by multiple tasks.

### 4.1 Built-in targets

| Target | Credentials | Deployment parameters |
| --- | --- | --- |
| Local directory | None | Certificate/private key paths, optional reload command |
| SSH / SFTP | Host, port, username, password or private key, host fingerprint | Remote certificate/private key paths, optional remote reload command |

SSH requires a target host fingerprint (`SHA256:...`) by default. TOFU is used only when "auto-trust host key on first connection" is explicitly enabled; subsequent fingerprint mismatches cause the connection to be rejected.

### 4.2 Plugin targets

The following targets are provided by deployment plugins and become available after installation and activation:

- FTP / FTPS: supports plain FTP, explicit TLS, and implicit TLS.
- Webhook: callback URL, custom authentication headers, and whether to skip TLS verification.
- Alibaba Cloud CDN, Qiniu Cloud, BaoTa panel, 1Panel, SafeLine WAF, and others.
- Select the specific product and parameters under "Deployment target" on the task deploy node.

Deployment plugins receive certificates and private keys and are equivalent to trusted code; they are disabled by default. When no plugin is installed, only local files and SSH are shown; existing credentials are preserved.

### 4.3 Internal CA trust chain

Under "Settings → TLS trust chain" you can import internal CA public keys (PEM, optionally including both a root CA and subordinate CAs). After importing:

- Deployment plugin targets validate internal HTTPS services against the system roots plus imported CAs, without disabling TLS verification.
- Certificate monitors trust certificates issued by these CAs; certificates from this application's private CAs are added automatically.
- Deployments, reload commands, and plugin subprocesses read the merged trust bundle through `SSL_CERT_FILE`, with `CURL_CA_BUNDLE` and `REQUESTS_CA_BUNDLE` also supported.
- The bundle is stored as `trusted-ca-bundle.pem` in the data directory and can be downloaded from the settings page.

Only import internal CAs you actually control; a wrong CA weakens TLS validation. Removing a CA makes targets that rely on its certificates untrusted again.

### 4.4 Deployment records

- Each deployment generates a deployment record, viewable in certificate details and logs.
- Manual deployments prefer the deploy node parameters of the certificate's task; parameters can also be entered for that target only.
- Local/SSH deployments execute reload commands; API keys must explicitly have `deploy:execute` to trigger them.
- A failed deployment does not automatically roll back the previous certificate on the target system; retain the previous version on the target side or use an atomic release mechanism.

## 5. Site certificate monitoring

After an external site is added, the service periodically reads its certificate over a TLS connection:

- Targets support `example.com`, `example.com:8443`, or a full URL; the default port is `443`.
- Checks run every 24 hours by default and can be adjusted per monitor.
- The service validates the certificate hostname, issuer, expiration, and status.
- Certificates with 30 days or less remaining are marked as expiring soon; expired certificates, connection failures, untrusted chains, incomplete chains, or hostname mismatches are marked as abnormal.
- A notification is sent when the status changes from normal to expiring soon, expired, or abnormal.
- Monitors validate the system trust chain to prevent a man-in-the-middle from using a self-signed certificate to mask real certificate problems; internal CAs imported by the administrator and private CAs are also trusted.

## 6. Notifications

### 6.1 Built-in channels

- **Email (SMTP)**: server, port, username, password, sender, and SSL support.
- Implicit TLS is used by default (`smtp.ssl=true`); when implicit TLS is disabled and the server does not support STARTTLS, the service refuses to send, preventing sensitive email such as reset tokens from being transmitted in plaintext.

### 6.2 Plugin channels

Gotify, ServerChan, ntfy, GoCQHTTP, PushDeer, Bark, Telegram, DingTalk, WeCom bot, WeCom app, WeCom intelligent bot, iGot, PushPlus, WePlus, WxPusher, WxPusher SPT, OpenILink, WPush, Synology Chat, Feishu, PushMe, ChronoCat, and custom generic webhooks.

- Channels other than email are provided by notification plugins.
- Channels can be enabled individually and send test messages.
- A task notification node can select specific channels or use all enabled channels.
- Trigger timing supports always, success only, and failure only.

## 7. Private CA

Manage internal certificate authorities under "Private CA".

### 7.1 Generation

- Private key types: ECDSA P-256 / P-384, RSA 2048 / 4096.
- Set the validity period as needed.
- You can generate a root CA, or select "Intermediate/Issuing CA" to have an existing private CA sign a subordinate CA.
- The following full X.509 fields and PKI parameters can be configured during generation:
  - Subject: ST, L, OU, Email, custom serial number.
  - Extensions: path length, Key Usage, Extended Key Usage, SAN (DNS/IP), Name Constraints (allowed/excluded DNS domains and IP ranges).
  - Infrastructure: parent CA, CRL distribution point, OCSP responder URL, CA issuer URL, certificate policy OID, and CPS URL.
- SKI, AKI, and CA basic constraints are generated automatically.
- When a parent CA is selected, validity and path length automatically converge to the range permitted by the parent CA.

### 7.2 Import

- Paste a PEM-format CA certificate and private key.
- Validate CA basic constraints, certificate signing usage, and whether the certificate matches the private key.
- When importing an intermediate CA, you can select a parent CA; the system validates the signature and establishes the hierarchy.

### 7.3 Download and deletion

- Download the CA certificate, private key, or full certificate chain (intermediate CA + parent CA chain).
- List APIs do not return certificate or private key contents; downloading a private key requires `private_cas:key`.
- Deletion removes the CA record and private key stored on the server; if subordinate CAs exist, delete them first.

### 7.4 Limitations

- Private CA private keys are encrypted before being written to the database, but they are not HSM/KMS-managed.
- Both root CAs and intermediate/issuing CAs can serve as issuers in "Certificate Request"; the full certificate chain is stored and deployed with the certificate.
- Certificates default to a 365-day validity and never exceed the CA's expiration.
- CRL/OCSP distribution URLs are written into new certificates, but the actual revocation lists and online status services must be provided by external PKI infrastructure.
- Existing certificates do not automatically gain CDP/AIA extensions and must be reissued.

## 8. ACME server

The built-in ACME v2 server is enabled by default and can expose private CAs to clients such as certbot, lego, and acme.sh. It does not issue certificates until an endpoint is created and enabled.

### 8.1 Endpoints

- Each endpoint is bound to one private CA.
- A random URL slug is used, for example `/acme/internal/directory`.
- Supports an EAB toggle, domain allowlists, and HTTP-01 / DNS-01 / TLS-ALPN-01 capability toggles.
- Can listen on a dedicated port, with direct TLS and optional mTLS.
- Certificate validity, order rate limits, and an HTTP-01 internal CIDR allowlist can be configured.

### 8.2 Protocol capabilities

- Accounts store only public keys, never client private keys.
- Supports account key rollover (`keyChange`).
- Supports ARI (RFC 9773) renewal suggestions: by default a renewal window is provided about one third of the lifetime before expiry (at most 30 days); revoked or expired certificates return a past window to prompt immediate renewal.
- Supports the ARI `replaces` field: a new order can declare the old certificate it replaces, and the server validates ownership, identifier overlap, and duplicate replacement.
- Supports DNS and IP identifiers; IP identifiers allow only HTTP-01 / TLS-ALPN-01 and require an IP CIDR allowlist.
- Newly issued certificates include CRL Distribution Point and OCSP AIA extensions; revocation status can be checked at `/pki/{ca}/crl` and `/pki/{ca}/ocsp`.
- Private CAs must have the `cRLSign` usage; this is validated when an ACME endpoint is enabled.

### 8.3 Diagnostics and cleanup

- The "ACME Diagnostics" page shows account, order, authorization, challenge, and certificate status.
- Cleanup of invalid orders and long-unused accounts is supported by endpoint, status, and age.
- Deleting an account removes its historical orders but does not affect issued certificates.
- CRL/OCSP is organized per CA; it should remain reachable after an ACME enrollment endpoint is disabled (if clients need to check revocation).

## 9. Dashboard and logs

### 9.1 Dashboard

The UI supports Simplified Chinese and English and can be switched in settings. The home page summarizes:

- Total certificates and expiring/expired counts.
- Task and monitor status.
- Deployment success rate.
- Expiry distribution and issuance trends.

### 9.2 Logs: Operation

- Shares the Misc → Logs page with system logs, switched via tabs.
- Records task execution, certificate issuance, deployments, notifications, monitors, and settings changes.
- Supports filtering by source, level, and time range.
- Can export CSV or JSON; CSV includes a UTF-8 BOM and opens directly in Excel/WPS.
- Export is limited to the most recent 90 days.
- Export times follow the browser time zone by default; if unavailable, the server `TZ` is used.

### 9.3 Logs: System

- Shares the Misc → Logs page with operation logs, switched via tabs.
- Filtered from process logs and written asynchronously to the database.
- Can be filtered by level, component, keyword, and time range.
- Can be exported as CSV/JSON and cleared; clearing writes an entry to the operation log.
- Records `info` and above by default, retaining 7 days or 20,000 rows.
- HTTP access logs are not written to the database by default and can be adjusted in `log.system`.

## 10. Capability boundaries

| Boundary | Description |
| --- | --- |
| Public certificate validation | DNS-01 only; HTTP-01 is not supported for public CA requests |
| Multi-user | Single administrator account; no RBAC or organizational structure |
| Identity federation | No LDAP/OIDC/SAML/SCIM |
| High availability | No built-in clustering or active-active; SQLite uses a single writer |
| Private key custody | Local encrypted storage; no HSM/KMS |
| Monitoring output | No Prometheus metrics endpoint; logs and an authenticated API are provided |
| Deployment rollback | No automatic rollback of previous certificates on targets; versioning must be retained on the target side |
| Compliance certification | No MLPS/ISO certificates are included; enterprises must complete their own assessment |

## Related documentation

- [DNS plugins](dns-plugins.md)
- [Deployment plugins](deploy-plugins.md)
- [Notification plugins](notify-plugins.md)
- [API integration](api.md)
- [Security baseline](security.md)
