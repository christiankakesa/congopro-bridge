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

# Success marker: one line, timestamp + newest dump. `make prod-backup-status`
# reads it, and an external monitor can watch its age (kito-platform pattern).
printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(basename "${DEST}")" > "${BACKUP_DIR}/last-success"
chmod 644 "${BACKUP_DIR}/last-success"

# Optional offsite push — a clean no-op until /opt/congopro-bridge/
# backup-offsite.env exists (see db-backup-offsite.sh's header). Never fails
# this run: the local dump above already succeeded and is the primary safety
# net, so an offsite hiccup is a journalled warning, not a failed unit.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -x "${SCRIPT_DIR}/db-backup-offsite.sh" ]]; then
  "${SCRIPT_DIR}/db-backup-offsite.sh" "${BACKUP_DIR}" || echo "⚠ offsite push failed — local backup above is unaffected"
fi
