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
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// panickingService is a service whose streaming entry point panics, standing
// in for any unexpected runtime failure inside the LLM pipeline.
type panickingService struct {
	service.LlmInteractiontService
}

func (panickingService) ProcessUnifiedChatMessageStream(common.ChatContext) error {
	panic("simulated pipeline panic")
}

// claimsInjector simulates an authenticated request without running the JWT
// interceptor, for both unary and streaming handlers.
type claimsInjector struct{ userID string }

func (i claimsInjector) inject(ctx context.Context) context.Context {
	return interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: i.userID})
}

func (i claimsInjector) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return next(i.inject(ctx), req)
	}
}

func (claimsInjector) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i claimsInjector) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(i.inject(ctx), conn)
	}
}

// TestStreamChatContainsServicePanic guards the process against a panic in the
// streaming pipeline goroutine: the client must receive a terminal error event
// and the RPC must end cleanly instead of the whole server crashing.
func TestStreamChatContainsServicePanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewChatHandler(panickingService{}, logger, nil)

	mux := http.NewServeMux()
	path, handler := chatconnect.NewChatServiceHandler(h,
		connect.WithInterceptors(claimsInjector{userID: uuid.New().String()}))
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := chatconnect.NewChatServiceClient(srv.Client(), srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := client.StreamChat(ctx, connect.NewRequest(&chatv1.ChatRequest{
		Message:  "hello",
		CityName: proto.String("Porto"),
	}))
	require.NoError(t, err)

	sawError := false
	for stream.Receive() {
		if stream.Msg().GetEventType() == chatv1.StreamEventType_STREAM_EVENT_TYPE_ERROR {
			sawError = true
		}
	}
	require.NoError(t, stream.Err(), "stream should end cleanly, not with a transport error")
	require.True(t, sawError, "client should receive a terminal error event when the pipeline panics")
}
