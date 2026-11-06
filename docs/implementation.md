# Rah Panel implementation ledger

Approved specification: custom server-focused panel, Go API/agent, PostgreSQL,
React/TypeScript, Ubuntu 24.04 SSH provisioning, Cloudflare DNS-01, Xray,
30-second usage reconciliation and five-minute fail-closed leases.

## Tasks
- [x] Control plane, migrations, password/TOTP sessions, users/groups/nodes.
- [x] Pinned SSH provisioning, durable job metadata, Cloudflare and ACME.
- [x] Xray configuration, subscriptions, authenticated probes, credentials.
- [x] Agent reconciliation, usage outbox, local expiry, independent watchdog.
- [x] Persian UI, deployment files, backup/restore scripts, local verification.
- [ ] Live infrastructure, real client and network acceptance gates (see acceptance.md).

## Verification evidence (2026-11-06)
- Go tests and vet passed, including real PostgreSQL 17.4 integration tests.
- Actual pinned Xray 25.3.6 accepted the generated three-inbound configuration.
- PostgreSQL concurrency tests covered duplicate delivery, cross-node totals and core restarts.
- Client certificate rotation recovered the same pending identity after a lost response.
- 1,000 users / 20 nodes: initial credentials/configuration approximately 1.16 seconds;
  steady-state reconciliation approximately 196 ms on the development machine. This is not traffic capacity.
- Frontend unit tests and production build passed. Chrome/Playwright exercised administrator login,
  user creation, suspension, subscription QR, deletion, settings, mobile layout and logout.
- Linux amd64 panel/agent and arm64 agent cross-compilation passed.
- Docker is unavailable on this workstation; Compose image build and live backup/restore are unverified.

## Simplified Docker deployment
- Added `install.sh --domain HOST`: official apt-based Docker bootstrap on fresh Ubuntu/Debian,
  cached image build, one-time secret generation, and health-gated Compose startup.
- Added `rah.sh` for status/logs/start/stop/restart/backup/restore/update; updates back up DB first.
- Added bounded container logs, persistent named volumes, panel/edge health checks and startup ordering.
- Installer contract tests passed for fresh installation, preserved secrets, domain validation,
  startup failure/retry and credential redaction. Restore contracts passed for archive preflight,
  prior backup, single-transaction restore and restarting the panel on failure.
- Go tests/vet and a real local HTTP healthcheck passed; the Linux rahctl binary cross-compiled.
- Compose validated against the official Compose specification JSON Schema; workflow YAML parsed.
- Added a CI job that builds the real image, starts all services, and checks DB persistence across
  recreation. This CI job has not been run here; there is no local Docker daemon.

## Rulings

- Added downloadable GitHub bootstrap for `moeendehqan/rahpanel`: shallow clone into `/opt/rahpanel`,
  safe resume, pinned branch/tag option and terminal prompting during piped execution.
- Bootstrap contracts and browser first-login setup passed. Installed endpoint URLs are populated
  by the API; unconfigured administrators enter provider setup before node installation.
- Use a fresh workspace in the existing empty directory; no existing Git branch or code exists.
- Use a separate mTLS listener for node traffic; browser/API requests cannot impersonate a node.
- SSH credentials remain process-local; an interrupted provisioning job requires credentials again.
- DNS credentials stay on the control plane. ACME issuance is performed centrally.
- No remote infrastructure credentials were supplied: live provisioning/network tests must be recorded as unverified.
- Use a local Go toolchain because Go and Docker are absent from PATH.
