package push

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
)

func TestBuildPayload(t *testing.T) {
	sid := uuid.MustParse("3043fb3f-15e8-461f-86de-6426fb389df2")
	p := BuildPayload(runs.Run{SessionID: sid, Domain: "itinerary", CityName: "Crete", Status: runs.StatusDone})
	require.Equal(t, "Your Crete itinerary is ready", p.Title)
	require.Equal(t, "Tap to open it.", p.Body)
	require.Equal(t, "done", p.Status)
	require.Equal(t, "/itinerary?sessionId="+sid.String()+"&cityName=Crete&domain=itinerary", p.URL)

	p = BuildPayload(runs.Run{SessionID: sid, Domain: "accommodation", Status: runs.StatusFailed})
	require.Equal(t, "Your hotels didn't finish", p.Title)
	require.Equal(t, "Tap to try again.", p.Body)
	require.Equal(t, "failed", p.Status)

	for domain, noun := range map[string]string{"general": "itinerary", "activities": "activities", "dining": "restaurants", "nearby": "nearby places"} {
		require.Equal(t, "Your Lisbon "+noun+" is ready", BuildPayload(runs.Run{SessionID: sid, Domain: domain, CityName: "Lisbon", Status: runs.StatusDone}).Title)
	}
}
