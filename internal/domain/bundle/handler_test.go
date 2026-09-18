package bundle

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A pack is a travel guide, so its stops have to arrive with positions.
// loci.trip.TripStop has no latitude or longitude of its own, so the snapshot
// is hydrated into the POI field. Without this the map draws nothing and the
// publish-time coordinate check guards something that never renders.
func TestToDayPB_CarriesCoordinatesToTheWire(t *testing.T) {
	lat, lon := 38.7223, -9.1393
	site := "https://example.test"
	poiID := uuid.New()

	day := Day{
		DayNumber: 2,
		Title:     "Alfama",
		Stops: []Stop{{
			ID: uuid.New(), OrderIndex: 0, POIID: &poiID,
			Name: "Miradouro", Category: "viewpoint", Description: "a view",
			Address: "Largo", Website: &site, Notes: "go at sunset",
			Latitude: &lat, Longitude: &lon,
		}},
	}

	got := toDayPB(day)
	require.Len(t, got.Stops, 1)
	stop := got.Stops[0]

	assert.Equal(t, int32(2), got.DayNumber)
	assert.Equal(t, "Miradouro", stop.Name)
	assert.Equal(t, "go at sunset", stop.Notes)

	require.NotNil(t, stop.Poi, "a stop with coordinates must carry them")
	require.NotNil(t, stop.Poi.Latitude)
	require.NotNil(t, stop.Poi.Longitude)
	assert.InDelta(t, lat, *stop.Poi.Latitude, 0.0001)
	assert.InDelta(t, lon, *stop.Poi.Longitude, 0.0001)
	assert.Equal(t, "viewpoint", stop.Poi.Category)
	assert.Equal(t, site, stop.Poi.Website)
	assert.Equal(t, poiID.String(), stop.Poi.Id)
}

// A stop whose POI was merged away keeps its snapshot, so it still renders.
func TestToDayPB_SurvivesALostPOILink(t *testing.T) {
	lat, lon := 38.7, -9.1
	got := toDayPB(Day{DayNumber: 1, Stops: []Stop{{
		Name: "orphan", Latitude: &lat, Longitude: &lon,
	}}})

	stop := got.Stops[0]
	assert.Empty(t, stop.PoiId, "the link is gone")
	require.NotNil(t, stop.Poi, "but the pack still knows where the place is")
	assert.InDelta(t, lat, *stop.Poi.Latitude, 0.0001)
}

// A stop with no coordinates must not be given 0,0 — that is a real location
// in the Atlantic, and it would pass a null check on the client.
func TestToDayPB_LeavesAPositionlessStopWithoutAPoi(t *testing.T) {
	got := toDayPB(Day{DayNumber: 1, Stops: []Stop{{Name: "nowhere"}}})
	assert.Nil(t, got.Stops[0].Poi)
}
