# Deployment

Two separate environments, on purpose:

- **Local dev** — `docker-compose.yml` runs Ollama, Meilisearch, and Postgres/PostGIS
  in containers. Convenient, disposable, never used in production.
- **Production (VPS)** — everything runs as a native **systemd** service: the Go
  binary, Ollama, Meilisearch, and PostgreSQL are all installed directly on the
  host via apt/binary downloads, not docker. This doc is about that path.

All deploy operations are `make` targets that SSH/rsync into the VPS using the
`DEPLOY_*`/`SSH_KEY`/`REMOTE_DIR` variables from `.env` (see `Makefile` for the
full list, or run `make help`).

## Prerequisites

- `.env` at the repo root with at least `DEPLOY_HOST`, `DEPLOY_USER` (defaults
  to `ops`), `DEPLOY_PORT`, `SSH_KEY`, `DOMAIN`. Never committed (`.gitignore`).
- The deploy user (`ops`) has passwordless SSH key access to the VPS.
- The deploy user has **scoped** sudo rights — not full `NOPASSWD: ALL`. The
  `db-*` targets shell out to `sudo systemctl …`, `sudo apt-get install …`, and
  two uploaded scripts (`db-provision.sh`, `db-restore-prod.sh`) that must run
  as root. A minimal `/etc/sudoers.d/congopro-bridge` covering exactly what's
  used:

  ```
  ops ALL=(root) NOPASSWD: /usr/bin/systemctl start congopro-bridge, \
                           /usr/bin/systemctl stop congopro-bridge, \
                           /usr/bin/systemctl restart congopro-bridge, \
                           /usr/bin/systemctl enable congopro-bridge, \
                           /usr/bin/systemctl status congopro-bridge, \
                           /usr/bin/systemctl daemon-reload, \
                           /usr/bin/systemctl enable --now postgresql, \
                           /usr/bin/systemctl status postgresql, \
                           /usr/bin/systemctl start congopro-bridge-db-backup.service, \
                           /usr/bin/systemctl enable --now congopro-bridge-db-backup.timer, \
                           /usr/bin/systemctl status congopro-bridge-db-backup.timer, \
                           /usr/bin/apt-get update, /usr/bin/apt-get install -y *, \
                           /usr/bin/mv /tmp/* /etc/systemd/system/*, \
                           /usr/bin/mv /tmp/* /opt/congopro-bridge/*, \
                           /usr/bin/mkdir -p /opt/congopro-bridge*, \
                           /usr/bin/chown * /opt/congopro-bridge*, \
                           /usr/bin/chmod * /opt/congopro-bridge*, \
                           /usr/bin/tee -a /opt/congopro-bridge/congopro-bridge.env, \
                           /opt/congopro-bridge/scripts/db-provision.sh, \
                           /opt/congopro-bridge/scripts/db-restore-prod.sh
  ```

  Adjust paths to match your actual binary locations (`which systemctl`, etc.).
  The point isn't the exact list — it's that `ops` should never have blanket
  root, since `db-restore-prod.sh` can overwrite the live database.

## First-time VPS bootstrap

Run once, in order, on a fresh server:

```bash
make prod-ollama-setup       # installs Ollama, pulls the generative + embedding models
make prod-search-setup        # installs Meilisearch, config, systemd unit, Traefik route
make prod-db-install          # installs PostgreSQL + PostGIS via apt, enables the service
make prod-db-provision        # generates a DB password into congopro-bridge.env, creates role + database
make prod-bootstrap-app         # uploads the binary, installs the app's systemd unit, starts it
make prod-db-migrate   # applies schema migrations against the new database
make prod-db-import    # one-time: loads the legacy embedded JSON export into Postgres
make prod-db-import-ads # one-time: ads CMS cutover, loads the legacy ads.yml campaigns
make prod-app-restart      # REQUIRED after db-import-ads-remote: the serving snapshot
                          # loads at boot; the import CLI cannot reload a running process
make prod-backup-install   # installs the daily backup timer
```

(`make prod-bootstrap-all` already chains `ollama-setup` + `meili-setup` + `deploy-full`
for the non-database parts; the `db-*` steps above are additive.)

Each step is idempotent — safe to re-run if one fails partway and you fix the
underlying issue (missing sudoers rule, DNS not propagated yet, etc.).

## Day-to-day deploy (code changes)

