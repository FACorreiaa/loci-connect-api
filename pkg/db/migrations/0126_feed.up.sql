-- +goose Up
-- +goose StatementBegin

-- The following feed: what people did that their followers see. Written once
-- per thing (a new city, a finished trip, a trip shared, the day's best
-- achievement) and read fan-out: a viewer's feed is the events of the people
-- they follow, so unfollowing or blocking takes them out at once.
--
-- kind: 1 achievement unlocked, 2 new city, 3 trip finished, 4 trip shared.
-- ref_key makes each event happen once per actor: "city:<key>", "trip:<id>",
-- "achievements:<local date>". trip_id ties trip events to the trip, so the
-- feed re-checks its visibility on read and the event goes with the trip.
CREATE TABLE IF NOT EXISTS feed_events (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind              SMALLINT NOT NULL CHECK (kind BETWEEN 1 AND 4),
    ref_key           TEXT NOT NULL,
    payload           JSONB NOT NULL DEFAULT '{}'::jsonb,
    trip_id           UUID REFERENCES trips (id) ON DELETE CASCADE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Set when the actor was told about the first cheer: one push per event,
    -- however often people cheer and take it back.
    cheer_notified_at TIMESTAMPTZ,
    UNIQUE (actor_id, kind, ref_key)
);

CREATE INDEX IF NOT EXISTS feed_events_actor_created_idx
    ON feed_events (actor_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS feed_events_trip_idx
    ON feed_events (trip_id) WHERE trip_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS cheers (
    event_id   UUID NOT NULL REFERENCES feed_events (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (event_id, user_id)
);

CREATE INDEX IF NOT EXISTS cheers_user_idx ON cheers (user_id);

-- Someone cheered one of the account's feed items. On unless turned off.
ALTER TABLE notification_settings
    ADD COLUMN IF NOT EXISTS cheers BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_settings DROP COLUMN IF EXISTS cheers;
DROP TABLE IF EXISTS cheers;
DROP TABLE IF EXISTS feed_events;
-- +goose StatementEnd
