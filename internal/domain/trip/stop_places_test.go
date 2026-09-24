package trip

import (
	"context"
	"errors"
	"testing"

	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type fakePlaceLookup struct {
	places map[uuid.UUID]*locitypes.POIDetailedInfo
	calls  int
}

func (f *fakePlaceLookup) GetPOIByID(_ context.Context, id uuid.UUID) (*locitypes.POIDetailedInfo, error) {
	f.calls++
	if p, ok := f.places[id]; ok {
		return p, nil
	}
	return nil, errors.New("not found")
}

func draftWithStops(poiIDs ...string) *tripv1.TripDraft {
	day := &tripv1.TripDay{Id: "d1"}
	for i, id := range poiIDs {
		day.Stops = append(day.Stops, &tripv1.TripStop{Id: "s" + string(rune('a'+i)), PoiId: id, Name: "Stop " + id})
	}
	return &tripv1.TripDraft{Id: "t1", Days: []*tripv1.TripDay{day}}
}

// Every stop that names a real place carries that place: its coordinates are
// what the phone uses for geofences and the "Next · 1.2 km" line.
func TestHydrateStopsFillsPOIFromTheLookup(t *testing.T) {
	colosseum := uuid.New()
	lookup := &fakePlaceLookup{places: map[uuid.UUID]*locitypes.POIDetailedInfo{
		colosseum: {ID: colosseum, Name: "Colosseum", Latitude: 41.8902, Longitude: 12.4922, Category: "Landmark"},
	}}
	h := NewHandler(nil, "", nil, nil).WithPlaces(lookup)
	draft := draftWithStops(colosseum.String())

	h.hydrateStops(context.Background(), draft)

	poi := draft.Days[0].Stops[0].GetPoi()
	require.NotNil(t, poi)
	require.InDelta(t, 41.8902, poi.GetLatitude(), 1e-6)
	require.Equal(t, "Colosseum", poi.GetName())
}

// A stop whose place is gone, or whose id is not a uuid, keeps its name and
// notes and simply has no place; one bad stop does not spoil the others.
func TestHydrateStopsToleratesMissingAndInvalidIDs(t *testing.T) {
	known := uuid.New()
	lookup := &fakePlaceLookup{places: map[uuid.UUID]*locitypes.POIDetailedInfo{known: {ID: known, Name: "Pantheon", Latitude: 41.8986, Longitude: 12.4769}}}
	h := NewHandler(nil, "", nil, nil).WithPlaces(lookup)
	draft := draftWithStops("not-a-uuid", uuid.New().String(), known.String())

	h.hydrateStops(context.Background(), draft)

	stops := draft.Days[0].Stops
	require.Nil(t, stops[0].GetPoi())
	require.Nil(t, stops[1].GetPoi())
	require.Equal(t, "Pantheon", stops[2].GetPoi().GetName())
	require.Equal(t, 2, lookup.calls, "the invalid id is never looked up")
}

// The same place on two stops is fetched once; without a lookup nothing happens.
func TestHydrateStopsDedupesAndIsOptional(t *testing.T) {
	id := uuid.New()
	lookup := &fakePlaceLookup{places: map[uuid.UUID]*locitypes.POIDetailedInfo{id: {ID: id, Name: "Twice"}}}
	h := NewHandler(nil, "", nil, nil).WithPlaces(lookup)
	draft := draftWithStops(id.String(), id.String())
	h.hydrateStops(context.Background(), draft)
	require.Equal(t, 1, lookup.calls)
	require.Equal(t, "Twice", draft.Days[0].Stops[1].GetPoi().GetName())

	bare := NewHandler(nil, "", nil, nil)
	plain := draftWithStops(id.String())
	bare.hydrateStops(context.Background(), plain)
	require.Nil(t, plain.Days[0].Stops[0].GetPoi())
}
