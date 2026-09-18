-- +goose Up
-- +goose StatementBegin

-- Backfill llm_interactions.intent.
--
-- The column has existed since migration 0043 and was never written. Until the
-- change that ships with this migration, the only record of what a request was
-- routed as lived inside the prompt string the chat service assembles:
--
--   Unified Chat Stream - Domain: itinerary, Message: Three days in Porto
--
-- The activity feed groups and filters on intent, so without this every row
-- written before today would show up untyped. Reading the domain back out of
-- that prefix recovers it exactly — the prefix is generated, not typed by a
-- person, so the match is not a guess.
--
-- Only rows the user-facing stream wrote match the prefix. Internal model calls
-- (POI detail lookups) do not, and are left with a NULL intent, which is how
-- the feed tells them apart from things somebody actually asked for.
UPDATE llm_interactions
SET intent = substring(prompt from '^Unified Chat Stream - Domain:\s*(\w+),')
WHERE intent IS NULL
  AND prompt LIKE 'Unified Chat Stream - Domain: %';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Irreversible in the strict sense: an intent written by the application since
-- this ran is indistinguishable from one this backfilled. Clearing every intent
-- would throw away live data to undo a derivation, so the down is a no-op. The
-- value is recoverable from the prompt column at any time by re-running the up.
SELECT 1;

-- +goose StatementEnd
