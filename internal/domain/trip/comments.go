package trip

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Comment is a note someone on a trip left about it or one of its stops.
type Comment struct {
	ID       uuid.UUID
	TripID   uuid.UUID
	StopID   *uuid.UUID
	AuthorID uuid.UUID
	Body     string
	Created  time.Time
}

// ReportedComment is a comment in the moderation queue.
type ReportedComment struct {
	Comment
	// Reporters is how many people have an open report on it.
	Reporters    int
	Reasons      map[string]int
	Details      []string
	LastReported time.Time
	Hidden       bool
}

// Report reasons a trip comment takes.
var commentReportReasons = map[string]bool{"spam": true, "inappropriate": true, "offensive": true, "other": true}

// Moderation decisions on a reported comment.
const (
	ModerationKeep   = "kept"
	ModerationRemove = "removed"
)

// Comment limits.
const (
	maxCommentBody      = 2000
	commentsPerHour     = 60
	defaultCommentsPage = 100
	maxCommentsPage     = 200
	// hideAfterReports is how many distinct open reports take a comment out
	// of the trip before an admin looks at it.
	hideAfterReports = 3
)

// CommentsRepository stores trip comments and their reports.
type CommentsRepository interface {
	// AddComment stores c and fills its ID and Created.
	AddComment(ctx context.Context, c *Comment) error
	// Comments is up to limit of a trip's comments that viewer may read,
	// newest first, created before `before` when it is set. Left out:
	// deleted and removed comments, auto-hidden ones (except to their author),
	// those viewer reported, and those by someone viewer blocked or who
	// blocked viewer.
	Comments(ctx context.Context, tripID, viewer uuid.UUID, before *time.Time, limit int) ([]Comment, error)
	// Comment loads one comment that is not deleted or removed. ErrNotFound
	// otherwise.
	Comment(ctx context.Context, id uuid.UUID) (*Comment, error)
	DeleteComment(ctx context.Context, id, by uuid.UUID, now time.Time) error
	// CommentsSince counts what author wrote after since, deleted or not.
	CommentsSince(ctx context.Context, author uuid.UUID, since time.Time) (int, error)
	// ReportComment records or replaces reporter's report on a comment.
	ReportComment(ctx context.Context, commentID, reporter uuid.UUID, reason, details string) error
	// ReportedComments pages the comments with open reports, most recently
	// reported first. more is true when there is another page.
	ReportedComments(ctx context.Context, limit, offset int) (out []ReportedComment, more bool, err error)
	// ResolveComment records an admin's decision (ModerationKeep or
	// ModerationRemove), closing the comment's open reports. ErrNotFound
	// when there is no such comment.
	ResolveComment(ctx context.Context, id, admin uuid.UUID, decision string, now time.Time) error
}

type commentsRepository struct{ db *pgxpool.Pool }

// NewCommentsRepository is the Postgres CommentsRepository.
func NewCommentsRepository(db *pgxpool.Pool) CommentsRepository { return &commentsRepository{db: db} }

const commentColumns = `c.id, c.trip_id, c.stop_id, c.author_id, c.body, c.created_at`

// openReports counts a comment's reports since a moderator last acted on it.
const openReports = `(SELECT COUNT(*) FROM trip_comment_reports r
	WHERE r.comment_id = c.id AND r.created_at > COALESCE(c.moderated_at, '-infinity'))`

func scanComment(row pgx.Row, c *Comment) error {
	return row.Scan(&c.ID, &c.TripID, &c.StopID, &c.AuthorID, &c.Body, &c.Created)
}

