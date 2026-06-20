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
	checkCalled bool
	checkUserID uuid.UUID
	checkEmail  string
}

func (s *recordingSubscriptionService) CheckRateLimit(_ context.Context, userID uuid.UUID, email string) error {
	s.checkCalled = true
	s.checkUserID = userID
	s.checkEmail = email
	return nil
}

func (s *recordingSubscriptionService) RecordUsage(_ context.Context, _ uuid.UUID) error {
	return nil
}

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
	if !subSvc.checkCalled {
		t.Fatal("expected subscription CheckRateLimit to run with authenticated user")
	}
	if subSvc.checkUserID != userID {
		t.Fatalf("check user id = %s, want %s", subSvc.checkUserID, userID)
	}
	if subSvc.checkEmail != email {
		t.Fatalf("check email = %q, want %q", subSvc.checkEmail, email)
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
	if subSvc.checkCalled {
		t.Fatal("CheckRateLimit should not run when claims are missing from context")
	}
}
