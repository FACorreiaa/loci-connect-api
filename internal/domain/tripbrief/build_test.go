package tripbrief

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
)

func ptr[T any](v T) *T { return &v }

func lisbonDay2() Candidate {
	return Candidate{
		UserID:    uuid.New(),
		TripID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		City:      "Lisbon",
		Lat:       ptr(38.72),
		Lon:       ptr(-9.14),
		LocalDate: time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC), // a Sunday
		DayNumber: 2,
	}
}

func TestBuildMorning_Full(t *testing.T) {
	stops := []Stop{
		{Name: "Manteigaria", StartMinute: ptr(600)},
		{Name: "Museu Nacional do Azulejo", OpeningHours: "tue-sun 10:00-18:00; mon closed"},
		{Name: "Gulbenkian", OpeningHours: "mon-sat 10:00-18:00; sun closed"},
	}
	wx := &localcontext.WeatherDay{HighC: 18.4, Condition: "Clear", PrecipProb: 0.1}
	alerts := []localcontext.Alert{{Title: "Metro strike from 10:00"}}
	b, ok := BuildMorning(lisbonDay2(), stops, wx, alerts)
	if !ok {
		t.Fatal("want a brief")
	}
	if b.Title != "Day 2 in Lisbon" {
		t.Errorf("title = %q", b.Title)
	}
	want := "18°C, clear · 3 stops, first Manteigaria at 10:00 · Closed today: Gulbenkian · Metro strike from 10:00"
	if b.Body != want {
		t.Errorf("body = %q\nwant  %q", b.Body, want)
	}
	if b.URL != "https://lociai.fyi/trips/11111111-1111-1111-1111-111111111111" {
		t.Errorf("url = %q", b.URL)
	}
}

// A closed stop links to swapping it, so a co-planner on a trip day can
// suggest something open instead.
func TestBuildMorning_ClosedStopLinksToASwap(t *testing.T) {
	closed := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	stops := []Stop{
		{ID: uuid.New(), Name: "Manteigaria"},
		{ID: closed, Name: "Gulbenkian", OpeningHours: "mon-sat 10:00-18:00; sun closed"},
	}
	b, ok := BuildMorning(lisbonDay2(), stops, nil, nil)
	if !ok {
		t.Fatal("want a brief")
	}
	if want := "https://lociai.fyi/trips/11111111-1111-1111-1111-111111111111?swap=" + closed.String(); b.URL != want {
		t.Errorf("url = %q, want %q", b.URL, want)
	}
	b, _ = BuildMorning(lisbonDay2(), stops[:1], nil, nil)
	if strings.Contains(b.URL, "swap") {
		t.Errorf("nothing closed, nothing to swap: %q", b.URL)
	}
}

func TestBuildMorning_WeatherOnly(t *testing.T) {
	b, ok := BuildMorning(lisbonDay2(), nil, &localcontext.WeatherDay{HighC: 21, Condition: "Rain"}, nil)
	if !ok || b.Body != "21°C, rain" {
		t.Fatalf("got (%q, %v)", b.Body, ok)
	}
}

func TestBuildMorning_StopsOnly(t *testing.T) {
	b, ok := BuildMorning(lisbonDay2(), []Stop{{Name: "Belém Tower"}}, nil, nil)
	if !ok || b.Body != "1 stop, first Belém Tower" {
		t.Fatalf("got (%q, %v)", b.Body, ok)
	}
}

func TestBuildMorning_NothingToSay(t *testing.T) {
	if _, ok := BuildMorning(lisbonDay2(), nil, nil, []localcontext.Alert{{Title: "x"}}); ok {
		t.Fatal("no weather and no stops should send nothing")
	}
}

func TestBuildMorning_NoCityNamesTheTrip(t *testing.T) {
	c := lisbonDay2()
	c.City = ""
	b, _ := BuildMorning(c, []Stop{{Name: "A"}}, nil, nil)
	if b.Title != "Day 2 of your trip" {
		t.Errorf("title = %q", b.Title)
	}
}

func TestBuildMorning_BodyIsCapped(t *testing.T) {
	var stops []Stop
	for i := 0; i < 40; i++ {
		stops = append(stops, Stop{Name: strings.Repeat("Closed Place ", 3), OpeningHours: "sun closed"})
	}
	b, ok := BuildMorning(lisbonDay2(), stops, &localcontext.WeatherDay{HighC: 18, Condition: "Clouds"}, nil)
	if !ok {
		t.Fatal("want a brief")
	}
	if n := utf8.RuneCountInString(b.Body); n > maxBodyRunes {
		t.Fatalf("body is %d runes, cap %d: %q", n, maxBodyRunes, b.Body)
	}
}

func TestBuildWeatherTurn(t *testing.T) {
	cases := []struct {
		morning, now float32
		fires        bool
	}{
		{0.3, 0.7, true},
		{0.5, 0.9, false}, // already wet at breakfast
		{0.3, 0.5, false}, // not wet enough now
		{0.39, 0.6, true}, // both thresholds inclusive where they should be
	}
	for _, c := range cases {
		b, ok := BuildWeatherTurn(lisbonDay2(), c.morning, c.now)
		if ok != c.fires {
			t.Errorf("morning %.2f now %.2f: fired=%v want %v", c.morning, c.now, ok, c.fires)
		}
		if ok && b.Title != "Rain likely in Lisbon" {
			t.Errorf("title = %q", b.Title)
		}
	}
}
