package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat/chatconnect"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/resumebuf"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// noGenerateService fails the test if a resume reaches the LLM pipeline.
type noGenerateService struct {
	service.LlmInteractiontService
	t *testing.T
}

func (s noGenerateService) ProcessUnifiedChatMessageStream(common.ChatContext) error {
	s.t.Errorf("a resume must not start a new generation")
	return nil
}

// sessionRunStore answers FindBySession with a fixed run.
type sessionRunStore struct {
	runs.Store
	run   runs.Run
	found bool
}

func (f *sessionRunStore) FindBySession(_ context.Context, _, sessionID uuid.UUID) (runs.Run, bool, error) {
	if !f.found || sessionID != f.run.SessionID {
		return runs.Run{}, false, nil
	}
	return f.run, true, nil
}

// resumeClient serves h over httptest as an authenticated user.
func resumeClient(t *testing.T, h *ChatHandler) chatconnect.ChatServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := chatconnect.NewChatServiceHandler(h,
		connect.WithInterceptors(claimsInjector{userID: uuid.New().String()}))
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return chatconnect.NewChatServiceClient(srv.Client(), srv.URL)
}

func resumeRequest(sid uuid.UUID, token string) *connect.Request[chatv1.ChatRequest] {
	return connect.NewRequest(&chatv1.ChatRequest{
		Message:     "resume",
		CityName:    proto.String("Crete"),
		SessionId:   proto.String(sid.String()),
		ResumeToken: proto.String(token),
	})
}

func TestResumeFollowsLiveRunToComplete(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewChatHandler(noGenerateService{t: t}, logger, nil)
	h.resumeBuf = resumebuf.New()
	sid := uuid.New()
	h.resumeBuf.Append(sid.String(), locitypes.StreamEvent{Type: locitypes.EventTypeStart, EventID: "e1"})
	h.resumeBuf.Append(sid.String(), locitypes.StreamEvent{Type: "token", EventID: "e2"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := resumeClient(t, h).StreamChat(ctx, resumeRequest(sid, "e1"))
	require.NoError(t, err)

	var ids []string
	var types []chatv1.StreamEventType
	for stream.Receive() {
		msg := stream.Msg()
		ids = append(ids, msg.GetEventId())
		types = append(types, msg.GetEventType())
		if msg.GetEventId() == "e2" {
			// The backlog has been delivered, so the handler is subscribed:
			// the rest of the run arrives live.
			go func() {
				time.Sleep(50 * time.Millisecond)
				h.resumeBuf.Append(sid.String(), locitypes.StreamEvent{
					Type: locitypes.EventTypeComplete, EventID: "e3", IsFinal: true,
				})
			}()
		}
	}
	require.NoError(t, stream.Err())
	require.Equal(t, []string{"e2", "e3"}, ids)
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_COMPLETE, types[1])
}

func TestResumeBufferGoneRunDoneLoadsFromSession(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sid := uuid.New()
	store := &sessionRunStore{found: true, run: runs.Run{
		Status: runs.StatusDone, SessionID: sid, Domain: "itinerary", CityName: "Crete",
	}}
	h := NewChatHandler(noGenerateService{t: t}, logger, nil).WithRuns(store, nil)
	h.resumeBuf = resumebuf.New()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := resumeClient(t, h).StreamChat(ctx, resumeRequest(sid, "e7"))
	require.NoError(t, err)

	var got []*chatv1.StreamEvent
	for stream.Receive() {
		got = append(got, proto.Clone(stream.Msg()).(*chatv1.StreamEvent))
	}
	require.NoError(t, stream.Err())
	require.Len(t, got, 1)
	ev := got[0]
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_COMPLETE, ev.GetEventType())
	require.True(t, ev.GetIsFinal())
	require.True(t, ev.GetComplete().GetLoadFromSession())
	require.Equal(t, sid.String(), ev.GetComplete().GetSessionId())
	require.Equal(t, "itinerary", ev.GetNavigation().GetRouteType())
	require.Equal(t, "/itinerary?sessionId="+sid.String()+"&cityName=Crete&domain=itinerary", ev.GetNavigation().GetUrl())
	require.Equal(t, sid.String(), ev.GetNavigation().GetQueryParams()["sessionId"])
}

