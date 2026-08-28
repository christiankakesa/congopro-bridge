-- +goose Up
-- +goose StatementBegin
-- Telegram ↔ staff identity mapping for bot v2 quick actions: a claim
-- approved from the Telegram chat is recorded under resolved_by like any
-- other, so the tap must resolve to a real users row. NULL means "not
-- linked" — the bot answers an unlinked tap with the person's Telegram id
-- so an admin can link it via `congopro-bridge -link-telegram`.
ALTER TABLE users ADD COLUMN telegram_user_id bigint;
-- +goose StatementEnd

-- +goose StatementBegin
-- One staff account per Telegram identity; NULLs (unlinked) stay free.
CREATE UNIQUE INDEX users_telegram_user_id_key
    ON users (telegram_user_id)
    WHERE telegram_user_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX users_telegram_user_id_key;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users DROP COLUMN telegram_user_id;
-- +goose StatementEnd
