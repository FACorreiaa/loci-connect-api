package trip

import (
	"testing"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
)

// CanView is the whole sharing policy: every non-owner read goes through it.
func TestCanView(t *testing.T) {
	owner, friend, stranger := uuid.New(), uuid.New(), uuid.New()
	anon := uuid.Nil
	type tc struct {
		name            string
		viewer          uuid.UUID
		vis             Visibility
		access          Access
		friends, blocks bool
		want            bool
	}
	cases := []tc{
		{"owner sees private", owner, VisibilityPrivate, AccessByID, false, false, true},
		{"owner sees own trip even when a block exists", owner, VisibilityPrivate, AccessByID, false, true, true},
		{"friend cannot see private", friend, VisibilityPrivate, AccessByID, true, false, false},
		{"friend cannot see private by link", friend, VisibilityPrivate, AccessByLink, true, false, false},
		{"friend sees friends trip by id", friend, VisibilityFriends, AccessByID, true, false, true},
		{"friend sees friends trip by link", friend, VisibilityFriends, AccessByLink, true, false, true},
		{"stranger cannot see friends trip", stranger, VisibilityFriends, AccessByID, false, false, false},
		{"stranger with link cannot see friends trip", stranger, VisibilityFriends, AccessByLink, false, false, false},
		{"anonymous cannot see friends trip", anon, VisibilityFriends, AccessByLink, false, false, false},
		{"anonymous opens link trip by link", anon, VisibilityLink, AccessByLink, false, false, true},
		{"link trip is not listed by id", stranger, VisibilityLink, AccessByID, false, false, false},
		{"friend cannot list a link trip by id", friend, VisibilityLink, AccessByID, true, false, false},
		{"anyone sees public", anon, VisibilityPublic, AccessByID, false, false, true},
		{"blocked viewer never sees public", stranger, VisibilityPublic, AccessByLink, false, true, false},
		{"blocked friend never sees friends trip", friend, VisibilityFriends, AccessByID, true, true, false},
		{"unknown visibility is private", friend, Visibility(0), AccessByLink, true, false, false},
	}
	for _, c := range cases {
		if got := CanView(c.viewer, owner, c.vis, c.access, c.friends, c.blocks); got != c.want {
			t.Errorf("%s: CanView = %v, want %v", c.name, got, c.want)
		}
	}
}

func sharedDraft() *tripv1.TripDraft {
	url := "https://book.example/1"
	sid := "session-1"
	return &tripv1.TripDraft{
		ShareCode:       "abc123",
		SourceSessionId: &sid,
		Days: []*tripv1.TripDay{{Stops: []*tripv1.TripStop{
			{Name: "Café Santiago", Notes: "Ask for Rui", BookingUrl: &url},
		}}},
		Legs: []*tripv1.TripLeg{{BookingUrl: &url}},
	}
}

func TestRedactForViewerHidesPrivateDetails(t *testing.T) {
	owner := &socialv1.PublicUser{Id: uuid.NewString(), Username: "ana"}
	p := redactForViewer(sharedDraft(), false, owner)
	if p.GetShareCode() != "" || p.SourceSessionId != nil {
		t.Error("share code or chat session leaked to a viewer")
	}
	stop := p.GetDays()[0].GetStops()[0]
	if stop.GetNotes() != "" || stop.BookingUrl != nil || p.GetLegs()[0].BookingUrl != nil {
		t.Error("notes or booking links shown although the owner did not share them")
	}
	if stop.GetName() != "Café Santiago" {
		t.Error("the stop itself was removed")
	}
	if p.GetOwner().GetUsername() != "ana" {
		t.Error("the owner card is missing")
	}

	shared := redactForViewer(sharedDraft(), true, owner)
	if shared.GetDays()[0].GetStops()[0].GetNotes() != "Ask for Rui" {
		t.Error("notes hidden although the owner chose to share them")
	}
	if shared.GetShareCode() != "" {
		t.Error("share code leaked with details on")
	}
}

func TestCopyOfIsANewPrivateTrip(t *testing.T) {
	srcOwner, newOwner := uuid.New(), uuid.New()
	cityID := uuid.New()
	sess := uuid.New()
	url := "https://book.example/1"
	src := &Trip{
		ID: uuid.New(), UserID: srcOwner, CityID: &cityID, CityName: "Porto", Title: "Porto weekend",
		Visibility: VisibilityPublic,
		Days: []TripDay{{ID: uuid.New(), DayNumber: 1, CityName: "Porto", Stops: []TripStop{
			{
				ID: uuid.New(), Name: "Ribeira", Notes: "sunset", BookingURL: &url,
				RecommendationTrace: &RecommendationTrace{RunID: "r1"},
			},
		}}},
		Legs:   []TripLeg{{ID: uuid.New(), FromName: "Porto", ToName: "Braga", BookingURL: &url}},
		Cities: []TripCity{{CityName: "Porto", SessionID: &sess, Nights: 2}},
	}

	c := copyOf(src, newOwner)
	switch {
	case c.ID != uuid.Nil:
		t.Error("copy kept the source id")
	case c.UserID != newOwner:
		t.Error("copy is not owned by the copier")
	case c.Visibility != VisibilityPrivate:
		t.Error("copy is not private")
	case c.CopiedFromTripID == nil || *c.CopiedFromTripID != src.ID:
		t.Error("copy does not record its source")
	}
	stop := c.Days[0].Stops[0]
	if c.Days[0].ID != uuid.Nil || stop.ID != uuid.Nil || c.Legs[0].ID != uuid.Nil {
		t.Error("copy kept source day, stop or leg ids")
	}
	if stop.Notes != "" || stop.BookingURL != nil || c.Legs[0].BookingURL != nil {
		t.Error("copy carried notes or booking links the owner did not share")
	}
	if stop.RecommendationTrace != nil || c.Cities[0].SessionID != nil {
		t.Error("copy carried the source owner's recommendation trace or chat session")
	}
	if stop.Name != "Ribeira" || c.Cities[0].Nights != 2 || c.Title != "Porto weekend" {
		t.Error("copy lost the plan itself")
	}
	// The copy must not alias the source's slices.
	c.Days[0].Stops[0].Name = "changed"
	if src.Days[0].Stops[0].Name != "Ribeira" {
		t.Error("editing the copy changed the source")
	}

	src.ShareDetails = true
	if copyOf(src, newOwner).Days[0].Stops[0].Notes != "sunset" {
		t.Error("shared notes were not copied")
	}
}

func TestShareURL(t *testing.T) {
	if got := ShareURL("abc"); got != "https://lociai.fyi/t/abc" {
		t.Errorf("ShareURL = %q", got)
	}
}
