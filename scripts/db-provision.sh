#!/usr/bin/env bash

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Creates the congopro-bridge Postgres role and database on this host, using
# the password `make secrets-init` already generated into congopro-bridge.env. Meant
# to run as root on the VPS (invoked by `make db-provision`, which uploads
# this file and runs it via sudo) — CREATE ROLE/DATABASE need postgres
# superuser privileges, which is why this is a separate, sudoers-gated script
# rather than something the app itself does at startup.
#
# Usage: db-provision.sh DB_NAME DB_USER SECRETS_FILE
# ─────────────────────────────────────────────────────────────────────────────

if [[ $# -ne 3 ]]; then
  echo "❌ Usage: db-provision.sh DB_NAME DB_USER SECRETS_FILE" >&2
  exit 1
fi

DB_NAME="$1"
DB_USER="$2"
SECRETS_FILE="$3"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "❌ Must run as root (this creates Postgres roles/databases)" >&2
  exit 1
fi

if [[ ! -s "${SECRETS_FILE}" ]]; then
  echo "❌ ${SECRETS_FILE} not found or empty — run 'make secrets-init' first" >&2
  exit 1
fi

DATABASE_URL="$(grep '^DATABASE_URL=' "${SECRETS_FILE}" | cut -d= -f2-)"
if [[ -z "${DATABASE_URL}" ]]; then
  echo "❌ DATABASE_URL not set in ${SECRETS_FILE} — run 'make secrets-init' first" >&2
  exit 1
fi

# postgres://user:PASSWORD@host:port/db?params — pull out just the password.
DB_PASSWORD="$(echo "${DATABASE_URL}" | sed -E 's#^postgres://[^:]+:([^@]+)@.*#\1#')"
if [[ -z "${DB_PASSWORD}" || "${DB_PASSWORD}" == "${DATABASE_URL}" ]]; then
  echo "❌ could not parse a password out of DATABASE_URL" >&2
  exit 1
fi

if sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='${DB_USER}'" | grep -q 1; then
  sudo -u postgres psql -c "ALTER ROLE \"${DB_USER}\" WITH LOGIN PASSWORD '${DB_PASSWORD}';" >/dev/null
  echo "✓ role ${DB_USER} already existed — password synced from congopro-bridge.env"
else
  sudo -u postgres psql -c "CREATE ROLE \"${DB_USER}\" WITH LOGIN PASSWORD '${DB_PASSWORD}';" >/dev/null
  echo "✓ created role ${DB_USER}"
fi

if sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'" | grep -q 1; then
  echo "✓ database ${DB_NAME} already exists"
else
  sudo -u postgres createdb -O "${DB_USER}" "${DB_NAME}"
  echo "✓ created database ${DB_NAME}"
fi

# CREATE EXTENSION postgis needs superuser (or a role granted extension-install
# rights) — ${DB_USER} deliberately isn't a superuser, so the app's own
# migration (which also runs "CREATE EXTENSION IF NOT EXISTS postgis") would
# fail with a permission error. Installing it here once, as postgres, makes
# that migration statement a no-op for everyone after.
sudo -u postgres psql -d "${DB_NAME}" -c "CREATE EXTENSION IF NOT EXISTS postgis;" >/dev/null
echo "✓ postgis extension ready on ${DB_NAME}"

echo "✓ provisioning complete — run 'make db-migrate-remote' to apply schema"
