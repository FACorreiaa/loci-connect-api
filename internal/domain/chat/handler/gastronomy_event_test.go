package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	gastronomyv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gastronomy"
)

func TestMapEventToProto_Gastronomy(t *testing.T) {
	h := &ChatHandler{}
	sessionID := uuid.New().String()
	lat, lon := 41.1456, -8.6110
	event := locitypes.StreamEvent{
		Type:      locitypes.EventTypeGastronomy,
		Timestamp: time.Now(),
		EventID:   uuid.New().String(),
		Data: locitypes.StreamGastronomyData{
			SessionID: sessionID,
			Gastronomy: locitypes.CityGastronomy{
				CityName: "Porto",
				Overview: "Hearty northern cooking.",
				Dishes: []locitypes.Dish{{
					Name:        "Francesinha",
					Category:    locitypes.DishCategoryMain,
					IsSignature: true,
					Places: []locitypes.GastronomyPlace{{
						Name: "Café Santiago", Latitude: &lat, Longitude: &lon,
					}},
				}},
			},
		},
	}

	resp, err := h.mapEventToProto(context.Background(), event, uuid.New())
	if err != nil {
		t.Fatalf("mapEventToProto: %v", err)
	}
	if resp.GetEventType() != chatv1.StreamEventType_STREAM_EVENT_TYPE_GASTRONOMY {
		t.Fatalf("EventType = %v", resp.GetEventType())
	}
	gp := resp.GetGastronomy()
	if gp == nil {
		t.Fatalf("expected a gastronomy payload, got %T", resp.GetPayload())
	}
	if gp.GetSessionId() != sessionID {
		t.Errorf("SessionId = %q, want %q", gp.GetSessionId(), sessionID)
	}
	dishes := gp.GetGastronomy().GetDishes()
	if len(dishes) != 1 || dishes[0].GetName() != "Francesinha" {
		t.Fatalf("dishes = %+v", dishes)
	}
	if dishes[0].GetCategory() != gastronomyv1.DishCategory_DISH_CATEGORY_MAIN || !dishes[0].GetIsSignature() {
		t.Errorf("dish = %+v", dishes[0])
	}
	place := dishes[0].GetPlaces()[0]
	if place.GetName() != "Café Santiago" || place.GetLatitude() != lat || place.GetLongitude() != lon {
		t.Errorf("place = %+v", place)
	}
}

// The itinerary event carries the gastronomy on the full response too, so a
// client that missed the early event still gets it, and so does a restore.
func TestMapEventToProto_ItineraryCarriesGastronomy(t *testing.T) {
	h := &ChatHandler{}
	event := locitypes.StreamEvent{
		Type:      locitypes.EventTypeItinerary,
		Timestamp: time.Now(),
		EventID:   uuid.New().String(),
		Data: locitypes.AiCityResponse{
			SessionID:       uuid.New(),
			GeneralCityData: locitypes.GeneralCityData{City: "Porto"},
			Gastronomy: &locitypes.CityGastronomy{
				CityName: "Porto",
				Dishes:   []locitypes.Dish{{Name: "Tripas à moda do Porto", Category: "unknown"}},
			},
		},
	}
	resp, err := h.mapEventToProto(context.Background(), event, uuid.New())
	if err != nil {
		t.Fatalf("mapEventToProto: %v", err)
	}
	g := resp.GetItinerary().GetCityResponse().GetGastronomy()
	if g == nil || len(g.GetDishes()) != 1 {
		t.Fatalf("gastronomy = %+v", g)
	}
	// An unrecognised category falls back to main rather than UNSPECIFIED.
	if got := g.GetDishes()[0].GetCategory(); got != gastronomyv1.DishCategory_DISH_CATEGORY_MAIN {
		t.Errorf("category = %v, want MAIN", got)
	}
}
