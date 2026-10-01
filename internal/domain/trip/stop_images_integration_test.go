//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	poipb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type countingStopImages struct {
	inner StopImageLookup
	calls int
}

func (c *countingStopImages) FirstImages(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*poipb.POIImage, error) {
	c.calls++
	return c.inner.FirstImages(ctx, ids)
}

func seedTripPOI(t *testing.T, name string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var cityID, poiID uuid.UUID
	require.NoError(t, testTripDB.QueryRow(ctx,
		`INSERT INTO cities (name, country) VALUES ($1, 'Portugal') RETURNING id`,
		"StopImgCity-"+uuid.NewString()[:8]).Scan(&cityID))
	require.NoError(t, testTripDB.QueryRow(ctx, `
		INSERT INTO points_of_interest (name, location, city_id)
		VALUES ($1, ST_SetSRID(ST_MakePoint(-8.61, 41.14), 4326), $2)
		RETURNING id`, name, cityID).Scan(&poiID))
	return poiID
}

func seedPOIImage(t *testing.T, poiID uuid.UUID, url string, position int) {
	t.Helper()
	_, err := testTripDB.Exec(context.Background(), `
		INSERT INTO poi_images (poi_id, url, licence, attribution, source_page_url, position)
		VALUES ($1, $2, 'CC BY-SA 4.0', 'Photographer', 'https://commons.example/file', $3)`,
		poiID, url, position)
	require.NoError(t, err)
}

// GetTrip and ListTrips fill TripStop.image for any trip, not only City Pack
// stops: the POI's lowest-position picture, with its credit.
func TestTripReadsCarryTheFirstPOIPicture(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "stopimg-"+uuid.NewString()+"@loci.test")

	pictured := seedTripPOI(t, "Livraria Lello")
	seedPOIImage(t, pictured, "https://img.example/second.jpg", 2)
	seedPOIImage(t, pictured, "https://img.example/first.jpg", 0)
	bare := seedTripPOI(t, "No photo yet")

	saved, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Porto", Title: "Porto",
		Days: []TripDay{{DayNumber: 1, CityName: "Porto", Stops: []TripStop{
			{POIID: pictured.String(), Name: "Livraria Lello", OrderIndex: 0},
			{POIID: bare.String(), Name: "No photo yet", OrderIndex: 1},
			{Name: "Free-text stop", OrderIndex: 2},
		}}},
	}, 0)
	require.NoError(t, err)
	_, err = repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Porto", Title: "Porto again",
		Days: []TripDay{{DayNumber: 1, CityName: "Porto", Stops: []TripStop{{POIID: pictured.String(), Name: "Lello", OrderIndex: 0}}}},
	}, 0)
	require.NoError(t, err)

	store := &countingStopImages{inner: NewStopImageStore(testTripDB)}
	h := NewHandler(repo, "", nil, nil).WithStopImages(store)
	uctx := interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: userID.String()})

	got, err := h.GetTrip(uctx, connect.NewRequest(&tripv1.GetTripRequest{TripId: saved.ID.String()}))
	require.NoError(t, err)
	stops := got.Msg.GetDays()[0].GetStops()
	require.Equal(t, "https://img.example/first.jpg", stops[0].GetImage().GetUrl(), "lowest position wins")
	require.Equal(t, "CC BY-SA 4.0", stops[0].GetImage().GetLicence())
	require.Equal(t, "Photographer", stops[0].GetImage().GetAttribution())
	require.Equal(t, "https://commons.example/file", stops[0].GetImage().GetSourcePageUrl())
	require.Nil(t, stops[1].GetImage())
	require.Nil(t, stops[2].GetImage())

	store.calls = 0
	list, err := h.ListTrips(uctx, connect.NewRequest(&tripv1.ListTripsRequest{}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetTrips(), 2)
	for _, tr := range list.Msg.GetTrips() {
		require.Equal(t, "https://img.example/first.jpg", tr.GetDays()[0].GetStops()[0].GetImage().GetUrl())
	}
	require.Equal(t, 1, store.calls, "one picture query for the whole page")
}
