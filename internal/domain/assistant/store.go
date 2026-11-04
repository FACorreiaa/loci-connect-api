package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Turn is one remembered line of a conversation. Plain text only: what the
// assistant looked up is not kept, because it may be stale by the next
// question and it can carry words written by other people.
type Turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// Spend is what one ask cost.
type Spend struct {
	InputTokens  int64
	OutputTokens int64
	CostMicroUSD int64
}

// Store keeps conversations and usage.
type Store interface {
	// ReserveAsk counts an ask against today's limit. False means the caller
	// has none left, and nothing was counted.
	ReserveAsk(ctx context.Context, userID uuid.UUID, day time.Time, limit int) (bool, error)
	AddSpend(ctx context.Context, userID uuid.UUID, day time.Time, spend Spend) error
	// DaySpendMicroUSD is what everybody has spent on the day.
	DaySpendMicroUSD(ctx context.Context, day time.Time) (int64, error)

	// LoadTurns returns a live conversation of the caller's. False means there
	// is none, which includes one that expired or belongs to somebody else.
	LoadTurns(ctx context.Context, userID, id uuid.UUID, now time.Time) ([]Turn, bool, error)
	// SaveTurns creates or replaces the conversation and returns its id. An id
	// that is not the caller's starts a new conversation.
	SaveTurns(ctx context.Context, userID uuid.UUID, id *uuid.UUID, turns []Turn, expiresAt time.Time) (uuid.UUID, error)
}

type pgStore struct{ db *pgxpool.Pool }

// NewStore creates the Postgres store.
func NewStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

func (s *pgStore) ReserveAsk(ctx context.Context, userID uuid.UUID, day time.Time, limit int) (bool, error) {
	var asks int
	err := s.db.QueryRow(ctx, `
		INSERT INTO assistant_daily_usage (user_id, day, asks)
		VALUES ($1, $2, 1)
		ON CONFLICT (user_id, day) DO UPDATE
		SET asks = assistant_daily_usage.asks + 1
		WHERE assistant_daily_usage.asks < $3
		RETURNING asks`, userID, day, limit).Scan(&asks)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reserve assistant ask: %w", err)
	}
	return true, nil
}

func (s *pgStore) AddSpend(ctx context.Context, userID uuid.UUID, day time.Time, spend Spend) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO assistant_daily_usage (user_id, day, input_tokens, output_tokens, cost_micro_usd)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, day) DO UPDATE
		SET input_tokens   = assistant_daily_usage.input_tokens + EXCLUDED.input_tokens,
		    output_tokens  = assistant_daily_usage.output_tokens + EXCLUDED.output_tokens,
		    cost_micro_usd = assistant_daily_usage.cost_micro_usd + EXCLUDED.cost_micro_usd`,
		userID, day, spend.InputTokens, spend.OutputTokens, spend.CostMicroUSD)
	if err != nil {
		return fmt.Errorf("add assistant spend: %w", err)
	}
	return nil
}

func (s *pgStore) DaySpendMicroUSD(ctx context.Context, day time.Time) (int64, error) {
	var total int64
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost_micro_usd), 0)::bigint FROM assistant_daily_usage WHERE day = $1`, day).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("read assistant day spend: %w", err)
	}
	return total, nil
}

func (s *pgStore) LoadTurns(ctx context.Context, userID, id uuid.UUID, now time.Time) ([]Turn, bool, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `
		SELECT messages FROM assistant_conversations
		WHERE id = $1 AND user_id = $2 AND expires_at > $3`, id, userID, now).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load assistant conversation: %w", err)
	}
	var turns []Turn
	if err := json.Unmarshal(raw, &turns); err != nil {
		return nil, false, fmt.Errorf("read assistant conversation: %w", err)
	}
	return turns, true, nil
}

func (s *pgStore) SaveTurns(ctx context.Context, userID uuid.UUID, id *uuid.UUID, turns []Turn, expiresAt time.Time) (uuid.UUID, error) {
	raw, err := json.Marshal(turns)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode assistant conversation: %w", err)
	}
	if id != nil {
		tag, err := s.db.Exec(ctx, `
			UPDATE assistant_conversations SET messages = $3, expires_at = $4, updated_at = now()
			WHERE id = $1 AND user_id = $2`, *id, userID, raw, expiresAt)
		if err != nil {
			return uuid.Nil, fmt.Errorf("update assistant conversation: %w", err)
		}
		if tag.RowsAffected() == 1 {
			return *id, nil
		}
	}
	// Expired conversations are deleted as new ones arrive: a day means a day
	// even for somebody who never comes back.
	if _, err := s.db.Exec(ctx, `DELETE FROM assistant_conversations WHERE expires_at < now()`); err != nil {
		return uuid.Nil, fmt.Errorf("expire assistant conversations: %w", err)
	}
	var created uuid.UUID
	err = s.db.QueryRow(ctx, `
		INSERT INTO assistant_conversations (user_id, messages, expires_at)
		VALUES ($1, $2, $3) RETURNING id`, userID, raw, expiresAt).Scan(&created)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create assistant conversation: %w", err)
	}
	return created, nil
}
