# API Integration

LACTSSL console endpoints can be called through either a browser login session or an API key. API keys are intended for long-running programs such as CI/CD, monitoring inspections, CMDB synchronization, and certificate distribution. Each key can be configured independently with permissions, expiry, and enabled state, avoiding shared administrator passwords.

> Sensitive operations such as account security, passkeys, session management, backup download/restore, entry changes, and database switching can only be called through a browser login session; API keys cannot complete interactive re-authentication. Changing SMTP/notification targets requires `settings:write`, while sending test messages remains browser-session only.

## 1. Choosing an Integration Approach

| Scenario | Recommended approach | Rationale |
| --- | --- | --- |
| Manual issuance, troubleshooting, configuration changes | Browser console | Full audit and interactive confirmation are available |
| CI/CD-triggered renewal/deployment | API key + `tasks:run` / `deploy:execute` | Least privilege, rotatable |
| Certificate inventory synchronization to CMDB | API key + read-only permissions | Private keys are not exposed |
| Certificate download and distribution | API key + `certificates:key` + `deploy:execute` | Highly sensitive; requires additional network restrictions |
| Monitoring inspections | API key + `system:read` / `monitors:read` | Read-only, short expiry |
| Log audit | API key + `logs:read` / `system_logs:read` | Read-only, export to SIEM |

API keys are not multi-user RBAC. Where separation of duties is required, implement approval and proxied calls through enterprise IAM, a bastion host, or an upstream platform.

## 2. Authentication

Place the key in the `Authorization: Bearer` or `X-API-Key` request header:

```bash
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```

```bash
curl -H "X-API-Key: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```

Key points:

- Endpoints are always under `/api`; the legacy `/<entry>/api` is retained as a compatibility alias.
- Requests still require authentication by login session or API key and do not depend on the secure entry.
- API key requests do not rely on cookies, so the `X-LACTSSL-CSRF` header is not required.
- Use HTTPS for all requests; keys must not appear in URL query parameters.
- Response headers include `X-Request-ID`; provide it to the vendor or operations team when troubleshooting.

## 3. API Key Lifecycle

### 3.1 Creating a Key

1. Sign in to the console with an administrator account.
2. Open the **API Keys** page and click **Create key**.
3. Enter a name and expiry, and select permissions as needed.
4. The page displays the full key once; after closing the dialog it cannot be viewed again and can only be rotated.

Keys are 86-character Base64URL random strings (64 bytes of entropy) with no fixed prefix. The server stores only a digest, so a database leak cannot directly recover the plaintext key.

Create request example:

```bash
curl -X POST \
  -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI deployment",
    "scopes": ["tasks:read", "tasks:run", "deploy:execute"],
    "expires_at": "2027-01-01T00:00:00Z",
    "allowed_ips": ["203.0.113.10", "2001:db8::/48"],
    "allowed_dns_account_ids": [1, 3]
  }' \
  https://ssl.example.com/api/api-keys
```

Leaving `expires_at` empty means the key never expires; never-expiring keys are not recommended for production. `allowed_ips` is optional and empty means unrestricted; when set, it accepts individual IPv4/IPv6 addresses or CIDR ranges, and requests from other IPs return `403`. `allowed_dns_account_ids` is optional and empty means unrestricted; when set, the key can only issue certificates, run tasks, and manage DNS configuration with the listed accounts. Certificates spanning multiple accounts require every resolved account to be in the list. Creating and modifying keys are high-risk operations; browser sessions rely on the login identity, while API key calls rely on the `api_keys:write` permission.

### 3.2 IP Restrictions

- Each key can have its own source IP/CIDR allowlist; browser sessions are unaffected.
- Empty means unrestricted. Accepted forms include `203.0.113.10`, `10.0.0.0/8`, and `2001:db8::/48`.
- Authentication uses the real client IP restored by a reverse proxy. Behind a proxy, configure the actual proxy addresses under **Settings → Config file → Trusted proxies**. Without trusted proxies, `X-Forwarded-For` and `X-Real-IP` are ignored.
- `0.0.0.0/0` and `::/0` are rejected to prevent accidental misconfiguration; leave the list empty for unrestricted access.
- When managing other keys through an API key, the key cannot create, modify, or rotate a key with a broader IP allowlist than its own.
- The key list shows the source IP of the most recent successful call. IP denials are available from the **Logs** action on each key, filtered to denial events by default.
- Proxy misconfiguration, dynamic egress IPs, or mixed old/new versions can weaken or bypass the allowlist. Use **Add current IP** to confirm the outbound address before enabling it.
- If the server is behind a CDN or reverse proxy and the real IP cannot be restored yet, use **WebRTC probe** in the create/edit dialog to view the current browser's public egress IP. The browser connects to public STUN servers for this; the result may differ from the IP actually used by API clients, so treat it only as a diagnostic hint and not as a replacement for trusted proxy configuration.

