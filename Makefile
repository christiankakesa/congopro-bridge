-include .env
export

DEPLOY_USER  ?= ops
DEPLOY_HOST  ?= xxx.xxx.xxx.xxx
DEPLOY_PORT  ?= 4242
SSH_KEY      ?= $(HOME)/.ssh/id_ed25519
REMOTE_DIR   ?= /opt/congopro-bridge
MEILI_DIR    ?= /opt/meilisearch
MEILI_VERSION ?= v1.43.1
IMAGE        ?= congopro-bridge
TAG          ?= latest
DOMAIN       ?= congopro.com
CMD_PATH     := ./cmd/congopro-bridge
BINARY       := congopro-bridge
BUILD_DIR    := ./build
GENERATIVE_MODEL ?= gemma3:1b
EMBEDDING_MODEL ?= nomic-embed-text
SERVICE      := congopro-bridge
TAILWIND_CLI := $(shell which tailwindcss)
TEMPL_CLI    := $(shell which templ)

# Database — local dev (docker compose, see db-* targets below)
POSTGRES_PORT     ?= 5433
DB_NAME           ?= congopro_bridge
DB_USER           ?= congopro_bridge
PG_VERSION        ?= 16
LOCAL_DATABASE_URL ?= postgres://congopro_bridge:congopro_bridge@localhost:$(POSTGRES_PORT)/congopro_bridge?sslmode=disable

# Database — backups (production)
BACKUP_DIR       ?= /opt/congopro-bridge/backups
BACKUP_KEEP      ?= 14
LOCAL_BACKUP_DIR ?= ./backups
BACKUP_FILE      ?=

_ssh_opts    := -p $(DEPLOY_PORT) -i $(SSH_KEY) \
                -o StrictHostKeyChecking=accept-new \
                -o ConnectTimeout=10
SSH          := ssh $(_ssh_opts) $(DEPLOY_USER)@$(DEPLOY_HOST)
RSYNC        := rsync -az --progress --delete \
                -e "ssh $(_ssh_opts)"

.PHONY: all build build-local clean test templ \
        docker-build docker-push docker-save docker-run docker-up docker-down docker-down-v meili-reset \
        deploy deploy-binary deploy-config deploy-service deploy-full deploy-all secrets-init \
        service-start service-stop service-restart service-status service-logs \
        traefik-reload traefik-logs \
        ollama-install ollama-configure-limit ollama-pull-models ollama-clean-models ollama-reset ollama-status ollama-setup ollama-logs \
        meili-install meili-deploy-config meili-deploy-service meili-deploy-traefik meili-setup meili-start meili-stop meili-restart meili-status meili-logs meili-index-reset \
        db-up db-down db-migrate db-import create-admin test-integration dev dev-down \
        db-install db-provision db-remote-status db-remote-check db-migrate-remote db-import-remote \
        db-backup-install db-backup-now db-backup-status db-backup-logs db-backup-list db-backup-pull \
        db-restore-test db-restore \
        ssh ping help

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

build-local:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY) $(CMD_PATH)

clean:
	@rm -rf $(BUILD_DIR)
	@echo "✓ clean"

test:
	go test ./... -v -race -timeout 60s

docker-build:
	@echo "▶ docker build $(IMAGE):$(TAG)…"
	docker build \
	  --build-arg VERSION=$(shell git describe --tags --always 2>/dev/null || echo dev) \
	  -t $(IMAGE):$(TAG) \
	  .
	@echo "✓ $(IMAGE):$(TAG)"

docker-save: docker-build
	@mkdir -p $(BUILD_DIR)
	docker save $(IMAGE):$(TAG) | gzip > $(BUILD_DIR)/$(IMAGE)-$(TAG).tar.gz
	@echo "✓ $(BUILD_DIR)/$(IMAGE)-$(TAG).tar.gz"

docker-run: docker-build
	docker run -p 8080:8080 $(IMAGE):$(TAG)

docker-up: css
	@echo "▶ Starting services…"
	docker compose up -d --build
	@echo "✓ Services running"

docker-down:
	@echo "▶ Stopping services…"
	docker compose down
	@echo "✓ Services stopped"

docker-down-v:
	@echo "▶ Stopping services (keeping ollama_data volume)…"
	docker compose down
	docker volume rm congopro-bridge_meili_data 2>/dev/null || true
	@echo "✓ Services stopped, meili_data removed, ollama_data preserved"

