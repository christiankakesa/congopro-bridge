# TODO

## Now

* Add a `make dev` local loop (Kora VPN-style): one target that starts deps
  (`db-up`), applies migrations, regenerates templ/CSS on change, and runs the
  binary. The production deploy side already exists (`make deploy`); local
  iteration is still multi-step.
* AI answer fallback: when the model has no grounded answer for a query, have
  it produce a useful insight from the search results themselves instead of a
  dead end.

## Next (Phase 2 — see [BACKEND_PROPOSAL.md](BACKEND_PROPOSAL.md))

* Customer accounts (email OTP) — needs the email-provider decision.
* Company claim/dispute workflow + admin queue.
* Ads CMS replacing `ads.yml`, with sales-rep attribution.
* Telegram bot as notification/quick-action layer on top of the CMS.

## Later

* Research: paid MCP server / API for B2B customers to point their own BI
  tools at their filtered data slice (Phase 4 in BACKEND_PROPOSAL.md — only
  worth building once auth + billing exist).
* Merge/dedupe tooling (`cmd/cleanr`) pointed at Postgres rows instead of the
  legacy JSON file.
* Admin: company delete/archive and user management UI (roles exist in the
  schema; only company CRUD is exposed so far).

## Decisions needed (blocking parts of Phase 2)

From BACKEND_PROPOSAL.md §Open questions:

* Email provider for customer OTP (Resend / Brevo / SES)?
* Payment processor for promoted listings (Stripe vs Flutterwave/Paystack)?
* Expected company/staff scale over 6–12 months?

## Reference

* PageSpeed (mobile): [report 1](https://pagespeed.web.dev/analysis/https-congopro-com/loz8e4kjae?hl=fr&form_factor=mobile),
  [report 2](https://pagespeed.web.dev/analysis/https-congopro-com/1w6laz73ws?utm_source=search_console&form_factor=mobile&hl=fr)

## Done (highlights — full history in git)

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