### 3.3 Rotation and Disabling

- After rotation, the old token is invalidated immediately.
- After disabling or deletion, all calls using the key return `401`.
- When managing other keys through an API key, the key cannot grant permissions, a broader IP allowlist, or a broader DNS account scope than it has, nor modify, rotate, or delete a key with higher permissions or a broader scope than its own.

### 3.4 Safekeeping

- Store keys in the enterprise password manager or secrets management system; do not write them to code repositories, images, or logs.
- Issue a separate key for each system; include the system name and environment in the key name.
- Set a reasonable expiry and rotate before it expires.
- If compromise is suspected, rotate or delete the key immediately and review operation logs for unusual calls.

## 4. Permission Model

Permissions are divided by resource:action. `*` represents all system API capabilities, including all sensitive operations.

| Permission | Description | Sensitive |
| --- | --- | --- |
| `*` | All system API capabilities | Yes |
| `profile:read` | View the account that owns the current key | No |
| `system:read` | View version, status, and metadata | No |
| `dashboard:read` | View dashboard statistics | No |
| `certificates:read` | View certificates, download certificate chains, and analyze certificates | No |
| `certificates:write` | Clean up and delete certificates | No |
| `certificates:key` | View or download certificate private keys | Yes |
| `certificates:request` | Submit a client CSR and let the server complete public CA validation and issuance, or sign with a private CA (`private_cas:write` is also required) | Yes |
| `deploy:execute` | Deploy certificates manually, or create, modify, or run tasks that contain deploy nodes (writes files; command execution requires `deploy:command`) | Yes |
| `deploy:command` | Allow local/SSH deployments to run reload commands (equivalent to executing arbitrary commands as the service account) | Yes |
| `tasks:read` | View task lists and task logs | No |
| `tasks:write` | Create, modify, delete, enable, and disable tasks, and clear task execution logs (creating an enabled task also requires `tasks:run`; deploy nodes require `deploy:execute`; private CAs require `private_cas:write`) | No |
| `tasks:run` | Run tasks manually or in batches (deploy nodes require `deploy:execute`; private CAs require `private_cas:write`) | No |
| `monitors:read` | View site certificate monitors | No |
| `monitors:write` | Create, modify, delete, and immediately check monitors | No |
| `dns_accounts:read` | View DNS provider accounts (secrets masked) | No |
| `dns_accounts:write` | Create, modify, and delete DNS provider accounts | Yes |
| `deploy_accounts:read` | View deployment target accounts (secrets masked) | No |
| `deploy_accounts:write` | Create, modify, and delete deployment target accounts | Yes |
| `deployments:read` | View certificate deployment history | No |
| `ca:read` | View ACME CAs, endpoints, accounts, and orders | No |
| `ca:write` | Modify ACME CAs, endpoints, and EAB, and clean up diagnostic data | Yes |
| `private_cas:read` | View private CA certificates and certificate chains | No |
| `private_cas:write` | Create, import, and delete private CAs | Yes |
| `private_cas:key` | Download private CA private keys | Yes |
| `plugins:read` | View plugins, the plugin repository, and enabled status | No |
| `plugins:write` | Upload, install, update, uninstall, and enable plugins | Yes |
| `logs:read` | View and export operation logs; view/download log archives | No |
| `logs:write` | Delete all operation logs (the clear action itself is audited) | Yes |
| `system_logs:read` | View and export process logs; view/download log archives | No |
| `system_logs:write` | Delete all system logs and log archives | Yes |
| `settings:read` | View system settings and the internal CA trust chain | No |
| `settings:write` | Modify general system settings (SMTP/notification targets; test messages remain browser-session only) | Yes |
| `backup:read` | View backup status and size | No |
| `backup:download` | Export encrypted backups (browser session only; retained for legacy keys) | Yes |
| `backup:restore` | Upload and restore backups; restarts the service (browser session only; retained for legacy keys) | Yes |
| `upgrade:read` | View database migration and program upgrade status | No |
| `upgrade:write` | Run migrations, optimize the database, and replace the program | Yes |
| `security:write` | Modify trust roots and plugin trust anchors (entry changes are browser-session only) | Yes |
| `api_keys:read` | View the API key list and permissions | No |
| `api_keys:write` | Create, modify, rotate, and delete API keys | Yes |

