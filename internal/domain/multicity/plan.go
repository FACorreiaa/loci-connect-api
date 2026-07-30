// Package multicity plans a route through several cities across several days.
//
// It generalises what used to be a two-city weekend check: given an origin, a
// set of candidate cities and a time window of any length, it decides which
// cities are worth including, in what order, how many nights each gets, and
// whether the whole thing actually fits. "Two cities in a weekend" is just the
// smallest case.
//
// Like the go/no-go score, this is a pure function over supplied inputs — no
// I/O, no clock, no provider calls — so the planning judgement is testable on
// its own and identical wherever it runs.
package multicity

import (
	"fmt"
	"sort"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/geo"
)

// travelShareCeiling is the most of the trip we are willing to spend moving
// between places. Past this a plan is not a trip, it is a commute — the same
// judgement the go-score makes, kept consistent on purpose.
const travelShareCeiling = 0.35

// minHoursPerCity is the least amount of window a city needs to earn a place in
// the route. Below this you arrive, look around, and leave.
const minHoursPerCity = 10

// City is a candidate destination.
type City struct {
	ID   string
	Name string
	Lat  float64
	Lon  float64
	// Score is the city's go-score (0-100) if known. Used to decide which
	// cities to drop when they do not all fit. Zero means "unranked".
	Score int
	// POICount informs how many days a city can absorb before it runs dry.
	POICount int
}

// Leg is travel between two consecutive points in the route.
type Leg struct {
	FromName     string
	FromLat      float64
	FromLon      float64
	ToName       string
	ToLat        float64
	ToLon        float64
	DistanceKm   float64
	DurationMins int
	// AfterDay is the day number the traveller moves on, i.e. the leg happens
	// at the end of that day. 0 means before the trip starts (the outbound leg).
	AfterDay int
	// Mode is currently always "drive" — the estimator is road-based. Kept
	// explicit so rail/air can slot in without changing the shape.
	Mode string
}

// DayPlan assigns one day of the trip to a city.
type DayPlan struct {
	DayNumber int
	CityName  string
	CityID    string
	Lat       float64
	Lon       float64
	// Date is set only when the caller supplied a start date.
	Date *time.Time
	// TravelDay marks a day that includes a city-to-city move, so the UI can
	// warn that sightseeing time is reduced.
	TravelDay bool
}

// Input describes the trip to plan.
type Input struct {
	OriginName string
	OriginLat  float64
	OriginLon  float64
	// Candidates are the cities the traveller is considering, in no particular
	// order. The planner picks and orders them.
	Candidates []City
	// Start/End bound the trip. Only the duration matters unless Start is set,
	// in which case days get calendar dates.
	Start time.Time
	End   time.Time
	// ReturnToOrigin adds the journey home to the travel budget. A one-way trip
	// (flying home from the last city) should leave this false.
	ReturnToOrigin bool
	// MaxCities caps the route length. Zero means "as many as fit".
	MaxCities int
}

// Route is the resulting plan: which cities, in what order, on which days.
type Route struct {
	// Feasible is false when not even one city fits the window.
	Feasible bool
	// Cities in visiting order.
	Cities []City
	// Dropped lists candidates left out, with the reason.
	Dropped []Dropped
	Days    []DayPlan
	Legs    []Leg
	// TotalTravelMins across every leg, including the return when requested.
	TotalTravelMins int
	// TravelShare is TotalTravelMins as a fraction of the whole window.
	TravelShare float64
	// Outline is a human summary, the one-liner a UI can show without walking
	// the structure.
	Outline string
	// Warnings flag things that are legal but worth saying out loud.
	Warnings []string
}

// Dropped explains why a candidate did not make the route.
type Dropped struct {
	CityName string
	Reason   string
}

