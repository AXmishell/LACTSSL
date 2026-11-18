# API Integration

LACTSSL console endpoints can be called through either a browser login session or an API key. API keys are intended for long-running programs such as CI/CD, monitoring inspections, CMDB synchronization, and certificate distribution. Each key can be configured independently with permissions, expiry, and enabled state, avoiding shared administrator passwords.

> Sensitive operations such as account security, passkeys, and session management can only be called through a browser login session; API keys cannot change the login password, register passkeys, or sign out other sessions.

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
curl -H "Authorization: Bearer lactssl_xxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```

```bash
curl -H "X-API-Key: lactssl_xxxxxxxxxxxxxxxx" \
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

Keys use the format of a random string prefixed with `lactssl_`. The server stores only a digest, so a database leak cannot directly recover the plaintext key.

Create request example:

```bash
curl -X POST \
  -H "Authorization: Bearer lactssl_xxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI deployment",
    "scopes": ["tasks:read", "tasks:run", "deploy:execute"],
    "expires_at": "2027-01-01T00:00:00Z"
  }' \
  https://ssl.example.com/api/api-keys
```

Leaving `expires_at` empty means the key never expires; never-expiring keys are not recommended for production. Creating and modifying keys are high-risk operations; browser sessions rely on the login identity, while API key calls rely on the `api_keys:write` permission.

### 3.2 Rotation and Disabling

- After rotation, the old token is invalidated immediately.
- After disabling or deletion, all calls using the key return `401`.
- When managing other keys through an API key, the key cannot grant permissions it does not itself have, nor modify, rotate, or delete a key with higher permissions than its own.

### 3.3 Safekeeping

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
| `certificates:request` | Submit a client CSR and let the server complete authoritative CA validation and issuance | Yes |
| `deploy:execute` | Deploy certificates manually, or create, modify, or run tasks that contain deploy nodes | Yes |
| `tasks:read` | View task lists and task logs | No |
| `tasks:write` | Create, modify, delete, enable, and disable tasks (also requires `deploy:execute` when deploy nodes are included) | No |
| `tasks:run` | Run tasks manually or in batches (also requires `deploy:execute` when deploy nodes are included) | No |
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
| `logs:read` | View and export operation logs | No |
| `system_logs:read` | View and export process logs | No |
| `system_logs:write` | Delete all system logs | Yes |
| `settings:read` | View system settings | No |
| `settings:write` | Modify system settings and send test notifications | Yes |
| `backup:read` | View backup status and size | No |
| `backup:download` | Export encrypted backups | Yes |
| `backup:restore` | Upload and restore backups; restarts the service | Yes |
| `upgrade:read` | View database migration and program upgrade status | No |
| `upgrade:write` | Run migrations, optimize the database, and replace the program | Yes |
| `security:write` | Modify the secure entry | Yes |
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
  "https://ssl.example.com/api/logs?page=1&size=50&level=error&source=task"
```

`size` has a maximum of 200.

### 6.2 Running a Task

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/run
```

If the task contains enabled deploy nodes, the key must also have `deploy:execute`.

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

### 6.6 Requesting an Authoritative CA Certificate from a Client CSR

The server acts as an intermediary: it accepts a client CSR, completes DNS-01 validation with the configured authoritative CA and DNS account, and returns the issued certificate. The server never generates or stores the private key.

Creating a request requires the `certificates:request` scope:

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

- Phase one supports DNS-01 only. Wildcard certificates require DNS-01.
- `ca` must be an ACME upstream from the "Authoritative CA" page, not a private CA.
- The server strictly validates the CSR signature, key strength, canonical SANs, common name, and internal domains. The server cannot modify a signed CSR.
- Provide an `idempotency_key`. Only one in-flight request is allowed per CSR and CA.
- Use the `csr.allowed_domains` setting to restrict requestable domains. Separate multiple domains with commas or whitespace.
- `key_available` is `false` for these certificates, so private-key download and private-key-dependent deployments are disabled.

The repository includes a ready-to-use Python client, `scripts/csr_client.py`. It generates the private key and CSR locally, submits the request, polls for completion, and verifies that the downloaded certificate matches the private key:

```bash
export LACTSSL_URL="https://ssl.example.com"
export LACTSSL_TOKEN="lactssl_xxxxxxxxxxxxxxxx"

python3 scripts/csr_client.py \
  --domains example.com,www.example.com \
  --dns-account-id 3 \
  --ca letsencrypt \
  --out-dir ./certs
```

The script uploads only the CSR; the private key never leaves the client. Outputs include `.key`, `.csr`, `.crt`, `.fullchain.crt`, and `.request.json`. If the request was created but the download was interrupted, resume with `--request-id csr_xxxxxxxx`.

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

- The API has no idempotency key; repeated calls to `run` trigger the task again, so scripts should deduplicate themselves.
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
