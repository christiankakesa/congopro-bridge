-- +goose Up
-- +goose StatementBegin
-- Ads CMS: campaigns live in Postgres, editable in /admin without a
-- redeploy. Creative fields mirror the legacy ads.yml 1:1 (period dates
-- NULLable = unbounded, end inclusive through end of day, enforced in Go
-- like before); status replaces the YAML active bool; sold_by/customer/price
-- carry the sales attribution (house ads leave them NULL).
CREATE TABLE ads (
    id               text PRIMARY KEY,
    title            text NOT NULL,
    description      text NOT NULL DEFAULT '',
    url              text NOT NULL,
    display_url      text NOT NULL DEFAULT '',
    label            text NOT NULL DEFAULT '',
    color            text NOT NULL DEFAULT '',
    period_start     date,
    period_end       date,
    weight           int NOT NULL DEFAULT 1 CHECK (weight >= 0),
    placement        text NOT NULL DEFAULT ''
                     CHECK (placement IN ('', 'homepage', 'search_results')),
    keywords         text[] NOT NULL DEFAULT '{}',
    status           text NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft', 'active', 'paused', 'expired')),
    sold_by_user_id  uuid REFERENCES users(id) ON DELETE SET NULL,
    customer_id      uuid REFERENCES customers(id) ON DELETE SET NULL,
    price_cents      int CHECK (price_cents IS NULL OR price_cents >= 0),
    currency         text NOT NULL DEFAULT 'USD',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX ads_status_idx ON ads (status);
-- +goose StatementEnd

-- +goose StatementBegin
-- Single-row global settings (id = 1), seeded by -import-ads. The master
-- switch is the no-redeploy kill switch for the whole ad system.
CREATE TABLE ads_settings (
    id           int PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    active       bool NOT NULL DEFAULT true,
    rotation_sec int NOT NULL DEFAULT 8 CHECK (rotation_sec > 0),
    max_per_page int NOT NULL DEFAULT 2 CHECK (max_per_page BETWEEN 1 AND 3)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE ads_settings;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE ads;
-- +goose StatementEnd
