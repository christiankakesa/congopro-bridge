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
[DEPLOY.md](docs/DEPLOY.md) (production),
[PROMOTIONS.md](docs/PROMOTIONS.md) (Stripe promoted listings),
[VISION.md](docs/VISION.md) (long-term direction).

## Prerequisites

- Go 1.25+
- Docker (for Postgres, Meilisearch, Ollama via `docker-compose.yml`)
- `templ` CLI (`go install github.com/a-h/templ/cmd/templ@latest`) and the
  `tailwindcss` CLI on your PATH — or just use the make targets, which check
  for both and tell you what's missing.

## Local setup

The one-command dev loop (first run pulls ~1 GB of Ollama models, once):

```bash
make dev    # deps up (docker) → migrations → app on :8080 with hot reload
make dev-db-import    # once: load the legacy embedded JSON into local Postgres
make dev-admin-create # once: create the first staff account (TOTP enrollment)
```

`make dev` watches `.templ`, `.go`, and Tailwind `input.css` files — every
save regenerates templ code, recompiles the CSS, and rebuilds/restarts the
binary via [air](https://github.com/air-verse/air) (install it with
`go install github.com/air-verse/air@latest`; without it the app runs via
`go run` with manual restarts). Ctrl+C stops the app but leaves the dockerised
deps running for fast restarts; `make dev-deps-down` stops the deps.

`DATABASE_URL` is required — `make dev` and the other `db-*` targets set it
for you. There is deliberately no default credential baked into the binary.

The manual equivalent, if you prefer separate steps: `make dev-db-up` (local
Postgres on 127.0.0.1:5433), `make dev-db-migrate`, `make build`. To run the
whole stack including the app in Docker: `make dev-stack-up`.

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | *(none, required)* | Postgres connection string |
| `PORT` | `8080` | Listen port |
| `OLLAMA_URL` | `http://127.0.0.1:11434` | Ollama base URL (used by the app itself) |
| `OLLAMA_EMBEDDER_URL` | *(same as `OLLAMA_URL`)* | Ollama URL as reached **by Meilisearch** — set to `http://ollama:11434` when Meilisearch runs in docker but the app runs natively (`make dev` sets this) |
| `GENERATIVE_MODEL` | `gemma3:1b` | Ollama model for AI answers |
| `EMBEDDING_MODEL` | `nomic-embed-text` | Ollama model for embeddings |
| `MEILI_URL` | `http://127.0.0.1:7700` | Meilisearch base URL |
| `MEILI_MASTER_KEY` | *(empty)* | Meilisearch API key (prod) |
| `MEILI_INDEX_NAME` | `companies` | Search index name |
| `ALLOWED_ORIGIN` | *(empty = no CORS)* | Allow a third-party origin to call the API |
| `TRUSTED_PROXIES` | `127.0.0.1/32,::1/128` | Proxies allowed to set forwarding headers |
| `SMTP_HOST` | *(empty = email disabled)* | SMTP server (OVH EmailPro: `ssl0.ovh.net`). All-or-nothing block — half-set is refused at boot |
| `SMTP_PORT` | `587` | SMTP port (587 with `starttls`, 465 with `implicit`) |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | *(empty)* | Full mailbox address + password (required for `starttls`/`implicit`; must be **empty** for `none`) |
| `SMTP_FROM` / `SMTP_FROM_NAME` | *(empty)* | Envelope sender — a mailbox actually owned on the domain |
| `SMTP_TLS` | `starttls` | `starttls` \| `implicit` \| `none` (local capture via Mailpit, no credentials — see `make dev-mail-up`) |
| `STRIPE_SECRET_KEY` | *(empty = disabled)* | Stripe key for promoted listings — all three Stripe keys required together |
| `STRIPE_WEBHOOK_SECRET` | *(empty)* | Signing secret for `POST /webhooks/stripe` (locally: `stripe listen --forward-to localhost:8090/webhooks/stripe`) |
| `STRIPE_PRICE_ID` | *(empty)* | Monthly recurring Price of the "Mise en avant" product |
| `TELEGRAM_BOT_TOKEN` | *(empty = disabled)* | Bot token for staff notifications — both Telegram keys required together |
| `TELEGRAM_CHAT_ID` | *(empty)* | Private staff chat id (negative for groups; read it from `getUpdates`) |

When Telegram is configured the server also long-polls for bot updates: claim
notifications carry approve/reject buttons, and the staff chat answers
`/pending` and `/stats` (no extra env vars). One consumer per bot token — dev
runs need their own bot, or they steal the production bot's updates.

## CLI flags

| Flag | Purpose |
|---|---|
| `-migrate` | Apply pending migrations and exit |
| `-import` | Upsert the embedded legacy JSON into Postgres and exit |
| `-create-admin` | Interactively create the first staff account and exit |
| `-digest` | Send the daily staff digest to Telegram and exit (run by a systemd timer) |
| `-link-telegram` | Interactively link a staff account to a Telegram user id and exit |

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
internal/ads/          ads CMS: DB-backed campaigns + in-memory serving snapshot
deploy/                systemd units, Traefik config, Meilisearch config
```

## Deployment

Production runs on a VPS via systemd + Traefik (no docker). See
[docs/DEPLOY.md](docs/DEPLOY.md) — bootstrap, day-to-day `make prod-deploy`,
backups with tested restores.
