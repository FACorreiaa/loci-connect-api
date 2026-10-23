package push

import (
	"strings"
	"testing"
	"unicode/utf8"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
)

func TestBuildTripCommentPayload(t *testing.T) {
	trip := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	p := BuildTripCommentPayload(trip, "Lisbon long weekend", &socialv1.PublicUser{DisplayName: "Rui"}, "  Shall we skip the castle?  ")
	if p.Title != "Rui on Lisbon long weekend" || p.Body != "Shall we skip the castle?" {
		t.Fatalf("got %q / %q", p.Title, p.Body)
	}
	if p.Category != TripCategory || p.ThreadID != "trip-"+trip.String() || p.Origin != "trip_comment" {
		t.Errorf("payload = %+v", p)
	}
	if p.URL != WebOrigin+"/trips/"+trip.String() {
		t.Errorf("url = %q", p.URL)
	}

	long := BuildTripCommentPayload(trip, "T", nil, strings.Repeat("ã", 300))
	if !strings.HasPrefix(long.Title, "Someone on") {
		t.Errorf("no card: %q", long.Title)
	}
	if n := utf8.RuneCountInString(long.Body); n != maxCommentPreview || !strings.HasSuffix(long.Body, "…") {
		t.Errorf("clipped body is %d runes: %q", n, long.Body)
	}
}
