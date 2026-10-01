package tripaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

var (
	// ErrNotFound: no such proposal, or not the caller's.
	ErrNotFound = errors.New("trip action not found")
	// ErrNotPending: already applied or dismissed.
	ErrNotPending = errors.New("trip action was already applied or dismissed")
	// ErrExpired: older than proposalTTL; the trip has likely moved on.
	ErrExpired = errors.New("trip action has expired")
	// ErrNoOption: a pick-one action applied without a valid option.
	ErrNoOption = errors.New("pick one of the action's options")
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusApplied   Status = "applied"
	StatusDismissed Status = "dismissed"
)

// Option is one choice of a pick-one action.
type Option struct {
	Label  string           `json:"label"`
	Detail string           `json:"detail,omitempty"`
	Stay   *trip.TripStay   `json:"stay,omitempty"`
	Flight *trip.TripFlight `json:"flight,omitempty"`
}

// Proposal is an action waiting for the traveller. SessionID is uuid.Nil
// when the turn had no chat thread to confirm into.
type Proposal struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	TripID    uuid.UUID `json:"trip_id"`
	SessionID uuid.UUID `json:"session_id"`
	Action    Action    `json:"action"`
	Summary   string    `json:"summary"`
	Options   []Option  `json:"options"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Store keeps proposals. Transition is the one-shot guard: it moves a
// proposal from one status to another only if it is still in the first.
type Store interface {
	Create(ctx context.Context, p *Proposal) error
	Get(ctx context.Context, id, userID uuid.UUID) (*Proposal, error)
	Transition(ctx context.Context, id uuid.UUID, from, to Status) error
}

type pgStore struct{ db *pgxpool.Pool }

// NewStore is the Postgres Store.
func NewStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

func nullable(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func (s *pgStore) Create(ctx context.Context, p *Proposal) error {
	action, err := json.Marshal(p.Action)
	if err != nil {
		return fmt.Errorf("marshal action: %w", err)
	}
	options := []byte("[]")
	if len(p.Options) > 0 {
		if options, err = json.Marshal(p.Options); err != nil {
			return fmt.Errorf("marshal options: %w", err)
		}
	}
	p.Status = StatusPending
	if err := s.db.QueryRow(ctx, `
		INSERT INTO trip_action_proposals (user_id, trip_id, session_id, action, options, summary, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`,
		p.UserID, p.TripID, nullable(p.SessionID), action, options, p.Summary, p.Status, p.ExpiresAt).
		Scan(&p.ID, &p.CreatedAt); err != nil {
		return fmt.Errorf("store trip action: %w", err)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id, userID uuid.UUID) (*Proposal, error) {
	var (
		p               Proposal
		session         *uuid.UUID
		action, options []byte
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, trip_id, session_id, action, options, summary, status, created_at, expires_at
		FROM trip_action_proposals WHERE id = $1 AND user_id = $2`, id, userID).
		Scan(&p.ID, &p.UserID, &p.TripID, &session, &action, &options, &p.Summary, &p.Status, &p.CreatedAt, &p.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get trip action: %w", err)
	}
	if session != nil {
		p.SessionID = *session
	}
	if err := json.Unmarshal(action, &p.Action); err != nil {
		return nil, fmt.Errorf("unmarshal action: %w", err)
	}
	if err := json.Unmarshal(options, &p.Options); err != nil {
		return nil, fmt.Errorf("unmarshal options: %w", err)
	}
	return &p, nil
}

func (s *pgStore) Transition(ctx context.Context, id uuid.UUID, from, to Status) error {
	ct, err := s.db.Exec(ctx, `UPDATE trip_action_proposals SET status = $3 WHERE id = $1 AND status = $2`, id, from, to)
	if err != nil {
		return fmt.Errorf("trip action %s→%s: %w", from, to, err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotPending
	}
	return nil
}
