package service

import (
	"strings"
	"testing"

	"github.com/markbates/goth"
)

func TestNewOAuthService_NoProvidersConfigured(t *testing.T) {
	goth.ClearProviders()
	svc := NewOAuthService(OAuthConfig{})

	if svc.IsProviderConfigured("google") {
		t.Error("google should not be configured with empty config")
	}
	if svc.IsProviderConfigured("apple") {
		t.Error("apple should not be configured with empty config")
	}
}

func TestGetAuthURL_UnconfiguredProviderErrors(t *testing.T) {
	goth.ClearProviders()
	svc := NewOAuthService(OAuthConfig{})

	if _, _, err := svc.GetAuthURL("google", "http://localhost/cb"); err == nil {
		t.Fatal("GetAuthURL on unconfigured provider should error")
	}
}

func TestGoogleProviderConfigured(t *testing.T) {
	goth.ClearProviders()
	svc := NewOAuthService(OAuthConfig{
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
		CallbackBaseURL:    "http://localhost:8080/auth",
	})

	if !svc.IsProviderConfigured("google") {
		t.Fatal("google should be configured")
	}

	authURL, state, err := svc.GetAuthURL("google", "http://localhost:8080/auth/google/callback")
	if err != nil {
		t.Fatalf("GetAuthURL: %v", err)
	}
	if !strings.Contains(authURL, "accounts.google.com") {
		t.Errorf("auth URL should point at Google, got %q", authURL)
	}
	if !strings.Contains(authURL, "test-client-id") {
		t.Errorf("auth URL should embed client id, got %q", authURL)
	}
	if state == "" {
		t.Error("state (serialized session) should not be empty")
	}
}

func TestCompleteAuth_UnconfiguredProviderErrors(t *testing.T) {
	goth.ClearProviders()
	svc := NewOAuthService(OAuthConfig{})

	if _, err := svc.CompleteAuth("google", "code", "state"); err == nil {
		t.Fatal("CompleteAuth on unconfigured provider should error")
	}
}
