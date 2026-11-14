# Features

LACTSSL splits the certificate lifecycle into modules: request certificates with ACME, deploy them to targets, send notifications, and monitor certificates on external endpoints. Certificate requests, deployment, and notifications can be combined into a single task workflow.

## Certificate issuance and renewal

- Uses **DNS-01** validation only, supporting single-domain, multi-domain, and wildcard certificates.
- Built-in certificate authorities:
  - Let's Encrypt
  - ZeroSSL (requires EAB)
  - Google Trust Services (requires EAB)
  - Buypass
  - LiteSSL (requires EAB)
- Private key types: `ecdsa` (default), `ecdsa384`, `rsa2048`, `rsa4096`.
- Automatic renewal: triggered 30 days before expiry by default and adjustable per task; the background scheduler checks for due tasks every 30 seconds.
- The certificate management page supports search, status filtering, details, and security analysis. It can also clean up certificates in one go by cleanup scope (expired, revoked, or all), expiry/revocation age, issuance time, domain, issuer, source, and "keep the latest certificate for each task". Viewing private keys and downloading certificates in the details view does not require secondary verification.
- "Authoritative CA" lets you configure the ACME account email, default CA, default private key type, default renewal days, and EAB credentials for each CA.
- "Authoritative CA" supports adding multiple **generic ACME upstreams**: each can have its own ACME directory URL, ACME v1 / v2 protocol version, and optional EAB credentials. Generic upstreams appear in the CA selection lists for tasks and default CA; ACME v1 only supports DNS-01 validation and does not support EAB.
- The issuer for a certificate request can be either a public ACME CA or a root CA or intermediate/issuing CA from "Private CA". When a private CA is selected, the system issues the certificate directly without DNS-01 validation; certificates default to a 365-day validity and never outlive the CA.

## ACME server

The built-in ACME v2 server is enabled by default and can expose private CAs to clients such as certbot, lego, and acme.sh (it does not issue certificates until an endpoint is created and enabled):

- Each endpoint is bound to one private CA and uses a random URL slug, for example `/acme/internal/directory`.
- Supports an EAB toggle, domain allowlists, and HTTP-01 / DNS-01 / TLS-ALPN-01 capability toggles.
- Can listen on a dedicated port, with direct TLS and optional mTLS.
- The "ACME Server" page configures endpoints, issuing CA, EAB, domain allowlists, challenge types, HTTP-01 internal CIDRs, certificate validity, and order rate limits, and can create/delete EAB credentials.
- The "ACME Diagnostics" page shows account, order, authorization, challenge, and certificate status to help troubleshoot client issues. It can also clean up invalid orders and long-unused accounts in one go by endpoint, status, and age; deleting an account also deletes its historical orders but does not affect issued certificates.
- Supports account key rollover (`keyChange`) and ARI (RFC 9773) renewal suggestions; by default ARI offers a renewal window about one third of the lifetime before expiry (at most 30 days), and returns a past window for revoked or expired certificates to prompt immediate renewal.
- Supports the ARI `replaces` field: a new order can declare the old certificate it replaces. The server validates ownership, identifier overlap, and duplicate replacement, and echoes the field in the order response.
- Accounts only store public keys, never client private keys.
- Implements orders, authorizations, HTTP-01 / DNS-01 / TLS-ALPN-01 challenges, finalize issuance, and certificate download.
- Supports DNS and IP identifiers; IP identifiers only allow HTTP-01 / TLS-ALPN-01 and require an IP CIDR allowlist.
- Newly issued certificates include CRL Distribution Point and OCSP AIA extensions; revocation status can be checked at `/pki/{ca}/crl` and `/pki/{ca}/ocsp`.
- Private CAs must have the `cRLSign` usage, which is validated when an ACME endpoint is enabled. Existing certificates do not automatically gain CDP/AIA extensions and must be reissued.

## Private CA

"Private CA" manages certificate authorities for internal use:

- **Generate**: choose a private key type (ECDSA P-256 / P-384, RSA 2048 / 4096) and validity period to generate a root CA automatically; you can also choose "Intermediate/Issuing CA" to have an existing private CA sign a subordinate CA.
- **Import**: paste PEM-formatted CA certificates and private keys. Import validates the CA basic constraints, certificate signing usage, and whether the certificate matches the private key; when importing an intermediate CA you can also select a parent CA, and the system validates the signature and builds the hierarchy.
- **Download**: download the CA certificate, private key, or full certificate chain (intermediate CA + parent CA chain) for internal service issuance and trust configuration.
- **Delete**: remove the CA record and private key stored on the server; when subordinate CAs exist, delete them first.

