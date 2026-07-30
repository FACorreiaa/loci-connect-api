package localcontext

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/localcontext"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeWeather struct {
	days []WeatherDay
	err  error
}

func (f fakeWeather) Forecast(context.Context, float64, float64, int) ([]WeatherDay, error) {
	return f.days, f.err
}

type fakeCities struct {
	city *locitypes.CityDetail
	err  error
}

func (f fakeCities) FindCityByFuzzyName(context.Context, string) (*locitypes.CityDetail, error) {
	return f.city, f.err
}

type fakePOIs struct {
	n   int
	err error
}

func (f fakePOIs) GetPOIsByCityID(context.Context, uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return make([]locitypes.POIDetailedInfo, f.n), nil
}

func f64(v float64) *float64 { return &v }

func testHandler(w WeatherAdapter, cities CityResolver, pois POICounter) *Handler {
	return NewHandler(w, false, slog.New(slog.NewTextHandler(io.Discard, nil))).WithScoring(cities, pois)
}

func lisbon() *locitypes.CityDetail {
	return &locitypes.CityDetail{
		ID: uuid.New(), Name: "Lisbon", Country: "Portugal",
		CenterLatitude: f64(38.72), CenterLongitude: f64(-9.14),
	}
}

func TestGetGoScore_ResolvesCityByNameAndScoresIt(t *testing.T) {
	h := testHandler(fakeWeather{days: dry(2)}, fakeCities{city: lisbon()}, fakePOIs{n: 9})

	start := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	resp, err := h.GetGoScore(context.Background(), connect.NewRequest(&lcv1.GetGoScoreRequest{
		CityName:  strptr("lisboa"),
		OriginLat: f64(41.15),
		OriginLon: f64(-8.61),
		Start:     timestamppb.New(start),
		End:       timestamppb.New(start.Add(48 * time.Hour)),
	}))
	if err != nil {
		t.Fatalf("GetGoScore: %v", err)
	}

	if resp.Msg.CityName != "Lisbon" {
		t.Errorf("city = %q, want the resolved name Lisbon", resp.Msg.CityName)
	}
	if resp.Msg.Score == nil || resp.Msg.Score.Score == 0 {
		t.Fatalf("expected a scored result, got %+v", resp.Msg.Score)
	}
	if len(resp.Msg.Score.Factors) < 3 {
		t.Errorf("expected the reasoning to travel with the score, got %d factors", len(resp.Msg.Score.Factors))
	}
}

// Coordinates alone must work — that is the "score where I'm standing" case, and
// it has no city row to count POIs against.
func TestGetGoScore_AcceptsCoordinatesWithoutACityRow(t *testing.T) {
	h := testHandler(fakeWeather{days: dry(2)}, fakeCities{}, fakePOIs{n: 5})

	resp, err := h.GetGoScore(context.Background(), connect.NewRequest(&lcv1.GetGoScoreRequest{
		Latitude:  f64(38.72),
		Longitude: f64(-9.14),
	}))
	if err != nil {
		t.Fatalf("GetGoScore: %v", err)
	}
	if resp.Msg.Score.Verdict == "" {
		t.Error("expected a verdict even without a resolved city")
	}
}

// A weather provider outage must not fail the call. The scorer treats a missing
// forecast as unknown, which is materially different from bad weather.
func TestGetGoScore_SurvivesWeatherOutage(t *testing.T) {
	h := testHandler(fakeWeather{err: errors.New("provider down")}, fakeCities{city: lisbon()}, fakePOIs{n: 6})

	resp, err := h.GetGoScore(context.Background(), connect.NewRequest(&lcv1.GetGoScoreRequest{
		CityName: strptr("Lisbon"),
	}))
	if err != nil {
		t.Fatalf("weather failure should not fail the RPC: %v", err)
	}
	if resp.Msg.Score.Score == 0 {
		t.Error("expected a usable score from the remaining inputs")
	}
}

func TestGetGoScore_RequiresADestination(t *testing.T) {
	h := testHandler(fakeWeather{days: dry(2)}, fakeCities{city: lisbon()}, fakePOIs{n: 6})

	_, err := h.GetGoScore(context.Background(), connect.NewRequest(&lcv1.GetGoScoreRequest{}))
	if err == nil {
		t.Fatal("expected InvalidArgument when neither a city nor coordinates are given")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestGetGoScore_UnknownCityIsInvalidArgument(t *testing.T) {
	h := testHandler(fakeWeather{days: dry(2)}, fakeCities{city: nil}, fakePOIs{n: 6})

	_, err := h.GetGoScore(context.Background(), connect.NewRequest(&lcv1.GetGoScoreRequest{
		CityName: strptr("Atlantis"),
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument for an unresolvable city, got %v", err)
	}
}

// A longer window should tolerate the same drive better — proving the window
// actually reaches the scorer rather than defaulting to 48h.
func TestGetGoScore_WindowLengthReachesTheScore(t *testing.T) {
	h := testHandler(fakeWeather{days: dry(2)}, fakeCities{city: lisbon()}, fakePOIs{n: 6})
	start := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)

	score := func(hours int) int32 {
		resp, err := h.GetGoScore(context.Background(), connect.NewRequest(&lcv1.GetGoScoreRequest{
			CityName:  strptr("Lisbon"),
			OriginLat: f64(41.15),
			OriginLon: f64(-8.61),
			Start:     timestamppb.New(start),
			End:       timestamppb.New(start.Add(time.Duration(hours) * time.Hour)),
		}))
		if err != nil {
			t.Fatalf("GetGoScore(%dh): %v", hours, err)
		}
		return resp.Msg.Score.Score
	}

	if long, short := score(120), score(24); long <= short {
		t.Fatalf("same trip should score better over a longer window: 120h=%d vs 24h=%d", long, short)
	}
}

func strptr(s string) *string { return &s }
