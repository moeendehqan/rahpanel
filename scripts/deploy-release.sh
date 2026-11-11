#!/usr/bin/env bash
set -euo pipefail

candidate="${1:?Expected the release commit}"
previous="$(git rev-parse HEAD)"
git merge-base --is-ancestor "$previous" "$candidate"
docker compose exec -T panel /app/rahctl healthcheck
container="$(docker compose ps -q panel)"
previous_image="$(docker inspect --format '{{.Image}}' "$container")"
rollback_tag="rahpanel:rollback-$previous"
docker image tag "$previous_image" "$rollback_tag"
public_url="$(docker compose exec -T panel printenv PUBLIC_URL)"
[[ "$public_url" == https://?* ]] || { echo 'Panel PUBLIC_URL must use HTTPS.' >&2; exit 1; }
services="$(docker compose config --services)"
rollback_services=(panel)
if grep -Fxq edge <<< "$services"; then
 rollback_services+=(edge)
fi

https_health() {
 local status
 if ! status="$(curl --fail --silent --show-error --max-time 10 --output /dev/null --write-out '%{http_code}' \
  "${public_url%/}/healthz")" || [[ "$status" != 200 ]]; then
  printf 'Public HTTPS health check failed (HTTP %s).\n' "${status:-unknown}" >&2
  return 1
 fi
 printf 'Public HTTPS health check passed (HTTP 200).\n'
}

https_health
backup_output="$(bash scripts/backup.sh)"
IFS= read -r backup_line <<< "$backup_output"
backup="${backup_line#Database backup: }"
[[ "$backup" == backups/rah-*.dump && -s "$backup" ]]
printf 'Deploying %s from %s, previous image %s, backup %s\n' "$candidate" "$previous" "$previous_image" "$backup"

git merge --ff-only "$candidate"
if bash rah.sh update --existing-backup "$backup" &&
 docker compose exec -T panel /app/rahctl healthcheck &&
 https_health; then
 docker image rm "$rollback_tag" >/dev/null || true
 exit 0
fi

failed_at=$SECONDS
rollback_failed() {
 printf 'Release %s failed. Rollback to %s failed at %s. Backup: %s. Saved image: %s\n' \
  "$candidate" "$previous" "$1" "$backup" "$rollback_tag" >&2
 exit 1
}

git reset --keep "$previous" || rollback_failed 'restoring checkout'
docker image tag "$rollback_tag" rahpanel:local || rollback_failed 'restoring image'
# Recreate Compose-managed Caddy so it reloads the restored bind-mounted Caddyfile.
docker compose up -d --no-build --pull never --no-deps --force-recreate --wait --wait-timeout 180 "${rollback_services[@]}" ||
 rollback_failed 'starting previous services'
docker compose exec -T panel /app/rahctl healthcheck || rollback_failed 'panel health check'
https_health || rollback_failed 'public HTTPS health check'
printf 'Release %s failed. Restored %s. Time from failure detection to verified recovery: %ss. Backup: %s\n' \
 "$candidate" "$previous" "$((SECONDS - failed_at))" "$backup" >&2
exit 1
