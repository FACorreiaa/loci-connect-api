package compare

import (
	"fmt"
	"strings"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func buildProsCons(
	cityName string,
	pois []locitypes.POIDetailedInfo,
	distanceKm float64,
	travelMins int,
	weatherClear bool,
) (pros, cons []string) {
	if len(pois) >= 5 {
		pros = append(pros, fmt.Sprintf("%d standout places to explore", len(pois)))
	} else if len(pois) > 0 {
		pros = append(pros, fmt.Sprintf("%d curated stops in %s", len(pois), cityName))
	} else {
		cons = append(cons, "Limited POI data — verify highlights before you go")
	}

	cats := map[string]int{}
	for _, p := range pois {
		if p.Category != "" {
			cats[strings.ToLower(p.Category)]++
		}
	}
	if len(cats) >= 3 {
		pros = append(pros, "Good mix of culture, food, and sights")
	}

	if distanceKm < 150 {
		pros = append(pros, fmt.Sprintf("Relatively close (~%.0f km)", distanceKm))
	} else if distanceKm > 280 {
		cons = append(cons, fmt.Sprintf("Long drive from origin (~%.0f km, ~%d h)", distanceKm, (travelMins+30)/60))
	}

	if weatherClear {
		pros = append(pros, "Forecast looks fair for the window")
	} else {
		cons = append(cons, "Rain or unsettled weather possible")
	}

	if travelMins <= 90 {
		pros = append(pros, "Quick hop from your starting point")
	}

	if len(pros) == 0 {
		pros = append(pros, fmt.Sprintf("%s is worth a weekend look", cityName))
	}
	if len(cons) == 0 {
		cons = append(cons, "Check opening hours for smaller towns")
	}
	return pros, cons
}

func pickRecommendation(columns []columnScore) (rec string, reason string) {
	if len(columns) == 0 {
		return "unspecified", ""
	}
	best := columns[0]
	for _, c := range columns[1:] {
		if c.score > best.score {
			best = c
		}
	}
	reason = fmt.Sprintf("%s balances travel time, weather, and things to do", best.name)
	return best.name, reason
}

type columnScore struct {
	name  string
	score float64
}

func scoreColumn(poiCount int, distanceKm float64, weatherClear bool) float64 {
	score := float64(poiCount) * 2
	if distanceKm < 200 {
		score += 10
	} else if distanceKm < 350 {
		score += 4
	}
	if weatherClear {
		score += 5
	}
	return score
}

func dualCityFeasible(originLat, originLon float64, cols []resolvedCity, windowHours float64) (bool, string, int) {
	if len(cols) < 2 {
		return false, "", 0
	}
	a, b := cols[0], cols[1]
	interKm := HaversineKm(a.lat, a.lon, b.lat, b.lon)
	leg1 := HaversineKm(originLat, originLon, a.lat, a.lon)
	leg2 := interKm
	leg3 := HaversineKm(b.lat, b.lon, originLat, originLon)
	totalKm := leg1 + leg2 + leg3
	totalMins := DriveMins(totalKm) + 60 // buffer for stops

	maxDrive := windowHours * 0.35 * 60 // ~35% of window for driving
	feasible := totalMins <= int(maxDrive) && interKm <= 180
	outline := fmt.Sprintf(
		"Day 1: %s (stay overnight nearby). Day 2: %s, then return. ~%d min total driving.",
		a.name, b.name, totalMins,
	)
	if !feasible {
		outline = fmt.Sprintf(
			"Tight for one weekend: ~%.0f km and ~%d min driving between %s and %s. Pick one or extend the trip.",
			totalKm, totalMins, a.name, b.name,
		)
	}
	return feasible, outline, totalMins
}

type resolvedCity struct {
	name string
	lat  float64
	lon  float64
}