// Plan builds the best route it can through the candidates within the window.
//
// The approach is deliberately simple and explainable rather than optimal:
// order by proximity (nearest-neighbour from the origin), then include cities
// while the travel budget and per-city day minimum both hold. Travelling
// salesman optimality is not worth the opacity here — a traveller needs to
// understand why their trip looks like this.
func Plan(in Input) Route {
	windowHours := in.End.Sub(in.Start).Hours()
	if windowHours <= 0 {
		windowHours = 48
	}
	totalDays := int(windowHours/24 + 0.5)
	if totalDays < 1 {
		totalDays = 1
	}

	out := Route{TravelShare: 0}

	if len(in.Candidates) == 0 {
		out.Outline = "No candidate cities to plan."
		return out
	}

	ordered := routeOrder(in.OriginLat, in.OriginLon, in.Candidates)

	budgetMins := int(windowHours * travelShareCeiling * 60)
	maxCities := in.MaxCities
	if maxCities <= 0 || maxCities > len(ordered) {
		maxCities = len(ordered)
	}

	var chosen []City
	travelMins := 0
	curLat, curLon := in.OriginLat, in.OriginLon

	for _, c := range ordered {
		if len(chosen) >= maxCities {
			out.Dropped = append(out.Dropped, Dropped{c.Name, "route already at its city limit"})
			continue
		}

		legMins := geo.DriveMins(geo.HaversineKm(curLat, curLon, c.Lat, c.Lon))
		prospective := travelMins + legMins
		if in.ReturnToOrigin {
			prospective += geo.DriveMins(geo.HaversineKm(c.Lat, c.Lon, in.OriginLat, in.OriginLon))
		}

		// Would adding this city blow the travel budget?
		if prospective > budgetMins {
			out.Dropped = append(out.Dropped, Dropped{
				c.Name,
				fmt.Sprintf("adding it would put %s of the trip on the road", humanMins(prospective)),
			})
			continue
		}

		// Would the cities then have too little time each to be worth visiting?
		if hoursEach := windowHours / float64(len(chosen)+1); hoursEach < minHoursPerCity {
			out.Dropped = append(out.Dropped, Dropped{
				c.Name,
				fmt.Sprintf("would leave under %dh per city", minHoursPerCity),
			})
			continue
		}

		chosen = append(chosen, c)
		travelMins += legMins
		curLat, curLon = c.Lat, c.Lon
	}

	if len(chosen) == 0 {
		out.Outline = "None of these cities fit the window — shorten the trip or extend the dates."
		return out
	}

	if in.ReturnToOrigin {
		travelMins += geo.DriveMins(geo.HaversineKm(curLat, curLon, in.OriginLat, in.OriginLon))
	}

	out.Feasible = true
	out.Cities = chosen
	out.TotalTravelMins = travelMins
	out.TravelShare = float64(travelMins) / (windowHours * 60)
	out.Days = allocateDays(chosen, totalDays, in.Start)
	out.Legs = buildLegs(in, chosen, out.Days)
	out.Warnings = warnings(chosen, out.Days, out.TravelShare)
	out.Outline = outline(chosen, out.Days, travelMins)

	return out
}

// routeOrder sequences cities nearest-neighbour from the origin, which keeps
// legs short and the resulting itinerary geographically sensible.
func routeOrder(originLat, originLon float64, cities []City) []City {
	remaining := make([]City, len(cities))
	copy(remaining, cities)

	// Sort by go-score first so that when two cities are similarly placed, the
	// better one gets considered earlier and survives the budget check.
	sort.SliceStable(remaining, func(i, j int) bool {
		return remaining[i].Score > remaining[j].Score
	})

	var route []City
	lat, lon := originLat, originLon

	for len(remaining) > 0 {
		best, bestDist := 0, -1.0
		for i, c := range remaining {
			d := geo.HaversineKm(lat, lon, c.Lat, c.Lon)
			if bestDist < 0 || d < bestDist {
				best, bestDist = i, d
			}
		}
		next := remaining[best]
		route = append(route, next)
		lat, lon = next.Lat, next.Lon
		remaining = append(remaining[:best], remaining[best+1:]...)
	}
	return route
}

