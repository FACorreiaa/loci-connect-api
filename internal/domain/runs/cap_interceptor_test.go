package runs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat/chatconnect"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type fakeStore struct {
	Store
	mu       sync.Mutex
	full     bool
	reserved []uuid.UUID
	released []uuid.UUID
}

func (f *fakeStore) Reserve(context.Context, uuid.UUID) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.full {
		return uuid.Nil, ErrAtCapacity
	}
	id := uuid.New()
	f.reserved = append(f.reserved, id)
	return id, nil
}

func (f *fakeStore) Release(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, id)
	return nil
}

// withUser stands in for the auth interceptor, which runs before this one.
type withUser struct{ connect.Interceptor }

func (withUser) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(context.WithValue(ctx, interceptors.UserIDKey, uuid.NewString()), conn)
	}
}
func (withUser) WrapUnary(n connect.UnaryFunc) connect.UnaryFunc { return n }
func (withUser) WrapStreamingClient(n connect.StreamingClientFunc) connect.StreamingClientFunc {
	return n
}

func serve(t *testing.T, store Store, handler func(context.Context, *connect.Request[chatv1.ChatRequest], *connect.ServerStream[chatv1.StreamEvent]) error) chatconnect.ChatServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(chatconnect.ChatServiceStreamChatProcedure, connect.NewServerStreamHandler(
		chatconnect.ChatServiceStreamChatProcedure, handler,
		connect.WithInterceptors(withUser{}, NewCapInterceptor(store, nil)),
	))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return chatconnect.NewChatServiceClient(srv.Client(), srv.URL)
}

func drain(t *testing.T, c chatconnect.ChatServiceClient, req *chatv1.ChatRequest) error {
	t.Helper()
	s, err := c.StreamChat(context.Background(), connect.NewRequest(req))
	require.NoError(t, err)
	for s.Receive() {
	}
	return s.Err()
}

func TestCapRefusesBeforeTheHandlerRuns(t *testing.T) {
	store := &fakeStore{full: true}
	ran := false
	c := serve(t, store, func(context.Context, *connect.Request[chatv1.ChatRequest], *connect.ServerStream[chatv1.StreamEvent]) error {
		ran = true
		return nil
	})
	err := drain(t, c, &chatv1.ChatRequest{Message: "3 days in Crete"})
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	require.Contains(t, err.Error(), CapMessage)
	require.False(t, ran)
}

func TestResumeIsNotCapped(t *testing.T) {
	store := &fakeStore{full: true}
	c := serve(t, store, func(context.Context, *connect.Request[chatv1.ChatRequest], *connect.ServerStream[chatv1.StreamEvent]) error {
		return nil
	})
	token := "evt-1"
	sid := uuid.NewString()
	require.NoError(t, drain(t, c, &chatv1.ChatRequest{Message: "x", SessionId: &sid, ResumeToken: &token}))
	require.Empty(t, store.reserved)
}

func TestHandlerSeesTheRequestAndTheReservation(t *testing.T) {
	store := &fakeStore{}
	var got string
	var claimed bool
	c := serve(t, store, func(ctx context.Context, req *connect.Request[chatv1.ChatRequest], _ *connect.ServerStream[chatv1.StreamEvent]) error {
		got = req.Msg.GetMessage()
		r, ok := ReservationFrom(ctx)
		claimed = ok
		r.Claim()
		return nil
	})
	require.NoError(t, drain(t, c, &chatv1.ChatRequest{Message: "3 days in Crete"}))
	require.Equal(t, "3 days in Crete", got, "the peeked message must reach the handler intact")
	require.True(t, claimed)
	require.Empty(t, store.released, "a claimed reservation belongs to the handler")
}

func TestUnclaimedReservationIsReleased(t *testing.T) {
	store := &fakeStore{}
	c := serve(t, store, func(context.Context, *connect.Request[chatv1.ChatRequest], *connect.ServerStream[chatv1.StreamEvent]) error {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("daily quota"))
	})
	_ = drain(t, c, &chatv1.ChatRequest{Message: "x"})
	require.Equal(t, store.reserved, store.released)
}