### 4.1 Recommended Role Mapping

| Role | Permissions |
| --- | --- |
| Read-only inspection | `system:read`, `dashboard:read`, `certificates:read`, `tasks:read`, `monitors:read`, `deployments:read` |
| CI/CD renewal | `tasks:read`, `tasks:run`; add `deploy:execute` when deploy nodes are included |
| Certificate distribution | `certificates:read`, `certificates:key`, `deploy:execute` |
| Log audit | `logs:read`, `system_logs:read` |
| Monitoring | `system:read`, `monitors:read` |

Private key permissions are additive:

- Downloading a certificate private key also requires `certificates:read`.
- Downloading a private CA private key also requires `private_cas:read`.
- Creating or running tasks with deploy nodes requires explicit `deploy:execute`; `certificates:write` and `tasks:*` do not imply deployment execution capability.
- Local/SSH `reload_command` additionally requires `deploy:command`; `deploy:execute` alone can only write certificate files.
- Tasks that issue from a private CA require `private_cas:write`; `tasks:write` does not imply private CA issuance.
- Creating a task with `enabled:true` issues immediately and therefore also requires `tasks:run`.

## 5. Response Format

Success:

```json
{
  "success": true,
  "data": {}
}
```

Failure:

```json
{
  "success": false,
  "code": "api.error.api_key_scope_denied",
  "message": "API key is missing the required permission: certificates:read",
  "params": { "scope": "certificates:read" }
}
```

Common status codes:

| Status code | Meaning |
| --- | --- |
| `400` | Invalid parameters or configuration validation failure |
| `401` | Unauthenticated; the key does not exist, is disabled, has expired, or has been deleted |
| `403` | Insufficient permissions, or the endpoint can only be called by a browser session |
| `404` | Resource not found or incorrect path |
| `409` | State conflict (for example, already running or duplicate operation) |
| `429` | Too many requests; retry according to `Retry-After` |
| `500` | Server error; record `X-Request-ID` and investigate |

## 6. Common Endpoints

### 6.1 Viewing Status and Certificates

```bash
# Process status (requires system:read)
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/status

# Certificate list (requires certificates:read)
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificates
```

List endpoints return arrays directly; log endpoints return a paginated object:

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/logs?page=1&size=50&level=error&source=task&q=demo"
```

`size` has a maximum of 200; `q` matches the raw stored content and parameter values. Use `DELETE /api/logs` to clear operation logs; it requires `logs:write`.

When `log.archive.enabled` is enabled, clearing writes an archive before deleting logs and the response includes an `archived` object. An archive failure returns 500 and leaves the logs intact.

Log archive endpoints:

```bash
# List: supports page, size, category, reason, from, to, q
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives?page=1&size=20&category=operation"

# Download an archive file
curl -fsS -OJ -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>/download"

# Delete an archive
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>"
```

Archive endpoints accept either `logs:*` or `system_logs:*` scopes: view/download need either read scope, delete needs either write scope. `category` is `operation` or `runtime`; `reason` is `manual_clear`, `retention`, or `max_rows`.

### 6.2 Running a Task

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/run
```

If the task contains enabled deploy nodes, the key must also have `deploy:execute`.

By default, a certificate that is still valid and outside its renewal window is reused, and only deployment nodes are executed; no new order is sent to the CA. Use `?force=1` or send `{"force":true}` to force re-issuance. Forced issuance consumes the CA's certificate quota.

List a task's execution logs (requires `tasks:read`):

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```

Clear a task's execution logs (requires `tasks:write`):

```bash
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```

Clearing removes that task's issuance and deployment logs, returns the number of deleted rows as `deleted`, and leaves an audit record in the operation log.

### 6.3 Downloading Certificates

```bash
# Full chain (requires certificates:read)
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.fullchain.pem \
  "https://ssl.example.com/api/certificates/42/download?type=fullchain"

# Private key (requires certificates:read + certificates:key)
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.key \
  "https://ssl.example.com/api/certificates/42/download?type=key"
