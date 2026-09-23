package presenter

import (
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestToAiCityResponseCarriesDomainLists(t *testing.T) {
	site := "https://example.com"
	resp := &locitypes.AiCityResponse{
		Hotels:      []locitypes.HotelDetailedInfo{{Name: "Hotel A", Website: &site}},
		Restaurants: []locitypes.RestaurantDetailedInfo{{Name: "Tasca B"}},
		Activities:  []locitypes.POIDetailedInfo{{Name: "Walk C"}},
	}
	out := ToAiCityResponse(resp)
	if len(out.GetHotels()) != 1 || out.GetHotels()[0].GetName() != "Hotel A" || out.GetHotels()[0].GetWebsite() != site {
		t.Fatalf("hotels not carried: %+v", out.GetHotels())
	}
	if len(out.GetRestaurants()) != 1 || out.GetRestaurants()[0].GetName() != "Tasca B" {
		t.Fatalf("restaurants not carried: %+v", out.GetRestaurants())
	}
	if len(out.GetActivities()) != 1 || out.GetActivities()[0].GetName() != "Walk C" {
		t.Fatalf("activities not carried: %+v", out.GetActivities())
	}
}
