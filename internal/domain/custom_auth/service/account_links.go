package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLinkedElsewhere is a phone number or Facebook account that already
// belongs to another Loci account.
var ErrLinkedElsewhere = errors.New("already linked to another account")

// AccountLinks attaches identifiers to a signed-in account so friends can
// find it: a verified phone number (contact matching) and a Facebook account
// (Facebook friends).
type AccountLinks struct {
	db *pgxpool.Pool
}

// NewAccountLinks returns the store.
func NewAccountLinks(db *pgxpool.Pool) *AccountLinks {
	return &AccountLinks{db: db}
}

// AttachPhone stores phone as userID's verified number. The caller has
// already checked the SMS code. A number verified on another active account
// is ErrLinkedElsewhere.
func (a *AccountLinks) AttachPhone(ctx context.Context, userID uuid.UUID, phone string) error {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin attach phone: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var other uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM users
		WHERE phone = $1 AND phone_verified_at IS NOT NULL AND is_active AND id <> $2
		LIMIT 1`, phone, userID).Scan(&other)
	switch {
	case err == nil:
		return ErrLinkedElsewhere
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("check phone owner: %w", err)
	}
	ct, err := tx.Exec(ctx, `
		UPDATE users SET phone = $2, phone_verified_at = NOW(), updated_at = NOW() WHERE id = $1`, userID, phone)
	if err != nil {
		return fmt.Errorf("attach phone: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("attach phone: user %s not found", userID)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit attach phone: %w", err)
	}
	return nil
}

// LinkFacebook links the Facebook subject to userID and replaces the stored
// friend list with friendIDs. A subject linked to another account is
// ErrLinkedElsewhere.
func (a *AccountLinks) LinkFacebook(ctx context.Context, userID uuid.UUID, subject string, friendIDs []string) error {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin link facebook: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var owner uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO user_providers (user_id, provider, provider_user_id) VALUES ($1, 'facebook', $2)
		ON CONFLICT (provider, provider_user_id) DO UPDATE SET provider = EXCLUDED.provider
		RETURNING user_id`, userID, subject).Scan(&owner)
	if err != nil {
		return fmt.Errorf("link facebook: %w", err)
	}
	if owner != userID {
		return ErrLinkedElsewhere
	}
	// One Facebook account per Loci account: an earlier link is replaced.
	if _, err := tx.Exec(ctx, `
		DELETE FROM user_providers WHERE user_id = $1 AND provider = 'facebook' AND provider_user_id <> $2`,
		userID, subject); err != nil {
		return fmt.Errorf("replace facebook link: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_facebook_friends WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear facebook friends: %w", err)
	}
	if len(friendIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_facebook_friends (user_id, facebook_id)
			SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`, userID, friendIDs); err != nil {
			return fmt.Errorf("store facebook friends: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit link facebook: %w", err)
	}
	return nil
}