When generating a CA you can expand "Advanced options" to configure the full set of X.509 fields and PKI parameters:

- **Certificate subject**: ST, L, OU, Email, and a custom certificate serial number.
- **X.509 extensions**: path length constraint, Key Usage, Extended Key Usage, SAN (DNS / IP), and Name Constraints (allowed/excluded DNS domains and IP ranges).
- **PKI infrastructure**: parent CA, CRL distribution point, OCSP responder URL, CA issuer URL, certificate policy OID, and CPS URL.
- Subject Key Identifier (SKI), Authority Key Identifier (AKI), CA basic constraints, and similar fields are generated automatically.
- When a parent CA is selected, validity and path length automatically converge to the range allowed by the parent CA.

Private CA keys are encrypted before being written to the database, and list APIs never return certificate or private key contents. Both root CAs and intermediate/issuing CAs can be used as issuers in "Certificate Request", and the full issued chain is stored and deployed with the certificate. CRL / OCSP distribution URLs are written into certificates; the actual revocation list and online status services must be provided by external PKI infrastructure.

## DNS providers

Add credentials under "DNS Accounts" first, then select them in request tasks. All DNS providers built into lego are supported (about 190; `manual` requires adding the TXT record by hand and is not part of the automated flow). The provider selector is searchable; common providers are listed below:

| Provider | Main credentials |
| --- | --- |
| Alibaba Cloud DNS (alidns) | AccessKey ID / Secret, optional Region |
| Alibaba Cloud ESA (aliesa) | AccessKey ID / Secret, optional Region |
| Cloudflare | API Token, or Email + Global API Key, optional API upstream URL |
| DNSPod | API Token |
| Tencent Cloud (tencentcloud) | SecretId / SecretKey, optional Region |
| Huawei Cloud (huaweicloud) | AccessKey ID / SecretAccessKey / Region |
| Volcengine (volcengine) | AccessKey / SecretKey, optional Region, API host, and scheme |
| Baidu AI Cloud (baiducloud) | AccessKey ID / SecretAccessKey |
| JD Cloud (jdcloud) | AccessKey ID / AccessKey Secret, optional Region |
| DigitalOcean | Token |
| Vultr | API Key |
| Linode | Token |
| Hetzner | API Key |
| Gandi | API Key |
| GoDaddy | API Key / Secret |
| Namecheap | API User / API Key, optional Source IP |
| NameSilo | API Key |
| Porkbun | API Key / Secret API Key |
| Spaceship | API Key / API Secret |
| ClouDNS | Auth ID / Sub Auth ID / Auth Password |
| Bunny DNS | API Key |
| Rainyun | API Key |
| West.cn (westcn) | Username / Password |
| PowerDNS (pdns) | API URL / API Key, optional server name and API version |
| DNSimple | OAuth Token, optional API upstream URL |
| DNS Made Easy | API Key / Secret, optional Sandbox |
| AWS Route 53 | AccessKey / SecretAccessKey, optional Region, Hosted Zone, private zone |
| Google Cloud DNS | Service account JSON, optional project ID |
| Azure DNS | Tenant ID / Client ID / Client Secret / Subscription ID / resource group |
| Netlify | Token |
| Vercel | API Token, optional team ID |
| DuckDNS | Token |
| FreeMyIP | Token |
| Gandi LiveDNS v5 | Personal Access Token |
| RFC 2136 dynamic update | DNS server, optional TSIG key / secret / algorithm and zone list |

The remaining providers are also backed by lego's built-in implementations, and their form fields are generated from lego environment variables. The provider selector in "DNS Accounts" is the authoritative full list.

Credentials are encrypted before being written to the database. Leaving a secret field empty while editing an account keeps the existing value.

Providers that support a custom API endpoint, such as Cloudflare, DNSimple, PowerDNS, and Volcengine, expose a Base URL, API URL, or API host field. Use it for proxies, self-hosted services, or compatible gateways.

## Certificate deployment

"Target credentials" only store authentication and connection details, such as host, port, username, password, panel URL, and API token. Parameters such as certificate paths, reload commands, deployment domains, and sites are configured on the "Certificate Request → Certificate Workflow → Deploy Certificate" node, so one credential can be reused by multiple tasks. Certificates can be deployed automatically after a successful issuance, or triggered manually.

Deployment targets built into the main program:

- **Local directory**: fill in certificate and private key paths on the deploy node, with an optional reload command (such as `systemctl reload nginx`).
- **SSH / SFTP**: the credential stores host, port, username, and password or private key; the deploy node specifies remote certificate/private key paths and an optional remote reload command.

