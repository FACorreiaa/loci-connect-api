-- +goose Up
-- +goose StatementBegin
-- Vector values are too large for B-tree index entries and similarity lookups
-- already use the dedicated HNSW indexes created in migration 0017. Keep the
-- scalar filters independently indexable so Postgres can combine both paths.
DROP INDEX IF EXISTS idx_poi_category_embedding;
DROP INDEX IF EXISTS idx_cities_country_embedding;

CREATE INDEX IF NOT EXISTS idx_poi_type_with_embedding
    ON points_of_interest (poi_type)
    WHERE embedding IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_cities_country_with_embedding
    ON cities (country)
    WHERE embedding IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_poi_type_with_embedding;
DROP INDEX IF EXISTS idx_cities_country_with_embedding;
-- The invalid vector-bearing B-tree indexes are intentionally not restored.
-- +goose StatementEnd
