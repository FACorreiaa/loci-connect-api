package localcontext

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type fakePlaces struct {
	p      Place
	err    error
	gotLat float64
	gotLon float64
}

func (f *fakePlaces) Place(_ context.Context, lat, lon float64) (Place, error) {
	f.gotLat, f.gotLon = lat, lon
	return f.p, f.err
}

type countingWeather struct {
	days []WeatherDay
	err  error
	n    int
}

func (f *countingWeather) Forecast(_ context.Context, _, _ float64, days int) ([]WeatherDay, error) {
	f.n = days
	return f.days, f.err
}

func authed() context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, uuid.New().String())
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(discard{}, nil)) }

func TestRoundCoord(t *testing.T) {
	for in, want := range map[float64]float64{41.69412: 41.69, -8.83499: -8.83, -8.8351: -8.84, 0: 0} {
		if got := roundCoord(in); got != want {
			t.Errorf("roundCoord(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestGetHereBriefRequiresAuth(t *testing.T) {
	h := NewHandler(&countingWeather{}, false, quietLogger())
	_, err := h.GetHereBrief(context.Background(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.69, Longitude: -8.83}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("want unauthenticated, got %v", err)
	}
}

func TestGetHereBriefHappyPath(t *testing.T) {
	svc, _ := hereHarness(t, &memPrefs{})
	places := &fakePlaces{p: herePlace}
	w := &countingWeather{days: []WeatherDay{{Date: time.Now(), HighC: 22, LowC: 14, Condition: "Clear"}}}
	h := NewHandler(w, false, quietLogger()).WithPlaces(places).WithNewsTicker(svc)

	resp, err := h.GetHereBrief(authed(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.694123, Longitude: -8.832987}))
	if err != nil {
		t.Fatal(err)
	}
	m := resp.Msg
	if places.gotLat != 41.69 || places.gotLon != -8.83 {
		t.Errorf("geocoder saw unrounded coords %v,%v", places.gotLat, places.gotLon)
	}
	if w.n != 3 || len(m.GetWeather()) != 1 {
		t.Errorf("weather: asked %d days, got %d", w.n, len(m.GetWeather()))
	}
	if m.GetPlace().GetLocality() != "Viana do Castelo" || m.GetPlace().GetCountryCode() != "PT" {
		t.Errorf("place = %+v", m.GetPlace())
	}
	if len(m.GetLocal()) != 5 || len(m.GetDisruption()) != 1 || len(m.GetWhatsOn()) != 1 {
		t.Errorf("lists = %d/%d/%d", len(m.GetLocal()), len(m.GetDisruption()), len(m.GetWhatsOn()))
	}
}

func TestGetHereBriefDegradesPerPart(t *testing.T) {
	// Geocoder down, weather down, no news service, no signals: still OK.
	h := NewHandler(&countingWeather{err: errors.New("boom")}, true, quietLogger()).
		WithPlaces(&fakePlaces{err: errors.New("geo down")})
	resp, err := h.GetHereBrief(authed(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.69, Longitude: -8.83}))
	if err != nil {
		t.Fatalf("a failing part must not fail the RPC: %v", err)
	}
	if len(resp.Msg.GetWeather()) != 0 || resp.Msg.GetPlace().GetLocality() != "" || !resp.Msg.GetWeatherIsEstimated() {
		t.Errorf("resp = %+v", resp.Msg)
	}

	// Geocoder down but weather up: weather still returned.
	h2 := NewHandler(&countingWeather{days: []WeatherDay{{Date: time.Now(), Condition: "Rain"}}}, false, quietLogger()).
		WithPlaces(&fakePlaces{err: errors.New("geo down")})
	resp2, err := h2.GetHereBrief(authed(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.69, Longitude: -8.83}))
	if err != nil || len(resp2.Msg.GetWeather()) != 1 {
		t.Errorf("weather must survive a geocoder failure: %+v %v", resp2.Msg, err)
	}
}
