# Congopro Bridge — Backend Architecture Proposal

Status: **actively used as the roadmap.** Phase 1 (Foundation) has shipped:
Postgres + PostGIS schema, migration off the embedded JSON, staff auth with
TOTP + roles, admin company CRUD, and the backup/restore tooling. Phases 2–4
are still open — this document is now the build order for them. Where a
decision only you can make is blocking, it's listed under "Open questions"
instead of guessed.

## How this relates to `docs/VISION.md`

That document (NATS JetStream ingestion, rqlite Raft cluster, Kubernetes, S3/MinIO, 13M companies) is the investor-pitch vision of the platform at a much later stage. When this proposal was written, the app was one Go binary with 1,500 companies embedded as a static JSON file, no database, no auth, no CMS, and ads configured through a YAML file you edit by hand — building toward *that* architecture then would have meant solving distributed-systems problems (Raft consensus, K8s cluster ops, message-broker backpressure) before solving the problems that actually existed.

This proposal was the "what do we actually build first" version. It has since
been renamed `VISION.md`, marked as aspirational, and superseded as the
working plan by this document plus [ARCHITECTURE.md](ARCHITECTURE.md), which
describes the system as shipped.

## Design principles

1. **Build for the scale you have, not the scale you're pitching.** Hundreds to low thousands of companies, a handful of staff, an ads business you're about to start selling manually. Revisit every piece of this in 12 months, not before.
2. **One source of truth.** A relational database holds the real data. Meilisearch stays exactly what it already is — a fast, disposable, rebuildable search index synced *from* the database, never written to directly. No dual-write consistency problems.
3. **No new moving parts without an operational reason.** No Kubernetes, no message broker, no multi-node database cluster — until team size, load, or a specific incident tells you that you need one. A single Go binary (your existing pattern) plus a single Postgres instance will comfortably outlast most guesses about when you'll "need to scale."
4. **Boring technology where money and trust are involved.** Ads sales, payments, and business identity data are the parts of this system where a mistake costs you a customer relationship or a legal headache. That's exactly where you want the most boring, best-documented, easiest-to-back-up tools available — not the newest ones.

## What I'd drop or swap from your list, and why

- **rqlite → PostgreSQL (+ PostGIS).** rqlite is SQLite replicated over Raft — clever, but you inherit SQLite's limits (no native geo types, weak concurrent-write story, no JSONB) *and* rqlite's much less mature backup/restore/monitoring tooling compared to Postgres, for a consistency guarantee you don't need at one-node scale. PostGIS directly solves "GPS location for all entries" — real geo columns, real distance/radius queries, real indexes — instead of you hand-rolling lat/lng math. If you outgrow one Postgres instance, the upgrade path (read replicas, then managed HA) is well-trodden.
- **PocketBase cluster → drop entirely.** PocketBase is explicitly a single-node, embedded-SQLite tool. There is no supported clustering story — "PocketBase cluster" isn't a real deployment pattern, it's a feature request people keep asking the maintainer for. It's great for a weekend prototype; it's not a foundation for a multi-country platform.
- **NATS JetStream ingestion pipeline → drop for now.** This solves "a million rows arrive at once and workers can't keep up" — a problem you don't have with thousands of rows. A Go import job writing into Postgres inside a transaction (you already have `cmd/cleanr`, `cmd/geocoder` — they'd point at Postgres rows instead of a JSON file) is enough until you're doing large, frequent bulk imports. Even then, a Postgres queue table usually gets you further than people expect before you need a real broker.
- **Kubernetes → drop for now.** Adds a second full-time operational surface (the cluster itself needs care) for an app that fits comfortably on the VPS you already deploy to via systemd + Traefik. Revisit if you have multiple services that need independent scaling and a team to run them — not before.
- **S3/MinIO for media → defer, not drop.** Real need once the CMS supports uploading company logos/photos at volume. Not urgent while images are static assets baked into the binary. Build it when "upload a logo" becomes a real CMS feature, not speculatively now.
- **Telegram as the ticketing system of record → narrow its role.** The Bot API is genuinely good for what Africa-first, mobile-first access patterns need: notifications, swipe-to-approve merges, quick replies. It's a poor system of record — no proper queue/assignment/SLA tracking/reporting, and your data lives in Telegram's platform instead of yours. Recommendation: a real (small) `disputes`/`tickets` table with an admin queue in your own CMS, and a Telegram bot as the notification/quick-action front door on top of it — not instead of it. Same pattern for "data management for the team": Telegram can *notify and deep-link into* the admin CMS, it shouldn't *be* the CMS.

