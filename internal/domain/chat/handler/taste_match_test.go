package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type everyPlaceScorer struct{ calls int }

func (s *everyPlaceScorer) ScorePOIs(_ context.Context, _ uuid.UUID, ids []string) (map[string]preference.Match, error) {
	s.calls++
	out := map[string]preference.Match{}
	for _, id := range ids {
		out[id] = preference.Match{Score: 0.7, Reasons: []string{"You tend to like cafe"}}
	}
	return out, nil
}

func TestMapEventToProto_GeneralPOIsCarryTasteMatch(t *testing.T) {
	scorer := &everyPlaceScorer{}
	h := (&ChatHandler{}).WithTasteScorer(scorer)
	ev := locitypes.StreamEvent{
		Type:    locitypes.EventTypeGeneralPOI,
		EventID: "run-1",
		Data:    locitypes.StreamDomainListData{POIs: []locitypes.POIDetailedInfo{{ID: uuid.New(), Name: "Café"}}},
	}
	got, err := h.mapEventToProto(context.Background(), ev, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	pois := got.GetGeneralPois().GetPois()
	if len(pois) != 1 || pois[0].GetTasteMatch().GetScore() != 0.7 {
		t.Fatalf("pois = %+v", pois)
	}
}

func TestMapEventToProto_ItineraryCarriesTasteMatchInOneCall(t *testing.T) {
	scorer := &everyPlaceScorer{}
	h := (&ChatHandler{}).WithTasteScorer(scorer)
	ev := locitypes.StreamEvent{
		Type:    locitypes.EventTypeItinerary,
		EventID: "run-2",
		Data: locitypes.AiCityResponse{
			PointsOfInterest: []locitypes.POIDetailedInfo{{ID: uuid.New(), Name: "Sé"}},
			AIItineraryResponse: locitypes.AIItineraryResponse{
				PointsOfInterest: []locitypes.POIDetailedInfo{{ID: uuid.New(), Name: "Belém"}},
				Restaurants:      []locitypes.POIDetailedInfo{{ID: uuid.New(), Name: "Ramiro"}},
			},
		},
	}
	got, err := h.mapEventToProto(context.Background(), ev, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	city := got.GetItinerary().GetCityResponse()
	if city.GetPointsOfInterest()[0].GetTasteMatch() == nil {
		t.Fatal("city POI has no taste match")
	}
	it := city.GetItineraryResponse()
	if it.GetPointsOfInterest()[0].GetTasteMatch() == nil || it.GetRestaurants()[0].GetTasteMatch() == nil {
		t.Fatalf("itinerary places lack taste match: %+v", it)
	}
	if scorer.calls != 1 {
		t.Fatalf("scorer calls = %d, want 1", scorer.calls)
	}
}

func TestMapEventToProto_NoScorerLeavesMatchUnset(t *testing.T) {
	h := &ChatHandler{}
	ev := locitypes.StreamEvent{
		Type:    locitypes.EventTypeGeneralPOI,
		EventID: "run-3",
		Data:    locitypes.StreamDomainListData{POIs: []locitypes.POIDetailedInfo{{ID: uuid.New(), Name: "Café"}}},
	}
	got, err := h.mapEventToProto(context.Background(), ev, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetGeneralPois().GetPois()[0].TasteMatch != nil {
		t.Fatal("no scorer, no match")
	}
}
