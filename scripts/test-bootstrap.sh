#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf -- "$test_dir"' EXIT
mkdir -p "$test_dir/bin"
cat > "$test_dir/bin/git" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$BOOTSTRAP_TEST_LOG"
case "$1" in
 check-ref-format) exit 0 ;;
 clone)
  [[ "${FAIL_CLONE:-0}" == 0 ]] || exit 1
  target="${!#}"
  mkdir -p "$target/.git"
  touch "$target/compose.yaml"
  cat > "$target/install.sh" <<'INSTALL'
#!/usr/bin/env bash
printf 'INSTALL %s\n' "$*" >> "$BOOTSTRAP_TEST_LOG"
INSTALL
  ;;
 -C)
  case "$4" in
   get-url) printf '%s\n' 'https://github.com/moeendehqan/rahpanel.git' ;;
   --verify) printf '%s\n' 'same-commit' ;;
   *) echo "Unexpected git command: $*" >&2; exit 2 ;;
  esac
  ;;
 *) echo "Unexpected git command: $*" >&2; exit 2 ;;
esac
MOCK
chmod +x "$test_dir/bin/git"
export PATH="$test_dir/bin:$PATH" BOOTSTRAP_TEST_LOG="$test_dir/git.log"
# Exercise the exact download-to-stdin execution mode used in the public command.
cat "$project_dir/bootstrap.sh" | bash -s -- --dir "$test_dir/rah" --domain panel.example.com --no-install-docker >/dev/null
test -f "$test_dir/rah/install.sh"
grep -q 'clone .*--branch main' "$BOOTSTRAP_TEST_LOG"
grep -q 'INSTALL --domain panel.example.com --no-install-docker' "$BOOTSTRAP_TEST_LOG"
printf 'PRESERVE\n' > "$test_dir/rah/.env"
bash "$project_dir/bootstrap.sh" --dir "$test_dir/rah" --domain panel.example.com --no-install-docker >/dev/null
test "$(cat "$test_dir/rah/.env")" = PRESERVE
test "$(grep -c '^clone ' "$BOOTSTRAP_TEST_LOG")" = 1
mkdir "$test_dir/unrelated"
if bash "$project_dir/bootstrap.sh" --dir "$test_dir/unrelated" --domain panel.example.com >/dev/null 2>&1; then
 echo 'Unrelated directory accepted' >&2; exit 1
fi
if FAIL_CLONE=1 bash "$project_dir/bootstrap.sh" --dir "$test_dir/failed" --domain panel.example.com >/dev/null 2>&1; then
 echo 'Failed download reported success' >&2; exit 1
fi
test ! -d "$test_dir/failed"
! find "$test_dir" -maxdepth 1 -name '.rahpanel-source.*' -print | grep -q .
if bash "$project_dir/bootstrap.sh" --repo 'https://token@github.com/owner/repo.git' --dir "$test_dir/rejected" >/dev/null 2>&1; then
 echo 'Credentials in repository URL accepted' >&2; exit 1
fi
printf 'Bootstrap contracts passed (stdin, clone, resume, unrelated directory, download failure, URL credentials).\n'
