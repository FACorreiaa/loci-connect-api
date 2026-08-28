package locitypes

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// The bug this guards against, observed live: the model returned a 40-character
// id, uuid.UUID rejected it, json.Unmarshal failed for the whole array, and
// handleGeneralPoisFromResponse logged the error and returned — so an entire
// generated itinerary arrived with zero points of interest and the map showed
// "Loading map data…" forever.
//
// One bad id must cost that POI its id, never the batch.
func TestPOIUnmarshal_BadIDDoesNotDiscardTheBatch(t *testing.T) {
	body := `{"points_of_interest":[
	 {"id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890xxxx","name":"Belem Tower","latitude":38.69,"longitude":-9.21},
	 {"id":"3f1a2b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b","name":"Jeronimos","latitude":38.697,"longitude":-9.206}
	]}`

	var out struct {
		POIs []POIDetailedInfo `json:"points_of_interest"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("a malformed id must not fail the batch: %v", err)
	}
	if len(out.POIs) != 2 {
		t.Fatalf("expected both POIs, got %d", len(out.POIs))
	}

	// The bad one keeps everything except its id.
	if out.POIs[0].ID != uuid.Nil {
		t.Errorf("an unparseable id should become uuid.Nil, got %v", out.POIs[0].ID)
	}
	if out.POIs[0].Name != "Belem Tower" {
		t.Errorf("name lost: %q", out.POIs[0].Name)
	}
	// Coordinates are the field the map needs; losing them is the visible bug.
	if out.POIs[0].Latitude == 0 || out.POIs[0].Longitude == 0 {
		t.Errorf("coordinates lost: %v,%v", out.POIs[0].Latitude, out.POIs[0].Longitude)
	}

	// A valid id still parses.
	if out.POIs[1].ID == uuid.Nil {
		t.Error("a valid id must survive")
	}
}

func TestPOIUnmarshal_IDShapes(t *testing.T) {
	valid := uuid.MustParse("3f1a2b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b")
	tests := []struct {
		name string
		raw  string
		want uuid.UUID
	}{
		{"valid uuid", `{"id":"3f1a2b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b"}`, valid},
		{"absent", `{"name":"x"}`, uuid.Nil},
		{"empty string", `{"id":""}`, uuid.Nil},
		{"null", `{"id":null}`, uuid.Nil},
		{"too long", `{"id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890xxxx"}`, uuid.Nil},
		{"too short", `{"id":"abc"}`, uuid.Nil},
		{"prefixed", `{"id":"poi_3f1a2b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b"}`, uuid.Nil},
		// A model has no obligation to send a string at all.
		{"a number", `{"id":42}`, uuid.Nil},
		{"an object", `{"id":{"value":"x"}}`, uuid.Nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p POIDetailedInfo
			if err := json.Unmarshal([]byte(tt.raw), &p); err != nil {
				t.Fatalf("must not error: %v", err)
			}
			if p.ID != tt.want {
				t.Errorf("got %v, want %v", p.ID, tt.want)
			}
		})
	}
}

// city_id gets the same treatment, for the same reason.
func TestPOIUnmarshal_BadCityIDIsTolerated(t *testing.T) {
	var p POIDetailedInfo
	if err := json.Unmarshal([]byte(`{"city_id":"not-a-uuid","name":"x"}`), &p); err != nil {
		t.Fatalf("must not error: %v", err)
	}
	if p.CityID != uuid.Nil {
		t.Errorf("got %v, want Nil", p.CityID)
	}
	if p.Name != "x" {
		t.Errorf("name lost: %q", p.Name)
	}
}

// The leniency that already existed must keep working.
func TestPOIUnmarshal_OpeningHoursAndStarRatingStillFlexible(t *testing.T) {
	var asMap POIDetailedInfo
	if err := json.Unmarshal([]byte(`{"opening_hours":{"mon":"9-5"},"star_rating":4.5}`), &asMap); err != nil {
		t.Fatalf("map form: %v", err)
	}
	if asMap.OpeningHours["mon"] != "9-5" || asMap.StarRating != "4.5" {
		t.Errorf("got %v / %q", asMap.OpeningHours, asMap.StarRating)
	}

	var asString POIDetailedInfo
	if err := json.Unmarshal([]byte(`{"opening_hours":"Daily 9-5","star_rating":"4"}`), &asString); err != nil {
		t.Fatalf("string form: %v", err)
	}
	if asString.OpeningHours["general"] != "Daily 9-5" || asString.StarRating != "4" {
		t.Errorf("got %v / %q", asString.OpeningHours, asString.StarRating)
	}
}

// Round-tripping must not corrupt a real id.
func TestPOIUnmarshal_RoundTrip(t *testing.T) {
	orig := POIDetailedInfo{
		ID:        uuid.MustParse("3f1a2b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b"),
		CityID:    uuid.MustParse("11111111-2222-4333-8444-555555555555"),
		Name:      "Belem Tower",
		Latitude:  38.6916,
		Longitude: -9.2159,
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back POIDetailedInfo
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.ID != orig.ID || back.CityID != orig.CityID || back.Name != orig.Name {
		t.Errorf("round trip lost data: %+v", back)
	}
}
