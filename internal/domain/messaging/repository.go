package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotLinked means this chat, or this user, has no link on that platform.
var ErrNotLinked = errors.New("messaging: not linked")

// ErrBadCode covers every way a code fails: unknown, expired, already used.
//
// One error on purpose. From the sender's side they are the same problem —
// "that code will not work, get another" — and distinguishing them in the reply
// would turn the bot into an oracle for which codes exist.
var ErrBadCode = errors.New("messaging: that code is not valid")

// PgxPool abstracts the subset of pgxpool.Pool used by the repository to allow
// mocking in tests.
type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ PgxPool = (*pgxpool.Pool)(nil)

// Link is a chat connected to an account.
type Link struct {
	UserID      uuid.UUID
	Platform    string
	ExternalID  string
	DisplayName string
	LinkedAt    time.Time
	LastSeenAt  *time.Time
}

// Live reports whether the chat has ever been used since it was linked.
func (l Link) Live() bool { return l.LastSeenAt != nil }

type Repository interface {
	// LinkForChat resolves a conversation to the account that owns it. This is
	// the lookup every inbound message makes.
	LinkForChat(ctx context.Context, platform, externalID string) (Link, error)
	// LinkForUser returns a user's link on a platform.
	LinkForUser(ctx context.Context, userID uuid.UUID, platform string) (Link, error)
	// Unlink removes a user's link. Idempotent from the caller's side: an
	// absent link reports ErrNotLinked.
	Unlink(ctx context.Context, userID uuid.UUID, platform string) error
	// UnlinkChat removes a link by conversation, for "/unlink" sent in a chat.
	UnlinkChat(ctx context.Context, platform, externalID string) error
	// TouchLink records that the chat was used, and refreshes the display name
	// because people rename themselves.
	TouchLink(ctx context.Context, platform, externalID, displayName string) error

	// CreateCode stores a code's hash for later redemption.
	CreateCode(ctx context.Context, userID uuid.UUID, platform string, codeHash []byte, expiresAt time.Time) error
	// RedeemCode consumes a code and links the chat to its owner, in one
	// statement, so two messages carrying the same code cannot both succeed.
	RedeemCode(ctx context.Context, platform, externalID, displayName string, codeHash []byte, now time.Time) (Link, error)

	// Cursor reads how far this bot's update stream has been consumed.
	Cursor(ctx context.Context, platform, accountID string) (int64, error)
	// SetCursor advances the watermark for this bot.
	SetCursor(ctx context.Context, platform, accountID string, updateID int64) error
}

type repository struct {
	pgpool PgxPool
}

func NewRepository(pgpool PgxPool) Repository {
	return &repository{pgpool: pgpool}
}

const linkColumns = `user_id, platform, external_id, display_name, linked_at, last_seen_at`

func scanLink(row pgx.Row) (Link, error) {
	var l Link
	err := row.Scan(&l.UserID, &l.Platform, &l.ExternalID, &l.DisplayName, &l.LinkedAt, &l.LastSeenAt)
	if err != nil {
		return Link{}, err
	}
	return l, nil
}

func (r *repository) LinkForChat(ctx context.Context, platform, externalID string) (Link, error) {
	query := `SELECT ` + linkColumns + ` FROM messaging_links WHERE platform = $1 AND external_id = $2`

	l, err := scanLink(r.pgpool.QueryRow(ctx, query, platform, externalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotLinked
	}
	if err != nil {
		return Link{}, fmt.Errorf("failed to read messaging link: %w", err)
	}
	return l, nil
}

func (r *repository) LinkForUser(ctx context.Context, userID uuid.UUID, platform string) (Link, error) {
	query := `SELECT ` + linkColumns + ` FROM messaging_links WHERE user_id = $1 AND platform = $2`

	l, err := scanLink(r.pgpool.QueryRow(ctx, query, userID, platform))
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotLinked
	}
	if err != nil {
		return Link{}, fmt.Errorf("failed to read messaging link: %w", err)
	}
	return l, nil
}