docker-logs:
	@echo "▶ Starting docker logs…"
	docker compose logs -f

docker-logs-app:
	@echo "▶ Starting docker logs…"
	docker compose logs -f app

meili-reset:
	@echo "▶ Resetting Meilisearch index (keeping Ollama models)…"
	docker compose rm -sf meilisearch
	docker volume rm congopro-bridge_meili_data
	docker compose up -d meilisearch
	@echo "✓ Meilisearch volume wiped and restarted — app will re-index on next boot"

# ──────────────────────────────────────────────────────────────────────────────
# Database (local dev — docker compose)
# ──────────────────────────────────────────────────────────────────────────────

db-up:
	@echo "▶ Starting local Postgres…"
	docker compose up -d --wait postgres
	@echo "✓ Postgres ready on 127.0.0.1:$(POSTGRES_PORT)"

db-down:
	@echo "▶ Stopping local Postgres…"
	docker compose stop postgres
	@echo "✓ Postgres stopped (data volume kept — use docker-down-v-style removal to wipe it)"

db-migrate: db-up
	@echo "▶ Applying migrations to local Postgres…"
	DATABASE_URL="$(LOCAL_DATABASE_URL)" go run $(CMD_PATH) -migrate

# One-time (idempotent) import of the legacy embedded JSON export into local Postgres.
db-import: db-migrate
	@echo "▶ Importing companies into local Postgres…"
	DATABASE_URL="$(LOCAL_DATABASE_URL)" go run $(CMD_PATH) -import

# Interactive: creates a staff account (super_admin) against local Postgres.
# Prints a TOTP enrollment secret/URI you'll need to log in — see -create-admin.
create-admin: db-up
	DATABASE_URL="$(LOCAL_DATABASE_URL)" go run $(CMD_PATH) -create-admin

# Integration tests are gated behind the "integration" build tag so `make test`
# stays fast and DB-free. Add tests with `//go:build integration` as the
# schema grows; this target is a no-op (passes trivially) until then.
test-integration: db-migrate
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

# Stops only the dev deps started by `make dev` (data volumes are kept).
dev-down:
	@echo "▶ Stopping dev deps…"
	@docker compose stop postgres meilisearch ollama ollama-init
	@echo "✓ dev deps stopped"

ping:
	@echo "▶ pinging $(DEPLOY_USER)@$(DEPLOY_HOST):$(DEPLOY_PORT)…"
	@$(SSH) "echo '✓ connected as $(DEPLOY_USER) on $(DEPLOY_HOST)'"

ssh:
	ssh $(_ssh_opts) $(DEPLOY_USER)@$(DEPLOY_HOST)

# ──────────────────────────────────────────────────────────────────────────────
# App deployment
# ──────────────────────────────────────────────────────────────────────────────

deploy: deploy-binary deploy-config service-restart
	@echo ""
	@echo "╔══════════════════════════════════════╗"
	@echo "║  ✓ Deployment complete               ║"
	@echo "║  https://$(DOMAIN)                ║"
	@echo "╚══════════════════════════════════════╝"

deploy-binary: build
	@echo "▶ Uploading binary → $(DEPLOY_HOST):$(REMOTE_DIR)/"
	@$(SSH) "sudo mkdir -p $(REMOTE_DIR) && sudo chown $(DEPLOY_USER): $(REMOTE_DIR)"
	@$(RSYNC) $(BUILD_DIR)/$(BINARY) $(DEPLOY_USER)@$(DEPLOY_HOST):$(REMOTE_DIR)/$(BINARY)
	@$(SSH) "chmod +x $(REMOTE_DIR)/$(BINARY)"
	@echo "✓ binary uploaded"

deploy-config:
	@echo "▶ Uploading Traefik dynamic config…"
	@$(SSH) "sudo mkdir -p /srv/traefik/dynamic"
	@$(RSYNC) deploy/traefik/dynamic/ $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/traefik-dynamic/
	@$(SSH) "sudo cp -r /tmp/traefik-dynamic/. /srv/traefik/dynamic/ && rm -rf /tmp/traefik-dynamic"
	@echo "✓ config uploaded"
	@$(MAKE) traefik-reload

