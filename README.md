# Congopro Bridge 🔍

A search engine and business directory for companies in the Democratic Republic
of the Congo — server-rendered Go, mobile-first, built for low-bandwidth users.
Live at [congopro.com](https://congopro.com).

## Architecture

One Go binary. Three backing services. Postgres is the only source of truth;
everything else is rebuildable.

```
PostgreSQL 16 + PostGIS          Meilisearch                Ollama
(source of truth: companies,  ──►  (full-text search     ──►  (AI answers,
 users, sessions)                   index, rebuildable)      embeddings)
        │
        │  sync on startup + on company writes (LoadAndIndex)
        ▼
   Meilisearch index "companies"

public site (templ + htmx)      /admin (templ + htmx)
  search, company profiles,       sessions + TOTP, roles,
  static pages, ads, AI answers    company CRUD
```

- **Frontend** — server-rendered with [templ](https://github.com/a-h/templ) +
  Tailwind CSS, interactivity via htmx. No client framework. CSS/JS/fonts are
  self-hosted and served from the binary.
- **Search** — Meilisearch (typo-tolerant full-text over name/activity/city/
  description), synced from Postgres. `status = 'published'` only.
- **AI answers** — a local Ollama model (`gemma3:1b` generative,
  `nomic-embed-text` embeddings) summarizes search results. No external AI API.
- **Auth** — session-based staff login with TOTP 2FA, roles
  (`super_admin`, `ads_rep`, `data_editor`, `support`).
- **Ops** — systemd + Traefik on a VPS in production; per-endpoint rate
  limiting (with `TRUSTED_PROXIES` so forwarding headers can't be spoofed).

Further reading: [ARCHITECTURE.md](docs/ARCHITECTURE.md) (current system),
[BACKEND_PROPOSAL.md](docs/BACKEND_PROPOSAL.md) (roadmap),
[DEPLOY.md](docs/DEPLOY.md) (production), [VISION.md](docs/VISION.md)
(long-term direction).

## Prerequisites

- Go 1.25+
- Docker (for Postgres, Meilisearch, Ollama via `docker-compose.yml`)
- `templ` CLI (`go install github.com/a-h/templ/cmd/templ@latest`) and the
  `tailwindcss` CLI on your PATH — or just use the make targets, which check
  for both and tell you what's missing.

## Local setup

```bash
make db-up          # start local Postgres (postgis:16-3.4) on 127.0.0.1:5433
make db-migrate     # apply goose migrations (embedded in the binary)
make db-import      # one-time: load the legacy embedded JSON into Postgres
make create-admin   # interactively create the first staff account (TOTP setup)

make build          # compile CSS + templ + binary into ./build
./build/congopro-bridge
```

`DATABASE_URL` is required — the make targets set it for you
(`postgres://congopro_bridge:congopro_bridge@localhost:5433/congopro_bridge`).
There is deliberately no default credential baked into the binary.

To run the backing services in Docker too: `make docker-up` (Ollama models are
pulled automatically by the `ollama-init` container).

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | *(none, required)* | Postgres connection string |
| `PORT` | `8080` | Listen port |
| `OLLAMA_URL` | `http://127.0.0.1:11434` | Ollama base URL |
| `GENERATIVE_MODEL` | `gemma3:1b` | Ollama model for AI answers |
| `EMBEDDING_MODEL` | `nomic-embed-text` | Ollama model for embeddings |
| `MEILI_URL` | `http://127.0.0.1:7700` | Meilisearch base URL |
| `MEILI_MASTER_KEY` | *(empty)* | Meilisearch API key (prod) |
| `MEILI_INDEX_NAME` | `companies` | Search index name |
| `ALLOWED_ORIGIN` | *(empty = no CORS)* | Allow a third-party origin to call the API |
| `TRUSTED_PROXIES` | `127.0.0.1/32,::1/128` | Proxies allowed to set forwarding headers |

## CLI flags

| Flag | Purpose |
|---|---|
| `-migrate` | Apply pending migrations and exit |
| `-import` | Upsert the embedded legacy JSON into Postgres and exit |
| `-create-admin` | Interactively create the first staff account and exit |

## API

All endpoints are rate-limited. `ALLOWED_ORIGIN` must be set for cross-origin
browser use.

### `GET /api/v1/search?q=<query>`

```json
{
  "query": "restaurant kinshasa",
  "total": 12,
  "results": [
    {
      "id": "5001fe28d964680200000305",
      "name": "DA SAFI DECOR",
      "name_seo": "da-safi-decor",
      "activity": "Ameublement et mobilier",
      "city": "Kinshasa",
      "country": "Democratic Republic of the Congo",
      "description": "...",
      "score": 0.847
    }
  ]
}
```

### `GET /api/v1/ask?q=<query>`

AI-generated answer (`{ "query": ..., "answer": ... }`) grounded in the
current search results, computed by the local Ollama model.

### `GET /api/v1/healthz`

`{"status":"ready","companies":1534}` once startup indexing is done.

Also: `GET /api/v1/ads` (active ad campaigns, YAML-configured — see
`internal/ads/ads.yml`), `GET /api/v1/content/{page}` (static pages as JSON).

## Project layout

```
cmd/congopro-bridge/   app entrypoint + admin bootstrap
cmd/cleanr/            data normalization tools (city/activity, link validation)
cmd/geocoder/          GPS coordinates backfill
internal/api/          HTTP handlers (public, API, admin)
internal/data/         engine: Postgres load → Meilisearch index, Ollama calls
internal/auth/         sessions, TOTP, password hashing
internal/db/           goose migrations (embedded)
internal/web/          templ templates, CSS, embedded static assets
internal/ads/          YAML-driven ad campaigns
deploy/                systemd units, Traefik config, Meilisearch config
```

## Deployment

Production runs on a VPS via systemd + Traefik (no docker). See
[docs/DEPLOY.md](docs/DEPLOY.md) — bootstrap, day-to-day `make deploy`,
backups with tested restores.
