package subscription

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	"github.com/google/uuid"
)

// stubService lets interceptor tests drive consumeQuota outcomes directly.
type stubService struct {
	consumeErr error
}

func (s *stubService) ConsumeQuota(_ context.Context, _ uuid.UUID, _ string) error {
	return s.consumeErr
}

func (s *stubService) InvalidatePlan(_ uuid.UUID) {}

func TestIsMeteredProcedure(t *testing.T) {
	cases := map[string]bool{
		"/loci.chat.ChatService/StartChat":               true,
		"/loci.chat.ChatService/ContinueChat":            true,
		"/loci.chat.ChatService/StreamChat":              true,
		"/loci.chat.ChatService/GetChatSession":          false,
		"/loci.chat.ChatService/ContinueSessionStreamed": false, // no such RPC in the proto
		"/loci.user.UserService/GetUser":                 false,
		"/loci.poi.POIService/SearchPOI":                 false,
		"":                                               false,
	}
	for proc, want := range cases {
		if got := isMeteredProcedure(proc); got != want {
			t.Errorf("isMeteredProcedure(%q) = %v, want %v", proc, got, want)
		}
	}
}

func ctxWithUser(t *testing.T, email string) (context.Context, uuid.UUID) {
	t.Helper()
	id := uuid.New()
	ctx := interceptors.ContextWithClaims(context.Background(), &interceptors.Claims{
		UserID: id.String(),
		Email:  email,
	})
	return ctx, id
}

func TestExtractUser(t *testing.T) {
	ctx, id := ctxWithUser(t, "user@example.com")
	got, err := extractUser(ctx)
	if err != nil {
		t.Fatalf("extractUser: %v", err)
	}
	if got != id {
		t.Fatalf("extractUser id mismatch: got %s want %s", got, id)
	}
}

func TestExtractUser_NoClaims(t *testing.T) {
	if _, err := extractUser(context.Background()); err == nil {
		t.Fatal("extractUser without claims should error")
	}
}

func TestExtractEmail(t *testing.T) {
	ctx, _ := ctxWithUser(t, "person@example.com")
	if got := extractEmail(ctx); got != "person@example.com" {
		t.Fatalf("extractEmail = %q, want person@example.com", got)
	}
	if got := extractEmail(context.Background()); got != "" {
		t.Fatalf("extractEmail without claims = %q, want empty", got)
	}
}

func TestConsumeQuota_Unauthenticated(t *testing.T) {
	i := NewRateLimitInterceptor(&stubService{})
	err := i.consumeQuota(context.Background())
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected Unauthenticated, got %v (%v)", connect.CodeOf(err), err)
	}
}

func TestConsumeQuota_FreeDenialMapsToResourceExhaustedWithReason(t *testing.T) {
	ctx, _ := ctxWithUser(t, "user@example.com")
	i := NewRateLimitInterceptor(&stubService{
		consumeErr: &QuotaExceededError{Plan: PlanFree, Limit: 10},
	})

	err := i.consumeQuota(ctx)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("expected ResourceExhausted, got %v (%v)", connect.CodeOf(err), err)
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if got := cerr.Meta().Get(QuotaReasonHeader); got != QuotaReasonFreeDaily {
		t.Fatalf("quota reason = %q, want %q", got, QuotaReasonFreeDaily)
	}
}

func TestConsumeQuota_ProDenialCarriesFairUseReason(t *testing.T) {
	ctx, _ := ctxWithUser(t, "user@example.com")
	i := NewRateLimitInterceptor(&stubService{
		consumeErr: &QuotaExceededError{Plan: PlanPremiumMonthly, Limit: 300},
	})

	err := i.consumeQuota(ctx)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("expected ResourceExhausted, got %v (%v)", connect.CodeOf(err), err)
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if got := cerr.Meta().Get(QuotaReasonHeader); got != QuotaReasonFairUse {
		t.Fatalf("quota reason = %q, want %q", got, QuotaReasonFairUse)
	}
}

func TestConsumeQuota_OtherErrorMapsToInternal(t *testing.T) {
	ctx, _ := ctxWithUser(t, "user@example.com")
	i := NewRateLimitInterceptor(&stubService{consumeErr: errors.New("boom")})
	err := i.consumeQuota(ctx)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected Internal, got %v (%v)", connect.CodeOf(err), err)
	}
}

func TestConsumeQuota_Pass(t *testing.T) {
	ctx, _ := ctxWithUser(t, "user@example.com")
	i := NewRateLimitInterceptor(&stubService{})
	if err := i.consumeQuota(ctx); err != nil {
		t.Fatalf("consumeQuota should pass, got %v", err)
	}
}
