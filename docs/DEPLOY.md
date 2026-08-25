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
make ollama-setup       # installs Ollama, pulls the generative + embedding models
make meili-setup        # installs Meilisearch, config, systemd unit, Traefik route
make db-install          # installs PostgreSQL + PostGIS via apt, enables the service
make db-provision        # generates a DB password into congopro-bridge.env, creates role + database
make deploy-full         # uploads the binary, installs the app's systemd unit, starts it
make db-migrate-remote   # applies schema migrations against the new database
make db-import-remote    # one-time: loads the legacy embedded JSON export into Postgres
make db-import-ads-remote # one-time: ads CMS cutover, loads the legacy ads.yml campaigns
make service-restart      # REQUIRED after db-import-ads-remote: the serving snapshot
                          # loads at boot; the import CLI cannot reload a running process
make db-backup-install   # installs the daily backup timer
```

(`make deploy-all` already chains `ollama-setup` + `meili-setup` + `deploy-full`
for the non-database parts; the `db-*` steps above are additive.)

Each step is idempotent — safe to re-run if one fails partway and you fix the
underlying issue (missing sudoers rule, DNS not propagated yet, etc.).

## Day-to-day deploy (code changes)

```bash
make deploy
```

Rebuilds the binary, uploads it, uploads Traefik config, restarts the
`congopro-bridge` service. If the change includes a new migration, run
`make db-migrate-remote` as well (before or after `make deploy` — migrations
here are additive/backward-compatible by convention, so order isn't critical
yet; that may stop being true once destructive migrations show up).

## Database

- **Local dev**: `make dev` (one-command loop: deps + migrations + templ/CSS
  regeneration + hot-reload app — see `scripts/dev.sh`), `make db-up` (starts
  Postgres in docker), `make db-migrate` (applies migrations),
  `make test-integration` (runs `-tags=integration` tests against it —
  currently a no-op until integration tests exist).
- **Production**: `make db-remote-check` verifies PostGIS and lists tables;
  `make db-remote-status` shows the systemd unit status.
- Migrations live in `internal/db/migrations/*.sql` (goose format, embedded
  into the binary via `go:embed`). `<binary> -migrate` applies pending ones
  and exits — that's what both `db-migrate` (local) and `db-migrate-remote`
  (production) ultimately run.
- The app loads companies from Postgres (`status = 'published'` only — drafts
  stay hidden) and no longer touches the embedded JSON at runtime.
  `<binary> -import` is the one-time migration that upserts the legacy
  embedded JSON export into the `companies` table (`make db-import` locally,
  `make db-import-remote` in production); safe to re-run, existing rows are
  updated in place. Once it has run against production, new companies are
  meant to be created through the admin CMS, not this — until that CMS
  exists, insert/update `companies` rows directly.

## Backups

An untested backup isn't a real backup — the tooling below is built around
that: every restore path other than the real production one runs against a
throwaway database, so testing a backup is cheap and safe to do often.

- `make db-backup-install` — installs `scripts/db-backup.sh` plus a systemd
  service + timer (`deploy/systemd/congopro-bridge-db-backup.{service,timer}`)
  that runs `pg_dump -Fc` daily at 03:15 as the `postgres` OS user (peer auth,
  no password needed), keeping the 14 most recent dumps in
  `/opt/congopro-bridge/backups` (override with `BACKUP_KEEP=`).
- `make db-backup-now` — triggers an out-of-schedule run.
- `make db-backup-status` / `db-backup-logs` / `db-backup-list` — inspect the
  timer, follow logs, list what's on the server.
- `make db-backup-pull` — downloads everything in the remote backup directory
  into `./backups/` locally (gitignored).
- `make db-restore-test [BACKUP_FILE=./backups/x.dump]` — restores a dump into
  a throwaway database on **local dev Postgres**, runs a sanity query, drops
  the throwaway database. Defaults to the newest file in `./backups/`. Never
  touches production. Run this periodically (e.g. after every `db-backup-pull`)
  — a backup you haven't restored is a guess, not a guarantee.
- `make db-restore [BACKUP_FILE=./backups/x.dump]` — **destructive.** Uploads
  the dump, stops the `congopro-bridge` service, terminates other connections,
  and runs `pg_restore --clean` against the live database. Requires typing
  `RESTORE <db_name>` at an interactive prompt (the script refuses to run
  without a tty), and the service is restarted automatically whether the
  restore succeeds or fails. Only run this after `db-restore-test` has
  verified the same file.

## Secrets

`make secrets-init` (also run automatically by `db-provision` and
`meili-setup`) generates `MEILI_MASTER_KEY` and `DATABASE_URL` directly on the
server — they're never downloaded to your machine or printed to your
terminal — and writes them to `/opt/congopro-bridge/congopro-bridge.env`
(`chmod 600`, `root:root`), which the app's systemd unit loads via
`EnvironmentFile=`. Re-running `secrets-init` after adding a new secret only
fills in what's missing; it won't rotate existing keys.

## Verification checklist after a fresh bootstrap

```bash
make service-status        # congopro-bridge is active (running)
make db-remote-status       # postgresql is active (running)
make db-remote-check        # postgis_version() returns a version, \dt lists companies
make meili-status           # meilisearch is active (running)
make ollama-status           # ollama is active, models are pulled
curl -sf https://$(DOMAIN)/api/v1/healthz  # app responds
```
