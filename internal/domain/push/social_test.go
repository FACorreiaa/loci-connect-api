package push

import (
	"testing"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
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
