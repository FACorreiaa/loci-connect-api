package push

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestBuildBriefPayload(t *testing.T) {
	trip := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	p := BuildBriefPayload(trip, "Day 2 in Lisbon", "18°C, clear", WebOrigin+"/trips/"+trip.String())
	require.Equal(t, BriefCategory, p.Category)
	require.Equal(t, "brief-"+trip.String(), p.ThreadID)
	require.Equal(t, "trip_brief", p.Origin)
	require.Equal(t, WebOrigin+"/trips/"+trip.String(), p.URL)
}

func briefNotifier(settings *locitypes.NotificationSettings) (*Notifier, *fakeSender, *fakeSender) {
	web, apns := newFakeSender(), newFakeSender()
	devices := &fakeDeviceStore{byPlatform: map[string][]Device{
		PlatformAPNS:    {apnsDevice()},
		PlatformWebPush: {{ID: uuid.New(), Platform: PlatformWebPush, Endpoint: "https://fcm.googleapis.com/x"}},
	}}
	n := NewNotifier(&fakeRunStore{}, &fakeSettingsReader{settings: settings}, devices, web, nil).WithSender(PlatformAPNS, apns)
	return n, web, apns
}

func TestTripBrief_SendsToEveryPlatform(t *testing.T) {
	n, web, apns := briefNotifier(&locitypes.NotificationSettings{TripReminders: true})
	trip := uuid.New()
	n.TripBrief(context.Background(), uuid.New(), trip, "Day 2 in Lisbon", "18°C, clear", WebOrigin+"/trips/"+trip.String())
	n.wait()
	require.Len(t, web.sentTo(), 1)
	require.Len(t, apns.sentTo(), 1)
	var p Payload
	require.NoError(t, json.Unmarshal(apns.bodies[apns.sentTo()[0].Endpoint], &p))
	require.Equal(t, "Day 2 in Lisbon", p.Title)
	require.Equal(t, BriefCategory, p.Category)
}

func TestTripBrief_OffWithoutTripReminders(t *testing.T) {
	n, web, apns := briefNotifier(&locitypes.NotificationSettings{TripReminders: false})
	n.TripBrief(context.Background(), uuid.New(), uuid.New(), "t", "b", WebOrigin)
	n.wait()
	require.Empty(t, web.sentTo())
	require.Empty(t, apns.sentTo())
}
