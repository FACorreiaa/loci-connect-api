// Package tripaction is the chat agent's write path into a trip: it turns a
// message into typed actions, offers them as proposals, and applies the one
// the traveller confirms through trip.Service. The agent proposes; nothing
// changes until the traveller says yes.
package tripaction

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// Kind names what an action changes.
type Kind string

const (
	KindSetDates       Kind = "set_dates"
	KindSearchHotels   Kind = "search_hotels"
	KindRegenerateDays Kind = "regenerate_days"
	KindSearchFlights  Kind = "search_flights"
)

// Place is one end of a flight as the extractor names it.
type Place struct {
	Name string `json:"name"`
	IATA string `json:"iata,omitempty"`
}

// Action is one change the agent proposes. Only the fields its Kind uses
// are set. It is also the JSON shape the extractor is asked for.
type Action struct {
	Kind Kind `json:"kind"`

	StartDate string `json:"start_date,omitempty"` // set_dates
	EndDate   string `json:"end_date,omitempty"`

	City     string `json:"city,omitempty"` // search_hotels
	MinStars int    `json:"min_stars,omitempty"`
	MaxStars int    `json:"max_stars,omitempty"`

	Days int `json:"days,omitempty"` // regenerate_days

	Origin      Place  `json:"origin,omitzero"` // search_flights
	Destination Place  `json:"destination,omitzero"`
	Depart      string `json:"depart,omitempty"`
	Return      string `json:"return,omitempty"`
	Passengers  int    `json:"passengers,omitempty"`
	Cabin       string `json:"cabin,omitempty"`
}

var errInvalid = errors.New("invalid trip action")

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, a...))
}

// cabins are the cabin words the extractor may use.
var cabins = map[string]flights.Cabin{
	"economy":         flights.CabinEconomy,
	"premium_economy": flights.CabinPremiumEconomy,
	"business":        flights.CabinBusiness,
	"first":           flights.CabinFirst,
}

const maxPassengers = 9

// normalize checks an extracted action against the trip and fills what the
// trip makes obvious. An action that fails is dropped, never applied: the
// model's output is a suggestion, and the rules here are the same ones
// trip.Service enforces when the traveller confirms.
func normalize(a Action, cities []string) (Action, error) {
	switch a.Kind {
	case KindSetDates:
		start, err := time.Parse(time.DateOnly, a.StartDate)
		if err != nil {
			return a, bad("start date %q", a.StartDate)
		}
		end, err := time.Parse(time.DateOnly, a.EndDate)
		if err != nil {
			return a, bad("end date %q", a.EndDate)
		}
		if end.Before(start) {
			return a, bad("end date before start date")
		}
		if span := int(end.Sub(start).Hours()/24) + 1; span > trip.MaxTripSpanDays {
			return a, bad("%d days is longer than a trip", span)
		}
	case KindSearchHotels:
		city, ok := pickCity(a.City, cities)
		if !ok {
			return a, bad("%q is not a city on this trip", a.City)
		}
		a.City = city
		if a.MinStars < 0 || a.MinStars > 5 || a.MaxStars < 0 || a.MaxStars > 5 {
			return a, bad("stars run from 1 to 5")
		}
		if a.MaxStars == 0 {
			a.MaxStars = 5
		}
		if a.MinStars > a.MaxStars {
			return a, bad("min stars above max stars")
		}
	case KindRegenerateDays:
		if a.Days < 1 || a.Days > trip.MaxTripSpanDays {
			return a, bad("a plan has 1 to %d days, not %d", trip.MaxTripSpanDays, a.Days)
		}
	case KindSearchFlights:
		a.Origin.Name = strings.TrimSpace(a.Origin.Name)
		a.Destination.Name = strings.TrimSpace(a.Destination.Name)
		if a.Origin.Name == "" || a.Destination.Name == "" {
			return a, bad("a flight needs an origin and a destination")
		}
		// A wrong code is dropped rather than fatal: Google Flights takes the
		// name, and Skyscanner links are simply not built without codes.
		if !flights.ValidIATA(a.Origin.IATA) {
			a.Origin.IATA = ""
		}
		if !flights.ValidIATA(a.Destination.IATA) {
			a.Destination.IATA = ""
		}
		depart, err := time.Parse(time.DateOnly, a.Depart)
		if err != nil {
			return a, bad("departure date %q", a.Depart)
		}
		if a.Return != "" {
			ret, err := time.Parse(time.DateOnly, a.Return)
			if err != nil || ret.Before(depart) {
				return a, bad("return date %q", a.Return)
			}
		}
		if a.Passengers < 1 {
			a.Passengers = 1
		}
		if a.Passengers > maxPassengers {
			return a, bad("at most %d passengers", maxPassengers)
		}
		if _, ok := cabins[a.Cabin]; !ok {
			a.Cabin = ""
		}
	default:
		return a, bad("unknown kind %q", a.Kind)
	}
	return a, nil
}

// pickCity is the trip's spelling of name. An empty name on a one-city trip
// means that city.
func pickCity(name string, cities []string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" && len(cities) == 1 {
		return cities[0], true
	}
	for _, c := range cities {
		if strings.EqualFold(strings.TrimSpace(c), name) {
			return c, true
		}
	}
	return "", false
}