## Proposed architecture

```
                    ┌─────────────────────────┐
                    │   PostgreSQL + PostGIS   │   ← single source of truth
                    │  companies, users, ads,  │
                    │  customers, disputes,    │
                    │  audit log               │
                    └────────────┬─────────────┘
                                 │ sync (existing pattern: LoadAndIndex)
                                 ▼
                    ┌─────────────────────────┐
                    │       Meilisearch        │   ← disposable, rebuildable
                    └─────────────────────────┘

        ┌───────────────────────┴───────────────────────┐
        │              Congopro Bridge (Go binary)        │
        │  public site (existing)   │   /admin CMS (new)  │
        │  templ + htmx             │   templ + htmx      │
        └───────────────────────┬───────────────────────┘
                                 │
                    ┌────────────────────────┐
                    │   Telegram bot (later)   │  notifications + quick actions
                    └────────────────────────┘
```

Concretely, this is your existing app plus a database and an `/admin` area — not a new system.

### Database

Single PostgreSQL 16 instance with the PostGIS extension. Runs on the same VPS to start (or an adjacent small managed instance if you'd rather not operate it yourself — see open questions). Core tables:

- `companies` — the existing `Company` struct's fields, plus `official_id` (RCCM number for DRC — see "Company identity" below), `geom` (PostGIS `geography(Point)` instead of hand-rolled lat/lon math), `status` (draft/published/disputed), `created_by`/`updated_by` for audit.
- `users` — staff and internal accounts: sales reps, data editors, support, super admin.
- `roles` — a small fixed enum to start (`super_admin`, `ads_rep`, `data_editor`, `support`), not a full permissions-matrix table. Add real granularity only when you actually have enough staff for "sales rep can't see support tickets" to matter.
- `customers` — external accounts: business owners claiming a listing, or buyers of promoted placement. Separate from `users` because the trust model and auth flow are different (see Authentication).
- `ads` — replaces `ads.yml`. Adds `sold_by_user_id` (nullable — null for house ads), `customer_id`, `price`, `status` (draft/active/expired), so "who sold which ad" is a query, not a memory.
- `company_claims` — the dispute/claim system: `company_id`, `claimant` info, evidence, `status`, `resolved_by`, `resolved_at`.
- `audit_log` — append-only, `who did what to which row when`. Cheap to add now, painful to retrofit later, and you'll want it the first time two staff members edit the same company.

`internal/data/engine.go`'s `LoadAndIndex` already does "load companies, push to Meilisearch" — the change is *where* it loads from (Postgres query instead of the embedded JSON), not the overall shape of your indexing flow. Your existing dedup/normalization tools (`cmd/cleanr`'s activity/city normalizers, link validator) become jobs that run against the database instead of a JSON file — same logic, different data source.

### Authentication — two systems, because the trust models differ

**Staff** (ad sales reps, data editors, support) are a small, known set of people you personally onboard. Don't build for internet-scale auth here: session-based login with either email OTP or password + TOTP (Google Authenticator-style) is plenty, and email deliverability risk barely matters at this volume.

**Customers** (business owners, promoted-listing buyers) need low-friction, scalable login — email OTP is the right call, exactly as you proposed. The part you're missing isn't the auth flow, it's *sending the email*:

