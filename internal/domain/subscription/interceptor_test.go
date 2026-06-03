package subscription

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	"github.com/google/uuid"
)

// stubService lets interceptor tests drive checkLimit outcomes directly.
type stubService struct {
	checkErr  error
	recordErr error
}

func (s *stubService) CheckRateLimit(_ context.Context, _ uuid.UUID, _ string) error {
	return s.checkErr
}

func (s *stubService) RecordUsage(_ context.Context, _ uuid.UUID) error {
	return s.recordErr
}

func TestIsChatProcedure(t *testing.T) {
	cases := map[string]bool{
		"/loci.chat.ChatService/StartChat":               true,
		"/loci.chat.ChatService/ContinueChat":            true,
		"/loci.chat.ChatService/ContinueSessionStreamed": true,
		"/loci.user.UserService/GetUser":                 false,
		"/loci.poi.POIService/SavePoi":                   false,
		"":                                               false,
	}
	for proc, want := range cases {
		if got := isChatProcedure(proc); got != want {
			t.Errorf("isChatProcedure(%q) = %v, want %v", proc, got, want)
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

func TestCheckLimit_Unauthenticated(t *testing.T) {
	i := NewRateLimitInterceptor(&stubService{})
	err := i.checkLimit(context.Background())
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected Unauthenticated, got %v (%v)", connect.CodeOf(err), err)
	}
}

func TestCheckLimit_QuotaExceededMapsToResourceExhausted(t *testing.T) {
	ctx, _ := ctxWithUser(t, "user@example.com")
	i := NewRateLimitInterceptor(&stubService{checkErr: ErrQuotaExceeded})
	err := i.checkLimit(ctx)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("expected ResourceExhausted, got %v (%v)", connect.CodeOf(err), err)
	}
}

func TestCheckLimit_OtherErrorMapsToInternal(t *testing.T) {
	ctx, _ := ctxWithUser(t, "user@example.com")
	i := NewRateLimitInterceptor(&stubService{checkErr: errors.New("boom")})
	err := i.checkLimit(ctx)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected Internal, got %v (%v)", connect.CodeOf(err), err)
	}
}

func TestCheckLimit_Pass(t *testing.T) {
	ctx, _ := ctxWithUser(t, "user@example.com")
	i := NewRateLimitInterceptor(&stubService{})
	if err := i.checkLimit(ctx); err != nil {
		t.Fatalf("checkLimit should pass, got %v", err)
	}
}
