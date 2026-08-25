-- +goose Up
-- +goose StatementBegin
-- Promoted listings: a customer with an approved claim pays a Stripe
-- subscription to promote their company. The promotions row is the local
-- source of truth for everything user-visible (badges, eligibility);
-- Stripe webhooks drive the lifecycle. Idempotent by Stripe ids — webhooks
-- replay.
CREATE TABLE promotions (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id             text NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    customer_id            uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    stripe_customer_id     text NOT NULL DEFAULT '',
    stripe_subscription_id text NOT NULL DEFAULT '',
    stripe_session_id      text NOT NULL DEFAULT '',
    status                 text NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending', 'active', 'past_due', 'canceled')),
    current_period_end     timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- One live promotion per company: pending (checkout open) counts as live so
-- a second checkout can't race the first; canceled frees the slot.
CREATE UNIQUE INDEX promotions_one_live_per_company
    ON promotions (company_id)
    WHERE status IN ('pending', 'active', 'past_due');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX promotions_stripe_sub
    ON promotions (stripe_subscription_id)
    WHERE stripe_subscription_id <> '';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX promotions_customer_idx ON promotions (customer_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Backs the badge lookups: "which of these companies are promoted now".
CREATE INDEX promotions_company_active_idx
    ON promotions (company_id)
    WHERE status IN ('active', 'past_due');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE promotions;
-- +goose StatementEnd