```

`type` supports `fullchain` (default), `cert`, and `key`. Download endpoints return raw PEM, not wrapped in JSON; save it with appropriate file permissions and do not write it to logs.

### 6.4 Site Monitoring

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors

curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors/7/check
```

### 6.5 API Key Management

| Endpoint | Method | Permission | Description |
| --- | --- | --- | --- |
| `/api-keys` | GET | `api_keys:read` | List keys without returning plaintext tokens |
| `/api-keys/scopes` | GET | `api_keys:read` | Get the permission list and localized descriptions |
| `/api-keys` | POST | `api_keys:write` | Create a key; `token` is returned only once in this response |
| `/api-keys/:id` | PUT | `api_keys:write` | Modify the name, permissions, enabled state, and expiry |
| `/api-keys/:id/rotate` | POST | `api_keys:write` | Rotate the key; the old token is invalidated immediately |
| `/api-keys/:id` | DELETE | `api_keys:write` | Delete a key |

### 6.6 Requesting a Certificate from a Client CSR

The server acts as an intermediary: it accepts a client CSR and issues the certificate either by completing DNS-01 validation with the configured public CA and DNS account, or by signing directly with a private CA managed by the server. The server never generates or stores the private key.

Creating a request requires the `certificates:request` scope. Using a private CA additionally requires `private_cas:write`:

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

For a private CA, set `ca` to `private-ca:<id>` or pass `private_ca_id`. Private CA requests do not use DNS-01, `dns_account_id`, or the ACME contact email, and internal domains such as `app.internal` are allowed:

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

`dns_account_id` is optional for public CA requests. When an administrator declares domains under "DNS Accounts → Zone bindings", the server selects the account by longest suffix match and reports `resolved_by` / `resolved_zone` in the response. Different domains may resolve to different accounts: the response includes `dns_routes` with the account used for each domain, and issuance writes TXT records per route. Only unbound domains, or one domain matching multiple equal-rank accounts, are rejected. An explicit `dns_account_id` still means that account covers every domain in the CSR.

The endpoint returns `202 Accepted` with a request ID. Poll the request until it is issued:

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx
```

Once the status is `issued`, download the certificate chain. Private keys are never available:

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx/certificate?type=fullchain" \
  -o example.com.fullchain.crt
```

Notes:

- Public CA requests support DNS-01 only. Wildcard certificates require DNS-01. Private CA requests are signed directly and do not use a challenge.
- `ca` is a public ACME upstream from the "Public CA" page, or `private-ca:<id>` for a private CA. Private CA issuance also requires `private_cas:write` and enforces the CA's ExtKeyUsage and NameConstraints.
- The server strictly validates the CSR signature, key strength, canonical SANs, and common name. Internal domains are rejected for public CA requests but allowed for private CA requests. The server cannot modify a signed CSR.
- Provide an `idempotency_key`. Only one in-flight request is allowed per CSR, CA, and DNS route set for the same caller.
- Use the `csr.allowed_domains` setting to restrict requestable domains. Separate multiple domains with commas or whitespace. Private CA requests skip this allowlist when it is empty and rely on the CA's NameConstraints; once configured, the allowlist is enforced for both CA types.
- `key_available` is `false` for these certificates, so private-key download and private-key-dependent deployments are disabled.

Zone binding and resolution preview endpoints:

| Method | Path | Description |
| --- | --- | --- |
| GET | `/api/dns-accounts/:id/zones` | List the zone bindings declared for an account |
| PUT | `/api/dns-accounts/:id/zones` | Atomically replace the bindings; a zone can belong to only one enabled account |
| POST | `/api/dns-accounts/:id/zones/probe` | Probe the zone with a read-only verification and backfill it; requires `dns_accounts:write` |
| POST | `/api/dns-accounts/resolve` | Preview which account each domain resolves to, without outbound requests |

Example:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"domains":["a.example.com","b.other.net"]}' \
  https://ssl.example.com/api/dns-accounts/resolve
```

The response includes `routes` and `account_ids`. `account_id` is only set when every domain uses the same account; otherwise it is `0`.

The repository includes a ready-to-use Python client, `scripts/csr_client.py`. It generates the private key and CSR locally, submits the request, polls for completion, and verifies that the downloaded certificate matches the private key:

```bash
export LACTSSL_URL="https://ssl.example.com"
export LACTSSL_TOKEN="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

python3 scripts/csr_client.py \
  --domains example.com,www.example.com \
  --dns-account-id 3 \
  --ca letsencrypt \
  --out-dir ./certs

