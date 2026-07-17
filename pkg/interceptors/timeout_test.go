package interceptors

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestParseClientTimeout_ConnectHeader(t *testing.T) {
	header := http.Header{"Connect-Timeout-Ms": []string{"1500"}}
	got, ok := parseClientTimeout(header)
	if !ok || got != 1500*time.Millisecond {
		t.Fatalf("parseClientTimeout = %v, ok=%v", got, ok)
	}
}

func TestParseClientTimeout_GrpcHeader(t *testing.T) {
	header := http.Header{"Grpc-Timeout": []string{"2S"}}
	got, ok := parseClientTimeout(header)
	if !ok || got != 2*time.Second {
		t.Fatalf("parseClientTimeout = %v, ok=%v", got, ok)
	}
}

func TestTimeoutInterceptor_UnaryAppliesDefault(t *testing.T) {
	interceptor := NewTimeoutInterceptor(TimeoutConfig{
		Default: 20 * time.Millisecond,
		Chat:    time.Minute,
	})
	handler := interceptor.WrapUnary(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected deadline on context")
		}
		if time.Until(deadline) > 20*time.Millisecond || time.Until(deadline) < 5*time.Millisecond {
			t.Fatalf("unexpected deadline: %v", time.Until(deadline))
		}
		return connect.NewResponse(&emptypb.Empty{}), nil
	})

	req := connect.NewRequest(&emptypb.Empty{})
	req.Header().Set("X-Procedure", "/loci.user.UserService/GetUser")
	if _, err := handler(context.Background(), req); err != nil {
		t.Fatalf("handler error: %v", err)
	}
}

func TestResolveUnaryTimeout_ChatProcedureUsesChatDefault(t *testing.T) {
	interceptor := NewTimeoutInterceptor(TimeoutConfig{
		Default: 5 * time.Second,
		Chat:    90 * time.Second,
	})
	timeout, apply := interceptor.resolveUnaryTimeout("/loci.chat.ChatService/StartChat", nil)
	if !apply || timeout != 90*time.Second {
		t.Fatalf("resolveUnaryTimeout = %v, apply=%v", timeout, apply)
	}
}

func TestResolveUnaryTimeout_ClientHeaderOverridesDefault(t *testing.T) {
	interceptor := NewTimeoutInterceptor(TimeoutConfig{
		Default: 30 * time.Second,
		Chat:    90 * time.Second,
	})
	header := http.Header{"Connect-Timeout-Ms": []string{"2500"}}
	timeout, apply := interceptor.resolveUnaryTimeout("/loci.user.UserService/GetUser", header)
	if !apply || timeout != 2500*time.Millisecond {
		t.Fatalf("resolveUnaryTimeout = %v, apply=%v", timeout, apply)
	}
}
