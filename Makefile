# Target naming: <where>-<what>-<verb>.
#   dev-*    act on your local machine (native app, dockerised Postgres)
#   prod-*   act on the live server over SSH
#   image-*  build the production container image locally
#   bare     environment-neutral (go build/test/vet, codegen, help)
# Anything that can destroy data says so in its help text.
#
# NOTE: do NOT `-include .env` + `export` here. Make parses .env as Makefile
# syntax and expands `$` sequences inside values — an SMTP password like
# "ab$9kC$d2..." silently arrives as "ab" (observed: 12 chars in the file,
# 4 by the time a make-spawned process read it) and the mail server answers
# a baffling 535. The Go app loads .env itself (internal/config loadDotEnv,
# with real environment variables winning), docker compose reads .env
# natively, and the prod-deploy targets below read the handful of keys they need
# via the sed helper — none of which expand anything.
#
# .env carries no trailing comments after values (see .env.template), so the
# extraction is a plain split on the first `=`. The `[^#]*#` form is kept so
# older .env files with inline comments still work. The trailing sed strips
# one layer of surrounding single or double quotes.
_env_var  = $(shell sed -nE "s/^$1=([^#]*)#.*/\1/p; s/^$1=(.*)$$/\1/p" .env 2>/dev/null | sed -E "s/[[:space:]]*$$//; s/^'(.*)'$$/\1/; s/^\"(.*)\"$$/\1/" | head -1)

DEPLOY_USER  ?= $(or $(call _env_var,DEPLOY_USER),ops)
DEPLOY_HOST  ?= $(or $(call _env_var,DEPLOY_HOST),xxx.xxx.xxx.xxx)
DEPLOY_PORT  ?= $(or $(call _env_var,DEPLOY_PORT),4242)
SSH_KEY      ?= $(patsubst ~/%,$(HOME)/%,$(or $(call _env_var,SSH_KEY),$(HOME)/.ssh/id_ed25519))
REMOTE_DIR   ?= $(or $(call _env_var,REMOTE_DIR),/opt/congopro-bridge)
# The app's systemd EnvironmentFile on the server (deploy/systemd/*.service
# references the same path). Named after the service rather than
# "secrets.env" so ownership is obvious in directory listings — the
# prod-secrets-init and db-* targets below are the single source of this name.
APP_ENV_FILE := congopro-bridge.env
MEILI_DIR    ?= /opt/meilisearch
MEILI_VERSION ?= v1.43.1
IMAGE        ?= congopro-bridge
TAG          ?= latest
DOMAIN       ?= congopro.com
CMD_PATH     := ./cmd/congopro-bridge
BINARY       := congopro-bridge
BUILD_DIR    := ./build
GENERATIVE_MODEL ?= $(or $(call _env_var,GENERATIVE_MODEL),gemma3:1b)
EMBEDDING_MODEL ?= $(or $(call _env_var,EMBEDDING_MODEL),nomic-embed-text)
SERVICE      := congopro-bridge
TAILWIND_CLI := $(shell which tailwindcss)
TEMPL_CLI    := $(shell which templ)

# Database — local dev (docker compose, see db-* targets below)
POSTGRES_PORT     ?= 5433
DB_NAME           ?= congopro_bridge
DB_USER           ?= congopro_bridge
DB_PASSWORD       ?= congopro_bridge
PG_VERSION        ?= 16
PG_CONTAINER      ?= congopro-bridge-postgres-1
LOCAL_DATABASE_URL ?= postgres://$(DB_USER):$(DB_PASSWORD)@localhost:$(POSTGRES_PORT)/$(DB_NAME)?sslmode=disable

# Database — backups (production)
BACKUP_DIR       ?= /opt/congopro-bridge/backups
BACKUP_KEEP      ?= 14
LOCAL_BACKUP_DIR ?= ./backups
BACKUP_FILE      ?=
# Object name inside the R2 bucket (prod-backup-offsite-pull / -restore-offsite).
# Namespaced deliberately: a bare NAME would be clobbered by any environment
# variable of the same name, since make imports the environment.
BACKUP_NAME      ?=

_ssh_opts    := -p $(DEPLOY_PORT) -i $(SSH_KEY) \
                -o StrictHostKeyChecking=accept-new \
                -o ConnectTimeout=10
SSH          := ssh $(_ssh_opts) $(DEPLOY_USER)@$(DEPLOY_HOST)
RSYNC        := rsync -az --progress --delete \
                -e "ssh $(_ssh_opts)"

.PHONY: all build build-quick clean css help templ test vet image-build image-run image-save \
         dev dev-admin-create dev-db-down dev-db-import dev-db-import-ads dev-db-migrate \
         dev-db-psql dev-db-reset dev-db-scrub dev-db-restore-test dev-db-restore-test-offsite dev-db-up dev-deps-down dev-deps-reset \
         dev-deps-up dev-mail-down dev-mail-test dev-mail-up dev-search-reset dev-stack-down \
         dev-stack-logs dev-stack-logs-app dev-stack-reset dev-stack-up dev-test-integration \
         prod-app-logs prod-app-push prod-app-restart prod-app-start prod-app-status \
         prod-app-stop prod-backup-install prod-backup-list prod-backup-logs prod-backup-offsite-configure prod-backup-offsite-pull prod-backup-offsite-status prod-db-restore-offsite prod-backup-now \
         prod-backup-pull prod-backup-status prod-bootstrap-all prod-bootstrap-app \
         prod-config-push prod-db-check prod-db-import prod-db-import-ads prod-db-install \
         prod-db-migrate prod-db-provision prod-db-restore prod-db-status prod-deploy \
         prod-ollama-clean prod-ollama-install prod-ollama-limit prod-ollama-logs \
         prod-ollama-pull prod-ollama-reset prod-ollama-setup prod-ollama-status prod-ping \
         prod-search-config-push prod-search-install prod-search-logs prod-search-reset \
         prod-search-restart prod-search-service-install prod-search-setup prod-search-start \
         prod-search-status prod-search-stop prod-search-traefik-push prod-secrets-init prod-secrets-list prod-secrets-set \
         prod-service-install prod-ssh prod-traefik-logs prod-traefik-reload

all: build

css:
	@echo "▶ Compiling Tailwind CSS using local binary…"
	@if [ ! -f $(TAILWIND_CLI) ]; then \
		echo "❌ Error: ./tailwindcss not found at root."; \
		echo "Download it via: curl -sLO https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-linux-x64 && mv tailwindcss-linux-x64 tailwindcss && chmod +x tailwindcss"; \
		exit 1; \
	fi
	@$(TAILWIND_CLI) -i ./internal/web/css/input.css -o ./internal/web/css/style.min.css --minify
	@echo "✓ CSS compiled"

templ:
	@echo "▶ Generating templ components…"
	@if [ ! -x "$(TEMPL_CLI)" ]; then \
		echo "❌ Error: templ CLI not found."; \
		echo "Install it via: go install github.com/a-h/templ/cmd/templ@latest"; \
		exit 1; \
	fi
	@$(TEMPL_CLI) generate ./...
	@echo "✓ templ components generated"

