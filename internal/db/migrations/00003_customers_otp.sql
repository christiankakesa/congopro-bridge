-- +goose Up
-- +goose StatementBegin
-- Customer accounts: passwordless, the verified email IS the identity.
-- Deliberately separate from users/sessions (staff) — different trust model,
-- no shared code paths. Auto-created only after a successful OTP verification,
-- never at code-request time, so bots spamming codes cannot create junk rows.
CREATE TABLE customers (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL,
    name          text NOT NULL DEFAULT '',
    status        text NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active', 'disabled')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX customers_email_key ON customers (lower(email));
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE customer_sessions (
    token        text PRIMARY KEY,
    customer_id  uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_agent   text NOT NULL DEFAULT '',
    ip           text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX customer_sessions_customer_id_idx ON customer_sessions (customer_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- One-time codes keyed by email, not customer_id: a code can be requested
-- before the account exists (account creation happens at verification).
-- code_hash is SHA-256 hex of the 6-digit code — short-lived, attempt-capped,
-- not a password, so no salt/argon2 is warranted.
CREATE TABLE otp_codes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email       text NOT NULL,
    code_hash   text NOT NULL,
    expires_at  timestamptz NOT NULL,
    attempts    int NOT NULL DEFAULT 0,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX otp_codes_email_idx ON otp_codes (email);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE otp_codes;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE customer_sessions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE customers;
-- +goose StatementEnd
