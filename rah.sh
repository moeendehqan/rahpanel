#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$project_dir"
command="${1:-help}"
if (($#)); then shift; fi
case "$command" in
 status) docker compose ps "$@" ;;
 logs) docker compose logs --tail 100 --follow "$@" ;;
 start) docker compose up -d --no-build --wait --wait-timeout 180 "$@" ;;
 stop) docker compose stop "$@" ;;
 restart) docker compose restart "$@" ;;
 backup) bash scripts/backup.sh "$@" ;;
 restore) bash scripts/restore.sh "$@" ;;
 update)
  if [[ ${1:-} == --existing-backup ]]; then
   [[ ${2:-} == backups/rah-*.dump && -s ${2:-} ]] || { echo 'A deployment backup is required.' >&2; exit 1; }
   shift 2
  else
   bash scripts/backup.sh
  fi
  bash install.sh --no-install-docker "$@"
  ;;
 help|-h|--help)
  printf 'Usage: bash rah.sh {status|logs [service]|start|stop|restart|backup|restore FILE|update}\n'
  printf 'update: back up the current database, rebuild the source already on disk, and restart.\n'
  ;;
 *) printf 'Unknown command: %s\n' "$command" >&2; exit 1 ;;
esac
