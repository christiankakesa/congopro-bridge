-include .env
export

DEPLOY_USER  ?= ops
DEPLOY_HOST  ?= xxx.xxx.xxx.xxx
DEPLOY_PORT  ?= 4242
SSH_KEY      ?= $(HOME)/.ssh/id_ed25519
REMOTE_DIR   ?= /opt/congopro-bridge
IMAGE        ?= congopro-bridge
TAG          ?= latest
DOMAIN       ?= congopro.com
CMD_PATH     := ./cmd/congopro-bridge
BINARY       := congopro-bridge
BUILD_DIR    := ./build
SERVICE      := congopro-bridge
TAILWIND_CLI := $(shell which tailwindcss)

_ssh_opts    := -p $(DEPLOY_PORT) -i $(SSH_KEY) \
                -o StrictHostKeyChecking=accept-new \
                -o ConnectTimeout=10
SSH          := ssh $(_ssh_opts) $(DEPLOY_USER)@$(DEPLOY_HOST)
RSYNC        := rsync -az --progress --delete \
                -e "ssh $(_ssh_opts)"

.PHONY: all build build-local clean test \
        docker-build docker-push docker-save docker-run \
        deploy deploy-binary deploy-config deploy-service deploy-full \
        service-start service-stop service-restart service-status service-logs \
        traefik-reload traefik-logs \
        ollama-install ollama-configure-limit ollama-pull-models ollama-status ollama-setup ollama-logs deploy-with-ai \
        ssh ping \
        help

all: build

css:
	@echo "▶ Compiling Tailwind CSS using local binary…"
	@# On vérifie si le fichier existe avant de lancer
	@if [ ! -f $(TAILWIND_CLI) ]; then \
		echo "❌ Erreur: ./tailwindcss introuvable à la racine."; \
		echo "Télécharge-le via: curl -sLO https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-linux-x64 && mv tailwindcss-linux-x64 tailwindcss && chmod +x tailwindcss"; \
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

ping:
	@echo "▶ pinging $(DEPLOY_USER)@$(DEPLOY_HOST):$(DEPLOY_PORT)…"
	@$(SSH) "echo '✓ connected as $(DEPLOY_USER) on $(DEPLOY_HOST)'"

ssh:
	ssh $(_ssh_opts) $(DEPLOY_USER)@$(DEPLOY_HOST)

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
	@echo "▶ Installing systemd unit…"
	@$(RSYNC) deploy/systemd/$(SERVICE).service $(DEPLOY_USER)@$(DEPLOY_HOST):/tmp/$(SERVICE).service
	@$(SSH) "sudo mv /tmp/$(SERVICE).service /etc/systemd/system/$(SERVICE).service && sudo systemctl daemon-reload"
	@echo "✓ unit installed — run 'make service-start' to enable"

deploy-full: deploy-service deploy
	@$(SSH) "sudo systemctl enable $(SERVICE)"
	@echo "✓ service enabled on boot"

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

traefik-reload:
	@echo "▶ Triggering Traefik dynamic config reload…"
	@$(SSH) "sudo touch /srv/traefik/dynamic/congopro-bridge.yml"
	@echo "✓ Traefik will pick up changes within a few seconds"

traefik-logs:
	$(SSH) "sudo journalctl -u traefik -f --no-pager 2>/dev/null || sudo docker logs -f \$$(sudo docker ps -qf name=traefik)"

OLLAMA_MODELS ?= nomic-embed-text gemma:2b phi4-mini llama3.2:3b
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

ollama-status:
	@$(SSH) "sudo systemctl status ollama --no-pager -l || true"
	@$(SSH) "ollama list"

ollama-setup: ollama-install ollama-configure-limit ollama-pull-models
	@echo "╔═════════════════════════════════════════════════════════════════════════════╗"
	@echo "║  Ollama is ready with nomic-embed-text, gemma:2b, phi4-mini & llama3.2:3b   ║"
	@echo "╚═════════════════════════════════════════════════════════════════════════════╝"

ollama-logs:
	$(SSH) "sudo journalctl -u ollama -f --no-pager"

deploy-with-ai: ollama-setup deploy
	@echo "✓ Application + AI search backend ready"

help:
	@echo ""
	@echo "  Congopro Bridge — available make targets"
	@echo "  ────────────────────────────────────────────────────────"
	@grep -E '^## ' $(MAKEFILE_LIST) \
	  | sed 's/^## //' \
	  | awk -F': ' '{printf "  \033[36m%-26s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "  Key variables (set in .env or as env overrides):"
	@echo "    DEPLOY_HOST, DEPLOY_USER, DEPLOY_PORT, SSH_KEY"
	@echo "    REMOTE_DIR, DOMAIN, IMAGE, TAG"
	@echo ""