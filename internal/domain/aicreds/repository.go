// Package aicreds stores the model-provider credential a user brought
// themselves, so their account runs on their key rather than Loci's.
//
// The key is sealed by pkg/secret before it reaches this package and is opened
// only where a client is built. Nothing here returns it by accident: reads come
// back as a Credential, which has no field that can hold it, and the one method
// that hands back the sealed bytes says so in its name.
package aicreds

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

// ErrNotFound means this user has not brought a credential. It is an ordinary
// state — most accounts run on Loci's own key — so callers branch on it rather
// than treating it as a failure.
var ErrNotFound = errors.New("no provider credential for this user")

// PgxPool abstracts the subset of pgxpool.Pool used by the repository to allow
// mocking in tests.
type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ PgxPool = (*pgxpool.Pool)(nil)

// Sealed is ciphertext produced by pkg/secret. A distinct type so that a
// function taking one cannot be handed a plaintext key by mistake.
type Sealed []byte

// Credential is everything about a stored credential except the credential.
//
// Deliberately missing an api key field: this is what Get returns, what the
// handler turns into a response, and what the settings page renders. Adding one
// here would put the secret on all three paths at once.
type Credential struct {
	UserID      uuid.UUID
	Provider    string
	KeyHint     string
	Model       string
	BaseURL     string
	LastError   string
	LastErrorAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Failing reports whether the last call on this credential was rejected. The
// settings page shows it, because the alternative is an account that quietly
// falls back to Loci's key while telling the user their own is in use.
func (c Credential) Failing() bool { return c.LastError != "" }

type Repository interface {
	// Get returns the credential's metadata. It cannot return the key.
	Get(ctx context.Context, userID uuid.UUID) (Credential, error)
	// SealedKey returns the metadata together with the sealed key, for the one
	// caller that builds a client from it.
	SealedKey(ctx context.Context, userID uuid.UUID) (Credential, Sealed, error)
	// Upsert replaces the credential and clears any recorded failure, since a
	// newly saved key has not failed yet.
	Upsert(ctx context.Context, userID uuid.UUID, provider string, key Sealed, hint, model, baseURL string) (Credential, error)
	// UpdateSettings changes the model and base URL without touching the key,
	// which is what "leave the key field blank to keep it" needs.
	UpdateSettings(ctx context.Context, userID uuid.UUID, model, baseURL string) (Credential, error)
	Delete(ctx context.Context, userID uuid.UUID) error
	// RecordError notes why the last call failed. Best effort: it runs on a
	// path that has already failed, and a second failure there must not
	// replace the first in what the caller reports.
	RecordError(ctx context.Context, userID uuid.UUID, reason string) error
}

type repository struct {
	pgpool PgxPool
}

func NewRepository(pgpool PgxPool) Repository {
	return &repository{pgpool: pgpool}
}

// credentialColumns deliberately omits api_key. Every read path in this file
// uses it, so the sealed key can only leave through a query that names the
// column explicitly — of which there is exactly one.
const credentialColumns = `user_id, provider, key_hint, model, base_url, last_error, last_error_at, created_at, updated_at`

func scanCredential(row pgx.Row) (Credential, error) {
	var c Credential
	err := row.Scan(&c.UserID, &c.Provider, &c.KeyHint, &c.Model, &c.BaseURL,
		&c.LastError, &c.LastErrorAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Credential{}, err
	}
	return c, nil
}

func (r *repository) Get(ctx context.Context, userID uuid.UUID) (Credential, error) {
	query := `SELECT ` + credentialColumns + ` FROM user_ai_credentials WHERE user_id = $1`

	c, err := scanCredential(r.pgpool.QueryRow(ctx, query, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("failed to read provider credential: %w", err)
	}
	return c, nil
}

func (r *repository) SealedKey(ctx context.Context, userID uuid.UUID) (Credential, Sealed, error) {
	query := `SELECT ` + credentialColumns + `, api_key FROM user_ai_credentials WHERE user_id = $1`

	var c Credential
	var key Sealed
	err := r.pgpool.QueryRow(ctx, query, userID).Scan(&c.UserID, &c.Provider, &c.KeyHint,
		&c.Model, &c.BaseURL, &c.LastError, &c.LastErrorAt, &c.CreatedAt, &c.UpdatedAt, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, nil, ErrNotFound
	}
	if err != nil {
		// Not wrapped with anything from the row: one of the columns is the key.
		return Credential{}, nil, errors.New("failed to read provider credential")
	}
	return c, key, nil
}

func (r *repository) Upsert(ctx context.Context, userID uuid.UUID, provider string, key Sealed, hint, model, baseURL string) (Credential, error) {
	query := `
		INSERT INTO user_ai_credentials (user_id, provider, api_key, key_hint, model, base_url)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET
			provider      = EXCLUDED.provider,
			api_key       = EXCLUDED.api_key,
			key_hint      = EXCLUDED.key_hint,
			model         = EXCLUDED.model,
			base_url      = EXCLUDED.base_url,
			last_error    = '',
			last_error_at = NULL,
			updated_at    = now()
		RETURNING ` + credentialColumns

	c, err := scanCredential(r.pgpool.QueryRow(ctx, query, userID, provider, []byte(key), hint, model, baseURL))
	if err != nil {
		// The arguments include the sealed key; naming them here is how a
		// ciphertext ends up formatted into a log.
		return Credential{}, errors.New("failed to save provider credential")
	}
	return c, nil
}

func (r *repository) UpdateSettings(ctx context.Context, userID uuid.UUID, model, baseURL string) (Credential, error) {
	query := `
		UPDATE user_ai_credentials
		SET model = $2, base_url = $3, last_error = '', last_error_at = NULL, updated_at = now()
		WHERE user_id = $1
		RETURNING ` + credentialColumns

	c, err := scanCredential(r.pgpool.QueryRow(ctx, query, userID, model, baseURL))
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("failed to update provider settings: %w", err)
	}
	return c, nil
}

func (r *repository) Delete(ctx context.Context, userID uuid.UUID) error {
	tag, err := r.pgpool.Exec(ctx, `DELETE FROM user_ai_credentials WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("failed to delete provider credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *repository) RecordError(ctx context.Context, userID uuid.UUID, reason string) error {
	// Truncated to the column's bound rather than rejected: the reason comes
	// from an upstream provider, and losing the note entirely because their
	// error was long would defeat the point of recording it.
	const maxReason = 500
	if len(reason) > maxReason {
		reason = reason[:maxReason]
	}

	_, err := r.pgpool.Exec(ctx, `
		UPDATE user_ai_credentials
		SET last_error = $2, last_error_at = now()
		WHERE user_id = $1`, userID, reason)
	if err != nil {
		return fmt.Errorf("failed to record provider error: %w", err)
	}
	return nil
}