build: css templ
	@echo "▶ Building $(BINARY)…"
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	  go build \
	    -ldflags="-s -w -extldflags '-static' \
	              -X main.version=$(shell git describe --tags --always 2>/dev/null || echo dev)" \
	    -trimpath \
	    -buildvcs=false \
	    -o $(BUILD_DIR)/$(BINARY) \
	    $(CMD_PATH)
	@echo "✓ $(BUILD_DIR)/$(BINARY)"

build-quick:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY) $(CMD_PATH)

clean:
	@rm -rf $(BUILD_DIR)
	@echo "✓ clean"

test:
	go test ./... -v -race -timeout 60s

vet:
	go vet ./...

image-build:
	@echo "▶ docker build $(IMAGE):$(TAG)…"
	docker build \
	  --build-arg VERSION=$(shell git describe --tags --always 2>/dev/null || echo dev) \
	  -t $(IMAGE):$(TAG) \
	  .
	@echo "✓ $(IMAGE):$(TAG)"

image-save: image-build
	@mkdir -p $(BUILD_DIR)
	docker save $(IMAGE):$(TAG) | gzip > $(BUILD_DIR)/$(IMAGE)-$(TAG).tar.gz
	@echo "✓ $(BUILD_DIR)/$(IMAGE)-$(TAG).tar.gz"

image-run: image-build
	docker run -p 8080:8080 $(IMAGE):$(TAG)

dev-stack-up: css
	@echo "▶ Starting services…"
	docker compose up -d --build
	@echo "✓ Services running"

dev-stack-down:
	@echo "▶ Stopping services…"
	docker compose down
	@echo "✓ Services stopped"

dev-stack-reset:
	@echo "▶ Stopping services (keeping ollama_data volume)…"
	docker compose down
	docker volume rm congopro-bridge_meili_data 2>/dev/null || true
	@echo "✓ Services stopped, meili_data removed, ollama_data preserved"

dev-stack-logs:
	@echo "▶ Starting docker logs…"
	docker compose logs -f

dev-stack-logs-app:
	@echo "▶ Starting docker logs…"
	docker compose logs -f app

dev-search-reset:
	@echo "▶ Resetting Meilisearch index (keeping Ollama models)…"
	docker compose rm -sf meilisearch
	docker volume rm congopro-bridge_meili_data
	docker compose up -d meilisearch
	@echo "✓ Meilisearch volume wiped and restarted — app will re-index on next boot"

# ──────────────────────────────────────────────────────────────────────────────
# Email (SMTP — OVH EmailPro in production, Mailpit locally)
# ──────────────────────────────────────────────────────────────────────────────

# Local capture: every email the app sends lands in Mailpit's web UI instead
# of a real mailbox. The app reaches it with SMTP_HOST=localhost SMTP_PORT=1025
# SMTP_TLS=none (no credentials — the sender refuses passwords in the clear).
dev-mail-up:
	@echo "▶ Starting Mailpit (local email capture)…"
	docker compose --profile dev up -d mail
	@echo "✓ SMTP on 127.0.0.1:1025, web UI at http://localhost:8025"

dev-mail-down:
	@echo "▶ Stopping Mailpit…"
	docker compose stop mail
	@echo "✓ Mailpit stopped"

# Proves the SMTP account from .env works end-to-end — run this against the
# real OVH account (and a real inbox) once, before anything depends on email.
dev-mail-test:
	@if [ -z "$(TO)" ]; then echo "Usage: make dev-mail-test TO=you@example.com" >&2; exit 2; fi
	go run ./cmd/dev-mail-test $(TO)

# ──────────────────────────────────────────────────────────────────────────────
# Database (local dev — docker compose)
# ──────────────────────────────────────────────────────────────────────────────

dev-db-up:
	@echo "▶ Starting local Postgres…"
	docker compose up -d --wait postgres
	@echo "✓ Postgres ready on 127.0.0.1:$(POSTGRES_PORT)"

dev-db-down:
	@echo "▶ Stopping local Postgres…"
	docker compose stop postgres
	@echo "✓ Postgres stopped (data volume kept — use docker-down-v-style removal to wipe it)"

dev-db-migrate: dev-db-up
	@echo "▶ Applying migrations to local Postgres…"
	DATABASE_URL="$(LOCAL_DATABASE_URL)" go run $(CMD_PATH) -migrate

# DESTRUCTIVE (local only): drops the local Postgres volume and re-applies
# migrations, leaving an empty schema. `docker compose rm -sf` first because a
# merely-stopped container still pins its volume (the same trap dev-search-reset hit).
dev-db-reset:
	@echo "▶ DESTRUCTIVE: wiping local Postgres data…"
	@docker compose rm -sf postgres
	@docker volume rm congopro-bridge_postgres_data 2>/dev/null || true
	@$(MAKE) dev-db-migrate
	@echo "✓ local database reset (empty schema) — run 'make dev-db-import' to reload companies"

