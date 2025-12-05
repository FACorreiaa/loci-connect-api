-- +goose Up
-- Enhanced LLM logging for comprehensive analytics and monitoring
-- This migration adds fields for request tracking, error handling, performance monitoring,
-- and context-specific analytics across all LLM endpoints (chat, discover, nearby, etc.)

-- Add new columns for comprehensive LLM logging
ALTER TABLE llm_interactions
    -- Request tracking
    ADD COLUMN IF NOT EXISTS request_id UUID DEFAULT uuid_generate_v4(),

    -- Timestamp tracking (preserve existing created_at, add updated_at for consistency)
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW(),

    -- Status and error tracking
    ADD COLUMN IF NOT EXISTS status_code INTEGER DEFAULT 200,
    ADD COLUMN IF NOT EXISTS error_message TEXT,
    ADD COLUMN IF NOT EXISTS provider VARCHAR(50) DEFAULT 'google', -- e.g., 'google', 'openai', 'anthropic'

    -- Intent and context tracking
    ADD COLUMN IF NOT EXISTS intent VARCHAR(100), -- e.g., 'itinerary', 'restaurant', 'hotel', 'discover', 'nearby'
    ADD COLUMN IF NOT EXISTS search_type VARCHAR(50), -- e.g., 'general', 'dining', 'accommodation', 'activities'

    -- Model parameters for quality correlation
    ADD COLUMN IF NOT EXISTS temperature REAL,
    ADD COLUMN IF NOT EXISTS top_p REAL,
    ADD COLUMN IF NOT EXISTS top_k INTEGER,
    ADD COLUMN IF NOT EXISTS max_tokens INTEGER,

    -- Cost tracking
    ADD COLUMN IF NOT EXISTS cost_estimate_usd NUMERIC(10, 6), -- Calculated field: tokens * per-token price

    -- User feedback and quality
    ADD COLUMN IF NOT EXISTS user_feedback_rating INTEGER CHECK (user_feedback_rating >= 1 AND user_feedback_rating <= 5),
    ADD COLUMN IF NOT EXISTS user_feedback_comment TEXT,
    ADD COLUMN IF NOT EXISTS user_feedback_timestamp TIMESTAMPTZ,

    -- Cache efficiency
    ADD COLUMN IF NOT EXISTS cache_hit BOOLEAN DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS cache_key TEXT,

    -- Device and platform tracking
    ADD COLUMN IF NOT EXISTS device_type VARCHAR(50), -- e.g., 'ios', 'android', 'web', 'desktop'
    ADD COLUMN IF NOT EXISTS platform VARCHAR(50), -- e.g., 'mobile', 'web', 'api'
    ADD COLUMN IF NOT EXISTS user_agent TEXT,

    -- Privacy and compliance
    ADD COLUMN IF NOT EXISTS prompt_hash VARCHAR(64), -- SHA256 hash for anonymized prompt tracking
    ADD COLUMN IF NOT EXISTS is_pii_redacted BOOLEAN DEFAULT FALSE,

    -- Streaming metadata
    ADD COLUMN IF NOT EXISTS is_streaming BOOLEAN DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS stream_chunks_count INTEGER,
    ADD COLUMN IF NOT EXISTS stream_duration_ms INTEGER;

-- Create indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_llm_interactions_request_id ON llm_interactions(request_id);
CREATE INDEX IF NOT EXISTS idx_llm_interactions_status_code ON llm_interactions(status_code);
CREATE INDEX IF NOT EXISTS idx_llm_interactions_intent ON llm_interactions(intent);
CREATE INDEX IF NOT EXISTS idx_llm_interactions_search_type ON llm_interactions(search_type);
CREATE INDEX IF NOT EXISTS idx_llm_interactions_provider ON llm_interactions(provider);
CREATE INDEX IF NOT EXISTS idx_llm_interactions_cache_hit ON llm_interactions(cache_hit);
CREATE INDEX IF NOT EXISTS idx_llm_interactions_updated_at ON llm_interactions(updated_at);
CREATE INDEX IF NOT EXISTS idx_llm_interactions_device_type ON llm_interactions(device_type);

