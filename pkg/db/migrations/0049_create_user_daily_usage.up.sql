-- +goose Up
-- +goose StatementBegin
CREATE TABLE user_daily_usage (
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    usage_date DATE NOT NULL DEFAULT CURRENT_DATE,
    request_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, usage_date)
);

-- Trigger to update 'updated_at' timestamp
CREATE TRIGGER trigger_set_user_daily_usage_updated_at
BEFORE UPDATE ON user_daily_usage
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Index for cleanup (though partitioning by date would be better for scale, this is fine for now)
CREATE INDEX idx_user_daily_usage_date ON user_daily_usage (usage_date);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_daily_usage;
-- +goose StatementEnd