package gamification

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Feed tells a user's followers about a new city or a finished trip. Best
// effort; social.Feed satisfies it.
type Feed interface {
	PostNewCity(ctx context.Context, actor uuid.UUID, key string, cityID *uuid.UUID, name string)
	PostTripFinished(ctx context.Context, actor, tripID uuid.UUID)
}

// WithFeed posts new cities and finished trips to the following feed.
func (s *Service) WithFeed(f Feed) *Service {
	s.feed = f
	return s
}

// toFeed posts what a fresh award says to the feed, when it is news.
func (s *Service) toFeed(ctx context.Context, a Award) {
	if s.feed == nil {
		return
	}
	switch a.Kind {
	case KindNewCity:
		s.feed.PostNewCity(ctx, a.UserID, strings.TrimPrefix(a.RefKey, "city:"), a.CityID, a.Name)
	case KindTripCompleted:
		if id, err := uuid.Parse(strings.TrimPrefix(a.RefKey, "trip:")); err == nil {
			s.feed.PostTripFinished(ctx, a.UserID, id)
		}
	}
}
