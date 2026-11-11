# Rah Panel

[فارسی](README.md)

Rah Panel is a standalone panel for managing users and multiple Xray servers. It is built with Go, PostgreSQL, and React/TypeScript. The panel interface is in Persian, and users connect with existing clients.

## Features

- User and node-group management, expiration, quotas, suspension, and revocable subscriptions.
- Administrator authentication with bcrypt and HttpOnly/SameSite session cookies.
- Node enrollment and worker installation over SSH with host fingerprint verification. SSH passwords and keys are kept out of the database and persistent queue.
- Cloudflare DNS-only records, DNS-01 certificate issuance and renewal, and a separate mTLS identity per node.
- VLESS/REALITY, VLESS/WS/TLS, and Trojan/TLS templates with configurable ports per node. The panel tests authentication with Xray and sends an HTTPS request through the tunnel before publishing each connection method.
- A systemd agent reports usage every 30 seconds. A persistent outbox and PostgreSQL deduplication prevent double counting.
- Local expiration enforcement and an independent watchdog with a maximum uptime allowance of 295 seconds, leaving time to stop within five minutes.
- Administrator audit logs exclude traffic contents and destination history.

## Installation

On Ubuntu 24.04, run:

```bash
curl -fsSL https://raw.githubusercontent.com/moeendehqan/rahpanel/main/bootstrap.sh | sudo bash -s -- --domain panel.example.com
```

Point your domain at the server IP and open TCP ports 80, 443, and 9443. Omit `--domain` to enter the hostname interactively. If curl is missing, run `sudo apt-get update && sudo apt-get install -y curl ca-certificates`. Use `--ref TAG` to install a specific release. Bootstrap fetches the source from GitHub and installs to `/opt/rahpanel`. Running it again preserves the existing source and secrets.

From an existing checkout, run:

```bash
sudo bash install.sh --domain panel.example.com
```

The installer provisions Docker and Compose from the official repository on Ubuntu or Debian when needed, builds the image, generates secrets, and starts PostgreSQL, the panel, and Caddy. Initial dependency and Xray downloads require internet access. Use `--no-install-docker` if Docker and Compose are ready. Rerunning the installer preserves configuration and volumes.

Caddy obtains the public HTTPS certificate automatically. Check public DNS and HTTPS separately from container health. The agent endpoint on 9443 must be directly reachable with a DNS-only record. Use `--agent-host agents.example.com` for a separate hostname.

Read the initial credentials privately:

```bash
sudo cat /opt/rahpanel/bootstrap-admin.txt
```

The installer creates the administrator when the database first starts and does not print the credentials. Keep the credential file offline and protect the `MASTER_KEY` from `.env`. Store the key separately from the database backup. Encrypted data requires the original key for recovery.

After signing in, complete the Cloudflare setup with the base domain for nodes, Zone ID, a token restricted to that zone with DNS Edit and Zone Read, and an ACME email address. The installer supplies the panel and agent URLs. Set a suitable REALITY TLS destination and matching SNI, then verify them. Let's Encrypt issues a separate certificate for each node and applies rate limits. Use staging for practice. The panel publishes connections after verification with a publicly trusted certificate.

See the [installation and troubleshooting guide](docs/installation.md).

## Management commands

Run from `/opt/rahpanel` or your installation directory.

```bash
sudo bash rah.sh status
sudo bash rah.sh logs
sudo bash rah.sh restart
sudo bash rah.sh stop
sudo bash rah.sh start
sudo bash rah.sh backup
sudo bash rah.sh restore backups/rah-DATE.dump
sudo bash rah.sh update
```

Fetch the desired source version before running `update`, which backs up the database, builds the current checkout, and restarts the services. Use `rah.sh stop` to preserve data. Running `docker compose down --volumes` deletes the persistent database and Caddy volumes.

For automatic deployment from `main`, configure these GitHub Actions secrets under **Settings → Secrets and variables → Actions**:

- `DEPLOY_HOST` and `DEPLOY_USER` identify the panel server and SSH account.
- `DEPLOY_SSH_KEY` is a dedicated private key without a passphrase for noninteractive deployment. Add the matching public key to the deployment user's `authorized_keys`.
- `DEPLOY_KNOWN_HOSTS` contains the SSH host key verified through a trusted channel.

