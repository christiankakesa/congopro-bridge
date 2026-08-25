# Congopro Bridge — Architecture (current, as shipped)

One Go binary on one VPS, three backing services, server-rendered frontend.
Deliberately boring: the build order and the reasoning behind every choice
(Postgres over rqlite, no Kubernetes yet, etc.) live in
[BACKEND_PROPOSAL.md](BACKEND_PROPOSAL.md).

```
                        ┌──────────────────────────┐
                        │   PostgreSQL 16 + PostGIS │  ← source of truth
                        │  companies · users ·      │
                        │  sessions                 │
                        └────────┬─────────────────┘
                                 │ load published companies
                                 ▼
   ┌────────────────┐   ┌──────────────────┐   ┌────────────────┐
   │  Meilisearch   │   │  Congopro Bridge │   │     Ollama     │
   │  "companies"   │◄──┤  (Go binary)     ├──►│ gemma3:1b      │
   │  full-text idx │   │                  │   │ nomic-embed-   │
   │ (rebuildable)  │   │  templ + htmx    │   │ text           │
   └────────────────┘   └────────┬─────────┘   └────────────────┘
                                 │
                    ┌────────────┴────────────┐
                    ▼                         ▼
             public site                  /admin
             search · company             sessions + TOTP · roles
             profiles · ads ·             company CRUD
             static pages · AI answers
```

## Invariants

- **Postgres is the only source of truth.** Meilisearch is a disposable,
  rebuildable index synced *from* Postgres — never written to directly except
  by the sync path (`Engine.LoadAndIndex` on startup, `Engine.Reload()`
  fired asynchronously after admin writes, since Meilisearch's embedding step
  can be slow). Only `status = 'published'` companies are indexed; drafts stay
  hidden.
- **No client framework.** templ components + Tailwind CSS + htmx for partial
  updates. CSS, JS, fonts, and images are embedded in the binary and
  self-hosted — the page weight budget is the product requirement
  (low-bandwidth, mobile-first DRC audience).
- **AI is local.** Ollama serves both the generative model (grounded answer
  over current search results, `GET /api/v1/ask`) and embeddings. No external
  AI API, no data leaves the VPS.

## Runtime pieces

| Piece | Where | Notes |
|---|---|---|
| HTTP server | `cmd/congopro-bridge/main.go` | Go 1.25 `net/http` pattern routing (`GET/POST /path`), all routes in one place |
| Handlers | `internal/api/` | public pages, JSON API, admin; every route wrapped with security headers |
| Data engine | `internal/data/engine.go` | Postgres → Meilisearch sync, Ollama calls, sitemap cache (RWMutex-guarded) |
| Auth | `internal/auth/` | bcrypt password hashing (72-byte guard), TOTP, DB-backed sessions |
| Rate limiting | `internal/middlewares/ratelimiter/` | per-endpoint; `TRUSTED_PROXIES` stops forwarding-header spoofing |
| Migrations | `internal/db/migrations/` | goose format, embedded via `go:embed`, applied by `<binary> -migrate` |
| Promoted listings | `internal/promotions` + `promotions` table | Stripe Checkout subscriptions gated on approved claims; webhook (`/webhooks/stripe`) drives lifecycle; badges on profile/results |
| Ads | `internal/ads` + `ads`/`ads_settings` tables | DB-backed CMS (`/admin/ads`), in-memory serving snapshot reloaded on admin writes; `ads.yml` remains only as the one-time `-import-ads` source |
| Templates | `internal/web/templates/` | `.templ` sources; generated `_templ.go` files are committed |

## Side tooling (offline jobs)

- `cmd/cleanr` — data normalization (city/activity normalizers, link
  validation) against the legacy JSON export.
- `cmd/geocoder` — GPS coordinates backfill.
- `cmd/oid` — misc one-off tooling.

## Environments

- **Local dev** — docker-compose for Postgres (host port 5433), Meilisearch,
  Ollama; the binary runs natively. `make db-up / db-migrate / db-import`.
- **Production** — single VPS, systemd services + Traefik, no docker. Secrets
  generated on-server (`make secrets-init`), daily `pg_dump` backups with a
  tested-restore path. Full runbook: [DEPLOY.md](DEPLOY.md).

## Where this goes next

Phases 2–4 (customer accounts, claims/disputes, ads CMS, subscriptions,
Telegram notifications, MCP/BI product) are scoped and sequenced in
[BACKEND_PROPOSAL.md](BACKEND_PROPOSAL.md). The long-term multi-country
end-state is sketched in [VISION.md](VISION.md).