deploy-service:
	@echo "▶ Installing $(SERVICE) systemd unit…"
	@$(RSYNC) deploy/systemd/$(SERVICE).service $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/$(SERVICE).service
	@$(SSH) "sudo mv /tmp/$(SERVICE).service /etc/systemd/system/$(SERVICE).service && sudo systemctl daemon-reload"
	@echo "✓ unit installed — run 'make service-start' to enable"

# First-time app setup: installs systemd unit, deploys binary, enables on boot.
deploy-full: deploy-service secrets-init deploy
	@$(SSH) "sudo systemctl enable $(SERVICE)"
	@echo "✓ $(SERVICE) enabled on boot"

# Generates secrets on the server (never downloaded, never printed) and writes them to
# EnvironmentFile(s). Idempotent per-key: only fills in whatever is missing, so re-running
# after adding a new secret (e.g. DATABASE_URL) doesn't touch keys already in place.
secrets-init:
	@echo "▶ Ensuring secrets exist on $(DEPLOY_HOST)…"
	@$(SSH) "sudo mkdir -p $(REMOTE_DIR) $(MEILI_DIR)/etc; \
	  sudo touch $(REMOTE_DIR)/secrets.env $(MEILI_DIR)/etc/secrets.env; \
	  if ! sudo grep -q '^MEILI_MASTER_KEY=' $(REMOTE_DIR)/secrets.env; then \
	    KEY=\$$(openssl rand -base64 48); \
	    echo \"MEILI_MASTER_KEY=\$$KEY\" | sudo tee -a $(REMOTE_DIR)/secrets.env >/dev/null; \
	    echo \"MEILI_MASTER_KEY=\$$KEY\" | sudo tee -a $(MEILI_DIR)/etc/secrets.env >/dev/null; \
	    echo '✓ generated MEILI_MASTER_KEY'; \
	  else \
	    echo '✓ MEILI_MASTER_KEY already present'; \
	  fi; \
	  if ! sudo grep -q '^DATABASE_URL=' $(REMOTE_DIR)/secrets.env; then \
	    DBPASS=\$$(openssl rand -base64 32 | tr -dc 'a-zA-Z0-9'); \
	    echo \"DATABASE_URL=postgres://$(DB_USER):\$$DBPASS@localhost:5432/$(DB_NAME)?sslmode=disable\" | sudo tee -a $(REMOTE_DIR)/secrets.env >/dev/null; \
	    echo '✓ generated DATABASE_URL (Postgres password)'; \
	  else \
	    echo '✓ DATABASE_URL already present'; \
	  fi; \
	  sudo chown root:root $(REMOTE_DIR)/secrets.env $(MEILI_DIR)/etc/secrets.env; \
	  sudo chmod 600 $(REMOTE_DIR)/secrets.env $(MEILI_DIR)/etc/secrets.env"

# Full server bootstrap: Ollama + Meilisearch + app. Run once on a fresh server.
deploy-all: ollama-setup meili-setup deploy-full
	@echo ""
	@echo "╔══════════════════════════════════════╗"
	@echo "║  ✓ Full stack ready                  ║"
	@echo "║  https://$(DOMAIN)                ║"
	@echo "╚══════════════════════════════════════╝"

# ──────────────────────────────────────────────────────────────────────────────
# App service
# ──────────────────────────────────────────────────────────────────────────────

service-start:
	@$(SSH) "sudo systemctl enable --now $(SERVICE)"
	@echo "✓ $(SERVICE) started"

service-stop:
	@$(SSH) "sudo systemctl stop $(SERVICE)"
	@echo "✓ $(SERVICE) stopped"

service-restart:
	@echo "▶ Restarting $(SERVICE)…"
	@$(SSH) "sudo systemctl restart $(SERVICE)"
	@sleep 2
	@$(MAKE) service-status

service-status:
	@$(SSH) "sudo systemctl status $(SERVICE) --no-pager -l || true"

service-logs:
	$(SSH) "sudo journalctl -u $(SERVICE) -f --no-pager"

# ──────────────────────────────────────────────────────────────────────────────
# Traefik
# ──────────────────────────────────────────────────────────────────────────────

traefik-reload:
	@echo "▶ Triggering Traefik dynamic config reload…"
	@$(SSH) "sudo touch /srv/traefik/dynamic/congopro-bridge.yml"
	@echo "✓ Traefik will pick up changes within a few seconds"

