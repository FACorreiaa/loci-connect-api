package push

import (
	"testing"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
)

func TestBuildSocialPayload(t *testing.T) {
	ana := &socialv1.PublicUser{Username: "ana", DisplayName: "Ana Sousa"}

	req := BuildSocialPayload(socialRequest, ana)
	if req.URL != WebOrigin+"/friends" || req.Body != "Ana Sousa wants to be friends on Loci." {
		t.Errorf("request payload = %+v", req)
	}
	if req.Category != SocialCategory || req.ThreadID != "friends" {
		t.Errorf("request grouping = %q / %q", req.Category, req.ThreadID)
	}

	acc := BuildSocialPayload(socialAccepted, ana)
	if acc.URL != WebOrigin+"/u/ana" || acc.Title != "New friend on Loci" {
		t.Errorf("accepted payload = %+v", acc)
	}

	// No card (a deleted account between the event and the push) still reads.
	if p := BuildSocialPayload(socialAccepted, nil); p.URL != WebOrigin+"/friends" || p.Body == "" {
		t.Errorf("payload without a card = %+v", p)
	}
}
