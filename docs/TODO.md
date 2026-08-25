# TODO

Later: 
  * add a contact form: must add support lnk (Telegram) in contact page
  * remove Support link from the footer add a contact link
  * From /help, /privacy, /terms change the telegram link to contact page link
  * From Homepage, ← Back to home link must not appears as we already are at homepage

## Now

* (nothing — pull from Next when ready)

## Next (Phase 2 wrap-up — see [BACKEND_PROPOSAL.md](BACKEND_PROPOSAL.md))

* Surface "verified owner" on public company profiles (data exists since
  claims landed; display is a deliberate separate step).
* Telegram bot as notification/quick-action layer on top of the CMS.
* Promoted-listing ranking (pin/badge-weight in Meilisearch) — v1 ships
  badges only.
* Admin revenue view (promotions + ads attribution).

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

## Deliverability watch (OVH EmailPro — SPF/DKIM/DMARC all PASS, new domain
lands in Gmail spam on reputation alone)

* Register Google Postmaster Tools for congopro.com (DNS TXT verify) — the
  only visibility into Gmail placement.
* Confirm DMARC `rua` aggregate reports actually arrive at ask@congopro.com
  and contain only OVH's sending IPs.
* DMARC step-up in progress: `p=quarantine; pct=25` (set 2026-08-17).
  Climb pct=50 → pct=100 as rua stays clean, then end state
  `p=reject; sp=reject; rua=mailto:ask@congopro.com` (no pct).
  `sp` stays unset until the end state — subdomains inherit `p`.
* Revisit the provider decision ONLY if Postmaster shows good reputation
  after ~4–6 weeks and placement is still bad.

## Reference

* PageSpeed (mobile): [report 1](https://pagespeed.web.dev/analysis/https-congopro-com/loz8e4kjae?hl=fr&form_factor=mobile),
  [report 2](https://pagespeed.web.dev/analysis/https-congopro-com/1w6laz73ws?utm_source=search_console&form_factor=mobile&hl=fr)

## Done (highlights — full history in git)

* Stripe promoted listings (2026-08-25): ownership-gated (approved claim
  required) monthly subscriptions via Stripe Checkout — migration 00006
  (`promotions`), `internal/promotions`, webhook
  `POST /webhooks/stripe` (signature-verified, idempotent appliers) for
  checkout.session.completed / subscription.updated / deleted, "Promu"
  badges on profiles + result rows, `/account/promote` with price display.
  All-or-nothing `STRIPE_*` env keys.
* Ads CMS (2026-08-25): campaigns in Postgres (migration 00005), editable in
  `/admin/ads` without redeploy — global settings incl. the live kill
  switch, campaign CRUD with sales attribution (sold_by / customer / price),
  label presets, keyword textarea. Serving semantics preserved byte-for-byte
  from ads.yml (keyword priority, weighting, 75/25, rotation), locked by
  unit tests. Cutover: `make db-import-ads` locally,
  `make db-import-ads-remote` in production.
* Company claims / dispute workflow (2026-08-24): customers claim companies
  from the profile page (`Réclamer`), staff arbitrate in `/admin/claims`
  (approve/reject with note, one form two `formaction` buttons), approval
  durably sets `companies.claimed_by_customer_id`; login gained `?next=`
  redirects; decision emails to claimants. Migration 00004, `internal/claims`,
  partial unique indexes arbitrate concurrency (one pending / one approved
  per company).
* Customer accounts (2026-08-18): passwordless email-OTP login under
  `/account` — `customers`/`customer_sessions`/`otp_codes` (migration 00003),
  `internal/customers`, auto-create on first verified login, 10-min codes
  (SHA-256 at rest, 5 attempts, 60s cooldown, atomic single-use), 30-day
  sessions, SameSite=Strict cookie, OVH EmailPro + Mailpit capture, rate
  limits on both POSTs. Includes the repo's first integration tests
  (`make test-integration`).
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
