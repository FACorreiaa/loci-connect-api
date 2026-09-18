-- +goose Up
-- +goose StatementBegin

-- One fact row per answer, not per field.
--
-- place_facts was keyed (poi_id, field), which forced a whole multi-choice
-- answer to be stored as one string. Corroboration is an exact match, so two
-- scouts only agreed when they picked the identical set: one saying a place is
-- vegan and gluten free, another saying only vegan, agreed about vegan and the
-- system saw nothing. With eight vibe tags the odds of two people choosing the
-- same set are small enough that those fields would never have verified at all.
--
-- Keying by value instead lets each answer stand on its own, so agreement is
-- counted per answer. Fields that can only have one answer (crowd level, price,
-- opening hours) keep that property in the handler, which clears the losing
-- values when one is verified.

ALTER TABLE place_facts DROP CONSTRAINT place_facts_pkey;
ALTER TABLE place_facts ADD PRIMARY KEY (poi_id, field, value);

-- The reading side asks for every live fact of a POI and groups by field, which
-- now returns several rows per field.
CREATE INDEX IF NOT EXISTS idx_place_facts_poi_field
    ON place_facts (poi_id, field);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Collapsing back to one row per field has to discard the extra answers; keep
-- the best-supported one.
DELETE FROM place_facts a
USING place_facts b
WHERE a.poi_id = b.poi_id
  AND a.field = b.field
  AND (b.contributor_count, b.verified_at) > (a.contributor_count, a.verified_at);

DROP INDEX IF EXISTS idx_place_facts_poi_field;
ALTER TABLE place_facts DROP CONSTRAINT place_facts_pkey;
ALTER TABLE place_facts ADD PRIMARY KEY (poi_id, field);

-- +goose StatementEnd