func TestResumeBufferGoneRunFailedSaysTryAgain(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sid := uuid.New()
	store := &sessionRunStore{found: true, run: runs.Run{
		Status: runs.StatusFailed, SessionID: sid, Domain: "itinerary", CityName: "Crete",
	}}
	h := NewChatHandler(noGenerateService{t: t}, logger, nil).WithRuns(store, nil)
	h.resumeBuf = resumebuf.New()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := resumeClient(t, h).StreamChat(ctx, resumeRequest(sid, "e7"))
	require.NoError(t, err)

	var got []*chatv1.StreamEvent
	for stream.Receive() {
		got = append(got, proto.Clone(stream.Msg()).(*chatv1.StreamEvent))
	}
	require.NoError(t, stream.Err())
	require.Len(t, got, 1)
	ev := got[0]
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_ERROR, ev.GetEventType())
	require.True(t, ev.GetIsFinal())
	require.True(t, ev.GetError().GetRetryable())
	require.Equal(t, "This search didn't finish. Try it again.", ev.GetError().GetUserMessage())
	require.Equal(t, "internal", ev.GetError().GetInternalCode(), "empty ErrorCode falls back to a non-empty code")
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A resumed client that falls behind (large events, a reader that does not keep
// up) overflows its live buffer. It must still get every event, in order, and
// end on the terminal event; the handler re-subscribes rather than skipping.
func TestResumeSlowFollowerSeesNoGapAndEndsOnTerminal(t *testing.T) {
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	h := NewChatHandler(noGenerateService{t: t}, logger, nil)
	h.resumeBuf = resumebuf.New()
	sid := uuid.New()
	h.resumeBuf.Append(sid.String(), locitypes.StreamEvent{Type: locitypes.EventTypeStart, EventID: "e1"})
	h.resumeBuf.Append(sid.String(), locitypes.StreamEvent{Type: "token", EventID: "e2"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := resumeClient(t, h).StreamChat(ctx, resumeRequest(sid, "e1"))
	require.NoError(t, err)

	require.True(t, stream.Receive())
	require.Equal(t, "e2", stream.Msg().GetEventId())

	// The handler is now following live. Append far more than its 64-slot
	// channel holds, each large enough that the socket pushes back, before
	// the client reads any of them.
	const n = 150
	want := []string{"e2"}
	for i := range n {
		raw := make([]byte, 32<<10)
		_, _ = rand.Read(raw)
		id := fmt.Sprintf("tok-%03d", i)
		want = append(want, id)
		h.resumeBuf.Append(sid.String(), locitypes.StreamEvent{Type: "token", EventID: id, Message: hex.EncodeToString(raw)})
	}
	want = append(want, "done")
	h.resumeBuf.Append(sid.String(), locitypes.StreamEvent{Type: locitypes.EventTypeComplete, EventID: "done", IsFinal: true})

	got := []string{"e2"}
	var last chatv1.StreamEventType
	for stream.Receive() {
		got = append(got, stream.Msg().GetEventId())
		last = stream.Msg().GetEventType()
	}
	require.NoError(t, stream.Err())
	require.Equal(t, want, got, "every event, once, in order")
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_COMPLETE, last)
	require.Contains(t, logs.String(), "resubscribing", "the follower overflowed and the handler re-subscribed")
}

// followUntilDropped resumes, waits for the backlog, then drops the session from
// the buffer mid-follow (as eviction would) and returns what the client got next.
func followUntilDropped(t *testing.T, h *ChatHandler, sid uuid.UUID) []*chatv1.StreamEvent {
	t.Helper()
	h.resumeBuf.Append(sid.String(), locitypes.StreamEvent{Type: locitypes.EventTypeStart, EventID: "e1"})
	h.resumeBuf.Append(sid.String(), locitypes.StreamEvent{Type: "token", EventID: "e2"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := resumeClient(t, h).StreamChat(ctx, resumeRequest(sid, "e1"))
	require.NoError(t, err)
	require.True(t, stream.Receive())
	require.Equal(t, "e2", stream.Msg().GetEventId())

	h.resumeBuf.Drop(sid.String())

	var got []*chatv1.StreamEvent
	for stream.Receive() {
		got = append(got, proto.Clone(stream.Msg()).(*chatv1.StreamEvent))
	}
	require.NoError(t, stream.Err())
	return got
}

func TestResumeFollowerDroppedRunDoneLoadsFromSession(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sid := uuid.New()
	store := &sessionRunStore{found: true, run: runs.Run{
		Status: runs.StatusDone, SessionID: sid, Domain: "itinerary", CityName: "Crete",
	}}
	h := NewChatHandler(noGenerateService{t: t}, logger, nil).WithRuns(store, nil)
	h.resumeBuf = resumebuf.New()

	got := followUntilDropped(t, h, sid)
	require.Len(t, got, 1)
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_COMPLETE, got[0].GetEventType())
	require.True(t, got[0].GetComplete().GetLoadFromSession())
}

func TestResumeFollowerDroppedRunStillRunningSaysRetry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sid := uuid.New()
	store := &sessionRunStore{found: true, run: runs.Run{
		Status: runs.StatusRunning, SessionID: sid, Domain: "itinerary", CityName: "Crete",
	}}
	h := NewChatHandler(noGenerateService{t: t}, logger, nil).WithRuns(store, nil)
	h.resumeBuf = resumebuf.New()

	got := followUntilDropped(t, h, sid)
	require.Len(t, got, 1)
	ev := got[0]
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_ERROR, ev.GetEventType())
	require.True(t, ev.GetIsFinal())
	require.True(t, ev.GetError().GetRetryable())
	require.Equal(t, "resume_lost", ev.GetError().GetInternalCode())
	require.Equal(t, "This search is still running. Try again in a moment.", ev.GetError().GetUserMessage())
}

// With no run store at all, a lost follower still ends on a retryable error.
func TestResumeFollowerDroppedNoRunStoreSaysRetry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewChatHandler(noGenerateService{t: t}, logger, nil)
	h.resumeBuf = resumebuf.New()

	got := followUntilDropped(t, h, uuid.New())
	require.Len(t, got, 1)
	require.Equal(t, "resume_lost", got[0].GetError().GetInternalCode())
}

