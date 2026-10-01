-- +goose Up
-- +goose StatementBegin
-- Reports landed in review_reports (0107) and nothing read them. Moderation
-- state lives on the review:
--
--   moderation_status  'none' until a moderator acts; 'kept' after KEEP;
--                      'removed' after REMOVE, which takes the review out of
--                      every public read for good (its author still sees it).
--   moderated_at       when a moderator last acted. Reports made before it are
--                      settled; only later ones count towards auto-hide or
--                      show in the moderation queue, so a kept review can be
--                      reported into a new round.
--   moderated_by       who acted. SET NULL keeps the decision if that account
--                      is deleted.
--
-- Auto-hide is not stored: a review with 3 or more open reports (distinct
-- reporters — review_reports is unique per reporter and review) is computed
-- as hidden at read time, so a report withdrawn by an account deletion
-- un-hides it without anything to keep in step.
ALTER TABLE reviews
    ADD COLUMN IF NOT EXISTS moderation_status TEXT NOT NULL DEFAULT 'none'
        CONSTRAINT reviews_moderation_status_check CHECK (moderation_status IN ('none', 'kept', 'removed')),
    ADD COLUMN IF NOT EXISTS moderated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS moderated_by UUID REFERENCES users (id) ON DELETE SET NULL;

-- The open-report count reads review_reports by review and report time.
CREATE INDEX IF NOT EXISTS idx_review_reports_review_created
    ON review_reports (review_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_review_reports_review_created;
ALTER TABLE reviews
    DROP COLUMN IF EXISTS moderated_by,
    DROP COLUMN IF EXISTS moderated_at,
    DROP COLUMN IF EXISTS moderation_status;
-- +goose StatementEnd
