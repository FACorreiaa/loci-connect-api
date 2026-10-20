-- +goose Up
-- +goose StatementBegin
-- Taste match calibration. A raw cosine similarity between a place and a
-- traveller's taste vector says little on its own: every vector sits in its own
-- band of similarities. RecomputeUser stores where this traveller's vector puts
-- the 10th and 90th percentile of places, so a match can be shown as "where this
-- place falls among places in general, for you". NULL until the next recompute,
-- and no match is shown while it is NULL.
ALTER TABLE user_preference_vectors
    ADD COLUMN match_p10 REAL,
    ADD COLUMN match_p90 REAL;

-- A traveller's personal ranking of places they have been, one list per city.
-- A verdict (1 Loved, 2 Fine, 3 Not for me) picks the bucket; position orders
-- the bucket, 0 being the best. search_lo/search_hi hold the open range of a
-- binary insertion that is still waiting on comparisons; both are NULL once the
-- place is placed.
CREATE TABLE user_poi_rankings (
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    poi_id     TEXT        NOT NULL,
    poi_name   TEXT        NOT NULL,
    category   TEXT        NOT NULL DEFAULT '',
    city_key   TEXT        NOT NULL,
    city_name  TEXT        NOT NULL,
    verdict    SMALLINT    NOT NULL CHECK (verdict BETWEEN 1 AND 3),
    position   INT         NOT NULL,
    search_lo  INT,
    search_hi  INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, poi_id)
);

CREATE INDEX idx_user_poi_rankings_bucket
    ON user_poi_rankings (user_id, city_key, verdict, position);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_poi_rankings;
ALTER TABLE user_preference_vectors
    DROP COLUMN IF EXISTS match_p90,
    DROP COLUMN IF EXISTS match_p10;
-- +goose StatementEnd
