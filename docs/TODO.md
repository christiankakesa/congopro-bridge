# TODO

## Now

* (nothing — pull from Next when ready)

## Next (Phase 2 — see [BACKEND_PROPOSAL.md](BACKEND_PROPOSAL.md))

* Customer accounts (email OTP over OVH EmailPro SMTP — sender ready and
  proven (`internal/mail`, `make mail-test`, Mailpit capture via
  `make mail-up`); OVH account created, SPF/DKIM/DMARC propagating. Still
  needed: the customers table + the OTP flow itself).
* Company claim/dispute workflow + admin queue.
* Ads CMS replacing `ads.yml`, with sales-rep attribution.
* Stripe integration for promoted listings + ads — **blocked on account
  creation**. When the account exists we will need: the API keys
  (`STRIPE_SECRET_KEY`), a webhook endpoint on our side (HTTPS, behind
  Traefik), the webhook signing secret (`STRIPE_WEBHOOK_SECRET`), and
  handling for at least `checkout.session.completed`,
  `customer.subscription.updated`, and `customer.subscription.deleted`.
  Test locally with the Stripe CLI (`stripe listen --forward-to`).
* Telegram bot as notification/quick-action layer on top of the CMS.

## Later

* Research: paid MCP server / API for B2B customers to point their own BI
  tools at their filtered data slice (Phase 4 in BACKEND_PROPOSAL.md — only
  worth building once auth + billing exist).
* Merge/dedupe tooling (`cmd/cleanr`) pointed at Postgres rows instead of the
  legacy JSON file.
* Admin: company delete/archive and user management UI (roles exist in the
  schema; only company CRUD is exposed so far).

## Decisions

Recorded 2026-08-17 (details in [BACKEND_PROPOSAL.md](BACKEND_PROPOSAL.md)):

* Email provider: **OVH EmailPro SMTP** (STARTTLS, AUTH PLAIN) — sender in
  `internal/mail`; SPF/DKIM/DMARC setup required on the sending domain.
* Payment processor: **Stripe** (account pending — webhook checklist under
  Next).
* Scale: **~10 staff over 12 months** — role enum stays, no permissions
  matrix.

Still open: Postgres hosting (same VPS vs small managed instance).

## Reference

* PageSpeed (mobile): [report 1](https://pagespeed.web.dev/analysis/https-congopro-com/loz8e4kjae?hl=fr&form_factor=mobile),
  [report 2](https://pagespeed.web.dev/analysis/https-congopro-com/1w6laz73ws?utm_source=search_console&form_factor=mobile&hl=fr)

## Done (highlights — full history in git)

* AI answer fallback: when the model answers "je l'ignore", the user now gets
  a grounded insight computed from the search results (count, top cities,
  top activities) instead of a dead end — `internal/data/engine.go`.
* `make dev` fixes found along the way: `OLLAMA_EMBEDDER_URL` split (Meilisearch
  in docker can't reach 127.0.0.1 — the embedder URL must be compose-internal),
  and `meili-reset` now actually wipes the volume (`compose rm -sf` — a stopped
  container still pins its volumes, the old target never worked).
* `make dev` local loop (Kora-style): deps via docker compose, migrations,
  templ + Tailwind regeneration and hot-reload rebuilds in one target — see
  `scripts/dev.sh` and `.air.toml`.
* Postgres + PostGIS migration off embedded JSON; goose migrations embedded in
  the binary (`-migrate`, `-import`).
* Admin Phase 1: session login + TOTP, roles, company list/create/edit with
  async Meilisearch reindex on write.
* Hybrid search via Meilisearch + local Ollama AI answers (`/api/v1/ask`).
* sitemap.xml.gz (static pages + company profiles), schema.org markup,
  `/(fr|en)` i18n routing, legacy congopro.com redirect handling.
* Cookie consent banner; YAML-driven ads with rotation.
* Full deploy tooling: systemd + Traefik, secrets-init, daily backups with
  tested-restore path (`db-restore-test`).
* Logo, favicon pipeline, social banners, OG images.
