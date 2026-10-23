package trip

import (
	"context"

	"github.com/google/uuid"
)

// The stop edits, as plain functions on a loaded trip. The Connect handler's
// AddStop/RemoveStop/ReplaceStop/ReorderStops and Service's methods (which a
// confirmed proposal goes through) share them, so a co-planner's suggestion
// edits the trip exactly the way the owner's own tap does.

// ErrStopNotFound and ErrDayNotFound wrap ErrInvalidEdit: the edit names a
// stop or day the trip no longer has.
var (
	ErrStopNotFound = invalid("stop not found")
	ErrDayNotFound  = invalid("day not found in trip")
)

func dayIndex(t *Trip, dayID uuid.UUID) int {
	for i := range t.Days {
		if t.Days[i].ID == dayID {
			return i
		}
	}
	return -1
}

func stopIndex(t *Trip, stopID uuid.UUID) (int, int) {
	for di := range t.Days {
		for si := range t.Days[di].Stops {
			if t.Days[di].Stops[si].ID == stopID {
				return di, si
			}
		}
	}
	return -1, -1
}

func renumber(stops []TripStop) {
	for i := range stops {
		stops[i].OrderIndex = int32(i)
	}
}

// FindStop is the trip's stop with stopID, and the day it is on.
func FindStop(t *Trip, stopID uuid.UUID) (*TripStop, *TripDay) {
	di, si := stopIndex(t, stopID)
	if di < 0 {
		return nil, nil
	}
	return &t.Days[di].Stops[si], &t.Days[di]
}

// FindDay is the trip's day with dayID.
func FindDay(t *Trip, dayID uuid.UUID) *TripDay {
	if i := dayIndex(t, dayID); i >= 0 {
		return &t.Days[i]
	}
	return nil
}

// addStop appends stop to the end of a day.
func addStop(t *Trip, dayID uuid.UUID, stop TripStop) error {
	di := dayIndex(t, dayID)
	if di < 0 {
		return ErrDayNotFound
	}
	stop.OrderIndex = int32(len(t.Days[di].Stops))
	t.Days[di].Stops = append(t.Days[di].Stops, stop)
	return nil
}

// removeStop takes a stop off its day and returns it.
func removeStop(t *Trip, stopID uuid.UUID) (TripStop, error) {
	di, si := stopIndex(t, stopID)
	if di < 0 {
		return TripStop{}, ErrStopNotFound
	}
	removed := t.Days[di].Stops[si]
	t.Days[di].Stops = append(t.Days[di].Stops[:si], t.Days[di].Stops[si+1:]...)
	renumber(t.Days[di].Stops)
	return removed, nil
}

// replaceStop puts replacement where stopID was, keeping its place in the day.
func replaceStop(t *Trip, stopID uuid.UUID, replacement TripStop) error {
	di, si := stopIndex(t, stopID)
	if di < 0 {
		return ErrStopNotFound
	}
	replacement.OrderIndex = t.Days[di].Stops[si].OrderIndex
	t.Days[di].Stops[si] = replacement
	return nil
}

// moveStop takes a stop to toDay at toIndex (past the end appends). The stop
// keeps its id, notes and times.
func moveStop(t *Trip, stopID, toDayID uuid.UUID, toIndex int) error {
	di, si := stopIndex(t, stopID)
	if di < 0 {
		return ErrStopNotFound
	}
	to := dayIndex(t, toDayID)
	if to < 0 {
		return ErrDayNotFound
	}
	stop := t.Days[di].Stops[si]
	t.Days[di].Stops = append(t.Days[di].Stops[:si], t.Days[di].Stops[si+1:]...)
	renumber(t.Days[di].Stops)
	stops := t.Days[to].Stops
	if toIndex < 0 {
		toIndex = 0
	}
	if toIndex > len(stops) {
		toIndex = len(stops)
	}
	stops = append(stops[:toIndex], append([]TripStop{stop}, stops[toIndex:]...)...)
	renumber(stops)
	t.Days[to].Stops = stops
	return nil
}

// editDays loads the owner's trip, refuses a stale base version, applies fn
// and saves the whole trip (days included; SavePlan writes only the plan).
func (s *Service) editDays(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, fn func(*Trip) error) (*Trip, error) {
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
	return s.trips.SaveTrip(ctx, t, baseVersion)
}

func (s *Service) AddStop(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, dayID uuid.UUID, stop TripStop) (*Trip, error) {
	return s.editDays(ctx, userID, tripID, baseVersion, func(t *Trip) error { return addStop(t, dayID, stop) })
}

func (s *Service) RemoveStop(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, stopID uuid.UUID) (*Trip, error) {
	return s.editDays(ctx, userID, tripID, baseVersion, func(t *Trip) error {
		_, err := removeStop(t, stopID)
		return err
	})
}

func (s *Service) ReplaceStop(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, stopID uuid.UUID, replacement TripStop) (*Trip, error) {
	return s.editDays(ctx, userID, tripID, baseVersion, func(t *Trip) error { return replaceStop(t, stopID, replacement) })
}

func (s *Service) MoveStop(ctx context.Context, userID, tripID uuid.UUID, baseVersion int64, stopID, toDayID uuid.UUID, toIndex int) (*Trip, error) {
	return s.editDays(ctx, userID, tripID, baseVersion, func(t *Trip) error { return moveStop(t, stopID, toDayID, toIndex) })
}
