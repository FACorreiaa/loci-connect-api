-- +goose Up
-- +goose StatementBegin

-- Pictures for a place, one row per image.
--
-- Why a table and not a TEXT[] column on points_of_interest: the images come
-- from Wikimedia Commons, whose licences (CC BY-SA and friends) require the
-- author and the licence to be shown wherever the picture is. An array of URLs
-- cannot carry that, and an image displayed without its attribution is a
-- licence breach rather than a cosmetic gap. One row per image keeps the credit
-- attached to the thing it credits.
--
-- points_of_interest has no image column at all today; GetPOIByID fakes one
-- with '{}'::text[] so the struct has something to fill. This table is what
-- that read should join to instead.
CREATE TABLE IF NOT EXISTS poi_images (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    poi_id UUID NOT NULL REFERENCES points_of_interest(id) ON DELETE CASCADE,

    url TEXT NOT NULL,

    -- Where it came from, so a source can be re-fetched or withdrawn wholesale
    -- if its terms change. 'wikimedia' is the only producer today.
    source TEXT NOT NULL DEFAULT 'wikimedia',

    -- Both are required to display the image legally. Empty strings are not
    -- allowed: an image we cannot credit is one we must not show, so it should
    -- never have been stored.
    licence TEXT NOT NULL,
    attribution TEXT NOT NULL,

    -- Page describing the file, for the credit line to link to.
    source_page_url TEXT NOT NULL DEFAULT '',

    -- Display order within one POI. Lowest first.
    position INT NOT NULL DEFAULT 0,

    fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT poi_images_licence_not_blank CHECK (btrim(licence) <> ''),
    CONSTRAINT poi_images_attribution_not_blank CHECK (btrim(attribution) <> ''),
    CONSTRAINT poi_images_url_not_blank CHECK (btrim(url) <> '')
);

-- The same file must not be attached to one place twice, which is what a
-- re-run of the backfill would otherwise do.
CREATE UNIQUE INDEX IF NOT EXISTS idx_poi_images_poi_url
    ON poi_images (poi_id, url);

-- The read path: every image for a place, in display order.
CREATE INDEX IF NOT EXISTS idx_poi_images_poi_position
    ON poi_images (poi_id, position);

-- The backfill's own question, asked once per batch: which places have no
-- picture yet? Answered by an anti-join against this index.
CREATE INDEX IF NOT EXISTS idx_poi_images_poi
    ON poi_images (poi_id);

-- The nightly image backfill is a job like the embedding one, and
-- enrichment_runs (migration 0075) is where a job says whether it ran, did
-- nothing, or died. Its kind is constrained, so a new job needs the constraint
-- widened or every run record it writes is rejected.
ALTER TABLE enrichment_runs DROP CONSTRAINT IF EXISTS enrichment_runs_kind_check;
ALTER TABLE enrichment_runs ADD CONSTRAINT enrichment_runs_kind_check
    CHECK (kind IN ('poi_embeddings', 'city_embeddings', 'preference_rerank', 'poi_images'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS poi_images;

-- Put the kind constraint back as it was, or a rollback leaves the column
-- accepting a value nothing downstream expects.
ALTER TABLE enrichment_runs DROP CONSTRAINT IF EXISTS enrichment_runs_kind_check;
ALTER TABLE enrichment_runs ADD CONSTRAINT enrichment_runs_kind_check
    CHECK (kind IN ('poi_embeddings', 'city_embeddings', 'preference_rerank'));
-- +goose StatementEnd
