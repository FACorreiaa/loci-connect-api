package service_test

import (
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
)

func newTokenManager() service.TokenManager {
	return service.NewTokenManager(
		[]byte("access-secret-for-tests-0123456789"),
		[]byte("refresh-secret-for-tests-01234567"),
		time.Hour,
		30*24*time.Hour,
	)
}

func TestMFAChallengeTokenRoundTrips(t *testing.T) {
	tm := newTokenManager()

	token, expiresAt, err := tm.GenerateMFAChallengeToken("user-1", "a@b.com", "traveller", "member")
	if err != nil {
		t.Fatalf("GenerateMFAChallengeToken: %v", err)
	}
	if !expiresAt.After(time.Now()) {
		t.Error("the challenge token must expire in the future")
	}
	// Short-lived by design: a leaked challenge token should be close to
	// worthless within minutes.
	if d := time.Until(expiresAt); d > 15*time.Minute {
		t.Errorf("challenge TTL is %v, want a short window", d)
	}

	claims, err := tm.ValidateMFAChallengeToken(token)
	if err != nil {
		t.Fatalf("ValidateMFAChallengeToken: %v", err)
	}
	if claims.UserID != "user-1" {
		t.Errorf("UserID = %q, want \"user-1\"", claims.UserID)
	}
}

// The guarantee that makes step-up meaningful: the token handed out after the
// password step must not open the API on its own.
func TestAnMFAChallengeTokenIsNotAnAccessToken(t *testing.T) {
	tm := newTokenManager()

	challenge, _, err := tm.GenerateMFAChallengeToken("user-1", "a@b.com", "traveller", "member")
	if err != nil {
		t.Fatalf("GenerateMFAChallengeToken: %v", err)
	}

	if _, err := tm.ValidateAccessToken(challenge); err == nil {
		t.Fatal("an MFA challenge token must NOT validate as an access token")
	}
	if _, err := tm.ValidateRefreshToken(challenge); err == nil {
		t.Fatal("an MFA challenge token must NOT validate as a refresh token")
	}
}

// And the reverse: a real access or refresh token must not complete a login.
func TestAccessAndRefreshTokensCannotCompleteAnMFAChallenge(t *testing.T) {
	tm := newTokenManager()

	pair, err := tm.GenerateTokenPair("user-1", "a@b.com", "traveller", "member")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	for label, token := range map[string]string{
		"access":  pair.AccessToken,
		"refresh": pair.RefreshToken,
	} {
		if _, err := tm.ValidateMFAChallengeToken(token); err == nil {
			t.Errorf("a %s token must not validate as an MFA challenge token", label)
		}
	}
}

// The challenge key is derived from the access secret, so a deployment that
// rotates its JWT secret invalidates outstanding challenges rather than
// accepting them under the new key.
func TestChallengeTokensDoNotSurviveASecretRotation(t *testing.T) {
	old := newTokenManager()
	token, _, err := old.GenerateMFAChallengeToken("user-1", "a@b.com", "traveller", "member")
	if err != nil {
		t.Fatalf("GenerateMFAChallengeToken: %v", err)
	}

	rotated := service.NewTokenManager(
		[]byte("a-completely-different-access-key"),
		[]byte("refresh-secret-for-tests-01234567"),
		time.Hour,
		30*24*time.Hour,
	)
	if _, err := rotated.ValidateMFAChallengeToken(token); err == nil {
		t.Error("a challenge token must not validate after the access secret changes")
	}
}

func TestAccessTokensStillValidateNormally(t *testing.T) {
	tm := newTokenManager()

	pair, err := tm.GenerateTokenPair("user-1", "a@b.com", "traveller", "member")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	claims, err := tm.ValidateAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken: %v", err)
	}
	if claims.UserID != "user-1" || claims.Role != "member" {
		t.Errorf("claims = %+v, want user-1/member", claims)
	}
}
