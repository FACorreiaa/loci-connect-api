package share

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	sharev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/share"
	"github.com/google/uuid"
)

// A share link must land on a page: the web app's origin, not the API's.
func TestShareURLIsOnTheOriginTheHandlerIsGiven(t *testing.T) {
	owner := uuid.New()
	h := NewHandler("https://lociai.fyi", &ownedRepo{owner: owner})
	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, owner.String())
	res, err := h.CreateShareLink(ctx, connect.NewRequest(&sharev1.CreateShareLinkRequest{
		UserId: "x", ContentType: sharev1.ShareContentType_SHARE_CONTENT_TYPE_POI, ContentId: uuid.NewString(), Title: "t",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://lociai.fyi/share/" + res.Msg.ShareCode; res.Msg.ShareUrl != want {
		t.Fatalf("share_url = %q, want %q", res.Msg.ShareUrl, want)
	}
}

// The OG page's redirect must point at a page the web app actually serves.
func TestWebPathPerContentType(t *testing.T) {
	cases := map[sharev1.ShareContentType]string{
		sharev1.ShareContentType_SHARE_CONTENT_TYPE_POI:        "/places/abc",
		sharev1.ShareContentType_SHARE_CONTENT_TYPE_HOTEL:      "/places/abc",
		sharev1.ShareContentType_SHARE_CONTENT_TYPE_RESTAURANT: "/places/abc",
		sharev1.ShareContentType_SHARE_CONTENT_TYPE_ACTIVITY:   "/places/abc",
		sharev1.ShareContentType_SHARE_CONTENT_TYPE_LIST:       "/lists/abc",
		sharev1.ShareContentType_SHARE_CONTENT_TYPE_ITINERARY:  "/itinerary/saved/abc",
		sharev1.ShareContentType_SHARE_CONTENT_TYPE_TRIP:       "/trips/abc",
	}
	for ct, want := range cases {
		if got := webPath(ct, "abc"); got != want {
			t.Errorf("%v: %q, want %q", ct, got, want)
		}
	}
	if got := webPath(sharev1.ShareContentType_SHARE_CONTENT_TYPE_UNSPECIFIED, "abc"); got != "" {
		t.Errorf("unknown type has no page (the OG page then stays on /share/<code>), got %q", got)
	}
}
