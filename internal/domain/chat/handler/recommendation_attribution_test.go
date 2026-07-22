package handler

import (
	"testing"

	"github.com/google/uuid"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/poi"
	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/recommendation"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestAttributePOIsUsesServerIdentityAndStableRun(t *testing.T) {
	userID := uuid.New()
	poiID := uuid.New().String()
	event := locitypes.StreamEvent{Type: "nearby", EventID: "run-123"}
	pois := []*poiv1.POIDetailedInfo{{Id: poiID}}

	attributePOIs(pois, event, userID)

	trace := pois[0].GetRecommendationTrace()
	if trace == nil {
		t.Fatal("expected recommendation trace")
	}
	if trace.GetRunId() != event.EventID || trace.GetItemId() != poiID || trace.GetRank() != 0 {
		t.Fatalf("unexpected trace identity: %+v", trace)
	}
	if trace.GetExperimentVariant() == "" || trace.GetExperimentVariant() == "assigned" {
		t.Fatalf("expected server experiment variant, got %q", trace.GetExperimentVariant())
	}
	if trace.GetSurface() != recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_NEARBY {
		t.Fatalf("unexpected surface: %s", trace.GetSurface())
	}
}

func TestAttributeCityResponseAttributesNestedItinerary(t *testing.T) {
	response := &chatv1.AiCityResponse{
		PointsOfInterest: []*poiv1.POIDetailedInfo{{Id: uuid.New().String()}},
		ItineraryResponse: &chatv1.AIItineraryResponse{
			PointsOfInterest: []*poiv1.POIDetailedInfo{{Id: uuid.New().String()}},
			Restaurants:      []*poiv1.POIDetailedInfo{{Id: uuid.New().String()}},
		},
	}
	event := locitypes.StreamEvent{Type: locitypes.EventTypeItinerary, EventID: "itinerary-run"}

	attributeCityResponse(response, event, uuid.New())

	for _, poi := range []*poiv1.POIDetailedInfo{
		response.GetPointsOfInterest()[0],
		response.GetItineraryResponse().GetPointsOfInterest()[0],
		response.GetItineraryResponse().GetRestaurants()[0],
	} {
		if poi.GetRecommendationTrace().GetRunId() != event.EventID {
			t.Fatalf("missing nested attribution for %s", poi.GetId())
		}
	}
}