// ownedRunStore is a run store with one run owned by owner. FindBySession
// honours ownership the way the SQL does (WHERE user_id = $1).
type ownedRunStore struct {
	runs.Store
	mu       sync.Mutex
	owner    uuid.UUID
	run      runs.Run
	full     bool
	reserved int
	finished []runs.Status
}

func (s *ownedRunStore) FindBySession(_ context.Context, userID, sessionID uuid.UUID) (runs.Run, bool, error) {
	if userID != s.owner || sessionID != s.run.SessionID {
		return runs.Run{}, false, nil
	}
	return s.run, true, nil
}

func (s *ownedRunStore) Reserve(context.Context, uuid.UUID) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		return uuid.Nil, runs.ErrAtCapacity
	}
	s.reserved++
	return uuid.New(), nil
}

func (s *ownedRunStore) Attach(context.Context, uuid.UUID, uuid.UUID, string, string) error {
	return nil
}

func (s *ownedRunStore) Finish(_ context.Context, id uuid.UUID, st runs.Status, _ string) (runs.Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = append(s.finished, st)
	return runs.Run{ID: id, Status: st}, true, nil
}

func (s *ownedRunStore) snapshot() (int, []runs.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserved, append([]runs.Status(nil), s.finished...)
}

// scriptedService emits a fixed run of events, like a real pipeline would.
type scriptedService struct {
	service.LlmInteractiontService
	events []locitypes.StreamEvent
	mu     sync.Mutex
	calls  int
}

func (s *scriptedService) ProcessUnifiedChatMessageStream(cc common.ChatContext) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	for _, ev := range s.events {
		cc.EventCh <- ev
	}
	return nil
}

func (s *scriptedService) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func startEvent(id string, sid uuid.UUID) locitypes.StreamEvent {
	return locitypes.StreamEvent{Type: locitypes.EventTypeStart, EventID: id, Data: map[string]any{
		"domain": "itinerary", "city": "Crete", "session_id": sid.String(),
	}}
}

// clientAs serves h over httptest as the given user.
func clientAs(t *testing.T, h *ChatHandler, userID uuid.UUID) chatconnect.ChatServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := chatconnect.NewChatServiceHandler(h,
		connect.WithInterceptors(claimsInjector{userID: userID.String()}))
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return chatconnect.NewChatServiceClient(srv.Client(), srv.URL)
}

func receiveAll(t *testing.T, c chatconnect.ChatServiceClient, req *connect.Request[chatv1.ChatRequest]) ([]*chatv1.StreamEvent, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := c.StreamChat(ctx, req)
	require.NoError(t, err)
	var got []*chatv1.StreamEvent
	for stream.Receive() {
		got = append(got, proto.Clone(stream.Msg()).(*chatv1.StreamEvent))
	}
	return got, stream.Err()
}

