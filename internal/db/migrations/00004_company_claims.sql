-- +goose Up
-- +goose StatementBegin
-- The approval's durable effect: this company belongs to that customer.
-- SET NULL so deleting a customer un-claims the company instead of blocking.
ALTER TABLE companies ADD COLUMN claimed_by_customer_id uuid REFERENCES customers(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- One claim row per submission. claimant_email is a snapshot at submission
-- time (the customer may change... it can't — email IS the identity — but
-- the historical record stays explicit anyway). Partial unique indexes
-- arbitrate concurrency: one PENDING claim per company, one APPROVED claim
-- per company. Rejected claims never block a new attempt.
CREATE TABLE company_claims (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id     text NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    customer_id    uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    claimant_email text NOT NULL,
    contact_phone  text NOT NULL DEFAULT '',
    relationship   text NOT NULL
                   CHECK (relationship IN ('owner', 'manager', 'employee', 'other')),
    evidence       text NOT NULL,
    status         text NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'approved', 'rejected')),
    admin_note     text NOT NULL DEFAULT '',
    resolved_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    resolved_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX company_claims_one_pending_per_company
    ON company_claims (company_id) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX company_claims_one_approved_per_company
    ON company_claims (company_id) WHERE status = 'approved';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX company_claims_customer_idx ON company_claims (customer_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX company_claims_status_created_idx ON company_claims (status, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE company_claims;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE companies DROP COLUMN claimed_by_customer_id;
-- +goose StatementEnd
