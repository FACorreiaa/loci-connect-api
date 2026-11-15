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

// A client places every stop from the stop's own latitude/longitude. A stop
// with a place behind it gets that place's position; a stop the traveller
// pinned themselves keeps theirs.
func TestHydrateStopsGivesEachStopAPin(t *testing.T) {
	castle := uuid.New()
	lookup := &fakePlaceLookup{places: map[uuid.UUID]*locitypes.POIDetailedInfo{
		castle: {ID: castle, Name: "Castelo", Latitude: 38.7139, Longitude: -9.1335, Address: "R. de Santa Cruz"},
	}}
	h := NewHandler(nil, "", nil, nil).WithPlaces(lookup)
	draft := draftWithStops(castle.String(), castle.String())
	ownLat, ownLon, ownAddr := 38.70, -9.14, "Somewhere else"
	draft.Days[0].Stops[1].Latitude, draft.Days[0].Stops[1].Longitude = &ownLat, &ownLon
	draft.Days[0].Stops[1].Address = &ownAddr

	h.hydrateStops(context.Background(), draft)

	fromPlace, own := draft.Days[0].Stops[0], draft.Days[0].Stops[1]
	require.InDelta(t, 38.7139, fromPlace.GetLatitude(), 1e-6)
	require.InDelta(t, -9.1335, fromPlace.GetLongitude(), 1e-6)
	require.Equal(t, "R. de Santa Cruz", fromPlace.GetAddress())
	require.InDelta(t, 38.70, own.GetLatitude(), 1e-6, "the stop's own pin wins")
	require.Equal(t, "Somewhere else", own.GetAddress())
}

// A place with no stored position is at 0,0 in the store. That is not a pin.
func TestHydrateStopsLeavesNoPinForAPlaceWithoutAPosition(t *testing.T) {
	id := uuid.New()
	lookup := &fakePlaceLookup{places: map[uuid.UUID]*locitypes.POIDetailedInfo{id: {ID: id, Name: "Unplaced"}}}
	h := NewHandler(nil, "", nil, nil).WithPlaces(lookup)
	draft := draftWithStops(id.String())

	h.hydrateStops(context.Background(), draft)

	require.Nil(t, draft.Days[0].Stops[0].Latitude)
	require.Nil(t, draft.Days[0].Stops[0].Longitude)
}

// A pin is a pair: half of one is dropped, not stored on the equator.
func TestStopFromProtoTakesAWholePinOrNone(t *testing.T) {
	lat, lon := 38.71, -9.13
	addr := "  Rua Augusta 1  "
	whole := stopFromProto(&tripv1.TripStop{Name: "Pasted", Latitude: &lat, Longitude: &lon, Address: &addr})
	require.NotNil(t, whole.Lat)
	require.InDelta(t, -9.13, *whole.Lon, 1e-9)
	require.Equal(t, "Rua Augusta 1", *whole.Address)

	half := stopFromProto(&tripv1.TripStop{Name: "Half", Latitude: &lat})
	require.Nil(t, half.Lat)
	require.Nil(t, half.Lon)
	require.Nil(t, half.Address)

	back := StopToProto(whole)
	require.InDelta(t, 38.71, back.GetLatitude(), 1e-9)
	require.Equal(t, "Rua Augusta 1", back.GetAddress())
	require.Nil(t, StopToProto(half).Latitude)
}
