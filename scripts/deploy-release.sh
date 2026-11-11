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
panel_host="$(docker compose exec -T edge printenv PANEL_HOST)"
test -n "$panel_host"

edge_health() {
 local status
 status="$(curl --fail --silent --show-error --max-time 10 --output /dev/null --write-out '%{http_code}' \
  --resolve "$panel_host:443:127.0.0.1" "https://$panel_host/healthz")" &&
  [[ "$status" == 200 ]]
}

edge_health
backup_output="$(bash scripts/backup.sh)"
IFS= read -r backup_line <<< "$backup_output"
backup="${backup_line#Database backup: }"
[[ "$backup" == backups/rah-*.dump && -s "$backup" ]]
printf 'Deploying %s from %s, previous image %s, backup %s\n' "$candidate" "$previous" "$previous_image" "$backup"

git merge --ff-only "$candidate"
if bash rah.sh update --existing-backup "$backup" &&
 docker compose exec -T panel /app/rahctl healthcheck &&
 edge_health; then
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
# Recreate Caddy too so it reloads the restored bind-mounted Caddyfile.
docker compose up -d --no-build --pull never --no-deps --force-recreate --wait --wait-timeout 180 panel edge ||
 rollback_failed 'starting previous services'
docker compose exec -T panel /app/rahctl healthcheck || rollback_failed 'panel health check'
edge_health || rollback_failed 'Caddy HTTPS health check'
printf 'Release %s failed. Restored %s. Time from failure detection to verified recovery: %ss. Backup: %s\n' \
 "$candidate" "$previous" "$((SECONDS - failed_at))" "$backup" >&2
exit 1
