package trip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
)

// fakeWeather records what it was asked for, so the tests can assert that each
// city gets a forecast for its own days rather than the whole trip's.
type fakeWeather struct {
	byCoord map[string][]localcontext.WeatherDay
	err     error
	asked   []int
}

func (f *fakeWeather) Forecast(_ context.Context, lat, lon float64, days int) ([]localcontext.WeatherDay, error) {
	f.asked = append(f.asked, days)
	if f.err != nil {
		return nil, f.err
	}
	key := coordKey(lat, lon)
	return f.byCoord[key], nil
}

func coordKey(lat, lon float64) string {
	return fmt.Sprintf("%.2f,%.2f", lat, lon)
}

// fakeTripRepo returns one prepared trip.
type fakeTripRepo struct {
	trip *Trip
	err  error
}

func (f *fakeTripRepo) GetTrip(context.Context, uuid.UUID, uuid.UUID) (*Trip, error) {
	return f.trip, f.err
}
func (f *fakeTripRepo) SaveTrip(context.Context, *Trip, int64) (*Trip, error) { return f.trip, nil }
func (f *fakeTripRepo) ListTrips(context.Context, uuid.UUID, int, int) ([]*Trip, int, error) {
	return nil, 0, nil
}

func (f *fakeTripRepo) SetShare(context.Context, uuid.UUID, uuid.UUID, bool, string) (*Trip, error) {
	return f.trip, nil
}

func rainyDays(n int) []localcontext.WeatherDay {
	out := make([]localcontext.WeatherDay, n)
	base := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	for i := range out {
		out[i] = localcontext.WeatherDay{
			Date: base.AddDate(0, 0, i), Condition: "Rain",
			PrecipProb: 0.9, HighC: 17, LowC: 13,
		}
	}
	return out
}

func f64p(v float64) *float64 { return &v }

func authed(ctx context.Context, userID uuid.UUID) context.Context {
	return interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: userID.String()})
}

