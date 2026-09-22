package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