```bash
make prod-deploy
```

Rebuilds the binary, uploads it, uploads Traefik config, restarts the
`congopro-bridge` service. If the change includes a new migration, run
`make prod-db-migrate` as well (before or after `make prod-deploy` — migrations
here are additive/backward-compatible by convention, so order isn't critical
yet; that may stop being true once destructive migrations show up).

## Database

- **Local dev**: `make dev` (one-command loop: deps + migrations + templ/CSS
  regeneration + hot-reload app — see `scripts/dev.sh`), `make dev-db-up` (starts
  Postgres in docker), `make dev-db-migrate` (applies migrations),
  `make dev-test-integration` (runs `-tags=integration` tests against it —
  currently a no-op until integration tests exist).
- **Production**: `make prod-db-check` verifies PostGIS and lists tables;
  `make prod-db-status` shows the systemd unit status.
- Migrations live in `internal/db/migrations/*.sql` (goose format, embedded
  into the binary via `go:embed`). `<binary> -migrate` applies pending ones
  and exits — that's what both `db-migrate` (local) and `prod-db-migrate`
  (production) ultimately run.
- The app loads companies from Postgres (`status = 'published'` only — drafts
  stay hidden) and no longer touches the embedded JSON at runtime.
  `<binary> -import` is the one-time migration that upserts the legacy
  embedded JSON export into the `companies` table (`make dev-db-import` locally,
  `make prod-db-import` in production); safe to re-run, existing rows are
  updated in place. Once it has run against production, new companies are
  meant to be created through the admin CMS, not this — until that CMS
  exists, insert/update `companies` rows directly.

## Backups

An untested backup isn't a real backup — the tooling below is built around
that: every restore path other than the real production one runs against a
throwaway database, so testing a backup is cheap and safe to do often.

- `make prod-backup-install` — installs `scripts/db-backup.sh` plus a systemd
  service + timer (`deploy/systemd/congopro-bridge-db-backup.{service,timer}`)
  that runs `pg_dump -Fc` daily at 03:15 as the `postgres` OS user (peer auth,
  no password needed), keeping the 14 most recent dumps in
  `/opt/congopro-bridge/backups` (override with `BACKUP_KEEP=`).
- `make prod-backup-now` — triggers an out-of-schedule run.
- `make prod-backup-status` / `db-backup-logs` / `db-backup-list` — inspect the
  timer, follow logs, list what's on the server.
- `make prod-backup-pull` — downloads everything in the remote backup directory
  into `./backups/` locally (gitignored).
- `make dev-db-restore-test [BACKUP_FILE=./backups/x.dump]` — restores a dump into
  a throwaway database on **local dev Postgres**, runs a sanity query, drops
  the throwaway database. Defaults to the newest file in `./backups/`. Never
  touches production. Run this periodically (e.g. after every `prod-backup-pull`)
  — a backup you haven't restored is a guess, not a guarantee.
- `make prod-db-restore [BACKUP_FILE=./backups/x.dump]` — **destructive.** Uploads
  the dump, stops the `congopro-bridge` service, terminates other connections,
  and runs `pg_restore --clean` against the live database. Requires typing
  `RESTORE <db_name>` at an interactive prompt (the script refuses to run
  without a tty), and the service is restarted automatically whether the
  restore succeeds or fails. Only run this after `dev-db-restore-test` has
  verified the same file.

Both restore paths were rehearsed against production on 2026-08-28: the
offsite copy restored into throwaway local Postgres, and `prod-db-restore`
ran against the live database (row counts afterwards identical to the
pre-restore baseline, app healthy). Rehearse again after any change to the
backup scripts or the Makefile's ssh plumbing — the 2026-08-28 drill found
`prod-db-restore` broken by a target rename, which nothing else exercises.

### Offsite backups (Cloudflare R2)

Local dumps live on the same VPS they protect. `db-backup.sh` ends by calling
`scripts/db-backup-offsite.sh`, which pushes every `*.dump` to an R2 bucket and
prunes offsite copies older than 90 days — age-based and deliberately longer
than the local 14-newest rotation, because the offsite copy exists to survive
events that also destroy local state.

**Where each copy lives, and what that means for restores:**

| Copy | Location | Retention |
|---|---|---|
| local | `/opt/congopro-bridge/backups/` on the VPS | 14 newest |
| offsite | R2 bucket `congopro-db-backups` | 90 days |
| working | `./backups/` on your machine (gitignored) | whatever you pull |

