package resumebuf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestSubscribeReplaysThenFollowsUntilComplete(t *testing.T) {
	b := New()
	b.Append("s1", locitypes.StreamEvent{Type: "start", EventID: "e1"})
	b.Append("s1", locitypes.StreamEvent{Type: "token", EventID: "e2"})

	backlog, live, cancel, ok := b.Subscribe("s1", "e1")
	defer cancel()
	require.True(t, ok)
	require.Len(t, backlog, 1)
	require.Equal(t, "e2", backlog[0].EventID)

	b.Append("s1", locitypes.StreamEvent{Type: "itinerary", EventID: "e3"})
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeComplete, EventID: "e4"})

	var got []string
	for ev := range live {
		got = append(got, ev.EventID)
	}
	require.Equal(t, []string{"e3", "e4"}, got)
}

func TestSubscribeToFinishedRunClosesImmediately(t *testing.T) {
	b := New()
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeComplete, EventID: "e1"})
	backlog, live, cancel, ok := b.Subscribe("s1", "")
	defer cancel()
	require.True(t, ok)
	require.Len(t, backlog, 1)
	select {
	case _, open := <-live:
		require.False(t, open)
	case <-time.After(time.Second):
		t.Fatal("live channel of a finished run must be closed")
	}
}

func TestSubscribeUnknownSession(t *testing.T) {
	_, _, _, ok := New().Subscribe("nope", "")
	require.False(t, ok)
}

// cancel after the terminal event already closed the channel must not panic
// with a double close; neither must a second cancel.
func TestSubscribeCancelAfterTerminalDoesNotDoubleClose(t *testing.T) {
	b := New()
	b.Append("s1", locitypes.StreamEvent{Type: "start", EventID: "e1"})
	_, live, cancel, ok := b.Subscribe("s1", "e1")
	require.True(t, ok)
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeError, EventID: "e2"})
	drained := 0
	for range live {
		drained++
	}
	require.Equal(t, 1, drained, "the terminal event is delivered before the close")
	require.NotPanics(t, cancel)
	require.NotPanics(t, cancel)
}

// A reaped session closes its subscribers, and their cancel is then a no-op.
func TestReaperClosesSubscribers(t *testing.T) {
	b := New()
	clock := time.Now()
	b.now = func() time.Time { return clock }
	b.Append("s1", locitypes.StreamEvent{Type: "start", EventID: "e1"})
	_, live, cancel, ok := b.Subscribe("s1", "e1")
	require.True(t, ok)

	clock = clock.Add(b.ttl + 2*time.Minute)
	b.Append("other", locitypes.StreamEvent{Type: "start", EventID: "x"}) // triggers reap

	select {
	case _, open := <-live:
		require.False(t, open)
	case <-time.After(time.Second):
		t.Fatal("reaped session must close its subscribers")
	}
	require.NotPanics(t, cancel)
}

// Drop closes subscribers too, so a follower never hangs on a dropped session.
func TestDropClosesSubscribers(t *testing.T) {
	b := New()
	b.Append("s1", locitypes.StreamEvent{Type: "start", EventID: "e1"})
	_, live, cancel, ok := b.Subscribe("s1", "e1")
	require.True(t, ok)
	b.Drop("s1")
	_, open := <-live
	require.False(t, open)
	require.NotPanics(t, cancel)
}