func (r *repository) Unlink(ctx context.Context, userID uuid.UUID, platform string) error {
	tag, err := r.pgpool.Exec(ctx,
		`DELETE FROM messaging_links WHERE user_id = $1 AND platform = $2`, userID, platform)
	if err != nil {
		return fmt.Errorf("failed to unlink: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotLinked
	}
	return nil
}

func (r *repository) UnlinkChat(ctx context.Context, platform, externalID string) error {
	tag, err := r.pgpool.Exec(ctx,
		`DELETE FROM messaging_links WHERE platform = $1 AND external_id = $2`, platform, externalID)
	if err != nil {
		return fmt.Errorf("failed to unlink chat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotLinked
	}
	return nil
}

func (r *repository) TouchLink(ctx context.Context, platform, externalID, displayName string) error {
	_, err := r.pgpool.Exec(ctx, `
		UPDATE messaging_links
		SET last_seen_at = now(),
		    display_name = CASE WHEN $3 = '' THEN display_name ELSE $3 END
		WHERE platform = $1 AND external_id = $2`, platform, externalID, displayName)
	if err != nil {
		return fmt.Errorf("failed to touch messaging link: %w", err)
	}
	return nil
}

func (r *repository) CreateCode(ctx context.Context, userID uuid.UUID, platform string, codeHash []byte, expiresAt time.Time) error {
	_, err := r.pgpool.Exec(ctx, `
		INSERT INTO messaging_link_codes (code_hash, user_id, platform, expires_at)
		VALUES ($1, $2, $3, $4)`, codeHash, userID, platform, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to store link code: %w", err)
	}
	return nil
}

func (r *repository) RedeemCode(ctx context.Context, platform, externalID, displayName string, codeHash []byte, now time.Time) (Link, error) {
	// Redemption and linking in one statement.
	//
	// Two messages carrying the same code arrive concurrently more often than
	// it sounds — people paste twice when the first reply is slow. Marking the
	// code used in the same statement that reads it means the second finds
	// nothing to claim, rather than both linking and the later one silently
	// winning.
	query := `
		WITH claimed AS (
			UPDATE messaging_link_codes
			SET redeemed_at = $5
			WHERE code_hash = $4
			  AND platform = $1
			  AND redeemed_at IS NULL
			  AND expires_at > $5
			RETURNING user_id
		)
		INSERT INTO messaging_links (user_id, platform, external_id, display_name)
		SELECT user_id, $1, $2, $3 FROM claimed
		ON CONFLICT (platform, external_id) DO UPDATE SET
			user_id      = EXCLUDED.user_id,
			display_name = EXCLUDED.display_name,
			linked_at    = now(),
			last_seen_at = NULL
		RETURNING ` + linkColumns

	l, err := scanLink(r.pgpool.QueryRow(ctx, query, platform, externalID, displayName, codeHash, now))
	if errors.Is(err, pgx.ErrNoRows) {
		// The code was unknown, expired, or already used. One error for all
		// three; see ErrBadCode.
		return Link{}, ErrBadCode
	}
	if err != nil {
		return Link{}, fmt.Errorf("failed to redeem link code: %w", err)
	}
	return l, nil
}

func (r *repository) Cursor(ctx context.Context, platform, accountID string) (int64, error) {
	var updateID int64
	err := r.pgpool.QueryRow(ctx,
		`SELECT last_update_id FROM messaging_cursors WHERE platform = $1 AND account_id = $2`,
		platform, accountID).Scan(&updateID)
	if errors.Is(err, pgx.ErrNoRows) {
		// A bot this deployment has not read before starts at the beginning of
		// its own sequence, which is exactly what a new bot needs.
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to read messaging cursor: %w", err)
	}
	return updateID, nil
}

func (r *repository) SetCursor(ctx context.Context, platform, accountID string, updateID int64) error {
	// GREATEST, so an out-of-order acknowledgement cannot move the watermark
	// backwards and cause everything after it to be answered a second time.
	_, err := r.pgpool.Exec(ctx, `
		INSERT INTO messaging_cursors (platform, account_id, last_update_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (platform, account_id) DO UPDATE SET
			last_update_id = GREATEST(messaging_cursors.last_update_id, EXCLUDED.last_update_id),
			updated_at     = now()`, platform, accountID, updateID)
	if err != nil {
		return fmt.Errorf("failed to advance messaging cursor: %w", err)
	}
	return nil
}
