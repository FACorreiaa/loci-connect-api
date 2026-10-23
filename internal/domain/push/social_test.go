package push

import (
	"testing"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
)

func TestBuildSocialPayload(t *testing.T) {
	ana := &socialv1.PublicUser{Username: "ana", DisplayName: "Ana Sousa"}

	followed := BuildSocialPayload(socialFollowed, ana)
	if followed.URL != WebOrigin+"/u/ana" || followed.Body != "Ana Sousa started following you on Loci." {
		t.Errorf("new follower payload = %+v", followed)
	}
	if followed.Category != SocialCategory || followed.ThreadID != "friends" {
		t.Errorf("grouping = %q / %q", followed.Category, followed.ThreadID)
	}

	back := BuildSocialPayload(socialFollowedBack, ana)
	if back.URL != WebOrigin+"/u/ana" || back.Title != "New friend on Loci" {
		t.Errorf("followed back payload = %+v", back)
	}

	req := BuildSocialPayload(socialFollowRequest, ana)
	if req.URL != WebOrigin+"/friends?tab=requests" || req.Body != "Ana Sousa asked to follow you on Loci." {
		t.Errorf("follow request payload = %+v", req)
	}

	// No card (a deleted account between the event and the push) still reads.
	if p := BuildSocialPayload(socialFollowApproved, nil); p.URL != WebOrigin+"/friends" || p.Body == "" {
		t.Errorf("payload without a card = %+v", p)
	}
}

func TestBuildCheerPayload(t *testing.T) {
	p := BuildCheerPayload(&socialv1.PublicUser{Username: "ana", DisplayName: "Ana Sousa"}, "Cartographer · Gold", "/achievements")
	if p.Body != "Ana Sousa cheered your Cartographer · Gold." || p.URL != WebOrigin+"/achievements" {
		t.Errorf("cheer payload = %+v", p)
	}
	if p.Origin != "cheered" || p.Category != SocialCategory {
		t.Errorf("cheer grouping = %+v", p)
	}
}

func TestBuildTripPayload(t *testing.T) {
	id := uuid.MustParse("6f1c2b8e-4a5d-4c3e-9b7a-1d2e3f4a5b6c")
	p := BuildTripPayload(id, "Lisbon trip", "Ana suggested: Add Miradouro to day 2.")
	if p.URL != WebOrigin+"/trips/"+id.String() || p.Category != TripCategory || p.ThreadID != "trip-"+id.String() {
		t.Errorf("trip payload = %+v", p)
	}
}
