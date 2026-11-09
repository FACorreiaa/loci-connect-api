package trip

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

func TestRebuildFlightLinksReplacesStored(t *testing.T) {
	depart := time.Date(2026, 12, 4, 0, 0, 0, 0, time.UTC)
	stored := []flights.Link{{Provider: "google_flights", Label: "Google Flights", URL: "old"}}
	fs := []TripFlight{{
		Origin:      flights.Place{Name: "Lisbon", IATA: "LIS"},
		Destination: flights.Place{Name: "Barcelona", IATA: "BCN"},
		DepartDate:  depart, Passengers: 1, Links: stored,
	}}

	rebuildFlightLinks(fs, flights.DeepLinks{SkyscannerTemplate: "https://aff.example/r?u={url}"})

	require.Len(t, fs[0].Links, 2)
	require.NotEqual(t, "old", fs[0].Links[0].URL)
	require.Equal(t, "skyscanner", fs[0].Links[1].Provider)
	require.True(t, fs[0].Links[1].Sponsored)
}

func TestRebuildFlightLinksNilProviderNoOp(t *testing.T) {
	stored := []flights.Link{{Provider: "google_flights", URL: "old"}}
	fs := []TripFlight{{Links: stored}}
	rebuildFlightLinks(fs, nil)
	require.Equal(t, stored, fs[0].Links)
}

func TestNewRepositoryWithFlightLinksOption(t *testing.T) {
	r := NewRepository(nil, testLogger(), WithFlightLinks(flights.DeepLinks{})).(*repository)
	require.NotNil(t, r.links)
	require.Nil(t, NewRepository(nil, testLogger()).(*repository).links)
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
