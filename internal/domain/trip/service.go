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

// FlightLinks is the manual form's search: links only, nothing saved. It
// checks the query as AddFlight would, since the agent calls it directly.
func (s *Service) FlightLinks(q flights.Query) ([]flights.Link, error) {
	if err := validateQuery(q); err != nil {
		return nil, err
	}
	if q.Passengers < 1 {
		q.Passengers = 1
	}
	return s.links.Links(q), nil
}

// FlightQuery is the search a saved flight stands for.
func FlightQuery(f TripFlight) flights.Query {
	return flights.Query{
		Origin: f.Origin, Destination: f.Destination,
		Depart: f.DepartDate, Return: f.ReturnDate,
		Passengers: int(f.Passengers), Cabin: f.Cabin,
	}
}

// ReplaceDays swaps a trip's days for a re-planned set (the chat agent's
// "make it 6 days"), keeping its dates, stays and flights. Days are
// renumbered 1..n as new days and, when the trip is dated, dated from its
// start, so the calendar follows the new plan.
func (s *Service) ReplaceDays(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, days []TripDay) (*Trip, error) {
	if len(days) == 0 || len(days) > MaxTripSpanDays {
		return nil, invalid("a plan has 1 to %d days", MaxTripSpanDays)
	}
	t, err := s.trips.GetTrip(ctx, tripID, userID)
	if err != nil {
		return nil, err
	}
	if t.Version != baseVersion {
		return nil, ErrVersionConflict
	}
	t.Days = make([]TripDay, len(days))
	for i, d := range days {
		d.ID = uuid.Nil
		d.DayNumber = int32(i + 1)
		d.Date = nil
		if t.StartDate != nil {
			at := t.StartDate.AddDate(0, 0, i)
			d.Date = &at
		}
		stops := make([]TripStop, len(d.Stops))
		for j, st := range d.Stops {
			st.ID = uuid.Nil
			stops[j] = st
		}
		d.Stops = stops
		t.Days[i] = d
	}
	return s.trips.SaveTrip(ctx, t, baseVersion)
}