func (r *commentsRepository) AddComment(ctx context.Context, c *Comment) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO trip_comments (trip_id, stop_id, author_id, body)
		VALUES ($1, $2, $3, $4) RETURNING id, created_at`,
		c.TripID, c.StopID, c.AuthorID, c.Body).Scan(&c.ID, &c.Created)
	if err != nil {
		return fmt.Errorf("add trip comment: %w", err)
	}
	return nil
}

func (r *commentsRepository) Comments(ctx context.Context, tripID, viewer uuid.UUID, before *time.Time, limit int) ([]Comment, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+commentColumns+` FROM trip_comments c
		WHERE c.trip_id = $1
		  AND c.deleted_at IS NULL AND c.moderation_status <> 'removed'
		  AND ($3::timestamptz IS NULL OR c.created_at < $3)
		  AND (c.author_id = $2 OR `+openReports+` < $5)
		  AND NOT EXISTS (SELECT 1 FROM trip_comment_reports mine
			WHERE mine.comment_id = c.id AND mine.reporter_id = $2)
		  AND NOT EXISTS (SELECT 1 FROM user_blocks b
			WHERE (b.blocker_id = $2 AND b.blocked_id = c.author_id)
			   OR (b.blocker_id = c.author_id AND b.blocked_id = $2))
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT $4`, tripID, viewer, before, limit, hideAfterReports)
	if err != nil {
		return nil, fmt.Errorf("list trip comments: %w", err)
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := scanComment(rows, &c); err != nil {
			return nil, fmt.Errorf("scan trip comment: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *commentsRepository) Comment(ctx context.Context, id uuid.UUID) (*Comment, error) {
	var c Comment
	err := scanComment(r.db.QueryRow(ctx, `
		SELECT `+commentColumns+` FROM trip_comments c
		WHERE c.id = $1 AND c.deleted_at IS NULL AND c.moderation_status <> 'removed'`, id), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load trip comment: %w", err)
	}
	return &c, nil
}

func (r *commentsRepository) DeleteComment(ctx context.Context, id, by uuid.UUID, now time.Time) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE trip_comments SET deleted_at = $3, deleted_by = $2
		WHERE id = $1 AND deleted_at IS NULL`, id, by, now)
	if err != nil {
		return fmt.Errorf("delete trip comment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *commentsRepository) CommentsSince(ctx context.Context, author uuid.UUID, since time.Time) (int, error) {
	var n int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM trip_comments WHERE author_id = $1 AND created_at > $2`,
		author, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("count trip comments: %w", err)
	}
	return n, nil
}

func (r *commentsRepository) ReportComment(ctx context.Context, commentID, reporter uuid.UUID, reason, details string) error {
	// A repeat report moves to now: it is a new report against whatever a
	// moderator decided before it.
	_, err := r.db.Exec(ctx, `
		INSERT INTO trip_comment_reports (comment_id, reporter_id, reason, details)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (reporter_id, comment_id)
		DO UPDATE SET reason = EXCLUDED.reason, details = EXCLUDED.details, created_at = NOW()`,
		commentID, reporter, reason, details)
	if err != nil {
		return fmt.Errorf("report trip comment: %w", err)
	}
	return nil
}

func (r *commentsRepository) ReportedComments(ctx context.Context, limit, offset int) ([]ReportedComment, bool, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+commentColumns+`,
		       COUNT(r.id), MAX(r.created_at),
		       array_agg(r.reason ORDER BY r.created_at DESC),
		       array_agg(r.details ORDER BY r.created_at DESC)
		FROM trip_comments c
		JOIN trip_comment_reports r ON r.comment_id = c.id
			AND r.created_at > COALESCE(c.moderated_at, '-infinity')
		WHERE c.deleted_at IS NULL AND c.moderation_status <> 'removed'
		GROUP BY c.id
		ORDER BY MAX(r.created_at) DESC, c.id
		LIMIT $1 OFFSET $2`, limit+1, offset)
	if err != nil {
		return nil, false, fmt.Errorf("list reported trip comments: %w", err)
	}
	defer rows.Close()
	var out []ReportedComment
	for rows.Next() {
		var (
			rc      ReportedComment
			reasons []string
			details []string
		)
		if err := rows.Scan(&rc.ID, &rc.TripID, &rc.StopID, &rc.AuthorID, &rc.Body, &rc.Created,
			&rc.Reporters, &rc.LastReported, &reasons, &details); err != nil {
			return nil, false, fmt.Errorf("scan reported trip comment: %w", err)
		}
		rc.Reasons = map[string]int{}
		for _, reason := range reasons {
			rc.Reasons[reason]++
		}
		for _, d := range details {
			if d = strings.TrimSpace(d); d != "" {
				rc.Details = append(rc.Details, d)
			}
		}
		rc.Hidden = rc.Reporters >= hideAfterReports
		out = append(out, rc)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func (r *commentsRepository) ResolveComment(ctx context.Context, id, admin uuid.UUID, decision string, now time.Time) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE trip_comments
		SET moderation_status = $3, moderated_at = $4, moderated_by = $2
		WHERE id = $1`, id, admin, decision, now)
	if err != nil {
		return fmt.Errorf("resolve trip comment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
