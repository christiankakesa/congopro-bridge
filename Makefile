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

_ssh_opts    := -p $(DEPLOY_PORT) -i $(SSH_KEY) \
                -o StrictHostKeyChecking=accept-new \
                -o ConnectTimeout=10
SSH          := ssh $(_ssh_opts) $(DEPLOY_USER)@$(DEPLOY_HOST)
RSYNC        := rsync -az --progress --delete \
                -e "ssh $(_ssh_opts)"

.PHONY: all build build-local clean test \
        docker-build docker-push docker-save docker-run docker-up docker-down docker-down-v meili-reset \
        deploy deploy-binary deploy-config deploy-service deploy-full deploy-all \
        service-start service-stop service-restart service-status service-logs \
        traefik-reload traefik-logs \
        ollama-install ollama-configure-limit ollama-pull-models ollama-clean-models ollama-reset ollama-status ollama-setup ollama-logs \
        meili-install meili-deploy-config meili-deploy-service meili-deploy-traefik meili-setup meili-start meili-stop meili-restart meili-status meili-logs meili-index-reset \
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

build: css
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

docker-up:
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
	docker compose stop meilisearch
	docker volume rm congopro-bridge_meili_data 2>/dev/null || true
	docker compose up -d meilisearch
	@echo "✓ Meilisearch volume wiped and restarted — app will re-index on next boot"

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
deploy-full: deploy-service deploy
	@$(SSH) "sudo systemctl enable $(SERVICE)"
	@echo "✓ $(SERVICE) enabled on boot"

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
meili-setup: meili-install meili-deploy-config meili-deploy-service meili-deploy-traefik
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

help:
	@echo ""
	@echo "  Congopro Bridge — available make targets"
	@echo "  ────────────────────────────────────────────────────────"
	@echo "  Bootstrap:  deploy-all          Fresh server: Ollama + Meilisearch + app"
	@echo "  App:        deploy              Rebuild and deploy binary"
	@echo "              deploy-full         First-time: installs systemd unit + deploy"
	@echo "  Dev:        docker-up/down      Start/stop local stack"
	@echo "              meili-reset         Wipe local Meilisearch index"
	@echo "  Meili:      meili-setup         First-time remote Meilisearch install"
	@echo "              meili-index-reset   Wipe remote index (rebuilds on next start)"
	@echo "  Ollama:     ollama-setup        Install + configure + pull models"
	@echo "  ────────────────────────────────────────────────────────"
	@echo "  Key variables (set in .env or as env overrides):"
	@echo "    DEPLOY_HOST, DEPLOY_USER, DEPLOY_PORT, SSH_KEY"
	@echo "    REMOTE_DIR, MEILI_DIR, MEILI_VERSION, DOMAIN"
	@echo ""