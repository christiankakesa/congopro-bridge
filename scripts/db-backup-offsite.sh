#!/usr/bin/env bash

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Optional offsite push for the local database dumps — a local-only backup
# doesn't survive losing the VPS itself. Clean no-op until configured, so
# db-backup.sh can call it unconditionally before a bucket exists.
#
# Runs as the `postgres` OS user (it's called from db-backup.sh inside the
# congopro-bridge-db-backup unit), so BOTH config files must be readable by
# postgres — which is why the rclone config lives next to the env file under
# /opt/congopro-bridge rather than in any home directory:
#
#   /opt/congopro-bridge/backup-offsite.env          (never in git)
#     OFFSITE_MODE=s3                                # or: none (default)
#     OFFSITE_RCLONE_REMOTE="r2congopro:bucket/path/"
#     OFFSITE_RETENTION_DAYS=90                      # optional, default 90
#
#   /opt/congopro-bridge/backup-offsite.rclone.conf  (never in git)
#     [r2congopro]
#     type = s3
#     provider = Cloudflare                          # needs rclone >= 1.56
#     access_key_id = <bucket-scoped R2 token id>
#     secret_access_key = <bucket-scoped R2 secret>
#     endpoint = https://<accountid>[.<jurisdiction>].r2.cloudflarestorage.com
#     region = auto
#     acl = private
#     no_check_bucket = true
#
# Both are written by `make prod-backup-offsite-configure` — don't hand-edit
# unless you enjoy the failure modes below.
#
# Hard-won gotchas (paid for in audio-server — see its DEPLOY.md):
#   - The endpoint's jurisdiction segment must match the bucket. An EU bucket
#     behind the non-.eu endpoint answers 403 AccessDenied, which looks
#     exactly like a bad token.
#   - `no_check_bucket = true` is required for an object-scoped token:
#     without it rclone tries HeadBucket/CreateBucket and fails even though
#     the bucket is writable.
#   - rclone's `endpoint` takes the https:// scheme (the opposite convention
#     of most Go S3 configs).
#   - Use a bucket + token no other project can reach: one leaked credential
#     must not expose every backup you own.
#
# Offsite retention is AGE-based and deliberately longer than the local
# count-based rotation (14 newest): the offsite copy exists to survive events
# that also destroy local state, so mirroring local retention would make it
# nearly useless.
#
# Usage: db-backup-offsite.sh BACKUP_DIR
# ─────────────────────────────────────────────────────────────────────────────

BACKUP_DIR="${1:?usage: db-backup-offsite.sh BACKUP_DIR}"
ENV_FILE="${OFFSITE_ENV_FILE:-/opt/congopro-bridge/backup-offsite.env}"
RCLONE_CONF="${OFFSITE_RCLONE_CONFIG:-/opt/congopro-bridge/backup-offsite.rclone.conf}"

OFFSITE_MODE="none"
if [[ -f "${ENV_FILE}" ]]; then
  # shellcheck disable=SC1090
  source "${ENV_FILE}"
fi

case "${OFFSITE_MODE}" in
  none)
    echo "offsite: not configured (${ENV_FILE} absent or OFFSITE_MODE=none) — skipping"
    exit 0
    ;;
  s3)
    : "${OFFSITE_RCLONE_REMOTE:?OFFSITE_RCLONE_REMOTE must be set in ${ENV_FILE} for s3 mode}"
    if ! command -v rclone >/dev/null 2>&1; then
      echo "offsite: rclone not installed" >&2
      exit 1
    fi
    if [[ ! -r "${RCLONE_CONF}" ]]; then
      echo "offsite: ${RCLONE_CONF} missing or unreadable by $(id -un)" >&2
      exit 1
    fi
    echo "▶ pushing ${BACKUP_DIR} to ${OFFSITE_RCLONE_REMOTE}…"
    # copy + age-based delete, deliberately NOT `rclone sync`: db-backup.sh
    # rotates local dumps down to the 14 newest BEFORE this runs, and it also
    # `mkdir -p`s the backup dir — with sync, a wiped or unmounted local dir
    # would be recreated empty, one fresh dump would land, and the next sync
    # would delete the entire remote history to match. copy only ever adds.
    # --include "*.dump" keeps a concurrent run's half-written .dump.tmp out.
    rclone --config "${RCLONE_CONF}" copy "${BACKUP_DIR}" "${OFFSITE_RCLONE_REMOTE}" --include "*.dump"
    rclone --config "${RCLONE_CONF}" delete "${OFFSITE_RCLONE_REMOTE}" --min-age "${OFFSITE_RETENTION_DAYS:-90}d" --include "*.dump"
    printf '%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "${BACKUP_DIR}/offsite-last-success"
    chmod 644 "${BACKUP_DIR}/offsite-last-success"
    echo "✓ offsite push complete"
    ;;
  *)
    echo "offsite: unknown OFFSITE_MODE '${OFFSITE_MODE}' in ${ENV_FILE}" >&2
    exit 1
    ;;
esac
