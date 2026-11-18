package trip

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
	"github.com/FACorreiaa/loci-connect-api/pkg/routing"
	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

type fakeAround struct {
	got transit.Surroundings
	err error
}

func (f fakeAround) Around(context.Context, transit.LatLon) (transit.Surroundings, error) {
	return f.got, f.err
}

// fakeRouter routes every walk as 1.25x the straight line at 75 m a minute,
// and counts what it was asked.
type fakeRouter struct {
	calls int
	err   error
}

func (f *fakeRouter) Route(_ context.Context, from, to routing.LatLon, opts routing.Options) (routing.Leg, error) {
	f.calls++
	if f.err != nil {
		return routing.Leg{}, f.err
	}
	if opts.Profile != routing.ProfileWalking {
		return routing.Leg{}, errors.New("stay transport routes ordinary walks")
	}
	d := float64(transit.DistanceM(transit.LatLon(from), transit.LatLon(to))) * 1.25
	return routing.Leg{DistanceM: d, DurationS: d / 75 * 60}, nil
}

func gracia() transit.Surroundings {
	return transit.Surroundings{
		FetchedAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
		Stops: []transit.Stop{
			{Name: "Trav de Dalt - La Granja", Mode: transit.ModeBus, Lines: []string{"24", "V17"}, Lat: 41.4089, Lon: 2.1535, DistanceM: 180},
			{Name: "Lesseps", Mode: transit.ModeMetro, Lines: []string{"L3"}, Lat: 41.4066, Lon: 2.1527, DistanceM: 520},
		},
	}
}

func verdiStay() TripStay {
	lat, lon := 41.4105, 2.1560
	return TripStay{CityName: "Barcelona", Name: "Verdi 254", Lat: &lat, Lon: &lon}
}

func pinned(name string, lat, lon float64) *tripv1.TripStop {
	return &tripv1.TripStop{Name: name, Latitude: &lat, Longitude: &lon}
}

func barcelonaDraft() *tripv1.TripDraft {
	return &tripv1.TripDraft{CityName: "Barcelona", Days: []*tripv1.TripDay{
		// Day 1 starts round the corner: Park Güell, placed by its POI.
		{Id: "d1", DayNumber: 1, Stops: []*tripv1.TripStop{
			{Name: "No position"},
			{Name: "Park Güell", Poi: &poiv1.POIDetailedInfo{Latitude: ptrF(41.4145), Longitude: ptrF(2.1527)}},
		}},
		// Day 2 starts across town.
		{Id: "d2", DayNumber: 2, Stops: []*tripv1.TripStop{pinned("Barceloneta", 41.3784, 2.1925)}},
		// Day 3 is in another city; day 4 has nowhere to start.
		{Id: "d3", DayNumber: 3, CityName: "Girona", Stops: []*tripv1.TripStop{pinned("Cathedral", 41.9874, 2.8262)}},
		{Id: "d4", DayNumber: 4, Stops: []*tripv1.TripStop{{Name: "Somewhere"}}},
	}}
}

func ptrF(v float64) *float64 { return &v }

func airports(t *testing.T) *flights.Airports {
	t.Helper()
	a, err := flights.LoadAirports()
	require.NoError(t, err)
	return a
}

func TestStayTransport(t *testing.T) {
	router := &fakeRouter{}
	got := stayTransport(context.Background(), barcelonaDraft(), verdiStay(), fakeAround{got: gracia()}, router, airports(t))

	require.Equal(t, tripv1.StayTransportStatus_STAY_TRANSPORT_STATUS_READY, got.GetStatus())
	require.Equal(t, "Verdi 254", got.GetStayName())
	require.Equal(t, transit.Attribution, got.GetAttribution())
	require.Equal(t, int64(1793523600), got.GetFetchedAt().GetSeconds())

	require.Len(t, got.GetStops(), 2)
	bus := got.GetStops()[0]
	require.Equal(t, tripv1.TransitMode_TRANSIT_MODE_BUS, bus.GetMode())
	require.Equal(t, []string{"24", "V17"}, bus.GetLines())
	require.True(t, bus.GetRouted())
	require.Positive(t, bus.GetWalkMinutes())

	require.Len(t, got.GetDayStarts(), 2, "the Girona day and the day with no placed stop are left out")
	near, far := got.GetDayStarts()[0], got.GetDayStarts()[1]
	require.Equal(t, "Park Güell", near.GetFirstStopName(), "the first stop that has a position")
	require.Equal(t, "walk", near.GetSuggestedMode())
	require.True(t, near.GetRouted())
	require.Equal(t, "d2", far.GetDayId())
	require.Equal(t, "transit", far.GetSuggestedMode())
	require.False(t, far.GetRouted(), "a start too far to walk is not routed on foot")
	require.Greater(t, far.GetDistanceM(), int32(4000))
	require.True(t, strings.HasPrefix(far.GetGoogleDirectionsUrl(), "https://www.google.com/maps/dir/?"))
	require.Contains(t, far.GetGoogleDirectionsUrl(), "travelmode=transit")
	require.Contains(t, far.GetAppleDirectionsUrl(), "dirflg=r")
	require.Equal(t, 3, router.calls, "two stops and the one walkable day")

	ap := got.GetAirport()
	require.NotNil(t, ap)
	require.Equal(t, "BCN", ap.GetIata())
	require.InDelta(t, 14, ap.GetDistanceKm(), 4)
	require.Contains(t, ap.GetGoogleDirectionsUrl(), "travelmode=transit")
}

func TestStayTransport_Degrades(t *testing.T) {
	ctx := context.Background()

	// No position: nothing can be measured from the stay.
	noPin := stayTransport(ctx, barcelonaDraft(), TripStay{CityName: "Barcelona", Name: "Verdi 254"}, fakeAround{got: gracia()}, &fakeRouter{}, airports(t))
	require.Equal(t, tripv1.StayTransportStatus_STAY_TRANSPORT_STATUS_NO_COORDINATES, noPin.GetStatus())
	require.Empty(t, noPin.GetStops())
	require.Empty(t, noPin.GetDayStarts())
	require.Nil(t, noPin.GetAirport())

	// The map is down: no stops, but the days still link out.
	down := stayTransport(ctx, barcelonaDraft(), verdiStay(), fakeAround{err: transit.ErrUnavailable}, nil, nil)
	require.Equal(t, tripv1.StayTransportStatus_STAY_TRANSPORT_STATUS_UNAVAILABLE, down.GetStatus())
	require.Empty(t, down.GetStops())
	require.Empty(t, down.GetAttribution())
	require.Len(t, down.GetDayStarts(), 2)
	require.False(t, down.GetDayStarts()[0].GetRouted(), "no router: an estimate, and it says so")
	require.Positive(t, down.GetDayStarts()[0].GetWalkMinutes())

	// No map configured at all behaves the same.
	none := stayTransport(ctx, barcelonaDraft(), verdiStay(), nil, nil, nil)
	require.Equal(t, tripv1.StayTransportStatus_STAY_TRANSPORT_STATUS_UNAVAILABLE, none.GetStatus())

	// The router's budget is spent: estimates, not errors.
	spent := &fakeRouter{err: routing.ErrUnavailable}
	est := stayTransport(ctx, barcelonaDraft(), verdiStay(), fakeAround{got: gracia()}, spent, nil)
	require.False(t, est.GetStops()[0].GetRouted())
	require.EqualValues(t, transit.EstimateWalkMinutes(180), est.GetStops()[0].GetWalkMinutes())
	require.False(t, est.GetDayStarts()[0].GetRouted())
}
