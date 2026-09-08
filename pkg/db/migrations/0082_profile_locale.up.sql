-- +goose Up
-- +goose StatementBegin

-- Where the user is, what they measure in, and what they pay in.
--
-- All three are on the account rather than in the browser, and timezone is the
-- one that forces it: a scheduled notification has to know whose evening it is
-- at a moment when no browser is running. Units and currency follow it here
-- because splitting a person's locale across two stores means answering "what
-- does this user see?" in two places.
--
-- Nullable rather than defaulted. NULL means "never chosen", which is a
-- different thing from "chose metric", and the difference matters: the client
-- may fill an unset timezone from the browser's own guess, but it must not
-- silently overwrite one somebody set deliberately.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS timezone TEXT,
    ADD COLUMN IF NOT EXISTS units    TEXT,
    ADD COLUMN IF NOT EXISTS currency TEXT;

-- Constrained where the set is closed and ours. Units is two values, and
-- currency is a three-letter ISO 4217 code — checking the shape rather than the
-- list, because the list changes and a rejected save is worse than an unusual
-- currency.
ALTER TABLE users
    ADD CONSTRAINT users_units_known CHECK (
        units IS NULL OR units IN ('metric', 'imperial')
    ),
    ADD CONSTRAINT users_currency_shape CHECK (
        currency IS NULL OR currency ~ '^[A-Z]{3}$'
    );

-- Timezone is deliberately unconstrained here. The authority on zone names is
-- the tzdata the server runs with, which is where the application validates it;
-- a CHECK listing zone names would go stale the first time a country changed
-- its clocks and would then reject a name the operating system considers valid.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_currency_shape;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_units_known;
ALTER TABLE users
    DROP COLUMN IF EXISTS currency,
    DROP COLUMN IF EXISTS units,
    DROP COLUMN IF EXISTS timezone;
-- +goose StatementEnd
