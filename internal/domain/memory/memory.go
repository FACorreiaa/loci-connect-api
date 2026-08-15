// Package memory exposes what Loci has learned about a person, with the
// evidence behind each belief, and lets them remove any of it.
//
// The learning loop was already complete and entirely opaque: signals went into
// preference_feedback, a 768-dimension vector and a set of taste traits came
// out, and the only control anyone had was a single button that erased
// everything. A trait could not be inspected, disputed, or removed on its own.
//
// The design borrows the property that makes a disposable index safe: derived
// state is rebuilt from its sources. Forgetting deletes evidence, then recomputes
// the vector and the traits from whatever remains — so removing one belief cannot
// leave the rest inconsistent with the record that produced them.
package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service reads and edits a user's learned profile.
type Service struct {
	db *pgxpool.Pool
	// recompute rebuilds the derived vector and traits for one user after
	// evidence is removed. Injected rather than imported so this package does
	// not depend on the reranker's construction.
	recompute func(ctx context.Context, userID uuid.UUID) error
}

// NewService wires the memory service. recompute may be nil, in which case
// forgetting deletes evidence but leaves the derived state to the next
// scheduled rerank — correct but slower to take effect.
func NewService(db *pgxpool.Pool, recompute func(context.Context, uuid.UUID) error) *Service {
	return &Service{db: db, recompute: recompute}
}

// Evidence is one recorded action that contributed to a belief.
type Evidence struct {
	ID         uuid.UUID `json:"id"`
	FeedbackID uuid.UUID `json:"feedback_id"`

	// Event is what the user did: saved, skipped, visited, favorited,
	// reordered, exported.
	Event string `json:"event"`
	// Weight is signed. A negative value means this action was evidence
	// against the trait — skipping something counts.
	Weight float64 `json:"weight"`

	POIID      string    `json:"poi_id,omitempty"`
	POIName    string    `json:"poi_name,omitempty"`
	CityName   string    `json:"city_name,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Trait is one thing Loci believes, with what taught it.
type Trait struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Score is -1..1; negative means an aversion, not a weak preference.
	Score float64 `json:"score"`
	// Confidence is 0..1 and rises with the number of signals.
	Confidence    float64    `json:"confidence"`
	EvidenceCount int        `json:"evidence_count"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Evidence      []Evidence `json:"evidence,omitempty"`
}

// Profile is everything Loci has learned, plus whether learning is switched on.
type Profile struct {
	Traits []Trait `json:"traits"`

	// PersonalizationEnabled reflects the user's own setting. When false, Loci
	// keeps the record but stops using it to rank anything.
	PersonalizationEnabled bool `json:"personalization_enabled"`

	// HasVector reports whether a preference embedding exists. It is derived
	// state: deleting it costs nothing that cannot be rebuilt.
	HasVector    bool       `json:"has_vector"`
	SignalCount  int        `json:"signal_count"`
	LastSignalAt *time.Time `json:"last_signal_at,omitempty"`

	GeneratedAt time.Time `json:"generated_at"`
}

