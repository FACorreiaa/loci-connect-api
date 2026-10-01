package trip

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// Service is the one write path for a trip's plan. The Connect handler calls
// it, and so will the chat agent and Telegram (plan 2), so ownership, the
// version lock and the edit rules are checked in one place whoever asks.
type Service struct {
	trips Repository
	plans PlanRepository
	links flights.Provider
}

func NewService(trips Repository, plans PlanRepository, links flights.Provider) *Service {
	return &Service{trips: trips, plans: plans, links: links}
}

// editPlan loads the caller's trip, refuses a stale base version before doing
// any work, applies fn and saves. fn's error is returned as-is (it wraps
// ErrInvalidEdit), and nothing is saved.
func (s *Service) editPlan(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, fn func(*Trip) error) (*Trip, error) {
	t, err := s.trips.GetTrip(ctx, tripID, userID)
	if err != nil {
		return nil, err
	}
	if t.Version != baseVersion {
		return nil, ErrVersionConflict
	}
	if err := fn(t); err != nil {
		return nil, err
	}
	return s.plans.SavePlan(ctx, t, baseVersion)
}

func (s *Service) SetDates(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, start, end time.Time) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error { return applyDates(t, start, end) })
}

func (s *Service) SetStay(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, stay TripStay) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error { return upsertStay(t, stay) })
}

func (s *Service) ClearStay(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, city string) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error { return removeStay(t, city) })
}

// AddFlight saves f with links the server builds. Links a caller sends are
// dropped: they are shown to friends as tappable buttons, so they must be ours.
func (s *Service) AddFlight(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, f TripFlight) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error {
		added, err := appendFlight(t, f)
		if err != nil {
			return err
		}
		t.Flights[len(t.Flights)-1].Links = s.links.Links(FlightQuery(added))
		return nil
	})
}

func (s *Service) RemoveFlight(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, flightID uuid.UUID) (*Trip, error) {
	return s.editPlan(ctx, userID, tripID, baseVersion, func(t *Trip) error { return removeFlight(t, flightID) })
}

// FlightLinks is the manual form's search: links only, nothing saved.
func (s *Service) FlightLinks(q flights.Query) []flights.Link { return s.links.Links(q) }

// FlightQuery is the search a saved flight stands for.
func FlightQuery(f TripFlight) flights.Query {
	return flights.Query{
		Origin: f.Origin, Destination: f.Destination,
		Depart: f.DepartDate, Return: f.ReturnDate,
		Passengers: int(f.Passengers), Cabin: f.Cabin,
	}
}
