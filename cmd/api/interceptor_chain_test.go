package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	chatpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat/chatconnect"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type recordingSubscriptionService struct {
	consumeCalled bool
	consumeUserID uuid.UUID
	consumeEmail  string
	consumeErr    error
}

func (s *recordingSubscriptionService) ConsumeQuota(_ context.Context, userID uuid.UUID, email string) error {
	s.consumeCalled = true
	s.consumeUserID = userID
	s.consumeEmail = email
	return s.consumeErr
}

func (s *recordingSubscriptionService) InvalidatePlan(_ uuid.UUID) {}

func signTestJWT(t *testing.T, secret []byte, userID uuid.UUID, email string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &interceptors.Claims{
		UserID: userID.String(),
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed
}

func startChatHandler(secret []byte, subSvc subscription.Service) http.Handler {
	authInterceptor := interceptors.NewAuthInterceptor(secret)
	subInterceptor := subscription.NewRateLimitInterceptor(subSvc)

	return connect.NewUnaryHandler(
		chatconnect.ChatServiceStartChatProcedure,
		func(ctx context.Context, _ *connect.Request[chatpb.StartChatRequest]) (*connect.Response[chatpb.ChatResponse], error) {
			return connect.NewResponse(&chatpb.ChatResponse{}), nil
		},
		connect.WithInterceptors(authInterceptor, subInterceptor),
	)
}

func TestInterceptorChain_AuthBeforeSubscriptionQuota(t *testing.T) {
	secret := []byte("test-secret")
	userID := uuid.New()
	email := "traveler@example.com"
	subSvc := &recordingSubscriptionService{}

	srv := httptest.NewServer(startChatHandler(secret, subSvc))
	defer srv.Close()

	client := chatconnect.NewChatServiceClient(http.DefaultClient, srv.URL)
	req := connect.NewRequest(&chatpb.StartChatRequest{})
	req.Header().Set("Authorization", "Bearer "+signTestJWT(t, secret, userID, email))

	if _, err := client.StartChat(context.Background(), req); err != nil {
		t.Fatalf("StartChat error: %v", err)
	}
	if !subSvc.consumeCalled {
		t.Fatal("expected subscription ConsumeQuota to run with authenticated user")
	}
	if subSvc.consumeUserID != userID {
		t.Fatalf("consume user id = %s, want %s", subSvc.consumeUserID, userID)
	}
	if subSvc.consumeEmail != email {
		t.Fatalf("consume email = %q, want %q", subSvc.consumeEmail, email)
	}
}

func TestInterceptorChain_SubscriptionBeforeAuthReturnsUnauthenticated(t *testing.T) {
	secret := []byte("test-secret")
	userID := uuid.New()
	subSvc := &recordingSubscriptionService{}

	// Wrong order: subscription before auth (the pre-fix bug).
	handler := connect.NewUnaryHandler(
		chatconnect.ChatServiceStartChatProcedure,
		func(ctx context.Context, _ *connect.Request[chatpb.StartChatRequest]) (*connect.Response[chatpb.ChatResponse], error) {
			return connect.NewResponse(&chatpb.ChatResponse{}), nil
		},
		connect.WithInterceptors(
			subscription.NewRateLimitInterceptor(subSvc),
			interceptors.NewAuthInterceptor(secret),
		),
	)

	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := chatconnect.NewChatServiceClient(http.DefaultClient, srv.URL)
	req := connect.NewRequest(&chatpb.StartChatRequest{})
	req.Header().Set("Authorization", "Bearer "+signTestJWT(t, secret, userID, "traveler@example.com"))

	_, err := client.StartChat(context.Background(), req)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected Unauthenticated with wrong order, got %v (%v)", connect.CodeOf(err), err)
	}
	if subSvc.consumeCalled {
		t.Fatal("ConsumeQuota should not run when claims are missing from context")
	}
}

// streamChatHandler serves the real server-streaming StreamChat procedure
// through the auth + quota interceptors, guarding against the pre-fix bug
// where StreamChat bypassed the daily quota entirely.
func streamChatHandler(secret []byte, subSvc subscription.Service) http.Handler {
	return connect.NewServerStreamHandler(
		chatconnect.ChatServiceStreamChatProcedure,
		func(ctx context.Context, _ *connect.Request[chatpb.ChatRequest], stream *connect.ServerStream[chatpb.StreamEvent]) error {
			return stream.Send(&chatpb.StreamEvent{})
		},
		connect.WithInterceptors(
			interceptors.NewAuthInterceptor(secret),
			subscription.NewRateLimitInterceptor(subSvc),
		),
	)
}

func TestInterceptorChain_StreamChatConsumesQuota(t *testing.T) {
	secret := []byte("test-secret")
	userID := uuid.New()
	subSvc := &recordingSubscriptionService{}

	srv := httptest.NewServer(streamChatHandler(secret, subSvc))
	defer srv.Close()

	client := chatconnect.NewChatServiceClient(http.DefaultClient, srv.URL)
	req := connect.NewRequest(&chatpb.ChatRequest{})
	req.Header().Set("Authorization", "Bearer "+signTestJWT(t, secret, userID, "traveler@example.com"))

	stream, err := client.StreamChat(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChat error: %v", err)
	}
	for stream.Receive() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("StreamChat stream error: %v", err)
	}
	if !subSvc.consumeCalled {
		t.Fatal("expected ConsumeQuota to run for StreamChat (streaming bypass regression)")
	}
	if subSvc.consumeUserID != userID {
		t.Fatalf("consume user id = %s, want %s", subSvc.consumeUserID, userID)
	}
}

func TestInterceptorChain_StreamChatQuotaDenialReachesClient(t *testing.T) {
	secret := []byte("test-secret")
	subSvc := &recordingSubscriptionService{
		consumeErr: &subscription.QuotaExceededError{Plan: subscription.PlanFree, Limit: 10},
	}

	srv := httptest.NewServer(streamChatHandler(secret, subSvc))
	defer srv.Close()

	client := chatconnect.NewChatServiceClient(http.DefaultClient, srv.URL)
	req := connect.NewRequest(&chatpb.ChatRequest{})
	req.Header().Set("Authorization", "Bearer "+signTestJWT(t, secret, uuid.New(), "traveler@example.com"))

	stream, err := client.StreamChat(context.Background(), req)
	if err == nil {
		for stream.Receive() {
		}
		err = stream.Err()
	}
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("expected ResourceExhausted for exhausted stream quota, got %v (%v)", connect.CodeOf(err), err)
	}
}