// A resume token skips the cap interceptor. When it cannot be answered from
// the buffer (here: no session_id at all) it generates, so the handler must
// hold it to the cap itself.
func TestResumeFallingThroughIsCapped(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &ownedRunStore{full: true}
	h := NewChatHandler(noGenerateService{t: t}, logger, nil).WithRuns(store, nil)

	_, err := receiveAll(t, clientAs(t, h, uuid.New()), connect.NewRequest(&chatv1.ChatRequest{
		Message:     "resume",
		CityName:    proto.String("Crete"),
		ResumeToken: proto.String("e1"),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	require.Contains(t, err.Error(), runs.CapMessage)
}

// A resume that falls through below the cap is reserved and tracked like any
// other run, so its row finishes instead of being left for the stale sweep.
func TestResumeFallingThroughIsTracked(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &ownedRunStore{}
	sid := uuid.New()
	svc := &scriptedService{events: []locitypes.StreamEvent{
		startEvent("s1", sid),
		{Type: locitypes.EventTypeComplete, EventID: "s2", IsFinal: true},
	}}
	h := NewChatHandler(svc, logger, nil).WithRuns(store, nil)

	got, err := receiveAll(t, clientAs(t, h, uuid.New()), connect.NewRequest(&chatv1.ChatRequest{
		Message:     "resume",
		CityName:    proto.String("Crete"),
		ResumeToken: proto.String("e1"),
	}))
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Eventually(t, func() bool {
		reserved, finished := store.snapshot()
		return reserved == 1 && len(finished) == 1 && finished[0] == runs.StatusDone
	}, 5*time.Second, 10*time.Millisecond)
}

// The buffer is gone (another pod, or a restart) but the caller's run is still
// running: say so, rather than start a second, untracked run of the same turn.
func TestResumeNoBufferRunStillRunningSaysResumeLost(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, sid := uuid.New(), uuid.New()
	store := &ownedRunStore{owner: owner, run: runs.Run{
		UserID: owner, SessionID: sid, Status: runs.StatusRunning, Domain: "itinerary", CityName: "Crete",
	}}
	h := NewChatHandler(noGenerateService{t: t}, logger, nil).WithRuns(store, nil)
	h.resumeBuf = resumebuf.New()

	got, err := receiveAll(t, clientAs(t, h, owner), resumeRequest(sid, "e7"))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_ERROR, got[0].GetEventType())
	require.True(t, got[0].GetIsFinal())
	require.True(t, got[0].GetError().GetRetryable())
	require.Equal(t, "resume_lost", got[0].GetError().GetInternalCode())
	reserved, _ := store.snapshot()
	require.Zero(t, reserved, "an answered resume reserves nothing")
}

// Knowing someone's session id is not enough to follow their stream: the
// resume falls through to the caller's own generation, and that generation
// writes nothing into the other person's buffer.
func TestResumeOfAnotherUsersSessionSeesNoneOfTheirEvents(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	victim, attacker := uuid.New(), uuid.New()
	victimSID, ownSID := uuid.New(), uuid.New()
	store := &ownedRunStore{owner: victim, run: runs.Run{
		UserID: victim, SessionID: victimSID, Status: runs.StatusRunning,
	}}
	svc := &scriptedService{events: []locitypes.StreamEvent{
		startEvent("own-start", ownSID),
		{Type: locitypes.EventTypeComplete, EventID: "own-done", IsFinal: true},
	}}
	h := NewChatHandler(svc, logger, nil).WithRuns(store, nil)
	h.resumeBuf = resumebuf.New()
	h.resumeBuf.Append(victimSID.String(), locitypes.StreamEvent{Type: locitypes.EventTypeStart, EventID: "secret-1"})
	h.resumeBuf.Append(victimSID.String(), locitypes.StreamEvent{Type: "token", EventID: "secret-2", Message: "private"})

	got, err := receiveAll(t, clientAs(t, h, attacker), resumeRequest(victimSID, "secret-1"))
	require.NoError(t, err)
	var ids []string
	for _, ev := range got {
		ids = append(ids, ev.GetEventId())
	}
	require.Equal(t, []string{"own-start", "own-done"}, ids)
	require.Equal(t, 1, svc.callCount())

	victimEvents, ok := h.resumeBuf.Replay(victimSID.String(), "")
	require.True(t, ok)
	require.Len(t, victimEvents, 2, "the attacker's run must not append into the victim's buffer")
	ownEvents, ok := h.resumeBuf.Replay(ownSID.String(), "")
	require.True(t, ok)
	require.Len(t, ownEvents, 2)
}

// The start event names the session the pipeline chose. When it differs from
// the one requested (refused, so a new one was minted), the buffer follows it.
func TestStartEventRekeysResumeBuffer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	requested, minted := uuid.New(), uuid.New()
	svc := &scriptedService{events: []locitypes.StreamEvent{
		startEvent("m1", minted),
		{Type: locitypes.EventTypeComplete, EventID: "m2", IsFinal: true},
	}}
	h := NewChatHandler(svc, logger, nil) // no run store: the requested id is pre-set
	h.resumeBuf = resumebuf.New()

	_, err := receiveAll(t, clientAs(t, h, uuid.New()), connect.NewRequest(&chatv1.ChatRequest{
		Message:   "continue",
		CityName:  proto.String("Crete"),
		SessionId: proto.String(requested.String()),
	}))
	require.NoError(t, err)

	_, ok := h.resumeBuf.Replay(requested.String(), "")
	require.False(t, ok, "nothing is buffered under the refused session id")
	events, ok := h.resumeBuf.Replay(minted.String(), "")
	require.True(t, ok)
	require.Len(t, events, 2)
}
