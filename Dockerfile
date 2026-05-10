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

FROM ubuntu:22.04 AS ollama-runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    zstd \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://ollama.com/install.sh | sh

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

## If you want the file to belong to a normal user, uncomment the next 2 lines
# RUN useradd -m -u 1000 appuser && chown -R appuser:appuser /root/.ollama /congopro-bridge /start.sh
# USER appuser

EXPOSE 8080
ENTRYPOINT ["/start.sh"]