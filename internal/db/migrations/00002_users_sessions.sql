-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email          text NOT NULL,
    name           text NOT NULL DEFAULT '',
    password_hash  text NOT NULL,
    -- Set once the user has scanned the enrollment QR code; NULL means TOTP
    -- isn't configured yet and the user cannot log in (see internal/auth).
    totp_secret    text,
    role           text NOT NULL
                       CHECK (role IN ('super_admin', 'ads_rep', 'data_editor', 'support')),
    status         text NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'disabled')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    last_login_at  timestamptz
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX users_email_key ON users (lower(email));
-- +goose StatementEnd

-- +goose StatementBegin
-- The token itself is the primary key (opaque, high-entropy, sent as a cookie
-- value) — a session lookup is a single indexed point read, and revoking a
-- session is just deleting its row. No separate "session id" needed.
CREATE TABLE sessions (
    token       text PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_agent  text NOT NULL DEFAULT '',
    ip          text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE sessions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd
