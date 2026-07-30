package mfa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Enrollment is the stored MFA state for one user.
type Enrollment struct {
	UserID          uuid.UUID
	SecretEncrypted []byte
	ConfirmedAt     *time.Time
	LastUsedStep    int64
	FailedAttempts  int
	LockedUntil     *time.Time
}

// Confirmed reports whether MFA is active for this user. An unconfirmed row is
// an abandoned enrolment and must not gate login.
func (e *Enrollment) Confirmed() bool {
	return e != nil && e.ConfirmedAt != nil
}

// RecoveryCode is one stored, hashed code.
type RecoveryCode struct {
	ID       uuid.UUID
	CodeHash string
}

// ErrNotFound means the user has no MFA row at all.
var ErrNotFound = errors.New("mfa: no enrollment for user")

// Repository is the storage port. Kept as an interface so the handler tests can
// exercise the login step-up logic without a database.
type Repository interface {
	Get(ctx context.Context, userID uuid.UUID) (*Enrollment, error)
	// UpsertSecret stores a new unconfirmed secret, replacing any pending one.
	UpsertSecret(ctx context.Context, userID uuid.UUID, secret []byte) error
	Confirm(ctx context.Context, userID uuid.UUID, at time.Time) error
	Delete(ctx context.Context, userID uuid.UUID) error
	SaveVerifyResult(ctx context.Context, userID uuid.UUID, res VerifyResult) error

	UnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]RecoveryCode, error)
	// ReplaceRecoveryCodes deletes every existing code and stores the new hashes,
	// atomically: a partial write would leave the user with a mix of codes they
	// have seen and codes they have not.
	ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, hashes []string) error
	// MarkRecoveryCodeUsed consumes one code. Returns false if it was already
	// used, which is how a concurrent double-submit is rejected.
	MarkRecoveryCodeUsed(ctx context.Context, id uuid.UUID, at time.Time) (bool, error)
}

// PostgresRepository is the pgx-backed Repository.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository builds the storage adapter.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Get(ctx context.Context, userID uuid.UUID) (*Enrollment, error) {
	const q = `
        SELECT user_id, secret_encrypted, confirmed_at,
               COALESCE(last_used_step, 0), failed_attempts, locked_until
        FROM user_mfa
        WHERE user_id = $1`

	var e Enrollment
	err := r.pool.QueryRow(ctx, q, userID).Scan(
		&e.UserID, &e.SecretEncrypted, &e.ConfirmedAt,
		&e.LastUsedStep, &e.FailedAttempts, &e.LockedUntil,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mfa: get enrollment: %w", err)
	}
	return &e, nil
}

func (r *PostgresRepository) UpsertSecret(ctx context.Context, userID uuid.UUID, secret []byte) error {
	// Restarting enrolment resets the counters too: the old secret's failure
	// history has nothing to do with the new one.
	const q = `
        INSERT INTO user_mfa (user_id, secret_encrypted)
        VALUES ($1, $2)
        ON CONFLICT (user_id) DO UPDATE
        SET secret_encrypted = EXCLUDED.secret_encrypted,
            confirmed_at     = NULL,
            last_used_step   = NULL,
            failed_attempts  = 0,
            locked_until     = NULL,
            updated_at       = NOW()`

	if _, err := r.pool.Exec(ctx, q, userID, secret); err != nil {
		return fmt.Errorf("mfa: upsert secret: %w", err)
	}
	return nil
}

func (r *PostgresRepository) Confirm(ctx context.Context, userID uuid.UUID, at time.Time) error {
	const q = `
        UPDATE user_mfa
        SET confirmed_at = $2, updated_at = NOW()
        WHERE user_id = $1`

	tag, err := r.pool.Exec(ctx, q, userID, at)
	if err != nil {
		return fmt.Errorf("mfa: confirm enrollment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) Delete(ctx context.Context, userID uuid.UUID) error {
	// Recovery codes cascade from the users FK, not from user_mfa, so they are
	// deleted explicitly — otherwise disabling MFA would leave live codes behind
	// that a re-enrolment would silently inherit.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mfa: begin delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM user_mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("mfa: delete recovery codes: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_mfa WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("mfa: delete enrollment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mfa: commit delete: %w", err)
	}
	return nil
}

func (r *PostgresRepository) SaveVerifyResult(ctx context.Context, userID uuid.UUID, res VerifyResult) error {
	// last_used_step only ever moves forward. GREATEST guards against a stale
	// concurrent write rolling it back and re-opening the replay window.
	const q = `
        UPDATE user_mfa
        SET last_used_step  = CASE WHEN $2 > 0
                                   THEN GREATEST(COALESCE(last_used_step, 0), $2)
                                   ELSE last_used_step END,
            failed_attempts = $3,
            locked_until    = $4,
            updated_at      = NOW()
        WHERE user_id = $1`

	if _, err := r.pool.Exec(ctx, q, userID, res.UsedStep, res.FailedAttempts, res.LockedUntil); err != nil {
		return fmt.Errorf("mfa: save verify result: %w", err)
	}
	return nil
}

func (r *PostgresRepository) UnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]RecoveryCode, error) {
	const q = `
        SELECT id, code_hash
        FROM user_mfa_recovery_codes
        WHERE user_id = $1 AND used_at IS NULL
        ORDER BY created_at`

	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("mfa: list recovery codes: %w", err)
	}
	defer rows.Close()

	var out []RecoveryCode
	for rows.Next() {
		var c RecoveryCode
		if err := rows.Scan(&c.ID, &c.CodeHash); err != nil {
			return nil, fmt.Errorf("mfa: scan recovery code: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *PostgresRepository) ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, hashes []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mfa: begin replace codes: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM user_mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("mfa: clear recovery codes: %w", err)
	}

	for _, h := range hashes {
		_, err := tx.Exec(ctx,
			`INSERT INTO user_mfa_recovery_codes (user_id, code_hash) VALUES ($1, $2)`,
			userID, h)
		if err != nil {
			return fmt.Errorf("mfa: insert recovery code: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mfa: commit replace codes: %w", err)
	}
	return nil
}

func (r *PostgresRepository) MarkRecoveryCodeUsed(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	// The `used_at IS NULL` predicate makes consumption atomic: two concurrent
	// submissions of the same code race on this UPDATE and exactly one wins.
	const q = `
        UPDATE user_mfa_recovery_codes
        SET used_at = $2
        WHERE id = $1 AND used_at IS NULL`

	tag, err := r.pool.Exec(ctx, q, id, at)
	if err != nil {
		return false, fmt.Errorf("mfa: mark recovery code used: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
