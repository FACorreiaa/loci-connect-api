-- +goose Up
-- +goose StatementBegin
-- Make poi_id column nullable to support non-POI content types
ALTER TABLE list_items ALTER COLUMN poi_id DROP NOT NULL;
-- +goose StatementEnd
