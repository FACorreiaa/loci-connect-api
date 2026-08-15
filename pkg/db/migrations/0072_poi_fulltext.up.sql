-- +goose Up
-- +goose StatementBegin

-- A deterministic lexical lane for POI search.
--
-- Until now every POI search was either cosine distance over embeddings, an
-- unindexed ILIKE, or an LLM call. Migration 0003 created two GIN indexes over
-- to_tsvector(name) and to_tsvector(description) and nothing has ever queried
-- them — there is not one to_tsquery, ts_rank or plainto_tsquery in the Go
-- codebase. So the cost of maintaining them has been paid since day one and the
-- benefit never collected.
--
-- Embeddings are good at "somewhere quiet and romantic" and bad at "Casa
-- Batlló": a rare proper noun is a weak signal in a 768-dimension space and a
-- decisive one in an inverted index. This adds the second lane rather than
-- replacing the first; the two are fused at query time.

-- One stored column instead of an expression index, because the search path
-- needs to rank on it (ts_rank_cd over the same vector) and not merely filter.
-- Recomputed by Postgres on write; no trigger to drift out of sync.
--
-- Weights: A the name, B the classification a user might search by, C the prose.
-- ts_rank_cd's default weights make an A hit worth roughly ten C hits, which is
-- the ordering we want — a place *called* "Market" should beat a place merely
-- described as near one.
--
-- 'english' throughout, deliberately. Mixing dictionaries inside one tsvector is
-- legal but makes the column queryable only by whichever config a caller
-- guesses; a single config keeps the query side honest. Stemming costs little on
-- proper nouns, and exact/misspelled name lookup is served by the trigram index
-- below rather than by the FTS lane.
--
-- tags is deliberately absent. Folding a text[] in requires array_to_string,
-- which Postgres marks STABLE rather than IMMUTABLE (it depends on the element
-- type's output function), and a generated column accepts only immutable
-- expressions. Tag filtering already has its own path; forcing it in here would
-- mean a trigger, and a trigger is a second source of truth that drifts.
ALTER TABLE points_of_interest
    ADD COLUMN IF NOT EXISTS search_tsv tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(category, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(poi_type, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'C') ||
        setweight(to_tsvector('english', coalesce(ai_summary, '')), 'C')
    ) STORED;

CREATE INDEX IF NOT EXISTS idx_poi_search_tsv
    ON points_of_interest USING GIN (search_tsv);

-- The typo lane. pg_trgm has been installed since 0001 and indexed only on
-- cities.name (0034) — POI names, the thing users actually mistype, had no
-- trigram index at all.
CREATE INDEX IF NOT EXISTS idx_poi_name_trgm
    ON points_of_interest USING GIN (name gin_trgm_ops);

-- Retire the two indexes from 0003 that nothing ever queried. Their content is
-- now a strict subset of search_tsv.
DROP INDEX IF EXISTS idx_poi_name;
DROP INDEX IF EXISTS idx_poi_description;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE INDEX IF NOT EXISTS idx_poi_name ON points_of_interest USING GIN (to_tsvector('english', name));
CREATE INDEX IF NOT EXISTS idx_poi_description ON points_of_interest USING GIN (to_tsvector('english', description));

DROP INDEX IF EXISTS idx_poi_name_trgm;
DROP INDEX IF EXISTS idx_poi_search_tsv;
ALTER TABLE points_of_interest DROP COLUMN IF EXISTS search_tsv;

-- +goose StatementEnd
