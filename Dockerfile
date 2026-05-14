FROM golang:1.25-alpine3.23 AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
      -ldflags="-s -w" \
      -trimpath \
      -buildvcs=false \
      -o /out/congopro-bridge \
      ./cmd/congopro-bridge

FROM alpine:3.23

RUN apk add --no-cache curl ca-certificates

RUN adduser -D -u 1000 appuser

WORKDIR /app

COPY --from=builder /out/congopro-bridge ./
COPY deploy/docker/start.sh ./
RUN chmod +x start.sh && chown appuser:appuser congopro-bridge start.sh

ENV HOME=/home/appuser

USER appuser

EXPOSE 8080

ENTRYPOINT ["./start.sh"]