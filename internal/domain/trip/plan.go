package trip

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// ErrInvalidEdit wraps every rule a plan edit can break. The handler answers
// InvalidArgument for it; the chat agent (plan 2) turns it into a card that
// says what was wrong.
var ErrInvalidEdit = errors.New("invalid trip edit")

// MaxTripSpanDays is the longest trip, inclusive, matching pkg/tripspan.
const MaxTripSpanDays = 30

const maxPassengers = 9

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidEdit, fmt.Sprintf(format, a...))
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// applyDates dates the trip and stamps each day: day N falls on start+N-1.
// Days are never added or dropped here. A 3-day plan inside a 5-day trip is a
// real choice (arrive late, leave early); re-planning is a separate step the
// traveller asks for.
func applyDates(t *Trip, start, end time.Time) error {
	start, end = dateOnly(start), dateOnly(end)
	if end.Before(start) {
		return invalid("end date %s is before start date %s", end.Format(time.DateOnly), start.Format(time.DateOnly))
	}
	if span := int(end.Sub(start).Hours()/24) + 1; span > MaxTripSpanDays {
		return invalid("a trip spans at most %d days, these dates span %d", MaxTripSpanDays, span)
	}
	t.StartDate, t.EndDate = &start, &end
	for i := range t.Days {
		d := start.AddDate(0, 0, int(t.Days[i].DayNumber)-1)
		t.Days[i].Date = &d
	}
	return nil
}

func sameCity(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// HasCity reports whether name is one of the trip's cities: the primary city,
// a multi-city stop, or the city any day is spent in.
func (t *Trip) HasCity(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if sameCity(t.CityName, name) {
		return true
	}
	for _, c := range t.Cities {
		if sameCity(c.CityName, name) {
			return true
		}
	}
	for _, d := range t.Days {
		if sameCity(d.CityName, name) {
			return true
		}
	}
	return false
}

// upsertStay sets the stay for s.CityName, replacing any stay already set for
// that city however it was spelled.
func upsertStay(t *Trip, s TripStay) error {
	s.CityName = strings.TrimSpace(s.CityName)
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return invalid("a stay needs a name")
	}
	if !t.HasCity(s.CityName) {
		return invalid("%q is not a city on this trip", s.CityName)
	}
	if s.CheckIn != nil && s.CheckOut != nil && s.CheckOut.Before(*s.CheckIn) {
		return invalid("check-out is before check-in")
	}
	for i := range t.Stays {
		if sameCity(t.Stays[i].CityName, s.CityName) {
			s.ID = t.Stays[i].ID
			t.Stays[i] = s
			return nil
		}
	}
	t.Stays = append(t.Stays, s)
	return nil
}

func removeStay(t *Trip, city string) error {
	for i := range t.Stays {
		if sameCity(t.Stays[i].CityName, city) {
			t.Stays = append(t.Stays[:i], t.Stays[i+1:]...)
			return nil
		}
	}
	return invalid("no stay is set for %q", city)
}

// appendFlight adds f under a fresh id. Any id the caller set is discarded:
// ids are the server's, so RemoveFlight can trust them.
func appendFlight(t *Trip, f TripFlight) (TripFlight, error) {
	f.Origin.Name = strings.TrimSpace(f.Origin.Name)
	f.Destination.Name = strings.TrimSpace(f.Destination.Name)
	switch {
	case f.Origin.Name == "":
		return TripFlight{}, invalid("a flight needs an origin")
	case f.Destination.Name == "":
		return TripFlight{}, invalid("a flight needs a destination")
	case f.DepartDate.IsZero():
		return TripFlight{}, invalid("a flight needs a departure date")
	case f.ReturnDate != nil && f.ReturnDate.Before(f.DepartDate):
		return TripFlight{}, invalid("the return is before the departure")
	case f.Origin.IATA != "" && !flights.ValidIATA(f.Origin.IATA),
		f.Destination.IATA != "" && !flights.ValidIATA(f.Destination.IATA):
		return TripFlight{}, invalid("airport codes are three capital letters")
	case f.Passengers > maxPassengers:
		return TripFlight{}, invalid("at most %d passengers", maxPassengers)
	}
	if f.Passengers < 1 {
		f.Passengers = 1
	}
	f.ID = uuid.New()
	t.Flights = append(t.Flights, f)
	return f, nil
}

func removeFlight(t *Trip, id uuid.UUID) error {
	for i := range t.Flights {
		if t.Flights[i].ID == id {
			t.Flights = append(t.Flights[:i], t.Flights[i+1:]...)
			return nil
		}
	}
	return invalid("no flight %s on this trip", id)
}