// allocateDays spreads the available days across the chosen cities, giving the
// remainder to the earlier (higher-scoring, nearer) cities rather than the last
// one — you would rather have an extra day where there is more to do.
func allocateDays(cities []City, totalDays int, start time.Time) []DayPlan {
	per := totalDays / len(cities)
	if per < 1 {
		per = 1
	}
	extra := totalDays - per*len(cities)

	var days []DayPlan
	dayNum := 1
	for i, c := range cities {
		n := per
		if extra > 0 {
			n++
			extra--
		}
		for d := 0; d < n && dayNum <= totalDays; d++ {
			plan := DayPlan{
				DayNumber: dayNum,
				CityName:  c.Name,
				CityID:    c.ID,
				Lat:       c.Lat,
				Lon:       c.Lon,
				// The first day in a city (other than the very first day of the
				// trip) is a travel day: you arrive during it.
				TravelDay: d == 0 && (i > 0 || dayNum == 1),
			}
			if !start.IsZero() {
				date := start.AddDate(0, 0, dayNum-1)
				plan.Date = &date
			}
			days = append(days, plan)
			dayNum++
		}
	}
	return days
}

// buildLegs derives the travel between consecutive cities, plus the outbound
// leg from the origin and the return when requested.
func buildLegs(in Input, cities []City, days []DayPlan) []Leg {
	var legs []Leg

	leg := func(fromName string, fromLat, fromLon float64, toName string, toLat, toLon float64, afterDay int) Leg {
		km := geo.HaversineKm(fromLat, fromLon, toLat, toLon)
		return Leg{
			FromName: fromName, FromLat: fromLat, FromLon: fromLon,
			ToName: toName, ToLat: toLat, ToLon: toLon,
			DistanceKm: km, DurationMins: geo.DriveMins(km),
			AfterDay: afterDay, Mode: "drive",
		}
	}

	// Outbound: origin to the first city, before day 1.
	legs = append(legs, leg(in.OriginName, in.OriginLat, in.OriginLon,
		cities[0].Name, cities[0].Lat, cities[0].Lon, 0))

	// Between cities: the move happens at the end of the last day spent in the
	// previous city.
	for i := 1; i < len(cities); i++ {
		prev, cur := cities[i-1], cities[i]
		legs = append(legs, leg(prev.Name, prev.Lat, prev.Lon, cur.Name, cur.Lat, cur.Lon,
			lastDayIn(days, prev.Name)))
	}

	if in.ReturnToOrigin {
		last := cities[len(cities)-1]
		legs = append(legs, leg(last.Name, last.Lat, last.Lon,
			in.OriginName, in.OriginLat, in.OriginLon, lastDayIn(days, last.Name)))
	}
	return legs
}

func lastDayIn(days []DayPlan, cityName string) int {
	last := 0
	for _, d := range days {
		if d.CityName == cityName && d.DayNumber > last {
			last = d.DayNumber
		}
	}
	return last
}

func warnings(cities []City, days []DayPlan, travelShare float64) []string {
	var w []string

	if travelShare > 0.25 {
		w = append(w, fmt.Sprintf("%.0f%% of this trip is travel time", travelShare*100))
	}

	// A city with one day and a travel day is a drive-by.
	counts := map[string]int{}
	for _, d := range days {
		counts[d.CityName]++
	}
	for _, c := range cities {
		if counts[c.Name] <= 1 {
			w = append(w, fmt.Sprintf("Only one day in %s — consider dropping it for more time elsewhere", c.Name))
		}
		if c.POICount > 0 && counts[c.Name] > 0 {
			// Roughly four worthwhile stops fill a day.
			if capacity := c.POICount / 4; capacity > 0 && counts[c.Name] > capacity+1 {
				w = append(w, fmt.Sprintf("%d days in %s may outrun what we know to do there", counts[c.Name], c.Name))
			}
		}
	}
	return w
}

func outline(cities []City, days []DayPlan, travelMins int) string {
	counts := map[string]int{}
	for _, d := range days {
		counts[d.CityName]++
	}

	parts := make([]string, 0, len(cities))
	for _, c := range cities {
		n := counts[c.Name]
		parts = append(parts, fmt.Sprintf("%s (%d day%s)", c.Name, n, plural(n)))
	}

	joined := parts[0]
	for i := 1; i < len(parts); i++ {
		joined += " → " + parts[i]
	}
	return fmt.Sprintf("%s · %s driving in total", joined, humanMins(travelMins))
}

func humanMins(mins int) string {
	if mins < 60 {
		return fmt.Sprintf("%d min", mins)
	}
	h := mins / 60
	m := mins % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02d", h, m)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