// Get returns the user's learned profile.
//
// includeEvidence controls whether each trait carries the actions behind it.
// It is opt-in because the evidence join is much larger than the traits, and a
// caller rendering a summary should not pay for it.
func (s *Service) Get(ctx context.Context, userID uuid.UUID, includeEvidence bool) (*Profile, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("memory service is not configured with a database")
	}

	profile := &Profile{GeneratedAt: time.Now().UTC()}

	// Absence of a settings row means enabled, matching the column default.
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(
			(SELECT personalization_enabled FROM personalization_settings WHERE user_id = $1),
			TRUE)`, userID).Scan(&profile.PersonalizationEnabled); err != nil {
		return nil, fmt.Errorf("read personalization setting: %w", err)
	}

	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM user_preference_vectors WHERE user_id = $1)`, userID).
		Scan(&profile.HasVector); err != nil {
		return nil, fmt.Errorf("check preference vector: %w", err)
	}

	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)::int, MAX(created_at)
		FROM preference_feedback WHERE user_id = $1`, userID).
		Scan(&profile.SignalCount, &profile.LastSignalAt); err != nil {
		return nil, fmt.Errorf("summarize feedback: %w", err)
	}

	traits, err := s.traits(ctx, userID)
	if err != nil {
		return nil, err
	}
	profile.Traits = traits

	if includeEvidence {
		for i := range profile.Traits {
			evidence, err := s.evidenceFor(ctx, userID, profile.Traits[i].Key)
			if err != nil {
				return nil, err
			}
			profile.Traits[i].Evidence = evidence
		}
	}

	return profile, nil
}

func (s *Service) traits(ctx context.Context, userID uuid.UUID) ([]Trait, error) {
	rows, err := s.db.Query(ctx, `
		SELECT trait_key, label, score, confidence, evidence_count, updated_at
		FROM user_taste_traits
		WHERE user_id = $1
		ORDER BY confidence DESC, evidence_count DESC, trait_key`, userID)
	if err != nil {
		return nil, fmt.Errorf("list taste traits: %w", err)
	}
	defer rows.Close()

	traits := make([]Trait, 0)
	for rows.Next() {
		var t Trait
		if err := rows.Scan(&t.Key, &t.Label, &t.Score, &t.Confidence,
			&t.EvidenceCount, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan taste trait: %w", err)
		}
		traits = append(traits, t)
	}
	return traits, rows.Err()
}

// evidenceFor returns the actions behind one trait, newest first, joined to the
// place they concerned so the user reads "you saved Bar Alta" rather than a uuid.
func (s *Service) evidenceFor(ctx context.Context, userID uuid.UUID, traitKey string) ([]Evidence, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			e.id,
			e.feedback_id,
			pf.event,
			e.weight,
			COALESCE(pf.poi_id, ''),
			COALESCE(p.name, ''),
			COALESCE(c.name, ''),
			e.occurred_at
		FROM taste_trait_evidence e
		JOIN preference_feedback pf ON pf.id = e.feedback_id
		LEFT JOIN points_of_interest p ON p.id::text = pf.poi_id
		LEFT JOIN cities c ON c.id = p.city_id
		WHERE e.user_id = $1 AND e.trait_key = $2
		ORDER BY e.occurred_at DESC`, userID, traitKey)
	if err != nil {
		return nil, fmt.Errorf("list trait evidence: %w", err)
	}
	defer rows.Close()

	evidence := make([]Evidence, 0)
	for rows.Next() {
		var e Evidence
		if err := rows.Scan(&e.ID, &e.FeedbackID, &e.Event, &e.Weight,
			&e.POIID, &e.POIName, &e.CityName, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan trait evidence: %w", err)
		}
		evidence = append(evidence, e)
	}
	return evidence, rows.Err()
}

// ForgetTrait removes a belief and the signals that produced it.
//
// The underlying preference_feedback rows are deleted, not just the trait: a
// trait deleted on its own would be rebuilt by the next rerank from the same
// signals, so "forget this" has to reach the evidence or it is theatre.
//
// Returns the number of signals removed.
func (s *Service) ForgetTrait(ctx context.Context, userID uuid.UUID, traitKey string) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("memory service is not configured with a database")
	}
	if traitKey == "" {
		return 0, fmt.Errorf("trait key is required")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin forget trait: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Deleting the feedback cascades to taste_trait_evidence.
	tag, err := tx.Exec(ctx, `
		DELETE FROM preference_feedback
		WHERE id IN (
			SELECT feedback_id FROM taste_trait_evidence
			WHERE user_id = $1 AND trait_key = $2
		)`, userID, traitKey)
	if err != nil {
		return 0, fmt.Errorf("delete trait feedback: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM user_taste_traits WHERE user_id = $1 AND trait_key = $2`,
		userID, traitKey); err != nil {
		return 0, fmt.Errorf("delete trait: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit forget trait: %w", err)
	}

	removed := int(tag.RowsAffected())
	s.rebuild(ctx, userID)
	return removed, nil
}

// ForgetEvidence removes a single recorded action.
//
// Finer-grained than ForgetTrait: it lets a user say "that one save was a
// mistake" without discarding an otherwise accurate belief.
func (s *Service) ForgetEvidence(ctx context.Context, userID, feedbackID uuid.UUID) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("memory service is not configured with a database")
	}

	// Scoped to the owner: a feedback id from another account must not be
	// deletable by guessing it.
	tag, err := s.db.Exec(ctx, `
		DELETE FROM preference_feedback WHERE id = $1 AND user_id = $2`,
		feedbackID, userID)
	if err != nil {
		return fmt.Errorf("delete feedback: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	s.rebuild(ctx, userID)
	return nil
}

// rebuild recomputes derived state after a deletion. Best-effort: the record of
// truth is already correct, and the next scheduled rerank will converge the
// derived state even if this call fails.
func (s *Service) rebuild(ctx context.Context, userID uuid.UUID) {
	if s.recompute == nil {
		return
	}
	_ = s.recompute(ctx, userID)
}

// ErrNotFound is returned when the requested item does not exist or does not
// belong to the caller. The two cases are deliberately indistinguishable.
var ErrNotFound = fmt.Errorf("not found")
