#!/usr/bin/env bash
set -euo pipefail
test "$#" -eq 1 || { echo 'Usage: scripts/restore.sh PATH.dump'; exit 1; }
test -f "$1"
backup_path="$(cd -- "$(dirname -- "$1")" && pwd)/$(basename -- "$1")"
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_dir"
# Reject an unreadable archive before stopping services or changing the database.
docker compose exec -T db pg_restore --list < "$backup_path" > /dev/null
echo 'Restoring replaces the current Rah database. Stop traffic management during restore.'
read -r -p 'Type RESTORE to continue: ' confirmation
test "$confirmation" = RESTORE
bash scripts/backup.sh
trap 'docker compose up -d panel >&2 || true' EXIT
docker compose stop panel
docker compose exec -T db pg_restore -U rah -d rah --clean --if-exists --no-owner --exit-on-error --single-transaction < "$backup_path"
docker compose up -d panel
trap - EXIT
