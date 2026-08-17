#!/usr/bin/env bash
# `make dev` — one command for the local loop (Kora VPN devstack pattern):
#   1. start deps (Postgres, Meilisearch, Ollama) via docker compose
#   2. apply pending migrations
#   3. run the app with hot reload (air): every save of a .templ, .go, or
#      input.css file regenerates templ + Tailwind CSS and rebuilds the binary
#
# air is deliberately the only watcher — `templ --watch`/`tailwindcss --watch`
# proved unreliable when run detached from a TTY. See .air.toml.
#
# Ctrl+C stops the app; deps keep running so restarts are fast.
# `make dev-down` stops the deps.
set -Eeuo pipefail

cd "$(dirname "$0")/.."

# Caller (Makefile) passes DATABASE_URL; keep the same default as the db-* targets.
export DATABASE_URL="${DATABASE_URL:-postgres://congopro_bridge:congopro_bridge@localhost:5433/congopro_bridge?sslmode=disable}"

# env_or_dotenv KEY: real environment first, then .env — parsed with sed, NOT
# sourced through make, whose `-include` would expand `$` sequences in values
# (the app does the same thing in internal/config loadDotEnv).
env_or_dotenv() {
	if [ -n "${!1:-}" ]; then printf '%s' "${!1}"; return; fi
	sed -nE "s/^$1=([^#]*)#.*/\1/p; s/^$1=(.*)\$/\1/p" .env 2>/dev/null \
		| sed -E "s/[[:space:]]*$//; s/^'(.*)'$/\1/; s/^\"(.*)\"$/\1/" | head -1
}
export PORT="${PORT:-$(env_or_dotenv PORT)}"
export PORT="${PORT:-8080}"
# The app runs natively and reaches Ollama via the published port, but
# Meilisearch runs in a container and cannot see 127.0.0.1:11434 — it must
# call the Ollama CONTAINER over the compose network instead, or the settings
# task that installs the semantic embedder wedges forever and indexing never
# finishes (Meilisearch processes tasks serially).
export OLLAMA_EMBEDDER_URL="${OLLAMA_EMBEDDER_URL:-http://ollama:11434}"

say() { printf '\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()  { printf '\033[1;32m✓ %s\033[0m\n' "$*"; }
die() { printf '\033[1;31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

# --- Preflight ----------------------------------------------------------------
command -v docker >/dev/null 2>&1 || die "docker not found — install it or start the daemon first"
docker compose version >/dev/null 2>&1 || die "docker compose plugin not available"
command -v templ >/dev/null 2>&1 || die "templ CLI not found: go install github.com/a-h/templ/cmd/templ@latest"
command -v tailwindcss >/dev/null 2>&1 || die "tailwindcss CLI not found (download line is in the Makefile 'css' target)"

# A stale compose `app` container (make docker-up) or an orphaned dev binary
# holding the port makes the new app die on listen — catch it up front.
if command -v ss >/dev/null 2>&1 && ss -ltn "sport = :$PORT" 2>/dev/null | grep -q LISTEN; then
	die "port $PORT is already in use — stop the other instance first (make docker-down), or run with PORT=8081"
fi

# --- 1. Deps -------------------------------------------------------------------
say "starting deps (postgres :5433, meilisearch :7700, ollama :11434)…"
# ollama-init (re)pulls the models; the first ever run downloads ~1 GB and can
# take minutes. Every later run is a quick no-op check.
docker compose up -d --wait postgres meilisearch ollama ollama-init
ok "deps up"

# --- 2. Migrations --------------------------------------------------------------
say "applying pending migrations…"
go run ./cmd/congopro-bridge -migrate
ok "database is up to date"

# First-run hint, non-fatal: a migrated-but-empty database serves an empty site
# and the reason is not obvious from the browser.
rows="$(docker compose exec -T postgres psql -U congopro_bridge -d congopro_bridge -tAc \
	'SELECT count(*) FROM companies' 2>/dev/null || echo '?')"
if [ "$rows" = "0" ]; then
	echo "  note: companies table is empty — run 'make db-import' once to load the legacy JSON export."
fi

# --- 3. App with hot reload ------------------------------------------------------
if command -v air >/dev/null 2>&1; then
	say "starting app on http://localhost:$PORT with hot reload (edit .templ/.go/input.css → rebuild)…"
	air
else
	echo "  (tip: install air for hot reload — go install github.com/air-verse/air@latest)"
	echo "  (falling back to go run: any change needs a manual restart)"
	say "starting app on http://localhost:$PORT…"
	templ generate && \
		tailwindcss -i ./internal/web/css/input.css -o ./internal/web/css/style.min.css --minify && \
		go run ./cmd/congopro-bridge
fi
