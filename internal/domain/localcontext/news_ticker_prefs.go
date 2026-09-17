package localcontext

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresNewsTickerPrefs stores the per-user switch in
// news_ticker_preferences (migration 0086). No row means enabled.
type PostgresNewsTickerPrefs struct {
	db *pgxpool.Pool
}

func NewPostgresNewsTickerPrefs(db *pgxpool.Pool) *PostgresNewsTickerPrefs {
	return &PostgresNewsTickerPrefs{db: db}
}

func (p *PostgresNewsTickerPrefs) NewsTickerEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	var enabled bool
	err := p.db.QueryRow(ctx, `SELECT enabled FROM news_ticker_preferences WHERE user_id = $1`, userID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("news ticker preference: %w", err)
	}
	return enabled, nil
}

func (p *PostgresNewsTickerPrefs) SetNewsTickerEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error {
	_, err := p.db.Exec(ctx, `
		INSERT INTO news_ticker_preferences (user_id, enabled, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()`, userID, enabled)
	if err != nil {
		return fmt.Errorf("set news ticker preference: %w", err)
	}
	return nil
}