Dumps only ever reach your machine because you pulled them — nothing restores
"from your laptop" by design. `prod-db-restore` happens to upload a local file,
which is convenient for a recent dump you already tested; `prod-db-restore-offsite`
skips your machine entirely and pulls straight from R2 onto the server.

#### One-time setup

By hand in the Cloudflare dashboard (R2):

1. Create a bucket used by **nothing else** (e.g. `congopro-db-backups`) — one
   leaked credential must not expose every backup you own. Pick the
   jurisdiction deliberately; it is fixed at creation.
2. On the bucket: *Manage API Tokens* → **Object Read & Write**, **scoped to
   this bucket only**. Note the access key id, the secret, and the endpoint
   **exactly as that screen shows it** — an EU bucket's endpoint carries a
   `.eu.` segment, and the default endpoint answers **403 AccessDenied, which
   looks exactly like a bad token**.

Then `make prod-backup-offsite-configure`: interactive, writes
`/opt/congopro-bridge/backup-offsite.{env,rclone.conf}` (postgres-owned `0600`,
never in git; the secret is typed with echo off and travels over stdin), and
proves the credentials with a real write/read/delete round trip. The generated
config bakes in `no_check_bucket = true` (object-scoped tokens cannot
HeadBucket) and the `https://` endpoint scheme.

#### Day-to-day

- `make prod-backup-offsite-status` — the offsite success marker plus the dumps
  currently in the bucket (this is where you read a dump's exact name).
- `make prod-backup-offsite-pull [BACKUP_NAME=…]` — copies a dump out of the
  bucket into `./backups/offsite/`. Newest unless you name one.
- `make dev-db-restore-test-offsite` — fetches from the bucket and restores into
  throwaway local Postgres. Run periodically: an untested offsite backup is a
  guess.

#### Restoring: which path

**The dump is recent (within 14 days) and the VPS is healthy.** Use the local
copies — no R2 involved:

```
make prod-backup-pull                                  # VPS → ./backups/
make dev-db-restore-test BACKUP_FILE=./backups/x.dump  # prove it restores
make prod-db-restore      BACKUP_FILE=./backups/x.dump # then production
```

**The dump is older than 14 days** (rotated out locally, still in R2), or the
server's backup directory is empty. Restore straight from the bucket, so the
dump never round-trips through your machine:

```
make prod-backup-offsite-status                        # find the exact name
make prod-backup-offsite-pull BACKUP_NAME=x.dump       # optional: to test it
make dev-db-restore-test BACKUP_FILE=./backups/offsite/offsite-verify.dump
make prod-db-restore-offsite  BACKUP_NAME=x.dump       # R2 → server → restore
```

**The VPS is gone.** Bootstrap a replacement (`make prod-bootstrap-all`), run
`make prod-backup-offsite-configure` with the same bucket and token, then
`make prod-db-restore-offsite` — the new box pulls its own history out of R2.

Both destructive targets behave identically: the app is stopped, you type
`RESTORE congopro_bridge` at a real prompt (the script refuses without a tty),
`pg_restore --clean` runs, and the service restarts whether it succeeded or
not.

The offsite push never fails the backup unit — the local dump is the primary
safety net, so an offsite hiccup is a journalled `⚠`, not a failed run.
`make prod-backup-status` shows both `last-success` markers.

## Secrets

`make prod-secrets-init` (also run automatically by `prod-db-provision` and
`meili-setup`) generates `MEILI_MASTER_KEY` and `DATABASE_URL` directly on the
server — they're never downloaded to your machine or printed to your
terminal — and writes them to `/opt/congopro-bridge/congopro-bridge.env`
(`chmod 600`, `root:root`), which the app's systemd unit loads via
`EnvironmentFile=`. Re-running `prod-secrets-init` after adding a new secret only
fills in what's missing; it won't rotate existing keys.

## Verification checklist after a fresh bootstrap

```bash
make prod-app-status        # congopro-bridge is active (running)
make prod-db-status       # postgresql is active (running)
make prod-db-check        # postgis_version() returns a version, \dt lists companies
make prod-search-status           # meilisearch is active (running)
make prod-ollama-status           # ollama is active, models are pulled
curl -sf https://$(DOMAIN)/api/v1/healthz  # app responds
```