-- Composite indexes for common queries
CREATE INDEX IF NOT EXISTS idx_llm_interactions_user_intent ON llm_interactions(user_id, intent, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_interactions_city_intent ON llm_interactions(city_id, intent, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_interactions_performance ON llm_interactions(intent, latency_ms, created_at DESC);

-- Create trigger to update 'updated_at' timestamp
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION update_llm_interactions_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trigger_update_llm_interactions_updated_at
    BEFORE UPDATE ON llm_interactions
    FOR EACH ROW
    EXECUTE FUNCTION update_llm_interactions_updated_at();

-- Add comments for documentation
COMMENT ON COLUMN llm_interactions.request_id IS 'Unique identifier for tracing requests across systems';
COMMENT ON COLUMN llm_interactions.status_code IS 'HTTP status or error code (200 for success, 429 for rate limit, etc.)';
COMMENT ON COLUMN llm_interactions.error_message IS 'Error details if the call failed';
COMMENT ON COLUMN llm_interactions.provider IS 'LLM provider (google, openai, anthropic, etc.)';
COMMENT ON COLUMN llm_interactions.intent IS 'Query categorization (itinerary, restaurant, hotel, discover, nearby)';
COMMENT ON COLUMN llm_interactions.search_type IS 'Specific search type within intent';
COMMENT ON COLUMN llm_interactions.temperature IS 'Model temperature parameter for response quality correlation';
COMMENT ON COLUMN llm_interactions.top_p IS 'Model top_p parameter';
COMMENT ON COLUMN llm_interactions.top_k IS 'Model top_k parameter';
COMMENT ON COLUMN llm_interactions.max_tokens IS 'Maximum tokens allowed in response';
COMMENT ON COLUMN llm_interactions.cost_estimate_usd IS 'Calculated cost based on token usage';
COMMENT ON COLUMN llm_interactions.user_feedback_rating IS 'User rating of response quality (1-5)';
COMMENT ON COLUMN llm_interactions.cache_hit IS 'Whether response was served from cache';
COMMENT ON COLUMN llm_interactions.cache_key IS 'Cache key for response caching';
COMMENT ON COLUMN llm_interactions.device_type IS 'Device type (ios, android, web, desktop)';
COMMENT ON COLUMN llm_interactions.platform IS 'Platform (mobile, web, api)';
COMMENT ON COLUMN llm_interactions.prompt_hash IS 'SHA256 hash of prompt for anonymized tracking';
COMMENT ON COLUMN llm_interactions.is_pii_redacted IS 'Whether PII has been redacted from prompt/response';
COMMENT ON COLUMN llm_interactions.is_streaming IS 'Whether response was streamed';
COMMENT ON COLUMN llm_interactions.stream_chunks_count IS 'Number of chunks in streaming response';
COMMENT ON COLUMN llm_interactions.stream_duration_ms IS 'Total duration of streaming response';

-- +goose Down
-- Remove enhanced LLM logging fields

-- Drop trigger and function
DROP TRIGGER IF EXISTS trigger_update_llm_interactions_updated_at ON llm_interactions;
DROP FUNCTION IF EXISTS update_llm_interactions_updated_at();

-- Drop indexes
DROP INDEX IF EXISTS idx_llm_interactions_request_id;
DROP INDEX IF EXISTS idx_llm_interactions_status_code;
DROP INDEX IF EXISTS idx_llm_interactions_intent;
DROP INDEX IF EXISTS idx_llm_interactions_search_type;
DROP INDEX IF EXISTS idx_llm_interactions_provider;
DROP INDEX IF EXISTS idx_llm_interactions_cache_hit;
DROP INDEX IF EXISTS idx_llm_interactions_updated_at;
DROP INDEX IF EXISTS idx_llm_interactions_device_type;
DROP INDEX IF EXISTS idx_llm_interactions_user_intent;
DROP INDEX IF EXISTS idx_llm_interactions_city_intent;
DROP INDEX IF EXISTS idx_llm_interactions_performance;

-- Remove columns (in reverse order of addition)
ALTER TABLE llm_interactions
    DROP COLUMN IF EXISTS stream_duration_ms,
    DROP COLUMN IF EXISTS stream_chunks_count,
    DROP COLUMN IF EXISTS is_streaming,
    DROP COLUMN IF EXISTS is_pii_redacted,
    DROP COLUMN IF EXISTS prompt_hash,
    DROP COLUMN IF EXISTS user_agent,
    DROP COLUMN IF EXISTS platform,
    DROP COLUMN IF EXISTS device_type,
    DROP COLUMN IF EXISTS cache_key,
    DROP COLUMN IF EXISTS cache_hit,
    DROP COLUMN IF EXISTS user_feedback_timestamp,
    DROP COLUMN IF EXISTS user_feedback_comment,
    DROP COLUMN IF EXISTS user_feedback_rating,
    DROP COLUMN IF EXISTS cost_estimate_usd,
    DROP COLUMN IF EXISTS max_tokens,
    DROP COLUMN IF EXISTS top_k,
    DROP COLUMN IF EXISTS top_p,
    DROP COLUMN IF EXISTS temperature,
    DROP COLUMN IF EXISTS search_type,
    DROP COLUMN IF EXISTS intent,
    DROP COLUMN IF EXISTS provider,
    DROP COLUMN IF EXISTS error_message,
    DROP COLUMN IF EXISTS status_code,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS request_id;
