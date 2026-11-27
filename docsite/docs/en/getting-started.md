# Quick start

Start an instance with Docker, initialize it, and issue your first certificate. Most deployments can be completed in about 30 minutes; for production hardening, see the [security baseline](security.md).

## Prerequisites

- A Linux host that runs Docker (VPS, NAS, or small machine)
- A domain and DNS API credentials
- A deployment target (local directory, SSH, or panel)

## Start the instance

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
docker compose up -d
docker compose logs -f lactssl
```

The repository uses the external `1panel-network` by default. Without 1Panel, delete both `networks` blocks in `docker-compose.yml` or run `docker network create 1panel-network` first. The GHCR image is available from the first stable Release; otherwise build locally as described in [Installation](install.md).

## Initialize

The startup log prints the secure entry and a one-time initialization token:

```text
Secure entry: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
Initialization token (valid for this startup only): ...
```

Create the administrator through the secure entry, enable TOTP or a passkey, and set a recovery email. The token expires immediately after initialization; if you lose the entry, set `security.entry` in `config.yaml` and restart.

> The secure entry is not a security boundary. Production still requires HTTPS, a strong password, TOTP/passkeys, and network access control.

## Enable plugins

The official DNS, deployment, and notification plugins are built in:

1. Open **Plugins** and confirm the categories are enabled.
2. Enable the DNS providers, deployment targets, or notification channels you need.
3. Upload a helper only for third-party extensions; see [Plugin governance](plugins.md).

DNS providers are disabled by default; deployment targets are enabled by default.

## Issue your first certificate

Start with a private CA, which does not depend on public DNS:

1. Create a root CA under **Private CA** (ECDSA P-256).
2. Create a task under **Certificate request** with that CA and a test domain.
3. Add a **Deploy certificate** node, choose **Local directory**, and fill in the certificate/key paths and an optional reload command.
4. Add a **Notify** node, choose an enabled channel, and set the trigger to **Always**.
5. Run the task manually and confirm the certificate, deployment record, and notification.

Then add a public chain: create a DNS account → choose a public CA → validate a test domain with DNS-01 → deploy to the target → observe one automatic renewal.

## Verify

| Action | How to verify |
| --- | --- |
| Backup and restore | Download an encrypted backup and import it into an isolated instance |
| Logs | Filter and export operation and system logs |
| API | Call `/api/certificates` with a read-only key |
| Notifications | Send a test notification |
| Monitoring | Add an external site and read its certificate |

## Production rollout

Validate with 1–2 real domains, one deployment target, and one internal CA scenario. Before production use, complete the checklist in the [security baseline](security.md), and follow the [operations handbook](operations.md) for backup, monitoring, and change management.

Common failure causes:

- Treating the secure entry as the security boundary.
- Using a personal DNS account as the API credential.
- Not verifying the reload command after the certificate is updated.
- Backing up only the database and not `config.yaml`; without `salt`, encrypted credentials cannot be decrypted.
