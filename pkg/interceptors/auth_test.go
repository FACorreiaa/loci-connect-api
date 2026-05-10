package interceptors

import (
	"context"
	"testing"
)

func TestGetClaimsFromContext_MissingClaimsReturnsError(t *testing.T) {
	_, err := GetClaimsFromContext(context.Background())
	if err == nil {
		t.Fatalf("expected error when claims are missing from context, got nil")
	}
}

func TestGetClaimsFromContext_WrongTypeReturnsError(t *testing.T) {
	ctx := context.WithValue(context.Background(), claimsKey, "not-claims")
	_, err := GetClaimsFromContext(ctx)
	if err == nil {
		t.Fatalf("expected error when claims value has wrong type, got nil")
	}
}

func TestGetClaimsFromContext_PresentClaimsReturned(t *testing.T) {
	want := &Claims{UserID: "u1", Email: "u@example.com", Role: "user"}
	ctx := context.WithValue(context.Background(), claimsKey, want)
	got, err := GetClaimsFromContext(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("expected claims pointer %p, got %p", want, got)
	}
}
