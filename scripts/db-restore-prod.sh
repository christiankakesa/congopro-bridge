#!/usr/bin/env bash

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Restores a database dump onto the LIVE production database, overwriting its
# current contents. Destructive and irreversible — requires typing an exact
# confirmation phrase before it touches anything. Stops the app service for
# the duration of the restore so nothing writes mid-restore, and restarts it
# afterwards regardless of outcome.
#
# Run this only after db-restore-test.sh has verified the same dump file
# restores cleanly — an untested backup isn't a real backup.
#
# Usage: db-restore-prod.sh DB_NAME DUMP_FILE SERVICE
# Must run as root, with a tty attached (for the confirmation prompt) —
# invoked by `make db-restore` over `ssh -t`.
# ─────────────────────────────────────────────────────────────────────────────

DB_NAME="${1:?usage: db-restore-prod.sh DB_NAME DUMP_FILE SERVICE}"
DUMP_FILE="${2:?usage: db-restore-prod.sh DB_NAME DUMP_FILE SERVICE}"
SERVICE="${3:?usage: db-restore-prod.sh DB_NAME DUMP_FILE SERVICE}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "❌ must run as root" >&2
  exit 1
fi

if [[ ! -f "${DUMP_FILE}" ]]; then
  echo "❌ dump file not found: ${DUMP_FILE}" >&2
  exit 1
fi

if [[ ! -t 0 ]]; then
  echo "❌ no tty attached — refusing to run non-interactively" >&2
  exit 1
fi

echo "⚠️  This will OVERWRITE the live '${DB_NAME}' database with the contents of:"
echo "    ${DUMP_FILE}"
echo "⚠️  ${SERVICE} will be stopped for the duration of the restore."
echo ""
read -r -p "Type 'RESTORE ${DB_NAME}' to continue: " CONFIRM
if [[ "${CONFIRM}" != "RESTORE ${DB_NAME}" ]]; then
  echo "Aborted — confirmation did not match." >&2
  exit 1
fi

SERVICE_STOPPED=0
restart_service() {
  if [[ "${SERVICE_STOPPED}" -eq 1 ]]; then
    echo "▶ restarting ${SERVICE}…"
    systemctl start "${SERVICE}" || true
  fi
}
trap restart_service EXIT

echo "▶ stopping ${SERVICE}…"
systemctl stop "${SERVICE}"
SERVICE_STOPPED=1

echo "▶ terminating other connections to ${DB_NAME}…"
sudo -u postgres psql -d postgres -tAc \
  "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${DB_NAME}' AND pid <> pg_backend_pid();" >/dev/null

echo "▶ restoring ${DUMP_FILE} into ${DB_NAME}…"
sudo -u postgres pg_restore --clean --if-exists -d "${DB_NAME}" "${DUMP_FILE}"

echo "✓ restore complete — verify the app once it restarts before walking away"
