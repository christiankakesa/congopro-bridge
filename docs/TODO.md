# TODO

Later:
  * From Homepage, ← Back to home link must not appears as we already are at homepage

## Now

* (nothing — pull from Next when ready)

## Next (Phase 2 wrap-up — see [BACKEND_PROPOSAL.md](BACKEND_PROPOSAL.md))

* **Offsite backups to Cloudflare R2.** Local `pg_dump`s already run daily
  (`make prod-backup-install` / `db-backup-now` / `db-restore-test`), but they
  live on the same VPS — they don't survive losing the box. Copy the pattern
  already proven in two sibling projects:
  `~/workspace/audio-server/DEPLOY.md` ("Offsite backups" section — the
  `db-backup-offsite.sh` no-op-until-configured hook, `OFFSITE_MODE=s3`,
  `OFFSITE_RCLONE_REMOTE`) and `~/workspace/kito-platform/deploy/kito.env.example`
  (`BACKUP_STATUS_FILE` last-success marker that `/healthz` reads).
  Congopro needs: an offsite step at the end of `scripts/db-backup.sh`, an
  `/opt/congopro-bridge/backup-offsite.env` (never in git), and an rclone R2
  remote written directly to `~ops/.config/rclone/rclone.conf`.
  Gotchas already paid for in audio-server — don't rediscover them: use a
  **separate bucket and a separate scoped token** from any other project;
  match the **jurisdiction segment** in the endpoint
  (`<accountid>.eu.r2.cloudflarestorage.com` for EU buckets — a missing
  `.eu.` returns 403, which looks exactly like a bad token); set
  `no_check_bucket = true` (object-scoped tokens can't HeadBucket); the
  rclone `endpoint` wants the `https://` scheme. Then extend
  `db-restore-test` to restore *from the offsite copy* — an untested backup
  isn't a backup.

* **Stripe PRODUCTION setup (pairing session — needs Christian's Stripe
  account).** DEV is done and verified (2026-08-27, details below); only the
  live-mode half remains:
  - Create the "Mise en avant" product + monthly price in **live mode**
    (test-mode price is $10.00 USD/month — confirm the real number before
    going live; the app reads the amount from Stripe at runtime, so changing
    it is a new price, not a code change).
  - Register the webhook endpoint `https://congopro.com/webhooks/stripe`
    with events checkout.session.completed, customer.subscription.updated,
    customer.subscription.deleted, and copy its signing secret. NOTE: unlike
    dev, production's `whsec_…` comes from the **Dashboard** endpoint, not
    from `stripe listen`.
  - Store `sk_live_…`, that `whsec_…` and the live `price_…` as deploy
    secrets via `make prod-secrets-init` — never in git, never in .env.
  - Re-run the end-to-end check against production with a real card, then
    refund/cancel it.

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
* Payment processor: **Stripe** — account live, DEV verified end to end
  (2026-08-27); production keys/product still to create, see Next.
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

* Stripe DEV configured and verified end to end (2026-08-27): test-mode
  product "Mise en avant" + $10.00 USD/month price, keys in `.env`. Full
  chain exercised for real — OTP login → claim → staff approval (admin htmx
  flow) → Checkout → `checkout.session.completed` → `promotions` row
  (`status=active`, period end +1 month) → "Promu" badge. Two things learned
  worth keeping: the dev `whsec_…` comes from `stripe listen --print-secret`
  (the Dashboard only issues one for a registered HTTPS endpoint), and air
  does NOT watch `.env`, so touch a `.go` file to make the app re-read keys.
  Dev SMTP now points at the Mailpit container (localhost:1025) instead of
  live OVH, so test mail never leaves the machine.
* "Promu" badge on company profiles (2026-08-27) — the paid feature had
  shipped with the badge on result rows only, while `/account/promote`
  promised it "sur sa fiche et dans les résultats"; `companyProfileCard`
  took a `promoted` param it never used. Pinned by a test covering both
  surfaces plus the unpromoted control.
* Contact page (2026-08-27): `/contact` with a rate-limited form that mails
  `CONTACT_TO` (default ask@congopro.com) — honeypot, per-field validation,
  input preserved on error, form hidden (not silently broken) when SMTP is
  off. The footer's Telegram "Support" link and the Telegram CTAs on
  help/privacy/terms now route here; `TelegramSupportURL` lives in exactly
  one place. Added to sitemap.xml. **The support mailbox is deliberately not
  printed on the page** — it is indexed and in the sitemap, so a mailto:
  would feed harvesters, and the form's defences protect the form, not a
  published address. Don't "helpfully" add it back; publish a rotatable
  alias instead if a visible address is ever needed.

* UI redesign (2026-08-27): design tokens + light/dark themes (`input.css`
  `@theme inline`, `data-theme`, footer Auto/Clair/Sombre toggle — light is
  the default), official charte logo (inline `brandMark` + Sora wordmark at
  the measured -0.079em tracking), real font weights (Google Sans dropped,
  fonts 127→63 KB), `index.html` SPA shell retired into `templates/home.templ`
  + cacheable `js/app.js`, redesigned home hero/results/profile, snap-speed
  layer (htmx preload, cross-document view transitions, search skeletons),
  and the admin rebuilt: dashboard, sidebar, htmx live search, toasts,
  sectioned + per-field-validated forms. Perf: Traefik compression, 396 KB
  favicon → 2 KB vector, `?h=` cache-bust fix on admin/account CSS.
* "Fiche vérifiée" badge on public company profiles (2026-08-27) —
  `claims.IsClaimed`; the search-results fragment deliberately skips the
  lookup to keep the hot path clean.
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
  unit tests. Cutover: `make dev-db-import-ads` locally,
  `make prod-db-import-ads` in production.
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
  (`make dev-test-integration`).
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
