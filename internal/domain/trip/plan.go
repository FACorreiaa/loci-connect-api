package trip

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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

// The proto's field limits, repeated here because protovalidate only runs on
// Connect requests. The chat agent calls Service directly, and a stored value
// the proto would reject makes every later SaveTrip that sends the draft back
// fail validation. Keep these at least as strict as trip.proto.
const (
	maxCityName   = 200
	maxStayName   = 300
	maxStarRating = 10
	maxPOIID      = 100
	maxBookingURL = 2000
	maxPlaceName  = 200
	maxCarrier    = 100
	maxFlightNo   = 20
	maxPriceText  = 50
	maxNotes      = 1000
)

func tooLong(s string, limit int) bool { return utf8.RuneCountInString(s) > limit }

func tooLongPtr(s *string, limit int) bool { return s != nil && tooLong(*s, limit) }

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
	_, ok := t.cityNamed(name)
	return ok
}

// cityNamed is the trip's own spelling of name, matched loosely.
func (t *Trip) cityNamed(name string) (string, bool) {
	if strings.TrimSpace(name) == "" {
		return "", false
	}
	if sameCity(t.CityName, name) {
		return t.CityName, true
	}
	for _, c := range t.Cities {
		if sameCity(c.CityName, name) {
			return c.CityName, true
		}
	}
	for _, d := range t.Days {
		if sameCity(d.CityName, name) {
			return d.CityName, true
		}
	}
	return "", false
}

// upsertStay sets the stay for s.CityName, replacing any stay already set for
// that city however it was spelled. The stay is stored under the trip's own
// spelling of the city, so clients matching stays to cities by name find it.
func upsertStay(t *Trip, s TripStay) error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return invalid("a stay needs a name")
	}
	city, ok := t.cityNamed(s.CityName)
	if !ok {
		return invalid("%q is not a city on this trip", strings.TrimSpace(s.CityName))
	}
	s.CityName = city
	switch {
	case tooLong(s.CityName, maxCityName), tooLong(s.Name, maxStayName),
		tooLong(s.StarRating, maxStarRating), tooLong(s.POIID, maxPOIID):
		return invalid("a stay field is too long")
	case s.BookingURL != nil && (!strings.HasPrefix(*s.BookingURL, "https://") || tooLong(*s.BookingURL, maxBookingURL)):
		return invalid("a booking link must be an https:// address of at most %d characters", maxBookingURL)
	case s.CheckIn != nil && s.CheckOut != nil && s.CheckOut.Before(*s.CheckIn):
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

// validateQuery is what a flight search needs, whether it is saved
// (appendFlight) or only linked to (Service.FlightLinks).
func validateQuery(q flights.Query) error {
	switch {
	case strings.TrimSpace(q.Origin.Name) == "":
		return invalid("a flight needs an origin")
	case strings.TrimSpace(q.Destination.Name) == "":
		return invalid("a flight needs a destination")
	case tooLong(q.Origin.Name, maxPlaceName), tooLong(q.Destination.Name, maxPlaceName):
		return invalid("a place name is longer than %d characters", maxPlaceName)
	case q.Depart.IsZero():
		return invalid("a flight needs a departure date")
	case q.Return != nil && q.Return.Before(q.Depart):
		return invalid("the return is before the departure")
	case q.Origin.IATA != "" && !flights.ValidIATA(q.Origin.IATA),
		q.Destination.IATA != "" && !flights.ValidIATA(q.Destination.IATA):
		return invalid("airport codes are three capital letters")
	case q.Passengers > maxPassengers:
		return invalid("at most %d passengers", maxPassengers)
	}
	return nil
}

// appendFlight adds f under a fresh id. Any id the caller set is discarded:
// ids are the server's, so RemoveFlight can trust them.
func appendFlight(t *Trip, f TripFlight) (TripFlight, error) {
	f.Origin.Name = strings.TrimSpace(f.Origin.Name)
	f.Destination.Name = strings.TrimSpace(f.Destination.Name)
	if err := validateQuery(FlightQuery(f)); err != nil {
		return TripFlight{}, err
	}
	if tooLongPtr(f.Carrier, maxCarrier) || tooLongPtr(f.FlightNo, maxFlightNo) ||
		tooLongPtr(f.PriceText, maxPriceText) || tooLongPtr(f.Notes, maxNotes) {
		return TripFlight{}, invalid("a flight field is too long")
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
