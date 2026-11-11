# Release recovery drill

Use a disposable Linux host with a dedicated hostname, valid Caddy HTTPS, test data,
and an independent MASTER_KEY. Use a separate repository or workflow configured
with the staging SSH credentials so the drill exercises the GitHub Actions job.
Before releasing schema changes, verify that the previous panel image can read
and write against the upgraded staging database.

1. Run a normal staging deployment. Record `git rev-parse HEAD` and
   `docker inspect --format '{{.Image}}' "$(docker compose ps -q panel)"`.
   Create a sample user and save a test encrypted setting through the panel.
2. On the staging branch, insert `false &&` immediately after the
   `bash rah.sh update ... &&` line in the deployment condition. Push the
   staging commit. The install must finish successfully before the injected
   failure. Confirm the job fails, the checkout and running image match step 1,
   and `curl -fsS https://STAGING_HOST/healthz` succeeds. Record the recovery
   time and backup path from the job log.
3. Repeat with a staging commit that also injects
   `rollback_failed 'simulated restart failure'` immediately before the
   rollback Compose command. Confirm the failure log names the step and lists
   the backup and saved image. On staging, after checking the checkout SHA,
   retag the saved image as `rahpanel:local` and run
   `docker compose up -d --no-build --pull never --no-deps --force-recreate --wait --wait-timeout 180 panel edge`.
   Verify the running image and HTTPS health. Remove both injections from the
   staging branch.
4. Using the backup reported by the drill, start a timer and run
   `bash rah.sh restore backups/FILE.dump`. Enter `RESTORE` when prompted.
   Keep the original staging MASTER_KEY in the staging configuration.
   Verify HTTPS health, sign in, find the sample user, and verify the panel
   can use the saved encrypted setting. Stop the timer after these checks.

Record the date, commits, observed outcomes, seconds from failure detection
to verified recovery, and seconds from restore start to completed checks.
Use those timings to set a recovery target. Live drill timings are pending.

For a manual recovery after a failed rollback, verify the checkout matches
the previous SHA in the job log and use the saved image and Compose command
from step 3. If restoring the database is necessary, use the procedure in
step 4 during a maintenance window. Restore replaces database contents with
the backup snapshot.
