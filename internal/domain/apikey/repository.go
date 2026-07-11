package apikey

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

// ErrNotFound is returned when a key does not exist or is not visible to
// the caller (revoked, expired, or owned by another user, depending on op).
var ErrNotFound = errors.New("api key not found")

// PgxPool abstracts the subset of pgxpool.Pool used by the repository to allow mocking in tests.
type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ PgxPool = (*pgxpool.Pool)(nil)

// Key is the persisted API key metadata. The plaintext secret is never stored.
type Key struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	KeyPrefix  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

type Repository interface {
	Create(ctx context.Context, userID uuid.UUID, name, keyPrefix string, keyHash []byte, expiresAt *time.Time) (*Key, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]Key, error)
	// Revoke marks the key revoked; returns ErrNotFound when the key does not
	// belong to userID or is already revoked.
	Revoke(ctx context.Context, userID, keyID uuid.UUID) error
	// LookupActiveByHash resolves a hashed key to its owner, rejecting
	// revoked and expired keys, and stamps last_used_at as a side effect.
	LookupActiveByHash(ctx context.Context, keyHash []byte) (*Key, error)
}

type repository struct {
	pgpool PgxPool
}

func NewRepository(pgpool PgxPool) Repository {
	return &repository{pgpool: pgpool}
}

const keyColumns = `id, user_id, name, key_prefix, created_at, last_used_at, expires_at, revoked_at`

func scanKey(row pgx.Row) (*Key, error) {
	var k Key
	err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.KeyPrefix, &k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *repository) Create(ctx context.Context, userID uuid.UUID, name, keyPrefix string, keyHash []byte, expiresAt *time.Time) (*Key, error) {
	query := `
		INSERT INTO api_keys (user_id, name, key_prefix, key_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + keyColumns
	k, err := scanKey(r.pgpool.QueryRow(ctx, query, userID, name, keyPrefix, keyHash, expiresAt))
	if err != nil {
		return nil, fmt.Errorf("failed to create api key: %w", err)
	}
	return k, nil
}

func (r *repository) ListByUser(ctx context.Context, userID uuid.UUID) ([]Key, error) {
	query := `
		SELECT ` + keyColumns + `
		FROM api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC`
	rows, err := r.pgpool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys: %w", err)
	}
	defer rows.Close()

	var keys []Key
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan api key: %w", err)
		}
		keys = append(keys, *k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate api keys: %w", err)
	}
	return keys, nil
}

func (r *repository) Revoke(ctx context.Context, userID, keyID uuid.UUID) error {
	query := `
		UPDATE api_keys
		SET revoked_at = NOW()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`
	tag, err := r.pgpool.Exec(ctx, query, keyID, userID)
	if err != nil {
		return fmt.Errorf("failed to revoke api key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *repository) LookupActiveByHash(ctx context.Context, keyHash []byte) (*Key, error) {
	// last_used_at stamping is best-effort and folded into the lookup to keep
	// the hot auth path at one round trip.
	query := `
		UPDATE api_keys
		SET last_used_at = NOW()
		WHERE key_hash = $1
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		RETURNING ` + keyColumns
	k, err := scanKey(r.pgpool.QueryRow(ctx, query, keyHash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to look up api key: %w", err)
	}
	return k, nil
}
