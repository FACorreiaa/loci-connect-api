package interceptors

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"connectrpc.com/connect"
)

type stubStreamingConn struct {
	procedure string
}

func (s *stubStreamingConn) Spec() connect.Spec            { return connect.Spec{Procedure: s.procedure} }
func (s *stubStreamingConn) Peer() connect.Peer            { return connect.Peer{} }
func (s *stubStreamingConn) RequestHeader() http.Header    { return nil }
func (s *stubStreamingConn) ResponseHeader() http.Header   { return nil }
func (s *stubStreamingConn) ResponseTrailer() http.Header  { return nil }
func (s *stubStreamingConn) Send(_ any) error              { return nil }
func (s *stubStreamingConn) Receive(_ any) error           { return nil }

func TestRecoveryInterceptor_StreamingRecoversPanic(t *testing.T) {
	interceptor := NewRecoveryInterceptor(slog.Default())
	handler := interceptor.WrapStreamingHandler(func(_ context.Context, _ connect.StreamingHandlerConn) error {
		panic("stream boom")
	})

	err := handler(context.Background(), &stubStreamingConn{procedure: "/loci.chat.ChatService/StreamChat"})
	if err == nil || connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected internal error, got %v", err)
	}
}