# DESTRUCTIVE (local only, narrow): removes customer-generated test data —
# customers and, by foreign key, their claims, sessions and promotions. The
# FKs also SET NULL companies.claimed_by_customer_id (so "Fiche vérifiée"
# clears itself) and ads.customer_id (campaigns survive, just unlinked).
# otp_codes is keyed by email, not customer_id, so it needs its own delete.
#
# Keeps the catalogue (companies, ads) and your staff account + TOTP enrolment
# — that is the whole point over dev-db-reset, which would make you re-enrol.
# Does NOT cancel Stripe test subscriptions; use `stripe subscriptions cancel`.
dev-db-scrub: dev-db-up
	@echo "▶ Scrubbing customer test data from the local database…"
	@docker exec -e PGPASSWORD=$(DB_PASSWORD) $(PG_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -q \
	  -c "DELETE FROM otp_codes;" \
	  -c "DELETE FROM customers;" \
	  -c "SELECT 'customers: '||count(*) FROM customers UNION ALL \
	      SELECT 'claims: '||count(*) FROM company_claims UNION ALL \
	      SELECT 'promotions: '||count(*) FROM promotions UNION ALL \
	      SELECT 'companies kept: '||count(*) FROM companies UNION ALL \
	      SELECT 'staff users kept: '||count(*) FROM users;"
	@echo "✓ scrubbed (catalogue and staff login untouched)"

# Opens a psql shell on the local dev database.
dev-db-psql: dev-db-up
	@docker exec -it -e PGPASSWORD=$(DB_PASSWORD) $(PG_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME)

# One-time (idempotent) import of the legacy embedded JSON export into local Postgres.
dev-db-import: dev-db-migrate
	@echo "▶ Importing companies into local Postgres…"
	DATABASE_URL="$(LOCAL_DATABASE_URL)" go run $(CMD_PATH) -import

# One-time (idempotent) import of the legacy embedded ads.yml into local Postgres.
dev-db-import-ads: dev-db-migrate
	@echo "▶ Importing ad campaigns into local Postgres…"
	DATABASE_URL="$(LOCAL_DATABASE_URL)" go run $(CMD_PATH) -import-ads

# Interactive: creates a staff account (super_admin) against local Postgres.
# Prints a TOTP enrollment secret/URI you'll need to log in — see -create-admin.
dev-admin-create: dev-db-up
	DATABASE_URL="$(LOCAL_DATABASE_URL)" go run $(CMD_PATH) -create-admin

# Integration tests are gated behind the "integration" build tag so `make test`
# stays fast and DB-free. Add tests with `//go:build integration` as the
# schema grows; this target is a no-op (passes trivially) until then.
dev-test-integration: dev-db-migrate
	@echo "▶ Running integration tests against local Postgres…"
	DATABASE_URL="$(LOCAL_DATABASE_URL)" go test ./... -tags=integration -race -timeout 120s

# ──────────────────────────────────────────────────────────────────────────────
# Dev loop
# ──────────────────────────────────────────────────────────────────────────────

# One command for local iteration: starts the dockerised deps (postgres,
# meilisearch, ollama), applies migrations, regenerates templ/CSS on change,
# and runs the app with hot reload (air if installed, go run otherwise).
# Ctrl+C stops app + watchers; deps keep running. First run pulls ~1 GB of
# Ollama models once. See scripts/dev.sh.
dev:
	DATABASE_URL="$(LOCAL_DATABASE_URL)" bash scripts/dev.sh

# Starts the dockerised dev dependencies without running the app.
dev-deps-up:
	@echo "▶ Starting dev deps (postgres, meilisearch, ollama)…"
	@docker compose up -d --wait postgres meilisearch ollama
	@echo "✓ dev deps running"

# DESTRUCTIVE (local only): wipes the Postgres AND Meilisearch volumes, then
# re-applies migrations. Ollama models are kept — re-pulling them costs ~1 GB.
dev-deps-reset:
	@echo "▶ DESTRUCTIVE: wiping local postgres + meilisearch data (ollama models kept)…"
	@docker compose rm -sf postgres meilisearch
	@docker volume rm congopro-bridge_postgres_data 2>/dev/null || true
	@docker volume rm congopro-bridge_meili_data 2>/dev/null || true
	@$(MAKE) dev-db-migrate
	@docker compose up -d --wait meilisearch
	@echo "✓ local deps reset — run 'make dev-db-import' to reload companies"

# Stops only the dev deps started by `make dev` (data volumes are kept).
dev-deps-down:
	@echo "▶ Stopping dev deps…"
	@docker compose stop postgres meilisearch ollama ollama-init
	@echo "✓ dev deps stopped"

prod-ping:
	@echo "▶ pinging $(DEPLOY_USER)@$(DEPLOY_HOST):$(DEPLOY_PORT)…"
	@$(SSH) "echo '✓ connected as $(DEPLOY_USER) on $(DEPLOY_HOST)'"

prod-ssh:
	ssh $(_ssh_opts) $(DEPLOY_USER)@$(DEPLOY_HOST)

# ──────────────────────────────────────────────────────────────────────────────
# App deployment
# ──────────────────────────────────────────────────────────────────────────────

prod-deploy: prod-app-push prod-config-push prod-app-restart
	@echo ""
	@echo "╔══════════════════════════════════════╗"
	@echo "║  ✓ Deployment complete               ║"
	@echo "║  https://$(DOMAIN)                ║"
	@echo "╚══════════════════════════════════════╝"

prod-app-push: build
	@echo "▶ Uploading binary → $(DEPLOY_HOST):$(REMOTE_DIR)/"
	@$(SSH) "sudo mkdir -p $(REMOTE_DIR) && sudo chown $(DEPLOY_USER): $(REMOTE_DIR)"
	@$(RSYNC) $(BUILD_DIR)/$(BINARY) $(DEPLOY_USER)@$(DEPLOY_HOST):$(REMOTE_DIR)/$(BINARY)
	@$(SSH) "chmod +x $(REMOTE_DIR)/$(BINARY)"
	@echo "✓ binary uploaded"

prod-config-push:
	@echo "▶ Uploading Traefik dynamic config…"
	@$(SSH) "sudo mkdir -p /srv/traefik/dynamic"
	@$(RSYNC) deploy/traefik/dynamic/ $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/traefik-dynamic/
	@$(SSH) "sudo cp -r /tmp/traefik-dynamic/. /srv/traefik/dynamic/ && rm -rf /tmp/traefik-dynamic"
	@echo "✓ config uploaded"
	@$(MAKE) prod-traefik-reload

prod-service-install:
	@echo "▶ Installing $(SERVICE) systemd unit…"
	@$(RSYNC) deploy/systemd/$(SERVICE).service $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/$(SERVICE).service
	@$(SSH) "sudo mv /tmp/$(SERVICE).service /etc/systemd/system/$(SERVICE).service && sudo systemctl daemon-reload"
	@echo "✓ unit installed — run 'make prod-app-start' to enable"

# First-time app setup: installs systemd unit, deploys binary, enables on boot.
prod-bootstrap-app: prod-service-install prod-secrets-init prod-deploy
	@$(SSH) "sudo systemctl enable $(SERVICE)"
	@echo "✓ $(SERVICE) enabled on boot"

# Generates secrets on the server (never downloaded, never printed) and writes them to
# EnvironmentFile(s). Idempotent per-key: only fills in whatever is missing, so re-running
# after adding a new secret doesn't touch keys already in place.
prod-secrets-init:
	@echo "▶ Ensuring secrets exist on $(DEPLOY_HOST)…"
	@$(SSH) "sudo mkdir -p $(REMOTE_DIR) $(MEILI_DIR)/etc; \
	  sudo touch $(REMOTE_DIR)/$(APP_ENV_FILE) $(MEILI_DIR)/etc/secrets.env; \
	  if ! sudo grep -q '^MEILI_MASTER_KEY=' $(REMOTE_DIR)/$(APP_ENV_FILE); then \
	    KEY=\$$(openssl rand -base64 48); \
	    echo \"MEILI_MASTER_KEY=\$$KEY\" | sudo tee -a $(REMOTE_DIR)/$(APP_ENV_FILE) >/dev/null; \
	    echo \"MEILI_MASTER_KEY=\$$KEY\" | sudo tee -a $(MEILI_DIR)/etc/secrets.env >/dev/null; \
	    echo '✓ generated MEILI_MASTER_KEY'; \
	  else \
	    echo '✓ MEILI_MASTER_KEY already present'; \
	  fi; \
	  if ! sudo grep -q '^DATABASE_URL=' $(REMOTE_DIR)/$(APP_ENV_FILE); then \
	    DBPASS=\$$(openssl rand -base64 32 | tr -dc 'a-zA-Z0-9'); \
	    echo \"DATABASE_URL=postgres://$(DB_USER):\$$DBPASS@localhost:5432/$(DB_NAME)?sslmode=disable\" | sudo tee -a $(REMOTE_DIR)/$(APP_ENV_FILE) >/dev/null; \
	    echo '✓ generated DATABASE_URL (Postgres password)'; \
	  else \
	    echo '✓ DATABASE_URL already present'; \
	  fi; \
	  sudo chown root:root $(REMOTE_DIR)/$(APP_ENV_FILE) $(MEILI_DIR)/etc/secrets.env; \
	  sudo chmod 600 $(REMOTE_DIR)/$(APP_ENV_FILE) $(MEILI_DIR)/etc/secrets.env"

# Writes ONE key into the server's EnvironmentFile. The value is typed with
# echo off and travels over stdin — it never appears on screen, in your shell
# history, or in the process list on either machine. Idempotent: an existing
# definition of the key is replaced, never duplicated (systemd silently takes
# the last one, which makes duplicates a nasty way to lose an afternoon).
#
#   make prod-secrets-set KEY=STRIPE_SECRET_KEY
#   make prod-secrets-set KEY=STRIPE_WEBHOOK_SECRET
#   make prod-secrets-set KEY=STRIPE_PRICE_ID
#
# Live keys belong here and ONLY here — never in .env, which is dev and holds
# test keys. Run prod-app-restart afterwards to load them.
prod-secrets-set:
	@if [ -z "$(KEY)" ]; then echo "Usage: make prod-secrets-set KEY=STRIPE_SECRET_KEY" >&2; exit 2; fi
	@$(RSYNC) scripts/secret-set.sh $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/secret-set.sh
	@printf "Value for %s (hidden): " "$(KEY)"; \
	  stty -echo 2>/dev/null || true; \
	  IFS= read -r VAL; \
	  stty echo 2>/dev/null || true; \
	  echo; \
	  printf '%s\n' "$$VAL" | $(SSH) "chmod +x /tmp/secret-set.sh && sudo /tmp/secret-set.sh '$(KEY)' '$(REMOTE_DIR)/$(APP_ENV_FILE)'; rm -f /tmp/secret-set.sh"
	@echo "  → apply with: make prod-app-restart"

# Lists which keys the server's EnvironmentFile defines. Names only — values
# are never printed, so this is safe to run while someone is watching.
prod-secrets-list:
	@echo "▶ Keys defined in $(REMOTE_DIR)/$(APP_ENV_FILE):"
	@$(SSH) "sudo grep -oE '^[A-Za-z_][A-Za-z0-9_]*=' $(REMOTE_DIR)/$(APP_ENV_FILE) 2>/dev/null | tr -d '=' | sort | sed 's/^/    /' || echo '    (file not found)'"

# Full server bootstrap: Ollama + Meilisearch + app. Run once on a fresh server.
prod-bootstrap-all: prod-ollama-setup prod-search-setup prod-bootstrap-app
	@echo ""
	@echo "╔══════════════════════════════════════╗"
	@echo "║  ✓ Full stack ready                  ║"
	@echo "║  https://$(DOMAIN)                ║"
	@echo "╚══════════════════════════════════════╝"

# ──────────────────────────────────────────────────────────────────────────────
# App service
# ──────────────────────────────────────────────────────────────────────────────

prod-app-start:
	@$(SSH) "sudo systemctl enable --now $(SERVICE)"
	@echo "✓ $(SERVICE) started"

prod-app-stop:
	@$(SSH) "sudo systemctl stop $(SERVICE)"
	@echo "✓ $(SERVICE) stopped"

prod-app-restart:
	@echo "▶ Restarting $(SERVICE)…"
	@$(SSH) "sudo systemctl restart $(SERVICE)"
	@sleep 2
	@$(MAKE) prod-app-status

prod-app-status:
	@$(SSH) "sudo systemctl status $(SERVICE) --no-pager -l || true"

prod-app-logs:
	$(SSH) "sudo journalctl -u $(SERVICE) -f --no-pager"

# ──────────────────────────────────────────────────────────────────────────────
# Traefik
# ──────────────────────────────────────────────────────────────────────────────

prod-traefik-reload:
	@echo "▶ Triggering Traefik dynamic config reload…"
	@$(SSH) "sudo touch /srv/traefik/dynamic/congopro-bridge.yml"
	@echo "✓ Traefik will pick up changes within a few seconds"

prod-traefik-logs:
	$(SSH) "sudo journalctl -u traefik -f --no-pager 2>/dev/null || sudo docker logs -f $$(sudo docker ps -qf name=traefik)"

# ──────────────────────────────────────────────────────────────────────────────
# Ollama
# ──────────────────────────────────────────────────────────────────────────────

OLLAMA_MODELS ?= $(GENERATIVE_MODEL) $(EMBEDDING_MODEL)
OLLAMA_NUM_THREADS ?= 2

prod-ollama-install:
	@echo "▶ Installing Ollama on $(DEPLOY_HOST)…"
	@$(SSH) "curl -fsSL https://ollama.com/install.sh | sh"
	@$(SSH) "sudo systemctl enable --now ollama"
	@echo "✓ Ollama installed and started"

prod-ollama-limit:
	@echo "▶ Limiting Ollama to $(OLLAMA_NUM_THREADS) CPU threads…"
	@$(SSH) "sudo mkdir -p /etc/systemd/system/ollama.service.d && \
	         echo '[Service]' | sudo tee /etc/systemd/system/ollama.service.d/override.conf >/dev/null && \
	         echo 'Environment=\"OLLAMA_NUM_THREADS=$(OLLAMA_NUM_THREADS)\"' | sudo tee -a /etc/systemd/system/ollama.service.d/override.conf >/dev/null && \
	         sudo systemctl daemon-reload && \
	         sudo systemctl restart ollama"
	@echo "✓ Ollama CPU limit applied"

prod-ollama-pull:
	@echo "▶ Pulling models: $(OLLAMA_MODELS)…"
	@$(SSH) "for model in $(OLLAMA_MODELS); do echo \"Pulling \$$model...\"; ollama pull \$$model; done"
	@echo "✓ All models pulled"

prod-ollama-clean:
	@echo "▶ Removing all Ollama models on $(DEPLOY_HOST)…"
	@$(SSH) "ollama list | tail -n +2 | awk '{print \$$1}' | xargs -I {} ollama rm {}"
	@echo "✓ All models removed"

prod-ollama-reset: prod-ollama-clean prod-ollama-pull
	@echo "✓ Models reset to: $(OLLAMA_MODELS)"

prod-ollama-status:
	@$(SSH) "sudo systemctl status ollama --no-pager -l || true"
	@$(SSH) "ollama list"

prod-ollama-setup: prod-ollama-install prod-ollama-limit prod-ollama-pull
	@echo "╔═════════════════════════════════════════════════════════════════════════════╗"
	@echo "║  Ollama is ready with $(OLLAMA_MODELS)                            ║"
	@echo "╚═════════════════════════════════════════════════════════════════════════════╝"

prod-ollama-logs:
	$(SSH) "sudo journalctl -u ollama -f --no-pager"

# ──────────────────────────────────────────────────────────────────────────────
# Meilisearch (production — systemd)
# ──────────────────────────────────────────────────────────────────────────────

prod-search-install:
	@echo "▶ Installing Meilisearch $(MEILI_VERSION) on $(DEPLOY_HOST)…"
	@$(SSH) "sudo useradd -r -s /bin/false meilisearch 2>/dev/null || true"
	@$(SSH) "sudo mkdir -p $(MEILI_DIR)/bin $(MEILI_DIR)/data/db $(MEILI_DIR)/data/dumps $(MEILI_DIR)/etc"
	@$(SSH) "sudo chown -R meilisearch:meilisearch $(MEILI_DIR)"
	@$(SSH) "curl -L https://github.com/meilisearch/meilisearch/releases/download/$(MEILI_VERSION)/meilisearch-linux-amd64 -o /tmp/meilisearch && sudo mv /tmp/meilisearch $(MEILI_DIR)/bin/meilisearch"
	@$(SSH) "sudo chmod +x $(MEILI_DIR)/bin/meilisearch"
	@echo "✓ Meilisearch $(MEILI_VERSION) installed at $(MEILI_DIR)/bin/meilisearch"

prod-search-config-push:
	@echo "▶ Uploading meilisearch.toml…"
	@$(RSYNC) deploy/meilisearch/meilisearch.toml $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/meilisearch.toml
	@$(SSH) "sudo mv /tmp/meilisearch.toml $(MEILI_DIR)/etc/meilisearch.toml && sudo chown meilisearch:meilisearch $(MEILI_DIR)/etc/meilisearch.toml"
	@echo "✓ meilisearch.toml deployed"

prod-search-service-install:
	@echo "▶ Installing meilisearch systemd unit…"
	@$(RSYNC) deploy/meilisearch/meilisearch.service $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/meilisearch.service
	@$(SSH) "sudo mv /tmp/meilisearch.service /etc/systemd/system/meilisearch.service && sudo systemctl daemon-reload"
	@echo "✓ systemd unit installed"

prod-search-traefik-push:
	@echo "▶ Uploading Meilisearch Traefik config…"
	@$(RSYNC) deploy/meilisearch/meilisearch.yml $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/meilisearch.yml
	@$(SSH) "sudo mkdir -p /srv/traefik/dynamic && sudo mv /tmp/meilisearch.yml /srv/traefik/dynamic/meilisearch.yml"
	@$(MAKE) prod-traefik-reload
	@echo "✓ Traefik config deployed"

# First-time Meilisearch setup: installs binary, config, systemd unit, Traefik, enables service.
prod-search-setup: prod-search-install prod-search-config-push prod-search-service-install prod-search-traefik-push prod-secrets-init
	@$(SSH) "sudo systemctl enable --now meilisearch"
	@echo ""
	@echo "╔══════════════════════════════════════╗"
	@echo "║  ✓ Meilisearch ready                 ║"
	@echo "║  https://meili.$(DOMAIN)          ║"
	@echo "╚══════════════════════════════════════╝"

prod-search-start:
	@$(SSH) "sudo systemctl enable --now meilisearch"
	@echo "✓ meilisearch started"

prod-search-stop:
	@$(SSH) "sudo systemctl stop meilisearch"
	@echo "✓ meilisearch stopped"

prod-search-restart:
	@echo "▶ Restarting meilisearch…"
	@$(SSH) "sudo systemctl restart meilisearch"
	@sleep 2
	@$(MAKE) prod-search-status

prod-search-status:
	@$(SSH) "sudo systemctl status meilisearch --no-pager -l || true"

prod-search-logs:
	$(SSH) "sudo journalctl -u meilisearch -f --no-pager"

# Wipes the index on the remote server; app re-indexes automatically on next start.
prod-search-reset:
	@echo "▶ Wiping Meilisearch data on $(DEPLOY_HOST) (index will rebuild on next app start)…"
	@$(MAKE) prod-search-stop
	@$(SSH) "sudo rm -rf $(MEILI_DIR)/data/db && sudo mkdir -p $(MEILI_DIR)/data/db && sudo chown meilisearch:meilisearch $(MEILI_DIR)/data/db"
	@$(MAKE) prod-search-start
	@echo "✓ Meilisearch index wiped"

# ──────────────────────────────────────────────────────────────────────────────
# Database (production — self-hosted PostgreSQL via systemd, not docker)
# ──────────────────────────────────────────────────────────────────────────────

prod-db-install:
	@echo "▶ Installing PostgreSQL $(PG_VERSION) + PostGIS on $(DEPLOY_HOST)…"
	@$(SSH) "MISSING=''; \
	  dpkg -s postgresql-$(PG_VERSION) >/dev/null 2>&1 || MISSING=\"\$$MISSING postgresql-$(PG_VERSION)\"; \
	  dpkg -s postgresql-$(PG_VERSION)-postgis-3 >/dev/null 2>&1 || MISSING=\"\$$MISSING postgresql-$(PG_VERSION)-postgis-3\"; \
	  if [ -n \"\$$MISSING\" ]; then sudo apt-get update -qq && sudo apt-get install -y \$$MISSING; else echo '✓ already installed'; fi"
	@$(SSH) "sudo systemctl enable --now postgresql"
	@echo "✓ PostgreSQL installed and running"

# Creates the app's role and database using the password prod-secrets-init already generated
# into $(APP_ENV_FILE). Requires sudo on the host (CREATE ROLE/DATABASE need postgres superuser) —
# gate scripts/db-provision.sh behind a dedicated sudoers entry rather than full root sudo.
prod-db-provision: prod-secrets-init
	@echo "▶ Provisioning database role and schema on $(DEPLOY_HOST)…"
	@$(RSYNC) scripts/db-provision.sh $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/db-provision.sh
	@$(SSH) "chmod +x /tmp/db-provision.sh && sudo /tmp/db-provision.sh '$(DB_NAME)' '$(DB_USER)' '$(REMOTE_DIR)/$(APP_ENV_FILE)' && rm -f /tmp/db-provision.sh"
	@echo "✓ database provisioned — run 'make prod-db-migrate' to apply schema"

prod-db-status:
	@$(SSH) "sudo systemctl status postgresql --no-pager -l || true"

prod-db-check:
	@echo "▶ Checking remote database and PostGIS…"
	@$(SSH) "sudo -u postgres psql -d $(DB_NAME) -c 'SELECT postgis_version();' -c '\dt'"

# Applies pending migrations using the already-deployed binary and the server's own
# $(APP_ENV_FILE) — no credentials ever leave the host.
prod-db-migrate:
	@echo "▶ Applying migrations on $(DEPLOY_HOST)…"
	@$(SSH) "cd $(REMOTE_DIR) && sudo bash -c 'set -a && . ./$(APP_ENV_FILE) && set +a && ./$(BINARY) -migrate'"
	@echo "✓ remote database is up to date"

# One-time (idempotent) import of the legacy embedded JSON export into production. Only
# needed once, when first cutting the app over from the embedded JSON to Postgres.
prod-db-import:
	@echo "▶ Importing companies on $(DEPLOY_HOST)…"
	@$(SSH) "cd $(REMOTE_DIR) && sudo bash -c 'set -a && . ./$(APP_ENV_FILE) && set +a && ./$(BINARY) -import'"
	@echo "✓ import complete"

# One-time (idempotent) import of the legacy ads.yml campaigns into production —
# the ads CMS cutover step. Settings row is seeded only (never clobbered).
prod-db-import-ads:
	@echo "▶ Importing ad campaigns on $(DEPLOY_HOST)…"
	@$(SSH) "cd $(REMOTE_DIR) && sudo bash -c 'set -a && . ./$(APP_ENV_FILE) && set +a && ./$(BINARY) -import-ads'"
	@echo "✓ import complete"

# ──────────────────────────────────────────────────────────────────────────────
# Database backups (production — systemd timer, runs as the postgres OS user)
# ──────────────────────────────────────────────────────────────────────────────

# First-time (and idempotent re-run) install: script + systemd unit + timer, enabled.
prod-backup-install:
	@echo "▶ Installing database backup script + timer on $(DEPLOY_HOST)…"
	@$(SSH) "sudo mkdir -p $(REMOTE_DIR)/scripts $(BACKUP_DIR) && sudo chown postgres:postgres $(BACKUP_DIR)"
	@$(RSYNC) scripts/db-backup.sh scripts/db-backup-offsite.sh $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/
	@$(SSH) "sudo mv /tmp/db-backup.sh /tmp/db-backup-offsite.sh $(REMOTE_DIR)/scripts/ && sudo chmod +x $(REMOTE_DIR)/scripts/db-backup.sh $(REMOTE_DIR)/scripts/db-backup-offsite.sh"
	@$(RSYNC) deploy/systemd/congopro-bridge-db-backup.service $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/congopro-bridge-db-backup.service
	@$(RSYNC) deploy/systemd/congopro-bridge-db-backup.timer $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/congopro-bridge-db-backup.timer
	@$(SSH) "sudo mv /tmp/congopro-bridge-db-backup.service /tmp/congopro-bridge-db-backup.timer /etc/systemd/system/ && sudo systemctl daemon-reload"
	@$(SSH) "sudo systemctl enable --now congopro-bridge-db-backup.timer"
	@echo "✓ backup timer installed — next run: $$($(SSH) 'systemctl show congopro-bridge-db-backup.timer -p NextElapseUSecRealtime --value')"

# Triggers an out-of-schedule backup run (the timer keeps its normal schedule).
prod-backup-now:
	@echo "▶ Running an ad-hoc backup on $(DEPLOY_HOST)…"
	@$(SSH) "sudo systemctl start congopro-bridge-db-backup.service"
	@$(MAKE) prod-backup-status

prod-backup-status:
	@$(SSH) "sudo systemctl status congopro-bridge-db-backup.timer --no-pager -l || true"
	@$(SSH) "echo 'local last-success:   '\$$(cat $(BACKUP_DIR)/last-success 2>/dev/null || echo '(none)'); \
	         echo 'offsite last-success: '\$$(cat $(BACKUP_DIR)/offsite-last-success 2>/dev/null || echo '(not configured)')"
	@$(MAKE) prod-backup-list

prod-backup-logs:
	$(SSH) "sudo journalctl -u congopro-bridge-db-backup.service -f --no-pager"

prod-backup-list:
	@$(SSH) "ls -lht $(BACKUP_DIR) 2>/dev/null || echo '(no backups yet)'"

# ── Offsite backups (Cloudflare R2 via rclone) ──
# The bucket and its bucket-scoped token are created by hand in the
# Cloudflare dashboard (R2 → bucket → Manage API Tokens); everything after
# that is these targets. The backup unit runs as postgres, so both config
# files live under $(REMOTE_DIR), postgres-owned 0600 — never in git.

# Interactive: writes backup-offsite.env + backup-offsite.rclone.conf on the
# server and runs a real write/read/delete round trip against the bucket.
# The token secret is read with echo off and travels over stdin (never argv,
# never shell history). Paste the ENDPOINT exactly as the R2 token screen
# shows it — that sidesteps the jurisdiction trap (.eu. buckets 403 through
# the default endpoint, which looks exactly like a bad token). The round trip
# uses copyto of a temp file, not rcat: rcat streams without Content-Length
# and R2 answers 501 NotImplemented, which reads like broken credentials.
prod-backup-offsite-configure:
	@printf "R2 access key id: "; IFS= read -r AKID; \
	printf "R2 secret access key (hidden): "; stty -echo; IFS= read -r AKSECRET; stty echo; echo; \
	printf "R2 endpoint (https://<accountid>[.<jurisdiction>].r2.cloudflarestorage.com): "; IFS= read -r ENDPOINT; \
	printf "Bucket name: "; IFS= read -r BUCKET; \
	case "$$ENDPOINT" in https://*) ;; *) echo "✗ endpoint must start with https:// (rclone convention)" >&2; exit 1;; esac; \
	echo "$$BUCKET" | grep -Eq '^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$$' || { \
	  echo "✗ '$$BUCKET' is not a valid R2 bucket name (lowercase letters, digits, hyphens)." >&2; \
	  echo "  Use the bucket's NAME from the R2 bucket list — not the token label or a display name." >&2; exit 1; }; \
	printf '%s\n' "$$AKSECRET" | $(SSH) "sudo tee /tmp/.r2secret >/dev/null && sudo chmod 600 /tmp/.r2secret"; \
	$(SSH) "sudo bash -c 'umask 077; \
	  { echo \"[r2congopro]\"; echo \"type = s3\"; echo \"provider = Cloudflare\"; \
	    echo \"access_key_id = $$AKID\"; echo \"secret_access_key = \$$(cat /tmp/.r2secret)\"; \
	    echo \"endpoint = $$ENDPOINT\"; echo \"region = auto\"; echo \"acl = private\"; \
	    echo \"no_check_bucket = true\"; } > $(REMOTE_DIR)/backup-offsite.rclone.conf; \
	  rm -f /tmp/.r2secret; \
	  { echo \"OFFSITE_MODE=s3\"; echo \"OFFSITE_RCLONE_REMOTE=r2congopro:$$BUCKET/\"; \
	    echo \"OFFSITE_RETENTION_DAYS=90\"; } > $(REMOTE_DIR)/backup-offsite.env; \
	  chown postgres:postgres $(REMOTE_DIR)/backup-offsite.rclone.conf $(REMOTE_DIR)/backup-offsite.env; \
	  chmod 600 $(REMOTE_DIR)/backup-offsite.rclone.conf $(REMOTE_DIR)/backup-offsite.env'"; \
	echo "▶ verifying with a write/read/delete round trip (as postgres)…"; \
	$(SSH) "sudo -u postgres bash -c 'set -e; \
	  R=\"r2congopro:$$BUCKET/\"; C=$(REMOTE_DIR)/backup-offsite.rclone.conf; \
	  T=\$$(mktemp); echo ok > \"\$$T\"; \
	  rclone --config \"\$$C\" copyto \"\$$T\" \"\$${R}.write-test\"; \
	  rclone --config \"\$$C\" cat \"\$${R}.write-test\" >/dev/null; \
	  rclone --config \"\$$C\" deletefile \"\$${R}.write-test\"; \
	  rm -f \"\$$T\"'" \
	  && echo "✓ offsite configured and verified — next timer run will push; or: make prod-backup-now"

# Lists the dumps currently in the bucket (newest last) + the success marker.
prod-backup-offsite-status:
	@$(SSH) "echo 'offsite last-success: '\$$(cat $(BACKUP_DIR)/offsite-last-success 2>/dev/null || echo '(never)')"
	@$(SSH) "sudo -u postgres bash -c 'test -f $(REMOTE_DIR)/backup-offsite.env || { echo \"(offsite not configured — run: make prod-backup-offsite-configure)\"; exit 0; }; . $(REMOTE_DIR)/backup-offsite.env && rclone --config $(REMOTE_DIR)/backup-offsite.rclone.conf lsl \"\$$OFFSITE_RCLONE_REMOTE\" --include \"*.dump\" | sort -k2,3 | tail -8'"

# Fetches a dump FROM THE BUCKET (not the local dir) into ./backups/offsite/
# — the honest input for an offsite restore test. Newest by default; pass
# BACKUP_NAME=congopro_bridge-….dump (see prod-backup-offsite-status) for a specific
# one, which is how you reach anything older than the local 14-day window.
prod-backup-offsite-pull:
	@mkdir -p $(LOCAL_BACKUP_DIR)/offsite
	@echo "▶ fetching offsite dump from R2…"
	@$(SSH) "sudo -u postgres bash -c '. $(REMOTE_DIR)/backup-offsite.env; \
	  C=$(REMOTE_DIR)/backup-offsite.rclone.conf; N=\"$(BACKUP_NAME)\"; \
	  if [ -z \"\$$N\" ]; then N=\$$(rclone --config \$$C lsl \"\$$OFFSITE_RCLONE_REMOTE\" --include \"*.dump\" | sort -k2,3 | tail -1 | awk \"{print \\\$$NF}\"); fi; \
	  [ -n \"\$$N\" ] || { echo \"✗ bucket holds no dumps yet\" >&2; exit 1; }; \
	  echo \"  fetching: \$$N\"; \
	  rclone --config \$$C copyto \"\$$OFFSITE_RCLONE_REMOTE\$$N\" /tmp/offsite-verify.dump; \
	  chmod 644 /tmp/offsite-verify.dump'"
	@rsync -az --progress -e "ssh $(_ssh_opts)" $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/offsite-verify.dump $(LOCAL_BACKUP_DIR)/offsite/
	@$(SSH) "sudo rm -f /tmp/offsite-verify.dump"
	@echo "✓ pulled to $(LOCAL_BACKUP_DIR)/offsite/offsite-verify.dump"

# Proves the OFFSITE copy restores: R2 → local throwaway Postgres → verify.
# An untested backup isn't a backup, and an untested offsite backup doubly so.
dev-db-restore-test-offsite: prod-backup-offsite-pull dev-db-up
	@bash scripts/db-restore-test.sh "$(LOCAL_BACKUP_DIR)/offsite/offsite-verify.dump"

# Downloads every backup currently on the server into LOCAL_BACKUP_DIR (no --delete,
# so older backups you've already pulled and the server has since rotated away stay put).
prod-backup-pull:
	@mkdir -p $(LOCAL_BACKUP_DIR)
	@echo "▶ Pulling backups from $(DEPLOY_HOST):$(BACKUP_DIR) → $(LOCAL_BACKUP_DIR)/…"
	@rsync -az --progress -e "ssh $(_ssh_opts)" $(DEPLOY_USER)@$(DEPLOY_HOST):$(BACKUP_DIR)/ $(LOCAL_BACKUP_DIR)/
	@echo "✓ backups pulled to $(LOCAL_BACKUP_DIR)/"

# Restores a dump into a throwaway database on the LOCAL dev Postgres and verifies it —
# proves a backup is actually restorable without going near production. Defaults to the
# newest file in LOCAL_BACKUP_DIR; pass BACKUP_FILE=path/to/x.dump to test a specific one.
dev-db-restore-test: dev-db-up
	@FILE="$(BACKUP_FILE)"; \
	if [ -z "$$FILE" ]; then \
	  FILE=$$(ls -t $(LOCAL_BACKUP_DIR)/*.dump 2>/dev/null | head -1); \
	fi; \
	if [ -z "$$FILE" ]; then \
	  echo "❌ no dump file found — run 'make prod-backup-pull' first or pass BACKUP_FILE=..."; \
	  exit 1; \
	fi; \
	echo "▶ testing restore of $$FILE against local dev Postgres…"; \
	bash scripts/db-restore-test.sh "$$FILE"

# DESTRUCTIVE: restores production straight FROM R2, without the dump ever
# touching your laptop. This is the path when the VPS is alive but the dump you
# need is older than the local 14-day rotation (offsite keeps 90 days), and the
# path on a rebuilt server, where /opt/congopro-bridge/backups is empty.
# Newest by default; BACKUP_NAME=… picks one (prod-backup-offsite-status lists them).
# Same guard rails as prod-db-restore: the app is stopped, a typed confirmation
# is required, and the service restarts either way. Verify the same dump with
# `dev-db-restore-test-offsite BACKUP_NAME=…` first.
prod-db-restore-offsite:
	@echo "▶ fetching dump from R2 onto $(DEPLOY_HOST) (never via this machine)…"
	@$(SSH) "sudo -u postgres bash -c 'set -e; . $(REMOTE_DIR)/backup-offsite.env; \
	  C=$(REMOTE_DIR)/backup-offsite.rclone.conf; N=\"$(BACKUP_NAME)\"; \
	  if [ -z \"\$$N\" ]; then N=\$$(rclone --config \$$C lsl \"\$$OFFSITE_RCLONE_REMOTE\" --include \"*.dump\" | sort -k2,3 | tail -1 | awk \"{print \\\$$NF}\"); fi; \
	  [ -n \"\$$N\" ] || { echo \"✗ bucket holds no dumps\" >&2; exit 1; }; \
	  echo \"  restoring from: \$$N\"; \
	  rclone --config \$$C copyto \"\$$OFFSITE_RCLONE_REMOTE\$$N\" /tmp/restore.dump; \
	  chmod 644 /tmp/restore.dump'"
	@$(RSYNC) scripts/db-restore-prod.sh $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/db-restore-prod.sh
	@ssh -t $(_ssh_opts) $(DEPLOY_USER)@$(DEPLOY_HOST) "chmod +x /tmp/db-restore-prod.sh && sudo /tmp/db-restore-prod.sh '$(DB_NAME)' /tmp/restore.dump '$(SERVICE)'; rm -f /tmp/db-restore-prod.sh /tmp/restore.dump"

# DESTRUCTIVE: overwrites the live production database. Requires dev-db-restore-test to have
# been run first, and a typed confirmation on the server (ssh -t for the interactive prompt).
# Defaults to the newest file in LOCAL_BACKUP_DIR; pass BACKUP_FILE=path/to/x.dump to pick one.
prod-db-restore:
	@FILE="$(BACKUP_FILE)"; \
	if [ -z "$$FILE" ]; then \
	  FILE=$$(ls -t $(LOCAL_BACKUP_DIR)/*.dump 2>/dev/null | head -1); \
	fi; \
	if [ -z "$$FILE" ]; then \
	  echo "❌ no dump file found — pass BACKUP_FILE=path/to/x.dump"; \
	  exit 1; \
	fi; \
	echo "▶ uploading $$FILE → $(DEPLOY_HOST):/tmp/restore.dump…"; \
	rsync -az --progress -e "ssh $(_ssh_opts)" "$$FILE" $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/restore.dump; \
	$(RSYNC) scripts/db-restore-prod.sh $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/db-restore-prod.sh; \
	ssh -t $(_ssh_opts) $(DEPLOY_USER)@$(DEPLOY_HOST) "chmod +x /tmp/db-restore-prod.sh && sudo /tmp/db-restore-prod.sh '$(DB_NAME)' /tmp/restore.dump '$(SERVICE)'; rm -f /tmp/db-restore-prod.sh /tmp/restore.dump"

# ──────────────────────────────────────────────────────────────────────────────

help:
	@echo ""
	@echo "  Congopro Bridge — make targets"
	@echo "  Naming: <where>-<what>-<verb>.  dev-* = this machine · prod-* = live server (SSH)"
	@echo "  ─────────────────────────────────────────────────────────────────────────────"
	@echo "  EVERYDAY"
	@echo "    dev                      Deps + migrations + templ/CSS watch + hot-reload app"
	@echo "    test                     Unit tests (race)          vet   go vet ./..."
	@echo "    build                    Release binary (static, linux/amd64)"
	@echo "    build-quick              Fast native build, no codegen"
	@echo "    css / templ              Regenerate stylesheet / *_templ.go"
	@echo ""
	@echo "  DEV — local machine"
	@echo "    dev-deps-up/-down        Start/stop postgres+meilisearch+ollama (volumes kept)"
	@echo "    dev-deps-reset           ⚠ DESTROYS local postgres+meili data (ollama models kept)"
	@echo "    dev-db-up/-down          Start/stop local Postgres only"
	@echo "    dev-db-migrate           Apply migrations locally"
	@echo "    dev-db-reset             ⚠ DESTROYS the local database, then re-migrates (empty schema)"
	@echo "    dev-db-scrub             ⚠ DELETES customers/claims/promotions (keeps catalogue + admin)"
	@echo "    dev-db-psql              psql shell on the local database"
	@echo "    dev-db-import            Load the embedded JSON export into local Postgres"
	@echo "    dev-db-import-ads        Load the legacy ads.yml campaigns locally"
	@echo "    dev-db-restore-test      Restore a backup into a throwaway local DB and verify it"
	@echo "    dev-db-restore-test-offsite    Same, but fetched from R2 — proves the offsite copy restores"
	@echo "    dev-search-reset         ⚠ DESTROYS the local Meilisearch index (rebuilds on next boot)"
	@echo "    dev-admin-create         Interactively create a staff account (super_admin)"
	@echo "    dev-test-integration     Integration-tagged tests against local Postgres"
	@echo "    dev-mail-up/-down        Mailpit local email capture (UI http://localhost:8025)"
	@echo "    dev-mail-test TO=…       Send one real test email through the .env SMTP account"
	@echo "    dev-stack-up/-down       Whole stack in docker, app container included"
	@echo "    dev-stack-reset          ⚠ DESTROYS the local Meilisearch volume"
	@echo "    dev-stack-logs[-app]     Follow compose logs"
	@echo ""
	@echo "  PROD — live server over SSH"
	@echo "    prod-ping / prod-ssh     Check connectivity / open a shell"
	@echo "    prod-deploy              Build, upload binary, push config, restart"
	@echo "    prod-app-push            Upload the binary only"
	@echo "    prod-config-push         Upload Traefik dynamic config + reload"
	@echo "    prod-app-start/-stop/-restart/-status/-logs"
	@echo "    prod-secrets-init        Generate missing secrets on the server (idempotent)"
	@echo "    prod-secrets-set KEY=…   Set one secret (hidden input, never echoed/logged)"
	@echo "    prod-secrets-list        List which keys the server defines (names only)"
	@echo "    prod-bootstrap-app       First-time app install (unit + secrets + deploy)"
	@echo "    prod-bootstrap-all       Fresh server: Ollama + Meilisearch + app"
	@echo "    prod-db-install          Install PostgreSQL + PostGIS (idempotent)"
	@echo "    prod-db-provision        Create app role/database from $(APP_ENV_FILE)"
	@echo "    prod-db-migrate          Apply migrations on the server"
	@echo "    prod-db-import[-ads]     One-time cutover imports"
	@echo "    prod-db-status/-check    systemctl status / PostGIS + tables"
	@echo "    prod-db-restore          ⚠ DESTROYS production data (typed confirmation required)"
	@echo "    prod-search-setup        First-time remote Meilisearch install"
	@echo "    prod-search-start/-stop/-restart/-status/-logs"
	@echo "    prod-search-reset        ⚠ DESTROYS the remote index (rebuilds on next start)"
	@echo "    prod-ollama-setup        Install + CPU limit + pull models"
	@echo "    prod-ollama-reset        ⚠ Removes all remote models, then re-pulls"
	@echo "    prod-ollama-status/-logs"
	@echo "    prod-traefik-reload/-logs"
	@echo "    prod-backup-install      Install backup script + daily systemd timer"
	@echo "    prod-backup-now/-status/-logs/-list"
	@echo "    prod-backup-pull         Download backups to ./backups/"
	@echo "    prod-backup-offsite-configure  Set up the R2 push (interactive, verified round trip)"
	@echo "    prod-backup-offsite-status     Offsite marker + dumps currently in the bucket"
	@echo "    prod-backup-offsite-pull [BACKUP_NAME=…]  Fetch a dump FROM the bucket to ./backups/offsite/"
	@echo "    prod-db-restore-offsite [BACKUP_NAME=…]   ⚠ DESTROYS production, restoring straight from R2"
	@echo ""
	@echo "  IMAGE — container build (local)"
	@echo "    image-build / image-save / image-run"
	@echo "  ─────────────────────────────────────────────────────────────────────────────"
	@echo "  Variables (.env or env overrides):"
	@echo "    DEPLOY_HOST DEPLOY_USER DEPLOY_PORT SSH_KEY REMOTE_DIR DOMAIN"
	@echo "    MEILI_DIR MEILI_VERSION POSTGRES_PORT DB_NAME DB_USER PG_VERSION"
	@echo "    BACKUP_DIR BACKUP_KEEP LOCAL_BACKUP_DIR BACKUP_FILE BACKUP_NAME"
	@echo ""
