package push

import (
	"testing"

	"github.com/google/uuid"
)

func TestBuildRecapPayload(t *testing.T) {
	trip := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	p := BuildRecapPayload(trip, "Your Lisbon recap is ready", "4 places. Tap to relive the trip.")
	if p.URL != WebOrigin+"/recap/trip/"+trip.String() {
		t.Errorf("url = %q", p.URL)
	}
	if p.Category != RecapCategory || p.ThreadID != "recap" || p.Origin != "recap_ready" {
		t.Errorf("grouping = %q / %q / %q", p.Category, p.ThreadID, p.Origin)
	}
	if p.Title != "Your Lisbon recap is ready" {
		t.Errorf("title = %q", p.Title)
	}
}
