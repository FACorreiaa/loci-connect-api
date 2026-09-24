package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func testProactive() ProactiveMessage {
	return ProactiveMessage{
		UserID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SessionID:   uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		MessageID:   uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Title:       "Rain in Lisbon",
		Content:     "\n## Dry until Thursday; take a light jacket.\nSecond line stays in the thread.",
		SourceLabel: "Standing task",
		CityName:    "Lisbon",
	}
}

func newProactiveNotifier(devices *fakeDeviceStore, apns Sender) *Notifier {
	n := NewNotifier(&fakeRunStore{}, &fakeSettingsReader{}, devices, newFakeSender(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return n.WithSender(PlatformAPNS, apns)
}

// The exact APNs body the iOS app receives for a standing task's message.
func TestProactiveAPNSBodyShape(t *testing.T) {
	srv := newAPNSServer(t, http.StatusOK, "")
	sender := testSender(t, srv)
	body, err := json.Marshal(BuildProactivePayload(testProactive()))
	require.NoError(t, err)

	gone, err := sender.Send(context.Background(), apnsDevice(), body)
	require.NoError(t, err)
	require.False(t, gone)

	require.JSONEq(t, `{
		"aps": {
			"alert": {"title": "Rain in Lisbon", "body": "Dry until Thursday; take a light jacket."},
			"sound": "default",
			"thread-id": "session-22222222-2222-2222-2222-222222222222",
			"category": "loci_chat"
		},
		"sessionId": "22222222-2222-2222-2222-222222222222",
		"cityName": "Lisbon",
		"domain": "itinerary",
		"url": "https://lociai.fyi/itinerary?sessionId=22222222-2222-2222-2222-222222222222&cityName=Lisbon&domain=itinerary",
		"messageId": "33333333-3333-3333-3333-333333333333",
		"origin": "proactive",
		"sourceLabel": "Standing task"
	}`, string(srv.lastB))
}

// A finished search's body gains none of the proactive keys.
func TestSearchFinishedAPNSBodyUnchanged(t *testing.T) {
	encoded, err := EncodeAPNS(BuildPayload(testRun()))
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(encoded, &body))
	for _, k := range []string{"messageId", "origin", "sourceLabel"} {
		require.NotContains(t, body, k)
	}
	aps := body["aps"].(map[string]any)
	require.NotContains(t, aps, "category")
	require.Equal(t, "search", aps["thread-id"])
}

func TestFirstLine(t *testing.T) {
	require.Equal(t, "Hello", FirstLine("\n\n  - Hello  \nworld", 180))
	require.Equal(t, "", FirstLine("  \n ", 180))
	long := strings.Repeat("é", 300)
	got := FirstLine(long, 180)
	require.Equal(t, 180, utf8.RuneCountInString(got))
	require.True(t, strings.HasSuffix(got, "…"))
	require.True(t, utf8.ValidString(got))
}

func TestNotifyProactiveSendsToAPNSDevicesOnly(t *testing.T) {
	iphone := apnsDevice()
	devices := &fakeDeviceStore{byPlatform: map[string][]Device{
		PlatformAPNS:    {iphone},
		PlatformWebPush: {{ID: uuid.New(), Platform: PlatformWebPush, Endpoint: "https://fcm.googleapis.com/x"}},
	}}
	apns := newFakeSender()
	n := newProactiveNotifier(devices, apns)
	web := n.senders[PlatformWebPush].(*fakeSender)

	n.NotifyProactive(context.Background(), testProactive())
	n.wait()

	require.Equal(t, []Device{iphone}, apns.sentTo())
	require.Empty(t, web.sentTo())
	var p Payload
	require.NoError(t, json.Unmarshal(apns.bodies[iphone.Endpoint], &p))
	require.Equal(t, "proactive", p.Origin)
	require.Equal(t, "loci_chat", p.Category)
	require.Empty(t, devices.removedEndpoints())
}

func TestNotifyProactivePrunesGoneTokens(t *testing.T) {
	gone, alive := apnsDevice(), apnsDevice()
	alive.Endpoint = strings.Repeat("ab", 32)
	devices := &fakeDeviceStore{byPlatform: map[string][]Device{PlatformAPNS: {gone, alive}}}
	apns := newFakeSender()
	apns.results[gone.Endpoint] = sendResult{gone: true}
	apns.results[alive.Endpoint] = sendResult{err: errors.New("apns: status 500")}
	n := newProactiveNotifier(devices, apns)

	n.NotifyProactive(context.Background(), testProactive())
	n.wait()

	require.Len(t, apns.sentTo(), 2)
	// Only the token APNs gave up on goes; a transient error keeps the row.
	require.Equal(t, []string{gone.Endpoint}, devices.removedEndpoints())
}

// BadDeviceToken / Unregistered from Apple reach the notifier as gone.
func TestNotifyProactivePrunesOnAPNSReasons(t *testing.T) {
	for _, reason := range []string{"BadDeviceToken", "Unregistered"} {
		t.Run(reason, func(t *testing.T) {
			status := http.StatusBadRequest
			if reason == "Unregistered" {
				status = http.StatusGone
			}
			srv := newAPNSServer(t, status, `{"reason":"`+reason+`"}`)
			d := apnsDevice()
			devices := &fakeDeviceStore{byPlatform: map[string][]Device{PlatformAPNS: {d}}}
			n := newProactiveNotifier(devices, testSender(t, srv))

			n.NotifyProactive(context.Background(), testProactive())
			n.wait()

			require.Equal(t, []string{d.Endpoint}, devices.removedEndpoints())
		})
	}
}

func TestNotifyProactiveDisabledWithoutAPNS(t *testing.T) {
	devices := &fakeDeviceStore{byPlatform: map[string][]Device{PlatformAPNS: {apnsDevice()}}}
	web := newFakeSender()
	n := NewNotifier(&fakeRunStore{}, &fakeSettingsReader{}, devices, web, nil)

	n.NotifyProactive(context.Background(), testProactive())
	n.wait()

	require.Empty(t, web.sentTo())
}

func TestNotifyProactiveIgnoresMessageWithoutThread(t *testing.T) {
	devices := &fakeDeviceStore{byPlatform: map[string][]Device{PlatformAPNS: {apnsDevice()}}}
	apns := newFakeSender()
	n := newProactiveNotifier(devices, apns)
	m := testProactive()
	m.SessionID = uuid.Nil

	n.NotifyProactive(context.Background(), m)
	n.wait()

	require.Empty(t, apns.sentTo())
}
