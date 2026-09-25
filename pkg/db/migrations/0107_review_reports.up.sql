-- +goose Up
-- +goose StatementBegin
-- ReportReview had nowhere to write, so it returned Unimplemented. One row per
-- reporter per review: reporting the same review again replaces the reason
-- rather than stacking reports, so one person cannot inflate a review's count.
-- Both sides cascade: a deleted review or account takes its reports with it.
CREATE TABLE IF NOT EXISTS review_reports (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    review_id   UUID NOT NULL REFERENCES reviews (id) ON DELETE CASCADE,
    reporter_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason      TEXT NOT NULL CHECK (reason IN ('spam', 'inappropriate', 'fake', 'offensive', 'other')),
    details     TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT review_reports_reporter_review_unique UNIQUE (reporter_id, review_id)
);

CREATE INDEX IF NOT EXISTS idx_review_reports_review ON review_reports (review_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS review_reports;
-- +goose StatementEnd