GitHub Actions must be able to reach the server, and the SSH user needs access to Git and Docker in the existing installation. After the tests and Docker checks pass, the workflow backs up the database, builds the release, and checks panel health.

The image build fetches a pinned Xray version from its official release and verifies its checksum. The templates were tested with `v25.3.6`. Changing versions requires compatibility testing and a new release. Nodes keep their pinned version until updated.

## Adding a node

Use Ubuntu 24.04 on amd64 or arm64 with at least 512 MB of free disk space, a reachable IP address, and SSH access as root or with noninteractive sudo. Open the selected TCP ports for REALITY, WebSocket, and Trojan and allow outbound HTTPS to the panel and internet. The installer checks port ownership. Configure the firewall and provider security groups separately.

For a node on the panel host, select the shared-server option in the add-server form. Its default ports are 2087 for REALITY, 8443 for WebSocket, 2053 for Trojan, and 10086 for local statistics. The panel reserves 80, 443, and 9443. You can change node ports after a preflight conflict. After activating the node identity, reinstallations retain its ports even if connection verification failed. Keep the statistics API on loopback. On reinstall, Xray ports must be free or owned by the same `rah-xray.service`.

On a standalone node's first install, the panel can share TCP 443 between REALITY and sites served by standard Ubuntu Nginx using SNI. The subscription still uses port 443. Local ports 2087 and 8444 must be free and the node SNI must differ from the site names. The installer backs up modified files under `/etc/rahpanel/nginx-before-reality` and restores them if configuration validation or reload fails. Sites served this way see the local Nginx connection address. Unsupported layouts and other port conflicts fail preflight.

Enter the node name, IP address, and group in **Servers**. Compare the SSH fingerprint with the provider console before entering credentials. Track progress in the installation operations view. Failed installs can be retried. A panel restart interrupts incomplete installations and requires re-entering SSH credentials. Reinstalling the same node revokes its previous identity and can disconnect open sessions.

The reference clients are v2rayNG and v2rayN. Add the subscription URL and enable TUN/VPN mode for device-wide coverage. System proxy mode covers only traffic routed through it. Automatic switching depends on the client.

## Backup and maintenance

```bash
bash scripts/backup.sh
bash scripts/restore.sh backups/rah-DATE.dump
```

Restore requires the original `MASTER_KEY`. The database stores the node CA and ACME key encrypted. The restore script asks for confirmation before replacing the current database. Back up `.env`, Caddy certificates, and domain settings separately, and retain the CA and master key for recovery.

The panel renews node web certificates before expiration. The node mTLS identity and endpoint certificate also renew automatically. The previous node identity remains valid for 24 hours to recover from a lost renewal response. Reinstall nodes that remain offline past certificate expiry.

An accounting failure stops and quarantines the agent, and restarting it preserves the quarantine. Inspect `journalctl -u rah-agent`, disk health, and agent state. Reconcile usage before clearing `/etc/rahpanel/accounting-blocked` and the `accounting_blocked` state field.

## Development and verification

```bash
go test ./...
go vet ./...
cd web
npm ci --legacy-peer-deps
npm test
npm run build
```

Point `TEST_DATABASE_URL` at a disposable PostgreSQL database. Tests clear their tables, and API tests use a separate schema.

```bash
TEST_DATABASE_URL='postgres://rah:test@localhost:5432/rah_test?sslmode=disable' go test ./...
RUN_CAPACITY_TEST=1 TEST_DATABASE_URL='postgres://rah:test@localhost:5432/rah_test?sslmode=disable' go test ./internal/store -run TestControlPlaneCapacity -v
```

The panel reads `DATABASE_URL`, `MASTER_KEY`, `ADMIN_USERNAME`, `ADMIN_PASSWORD`, `PUBLIC_URL`, and `AGENT_URL` from the environment. Set `INSECURE_DEV=true` for local HTTP development. Compose loads `.env`. When running Go directly, supply the variables through the environment. With Vite, set `PUBLIC_URL` to its origin so origin validation passes.

Pending acceptance checks include installation on two Ubuntu servers, live Cloudflare and ACME integration, reference clients, practical recovery, and testing from Iranian networks. The project makes no guarantees of undetectability or connectivity during a complete loss of external routes. See the [acceptance checklist](docs/acceptance.md).
