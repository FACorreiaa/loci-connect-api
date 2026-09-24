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

// A follow-up turn reuses the session id. Its follower must get a live stream
// of the new turn, not a closed one left over from the finished turn.
func TestFollowUpTurnReopensTheSession(t *testing.T) {
	b := New()
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeStart, EventID: "t1-start"})
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeComplete, EventID: "t1-done"})

	b.Append("s1", locitypes.StreamEvent{Type: "session_validated", EventID: "t2-a"})
	backlog, live, cancel, ok := b.Subscribe("s1", "t2-a")
	defer cancel()
	require.True(t, ok)
	require.Empty(t, backlog)

	full, _ := b.Replay("s1", "")
	require.Len(t, full, 1, "the ring holds only the current turn")
	require.Equal(t, "t2-a", full[0].EventID)

	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeProgress, EventID: "t2-b"})
	select {
	case ev, open := <-live:
		require.True(t, open, "live must stay open for the new turn")
		require.Equal(t, "t2-b", ev.EventID)
	case <-time.After(time.Second):
		t.Fatal("new turn's event was not delivered live")
	}
}

// A trailing terminal event after a turn's complete must not wipe that turn.
func TestTerminalAfterDoneKeepsTheTurn(t *testing.T) {
	b := New()
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeStart, EventID: "e1"})
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeComplete, EventID: "e2"})
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeError, EventID: "e3"})
	full, _ := b.Replay("s1", "")
	require.Len(t, full, 3)
}

// A follower that never reads past its 64-slot buffer is closed rather than
// silently skipped. Re-subscribing from the last event it got continues with no
// gap and ends with the terminal event.
func TestSlowFollowerIsClosedAndResumesWithoutGap(t *testing.T) {
	b := New()
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeStart, EventID: "e0"})
	_, live, cancel, ok := b.Subscribe("s1", "e0")
	require.True(t, ok)
	defer cancel()

	const n = 100
	want := make([]string, 0, n+1)
	for i := range n {
		id := "t" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		want = append(want, id)
		b.Append("s1", locitypes.StreamEvent{Type: "token", EventID: id})
	}

	// The run is still going: the overflowed follower must already be closed,
	// not left open with a silent hole in its stream.
	var got []string
	func() {
		for {
			select {
			case ev, open := <-live:
				if !open {
					return
				}
				got = append(got, ev.EventID)
			case <-time.After(time.Second):
				t.Fatal("an overflowed follower must be closed while the run is still going")
			}
		}
	}()
	require.Len(t, got, 64, "the follower gets exactly what fit, then a close")

	want = append(want, "done")
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeComplete, EventID: "done"})

	backlog, live2, cancel2, ok := b.Subscribe("s1", got[len(got)-1])
	require.True(t, ok)
	defer cancel2()
	for _, ev := range backlog {
		got = append(got, ev.EventID)
	}
	_, open := <-live2
	require.False(t, open, "the run has ended, so the re-subscription is already closed")
	require.Equal(t, want, got, "no gap, no duplicate, terminal last")
}

// A multi-city run's city error is not the end of the run: a reconnect must
// keep following it.
func TestSubscribeStopErrorKeepsRunLive(t *testing.T) {
	b := New()
	one := 1
	b.Append("s1", locitypes.StreamEvent{Type: locitypes.EventTypeError, EventID: "e1", StopIndex: &one})
	_, live, cancel, ok := b.Subscribe("s1", "")
	defer cancel()
	require.True(t, ok)
	select {
	case _, open := <-live:
		require.True(t, open, "live channel must stay open after a stop-tagged error")
	case <-time.After(100 * time.Millisecond):
	}
}
