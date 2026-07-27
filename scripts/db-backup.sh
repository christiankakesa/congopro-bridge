#!/usr/bin/env bash

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Dumps the congopro-bridge database to BACKUP_DIR using pg_dump's custom
# format (self-compressed, restorable with pg_restore), then deletes dumps
# older than the newest BACKUP_KEEP. Runs as the `postgres` OS user (peer
# auth, no password needed) via the congopro-bridge-db-backup systemd timer
# — see deploy/systemd/congopro-bridge-db-backup.{service,timer}.
#
# Usage: db-backup.sh DB_NAME BACKUP_DIR BACKUP_KEEP
# ─────────────────────────────────────────────────────────────────────────────

DB_NAME="${1:?usage: db-backup.sh DB_NAME BACKUP_DIR BACKUP_KEEP}"
BACKUP_DIR="${2:?usage: db-backup.sh DB_NAME BACKUP_DIR BACKUP_KEEP}"
BACKUP_KEEP="${3:?usage: db-backup.sh DB_NAME BACKUP_DIR BACKUP_KEEP}"

mkdir -p "${BACKUP_DIR}"

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
DEST="${BACKUP_DIR}/${DB_NAME}-${STAMP}.dump"
TMP="${DEST}.tmp"

echo "▶ dumping ${DB_NAME} to ${DEST}…"
pg_dump -Fc -d "${DB_NAME}" -f "${TMP}"
mv "${TMP}" "${DEST}"
echo "✓ wrote $(du -h "${DEST}" | cut -f1) to ${DEST}"

# Rotate: keep only the BACKUP_KEEP most recent dumps for this database.
mapfile -t OLD < <(ls -1t "${BACKUP_DIR}/${DB_NAME}"-*.dump 2>/dev/null | tail -n "+$((BACKUP_KEEP + 1))")
if [[ "${#OLD[@]}" -gt 0 ]]; then
  echo "▶ removing ${#OLD[@]} backup(s) older than the ${BACKUP_KEEP} most recent…"
  rm -f "${OLD[@]}"
fi

echo "✓ backup complete — $(ls -1 "${BACKUP_DIR}/${DB_NAME}"-*.dump | wc -l) dump(s) retained"
