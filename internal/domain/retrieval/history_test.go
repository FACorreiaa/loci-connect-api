package retrieval

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRenderHistory_EmptyIsBlank(t *testing.T) {
	if got := renderHistory(nil); got != "" {
		t.Fatalf("nil history rendered %q", got)
	}
	if got := renderHistory(&UserHistory{}); got != "" {
		t.Fatalf("empty history rendered %q", got)
	}
}

func TestRenderHistory_ListsEachSection(t *testing.T) {
	march := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)
	got := renderHistory(&UserHistory{
		CityName:  "Lisbon",
		PastTrips: []PastTrip{{City: "Porto", Start: &march}, {City: "Lisbon"}},
		SavedHere: []string{"Manteigaria", "Time Out Market"},
		Lists:     []string{"Coffee to try"},
	})
	for _, want := range []string{
		"YOUR HISTORY",
		"Porto (Mar 2026)",
		"Lisbon",
		"Saved in Lisbon: Manteigaria, Time Out Market",
		"Coffee to try",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered history missing %q:\n%s", want, got)
		}
	}
}

// Fifty trips and a hundred saves must not blow the prompt: the block is capped
// whatever the account holds.
func TestRenderHistory_CappedAtMaxHistoryChars(t *testing.T) {
	h := &UserHistory{CityName: "Lisbon"}
	for i := range 50 {
		h.PastTrips = append(h.PastTrips, PastTrip{City: fmt.Sprintf("A rather long city name number %d", i)})
	}
	for i := range 100 {
		h.SavedHere = append(h.SavedHere, fmt.Sprintf("A saved place with a long name %d", i))
	}
	got := renderHistory(h)
	if n := utf8.RuneCountInString(got); n > MaxHistoryChars {
		t.Fatalf("history block is %d chars, want <= %d", n, MaxHistoryChars)
	}
	if !strings.Contains(got, "YOUR HISTORY") {
		t.Fatalf("capped block lost its heading:\n%s", got)
	}
}

// History is context about the person, so it is rendered even when retrieval
// found no places.
func TestRender_IncludesHistoryWithAndWithoutEvidence(t *testing.T) {
	h := &UserHistory{PastTrips: []PastTrip{{City: "Porto"}}}

	empty := Render(&ContextPacket{History: h})
	if !strings.Contains(empty, "YOUR HISTORY") {
		t.Errorf("empty packet dropped history:\n%s", empty)
	}

	full := Render(&ContextPacket{
		History:  h,
		Evidence: []Evidence{{POIID: mustUUID(t, idA), Name: "Miradouro", Category: "viewpoint"}},
	})
	if !strings.Contains(full, "YOUR HISTORY") {
		t.Errorf("packet with evidence dropped history:\n%s", full)
	}
}
