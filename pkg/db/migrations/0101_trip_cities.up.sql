-- +goose Up
-- +goose StatementBegin
-- The cities of a multi-city trip, in visiting order, each linked to the chat
-- session its places were generated in. trip_days already say which city a
-- day is spent in; this is the list a UI walks to show "Lisbon · 3n → Porto · 2n"
-- and to reopen one city's hotels, restaurants and activities. city_id has no
-- foreign key, like trip_days.city_id (0066).
CREATE TABLE IF NOT EXISTS trip_cities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id     UUID NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
    order_index INT  NOT NULL,
    city_name   TEXT NOT NULL,
    city_id     UUID NULL,
    session_id  UUID NULL,
    nights      INT  NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (trip_id, order_index)
);
CREATE INDEX IF NOT EXISTS idx_trip_cities_trip ON trip_cities (trip_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_cities;
-- +goose StatementEnd
