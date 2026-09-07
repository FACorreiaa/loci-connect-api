package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/markbates/goth"
)

// mustService builds the service or fails the test, for the cases where the
// config is expected to be accepted.
func mustService(t *testing.T, cfg OAuthConfig) *OAuthService {
	t.Helper()
	goth.ClearProviders()
	svc, err := NewOAuthService(cfg, nil)
	if err != nil {
		t.Fatalf("NewOAuthService: %v", err)
	}
	return svc
}

// testP8 generates a throwaway P-256 key in the PKCS#8 PEM form Apple hands out
// as a .p8 file. Generated rather than checked in: a fixture signing key in the
// repo is a signing key in the repo.
func testP8(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func appleConfig(t *testing.T) OAuthConfig {
	return OAuthConfig{
		AppleClientID:   "fyi.lociai.web",
		AppleTeamID:     "ABCDE12345",
		AppleKeyID:      "KEY1234567",
		ApplePrivateKey: testP8(t),
		CallbackBaseURL: "https://api.lociai.fyi/auth/oauth",
	}
}

func TestNewOAuthService_NoProvidersConfigured(t *testing.T) {
	svc := mustService(t, OAuthConfig{})

	if svc.IsProviderConfigured("google") {
		t.Error("google should not be configured with empty config")
	}
	if svc.IsProviderConfigured("apple") {
		t.Error("apple should not be configured with empty config")
	}
}

func TestGetAuthURL_UnconfiguredProviderErrors(t *testing.T) {
	svc := mustService(t, OAuthConfig{})

	if _, _, err := svc.GetAuthURL("google", "http://localhost/cb"); err == nil {
		t.Fatal("GetAuthURL on unconfigured provider should error")
	}
}

func TestGoogleProviderConfigured(t *testing.T) {
	svc := mustService(t, OAuthConfig{
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
	svc := mustService(t, OAuthConfig{})

	if _, err := svc.CompleteAuth("google", "code", "state"); err == nil {
		t.Fatal("CompleteAuth on unconfigured provider should error")
	}
}

// The whole reason this file changed: the client secret is now signed here from
// the .p8, rather than read from a hand-minted APPLE_SECRET that expires on a
// date nobody has in mind.
func TestAppleSecretIsSignedFromThePrivateKey(t *testing.T) {
	svc := mustService(t, appleConfig(t))

	if !svc.IsProviderConfigured("apple") {
		t.Fatal("apple should be configured")
	}

	provider, err := goth.GetProvider("apple")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	// goth keeps the secret private, so the assertion is on what the provider
	// does with it rather than on the field: an auth URL only builds once the
	// provider is fully configured.
	session, err := provider.BeginAuth("state")
	if err != nil {
		t.Fatalf("BeginAuth: %v", err)
	}
	if _, err := session.GetAuthURL(); err != nil {
		t.Fatalf("GetAuthURL: %v", err)
	}
}

// Apple rejects a client secret whose lifetime exceeds six months, so the
// claims this signs have to stay inside that.
func TestTheSignedSecretExpiresInsideApplesCeiling(t *testing.T) {
	cfg := appleConfig(t)
	goth.ClearProviders()
	svc := &OAuthService{config: cfg}

	before := time.Now()
	if err := svc.registerApple(); err != nil {
		t.Fatalf("registerApple: %v", err)
	}

	// Re-sign so the token is in hand: registerApple hands its only copy to
	// goth, and reading it back out is not something goth offers.
	secret, err := appleMakeSecret(svc.config, time.Now())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims := decodeJWTClaims(t, secret)

	iat, exp := int64(claims["iat"].(float64)), int64(claims["exp"].(float64))
	life := time.Duration(exp-iat) * time.Second

	// Apple's documented maximum is 15777000 seconds.
	if life > 15777000*time.Second {
		t.Errorf("secret lifetime %v exceeds Apple's six-month ceiling", life)
	}
	// And it must be long enough that a daily renewal is slack, not a deadline.
	if life < 30*24*time.Hour {
		t.Errorf("secret lifetime %v is shorter than intended", life)
	}
	if iat < before.Unix()-5 {
		t.Errorf("iat %d predates the call", iat)
	}
	if got := claims["iss"]; got != cfg.AppleTeamID {
		t.Errorf("iss = %v, want the team id %q", got, cfg.AppleTeamID)
	}
	if got := claims["sub"]; got != cfg.AppleClientID {
		t.Errorf("sub = %v, want the client id %q", got, cfg.AppleClientID)
	}
}

// A provider the operator meant to enable must not silently stay off. The old
// code checked `AppleClientID != "" && AppleSecret != ""` and skipped Apple
// otherwise, so a missing team id left the button on the page failing for
// everyone with nothing in the logs.
func TestAPartlyConfiguredAppleIsRefused(t *testing.T) {
	full := appleConfig(t)

	for _, unset := range []string{"client id", "team id", "key id", "private key"} {
		t.Run(unset, func(t *testing.T) {
			cfg := full
			switch unset {
			case "client id":
				cfg.AppleClientID = ""
			case "team id":
				cfg.AppleTeamID = ""
			case "key id":
				cfg.AppleKeyID = ""
			case "private key":
				cfg.ApplePrivateKey = ""
			}

			goth.ClearProviders()
			_, err := NewOAuthService(cfg, nil)
			if err == nil {
				t.Fatal("a partly configured Apple was accepted")
			}
			if !strings.Contains(err.Error(), "partly configured") {
				t.Errorf("error should say what is wrong, got %q", err)
			}
		})
	}
}

func TestNoAppleSettingsAtAllIsNotAnError(t *testing.T) {
	svc := mustService(t, OAuthConfig{
		GoogleClientID:     "id",
		GoogleClientSecret: "secret",
		CallbackBaseURL:    "https://api.lociai.fyi/auth/oauth",
	})

	if svc.IsProviderConfigured("apple") {
		t.Error("apple should be off when none of its settings are present")
	}
	if !svc.IsProviderConfigured("google") {
		t.Error("google should still be on")
	}
}

// A key that is not a key must fail at boot, not at the first sign-in attempt.
func TestAnUnusablePrivateKeyIsRefused(t *testing.T) {
	for name, key := range map[string]string{
		"not pem":        "hunter2",
		"empty pem":      "-----BEGIN PRIVATE KEY-----\n-----END PRIVATE KEY-----\n",
		"wrong contents": "-----BEGIN PRIVATE KEY-----\naGVsbG8=\n-----END PRIVATE KEY-----\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := appleConfig(t)
			cfg.ApplePrivateKey = key

			goth.ClearProviders()
			_, err := NewOAuthService(cfg, nil)
			if err == nil {
				t.Fatal("an unusable private key was accepted")
			}
			// The message must name the variable to fix, and must not echo the
			// key material back into a log.
			if !strings.Contains(err.Error(), "APPLE_PRIVATE_KEY") {
				t.Errorf("error should name the variable, got %q", err)
			}
			if strings.Contains(err.Error(), "BEGIN PRIVATE KEY") {
				t.Errorf("error echoes the key material: %q", err)
			}
		})
	}
}

func TestRunAppleSecretRefreshReturnsWhenAppleIsOff(t *testing.T) {
	svc := mustService(t, OAuthConfig{})

	// No context needed: with Apple unconfigured this must return rather than
	// block on a ticker, so a caller can start it unconditionally.
	done := make(chan error, 1)
	go func() { done <- svc.RunAppleSecretRefresh(t.Context()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunAppleSecretRefresh = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunAppleSecretRefresh blocked with Apple unconfigured")
	}
}

func TestRunAppleSecretRefreshStopsWithItsContext(t *testing.T) {
	svc := mustService(t, appleConfig(t))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- svc.RunAppleSecretRefresh(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunAppleSecretRefresh = %v, want nil on cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunAppleSecretRefresh ignored its cancelled context")
	}
}

func decodeJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}
