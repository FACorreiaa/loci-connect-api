package localcontext

import (
	"strings"
	"testing"
	"time"
)

func dry(n int) []WeatherDay {
	days := make([]WeatherDay, n)
	base := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	for i := range days {
		days[i] = WeatherDay{
			Date:       base.AddDate(0, 0, i),
			HighC:      22,
			LowC:       14,
			Condition:  "Clear",
			PrecipProb: 0.05,
		}
	}
	return days
}

func wet(n int) []WeatherDay {
	days := dry(n)
	for i := range days {
		days[i].Condition = "Rain"
		days[i].PrecipProb = 0.8
	}
	return days
}

// The headline case: near, dry, plenty to do should be an unambiguous "go".
func TestScore_IdealWeekendSaysGo(t *testing.T) {
	got := Score(ScoreInput{
		CityName:    "Évora",
		Forecast:    dry(2),
		TravelMins:  90,
		WindowHours: 48,
		POICount:    10,
	})

	if got.Verdict != VerdictGo {
		t.Fatalf("verdict = %q (score %d), want %q", got.Verdict, got.Score, VerdictGo)
	}
	if got.Score < 80 {
		t.Errorf("score = %d, expected a strong score for an ideal weekend", got.Score)
	}
	if !strings.Contains(got.Summary, "Évora") {
		t.Errorf("summary should name the city, got %q", got.Summary)
	}
}

// Far away, raining, nothing on file: the score has to be willing to say no,
// otherwise it is decoration rather than a judgement.
func TestScore_BadWeekendSaysSkip(t *testing.T) {
	got := Score(ScoreInput{
		CityName:    "Beja",
		Forecast:    wet(2),
		TravelMins:  330,
		WindowHours: 48,
		POICount:    0,
	})

	if got.Verdict != VerdictSkip {
		t.Fatalf("verdict = %q (score %d), want %q", got.Verdict, got.Score, VerdictSkip)
	}
}

// Every score must arrive with its reasoning attached.
func TestScore_AlwaysExplainsItself(t *testing.T) {
	got := Score(ScoreInput{CityName: "Porto", Forecast: dry(2), TravelMins: 60, WindowHours: 48, POICount: 5})

	want := map[string]bool{"Weather": false, "Travel time": false, "Things to do": false}
	for _, f := range got.Factors {
		if _, ok := want[f.Label]; ok {
			want[f.Label] = true
		}
		if f.Detail == "" {
			t.Errorf("factor %q has no detail; a bare number is not an explanation", f.Label)
		}
		if f.Contribution > f.MaxContribution && f.MaxContribution > 0 {
			t.Errorf("factor %q contributed %d over its %d ceiling", f.Label, f.Contribution, f.MaxContribution)
		}
	}
	for label, seen := range want {
		if !seen {
			t.Errorf("missing factor %q", label)
		}
	}
}

// Missing data must not read as bad data: a city we have no forecast for should
// score neutrally on weather, not zero.
func TestScore_UnknownForecastScoresNeutral(t *testing.T) {
	unknown := Score(ScoreInput{CityName: "X", TravelMins: 60, WindowHours: 48, POICount: 6})
	raining := Score(ScoreInput{CityName: "X", Forecast: wet(2), TravelMins: 60, WindowHours: 48, POICount: 6})

	if unknown.Score <= raining.Score {
		t.Fatalf("unknown weather (%d) should score above known-bad weather (%d)", unknown.Score, raining.Score)
	}
	var weather ScoreFactor
	for _, f := range unknown.Factors {
		if f.Label == "Weather" {
			weather = f
		}
	}
	if weather.Contribution != maxWeatherPoints/2 {
		t.Errorf("unknown weather = %d points, want the %d midpoint", weather.Contribution, maxWeatherPoints/2)
	}
}

// Travel is judged against the window, not in absolute terms: three hours is
// most of a day trip but a rounding error on a week.
func TestScore_TravelIsRelativeToTheWindow(t *testing.T) {
	shortWindow := Score(ScoreInput{CityName: "X", Forecast: dry(2), TravelMins: 180, WindowHours: 24, POICount: 6})
	longWindow := Score(ScoreInput{CityName: "X", Forecast: dry(2), TravelMins: 180, WindowHours: 120, POICount: 6})

	if longWindow.Score <= shortWindow.Score {
		t.Fatalf("same drive should hurt less over a longer window: %d vs %d", longWindow.Score, shortWindow.Score)
	}
}

// Alerts subtract, and say what they were.
func TestScore_AlertsPenaliseAndAreNamed(t *testing.T) {
	clean := Score(ScoreInput{CityName: "X", Forecast: dry(2), TravelMins: 60, WindowHours: 48, POICount: 6})
	disrupted := Score(ScoreInput{
		CityName: "X", Forecast: dry(2), TravelMins: 60, WindowHours: 48, POICount: 6,
		Alerts: []Alert{{Kind: AlertStrike, Title: "Rail strike Saturday"}},
	})

	if disrupted.Score != clean.Score-alertPenalty {
		t.Fatalf("expected a %d point penalty, got %d -> %d", alertPenalty, clean.Score, disrupted.Score)
	}

	var found bool
	for _, f := range disrupted.Factors {
		if f.Label == "Local disruptions" {
			found = true
			if !strings.Contains(f.Detail, "Rail strike Saturday") {
				t.Errorf("disruption factor should name the alert, got %q", f.Detail)
			}
		}
	}
	if !found {
		t.Error("expected a disruption factor when alerts are present")
	}
}