traefik-logs:
	$(SSH) "sudo journalctl -u traefik -f --no-pager 2>/dev/null || sudo docker logs -f $$(sudo docker ps -qf name=traefik)"

# ──────────────────────────────────────────────────────────────────────────────
# Ollama
# ──────────────────────────────────────────────────────────────────────────────

OLLAMA_MODELS ?= $(GENERATIVE_MODEL) $(EMBEDDING_MODEL)
OLLAMA_NUM_THREADS ?= 2

ollama-install:
	@echo "▶ Installing Ollama on $(DEPLOY_HOST)…"
	@$(SSH) "curl -fsSL https://ollama.com/install.sh | sh"
	@$(SSH) "sudo systemctl enable --now ollama"
	@echo "✓ Ollama installed and started"

ollama-configure-limit:
	@echo "▶ Limiting Ollama to $(OLLAMA_NUM_THREADS) CPU threads…"
	@$(SSH) "sudo mkdir -p /etc/systemd/system/ollama.service.d && \
	         echo '[Service]' | sudo tee /etc/systemd/system/ollama.service.d/override.conf >/dev/null && \
	         echo 'Environment=\"OLLAMA_NUM_THREADS=$(OLLAMA_NUM_THREADS)\"' | sudo tee -a /etc/systemd/system/ollama.service.d/override.conf >/dev/null && \
	         sudo systemctl daemon-reload && \
	         sudo systemctl restart ollama"
	@echo "✓ Ollama CPU limit applied"

ollama-pull-models:
	@echo "▶ Pulling models: $(OLLAMA_MODELS)…"
	@$(SSH) "for model in $(OLLAMA_MODELS); do echo \"Pulling \$$model...\"; ollama pull \$$model; done"
	@echo "✓ All models pulled"

ollama-clean-models:
	@echo "▶ Removing all Ollama models on $(DEPLOY_HOST)…"
	@$(SSH) "ollama list | tail -n +2 | awk '{print \$$1}' | xargs -I {} ollama rm {}"
	@echo "✓ All models removed"

ollama-reset: ollama-clean-models ollama-pull-models
	@echo "✓ Models reset to: $(OLLAMA_MODELS)"

ollama-status:
	@$(SSH) "sudo systemctl status ollama --no-pager -l || true"
	@$(SSH) "ollama list"

ollama-setup: ollama-install ollama-configure-limit ollama-pull-models
	@echo "╔═════════════════════════════════════════════════════════════════════════════╗"
	@echo "║  Ollama is ready with $(OLLAMA_MODELS)                            ║"
	@echo "╚═════════════════════════════════════════════════════════════════════════════╝"

ollama-logs:
	$(SSH) "sudo journalctl -u ollama -f --no-pager"

# ──────────────────────────────────────────────────────────────────────────────
# Meilisearch (production — systemd)
# ──────────────────────────────────────────────────────────────────────────────

meili-install:
	@echo "▶ Installing Meilisearch $(MEILI_VERSION) on $(DEPLOY_HOST)…"
	@$(SSH) "sudo useradd -r -s /bin/false meilisearch 2>/dev/null || true"
	@$(SSH) "sudo mkdir -p $(MEILI_DIR)/bin $(MEILI_DIR)/data/db $(MEILI_DIR)/data/dumps $(MEILI_DIR)/etc"
	@$(SSH) "sudo chown -R meilisearch:meilisearch $(MEILI_DIR)"
	@$(SSH) "curl -L https://github.com/meilisearch/meilisearch/releases/download/$(MEILI_VERSION)/meilisearch-linux-amd64 -o /tmp/meilisearch && sudo mv /tmp/meilisearch $(MEILI_DIR)/bin/meilisearch"
	@$(SSH) "sudo chmod +x $(MEILI_DIR)/bin/meilisearch"
	@echo "✓ Meilisearch $(MEILI_VERSION) installed at $(MEILI_DIR)/bin/meilisearch"

meili-deploy-config:
	@echo "▶ Uploading meilisearch.toml…"
	@$(RSYNC) deploy/meilisearch/meilisearch.toml $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/meilisearch.toml
	@$(SSH) "sudo mv /tmp/meilisearch.toml $(MEILI_DIR)/etc/meilisearch.toml && sudo chown meilisearch:meilisearch $(MEILI_DIR)/etc/meilisearch.toml"
	@echo "✓ meilisearch.toml deployed"

