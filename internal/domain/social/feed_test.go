package social

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFeedCursorRoundTrips(t *testing.T) {
	c := feedCursor{At: time.Date(2026, 10, 22, 12, 30, 1, 123456000, time.UTC), ID: uuid.New()}
	got, err := decodeFeedCursor(encodeFeedCursor(c))
	if err != nil || !got.At.Equal(c.At) || got.ID != c.ID {
		t.Fatalf("round trip = %+v, %v; want %+v", got, err, c)
	}
	for _, bad := range []string{"%%", "bm8tY29sb24", "MTIzOm5vdC1hLXV1aWQ"} {
		if _, err := decodeFeedCursor(bad); err == nil {
			t.Errorf("decoded %q", bad)
		}
	}
}

// An item the server cannot render is left out rather than sent half-empty.
func TestFeedItemProtoDropsWhatItCannotRender(t *testing.T) {
	base := FeedRow{ID: uuid.New(), CreatedAt: time.Now()}
	cases := map[string]FeedRow{
		"unknown kind":          {Kind: 9},
		"achievement, no id":    {Kind: FeedAchievement, Payload: []byte(`{"title":"x"}`)},
		"achievement, bad json": {Kind: FeedAchievement, Payload: []byte(`{`)},
		"trip without its trip": {Kind: FeedTripShared},
	}
	for name, r := range cases {
		r.ID, r.CreatedAt = base.ID, base.CreatedAt
		if p := feedItemProto(FeedItem{FeedRow: r}); p != nil {
			t.Errorf("%s rendered as %v", name, p)
		}
	}
	trip := uuid.New()
	r := base
	r.Kind, r.TripID, r.Trip = FeedTripFinished, &trip, FeedTripInfo{Title: "Douro", Days: 3}
	if p := feedItemProto(FeedItem{FeedRow: r}); p.GetTrip().GetTripId() != trip.String() || p.GetTrip().GetDays() != 3 {
		t.Errorf("trip item = %v", p)
	}
}

func TestCheeredWhatNamesTheItem(t *testing.T) {
	cases := []struct {
		row  FeedRow
		what string
		path string
	}{
		{FeedRow{Kind: FeedAchievement, Payload: []byte(`{"id":"cartographer","title":"Cartographer","tier":3}`)}, "Cartographer · Gold", "/achievements"},
		{FeedRow{Kind: FeedAchievement, Payload: []byte(`{"id":"first-pin","title":"First Pin"}`)}, "First Pin", "/achievements"},
		{FeedRow{Kind: FeedNewCity, CityName: "Porto"}, "new city: Porto", "/profile"},
		{FeedRow{Kind: FeedTripShared, Trip: FeedTripInfo{Title: "Douro"}}, "trip Douro", "/profile"},
	}
	for _, c := range cases {
		what, path := cheeredWhat(c.row)
		if what != c.what || path != c.path {
			t.Errorf("cheeredWhat(%v) = %q %q; want %q %q", c.row.Kind, what, path, c.what, c.path)
		}
	}
}
