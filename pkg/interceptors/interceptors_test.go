package interceptors

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRequestIDInterceptor_GeneratesID(t *testing.T) {
	interceptor := NewRequestIDInterceptor("X-Request-ID")
	handler := interceptor.WrapUnary(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		id, ok := RequestIDFromContext(ctx)
		if !ok || id == "" {
			t.Fatalf("expected request id in context")
		}
		return connect.NewResponse(&emptypb.Empty{}), nil
	})

	req := connect.NewRequest(&emptypb.Empty{})
	if _, err := handler(context.Background(), req); err != nil {
		t.Fatalf("handler error: %v", err)
	}
}

func TestRateLimitInterceptor_ExceedsLimit(t *testing.T) {
	limiter := rate.NewLimiter(0, 0)
	interceptor := NewRateLimitInterceptor(limiter)
	handler := interceptor.WrapUnary(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	})
	req := connect.NewRequest(&emptypb.Empty{})
	_, err := handler(context.Background(), req)
	if err == nil || connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("expected resource exhausted, got %v", err)
	}
}