# Private CA request (the private key is still generated locally)
python3 scripts/csr_client.py \
  --domains app.internal,api.internal \
  --private-ca-id 2 \
  --out-dir ./certs
```

You can also use the interactive wizard to fill in each value (running with no arguments in a terminal starts it automatically):

```bash
python3 scripts/csr_client.py --interactive
```

Renewal, status, and batch operations:

```bash
# Renewal: create a new CSR with the existing key (the key is never copied or modified)
python3 scripts/csr_client.py \
  --key ./certs/example.com.key \
  --domains example.com,www.example.com \
  --out-dir ./certs \
  --force

# Or renew directly from the previous state file (reuses key, CA, DNS account, and output name)
python3 scripts/csr_client.py --renew-from ./certs/example.com.request.json

# Status / cancel (--json is convenient for scripts)
python3 scripts/csr_client.py --status csr_xxxxxxxx --json
python3 scripts/csr_client.py --cancel csr_xxxxxxxx --yes
python3 scripts/csr_client.py --list --json

# Batch: sequential execution, stops on the first failure by default
python3 scripts/csr_client.py --manifest ./certs.json --json
```

Batch manifest format:

```json
{
  "defaults": {"ca": "letsencrypt", "dns_account_id": 3, "out_dir": "./certs"},
  "certificates": [
    {"domains": ["example.com", "www.example.com"]},
    {"domains": ["api.example.com"], "name": "api", "key_type": "rsa2048"}
  ]
}
```

The script uploads only the CSR; the private key never leaves the client. Outputs include `.key`, `.csr`, `.crt`, `.fullchain.crt`, and `.request.json`. Locally generated keys and CSRs are first staged as `.new` files and only atomically replace the final files after the certificate has been downloaded and verified, so a failed request never destroys the previous working set. The `.request.json` state is written immediately after the request is created; even if the connection drops or `--quiet` is used, resume with `--request-id csr_xxxxxxxx`, which also discovers the previous `.key.new`/`.key`. Transient network errors are retried 3 times by default (`--retries`), retryable server failures trigger one automatic `/retry` (`--auto-retry`), and HTTP 429 honors `Retry-After`. Progress goes to stderr; with `--json`, stdout contains only the final JSON. Tokens can be read via `LACTSSL_TOKEN_FILE` or `--token-file` to keep them out of command-line arguments. The client UI supports `zh-CN`, `zh-TW`, `en-US`, `ja-JP`, and `ru-RU` via `--lang` or `LACTSSL_LANG`; API errors are requested in the same language. Exit codes: 0 success, 1 generic failure, 2 argument/configuration, 3 authentication/authorization, 4 network, 5 server-side request failure, 6 local verification failure, 130 cancelled by the user.

### 6.7 Verifying Credential Availability

After adding or editing a DNS account or target credential, you can verify that the credential actually works. These endpoints require `dns_accounts:write` / `deploy_accounts:write`. Browser sessions no longer need to re-authenticate with the current password or TOTP. Results are advisory and never block saving or running tasks.

When `domain` is omitted, a saved DNS account uses one of its enabled domain bindings first; if there are none, the response uses `mode: "scope"` and only checks the credential configuration and call permission.

`mode` accepts `auto` (default), `read`, and `write`. `auto` prefers a read-only probe and falls back to a write test. A new DNS helper performs the temporary TXT create and delete in a single helper process, reusing the provider instance, authentication, and connections; older helpers that do not advertise `verify-write-v1` still use separate `present`/`cleanup` processes. The write test only creates a random TXT record under `_acme-challenge` and deletes it immediately; it never deploys or runs reload commands.

| Method | Path | Description |
| --- | --- | --- |
| POST | `/api/dns-accounts/:id/verify` | Verify a saved DNS account; accepts `domain` and `mode` |
| POST | `/api/dns-accounts/verify` | Verify an unsaved DNS configuration; secrets must be supplied explicitly |
| POST | `/api/deploy-accounts/:id/verify` | Verify a saved target credential; accepts `deploy_config` |
| POST | `/api/deploy-accounts/verify` | Verify an unsaved target configuration; secrets must be supplied explicitly |
| POST | `/api/deploy-accounts/:id/options` | Read dynamic deployment field options with a saved credential, such as the BaoTa website list |
| POST | `/api/deploy-accounts/:id/match` | Match target websites by certificate domains; returns `field`, `multiple` and `values` |

Example:

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"domain":"example.com","mode":"auto"}' \
  https://ssl.example.com/api/dns-accounts/3/verify
```

