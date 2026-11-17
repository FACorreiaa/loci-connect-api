package hotelcompare

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// TripSource reads the traveller's own trips (trip.Repository).
type TripSource interface {
	GetTrip(ctx context.Context, id, userID uuid.UUID) (*trip.Trip, error)
	ListTrips(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*trip.Trip, int, error)
}

// maxTripsScanned is how many of the traveller's trips are looked through
// for the one a report's city is on.
const maxTripsScanned = 50

// TripFor is the trip a report's hotels are offered to. When the chat is
// about a trip (preferred), it is that trip or none: a stay is not quietly
// put on another one. Otherwise it is the traveller's only trip that has the
// city and is not over. With none, or with several to choose between, it is
// uuid.Nil and the client asks which trip.
func TripFor(ctx context.Context, src TripSource, userID, preferred uuid.UUID, city string, now time.Time) uuid.UUID {
	if src == nil || city == "" {
		return uuid.Nil
	}
	if preferred != uuid.Nil {
		if t, err := src.GetTrip(ctx, preferred, userID); err == nil && t.HasCity(city) {
			return t.ID
		}
		return uuid.Nil
	}
	trips, _, err := src.ListTrips(ctx, userID, maxTripsScanned, 0)
	if err != nil {
		return uuid.Nil
	}
	today := now.UTC().Truncate(24 * time.Hour)
	found := uuid.Nil
	for _, t := range trips {
		if t.UserID != userID || !t.HasCity(city) || (t.EndDate != nil && t.EndDate.Before(today)) {
			continue
		}
		if found != uuid.Nil {
			return uuid.Nil
		}
		found = t.ID
	}
	return found
}