meili-deploy-service:
	@echo "▶ Installing meilisearch systemd unit…"
	@$(RSYNC) deploy/meilisearch/meilisearch.service $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/meilisearch.service
	@$(SSH) "sudo mv /tmp/meilisearch.service /etc/systemd/system/meilisearch.service && sudo systemctl daemon-reload"
	@echo "✓ systemd unit installed"

meili-deploy-traefik:
	@echo "▶ Uploading Meilisearch Traefik config…"
	@$(RSYNC) deploy/meilisearch/meilisearch.yml $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/meilisearch.yml
	@$(SSH) "sudo mkdir -p /srv/traefik/dynamic && sudo mv /tmp/meilisearch.yml /srv/traefik/dynamic/meilisearch.yml"
	@$(MAKE) traefik-reload
	@echo "✓ Traefik config deployed"

# First-time Meilisearch setup: installs binary, config, systemd unit, Traefik, enables service.
meili-setup: meili-install meili-deploy-config meili-deploy-service meili-deploy-traefik secrets-init
	@$(SSH) "sudo systemctl enable --now meilisearch"
	@echo ""
	@echo "╔══════════════════════════════════════╗"
	@echo "║  ✓ Meilisearch ready                 ║"
	@echo "║  https://meili.$(DOMAIN)          ║"
	@echo "╚══════════════════════════════════════╝"

meili-start:
	@$(SSH) "sudo systemctl enable --now meilisearch"
	@echo "✓ meilisearch started"

meili-stop:
	@$(SSH) "sudo systemctl stop meilisearch"
	@echo "✓ meilisearch stopped"

meili-restart:
	@echo "▶ Restarting meilisearch…"
	@$(SSH) "sudo systemctl restart meilisearch"
	@sleep 2
	@$(MAKE) meili-status

meili-status:
	@$(SSH) "sudo systemctl status meilisearch --no-pager -l || true"

meili-logs:
	$(SSH) "sudo journalctl -u meilisearch -f --no-pager"

# Wipes the index on the remote server; app re-indexes automatically on next start.
meili-index-reset:
	@echo "▶ Wiping Meilisearch data on $(DEPLOY_HOST) (index will rebuild on next app start)…"
	@$(MAKE) meili-stop
	@$(SSH) "sudo rm -rf $(MEILI_DIR)/data/db && sudo mkdir -p $(MEILI_DIR)/data/db && sudo chown meilisearch:meilisearch $(MEILI_DIR)/data/db"
	@$(MAKE) meili-start
	@echo "✓ Meilisearch index wiped"

# ──────────────────────────────────────────────────────────────────────────────
# Database (production — self-hosted PostgreSQL via systemd, not docker)
# ──────────────────────────────────────────────────────────────────────────────

db-install:
	@echo "▶ Installing PostgreSQL $(PG_VERSION) + PostGIS on $(DEPLOY_HOST)…"
	@$(SSH) "MISSING=''; \
	  dpkg -s postgresql-$(PG_VERSION) >/dev/null 2>&1 || MISSING=\"\$$MISSING postgresql-$(PG_VERSION)\"; \
	  dpkg -s postgresql-$(PG_VERSION)-postgis-3 >/dev/null 2>&1 || MISSING=\"\$$MISSING postgresql-$(PG_VERSION)-postgis-3\"; \
	  if [ -n \"\$$MISSING\" ]; then sudo apt-get update -qq && sudo apt-get install -y \$$MISSING; else echo '✓ already installed'; fi"
	@$(SSH) "sudo systemctl enable --now postgresql"
	@echo "✓ PostgreSQL installed and running"

# Creates the app's role and database using the password secrets-init already generated
# into secrets.env. Requires sudo on the host (CREATE ROLE/DATABASE need postgres superuser) —
# gate scripts/db-provision.sh behind a dedicated sudoers entry rather than full root sudo.
db-provision: secrets-init
	@echo "▶ Provisioning database role and schema on $(DEPLOY_HOST)…"
	@$(RSYNC) scripts/db-provision.sh $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/db-provision.sh
	@$(SSH) "chmod +x /tmp/db-provision.sh && sudo /tmp/db-provision.sh '$(DB_NAME)' '$(DB_USER)' '$(REMOTE_DIR)/secrets.env' && rm -f /tmp/db-provision.sh"
	@echo "✓ database provisioned — run 'make db-migrate-remote' to apply schema"

