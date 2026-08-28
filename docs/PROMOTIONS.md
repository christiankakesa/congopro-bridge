# Promoted listings (Stripe)

A customer with an **approved claim** on a company pays a monthly Stripe
subscription to promote it. The full trust chain from Phase 2 does the fraud
prevention: verified email (OTP login) → approved claim (staff-verified
ownership) → eligible to promote.

```
customer ──claim──► staff approves ──ownership──► /account/promote
                                                        │
                                        Stripe Checkout (hosted, subscription)
                                                        │
              POST /webhooks/stripe ◄──────────────────┘
              (signature-verified, idempotent)
                    │
        promotions table (source of truth for badges/eligibility)
                    │
        "Promu" badge: company profile + search result rows
```

Money never touches our servers: Stripe hosts the payment page, the webhook
is the lifecycle source of truth, and the local `promotions` table drives
everything user-visible.

## State model

| Status | Meaning | Badge | Can re-promote |
|---|---|---|---|
| `pending` | Checkout opened, not completed (auto-swept after 24h) | no | blocked while pending |
| `active` | Subscription live | **Promu** | blocked |
| `past_due` | Payment failed (grace — Stripe retries) | **Promu** | blocked |
| `canceled` | Subscription ended or checkout abandoned | no | yes |

One live promotion per company — enforced by a partial unique index, so
concurrent checkouts can't race.

## Webhook events

`POST /webhooks/stripe` handles exactly three event types (send no others —
they'd be acked and ignored):

- `checkout.session.completed` — links the Stripe customer + subscription to
  the pending promotion row and activates it.
- `customer.subscription.updated` — syncs status (`active`/`trialing` →
  active, `past_due`/`unpaid` → past_due, anything else → canceled) and the
  renewal date.
- `customer.subscription.deleted` — cancels, freeing the company's slot.

Everything is idempotent by Stripe ids — webhook replays are harmless.
Signature verification uses `STRIPE_WEBHOOK_SECRET` with the recommended
5-minute tolerance; bad signatures get a 400, database failures get a 5xx so
Stripe retries.

## Configuration

All-or-nothing like the SMTP block: **any** of the three keys set requires
all three; none set disables the promote endpoints cleanly (503).

| Key | Value |
|---|---|
| `STRIPE_SECRET_KEY` | `sk_test_…` (test) / `sk_live_…` (prod) |
| `STRIPE_PRICE_ID` | the monthly recurring Price of the "Mise en avant" product |
| `STRIPE_WEBHOOK_SECRET` | from `stripe listen` (local) or the dashboard endpoint (prod) |

## Local setup

1. **Stripe dashboard (test mode)**: create Product *Mise en avant Congopro*
   with a monthly recurring Price → copy the `price_…` id.
2. **`.env`**: set the three keys (see `.env.template`). For the webhook
   secret, first run:

   ```bash
   stripe listen --forward-to localhost:8090/webhooks/stripe
   ```

   and copy the printed `whsec_…`. Keep it running while testing.
3. **Test the full flow**:
   - log in at `/account/login` (OTP — read the code in Mailpit, `make dev-mail-up`)
   - claim a company from its profile ("Réclamer"), approve in `/admin/claims`
   - `/account/promote` → *Promouvoir* → Stripe Checkout page
   - pay with `4242 4242 4242 4242`, any future expiry, any CVC
   - back on `/account/promote`: promotion active with renewal date
   - the company profile + search rows show the **Promu** badge

## Production

1. Add the three keys to `/opt/congopro-bridge/congopro-bridge.env` on the
   server (test keys first, `sk_live_…` when going live).
2. Stripe dashboard → Developers → Webhooks → **Add endpoint**:
   `https://congopro.com/webhooks/stripe`, events: the three above. Copy its
   signing secret (`whsec_…` — **different from the local one**) into
   `congopro-bridge.env`.
3. `make prod-app-restart`, then verify: `curl -sf
   https://congopro.com/webhooks/stripe -X POST` → 400 (invalid signature)
   is the healthy answer — the route exists and verifies.

## Troubleshooting

- **`webhook signature rejected` in logs** — the `Stripe-Signature` header
  doesn't match `STRIPE_WEBHOOK_SECRET`: wrong whsec (local vs prod secret
  mixed up is the usual cause), or the event wasn't sent by your endpoint.
- **Checkout opens but promotion stays `pending`** — the webhook isn't
  arriving: is `stripe listen` running (local) / does the dashboard endpoint
  exist and show deliveries (prod)?
- **Promotion active but no badge** — check `promotions` row status via SQL;
  `active`/`past_due` are the badge states.
- **401 from Stripe in logs** — `STRIPE_SECRET_KEY` empty or a test key
  against live mode.

## Ranking

Promoted companies that match a query are pinned to the top of the search
results, keeping Meilisearch's relevance order within each group. The pin is
applied Go-side in the search handler (`pinPromoted`, over the same
active/`past_due` set that drives the badge) — deliberately NOT a Meilisearch
ranking rule, so a subscription changes ranking the instant its webhook lands,
with no reindex and no interaction with the hybrid semantic scoring.

## Follow-ups (tracked in TODO.md)

- Renewal reminder emails via the existing mailer.
