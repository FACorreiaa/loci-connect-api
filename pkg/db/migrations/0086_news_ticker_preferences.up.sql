-- +goose Up
-- +goose StatementBegin

-- The per-user switch for the desk's breaking-news strip.
--
-- Its own table rather than a column on users: the strip is an optional
-- nicety, and "no row" meaning "on" keeps every existing account on the
-- default without a backfill. Nothing else about the ticker is stored — the
-- headlines live in the shared feed aggregator, and the countries they are
-- selected for are derived from the profile, trips and travel history at
-- read time.
CREATE TABLE IF NOT EXISTS news_ticker_preferences (
    user_id    UUID        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    enabled    BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS news_ticker_preferences;
-- +goose StatementEnd
