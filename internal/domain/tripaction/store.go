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

// Source is where a proposal came from.
type Source string

const (
	SourceChat   Source = "chat"
	SourceEditor Source = "editor"
)

// Proposal is an action waiting for the traveller. SessionID is uuid.Nil
// when the turn had no chat thread to confirm into. UserID is whoever
// proposed it: the owner for the planner's own cards, or a trip member.
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

	Source      Source    `json:"source"`
	BaseVersion int64     `json:"base_version"`
	AppliedBy   uuid.UUID `json:"applied_by"`
}

// Tally is how a trip's members reacted to a proposal; Mine is the
// viewer's own (1, -1 or 0).
type Tally struct {
	Up, Down, Mine int
}

// Board is the shared side of proposals, for trips planned together: any
// proposal by id, a trip's list, and reactions.
type Board interface {
	GetByID(ctx context.Context, id uuid.UUID) (*Proposal, error)
	// ListForTrip lists a trip's proposals, newest first: pending ones, and
	// with closedSince set, those applied or dismissed after it.
	ListForTrip(ctx context.Context, tripID uuid.UUID, closedSince *time.Time) ([]Proposal, error)
	// React sets userID's reaction; 0 removes it.
	React(ctx context.Context, proposalID, userID uuid.UUID, value int) error
	Tallies(ctx context.Context, ids []uuid.UUID, viewer uuid.UUID) (map[uuid.UUID]Tally, error)
	MarkApplied(ctx context.Context, id, by uuid.UUID) error
}

// Store keeps proposals. Transition is the one-shot guard: it moves a
// proposal from one status to another only if it is still in the first.
type Store interface {
	Create(ctx context.Context, p *Proposal) error
	Get(ctx context.Context, id, userID uuid.UUID) (*Proposal, error)
	Transition(ctx context.Context, id uuid.UUID, from, to Status) error
}

type pgStore struct{ db *pgxpool.Pool }

// NewStore is the Postgres Store; it is also the Board.
func NewStore(db *pgxpool.Pool) *pgStore { return &pgStore{db: db} }

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
	if p.Source == "" {
		p.Source = SourceChat
	}
	var base *int64
	if p.Source == SourceEditor {
		base = &p.BaseVersion
	}
	if err := s.db.QueryRow(ctx, `
		INSERT INTO trip_action_proposals (user_id, trip_id, session_id, action, options, summary, status, expires_at, source, base_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at`,
		p.UserID, p.TripID, nullable(p.SessionID), action, options, p.Summary, p.Status, p.ExpiresAt, p.Source, base).
		Scan(&p.ID, &p.CreatedAt); err != nil {
		return fmt.Errorf("store trip action: %w", err)
	}
	return nil
}

const proposalColumns = `id, user_id, trip_id, session_id, action, options, summary, status, created_at, expires_at,
	source, COALESCE(base_version, 0), applied_by`

func scanProposal(row pgx.Row) (*Proposal, error) {
	var (
		p                Proposal
		session, applied *uuid.UUID
		action, options  []byte
	)
	if err := row.Scan(&p.ID, &p.UserID, &p.TripID, &session, &action, &options, &p.Summary, &p.Status,
		&p.CreatedAt, &p.ExpiresAt, &p.Source, &p.BaseVersion, &applied); err != nil {
		return nil, err
	}
	if session != nil {
		p.SessionID = *session
	}
	if applied != nil {
		p.AppliedBy = *applied
	}
	if err := json.Unmarshal(action, &p.Action); err != nil {
		return nil, fmt.Errorf("unmarshal action: %w", err)
	}
	if err := json.Unmarshal(options, &p.Options); err != nil {
		return nil, fmt.Errorf("unmarshal options: %w", err)
	}
	return &p, nil
}

func (s *pgStore) Get(ctx context.Context, id, userID uuid.UUID) (*Proposal, error) {
	p, err := scanProposal(s.db.QueryRow(ctx, `SELECT `+proposalColumns+` FROM trip_action_proposals WHERE id = $1 AND user_id = $2`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get trip action: %w", err)
	}
	return p, nil
}

func (s *pgStore) GetByID(ctx context.Context, id uuid.UUID) (*Proposal, error) {
	p, err := scanProposal(s.db.QueryRow(ctx, `SELECT `+proposalColumns+` FROM trip_action_proposals WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get trip action: %w", err)
	}
	return p, nil
}

func (s *pgStore) ListForTrip(ctx context.Context, tripID uuid.UUID, closedSince *time.Time) ([]Proposal, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+proposalColumns+` FROM trip_action_proposals
		WHERE trip_id = $1 AND (status = 'pending' OR ($2::timestamptz IS NOT NULL AND COALESCE(resolved_at, created_at) > $2))
		ORDER BY created_at DESC LIMIT 100`, tripID, closedSince)
	if err != nil {
		return nil, fmt.Errorf("list trip actions: %w", err)
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trip action: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *pgStore) React(ctx context.Context, proposalID, userID uuid.UUID, value int) error {
	var err error
	if value == 0 {
		_, err = s.db.Exec(ctx, `DELETE FROM trip_proposal_reactions WHERE proposal_id = $1 AND user_id = $2`, proposalID, userID)
	} else {
		_, err = s.db.Exec(ctx, `
			INSERT INTO trip_proposal_reactions (proposal_id, user_id, value) VALUES ($1, $2, $3)
			ON CONFLICT (proposal_id, user_id) DO UPDATE SET value = EXCLUDED.value, created_at = NOW()`,
			proposalID, userID, value)
	}
	if err != nil {
		return fmt.Errorf("react to trip action: %w", err)
	}
	return nil
}

func (s *pgStore) Tallies(ctx context.Context, ids []uuid.UUID, viewer uuid.UUID) (map[uuid.UUID]Tally, error) {
	out := make(map[uuid.UUID]Tally, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT proposal_id,
		       COUNT(*) FILTER (WHERE value = 1),
		       COUNT(*) FILTER (WHERE value = -1),
		       COALESCE(MAX(value) FILTER (WHERE user_id = $2), 0)
		FROM trip_proposal_reactions WHERE proposal_id = ANY($1)
		GROUP BY proposal_id`, ids, viewer)
	if err != nil {
		return nil, fmt.Errorf("tally reactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id             uuid.UUID
			up, down, mine int
		)
		if err := rows.Scan(&id, &up, &down, &mine); err != nil {
			return nil, fmt.Errorf("scan tally: %w", err)
		}
		out[id] = Tally{Up: up, Down: down, Mine: mine}
	}
	return out, rows.Err()
}

func (s *pgStore) MarkApplied(ctx context.Context, id, by uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `UPDATE trip_action_proposals SET applied_by = $2 WHERE id = $1`, id, by); err != nil {
		return fmt.Errorf("mark trip action applied: %w", err)
	}
	return nil
}

func (s *pgStore) Transition(ctx context.Context, id uuid.UUID, from, to Status) error {
	ct, err := s.db.Exec(ctx, `
		UPDATE trip_action_proposals
		SET status = $3, resolved_at = CASE WHEN $3 = 'pending' THEN NULL ELSE NOW() END
		WHERE id = $1 AND status = $2`, id, from, to)
	if err != nil {
		return fmt.Errorf("trip action %s→%s: %w", from, to, err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotPending
	}
	return nil
}
