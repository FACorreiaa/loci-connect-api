package analytics

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
)

// recordingSink stands in for the PostHog client.
type recordingSink struct {
	mu     sync.Mutex
	events []Event
	err    error
	closed bool
}

func (r *recordingSink) Enqueue(e Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, e)
	return nil
}

func (r *recordingSink) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func (r *recordingSink) captured() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Without a sink the recorder is inert. This is the normal state in
// development and in any deployment with no PostHog key.
func TestRecorderWithoutASinkIsInert(t *testing.T) {
	r := New(nil, testLogger())
	r.Capture("user-1", "trip_reopened", map[string]any{"trip_id": "t1"})
	if err := r.Close(); err != nil {
		t.Fatalf("Close on an inert recorder: %v", err)
	}
}

// A nil *Recorder must behave like an inert one, so callers holding an
// optional dependency never need a nil check at the call site.
func TestNilRecorderIsSafe(t *testing.T) {
	var r *Recorder
	r.Capture("user-1", "trip_reopened", nil)
	if err := r.Close(); err != nil {
		t.Fatalf("Close on a nil recorder: %v", err)
	}
}

func TestCaptureForwardsUserEventAndProperties(t *testing.T) {
	sink := &recordingSink{}
	r := New(sink, testLogger())

	r.Capture("user-42", "trip_reopened", map[string]any{"trip_id": "t-9", "hours": 48})

	got := sink.captured()
	if len(got) != 1 {
		t.Fatalf("captured %d events, want 1", len(got))
	}
	if got[0].DistinctID != "user-42" || got[0].Name != "trip_reopened" {
		t.Fatalf("captured %+v, want user-42/trip_reopened", got[0])
	}
	if got[0].Properties["trip_id"] != "t-9" {
		t.Fatalf("properties = %+v, want trip_id t-9", got[0].Properties)
	}
}

// An event with no user cannot be attributed to anyone and would create a
// phantom person in PostHog, so it is dropped rather than sent anonymously.
func TestCaptureDropsEventsWithNoUser(t *testing.T) {
	sink := &recordingSink{}
	r := New(sink, testLogger())

	r.Capture("", "trip_reopened", nil)

	if got := sink.captured(); len(got) != 0 {
		t.Fatalf("captured %d events for an empty user, want 0", len(got))
	}
}

// Analytics must never be the reason a request fails.
func TestCaptureSwallowsSinkErrors(t *testing.T) {
	sink := &recordingSink{err: errors.New("queue full")}
	r := New(sink, testLogger())
	r.Capture("user-1", "trip_reopened", nil)
}

func TestCloseFlushesTheSink(t *testing.T) {
	sink := &recordingSink{}
	r := New(sink, testLogger())
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sink.closed {
		t.Fatal("Close must flush and close the underlying sink")
	}
}
