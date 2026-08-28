# TODO

## Now

* (nothing — pull from Next when ready)

## Next (Phase 2 wrap-up — see [BACKEND_PROPOSAL.md](BACKEND_PROPOSAL.md))

* Telegram bot v2: quick actions (approve/reject claims from the chat via
  callback queries — needs a receiver and Telegram↔staff identity mapping;
  v1 is deliberately send-only).

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
* Payment processor: **Stripe** — live and verified with a real payment
  (2026-08-27). Note the CLI on this machine is logged into a **sandbox**
  (`acct_1U5NcX…`) which can never do livemode; the real account is
  `acct_1U5NcNLbym0EdjAY` and production work happens in the Dashboard.
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

* Telegram staff notifications v1 (2026-08-28): send-only bot posting to a
  private chat — new claims (with /admin/claims deep link), contact
  messages, promotion activated/past-due/canceled (transition-gated via
  RowsAffected/RETURNING so webhook replays never re-notify), companies on
  their transition into published, ERROR-level logs (zerolog hook:
  non-blocking, rate-limited, `[telegram]`-marker loop guard), and a daily
  07:00 digest via systemd timer + `-digest` flag
  (`make prod-digest-install/-now/-status`). Config is all-or-nothing
  TELEGRAM_BOT_TOKEN + TELEGRAM_CHAT_ID; empty disables everything cleanly.

* Admin revenue view (2026-08-28): /admin/revenue — MRR and per-subscription
  amounts fetched live from Stripe (prices are immutable, so old subscribers
  can be on different amounts; the local table deliberately stores no money),
  60s cache, 5s timeout, warning-banner degradation when Stripe is down;
  promotions table with customer emails joined, ads sales ledger with
  attribution from price_cents/sold_by.

* Promoted-listing ranking (2026-08-28): promoted companies pin to the top
  of matching search results, Go-side (`pinPromoted` in the search handler)
  rather than a Meilisearch ranking rule — instant on webhook, no reindex,
  no coupling with hybrid scoring. Promise copy on /account/promote updated.

* Disaster recovery rehearsed on production (2026-08-28): `prod-db-restore`
  run against the live database with a fresh dump — service stopped,
  `pg_restore --clean`, auto-restart, and afterwards row counts identical to
  the pre-restore baseline across all 12 tables, healthz ready, search
  re-indexed, claim/promotion state intact. The drill paid for itself
  immediately: it exposed `prod-db-restore` as broken by the Makefile rename
  (`prod-ssh: not found`), a failure that would otherwise have appeared for
  the first time during a real outage.
* Offsite backups LIVE and verified (2026-08-28): bucket
  `congopro-db-backups` (EU jurisdiction, bucket-scoped token), 8 dumps
  pushed on the first run, and the full guarantee proven —
  `dev-db-restore-test-offsite` fetched the newest dump FROM the bucket and
  restored 1500 companies into throwaway local Postgres. One R2 quirk found
  live: `rclone rcat` streams without Content-Length and R2 answers **501
  NotImplemented**, which reads like broken credentials — the configure
  round trip now uses `copyto` of a temp file, the same operation the
  backup itself uses.
* Offsite backup tooling (2026-08-28): `scripts/db-backup-offsite.sh`
  (no-op until `/opt/congopro-bridge/backup-offsite.env` exists; `rclone
  copy` + 90-day age prune — deliberately NOT `sync`, which would collapse
  offsite retention to the local 14-newest rotation and could wipe remote
  history after a local disk loss), wired into `db-backup.sh` as a
  never-fatal last step, plus `last-success`/`offsite-last-success` markers
  (kito pattern). Make targets: `prod-backup-offsite-configure` (interactive,
  secret over stdin, verified write/read/delete round trip),
  `-status`, `-pull`, and `dev-db-restore-test-offsite` (restores the copy
  fetched FROM the bucket). Runs as `postgres`, so both config files live
  under `/opt/congopro-bridge` — not any home directory. Deployed; the
  audio-server gotchas (`.eu.` endpoint, `no_check_bucket`, https scheme,
  separate bucket+token) are baked into the generated config and DEPLOY.md.

* Stripe production VERIFIED with a real payment (2026-08-27). Live product
  "Mise en avant" on `acct_1U5NcNLbym0EdjAY`, USD monthly price
  `price_1U95Jh…`, webhook `https://congopro.com/webhooks/stripe`. Proven end
  to end against live Stripe by promoting Congopro's own listing at a
  temporary $1 price: Checkout → signed webhook 200 → `promotions` row active
  → "Promu" badge live → cancel → webhook → row `canceled` → badge gone. The
  $1 price was then archived, the $15 price restored, subscription cancelled
  and the charge refunded. Note prices are immutable once used: editing the
  amount works only while a price has no subscription, otherwise create a new
  price and repoint `STRIPE_PRICE_ID`.

  Three real bugs surfaced only because the payment was run for real, all
  fixed in `ed180c7`:
  - `/account/promote` was unreachable from anywhere in the UI — the only
    mention of it in the template tree was the form action on the page
    itself, and the account dashboard had no links at all. The paid feature
    could not be bought without typing the URL.
  - The customer session cookie was `SameSite=Strict`, so the cross-site
    return from Stripe Checkout arrived without it: customers were bounced to
    the login page seconds after paying, subscription already created. Now
    `Lax` (still withheld on cross-site POST, which is where CSRF lives); the
    admin cookie stays Strict.
  - "Réclamer" stayed on profiles that already had an approved owner, where
    the endpoint can only answer ErrAlreadyClaimed.

* Stripe PRODUCTION configured (2026-08-27): live product "Mise en avant"
  with a monthly price on `acct_1U5NcNLbym0EdjAY`, webhook endpoint
  `https://congopro.com/webhooks/stripe`, and all three keys pushed to the
  server's EnvironmentFile via the new `make prod-secrets-set` (hidden input,
  value over stdin — never in argv, history or `ps`). Verified: service
  active, `Stripe enabled (price price_…)` at boot, unsigned webhook POST
  rejected with 400.
  Two traps worth remembering. The Stripe CLI here is logged into a
  **sandbox** (`acct_1U5NcX…`), which can never do livemode — production work
  happens in the Dashboard, not the CLI. And the dashboard's product page
  shows "ID du produit" (`prod_…`) far more prominently than the price id;
  pasting it into `STRIPE_PRICE_ID` boots fine and only fails when a customer
  clicks Checkout. `ValidateStripe` now rejects it at boot (prefix checks on
  all three keys, values redacted in the error).

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
  tested-restore path (`dev-db-restore-test`).
* Logo, favicon pipeline, social banners, OG images.
