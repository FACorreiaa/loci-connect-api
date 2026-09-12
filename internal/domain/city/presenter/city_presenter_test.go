package presenter

import (
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
)

// Coordinates were dropped here for a long time, which quietly made every
// city-search result unusable as a map position: a picker could not send
// origin_lat/origin_lon even for cities we held good coordinates for.
func TestToCityProto_CarriesCoordinatesAndSummary(t *testing.T) {
	lat, lon := 41.14961, -8.61099
	id := uuid.New()

	got := ToCityProto(locitypes.CityDetail{
		ID:              id,
		Name:            "Porto",
		Country:         "Portugal",
		StateProvince:   "Porto",
		AiSummary:       "Northern Portugal city",
		CenterLatitude:  &lat,
		CenterLongitude: &lon,
	})

	if got.GetId() != id.String() || got.GetName() != "Porto" || got.GetCountry() != "Portugal" {
		t.Errorf("identity: %q/%q/%q", got.GetId(), got.GetName(), got.GetCountry())
	}
	if got.CenterLatitude == nil || *got.CenterLatitude != lat {
		t.Errorf("latitude: got %v, want %v", got.CenterLatitude, lat)
	}
	if got.CenterLongitude == nil || *got.CenterLongitude != lon {
		t.Errorf("longitude: got %v, want %v", got.CenterLongitude, lon)
	}
	if got.GetAiSummary() != "Northern Portugal city" {
		t.Errorf("ai summary: got %q", got.GetAiSummary())
	}
	if got.GetStateProvince() != "Porto" {
		t.Errorf("state province: got %q", got.GetStateProvince())
	}
}

// A city with no stored centre is normal — the chat stream writes rows like
// that — and must convert without inventing a position at (0, 0).
func TestToCityProto_LeavesMissingCoordinatesUnset(t *testing.T) {
	got := ToCityProto(locitypes.CityDetail{
		ID: uuid.New(), Name: "Porto", Country: "Unknown",
	})

	if got.CenterLatitude != nil || got.CenterLongitude != nil {
		t.Errorf("expected unset coordinates, got %v/%v", got.CenterLatitude, got.CenterLongitude)
	}
}

// Each element must carry its own state_province pointer; sharing one address
// across the loop would give every city the last one's value.
func TestToCityProtos_DoesNotAliasFields(t *testing.T) {
	protos := ToCityProtos([]locitypes.CityDetail{
		{ID: uuid.New(), Name: "Porto", Country: "Portugal", StateProvince: "Porto"},
		{ID: uuid.New(), Name: "Évora", Country: "Portugal", StateProvince: "Évora"},
	})

	if len(protos) != 2 {
		t.Fatalf("got %d protos", len(protos))
	}
	if protos[0].GetStateProvince() != "Porto" || protos[1].GetStateProvince() != "Évora" {
		t.Errorf("aliased: %q / %q", protos[0].GetStateProvince(), protos[1].GetStateProvince())
	}
}