- **Don't self-host SMTP.** Outbound mail from a fresh server/domain has terrible deliverability without IP warm-up, and OTP emails landing in spam defeats the entire point.
- **OVH's mailbox hosting (MX Plan/Exchange) is for receiving mail into a human inbox**, not for an app sending transactional email at a domain's reputation. It's the wrong tool for this even if it's "free with the domain."
- **Use a transactional email API instead.** [Resend](https://resend.com) (3,000 emails/month free), [Brevo](https://www.brevo.com) (300/day free), and Amazon SES (extremely cheap, ~$0.10/1,000, but starts in a sending sandbox you have to request removal from) are all real options that comfortably cover OTP volume at your current and near-future scale, handle SPF/DKIM/DMARC for you, and give you delivery/bounce visibility you'd never get self-hosting. This is a small decision I'd like your input on — see open questions.

### Company identity — official records, not a new protocol

You mentioned wanting to know "what protocol is used to store official company information to be compatible with." The honest answer for DRC specifically: there isn't a heavyweight international standard you need to adopt (no need for GS1/GLN or LEI at this stage) — the practical one is the **RCCM number** (Registre du Commerce et du Crédit Mobilier), which is already DRC's real company registration identifier, and `docs/PARTNERSHIP.md` already lists RCCM and ANAPI as data partners. Add `official_id` + `official_id_country` columns now so future countries can each have their own identifier scheme (RCCM for DRC, whatever the equivalent is elsewhere) without a schema change later. On the interoperability side, you're already emitting schema.org `LocalBusiness` markup in the frontend — that *is* the right external format for search engines/AI assistants to understand your listings; nothing heavier is needed.

### Multi-country frontends

"Each country gets its own frontend with filtered companies" doesn't require separate databases per country — it requires a `country` column (which you already have) and a Meilisearch filter on it. One Postgres instance, one Meilisearch instance, N thin frontend configurations that each just search a filtered slice. Stand up per-country infrastructure only if a specific country's data-residency law forces it, not by default.

### Promoted listings / paid subscriptions

Model as a `customers` account with a `subscription` (plan, status, renewal date) tied to an `ads` row with `placement=promoted`. Payment processor is a real open question, not a technical one — Stripe has the best DX but patchy African country support; Flutterwave/Paystack are built for exactly the cross-border-into-Africa pattern you're describing. I'd rather you tell me which side of that transaction (the business paying) is more likely to be paying from than guess.

## The MCP-server-for-BI idea

Worth pursuing, but as a **later phase**, not a v1 requirement. A paid MCP server (or plain API) that lets a customer point their own BI tool or LLM agent at their filtered slice of the data is a genuinely good monetization angle *once you have structured data, auth, and billing already working* — it's a thin, valuable layer on top of the CMS backend, not a replacement for building the CMS backend. Building it first would mean building a nice API onto nothing. I'd sequence this as Phase 4 below.

## Phased rollout

1. **Foundation** — ✅ **shipped** (Postgres + PostGIS schema; company data migrated off the embedded JSON; staff auth + roles; admin CRUD for companies). One leftover: pointing the merge/dedupe tooling (`cmd/cleanr`) at Postgres rows instead of the legacy JSON — tracked in `TODO.md`.
2. **Trust & revenue plumbing** — customer email-OTP auth; company claim/dispute workflow with an admin queue; ads CMS with sales-rep attribution. *(Email provider decided — see Decisions below.)*
3. **Monetize promoted listings** — subscriptions + payment integration; Telegram bot as the notification/quick-action layer on top of the CMS (dispute alerts, merge approvals).
4. **Opportunistic** — MCP/BI product, further multi-country tooling, geo-dedup automation.

## Decisions (recorded 2026-08-17)

1. **Email provider — DECIDED: OVH EmailPro SMTP** (STARTTLS, AUTH PLAIN).
   The sender lives in `internal/mail`. Deliverability is the one risk the
   original recommendation warned about, so it comes with obligations:
   SPF, DKIM and DMARC records for the sending domain, and watching bounces
   once OTP volume starts. If OTPs land in spam at scale, the decision to
   revisit is the provider, not the code.
2. **Payment processor — DECIDED: Stripe** (account creation pending).
   Webhook + event integration is deferred until the account exists — see
   the Stripe checklist in `TODO.md`.
3. **Scale — DECIDED: ~10 staff over 12 months** (1–2 hires/month). The
   fixed role enum in `users` stays; no permissions matrix. Plan the admin
   UX for a team that grows steadily: onboarding, audit log, "who changed
   this" everywhere.

## Open questions (remaining)

1. **Postgres hosting** — same VPS as the app (cheapest, you already operate it), or a small managed instance (less ops burden, small monthly cost)?
