package ranking

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
)

// defaultLimit and maxLimit bound a list.
const (
	defaultLimit = 100
	maxLimit     = 200
)

// Repository stores rankings. Rate, Compare and Delete each run in one
// transaction and keep every bucket's positions dense (0..n-1).
type Repository interface {
	Rate(ctx context.Context, userID uuid.UUID, in RateInput) (Place, *Comparison, error)
	Compare(ctx context.Context, userID uuid.UUID, poiID, otherID string, better bool) (Place, *Comparison, error)
	Delete(ctx context.Context, userID uuid.UUID, poiID string) (bool, error)
	List(ctx context.Context, userID uuid.UUID, city string, limit int) ([]Place, error)
}

// FriendChecker answers whether two travellers are friends; *social.Service
// satisfies it.
type FriendChecker interface {
	Relation(ctx context.Context, a, b uuid.UUID) (friends, blocked bool, err error)
}

// Service is the ranking domain.
type Service struct {
	repo    Repository
	friends FriendChecker
	taste   preference.Recorder
	kicker  preference.Kicker
	log     *slog.Logger
}

// NewService builds the ranking service. taste and kicker may be nil, in which
// case verdicts do not teach taste.
func NewService(repo Repository, friends FriendChecker, taste preference.Recorder, kicker preference.Kicker, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, friends: friends, taste: taste, kicker: kicker, log: log.With(slog.String("component", "ranking"))}
}

// RatePlace gives a place a verdict (or a new one) and returns the first
// comparison, if its bucket already holds other places.
func (s *Service) RatePlace(ctx context.Context, userID uuid.UUID, in RateInput) (Place, *Comparison, error) {
	place, next, err := s.repo.Rate(ctx, userID, in)
	if err != nil {
		return Place{}, nil, fmt.Errorf("rate place: %w", err)
	}
	if s.taste != nil {
		event, weight := TasteSignal(in.Verdict)
		s.taste.Replace(ctx, userID, TasteKey, in.POIID, event, preference.RecordOpts{
			POIID:    in.POIID,
			Weight:   weight,
			Metadata: map[string]any{TasteKey: in.POIID},
		})
		s.kick(userID)
	}
	return place, next, nil
}

// ComparePlaces answers one comparison for a place still being ranked.
func (s *Service) ComparePlaces(ctx context.Context, userID uuid.UUID, poiID, otherID string, better bool) (Place, *Comparison, error) {
	return s.repo.Compare(ctx, userID, poiID, otherID, better)
}

// Delete removes a ranking and the taste signal it produced.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID, poiID string) (bool, error) {
	deleted, err := s.repo.Delete(ctx, userID, poiID)
	if err != nil {
		return false, fmt.Errorf("delete ranking: %w", err)
	}
	if deleted && s.taste != nil {
		s.taste.Forget(ctx, userID, TasteKey, poiID)
		s.kick(userID)
	}
	return deleted, nil
}

// ListMine lists the caller's own rankings.
func (s *Service) ListMine(ctx context.Context, userID uuid.UUID, city string, limit int) ([]Place, error) {
	return s.repo.List(ctx, userID, city, clampLimit(limit))
}

// ListFor lists owner's rankings for viewer, who must be owner or a friend
// owner has not blocked (and who has not blocked owner).
func (s *Service) ListFor(ctx context.Context, viewer, owner uuid.UUID, city string, limit int) ([]Place, error) {
	if viewer != owner {
		if s.friends == nil {
			return nil, ErrNotFriends
		}
		friends, blocked, err := s.friends.Relation(ctx, viewer, owner)
		if err != nil {
			return nil, fmt.Errorf("check friendship: %w", err)
		}
		if !friends || blocked {
			return nil, ErrNotFriends
		}
	}
	return s.repo.List(ctx, owner, city, clampLimit(limit))
}

func (s *Service) kick(userID uuid.UUID) {
	if s.kicker != nil {
		s.kicker.Kick(userID)
	}
}

func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultLimit
	case limit > maxLimit:
		return maxLimit
	}
	return limit
}
