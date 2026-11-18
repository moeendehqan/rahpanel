# Acceptance and release gates

## Local verification

Run `go test ./...`, `go vet ./...`, frontend tests and production build. With an isolated PostgreSQL database, test concurrent duplicate reports, multi-node aggregation, process-run changes, password-only admin login, wrong-password rejection, user suspension and provider secret redaction. `RUN_CAPACITY_TEST=1` enables the 1,000-user/20-node control-plane exercise.

Lease tests use boot uptime, include reboot and deadline boundaries, and bound expiration by server time independently of node wall-clock skew. A fatal accounting failure must remain quarantined across reconciler restart.

For the real Hysteria 2 gate, provide official Xray 26.11.9 and Mihomo 1.19.34 binaries, Python 3, curl, an isolated PostgreSQL database and an outbound TLS connection to www.apple.com. Run `TEST_XRAY_BIN=/path/to/xray TEST_MIHOMO_BIN=/path/to/mihomo TEST_HYSTERIA_DATABASE_URL='postgres://user:password@localhost/isolated_db?sslmode=disable' go test ./internal/provision -run '^TestHysteriaAcceptance$' -v -count=1 -timeout=5m`. It uses loopback ports 12053, 14443–14446, 15080–15082, 15085–15086, 15089–15091 and 18443. It validates generated configurations, TLS verification, TCP/UDP counters, two-way fallback, active traffic revocation and reconnection after restoration, and idempotent persistence to the shared user quota.

## Required live gates — not represented as locally verified

- Provision two clean Ubuntu 24.04 hosts, including one arm64 if offered in a release, through the UI without editing host configuration.
- Confirm SSH fingerprint mismatch sends no authentication credentials.
- On a shared panel host, preserve existing Nginx and other cores; verify custom public ports,
  loopback-only statistics, subscription/probe port consistency and retry after a selected port conflict.
- Exercise insufficient sudo, occupied ports, upload interruption, panel restart, DNS denial, ACME failure and retry. Check owned DNS records remain identifiable and unrelated records untouched.
- Verify all three authenticated public transports using Xray probes and then real v2rayNG/v2rayN clients. Check DNS, IPv6 and UDP under each client's VPN/TUN implementation.
- Cut panel connectivity and kill the reconciler; verify watchdog stops every Xray process within 300 seconds of the last successful lease request. Repeat with wall-clock changes and host reboot.
- Suspend and exhaust a shared user on multiple healthy nodes; verify enforcement within 90 seconds. Test local expiry while panel is inaccessible.
- Restore a real backup into a fresh installation with the same master key; verify administrator, users, CA, credentials and usage.
- Test invalid candidate configuration, certificate renewal, core update and rollback. Reloads may reset open connections.
- Measure simultaneous traffic capacity independently from control-plane population. Run network tests from Iran before making any censorship-resistance claim.

## Accounting boundaries

Accepted reports are transactionally deduplicated by `(node,id)` and `(node,run,sequence)`. A baseline and its queued deltas are persisted together before transmission; lost acknowledgements replay the same reports safely. Controlled restarts collect a final sample before stopping Xray. Bytes in flight between that sample and process termination, and bytes not durably sampled before an abrupt core crash/power loss, can be lost. This is periodic accounting, not billing-grade byte-exact accounting. Quota overshoot depends on reporting latency and throughput; offline nodes stop after the five-minute lease policy.

## Operational limitations

The initial release has three fixed versioned Xray templates, one active control plane, one serial installation worker, no automatic client selection policy and no reseller/payment module. Node and endpoint mTLS identities renew automatically before expiry; nodes offline past certificate expiry require reinstall. SSH-based reinstall remains the recovery/update mechanism. Uninstalling node software and deleting infrastructure/DNS are not silently performed by disabling a node.
