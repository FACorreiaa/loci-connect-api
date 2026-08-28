package localcontext

import (
	"fmt"
	"math"
	"strings"
)

// The "should I go this weekend?" score.
//
// This is deliberately a pure function over already-gathered inputs: no I/O, no
// clock, no provider calls. Callers fetch weather, travel time and POI counts
// however they already do (CompareService has all three in hand) and pass them
// in. That keeps the judgement itself testable and makes it identical wherever
// it is shown.
//
// The score is meant to be *shown with its inputs*, never as a bare number. A
// user who disagrees with a 62 should be able to see it was 22/40 on weather
// and decide for themselves — that transparency is the difference between a
// recommendation and a black box.

// Weight ceilings per dimension. They sum to 100.
const (
	maxWeatherPoints = 40
	maxTravelPoints  = 30
	maxThingsPoints  = 30

	// alertPenalty is the MOST one alert can cost. An alert's severity scales
	// it down from there; an alert with no severity set costs the full amount,
	// which is the flat behaviour this used to have.
	alertPenalty = 10

	// maxDisruptionPenalty caps the total. Disruptions are one dimension among
	// four, and without a cap a destination with a run of minor notices — a
	// holiday, an observance, a small tremor — would be scored worse than one
	// with a single genuinely disqualifying problem. Real feeds produce runs of
	// minor notices constantly; the flat per-alert charge was only ever safe
	// while nothing produced alerts at all.
	maxDisruptionPenalty = 30
)

// Verdicts. Thresholds are judgement calls, not science; they exist so the UI
// can colour the result without re-deriving the rule.
const (
	VerdictGo    = "go"
	VerdictMaybe = "maybe"
	VerdictSkip  = "skip"
)

// ScoreFactor is one contributing dimension, with the reasoning shown.
type ScoreFactor struct {
	// Label names the dimension, e.g. "Weather".
	Label string `json:"label"`
	// Contribution is the signed points this dimension added.
	Contribution int `json:"contribution"`
	// MaxContribution is the ceiling for the dimension, so a UI can render
	// "22 / 40" or a proportional bar without hardcoding the weights.
	MaxContribution int `json:"max_contribution"`
	// Detail is a short human explanation of why, e.g. "Rain likely on 1 of 2 days".
	Detail string `json:"detail"`
}

// GoScore is the verdict plus the reasoning behind it.
type GoScore struct {
	Score   int           `json:"score"` // 0-100
	Verdict string        `json:"verdict"`
	Factors []ScoreFactor `json:"factors"`
	// Summary is a one-line answer suitable for a headline.
	Summary string `json:"summary"`
	// HasEstimatedInputs is true when any input was a stub rather than real
	// provider data. The UI must say so — an unlabelled guess is worse than no
	// score at all.
	HasEstimatedInputs bool `json:"has_estimated_inputs"`
}

// ScoreInput is everything the score is computed from.
type ScoreInput struct {
	// CityName is used only for phrasing.
	CityName string
	// Forecast covers the trip window. An empty forecast means "unknown", which
	// scores neutrally rather than badly.
	Forecast []WeatherDay
	// WeatherEstimated marks a stubbed forecast.
	WeatherEstimated bool
	// TravelMins is one-way travel time from the origin.
	TravelMins int
	// WindowHours is the length of the trip window.
	WindowHours float64
	// POICount is how many worthwhile stops we know about.
	POICount int
	// Alerts are closures/holidays/strikes affecting the window.
	Alerts []Alert
}

// Score answers "should I go?" for one city in one window.
func Score(in ScoreInput) GoScore {
	weather, weatherDetail := scoreWeather(in.Forecast)
	travel, travelDetail := scoreTravel(in.TravelMins, in.WindowHours)
	things, thingsDetail := scoreThings(in.POICount, in.CityName)

	factors := []ScoreFactor{
		{Label: "Weather", Contribution: weather, MaxContribution: maxWeatherPoints, Detail: weatherDetail},
		{Label: "Travel time", Contribution: travel, MaxContribution: maxTravelPoints, Detail: travelDetail},
		{Label: "Things to do", Contribution: things, MaxContribution: maxThingsPoints, Detail: thingsDetail},
	}

	total := weather + travel + things

	if penalty := disruptionPenalty(in.Alerts); penalty > 0 {
		total -= penalty
		factors = append(factors, ScoreFactor{
			Label:           "Local disruptions",
			Contribution:    -penalty,
			MaxContribution: 0,
			Detail:          alertDetail(in.Alerts),
		})
	}

	total = clamp(total, 0, 100)

	return GoScore{
		Score:              total,
		Verdict:            verdictFor(total),
		Factors:            factors,
		Summary:            summarize(total, in.CityName),
		HasEstimatedInputs: in.WeatherEstimated,
	}
}

