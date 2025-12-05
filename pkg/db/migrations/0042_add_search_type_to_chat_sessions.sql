-- +goose Up
-- This allows us to differentiate between:
-- - 'discover': Quick searches from the /discover page
-- - 'itinerary': Full itinerary planning from main page
-- - 'restaurant', 'hotel', 'activity': Domain-specific searches

ALTER TABLE chat_sessions
ADD COLUMN search_type VARCHAR(20) DEFAULT 'itinerary';

-- Create index for faster filtering
CREATE INDEX idx_chat_sessions_search_type ON chat_sessions (search_type);

-- Create composite index for user + search_type queries
CREATE INDEX idx_chat_sessions_user_search_type ON chat_sessions (user_id, search_type);

-- Update existing sessions to have 'itinerary' search_type
UPDATE chat_sessions SET search_type = 'itinerary' WHERE search_type IS NULL;

-- Make search_type NOT NULL after backfilling
ALTER TABLE chat_sessions ALTER COLUMN search_type SET NOT NULL;

-- Add constraint to ensure valid search types
ALTER TABLE chat_sessions
ADD CONSTRAINT chat_sessions_search_type_check
CHECK (search_type IN ('discover', 'itinerary', 'restaurant', 'hotel', 'activity'));

-- +goose Down

DROP INDEX IF EXISTS idx_chat_sessions_user_search_type;
DROP INDEX IF EXISTS idx_chat_sessions_search_type;

ALTER TABLE chat_sessions DROP CONSTRAINT IF EXISTS chat_sessions_search_type_check;
ALTER TABLE chat_sessions DROP COLUMN IF EXISTS search_type;
