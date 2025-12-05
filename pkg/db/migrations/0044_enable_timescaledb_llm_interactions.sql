-- +goose Up
-- Enable TimescaleDB for efficient time-series querying of LLM interactions
-- TimescaleDB automatically partitions data by time for better query performance

-- Enable TimescaleDB extension (if not already enabled)
-- Note: This requires TimescaleDB to be installed in PostgreSQL
-- If TimescaleDB is not available, this migration will be skipped

-- +goose StatementBegin
        DO $$
BEGIN
    -- Try to create the extension
    CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE;

    -- Convert llm_interactions table to a hypertable if TimescaleDB is available
    -- This enables time-series optimizations like automatic partitioning
    -- The table will be partitioned by created_at with 7-day chunks
            PERFORM create_hypertable(
        'llm_interactions',
        'created_at',
        chunk_time_interval => INTERVAL '7 days',
        if_not_exists => TRUE,
        migrate_data => TRUE
    );
            EXCEPTION
            WHEN undefined_function THEN
        RAISE NOTICE 'TimescaleDB is not available - skipping hypertable creation';
            WHEN OTHERS THEN
        RAISE NOTICE 'Could not create hypertable: %', SQLERRM;
END $$;
-- +goose StatementEnd

-- Create continuous aggregates for common analytics queries
-- This pre-computes aggregations for faster dashboard queries

-- Daily LLM usage statistics by intent
-- +goose StatementBegin
        DO $$
BEGIN
    CREATE MATERIALIZED VIEW IF NOT EXISTS llm_daily_stats_by_intent
        WITH (timescaledb.continuous) AS
    SELECT
        time_bucket('1 day', created_at) AS day,
        intent,
        provider,
        COUNT(*) as total_requests,
        COUNT(*) FILTER (WHERE status_code = 200) as successful_requests,
        COUNT(*) FILTER (WHERE status_code != 200) as failed_requests,
        AVG(latency_ms) as avg_latency_ms,
        PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY latency_ms) as p50_latency_ms,
        PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms) as p95_latency_ms,
        PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_ms) as p99_latency_ms,
        SUM(total_tokens) as total_tokens,
        AVG(total_tokens) as avg_tokens,
        SUM(cost_estimate_usd) as total_cost_usd,
        COUNT(*) FILTER (WHERE cache_hit = TRUE) as cache_hits,
        COUNT(*) FILTER (WHERE cache_hit = FALSE) as cache_misses,
        AVG(user_feedback_rating) FILTER (WHERE user_feedback_rating IS NOT NULL) as avg_user_rating
    FROM llm_interactions
    WHERE created_at > NOW() - INTERVAL '90 days'
    GROUP BY day, intent, provider;

    -- Add refresh policy to update continuous aggregate every hour
            PERFORM add_continuous_aggregate_policy('llm_daily_stats_by_intent',
        start_offset => INTERVAL '3 days',
        end_offset => INTERVAL '1 hour',
        schedule_interval => INTERVAL '1 hour');
            EXCEPTION
            WHEN undefined_function THEN
        RAISE NOTICE 'TimescaleDB continuous aggregates not available - skipping llm_daily_stats_by_intent';
            WHEN OTHERS THEN
        RAISE NOTICE 'Could not create continuous aggregate llm_daily_stats_by_intent: %', SQLERRM;
END $$;
-- +goose StatementEnd

-- Hourly LLM performance metrics
-- +goose StatementBegin
        DO $$
BEGIN
    CREATE MATERIALIZED VIEW IF NOT EXISTS llm_hourly_performance
        WITH (timescaledb.continuous) AS
    SELECT
        time_bucket('1 hour', created_at) AS hour,
        intent,
        search_type,
        device_type,
        COUNT(*) as request_count,
        AVG(latency_ms) as avg_latency_ms,
        MAX(latency_ms) as max_latency_ms,
        MIN(latency_ms) as min_latency_ms,
        STDDEV(latency_ms) as stddev_latency_ms,
        COUNT(*) FILTER (WHERE status_code >= 400) as error_count,
        COUNT(*) FILTER (WHERE is_streaming = TRUE) as streaming_requests,
        AVG(stream_duration_ms) FILTER (WHERE is_streaming = TRUE) as avg_stream_duration_ms
    FROM llm_interactions
    WHERE created_at > NOW() - INTERVAL '30 days'
    GROUP BY hour, intent, search_type, device_type;

    -- Add refresh policy
            PERFORM add_continuous_aggregate_policy('llm_hourly_performance',
        start_offset => INTERVAL '2 days',
        end_offset => INTERVAL '30 minutes',
        schedule_interval => INTERVAL '30 minutes');
            EXCEPTION
            WHEN undefined_function THEN
        RAISE NOTICE 'TimescaleDB continuous aggregates not available - skipping llm_hourly_performance';
            WHEN OTHERS THEN
        RAISE NOTICE 'Could not create continuous aggregate llm_hourly_performance: %', SQLERRM;