db-remote-status:
	@$(SSH) "sudo systemctl status postgresql --no-pager -l || true"

db-remote-check:
	@echo "▶ Checking remote database and PostGIS…"
	@$(SSH) "sudo -u postgres psql -d $(DB_NAME) -c 'SELECT postgis_version();' -c '\dt'"

# Applies pending migrations using the already-deployed binary and the server's own
# secrets.env — no credentials ever leave the host.
db-migrate-remote:
	@echo "▶ Applying migrations on $(DEPLOY_HOST)…"
	@$(SSH) "cd $(REMOTE_DIR) && sudo bash -c 'set -a && . ./secrets.env && set +a && ./$(BINARY) -migrate'"
	@echo "✓ remote database is up to date"

# One-time (idempotent) import of the legacy embedded JSON export into production. Only
# needed once, when first cutting the app over from the embedded JSON to Postgres.
db-import-remote:
	@echo "▶ Importing companies on $(DEPLOY_HOST)…"
	@$(SSH) "cd $(REMOTE_DIR) && sudo bash -c 'set -a && . ./secrets.env && set +a && ./$(BINARY) -import'"
	@echo "✓ import complete"

# ──────────────────────────────────────────────────────────────────────────────
# Database backups (production — systemd timer, runs as the postgres OS user)
# ──────────────────────────────────────────────────────────────────────────────

# First-time (and idempotent re-run) install: script + systemd unit + timer, enabled.
db-backup-install:
	@echo "▶ Installing database backup script + timer on $(DEPLOY_HOST)…"
	@$(SSH) "sudo mkdir -p $(REMOTE_DIR)/scripts $(BACKUP_DIR) && sudo chown postgres:postgres $(BACKUP_DIR)"
	@$(RSYNC) scripts/db-backup.sh $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/db-backup.sh
	@$(SSH) "sudo mv /tmp/db-backup.sh $(REMOTE_DIR)/scripts/db-backup.sh && sudo chmod +x $(REMOTE_DIR)/scripts/db-backup.sh"
	@$(RSYNC) deploy/systemd/congopro-bridge-db-backup.service $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/congopro-bridge-db-backup.service
	@$(RSYNC) deploy/systemd/congopro-bridge-db-backup.timer $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/congopro-bridge-db-backup.timer
	@$(SSH) "sudo mv /tmp/congopro-bridge-db-backup.service /tmp/congopro-bridge-db-backup.timer /etc/systemd/system/ && sudo systemctl daemon-reload"
	@$(SSH) "sudo systemctl enable --now congopro-bridge-db-backup.timer"
	@echo "✓ backup timer installed — next run: $$($(SSH) 'systemctl show congopro-bridge-db-backup.timer -p NextElapseUSecRealtime --value')"

# Triggers an out-of-schedule backup run (the timer keeps its normal schedule).
db-backup-now:
	@echo "▶ Running an ad-hoc backup on $(DEPLOY_HOST)…"
	@$(SSH) "sudo systemctl start congopro-bridge-db-backup.service"
	@$(MAKE) db-backup-status

db-backup-status:
	@$(SSH) "sudo systemctl status congopro-bridge-db-backup.timer --no-pager -l || true"
	@$(MAKE) db-backup-list

db-backup-logs:
	$(SSH) "sudo journalctl -u congopro-bridge-db-backup.service -f --no-pager"

db-backup-list:
	@$(SSH) "ls -lht $(BACKUP_DIR) 2>/dev/null || echo '(no backups yet)'"

# Downloads every backup currently on the server into LOCAL_BACKUP_DIR (no --delete,
# so older backups you've already pulled and the server has since rotated away stay put).
db-backup-pull:
	@mkdir -p $(LOCAL_BACKUP_DIR)
	@echo "▶ Pulling backups from $(DEPLOY_HOST):$(BACKUP_DIR) → $(LOCAL_BACKUP_DIR)/…"
	@rsync -az --progress -e "ssh $(_ssh_opts)" $(DEPLOY_USER)@$(DEPLOY_HOST):$(BACKUP_DIR)/ $(LOCAL_BACKUP_DIR)/
	@echo "✓ backups pulled to $(LOCAL_BACKUP_DIR)/"

