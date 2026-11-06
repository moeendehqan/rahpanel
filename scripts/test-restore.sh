#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf -- "$test_dir"' EXIT
mkdir -p "$test_dir/project/scripts" "$test_dir/bin"
cp "$project_dir/scripts/backup.sh" "$project_dir/scripts/restore.sh" "$test_dir/project/scripts/"
printf archive > "$test_dir/incoming.dump"
cat > "$test_dir/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$RESTORE_TEST_LOG"
case "$*" in
 *'pg_restore --list'*) cat >/dev/null; exit "${BAD_ARCHIVE:-0}" ;;
 *pg_dump*)
  if IFS= read -r unexpected; then echo 'Backup inherited caller stdin' >&2; exit 2; fi
  printf previous-backup; exit 0 ;;
 *pg_restore*)
  [[ "$*" == *--single-transaction* ]] || exit 2
  cat >/dev/null
  exit "${FAIL_RESTORE:-0}" ;;
 'compose stop panel'|'compose up -d panel') exit 0 ;;
esac
echo "Unexpected Docker command: $*" >&2; exit 2
MOCK
chmod +x "$test_dir/bin/docker"
export PATH="$test_dir/bin:$PATH" RESTORE_TEST_LOG="$test_dir/docker.log"
cd "$test_dir/project"
printf 'caller-input\n' | bash scripts/backup.sh >/dev/null
printf 'RESTORE\n' | bash scripts/restore.sh "$test_dir/incoming.dump" >/dev/null
grep -q 'pg_restore --list' "$RESTORE_TEST_LOG"
grep -q 'pg_restore .*--single-transaction' "$RESTORE_TEST_LOG"
test -n "$(find backups -name '*.dump' -print -quit)"
test "$(tail -n 1 "$RESTORE_TEST_LOG")" = 'compose up -d panel'
: > "$RESTORE_TEST_LOG"
if printf 'RESTORE\n' | BAD_ARCHIVE=1 bash scripts/restore.sh "$test_dir/incoming.dump" >/dev/null 2>&1; then
 echo 'Invalid archive accepted' >&2; exit 1
fi
! grep -q 'compose stop' "$RESTORE_TEST_LOG"
: > "$RESTORE_TEST_LOG"
if printf 'RESTORE\n' | FAIL_RESTORE=1 bash scripts/restore.sh "$test_dir/incoming.dump" >/dev/null 2>&1; then
 echo 'Failed restore reported success' >&2; exit 1
fi
test "$(tail -n 1 "$RESTORE_TEST_LOG")" = 'compose up -d panel'
printf 'Restore contracts passed (archive check, prior backup, atomic restore, restart on failure).\n'
