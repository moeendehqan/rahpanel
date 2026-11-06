#!/usr/bin/env bash
set -euo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_dir"
mkdir -p backups
target="backups/rah-$(date -u +%Y%m%dT%H%M%SZ)-$$.dump"
docker compose exec -T db pg_dump -U rah -d rah -Fc > "$target.tmp" < /dev/null
mv "$target.tmp" "$target"
printf 'Database backup: %s\nKeep .env / MASTER_KEY separately; encrypted data cannot be restored without it.\n' "$target"
