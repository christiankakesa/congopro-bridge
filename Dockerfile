# ─────────────────────────────────────────────────────────────────
# Stage 1 — Build (Go binary)
# ─────────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
      -ldflags="-s -w -extldflags '-static'" \
      -trimpath \
      -o /out/congopro-bridge \
      ./cmd/congopro-bridge

# ─────────────────────────────────────────────────────────────────
# Stage 2 — Runtime (Ubuntu + Ollama + modèles intégrés)
# ─────────────────────────────────────────────────────────────────
FROM ubuntu:22.04 AS ollama-runtime

# Installation des dépendances système
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    zstd \
    && rm -rf /var/lib/apt/lists/*

# Installation d'Ollama
RUN curl -fsSL https://ollama.com/install.sh | sh

# ── Téléchargement des modèles pendant le build (intégration) ──
# On lance le serveur Ollama en arrière-plan, on attend son démarrage,
# on télécharge les trois modèles, puis on arrête le serveur.
# Les fichiers restent dans /root/.ollama/models
RUN nohup ollama serve > /tmp/ollama.log 2>&1 & \
    echo "Waiting for Ollama to start..." && \
    while ! curl -s http://localhost:11434/api/tags > /dev/null; do sleep 2; done && \
    echo "Ollama ready, pulling models..." && \
    ollama pull nomic-embed-text && \
    ollama pull gemma:2b && \
    ollama pull phi4-mini && \
    ollama pull llama3.2:3b && \
    echo "Models pulled, stopping Ollama..." && \
    pkill ollama || true

# Script de démarrage simplifié (plus besoin de pulls)
RUN echo '#!/bin/bash\n\
set -e\n\
ollama serve &\n\
OLLAMA_PID=$!\n\
echo "Waiting for Ollama to be ready..."\n\
while ! curl -s http://localhost:11434/api/tags > /dev/null; do sleep 1; done\n\
echo "Ollama is ready. Starting Go app..."\n\
exec /congopro-bridge "$@"\n\
' > /start.sh && chmod +x /start.sh

ENV OLLAMA_NUM_THREADS=2

COPY --from=builder /out/congopro-bridge /congopro-bridge

# (Optionnel) Utilisateur non-root – attention : les modèles appartiennent à root
# Si vous voulez passer à un utilisateur non-root, décommentez les lignes suivantes :
# RUN useradd -m -u 1000 appuser && chown -R appuser:appuser /root/.ollama /congopro-bridge /start.sh
# USER appuser

EXPOSE 8080
ENTRYPOINT ["/start.sh"]