END $$;
-- +goose StatementEnd

-- City-based LLM usage patterns
-- +goose StatementBegin
        DO $$
BEGIN
    CREATE MATERIALIZED VIEW IF NOT EXISTS llm_city_usage_daily
        WITH (timescaledb.continuous) AS
    SELECT
        time_bucket('1 day', created_at) AS day,
        city_name,
        intent,
        COUNT(*) as request_count,
        COUNT(DISTINCT user_id) as unique_users,
        AVG(latency_ms) as avg_latency_ms,
        SUM(total_tokens) as total_tokens_used,
        AVG(user_feedback_rating) FILTER (WHERE user_feedback_rating IS NOT NULL) as avg_rating
    FROM llm_interactions
    WHERE created_at > NOW() - INTERVAL '90 days'
      AND city_name IS NOT NULL
    GROUP BY day, city_name, intent;

    -- Add refresh policy
            PERFORM add_continuous_aggregate_policy('llm_city_usage_daily',
        start_offset => INTERVAL '3 days',
        end_offset => INTERVAL '1 hour',
        schedule_interval => INTERVAL '1 hour');
            EXCEPTION
            WHEN undefined_function THEN
        RAISE NOTICE 'TimescaleDB continuous aggregates not available - skipping llm_city_usage_daily';
            WHEN OTHERS THEN
        RAISE NOTICE 'Could not create continuous aggregate llm_city_usage_daily: %', SQLERRM;
END $$;
-- +goose StatementEnd

-- Create compression policy to automatically compress old data
-- Compress chunks older than 14 days to save storage space
-- +goose StatementBegin
        DO $$
BEGIN
            PERFORM add_compression_policy('llm_interactions', INTERVAL '14 days');
            EXCEPTION
            WHEN undefined_function THEN
        RAISE NOTICE 'TimescaleDB compression policy not available - skipping';
            WHEN OTHERS THEN
        RAISE NOTICE 'Could not create compression policy: %', SQLERRM;
END $$;
-- +goose StatementEnd

-- Create data retention policy
-- Automatically drop chunks older than 90 days to manage storage
-- Adjust this based on your retention
-- +goose StatementBegin
        DO $$
BEGIN
            PERFORM add_retention_policy('llm_interactions', INTERVAL '90 days');
            EXCEPTION
            WHEN undefined_function THEN
        RAISE NOTICE 'TimescaleDB retention policy not available - skipping';
            WHEN OTHERS THEN
        RAISE NOTICE 'Could not create retention policy: %', SQLERRM;
END $$;
-- +goose StatementEnd

-- Add comments (only if the views were created successfully)
-- +goose StatementBegin
        DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_matviews WHERE matviewname = 'llm_daily_stats_by_intent') THEN
        COMMENT ON MATERIALIZED VIEW llm_daily_stats_by_intent IS 'Daily aggregated LLM statistics by intent and provider';
END IF;
IF EXISTS (SELECT 1 FROM pg_matviews WHERE matviewname = 'llm_hourly_performance') THEN
        COMMENT ON MATERIALIZED VIEW llm_hourly_performance IS 'Hourly LLM performance metrics for monitoring';
END IF;
IF EXISTS (SELECT 1 FROM pg_matviews WHERE matviewname = 'llm_city_usage_daily') THEN
        COMMENT ON MATERIALIZED VIEW llm_city_usage_daily IS 'Daily LLM usage patterns by city and intent';
END IF;
END $$;
-- +goose StatementEnd