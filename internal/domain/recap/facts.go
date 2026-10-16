package recap

import (
	"time"

	"github.com/google/uuid"
)

// TripFacts is everything a trip recap is built from, loaded in one go by
// the repository. Dates are the traveller's local dates, as midnight UTC.
type TripFacts struct {
	TripID   uuid.UUID
	UserID   uuid.UUID
	Title    string
	Timezone string

	PeriodStart, PeriodEnd time.Time

	// Cities in travel order; the first is the trip's main city.
	Cities []City
	// ArrivalIATA is the first flight's destination airport, when known.
	ArrivalIATA string

	Days []Day
	// Places is every distinct place the traveller visited or marked done.
	Places []Place
	// StopsPlanned and StopsDone count the itinerary's stops.
	StopsPlanned, StopsDone int
	// LegsKm sums the trip's own legs; zero when it has none.
	LegsKm float64

	// TripTaste is taste evidence recorded against this trip, by trait.
	TripTaste []Trait
	// OverallTaste is the traveller's current positive traits, the fallback
	// when the trip itself left no evidence.
	OverallTaste []Trait

	// Points earned on the trip's dates, the local dates any were earned, and
	// the badges awarded in the period.
	Points      int64
	ActiveDates []time.Time
	Badges      []string

	// Field is the traveller's field score in the main city; nil when none.
	Field *Field

	// Friends with the main city on a trip they let friends see.
	Friends []Friend

	Highlights []HighlightFact

	Owner Owner
}

// City is one city on the trip.
type City struct {
	Name        string
	Country     string
	CountryCode string
	Lat, Lon    float64
	HasCoords   bool
}

// Day is one trip day.
type Day struct {
	Number     int
	Date       *time.Time
	CityName   string
	StopsTotal int
	StopsDone  int
	Completed  bool
}

// Place is one place kept on the trip.
type Place struct {
	POIID        string
	Name         string
	Neighborhood string
	Category     string
	CityName     string
	Lat, Lon     float64
	HasCoords    bool
	At           time.Time
}

// Trait is one taste trait with the evidence behind it.
type Trait struct {
	Key, Label string
	// Weight is the summed evidence weight in scope.
	Weight float64
	// Evidence counts the evidence rows in scope; Saves and Visits split them.
	Evidence, Saves, Visits int
	// Score and Confidence are the trait's current values, and TotalEvidence
	// its evidence across all time.
	Score, Confidence float64
	TotalEvidence     int
}

// Field is a field score in one city.
type Field struct {
	CityID       uuid.UUID
	CityName     string
	Score        int64
	PlacesKept   int
	DaysFinished int
}

// Friend is a friend who has been to the city.
type Friend struct {
	ID          uuid.UUID
	DisplayName string
	AvatarURL   string
}

// HighlightReason says why a place made the highlights.
type HighlightReason int

const (
	HighlightFavorited  HighlightReason = 1
	HighlightReviewed   HighlightReason = 2
	HighlightFirstVisit HighlightReason = 3
)

// HighlightFact is one candidate highlight.
type HighlightFact struct {
	POIID    string
	Name     string
	Category string
	Rating   int
	Reason   HighlightReason
}

// Owner is who the recap belongs to.
type Owner struct {
	ID          uuid.UUID
	DisplayName string
	AvatarURL   string
}