# Restores a dump into a throwaway database on the LOCAL dev Postgres and verifies it —
# proves a backup is actually restorable without going near production. Defaults to the
# newest file in LOCAL_BACKUP_DIR; pass BACKUP_FILE=path/to/x.dump to test a specific one.
db-restore-test: db-up
	@FILE="$(BACKUP_FILE)"; \
	if [ -z "$$FILE" ]; then \
	  FILE=$$(ls -t $(LOCAL_BACKUP_DIR)/*.dump 2>/dev/null | head -1); \
	fi; \
	if [ -z "$$FILE" ]; then \
	  echo "❌ no dump file found — run 'make db-backup-pull' first or pass BACKUP_FILE=..."; \
	  exit 1; \
	fi; \
	echo "▶ testing restore of $$FILE against local dev Postgres…"; \
	bash scripts/db-restore-test.sh "$$FILE"

# DESTRUCTIVE: overwrites the live production database. Requires db-restore-test to have
# been run first, and a typed confirmation on the server (ssh -t for the interactive prompt).
# Defaults to the newest file in LOCAL_BACKUP_DIR; pass BACKUP_FILE=path/to/x.dump to pick one.
db-restore:
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
	@echo "  Congopro Bridge — available make targets"
	@echo "  ────────────────────────────────────────────────────────"
	@echo "  Bootstrap:  deploy-all          Fresh server: Ollama + Meilisearch + app"
	@echo "  App:        deploy              Rebuild and deploy binary"
	@echo "              deploy-full         First-time: installs systemd unit + deploy"
	@echo "  Dev:        dev                 One command: deps + migrations + templ/CSS watch + hot-reload app"
	@echo "              dev-down            Stop the dev deps (postgres, meili, ollama; volumes kept)"
	@echo "              docker-up/down      Start/stop the whole stack in docker (incl. app container)"
	@echo "              templ               Regenerate *_templ.go from .templ sources"
	@echo "              meili-reset         Wipe local Meilisearch index"
	@echo "  Meili:      meili-setup         First-time remote Meilisearch install"
	@echo "              meili-index-reset   Wipe remote index (rebuilds on next start)"
	@echo "  Ollama:     ollama-setup        Install + configure + pull models"
	@echo "  DB (dev):   db-up/db-down       Start/stop local Postgres (docker compose)"
	@echo "              db-migrate          Apply migrations to local Postgres"
	@echo "              db-import           One-time: load the embedded JSON export into local Postgres"
	@echo "              create-admin        Interactively create a staff account (super_admin)"
	@echo "              test-integration    Run integration-tagged tests against local Postgres"
	@echo "  DB (prod):  db-install          Install PostgreSQL + PostGIS via apt (idempotent)"
	@echo "              db-provision        Create app role/database from secrets.env"
	@echo "              db-migrate-remote   Apply migrations on the VPS"
	@echo "              db-import-remote    One-time: load the embedded JSON export into production"
	@echo "              db-remote-status    systemctl status postgresql"
	@echo "              db-remote-check     Verify PostGIS + tables on the VPS"
	@echo "  Backups:    db-backup-install   Install backup script + daily systemd timer"
	@echo "              db-backup-now       Trigger an ad-hoc backup"
	@echo "              db-backup-status    Timer status + list of backups on the VPS"
	@echo "              db-backup-logs      Follow backup service logs"
	@echo "              db-backup-list      List backups on the VPS"
	@echo "              db-backup-pull      Download backups to ./backups/"
	@echo "              db-restore-test     Restore a backup into a local throwaway DB and verify it"
	@echo "              db-restore          DESTRUCTIVE: restore a backup onto production (confirmation required)"
	@echo "  ────────────────────────────────────────────────────────"
	@echo "  Key variables (set in .env or as env overrides):"
	@echo "    DEPLOY_HOST, DEPLOY_USER, DEPLOY_PORT, SSH_KEY"
	@echo "    REMOTE_DIR, MEILI_DIR, MEILI_VERSION, DOMAIN"
	@echo "    POSTGRES_PORT, DB_NAME, DB_USER, PG_VERSION"
	@echo "    BACKUP_DIR, BACKUP_KEEP, LOCAL_BACKUP_DIR, BACKUP_FILE"
	@echo ""