-- +goose Up
-- +goose StatementBegin
-- Planning together: the owner and members leave notes on a trip or one of
-- its stops. Shaped like board_comments (0110), without threads.
--
-- stop_id has no foreign key: a stop's comments outlive the stop, and the
-- clients show them with the trip's. A deleted comment keeps its row
-- (deleted_at) so a report on it still resolves.
--
-- Moderation mirrors reviews (0112): moderation_status is 'none' until an
-- admin acts, 'kept' after KEEP, 'removed' after REMOVE. Auto-hide is not
-- stored: three or more distinct reporters since moderated_at hide the
-- comment at read time.
CREATE TABLE IF NOT EXISTS trip_comments (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id           UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    stop_id           UUID,
    author_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body              TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMPTZ,
    deleted_by        UUID REFERENCES users (id) ON DELETE SET NULL,
    moderation_status TEXT NOT NULL DEFAULT 'none'
        CHECK (moderation_status IN ('none', 'kept', 'removed')),
    moderated_at      TIMESTAMPTZ,
    moderated_by      UUID REFERENCES users (id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_trip_comments_trip ON trip_comments (trip_id, created_at DESC);
-- The hourly write limit counts an author's recent comments.
CREATE INDEX IF NOT EXISTS idx_trip_comments_author ON trip_comments (author_id, created_at DESC);

-- One report per reporter per comment; reporting again replaces the reason.
CREATE TABLE IF NOT EXISTS trip_comment_reports (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    comment_id  UUID NOT NULL REFERENCES trip_comments (id) ON DELETE CASCADE,
    reporter_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason      TEXT NOT NULL CHECK (reason IN ('spam', 'inappropriate', 'offensive', 'other')),
    details     TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT trip_comment_reports_reporter_comment_unique UNIQUE (reporter_id, comment_id)
);

CREATE INDEX IF NOT EXISTS idx_trip_comment_reports_comment
    ON trip_comment_reports (comment_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_comment_reports;
DROP TABLE IF EXISTS trip_comments;
-- +goose StatementEnd
