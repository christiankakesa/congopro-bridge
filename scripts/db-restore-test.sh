#!/usr/bin/env bash

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Restores a database dump into a throwaway database on the local dev
# Postgres (docker compose) to prove the backup is actually restorable — an
# untested backup isn't a real backup. Never touches production; the
# throwaway database is dropped afterwards either way (success or failure).
#
# Must be run from the repository root with the local `postgres` service
# already up (`make dev-db-up`).
#
# Usage: db-restore-test.sh DUMP_FILE [DB_USER]
# ─────────────────────────────────────────────────────────────────────────────

DUMP_FILE="${1:?usage: db-restore-test.sh DUMP_FILE [DB_USER]}"
DB_USER="${2:-congopro_bridge}"

if [[ ! -f "${DUMP_FILE}" ]]; then
  echo "❌ dump file not found: ${DUMP_FILE}" >&2
  exit 1
fi

TEST_DB="restore_test_$(date +%s)"
CONTAINER_DUMP="/tmp/$(basename "${DUMP_FILE}").restoretest"

cleanup() {
  docker compose exec -T postgres psql -U "${DB_USER}" -d postgres -tAc "DROP DATABASE IF EXISTS ${TEST_DB};" >/dev/null 2>&1 || true
  docker compose exec -T postgres rm -f "${CONTAINER_DUMP}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "▶ copying ${DUMP_FILE} into the postgres container…"
docker compose cp "${DUMP_FILE}" "postgres:${CONTAINER_DUMP}"

echo "▶ creating throwaway database ${TEST_DB}…"
docker compose exec -T postgres psql -U "${DB_USER}" -d postgres -tAc "CREATE DATABASE ${TEST_DB};"

echo "▶ restoring into ${TEST_DB}…"
docker compose exec -T postgres pg_restore -U "${DB_USER}" -d "${TEST_DB}" --no-owner --no-privileges "${CONTAINER_DUMP}"

COMPANY_COUNT="$(docker compose exec -T postgres psql -U "${DB_USER}" -d "${TEST_DB}" -tAc "SELECT count(*) FROM companies;")"
echo "✓ restore verified — companies table has ${COMPANY_COUNT// /} row(s) in the restored dump"
