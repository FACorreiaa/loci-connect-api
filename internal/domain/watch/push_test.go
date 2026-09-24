package watch

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/push"
)

type fakePusher struct{ got []push.ProactiveMessage }

func (f *fakePusher) NotifyProactive(_ context.Context, m push.ProactiveMessage) {
	f.got = append(f.got, m)
}

func TestPushNotifierHandsTheMessageToPush(t *testing.T) {
	p := &fakePusher{}
	w := Watch{ID: uuid.New(), UserID: uuid.New(), SessionID: uuid.New(), Title: "Rain in Lisbon"}
	msg := proactiveMessage("Dry until Thursday.\nMore detail.", fixedNow)

	NewPushNotifier(p).WatchPosted(context.Background(), w, "Lisbon", msg)

	require.Equal(t, []push.ProactiveMessage{{
		UserID:      w.UserID,
		SessionID:   w.SessionID,
		MessageID:   msg.ID,
		Title:       "Rain in Lisbon",
		Content:     "Dry until Thursday.\nMore detail.",
		SourceLabel: SourceLabel,
		CityName:    "Lisbon",
	}}, p.got)
}

func TestNewPushNotifierWithoutPusherLeavesTheNoop(t *testing.T) {
	require.Nil(t, NewPushNotifier(nil))
	s, _, _ := newTestService(nil)
	s.WithNotifier(NewPushNotifier(nil))
	require.IsType(t, noopNotifier{}, s.notifier)
}
