package runs

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type recordingStore struct {
	Store
	mu       sync.Mutex
	attached []string
	finished []Status
	codes    []string
}

func (r *recordingStore) Attach(_ context.Context, _, sid uuid.UUID, domain, city string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attached = append(r.attached, sid.String()+"|"+domain+"|"+city)
	return nil
}

func (r *recordingStore) Finish(_ context.Context, id uuid.UUID, s Status, code string) (Run, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, s)
	r.codes = append(r.codes, code)
	return Run{ID: id, Status: s}, true, nil
}

func start(sid string) locitypes.StreamEvent {
	return locitypes.StreamEvent{Type: locitypes.EventTypeStart, Data: locitypes.StreamStartData{SessionID: sid, Domain: "itinerary", City: "Crete"}}
}

func TestTrackerAttachesThenFinishesDone(t *testing.T) {
	store := &recordingStore{}
	var got []Run
	tr := NewTracker(store, uuid.New(), func(_ context.Context, r Run) { got = append(got, r) }, nil)
	sid := uuid.NewString()

	tr.Observe(start(sid))
	tr.Observe(locitypes.StreamEvent{Type: "token"})
	tr.Observe(locitypes.StreamEvent{Type: locitypes.EventTypeComplete})
	tr.Close()

	require.Equal(t, []string{sid + "|itinerary|Crete"}, store.attached)
	require.Equal(t, []Status{StatusDone}, store.finished, "Close after complete must not write again")
	require.Len(t, got, 1)
}

func TestTrackerErrorFinishesFailedWithCode(t *testing.T) {
	store := &recordingStore{}
	tr := NewTracker(store, uuid.New(), nil, nil)
	tr.Observe(start(uuid.NewString()))
	tr.Observe(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "boom", ErrorCode: "unavailable"})
	require.Equal(t, []Status{StatusFailed}, store.finished)
	require.Equal(t, []string{"unavailable"}, store.codes)
}

func TestTrackerStreamEndingSilentlyIsAFailure(t *testing.T) {
	store := &recordingStore{}
	tr := NewTracker(store, uuid.New(), nil, nil)
	tr.Observe(start(uuid.NewString()))
	tr.Close()
	require.Equal(t, []Status{StatusFailed}, store.finished)
	require.Equal(t, []string{"incomplete"}, store.codes)
}

func TestNilTrackerIsANoop(t *testing.T) {
	var tr *Tracker
	tr.Observe(start(uuid.NewString()))
	tr.Close()
}

// A run that fails before its start event has no session to link to, so it
// is recorded as failed but never announced.
func TestTrackerFailureBeforeStartRecordsButDoesNotAnnounce(t *testing.T) {
	store := &recordingStore{}
	var got []Run
	tr := NewTracker(store, uuid.New(), func(_ context.Context, r Run) { got = append(got, r) }, nil)

	tr.Observe(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "boom"})
	tr.Close()

	require.Empty(t, store.attached)
	require.Equal(t, []Status{StatusFailed}, store.finished)
	require.Empty(t, got, "no session, no notification")
}

// A multi-city stream reports one city's failure as a tagged ERROR and goes
// on to the next city; the run must not be marked failed for it.
func TestTrackerStopErrorIsNotTerminal(t *testing.T) {
	store := &recordingStore{}
	tr := NewTracker(store, uuid.New(), nil, nil)
	tr.Observe(start(uuid.NewString()))
	one := 1
	tr.Observe(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "porto failed", StopIndex: &one})
	require.Empty(t, store.finished, "a stop-tagged error must not finish the run")
	tr.Observe(locitypes.StreamEvent{Type: locitypes.EventTypeComplete})
	require.Equal(t, []Status{StatusDone}, store.finished)
}