The dynamic options endpoint is only available for deployment fields that declare `options_source` in the manifest, and API keys need the `deploy_accounts:write` scope. Supply the field name and current deployment parameters; the server reads the credential from the saved target:

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"field":"sites","config":{"target":"docker"}}' \
  https://ssl.example.com/api/deploy-accounts/3/options
```

It returns `{"options":[{"label":"site name","value":"site name"}]}`. If the plugin is unavailable or the panel is unreachable, the response contains an explicit error and the UI keeps manual input available.

The response contains per-check results, so callers can tell authentication failures from missing zone permissions or unwritable directories:

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

Behavior:

- DNS verification defaults to `auto`: when the plugin declares `verify-v1`, it performs a read-only zone listing and domain ownership check; otherwise it falls back to a write test that creates and immediately removes a random `_acme-challenge` TXT record. `mode=read` forces read-only and returns `unsupported` when the plugin cannot do it.
- Saved accounts use only the configuration stored in the database. Unsaved configurations must include secrets explicitly; stored secrets are never reused, so changing an endpoint cannot redirect a credential to another server.
- Target credential verification supports built-in `local`/`ssh`: it connects and authenticates, creates and removes a temporary file in the target directory, and never executes `reload_command`. Other deployment plugins return `unsupported` until they implement the `verify` capability.
- Pass `deploy_config` to verify the scope of a specific deployment node (site, directory, bucket, or domain). This is the recommended way to check whether newly granted permissions are usable.

## 7. CI/CD Examples

### 7.1 Distributing Certificates After Renewal

```bash
#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${LACTSSL_URL:?}"
TOKEN="${LACTSSL_TOKEN:?}"

# Trigger the task (the task ID is provided by CMDB or configuration management)
curl -fsS -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  "${BASE_URL}/api/tasks/${TASK_ID}/run"

# After the task completes, download and distribute by certificate ID
curl -fL \
  -H "Authorization: Bearer ${TOKEN}" \
  -o /run/secrets/example.com.fullchain.pem \
  "${BASE_URL}/api/certificates/${CERT_ID}/download?type=fullchain"
```

Notes:

- The API has no idempotency key; repeated calls to `run` execute the task again. If the certificate is still valid, issuance is skipped and only deployment runs, but scripts should still deduplicate themselves.
- Task runs are asynchronous; when completion must be awaited, poll the task list or subscribe to notifications. Do not assume that a POST response means the task has completed.
- Downloading private keys requires additional permissions; most distribution scenarios only need certificate chains, with the deployment node writing the private key directly to the target. Relaying private keys through CI is not recommended.

### 7.2 Inspection

```bash
#!/usr/bin/env bash
set -euo pipefail

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/status" >/dev/null

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/certificates" |
  jq -e '[.data[] | select(.not_after < (now + 14*86400 | todate))] | length == 0'
```

(The example uses `jq`; actual field names are determined by the API response.)

## 8. Error Handling and Retries

- For `429`, back off according to `Retry-After`; do not retry immediately.
- For `5xx`, use exponential backoff and record `X-Request-ID`.
- For `401`, do not retry; check whether the key has expired, been disabled, or been rotated.
- For `403`, check permissions and endpoint restrictions (some endpoints can only be called by browser sessions).
- For `400`/`409`, do not retry automatically; correct the parameters or state first.
- Requests involving issuance or deployment may produce external side effects; query the task or deployment status before retrying.

## 9. Security Recommendations

- Create a separate key for each automation task; do not share keys.
- Grant only the permissions the task actually needs; do not grant `*` to read-only tasks.
- Set a reasonable expiry and rotate keys regularly.
- Do not expose full keys in logs, screenshots, or public repositories.
- If compromise is suspected, disable or delete the key immediately; rotation invalidates the old key at once.
- Restrict call sources at the network layer to prevent a leaked key from being used from arbitrary addresses.
- Correlate API call logs with operation logs, retaining the caller, time, and `X-Request-ID`.

## Related Documentation

- [Administration](administration.md): key governance and separation of duties.
- [Security baseline](security.md): authentication, auditing, and network controls.
- [Monitoring and troubleshooting](troubleshooting.md): 429, 401, task failures, and related issues.
