//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/placecheck"
	poirepo "github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

// recordingMap is the map server: it records what it is asked and around
// where, and knows no place.
type recordingMap struct {
	mu    sync.Mutex
	names [][]string
	near  []transit.LatLon
}

func (m *recordingMap) FindPlaces(_ context.Context, asks []transit.PlaceQuery, near transit.LatLon) ([]transit.PlaceMatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, len(asks))
	for i, a := range asks {
		names[i] = a.Name
	}
	m.names = append(m.names, names)
	m.near = append(m.near, near)
	return make([]transit.PlaceMatch, len(asks)), nil
}

type freeLock struct{}

func (freeLock) TryLock(context.Context) (func(), error) { return func() {}, nil }

// windowOnly lists the trips the real query finds in a window of its own, so
// the tick reads this test's trip and not every other test's.
type windowOnly struct {
	inner    UpcomingTrips
	from, to time.Time
}

func (w windowOnly) Upcoming(ctx context.Context, _, _ time.Time, limit int) ([]TripRef, error) {
	return w.inner.Upcoming(ctx, w.from, w.to, limit)
}

// The runner on a stored trip, with everything real but the map: a trip
// saved to Postgres, its places read from the places table, the real
// planner and the real store of answers. What it proves is the join the
// unit tests cannot: a stored trip comes out of the read path with the
// place's own name and a centre to search around, so the map is asked at
// all. The save hook and the trip read go through the same code.
func TestStopCheckRunner_AsksTheMapAboutAStoredTripsPlaces(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, log)
	userID := newTripUser(t, "refresh-"+uuid.NewString()+"@loci.test")
	lello := seedTripPOI(t, "Livraria Lello")

	// A week no other test plans a trip in, and far enough ahead that the
	// day is not past.
	day := time.Date(2092, 6, 10, 0, 0, 0, 0, time.UTC)
	_, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Porto", Title: "Porto",
		Days: []TripDay{{DayNumber: 1, Date: &day, CityName: "Porto", Stops: []TripStop{
			// The traveller renamed the stop; the place keeps its own name.
			{POIID: lello.String(), Name: "Books before lunch"},
			{Name: "A note with no place behind it"},
		}}},
	}, 0)
	require.NoError(t, err)

	osm := &recordingMap{}
	planner := placecheck.NewPlanner(placecheck.NewService(osm, placecheck.NewRepository(testTripDB)), nil, log)
	h := NewHandler(repo, "", nil, nil).WithPlaces(poirepo.NewRepository(testTripDB, log)).WithStopChecks(planner)
	runner := NewStopCheckRunner(h, planner, windowOnly{NewUpcomingTrips(testTripDB), day, day.AddDate(0, 0, 7)}, freeLock{}, 10, log)

	require.Equal(t, 1, runner.Tick(ctx))
	require.Equal(t, [][]string{{"Livraria Lello"}}, osm.names, "asked for the place's own name, and not for the note")
	require.InDelta(t, 41.14, osm.near[0].Lat, 0.001, "around where the place is stored")
	require.InDelta(t, -8.61, osm.near[0].Lon, 0.001)

	// The map did not have it. That answer is kept, and the runner does not
	// ask again.
	require.Zero(t, runner.Tick(ctx))
	require.Len(t, osm.names, 1)
}
