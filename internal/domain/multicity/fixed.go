package multicity

import "strings"

// FixedStop is a city the traveller placed themselves, with their own nights.
type FixedStop struct {
	City   City
	Nights int
}

// PlanFixed lays out a route the traveller already decided: their order,
// their nights. Nothing is dropped or reordered — Plan is for when they want
// a suggestion — but legs, travel days, warnings and the outline are computed
// exactly as Plan computes them, so the two routes read the same.
func PlanFixed(in Input, stops []FixedStop) Route {
	merged := make([]FixedStop, 0, len(stops))
	index := map[string]int{}
	for _, s := range stops {
		if s.Nights < 1 {
			s.Nights = 1
		}
		key := strings.ToLower(strings.TrimSpace(s.City.Name))
		if i, ok := index[key]; ok {
			merged[i].Nights += s.Nights
			continue
		}
		index[key] = len(merged)
		merged = append(merged, s)
	}
	if len(merged) == 0 {
		return Route{Outline: "No candidate cities to plan."}
	}

	cities := make([]City, len(merged))
	var days []DayPlan
	dayNum := 1
	for i, s := range merged {
		cities[i] = s.City
		for d := 0; d < s.Nights; d++ {
			plan := DayPlan{
				DayNumber: dayNum,
				CityName:  s.City.Name,
				CityID:    s.City.ID,
				Lat:       s.City.Lat,
				Lon:       s.City.Lon,
				TravelDay: d == 0 && (i > 0 || in.hasOrigin()),
			}
			if !in.Start.IsZero() {
				date := in.Start.AddDate(0, 0, dayNum-1)
				plan.Date = &date
			}
			days = append(days, plan)
			dayNum++
		}
	}

	legs := buildLegs(in, cities, days)
	travelMins := 0
	for _, l := range legs {
		travelMins += l.DurationMins
	}
	share := float64(travelMins) / (float64(len(days)) * 24 * 60)
	if share > 1 {
		share = 1
	}

	return Route{
		Feasible:        true,
		Cities:          cities,
		Days:            days,
		Legs:            legs,
		TotalTravelMins: travelMins,
		TravelShare:     share,
		Warnings:        warnings(cities, days, share),
		Outline:         outline(cities, days, travelMins, in.MultiModal),
	}
}