// scoreWeather rewards dry, mild days. An unknown forecast scores at the
// midpoint: we should not punish a city for our own missing data.
func scoreWeather(days []WeatherDay) (int, string) {
	if len(days) == 0 {
		return maxWeatherPoints / 2, "No forecast available for this window yet"
	}

	var wetDays int
	var precipSum float64
	var highSum float64
	for _, d := range days {
		if isWet(d) {
			wetDays++
		}
		precipSum += d.PrecipProb
		highSum += d.HighC
	}

	avgPrecip := precipSum / float64(len(days))
	avgHigh := highSum / float64(len(days))

	// Start from the ceiling and subtract for rain probability, then for days
	// that are outright wet.
	points := float64(maxWeatherPoints) * (1 - avgPrecip)
	points -= float64(wetDays) / float64(len(days)) * 10

	// Comfort band: penalise genuinely unpleasant temperatures, mildly.
	switch {
	case avgHigh < 5:
		points -= 6
	case avgHigh > 34:
		points -= 6
	}

	score := clamp(int(math.Round(points)), 0, maxWeatherPoints)

	var detail string
	switch {
	case wetDays == 0 && avgPrecip < 0.2:
		detail = fmt.Sprintf("Dry across the window, highs around %.0f°C", avgHigh)
	case wetDays == len(days):
		detail = fmt.Sprintf("Rain likely every day, highs around %.0f°C", avgHigh)
	case wetDays > 0:
		detail = fmt.Sprintf("Rain likely on %d of %d days, highs around %.0f°C", wetDays, len(days), avgHigh)
	default:
		detail = fmt.Sprintf("Mixed but mostly dry, highs around %.0f°C", avgHigh)
	}
	return score, detail
}

func isWet(d WeatherDay) bool {
	c := strings.ToLower(d.Condition)
	if strings.Contains(c, "rain") || strings.Contains(c, "storm") || strings.Contains(c, "snow") {
		return true
	}
	return d.PrecipProb >= 0.5
}

// scoreTravel compares round-trip travel against the window. The question is
// not "how far" but "how much of the trip is spent getting there".
func scoreTravel(oneWayMins int, windowHours float64) (int, string) {
	if windowHours <= 0 {
		windowHours = 48
	}
	if oneWayMins <= 0 {
		return maxTravelPoints, "Already nearby"
	}

	roundTripHours := float64(oneWayMins*2) / 60
	share := roundTripHours / windowHours

	// Under 10% of the window on the road is ideal; at 40% most of the value is
	// gone. Past that, travel goes *negative* rather than merely scoring zero:
	// a 23-hour drive each way for a two-day window is not a neutral trait, it
	// is the reason not to go, and flooring at zero let such trips still reach
	// "maybe" on the strength of good weather.
	var points float64
	switch {
	case share <= 0.10:
		points = maxTravelPoints
	case share <= 0.40:
		points = float64(maxTravelPoints) * (0.40 - share) / 0.30
	default:
		// Ramps to the full negative weight by the time the round trip eats 70%
		// of the window. The curve is deliberately steep: scoring is additive, so
		// a gentler slope let good weather and a rich POI list outvote a
		// disqualifying drive — Lisbon to Paris for a weekend (18 hours each way)
		// still came out "maybe" until this was tightened.
		over := math.Min((share-0.40)/0.30, 1)
		points = -float64(maxTravelPoints) * over
	}

	detail := fmt.Sprintf(
		"%s each way — %.0f%% of a %.0f-hour window on the road",
		humanMins(oneWayMins), share*100, windowHours,
	)
	return clamp(int(math.Round(points)), -maxTravelPoints, maxTravelPoints), detail
}

// scoreThings saturates: beyond a handful of good stops, more does not make a
// weekend better.
func scoreThings(poiCount int, cityName string) (int, string) {
	const saturation = 8

	if poiCount <= 0 {
		return 0, "No places on file yet — worth checking before you commit"
	}

	ratio := math.Min(float64(poiCount)/float64(saturation), 1)
	points := clamp(int(math.Round(float64(maxThingsPoints)*ratio)), 0, maxThingsPoints)

	name := cityName
	if name == "" {
		name = "this city"
	}
	detail := fmt.Sprintf("%d place%s worth your time in %s", poiCount, plural(poiCount), name)
	if poiCount >= saturation {
		detail = fmt.Sprintf("Plenty to fill the window in %s (%d+ places)", name, saturation)
	}
	return points, detail
}

// disruptionPenalty totals what the alerts should cost, graded by severity and
// capped.
//
// Kept a pure function over the already-gathered alerts, like the rest of this
// file: whether a wildfire outranks a public holiday is a judgement the score
// should make identically everywhere, and it must be testable without a
// network.
func disruptionPenalty(alerts []Alert) int {
	var total float64
	for _, a := range alerts {
		total += alertPenalty * float64(effectiveSeverity(a))
	}
	capped := math.Min(math.Round(total), maxDisruptionPenalty)
	return int(capped)
}

func alertDetail(alerts []Alert) string {
	if len(alerts) == 0 {
		return ""
	}
	titles := make([]string, 0, len(alerts))
	for _, a := range alerts {
		titles = append(titles, a.Title)
	}
	return strings.Join(titles, "; ")
}

func verdictFor(score int) string {
	switch {
	case score >= 70:
		return VerdictGo
	case score >= 45:
		return VerdictMaybe
	default:
		return VerdictSkip
	}
}

func summarize(score int, cityName string) string {
	name := cityName
	if name == "" {
		name = "This trip"
	}
	switch verdictFor(score) {
	case VerdictGo:
		return fmt.Sprintf("%s is a good call this window", name)
	case VerdictMaybe:
		return fmt.Sprintf("%s could work, with caveats", name)
	default:
		return fmt.Sprintf("%s is a hard sell this window", name)
	}
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

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
