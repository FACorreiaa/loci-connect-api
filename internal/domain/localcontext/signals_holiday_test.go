package localcontext

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

const nagerFixture = `[
  {"date":"2026-09-02","localName":"Dia Publico","name":"Public Day","global":true,"counties":null,"types":["Public"]},
  {"date":"2026-09-03","localName":"Carnaval","name":"Carnival","global":true,"counties":null,"types":["Optional"]},
  {"date":"2026-09-04","localName":"Festa Regional","name":"Regional Feast","global":false,"counties":["PT-30"],"types":["Public"]},
  {"date":"2026-12-25","localName":"Natal","name":"Christmas Day","global":true,"counties":null,"types":["Public"]}
]`

func nagerSource(t *testing.T, body string, status int) (*HolidaySource, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client := httpx.New(httpx.Config{
		Timeout: 2 * time.Second, MaxRetries: 1,
		BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond,
		RatePerSecond: 1000, Burst: 1000, UserAgent: "loci-test/1.0",
	})
	return NewHolidaySource(srv.URL, client, testCache(t)), &hits
}

func window(fromDay, toDay int) (time.Time, time.Time) {
	return time.Date(2026, 9, fromDay, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, toDay, 18, 0, 0, 0, time.UTC)
}

// The headline: this is the first producer of an Alert the app has ever had.
func TestHolidaySource_ProducesHolidayAlertsInsideTheWindow(t *testing.T) {
	s, _ := nagerSource(t, nagerFixture, http.StatusOK)
	start, end := window(2, 4)

	got, err := s.Fetch(context.Background(), SignalRequest{CountryCode: "PT", Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected the 3 holidays inside the window, got %d", len(got))
	}
	for _, a := range got {
		if a.Kind != AlertHoliday {
			t.Errorf("kind: got %q, want holiday", a.Kind)
		}
		if a.Source != SourceHolidays {
			t.Errorf("source: got %q, want %q", a.Source, SourceHolidays)
		}
		if a.Date == nil {
			t.Error("a holiday alert must carry its date")
		}
	}
}

// Christmas is in the same year but nowhere near a September weekend; a source
// that ignored the window would penalise every trip all year round.
func TestHolidaySource_FiltersToTheWindow(t *testing.T) {
	s, _ := nagerSource(t, nagerFixture, http.StatusOK)
	start, end := window(2, 2)

	got, err := s.Fetch(context.Background(), SignalRequest{CountryCode: "PT", Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 holiday, got %d", len(got))
	}
	if !strings.Contains(got[0].Title, "Dia Publico") {
		t.Errorf("got %q", got[0].Title)
	}
}

// A public holiday shuts a city; an observance and a regional feast do not.
// Charging them all the same would make a destination look worse than it is.
func TestHolidaySource_GradesSeverityByHowMuchItActuallyCloses(t *testing.T) {
	s, _ := nagerSource(t, nagerFixture, http.StatusOK)
	start, end := window(2, 4)

	got, err := s.Fetch(context.Background(), SignalRequest{CountryCode: "PT", Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bySubstring := func(sub string) Alert {
		t.Helper()
		for _, a := range got {
			if strings.Contains(a.Title, sub) {
				return a
			}
		}
		t.Fatalf("no alert containing %q in %+v", sub, got)
		return Alert{}
	}

	if s := bySubstring("Dia Publico").Severity; s != SeverityModerate {
		t.Errorf("a national public holiday should be moderate, got %v", s)
	}
	if s := bySubstring("Carnaval").Severity; s != SeverityMinor {
		t.Errorf("an optional observance should be minor, got %v", s)
	}
	if a := bySubstring("Festa Regional"); a.Severity != SeverityMinor {
		t.Errorf("a regional holiday should be minor, got %v", a.Severity)
	} else if !strings.Contains(a.Detail, "PT-30") {
		t.Errorf("a regional holiday should name its regions, got %q", a.Detail)
	}
}

// Showing both names saves a traveller having to recognise the local one.
func TestHolidaySource_TitleCarriesLocalAndEnglishNames(t *testing.T) {
	s, _ := nagerSource(t, nagerFixture, http.StatusOK)
	start, end := window(2, 2)

	got, _ := s.Fetch(context.Background(), SignalRequest{CountryCode: "PT", Start: start, End: end})
	if len(got) != 1 {
		t.Fatalf("got %d alerts", len(got))
	}
	if !strings.Contains(got[0].Title, "Dia Publico") || !strings.Contains(got[0].Title, "Public Day") {
		t.Errorf("title should carry both names, got %q", got[0].Title)
	}
}

// Coordinates at sea have no country. That is a valid answer, not an error.
func TestHolidaySource_NoCountryIsQuietNotAnError(t *testing.T) {
	s, hits := nagerSource(t, nagerFixture, http.StatusOK)

	got, err := s.Fetch(context.Background(), SignalRequest{CountryCode: ""})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no alerts, got %d", len(got))
	}
	if *hits != 0 {
		t.Errorf("expected no upstream call without a country, got %d", *hits)
	}
}

// A year's holidays are decided long in advance; refetching them per request
// would spend the free tier's quota on data that cannot change.
func TestHolidaySource_CachesPerCountryYear(t *testing.T) {
	s, hits := nagerSource(t, nagerFixture, http.StatusOK)
	start, end := window(2, 4)
	ctx := context.Background()

	for range 3 {
		if _, err := s.Fetch(ctx, SignalRequest{CountryCode: "PT", Start: start, End: end}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if *hits != 1 {
		t.Errorf("expected 1 upstream call, got %d", *hits)
	}

	// A different country must not be served from Portugal's entry.
	if _, err := s.Fetch(ctx, SignalRequest{CountryCode: "ES", Start: start, End: end}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *hits != 2 {
		t.Errorf("expected a second call for a new country, got %d", *hits)
	}
}

// A New Year trip spans two years of holiday data.
func TestYearsSpanned(t *testing.T) {
	dec := time.Date(2026, 12, 30, 0, 0, 0, 0, time.UTC)
	jan := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)

	if got := yearsSpanned(dec, jan); len(got) != 2 || got[0] != 2026 || got[1] != 2027 {
		t.Errorf("a new-year window should span both years, got %v", got)
	}
	if got := yearsSpanned(dec, dec); len(got) != 1 || got[0] != 2026 {
		t.Errorf("a same-year window should be one year, got %v", got)
	}
	// An absurd window must not fan out into thousands of requests.
	far := time.Date(2199, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := yearsSpanned(dec, far); len(got) > 2 {
		t.Errorf("an absurd window must be bounded, got %d years", len(got))
	}
}

func TestHolidaySource_UpstreamFailureIsAnError(t *testing.T) {
	s, _ := nagerSource(t, `{}`, http.StatusInternalServerError)
	start, end := window(2, 4)

	if _, err := s.Fetch(context.Background(), SignalRequest{CountryCode: "PT", Start: start, End: end}); err == nil {
		t.Fatal("expected an error so the Gatherer can log and skip it")
	}
}

// One malformed row must not lose the rest of the year.
func TestHolidaySource_SkipsUnparseableRows(t *testing.T) {
	body := `[
      {"date":"garbage","localName":"Broken","name":"Broken","global":true,"types":["Public"]},
      {"date":"2026-09-02","localName":"Good","name":"Good","global":true,"types":["Public"]}
    ]`
	s, _ := nagerSource(t, body, http.StatusOK)
	start, end := window(2, 3)

	got, err := s.Fetch(context.Background(), SignalRequest{CountryCode: "PT", Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Title, "Good") {
		t.Errorf("expected only the parseable row, got %+v", got)
	}
}

func TestHolidaySource_ImplementsSignalSource(t *testing.T) {
	var _ SignalSource = NewHolidaySource("", httpx.New(httpx.Config{}), nil)
}

// Nager answers 204 No Content for a country it does not cover (Taiwan and
// India, among others). That is "no holidays on file", not a failure — and
// treating it as one benched the whole holiday source, so one unsupported
// destination silently removed holidays from every other destination too.
func TestHolidaySource_UncoveredCountryIsEmptyNotAnError(t *testing.T) {
	s, _ := nagerSource(t, "", http.StatusNoContent)
	start, end := window(2, 4)

	got, err := s.Fetch(context.Background(), SignalRequest{CountryCode: "TW", Start: start, End: end})
	if err != nil {
		t.Fatalf("a 204 must not be an error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no holidays, got %d", len(got))
	}
}
