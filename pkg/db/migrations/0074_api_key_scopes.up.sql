-- +goose Up
-- +goose StatementBegin

-- Scopes for API keys.
--
-- An API key is currently a full bearer of its owner's identity: the MCP
-- middleware authenticates the key and injects interceptors.Claims{UserID} with
-- nothing else attached, so any key can drive every tool — including the four
-- that write (update_itinerary, add_poi_to_list, add_favorite, plan_itinerary).
-- A user who hands a key to a third-party agent so it can *read* their saved
-- places is also handing it the ability to rewrite their itineraries.
--
-- Three scopes, smallest useful set:
--   read           retrieval and listing. Everything an agent needs to answer.
--   write          mutate the user's own saved data (favorites, lists, trips).
--   write:generate spend the daily LLM quota. Separate from write because it
--                  costs money per call, and "may save a favorite" should not
--                  imply "may run generations against my balance".

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS scopes TEXT[] NOT NULL DEFAULT ARRAY['read']::text[];

-- Every scope must be one we recognise, and a key with no scopes at all would
-- be a key that silently authenticates and then denies everything.
ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_scopes_known CHECK (
        cardinality(scopes) > 0
        AND scopes <@ ARRAY['read', 'write', 'write:generate']::text[]
    );

-- Existing keys keep the access they already had. Narrowing them here would
-- break live integrations at deploy time with no warning and no way for the
-- owner to know why; the read-only default applies to keys minted from now on,
-- where the owner chooses the scopes up front.
UPDATE api_keys
    SET scopes = ARRAY['read', 'write', 'write:generate']::text[]
    WHERE revoked_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_known;
ALTER TABLE api_keys DROP COLUMN IF EXISTS scopes;
-- +goose StatementEnd