The following targets are provided by [deployment plugins](deploy-plugins.md) and become available after installing and enabling the official helper:

- **FTP / FTPS**: the credential stores host, port, username, and password; plain FTP, explicit TLS, and implicit TLS (FTPS) are supported, and the deploy node specifies remote certificate/private key paths.
- **Webhook**: the credential stores the callback URL, custom authentication headers, and whether to skip TLS verification.
- **Alibaba Cloud CDN**: the credential stores the AccessKey; the deploy node specifies the domain to update and a custom certificate name.
- **Qiniu Cloud**: the credential stores AccessKey / SecretKey; the deploy node selects the domain type (CDN accelerated domain or Kodo custom origin domain) and specifies the domains and certificate name. HTTPS is enabled automatically for CDN domains that have not enabled it.
- **BaoTa panel**: the credential stores the panel URL and API Secret; the deploy node selects the deploy target (website or the panel itself), and website mode specifies the website name.
- **1Panel**: the credential stores the panel URL, API Key, and API version; the deploy node selects the deploy target (website or the panel itself). Website mode specifies the website domain, alias, or ID; panel mode selects Enable / Mux.
- **SafeLine WAF**: the credential stores the panel URL and API Token; the deploy node can specify a certificate ID, or leave it empty to match by certificate domain.

Every deployment creates a deployment record that can be viewed in certificate details and logs. Manual deployments prefer reusing the deployment node parameters of the certificate's task, and can also fill in parameters for that specific target.

Plugins run as one-shot subprocesses and receive the certificate and private key; they are disabled by default and only trusted helpers should be enabled. When no plugin is installed or enabled, only local files and SSH appear as deployment types; existing credentials are preserved.

## Notifications

Built-in channels:

- **Email (SMTP)**: server, port, username, password, sender, and SSL support.
- **Gotify**: server URL, token, and priority.
- **ServerChan**: SendKey, with Turbo / Server酱³ / auto-detection support.

Extended channels:

ntfy, GoCQHTTP, PushDeer, Bark, Telegram, DingTalk, WeCom bot, WeCom app, WeCom intelligent bot, iGot, PushPlus, WePlus, WxPusher, WxPusher SPT, OpenILink, WPush, Synology Chat, Feishu, PushMe, ChronoCat, and generic custom webhooks.

Channels can be enabled individually and send test messages. A task's notification node can select specific channels or use all enabled channels; trigger timing supports always, success only, and failure only.

## Endpoint certificate monitoring

After adding an external endpoint, the service periodically reads its certificate over TLS:

- Targets support `example.com`, `example.com:8443`, or a full URL; the default port is `443`.
- Checks run every 24 hours by default and can be adjusted per monitor.
- Validates the certificate hostname and records issuer, expiry, and status.
- Marks certificates expiring within 30 days as expiring soon; expired certificates, connection failures, or hostname mismatches are marked as abnormal.
- Sends a notification when status changes from normal to expiring soon, expired, or abnormal.

## Task workflow

A task's execution flow consists of nodes, defaulting to "Request → Deploy (one or more) → Notify":

- **Request**: domains, DNS account, CA, private key type, automatic renewal, and renewal lead days.
- **Deploy**: choose a target credential and fill in certificate paths, sites, domains, and other parameters for this deployment.
- **Notify**: choose channels and trigger timing.

Nodes can be enabled/disabled and reordered. When an old task has no explicit workflow, an equivalent workflow is derived automatically from its existing domains, target credentials, and other fields. During upgrades, deployment paths, sites, and domains from old credentials are migrated to task deploy nodes first, and then deployment parameters are removed from credentials.

The task list supports select-all in the header and multi-select, allowing batch run, pause, or resume of automatic renewal for selected tasks. Batch runs skip tasks that are already running, and batch start/stop only changes scheduling state without interrupting an in-progress issuance.

## Dashboard and logs

The dashboard summarizes the total number of certificates, expiring/expired counts, task and monitor status, deployment success rate, expiry distribution, and issuance trends. Task runs, deployments, notifications, monitoring, and settings changes are all written to the operation log, which can be filtered by source, level, and time range in the UI and exported as CSV or JSON. A separate System Logs page shows low-level process logs from startup, HTTP, scheduling, plugins, and ACME, filterable by level, component, keyword, and time range, and exportable as CSV or JSON.

## Interface

Simplified Chinese and English are supported and can be switched in Settings. "System Settings → Backup and Restore" can download a password-encrypted ZIP backup (including the database and configuration file) or upload a backup to restore system data; backups can also be imported directly from the advanced options on `/install` during first-time setup. See [Security](security.md) for security-related capabilities.