// A stubbed forecast has to be flagged so the UI can label it. An unlabelled
// guess is worse than no score.
func TestScore_FlagsEstimatedInputs(t *testing.T) {
	got := Score(ScoreInput{CityName: "X", Forecast: dry(2), WeatherEstimated: true, TravelMins: 60, WindowHours: 48, POICount: 6})
	if !got.HasEstimatedInputs {
		t.Fatal("expected HasEstimatedInputs to survive into the result")
	}
}

// The score is a percentage and must behave like one at the extremes.
func TestScore_StaysWithinBounds(t *testing.T) {
	floor := Score(ScoreInput{
		CityName: "X", Forecast: wet(3), TravelMins: 900, WindowHours: 24, POICount: 0,
		Alerts: []Alert{
			{Kind: AlertStrike, Title: "a"},
			{Kind: AlertClosure, Title: "b"},
			{Kind: AlertHoliday, Title: "c"},
			{Kind: AlertClosure, Title: "d"},
		},
	})
	if floor.Score < 0 || floor.Score > 100 {
		t.Fatalf("score out of bounds: %d", floor.Score)
	}

	ceiling := Score(ScoreInput{CityName: "X", Forecast: dry(2), TravelMins: 1, WindowHours: 96, POICount: 50})
	if ceiling.Score > 100 {
		t.Fatalf("score above 100: %d", ceiling.Score)
	}
}

// A drive that eats the whole window is the reason not to go, not a neutral
// trait. Flooring travel at zero let such trips reach "maybe" on the strength of
// good weather — a 23h drive each way for a weekend scored 66.
func TestScore_TravelThatEatsTheWindowForcesSkip(t *testing.T) {
	got := Score(ScoreInput{
		CityName:    "Amsterdam",
		Forecast:    dry(2), // deliberately perfect weather
		TravelMins:  1397,   // ~23h each way
		WindowHours: 48,
		POICount:    20, // and plenty to do
	})

	if got.Verdict != VerdictSkip {
		t.Fatalf("verdict = %q (score %d), want %q — the drive alone should sink it",
			got.Verdict, got.Score, VerdictSkip)
	}

	var travel ScoreFactor
	for _, f := range got.Factors {
		if f.Label == "Travel time" {
			travel = f
		}
	}
	if travel.Contribution >= 0 {
		t.Errorf("travel contribution = %d, expected it to count against the trip", travel.Contribution)
	}
}

// The penalty must stay proportionate: a drive slightly over the comfortable
// share should not be treated the same as one that consumes the whole window.
func TestScore_TravelPenaltyIsProportionate(t *testing.T) {
	mild := Score(ScoreInput{CityName: "X", Forecast: dry(2), TravelMins: 600, WindowHours: 48, POICount: 8})
	severe := Score(ScoreInput{CityName: "X", Forecast: dry(2), TravelMins: 1397, WindowHours: 48, POICount: 8})

	if severe.Score >= mild.Score {
		t.Fatalf("a longer drive must score worse: 10h=%d vs 23h=%d", mild.Score, severe.Score)
	}
}

// Scoring is additive, which means a single disqualifying dimension can be
// outvoted by two good ones unless the penalty is steep enough. Lisbon to Paris
// for a weekend — perfect weather, plenty to do, 18 hours each way — read
// "maybe" until the travel curve was tightened. It is a skip.
func TestScore_LongHaulWeekendIsASkipDespiteGoodWeatherAndPOIs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		travelMins int
	}{
		{"Lisbon to Paris", 1089},
		{"Lisbon to London", 1188},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(ScoreInput{
				CityName:    "Elsewhere",
				Forecast:    dry(2),
				TravelMins:  tc.travelMins,
				WindowHours: 48,
				POICount:    20,
			})
			if got.Verdict != VerdictSkip {
				t.Fatalf("verdict = %q (score %d), want %q", got.Verdict, got.Score, VerdictSkip)
			}
		})
	}
}

// The steeper curve must not turn ordinary road trips into skips.
func TestScore_ReasonableDriveStillReadsWell(t *testing.T) {
	got := Score(ScoreInput{
		CityName:    "Évora",
		Forecast:    dry(2),
		TravelMins:  105, // ~1h45, a normal weekend hop
		WindowHours: 48,
		POICount:    8,
	})
	if got.Verdict != VerdictGo {
		t.Fatalf("verdict = %q (score %d), want %q — a 1h45 drive is not a deterrent",
			got.Verdict, got.Score, VerdictGo)
	}
}
