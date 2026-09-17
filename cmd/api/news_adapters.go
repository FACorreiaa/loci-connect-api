package api

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/travelhistory"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/user"
)

// Adapters that answer the news ticker's three questions from the user, trip
// and travel-history domains. They live here because localcontext cannot
// import trip (trip imports it for packing) and should not learn the other
// two just for this.

type newsHomeAdapter struct{ users user.UserRepo }

func (a newsHomeAdapter) HomeCountry(ctx context.Context, userID uuid.UUID) (string, error) {
	profile, err := a.users.GetUserByID(ctx, userID)
	if err != nil || profile == nil || profile.Country == nil {
		return "", err
	}
	return strings.TrimSpace(*profile.Country), nil
}

type newsNextTripAdapter struct {
	trips  trip.Repository
	cities localcontext.CityResolver
}

// NextTripCountry is the country of the soonest trip that has a day today or
// later, resolved through the city catalogue.
func (a newsNextTripAdapter) NextTripCountry(ctx context.Context, userID uuid.UUID, now time.Time) (string, error) {
	trips, _, err := a.trips.ListTrips(ctx, userID, 50, 0)
	if err != nil {
		return "", err
	}
	var best *trip.Trip
	var bestDate time.Time
	today := now.Truncate(24 * time.Hour)
	for _, t := range trips {
		for _, d := range t.Days {
			if d.Date == nil || d.Date.Before(today) {
				continue
			}
			if best == nil || d.Date.Before(bestDate) {
				best, bestDate = t, *d.Date
			}
		}
	}
	if best == nil || a.cities == nil || strings.TrimSpace(best.CityName) == "" {
		return "", nil
	}
	resolved, err := a.cities.Resolve(ctx, cityrepo.ResolveQuery{Name: best.CityName})
	if err != nil || resolved == nil {
		return "", err
	}
	return resolved.City.Country, nil
}

type newsVisitedAdapter struct{ history travelhistory.Repository }

func (a newsVisitedAdapter) RecentCountries(ctx context.Context, userID uuid.UUID, limit int) ([]localcontext.VisitedCountry, error) {
	cities, _, err := a.history.ListVisitedCities(ctx, userID, limit, 0)
	if err != nil {
		return nil, err
	}
	out := make([]localcontext.VisitedCountry, 0, len(cities))
	for _, c := range cities {
		if c.CountryCode == nil || *c.CountryCode == "" {
			continue
		}
		out = append(out, localcontext.VisitedCountry{Code: *c.CountryCode, At: c.FirstVisitAt})
	}
	return out, nil
}
