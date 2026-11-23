-- +goose Up
-- Remove two extensions that block the move to PostgreSQL 19 and that nothing
-- in the application uses:
--
--   * timescaledb: no release supports PostgreSQL 19 (2.32.x covers 16-18).
--     Migration 0044 tried to make llm_interactions a hypertable and hang three
--     continuous aggregates and two policies off it, but every step sat inside
--     EXCEPTION WHEN OTHERS. The table's primary key is (id) alone, which
--     TimescaleDB rejects for a table partitioned on created_at. Replaying
--     0001-0137 on a TimescaleDB image leaves no hypertable, no continuous
--     aggregate and no job: every step after the first failed the same quiet
--     way. No Go code reads the aggregates or calls a TimescaleDB function.
--   * postgis_tiger_geocoder: PostGIS 3.7, the first release for PostgreSQL 19,
--     no longer ships it. Migration 0001 created it; nothing calls it.
--
-- If a hypertable does exist after all, this refuses to run rather than let
-- DROP ... CASCADE take its data: move the rows into a plain table first.
--
-- The server keeps shared_preload_libraries=timescaledb until this has run in
-- production. Removing the preload (platform/infra apps/loci/data/postgres.yaml)
-- and the package (postgres.Dockerfile) are separate, later changes; doing them
-- first would stop Postgres from starting while the extension is installed.

-- +goose StatementBegin
DO $$
DECLARE
    hypertable_count integer;
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        EXECUTE 'SELECT count(*) FROM timescaledb_information.hypertables'
            INTO hypertable_count;
        IF hypertable_count > 0 THEN
            RAISE EXCEPTION
                'refusing to drop timescaledb: % hypertable(s) exist. Copy their rows into plain tables first.',
                hypertable_count;
        END IF;
        DROP EXTENSION timescaledb CASCADE;
    END IF;
END $$;
-- +goose StatementEnd

-- Dropping the extension removes tiger_data and every object in tiger, but
-- leaves the tiger schema itself behind, empty. No CASCADE on the schema: if
-- anything else ever landed in it, this fails instead of deleting it.
-- fuzzystrmatch, which tiger needed, stays: the infra init script creates it
-- too, and it is harmless.
DROP EXTENSION IF EXISTS postgis_tiger_geocoder CASCADE;
DROP SCHEMA IF EXISTS tiger;
