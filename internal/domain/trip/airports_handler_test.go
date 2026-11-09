package trip

import (
	"context"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

func airportsHandler(t *testing.T) (*Handler, context.Context, *flights.Airports) {
	t.Helper()
	ap, err := flights.LoadAirports()
	require.NoError(t, err)
	mem := &memTrips{}
	svc := NewService(mem, mem, flights.NewDeepLinks(ap, "", slog.Default()))
	h := NewHandler(mem, "", nil, nil).WithPlan(svc).WithAirports(ap)
	return h, authed(context.Background(), uuid.New()), ap
}

func TestSearchAirportsHandler(t *testing.T) {
	h, ctx, _ := airportsHandler(t)
	res, err := h.SearchAirports(ctx, connect.NewRequest(&tripv1.SearchAirportsRequest{Query: "lis"}))
	require.NoError(t, err)
	require.NotEmpty(t, res.Msg.GetOptions())
	first := res.Msg.GetOptions()[0]
	require.Equal(t, "LIS", first.GetIata())
	require.Equal(t, tripv1.FlightPlaceKind_FLIGHT_PLACE_KIND_AIRPORT, first.GetKind())
	require.Equal(t, "PT", first.GetCountryCode())
	require.LessOrEqual(t, len(res.Msg.GetOptions()), 8, "limit 0 means 8")

	_, err = h.SearchAirports(context.Background(), connect.NewRequest(&tripv1.SearchAirportsRequest{Query: "lis"}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestSearchAirportsHandlerWithoutDataIsEmpty(t *testing.T) {
	mem := &memTrips{}
	h := NewHandler(mem, "", nil, nil).WithPlan(NewService(mem, mem, flights.DeepLinks{}))
	res, err := h.SearchAirports(authed(context.Background(), uuid.New()), connect.NewRequest(&tripv1.SearchAirportsRequest{Query: "lis"}))
	require.NoError(t, err)
	require.Empty(t, res.Msg.GetOptions())
}

func TestResolveFlightDestinationHandler(t *testing.T) {
	h, ctx, _ := airportsHandler(t)
	lat, lon := 41.39, 2.17
	res, err := h.ResolveFlightDestination(ctx, connect.NewRequest(&tripv1.ResolveFlightDestinationRequest{
		City: "Barcelona", CountryCode: "es", Latitude: &lat, Longitude: &lon,
	}))
	require.NoError(t, err)
	require.Equal(t, "BCN", res.Msg.GetDestination().GetIata())

	res, err = h.ResolveFlightDestination(ctx, connect.NewRequest(&tripv1.ResolveFlightDestinationRequest{City: "London", CountryCode: "GB"}))
	require.NoError(t, err)
	require.Equal(t, "LON", res.Msg.GetDestination().GetIata())
	require.Equal(t, tripv1.FlightPlaceKind_FLIGHT_PLACE_KIND_METRO, res.Msg.GetDestination().GetKind())
	require.Equal(t, "London (all airports)", res.Msg.GetDestination().GetName())

	res, err = h.ResolveFlightDestination(ctx, connect.NewRequest(&tripv1.ResolveFlightDestinationRequest{City: "Atlantis"}))
	require.NoError(t, err)
	require.Nil(t, res.Msg.Destination)
}

func TestBuildFlightLinksResolvesSkyscanner(t *testing.T) {
	h, ctx, _ := airportsHandler(t)
	res, err := h.BuildFlightLinks(ctx, connect.NewRequest(&tripv1.BuildFlightLinksRequest{
		Origin:      &tripv1.FlightPlace{Name: "Lisbon"},
		Destination: &tripv1.FlightPlace{Name: "Porto"},
		DepartDate:  "2026-12-04", Passengers: 1,
	}))
	require.NoError(t, err)
	require.Len(t, res.Msg.GetLinks(), 2)
	require.Contains(t, res.Msg.GetLinks()[1].GetUrl(), "/lis/opo/")
	require.Contains(t, res.Msg.GetLinks()[0].GetUrl(), "Lisbon")
	require.Equal(t, "skyscanner", res.Msg.GetLinks()[1].GetProvider())
	require.False(t, res.Msg.GetLinks()[1].GetSponsored())
}

// Barcelona is also a large airport in Venezuela (BLA): a bare name is
// ambiguous, so Skyscanner is left off rather than risk the wrong continent.
// The Find flights sheet sends the resolved code and is not affected.
func TestBuildFlightLinksAmbiguousNameStaysGoogleOnly(t *testing.T) {
	h, ctx, _ := airportsHandler(t)
	res, err := h.BuildFlightLinks(ctx, connect.NewRequest(&tripv1.BuildFlightLinksRequest{
		Origin:      &tripv1.FlightPlace{Name: "Lisbon"},
		Destination: &tripv1.FlightPlace{Name: "Barcelona"},
		DepartDate:  "2026-12-04", Passengers: 1,
	}))
	require.NoError(t, err)
	require.Len(t, res.Msg.GetLinks(), 1)
}

func TestFlightLinkToProtoCarriesSponsored(t *testing.T) {
	p := flightLinkToProto(flights.Link{Provider: "skyscanner", Label: "Skyscanner", URL: "https://x", Sponsored: true})
	require.True(t, p.GetSponsored())
	require.Equal(t, "https://x", p.GetUrl())
}
