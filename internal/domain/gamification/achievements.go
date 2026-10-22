package gamification

import (
	"context"
	"log/slog"
	"sync"

	achievementv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/achievement"
	gamificationv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gamification"
	"github.com/google/uuid"
)

// Achiever is the achievements layer, which judges a user on what the field
// score and the rest of the app already recorded. achievements.Service
// satisfies it; it lives in its own package and imports this one.
type Achiever interface {
	// Evaluate grants what userID has newly earned and returns it, one entry
	// per achievement.
	Evaluate(ctx context.Context, userID uuid.UUID) ([]*achievementv1.Achievement, error)
	// List is owner's whole catalogue as viewer may see it.
	List(ctx context.Context, viewer, owner uuid.UUID) (*gamificationv1.ListAchievementsResponse, error)
	MarkSeen(ctx context.Context, userID uuid.UUID, ids []string) error
	// BackfillBatch evaluates up to limit users whose past was never
	// evaluated, silently.
	BackfillBatch(ctx context.Context, limit int) (int, error)
	// RefreshRarity recounts holders when the last count is stale.
	RefreshRarity(ctx context.Context) error
}

// WithAchievements judges achievements after every award, in place of the
// old badges.
func (s *Service) WithAchievements(a Achiever) *Service {
	s.achiever = a
	return s
}

// evaluate runs the achievement check after an award. It never fails the
// award, and what it unlocks also goes to the request's collector.
func (s *Service) evaluate(ctx context.Context, userID uuid.UUID) []*achievementv1.Achievement {
	if s.achiever == nil {
		return nil
	}
	got, err := s.achiever.Evaluate(ctx, userID)
	if err != nil {
		s.log.Warn("achievement check failed", slog.String("user_id", userID.String()), slog.Any("error", err))
		return nil
	}
	if c, ok := ctx.Value(unlocksKey{}).(*unlocks); ok {
		c.add(got)
	}
	return got
}

type unlocksKey struct{}

// unlocks gathers what one request unlocked. A request can award several
// times (a stop, its day, the trip), and some awards are made by other
// packages on the same request, so the handler collects them all here.
type unlocks struct {
	mu  sync.Mutex
	got []*achievementv1.Achievement
}

// collectUnlocks returns a context whose awards report their unlocks to the
// returned collector.
func collectUnlocks(ctx context.Context) (context.Context, *unlocks) {
	c := &unlocks{}
	return context.WithValue(ctx, unlocksKey{}, c), c
}

func (c *unlocks) add(as []*achievementv1.Achievement) {
	c.mu.Lock()
	defer c.mu.Unlock()
next:
	for _, a := range as {
		for i, had := range c.got {
			if had.GetId() == a.GetId() {
				// A later evaluation sees every tier the earlier one did.
				c.got[i] = a
				continue next
			}
		}
		c.got = append(c.got, a)
	}
}

func (c *unlocks) list() []*achievementv1.Achievement {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*achievementv1.Achievement(nil), c.got...)
}
