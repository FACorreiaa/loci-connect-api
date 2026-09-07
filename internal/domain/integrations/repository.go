// Package integrations registers external MCP servers that Loci may call on
// the user's behalf while planning — their own Hermes instance, a calendar.
//
// This is the direction Loci does not usually run in: everywhere else it is the
// MCP server and somebody's agent is the client. Here Loci dials an address a
// person typed, which is why the endpoint is checked before it is stored and
// again before it is used, and why the token is write-only.
package integrations

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

// ErrNotFound means this user has not connected that integration.
var ErrNotFound = errors.New("integration not connected")

// PgxPool abstracts the subset of pgxpool.Pool used by the repository to allow
// mocking in tests.
type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ PgxPool = (*pgxpool.Pool)(nil)

// Sealed is ciphertext produced by pkg/secret. A distinct type so a function
// taking one cannot be handed a plaintext token by mistake.
type Sealed []byte

// Connection is everything about a registered server except its token.
//
// Deliberately missing a token field: this is what List returns and what the
// settings page renders, and a token reaching it would reach both.
type Connection struct {
	UserID     uuid.UUID
	Provider   string
	Endpoint   string
	HasToken   bool
	LastSeenAt *time.Time
	LastError  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Reachable reports whether this server has ever answered.
func (c Connection) Reachable() bool { return c.LastSeenAt != nil }

// Failing reports whether the last call to it failed.
func (c Connection) Failing() bool { return c.LastError != "" }

type Repository interface {
	// List returns the user's connections, without their tokens.
	List(ctx context.Context, userID uuid.UUID) ([]Connection, error)
	// Get returns one connection, without its token.
	Get(ctx context.Context, userID uuid.UUID, provider string) (Connection, error)
	// SealedToken returns a connection together with its sealed token, for the
	// one caller that dials the server.
	SealedToken(ctx context.Context, userID uuid.UUID, provider string) (Connection, Sealed, error)
	// Upsert registers or replaces a connection, clearing any recorded
	// failure: a newly saved endpoint has not failed yet.
	Upsert(ctx context.Context, userID uuid.UUID, provider, endpoint string, token Sealed) (Connection, error)
	Delete(ctx context.Context, userID uuid.UUID, provider string) error
	// MarkSeen records that the server answered, and clears the last error.
	MarkSeen(ctx context.Context, userID uuid.UUID, provider string) error
	// RecordError notes why the last call failed. Best effort.
	RecordError(ctx context.Context, userID uuid.UUID, provider, reason string) error
}

type repository struct {
	pgpool PgxPool
}

func NewRepository(pgpool PgxPool) Repository {
	return &repository{pgpool: pgpool}
}

// connectionColumns deliberately omits access_token. Every read path uses it,
// so the sealed token can only leave through the one query that names the
// column explicitly.
const connectionColumns = `user_id, provider, endpoint, access_token IS NOT NULL AS has_token,
	last_seen_at, last_error, created_at, updated_at`

func scanConnection(row pgx.Row) (Connection, error) {
	var c Connection
	err := row.Scan(&c.UserID, &c.Provider, &c.Endpoint, &c.HasToken,
		&c.LastSeenAt, &c.LastError, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Connection{}, err
	}
	return c, nil
}

func (r *repository) List(ctx context.Context, userID uuid.UUID) ([]Connection, error) {
	query := `SELECT ` + connectionColumns + `
		FROM integration_connections WHERE user_id = $1 ORDER BY provider`

	rows, err := r.pgpool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list integrations: %w", err)
	}
	defer rows.Close()

	var connections []Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan integration: %w", err)
		}
		connections = append(connections, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate integrations: %w", err)
	}
	return connections, nil
}

func (r *repository) Get(ctx context.Context, userID uuid.UUID, provider string) (Connection, error) {
	query := `SELECT ` + connectionColumns + `
		FROM integration_connections WHERE user_id = $1 AND provider = $2`

	c, err := scanConnection(r.pgpool.QueryRow(ctx, query, userID, provider))
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, fmt.Errorf("failed to read integration: %w", err)
	}
	return c, nil
}

func (r *repository) SealedToken(ctx context.Context, userID uuid.UUID, provider string) (Connection, Sealed, error) {
	query := `SELECT ` + connectionColumns + `, access_token
		FROM integration_connections WHERE user_id = $1 AND provider = $2`

	var c Connection
	var token Sealed
	err := r.pgpool.QueryRow(ctx, query, userID, provider).Scan(&c.UserID, &c.Provider, &c.Endpoint,
		&c.HasToken, &c.LastSeenAt, &c.LastError, &c.CreatedAt, &c.UpdatedAt, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, nil, ErrNotFound
	}
	if err != nil {
		// Not wrapped with anything from the row: one of the columns is the token.
		return Connection{}, nil, errors.New("failed to read integration")
	}
	return c, token, nil
}

func (r *repository) Upsert(ctx context.Context, userID uuid.UUID, provider, endpoint string, token Sealed) (Connection, error) {
	// A nil token is stored as NULL rather than empty, so "this server needs no
	// credential" stays distinguishable from "we failed to store one".
	var stored any
	if len(token) > 0 {
		stored = []byte(token)
	}

	query := `
		INSERT INTO integration_connections (user_id, provider, endpoint, access_token)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, provider) DO UPDATE SET
			endpoint     = EXCLUDED.endpoint,
			access_token = EXCLUDED.access_token,
			last_error   = '',
			updated_at   = now()
		RETURNING ` + connectionColumns

	c, err := scanConnection(r.pgpool.QueryRow(ctx, query, userID, provider, endpoint, stored))
	if err != nil {
		// The arguments include the sealed token.
		return Connection{}, errors.New("failed to save integration")
	}
	return c, nil
}

func (r *repository) Delete(ctx context.Context, userID uuid.UUID, provider string) error {
	tag, err := r.pgpool.Exec(ctx,
		`DELETE FROM integration_connections WHERE user_id = $1 AND provider = $2`, userID, provider)
	if err != nil {
		return fmt.Errorf("failed to delete integration: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *repository) MarkSeen(ctx context.Context, userID uuid.UUID, provider string) error {
	_, err := r.pgpool.Exec(ctx, `
		UPDATE integration_connections
		SET last_seen_at = now(), last_error = ''
		WHERE user_id = $1 AND provider = $2`, userID, provider)
	if err != nil {
		return fmt.Errorf("failed to mark integration seen: %w", err)
	}
	return nil
}

func (r *repository) RecordError(ctx context.Context, userID uuid.UUID, provider, reason string) error {
	// Truncated to the column's bound rather than rejected: the reason comes
	// from a server that is not ours, and losing the note because their error
	// was long would defeat the point of recording it.
	const maxReason = 500
	if len(reason) > maxReason {
		reason = reason[:maxReason]
	}

	_, err := r.pgpool.Exec(ctx, `
		UPDATE integration_connections
		SET last_error = $3
		WHERE user_id = $1 AND provider = $2`, userID, provider, reason)
	if err != nil {
		return fmt.Errorf("failed to record integration error: %w", err)
	}
	return nil
}
