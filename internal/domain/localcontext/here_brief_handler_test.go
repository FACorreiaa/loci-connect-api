package localcontext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
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

type slowPlaces struct{}

func (slowPlaces) Place(ctx context.Context, _, _ float64) (Place, error) {
	select {
	case <-ctx.Done():
		return Place{}, ctx.Err()
	case <-time.After(2 * time.Second):
		return Place{}, errors.New("geocoder timed out")
	}
}

// Only news needs the place. A hanging geocoder must neither delay the
// weather nor hold the RPC past its own short deadline.
func TestGetHereBriefSlowGeocoderDoesNotHoldWeather(t *testing.T) {
	old := herePlaceTimeout
	herePlaceTimeout = 100 * time.Millisecond
	t.Cleanup(func() { herePlaceTimeout = old })

	h := NewHandler(&countingWeather{days: []WeatherDay{{Date: time.Now(), Condition: "Clear"}}}, false, quietLogger()).
		WithPlaces(slowPlaces{})
	started := time.Now()
	resp, err := h.GetHereBrief(authed(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.69, Longitude: -8.83}))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("slow geocoder held the brief for %v", elapsed)
	}
	if len(resp.Msg.GetWeather()) != 1 {
		t.Errorf("weather missing: %+v", resp.Msg)
	}
}

// Transport errors carry the request URL, and the URL carries the caller's
// position. None of it may reach the logs.
func TestGetHereBriefLogsNoCoordinates(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	leak := &url.Error{Op: "Get", URL: "https://api.bigdatacloud.net/data/reverse-geocode-client?latitude=41.690000&longitude=-8.830000", Err: errors.New("i/o timeout")}
	h := NewHandler(&countingWeather{err: fmt.Errorf("open-meteo request: %w", &url.Error{Op: "Get", URL: "https://api.open-meteo.com/v1/forecast?latitude=41.69&longitude=-8.83", Err: errors.New("i/o timeout")})}, false, logger).
		WithPlaces(&fakePlaces{err: fmt.Errorf("bigdatacloud request: %w", leak)})
	if _, err := h.GetHereBrief(authed(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.69, Longitude: -8.83})); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "latitude") || strings.Contains(out, "41.69") {
		t.Errorf("coordinates leaked into logs:\n%s", out)
	}
	if !strings.Contains(out, "i/o timeout") {
		t.Errorf("the failure itself should still be logged:\n%s", out)
	}
}
