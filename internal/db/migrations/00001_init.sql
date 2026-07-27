-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS postgis;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE companies (
    -- text, not uuid: preserves the existing Mongo-derived hex IDs (e.g.
    -- "5001fe2ad96468020000030a") when the current embedded-JSON dataset is
    -- imported here, and stays a free choice (uuid string or otherwise) for
    -- anything created after that.
    id                   text PRIMARY KEY,
    name                 text NOT NULL,
    name_seo             text NOT NULL DEFAULT '',
    activity             text NOT NULL DEFAULT '',
    city                 text NOT NULL DEFAULT '',
    country              text NOT NULL DEFAULT '',
    description          text NOT NULL DEFAULT '',
    slogan               text NOT NULL DEFAULT '',
    website              text NOT NULL DEFAULT '',
    email                text NOT NULL DEFAULT '',
    phone                text NOT NULL DEFAULT '',
    address_line_1       text NOT NULL DEFAULT '',
    address_line_2       text NOT NULL DEFAULT '',
    twitter              text NOT NULL DEFAULT '',
    facebook             text NOT NULL DEFAULT '',
    linkedin             text NOT NULL DEFAULT '',
    instagram            text NOT NULL DEFAULT '',
    tiktok               text NOT NULL DEFAULT '',
    whatsapp             text NOT NULL DEFAULT '',
    youtube              text NOT NULL DEFAULT '',
    -- Official business-registry identifier (e.g. RCCM number for DRC).
    -- official_id_country lets each country keep its own identifier scheme
    -- without a schema change later — see docs/BACKEND_PROPOSAL.md.
    official_id          text NOT NULL DEFAULT '',
    official_id_country  text NOT NULL DEFAULT '',
    stats_show           integer NOT NULL DEFAULT 0,
    location             geography(Point, 4326),
    status               text NOT NULL DEFAULT 'published'
                             CHECK (status IN ('draft', 'published', 'disputed')),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX companies_name_seo_key ON companies (name_seo) WHERE name_seo <> '';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX companies_country_idx ON companies (country);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX companies_location_gix ON companies USING GIST (location);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE companies;
-- +goose StatementEnd
