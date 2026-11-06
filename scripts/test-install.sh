#!/usr/bin/env bash
# Isolated contract test: no daemon, root privileges or host changes required.
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf -- "$test_dir"' EXIT
mkdir -p "$test_dir/project" "$test_dir/bin"
cp "$project_dir/install.sh" "$test_dir/project/"
cp "$project_dir/compose.yaml" "$project_dir/Dockerfile" "$test_dir/project/"
mkdir -p "$test_dir/project/deploy"
cp "$project_dir/deploy/Caddyfile" "$test_dir/project/deploy/"
cat > "$test_dir/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$INSTALL_TEST_LOG"
if [[ "${INSTALL_TEST_FAIL_UP:-0}" == 1 && "$*" == 'compose up '* ]]; then exit 1; fi
case "$*" in
  'info'|'compose version'|'compose config --quiet'|'compose up -d --no-build --wait --wait-timeout 180'|'compose ps') exit 0 ;;
  build*) exit 0 ;;
  run*) printf 'PANEL_HOST=panel.example.com\n' > .env; printf 'private bootstrap data\n' > bootstrap-admin.txt; exit 0 ;;
esac
echo "Unexpected Docker command: $*" >&2
exit 2
MOCK
chmod +x "$test_dir/bin/docker"
export INSTALL_TEST_LOG="$test_dir/docker.log"
export PATH="$test_dir/bin:$PATH"
cd "$test_dir/project"
bash install.sh --domain panel.example.com --no-install-docker > "$test_dir/output"
test -f .env && test -f bootstrap-admin.txt
grep -q '^run .*rahctl.*init.*--domain panel.example.com' "$INSTALL_TEST_LOG"
grep -q '^compose up .*--wait' "$INSTALL_TEST_LOG"
! grep -q 'private bootstrap data' "$test_dir/output"
before="$(cat .env)"
bash install.sh --no-install-docker >> "$test_dir/output"
test "$(cat .env)" = "$before"
test "$(grep -c '^run ' "$INSTALL_TEST_LOG")" = 1
if INSTALL_TEST_FAIL_UP=1 bash install.sh --no-install-docker >> "$test_dir/output" 2>&1; then
 echo 'Failed startup reported success' >&2; exit 1
fi
test "$(cat .env)" = "$before"
test ! -d .install.lock
bash install.sh --no-install-docker >> "$test_dir/output"
if bash install.sh --domain other.example.com --no-install-docker >> "$test_dir/output" 2>&1; then
 echo 'Existing domain unexpectedly replaced' >&2; exit 1
fi
rm .env bootstrap-admin.txt
if bash install.sh --domain 'bad/domain' --no-install-docker >> "$test_dir/output" 2>&1; then
 echo 'Invalid domain accepted' >&2; exit 1
fi
printf 'Installer contracts passed (fresh install, rerun, startup failure/retry, domain mismatch, invalid input, secret redaction).\n'