func newPackingHandler(t *testing.T, tr *Trip, w WeatherSource, estimated bool) *Handler {
	t.Helper()
	h := NewHandler(&fakeTripRepo{trip: tr}, "https://loci.test", nil, nil)
	return h.WithPacking(w, estimated).WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func suggestionTexts(resp *tripv1.SuggestPackingResponse) string {
	var b strings.Builder
	for _, s := range resp.Suggestions {
		b.WriteString(s.Text + " | " + s.Reason + "\n")
	}
	return b.String()
}

// The whole point: the list reflects THIS trip's forecast, and says so.
func TestSuggestPacking_UsesTheTripsOwnForecast(t *testing.T) {
	userID := uuid.New()
	tr := &Trip{
		ID: uuid.New(), UserID: userID, CityName: "Lisbon", Title: "Lisbon",
		Days: []TripDay{
			{DayNumber: 1, CityName: "Lisbon", CityLat: f64p(38.72), CityLon: f64p(-9.14)},
			{DayNumber: 2, CityName: "Lisbon", CityLat: f64p(38.72), CityLon: f64p(-9.14)},
		},
	}
	w := &fakeWeather{byCoord: map[string][]localcontext.WeatherDay{
		coordKey(38.72, -9.14): rainyDays(2),
	}}

	h := newPackingHandler(t, tr, w, false)
	resp, err := h.SuggestPacking(authed(context.Background(), userID),
		connect.NewRequest(&tripv1.SuggestPackingRequest{TripId: tr.ID.String()}))
	if err != nil {
		t.Fatalf("SuggestPacking: %v", err)
	}

	if !resp.Msg.UsedForecast {
		t.Error("expected used_forecast when a forecast was returned")
	}
	if !strings.Contains(strings.ToLower(suggestionTexts(resp.Msg)), "umbrella") {
		t.Errorf("a rainy trip should suggest rain gear:\n%s", suggestionTexts(resp.Msg))
	}
}

// Without a forecast the list must still be useful, and must not invent weather
// advice.
func TestSuggestPacking_NoWeatherSourceStillReturnsEssentials(t *testing.T) {
	userID := uuid.New()
	tr := &Trip{
		ID: uuid.New(), UserID: userID, CityName: "Lisbon",
		Days: []TripDay{{DayNumber: 1, CityName: "Lisbon"}},
	}

	h := newPackingHandler(t, tr, nil, false)
	resp, err := h.SuggestPacking(authed(context.Background(), userID),
		connect.NewRequest(&tripv1.SuggestPackingRequest{TripId: tr.ID.String()}))
	if err != nil {
		t.Fatalf("SuggestPacking: %v", err)
	}

	if resp.Msg.UsedForecast {
		t.Error("used_forecast must be false with no weather source")
	}
	if len(resp.Msg.Suggestions) == 0 {
		t.Fatal("expected essentials even without a forecast")
	}
	if strings.Contains(strings.ToLower(suggestionTexts(resp.Msg)), "umbrella") {
		t.Error("must not suggest rain gear for weather it never checked")
	}
}

// A provider outage must degrade, not fail: the user still gets a list.
func TestSuggestPacking_SurvivesForecastFailure(t *testing.T) {
	userID := uuid.New()
	tr := &Trip{
		ID: uuid.New(), UserID: userID, CityName: "Lisbon",
		Days: []TripDay{{DayNumber: 1, CityName: "Lisbon", CityLat: f64p(38.72), CityLon: f64p(-9.14)}},
	}

	h := newPackingHandler(t, tr, &fakeWeather{err: errors.New("provider down")}, false)
	resp, err := h.SuggestPacking(authed(context.Background(), userID),
		connect.NewRequest(&tripv1.SuggestPackingRequest{TripId: tr.ID.String()}))
	if err != nil {
		t.Fatalf("a forecast failure must not fail the packing list: %v", err)
	}
	if resp.Msg.UsedForecast {
		t.Error("used_forecast should be false when the forecast failed")
	}
	if len(resp.Msg.Suggestions) == 0 {
		t.Error("expected a usable list from the remaining inputs")
	}
}

// A multi-city trip must ask for each city separately, for its own days.
func TestSuggestPacking_AsksPerCityForItsOwnDays(t *testing.T) {
	userID := uuid.New()
	tr := &Trip{
		ID: uuid.New(), UserID: userID, CityName: "Lisbon",
		Days: []TripDay{
			{DayNumber: 1, CityName: "Lisbon", CityLat: f64p(38.72), CityLon: f64p(-9.14)},
			{DayNumber: 2, CityName: "Lisbon", CityLat: f64p(38.72), CityLon: f64p(-9.14)},
			{DayNumber: 3, CityName: "Porto", CityLat: f64p(41.15), CityLon: f64p(-8.61), TravelDay: true},
		},
		Legs: []TripLeg{{AfterDay: 2, FromName: "Lisbon", ToName: "Porto", DurationMins: 200}},
	}
	w := &fakeWeather{byCoord: map[string][]localcontext.WeatherDay{
		coordKey(38.72, -9.14): rainyDays(2),
		coordKey(41.15, -8.61): rainyDays(1),
	}}

	h := newPackingHandler(t, tr, w, false)
	if _, err := h.SuggestPacking(authed(context.Background(), userID),
		connect.NewRequest(&tripv1.SuggestPackingRequest{TripId: tr.ID.String()})); err != nil {
		t.Fatalf("SuggestPacking: %v", err)
	}

	if len(w.asked) != 2 {
		t.Fatalf("expected one forecast call per city, got %d", len(w.asked))
	}
	if w.asked[0] != 2 || w.asked[1] != 1 {
		t.Errorf("each city should be asked for its own day count, got %v", w.asked)
	}
}

// A trip written before multi-city support has no per-day city; it must still
// resolve to the trip's primary city rather than producing nothing.
func TestSuggestPacking_FallsBackToThePrimaryCity(t *testing.T) {
	userID := uuid.New()
	tr := &Trip{
		ID: uuid.New(), UserID: userID, CityName: "Lisbon",
		Days: []TripDay{{DayNumber: 1}, {DayNumber: 2}},
	}

	h := newPackingHandler(t, tr, nil, false)
	resp, err := h.SuggestPacking(authed(context.Background(), userID),
		connect.NewRequest(&tripv1.SuggestPackingRequest{TripId: tr.ID.String()}))
	if err != nil {
		t.Fatalf("SuggestPacking: %v", err)
	}
	if len(resp.Msg.Suggestions) == 0 {
		t.Error("a legacy single-city trip should still get suggestions")
	}
}

// A stubbed forecast has to be labelled, and only when it actually contributed.
func TestSuggestPacking_FlagsEstimatedWeatherOnlyWhenUsed(t *testing.T) {
	userID := uuid.New()
	withCoords := &Trip{
		ID: uuid.New(), UserID: userID, CityName: "Lisbon",
		Days: []TripDay{{DayNumber: 1, CityName: "Lisbon", CityLat: f64p(38.72), CityLon: f64p(-9.14)}},
	}
	w := &fakeWeather{byCoord: map[string][]localcontext.WeatherDay{
		coordKey(38.72, -9.14): rainyDays(1),
	}}

	h := newPackingHandler(t, withCoords, w, true)
	resp, err := h.SuggestPacking(authed(context.Background(), userID),
		connect.NewRequest(&tripv1.SuggestPackingRequest{TripId: withCoords.ID.String()}))
	if err != nil {
		t.Fatalf("SuggestPacking: %v", err)
	}
	if !resp.Msg.WeatherIsEstimated {
		t.Error("a stub forecast that was used must be labelled estimated")
	}

	// No coordinates means no forecast was fetched, so there is nothing to label.
	noCoords := &Trip{
		ID: uuid.New(), UserID: userID, CityName: "Lisbon",
		Days: []TripDay{{DayNumber: 1, CityName: "Lisbon"}},
	}
	h2 := newPackingHandler(t, noCoords, w, true)
	resp2, err := h2.SuggestPacking(authed(context.Background(), userID),
		connect.NewRequest(&tripv1.SuggestPackingRequest{TripId: noCoords.ID.String()}))
	if err != nil {
		t.Fatalf("SuggestPacking: %v", err)
	}
	if resp2.Msg.WeatherIsEstimated {
		t.Error("nothing to label when no forecast was used")
	}
}

func TestSuggestPacking_RejectsABadTripID(t *testing.T) {
	h := newPackingHandler(t, &Trip{}, nil, false)
	_, err := h.SuggestPacking(authed(context.Background(), uuid.New()),
		connect.NewRequest(&tripv1.SuggestPackingRequest{TripId: "not-a-uuid"}))
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}
