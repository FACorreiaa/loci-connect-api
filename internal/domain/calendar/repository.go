package calendar

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Connection is one stored OAuth calendar grant.
type Connection struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	Provider     string
	AccountLabel string
	RefreshToken []byte
	ConnectedAt  time.Time
}

// Store persists feed tokens and OAuth connections.
type Store interface {
	FeedStore
	UpsertConnection(ctx context.Context, userID uuid.UUID, provider, label string, sealed []byte) (*Connection, error)
	ListConnections(ctx context.Context, userID uuid.UUID) ([]*Connection, error)
	GetConnection(ctx context.Context, userID, id uuid.UUID) (*Connection, error)
	DeleteConnection(ctx context.Context, userID, id uuid.UUID) error
}

type pgStore struct {
	db *pgxpool.Pool
}

func NewStore(db *pgxpool.Pool) Store {
	return &pgStore{db: db}
}

// NewFeedStore is kept so existing call sites compile; it returns the same store.
func NewFeedStore(db *pgxpool.Pool) FeedStore { return NewStore(db) }

func (s *pgStore) GetOrCreateFeedToken(ctx context.Context, userID uuid.UUID, mint func() string) (string, error) {
	var token string
	err := s.db.QueryRow(ctx, `SELECT token FROM calendar_feeds WHERE user_id = $1`, userID).Scan(&token)
	if err == nil {
		return token, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("select feed token: %w", err)
	}
	token = mint()
	_, err = s.db.Exec(ctx, `
		INSERT INTO calendar_feeds (user_id, token) VALUES ($1, $2)
		ON CONFLICT (user_id) DO NOTHING`, userID, token)
	if err != nil {
		return "", fmt.Errorf("insert feed token: %w", err)
	}
	err = s.db.QueryRow(ctx, `SELECT token FROM calendar_feeds WHERE user_id = $1`, userID).Scan(&token)
	if err != nil {
		return "", fmt.Errorf("reload feed token: %w", err)
	}
	return token, nil
}

func (s *pgStore) UserIDForFeedToken(ctx context.Context, token string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT user_id FROM calendar_feeds WHERE token = $1`, token).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (s *pgStore) UpsertConnection(ctx context.Context, userID uuid.UUID, provider, label string, sealed []byte) (*Connection, error) {
	row := s.db.QueryRow(ctx, `
		INSERT INTO calendar_connections (user_id, provider, account_label, refresh_token)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, provider) DO UPDATE SET
			account_label = EXCLUDED.account_label,
			refresh_token = EXCLUDED.refresh_token,
			updated_at = now()
		RETURNING id, user_id, provider, account_label, refresh_token, connected_at`,
		userID, provider, label, sealed)
	return scanConnection(row)
}

func (s *pgStore) ListConnections(ctx context.Context, userID uuid.UUID) ([]*Connection, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, provider, account_label, refresh_token, connected_at
		FROM calendar_connections WHERE user_id = $1 ORDER BY connected_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *pgStore) GetConnection(ctx context.Context, userID, id uuid.UUID) (*Connection, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, user_id, provider, account_label, refresh_token, connected_at
		FROM calendar_connections WHERE user_id = $1 AND id = $2`, userID, id)
	c, err := scanConnection(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	return c, err
}

func (s *pgStore) DeleteConnection(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM calendar_connections WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanConnection(row scannable) (*Connection, error) {
	var c Connection
	if err := row.Scan(&c.ID, &c.UserID, &c.Provider, &c.AccountLabel, &c.RefreshToken, &c.ConnectedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

var errNotFound = errors.New("calendar connection not found")
