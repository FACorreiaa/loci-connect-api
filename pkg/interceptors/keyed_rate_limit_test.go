package interceptors

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/protobuf/types/known/emptypb"
)

func startIPLimitedHandler(perSecond, burst, maxEntries int) http.Handler {
	interceptor := NewIPRateLimitInterceptor(perSecond, burst, maxEntries)
	return connect.NewUnaryHandler(
		"/test.Service/Method",
		func(_ context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
			return connect.NewResponse(&emptypb.Empty{}), nil
		},
		connect.WithInterceptors(interceptor),
	)
}

func TestIPRateLimitInterceptor_ExceedsLimitSetsRetryAfter(t *testing.T) {
	srv := httptest.NewServer(startIPLimitedHandler(1, 1, 10))
	defer srv.Close()

	client := connect.NewClient[emptypb.Empty, emptypb.Empty](
		http.DefaultClient,
		srv.URL,
		connect.WithProtoJSON(),
	)

	send := func() error {
		req := connect.NewRequest(&emptypb.Empty{})
		req.Header().Set("X-Forwarded-For", "203.0.113.10")
		_, err := client.CallUnary(context.Background(), req)
		return err
	}

	if err := send(); err != nil {
		t.Fatalf("first request should pass: %v", err)
	}

	err := send()
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("expected resource exhausted, got %v", err)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect error, got %T", err)
	}
	if got := connectErr.Meta().Get("Retry-After"); got == "" {
		t.Fatal("expected Retry-After header on rate limit error")
	}
}

func TestUserRateLimitInterceptor_SkipsWithoutUserID(t *testing.T) {
	if NewUserRateLimitInterceptor(0, 0, 10) != nil {
		t.Fatal("expected nil interceptor when disabled")
	}

	enabled := NewUserRateLimitInterceptor(1, 1, 10)
	handler := enabled.WrapUnary(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	})
	req := connect.NewRequest(&emptypb.Empty{})

	for i := 0; i < 2; i++ {
		if _, err := handler(context.Background(), req); err != nil {
			t.Fatalf("public request %d should not be user-limited: %v", i+1, err)
		}
	}
}

func TestUserRateLimitInterceptor_LimitsAuthenticatedUser(t *testing.T) {
	interceptor := NewUserRateLimitInterceptor(1, 1, 10)
	handler := interceptor.WrapUnary(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	})
	req := connect.NewRequest(&emptypb.Empty{})

	ctx := ContextWithClaims(context.Background(), &Claims{
		UserID:           "user-42",
		RegisteredClaims: jwt.RegisteredClaims{},
	})

	if _, err := handler(ctx, req); err != nil {
		t.Fatalf("first authenticated request should pass: %v", err)
	}

	_, err := handler(ctx, req)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("expected resource exhausted, got %v", err)
	}
}

func TestClientIPFromHeader_PrefersForwardedFor(t *testing.T) {
	header := http.Header{
		"X-Forwarded-For": []string{"198.51.100.7, 10.0.0.1"},
		"X-Real-IP":       []string{"203.0.113.1"},
	}
	if got := clientIPFromHeader(header, "127.0.0.1:8080"); got != "198.51.100.7" {
		t.Fatalf("client IP = %q, want 198.51.100.7", got)
	}
}
