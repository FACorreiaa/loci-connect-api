package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/servicetest"
)

// stubMFA is a minimal MFAVerifier. The point of these tests is the login flow's
// ordering, not the TOTP maths, which is covered in internal/domain/mfa.
type stubMFA struct {
	enabled    bool
	enabledErr error
	verifyErr  error

	verifyCalls  int
	lastUserID   uuid.UUID
	lastCode     string
	lastRecovery string
}

func (s *stubMFA) IsEnabled(_ context.Context, _ uuid.UUID) (bool, error) {
	if s.enabledErr != nil {
		return false, s.enabledErr
	}
	return s.enabled, nil
}

func (s *stubMFA) VerifyForLogin(_ context.Context, userID uuid.UUID, code, recoveryCode string) error {
	s.verifyCalls++
	s.lastUserID = userID
	s.lastCode = code
	s.lastRecovery = recoveryCode
	return s.verifyErr
}

const testPassword = "Str0ng!Pass"

// registerUser creates a user through the service so the password is hashed the
// same way login will check it, and returns the number of sessions that already
// exist afterwards — registration issues one of its own, so session assertions
// have to measure the delta.
//
// It also gives the mock a unique refresh token per call: the default constant
// would make every session collide on the same map key and hide a real leak.
func registerUser(t *testing.T, svc *service.AuthService, tokens *servicetest.MockTokenManager, repo *servicetest.MockAuthRepo, email string) int {
	t.Helper()

	issued := 0
	tokens.GenerateFunc = func(_, _, _, _ string) (*service.TokenPair, error) {
		issued++
		return &service.TokenPair{
			AccessToken:  fmt.Sprintf("access-%d", issued),
			RefreshToken: fmt.Sprintf("refresh-%d", issued),
			ExpiresAt:    time.Now().Add(time.Hour),
			TokenType:    "Bearer",
		}, nil
	}

	_, err := svc.RegisterUser(context.Background(), service.RegisterParams{
		Email:       email,
		Username:    "traveller",
		Password:    testPassword,
		DisplayName: "Traveller",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return len(repo.Sessions)
}

// The core guarantee of step-up: a correct password alone yields no usable
// credential when the user has a second factor.
func TestLoginWithMFAIssuesNoTokens(t *testing.T) {
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	svc.WithMFA(&stubMFA{enabled: true})
	baseline := registerUser(t, svc, tokens, repo, "mfa@example.com")

	result, err := svc.Login(context.Background(), service.LoginParams{
		Email:    "mfa@example.com",
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if !result.MFARequired {
		t.Error("expected MFARequired")
	}
	if result.Tokens != nil {
		t.Fatal("a challenged login must not issue tokens — that is the whole point of step-up")
	}
	if result.MFAToken == "" {
		t.Error("expected a challenge token so the client can complete the login")
	}

	// No new session row either: a session is a credential too. Measured against
	// the session registration already created.
	if got := len(repo.Sessions) - baseline; got != 0 {
		t.Errorf("a challenged login created %d sessions, want 0", got)
	}

	user, _ := repo.GetUserByEmail(context.Background(), "mfa@example.com")
	if user.LastLoginAt != nil {
		t.Error("last login must not be stamped before the login actually completes")
	}
}

func TestLoginWithoutMFAStillIssuesTokens(t *testing.T) {
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	svc.WithMFA(&stubMFA{enabled: false})
	registerUser(t, svc, tokens, repo, "nomfa@example.com")

	result, err := svc.Login(context.Background(), service.LoginParams{
		Email:    "nomfa@example.com",
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.MFARequired {
		t.Error("a user without MFA must not be challenged")
	}
	if result.Tokens == nil {
		t.Fatal("expected tokens for a user without MFA")
	}
}

// A wrong password must not produce a challenge token: that would confirm the
// account exists and hand out a token to someone who failed the first factor.
func TestLoginWithAWrongPasswordNeverReachesTheMFAStep(t *testing.T) {
	stub := &stubMFA{enabled: true}
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	svc.WithMFA(stub)
	registerUser(t, svc, tokens, repo, "mfa@example.com")

	_, err := svc.Login(context.Background(), service.LoginParams{
		Email:    "mfa@example.com",
		Password: "WrongPass!1",
	})
	if !errors.Is(err, common.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	if stub.verifyCalls != 0 {
		t.Error("MFA must not be consulted after a failed password")
	}
}

// If the MFA table cannot be read, login must fail rather than proceed. Failing
// open would silently disable MFA for every enrolled user.
func TestLoginFailsClosedWhenMFAStatusIsUnknown(t *testing.T) {
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	svc.WithMFA(&stubMFA{enabledErr: errors.New("database unavailable")})
	registerUser(t, svc, tokens, repo, "mfa@example.com")

	result, err := svc.Login(context.Background(), service.LoginParams{
		Email:    "mfa@example.com",
		Password: testPassword,
	})
	if err == nil {
		t.Fatal("expected login to fail when MFA status cannot be determined")
	}
	if result != nil {
		t.Error("no result should be returned when the MFA check errored")
	}
}

func TestCompleteMFALoginIssuesTokensAfterAValidFactor(t *testing.T) {
	stub := &stubMFA{enabled: true}
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	svc.WithMFA(stub)
	baseline := registerUser(t, svc, tokens, repo, "mfa@example.com")

	challenge, err := svc.Login(context.Background(), service.LoginParams{
		Email:    "mfa@example.com",
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	result, err := svc.CompleteMFALogin(context.Background(), challenge.MFAToken, "123456", "", service.SessionMetadata{})
	if err != nil {
		t.Fatalf("CompleteMFALogin: %v", err)
	}
	if result.Tokens == nil {
		t.Fatal("expected tokens after a verified second factor")
	}
	if result.MFARequired {
		t.Error("the completed login must not still report MFARequired")
	}

	// The factor was checked for the user who was actually challenged.
	user, _ := repo.GetUserByEmail(context.Background(), "mfa@example.com")
	if stub.lastUserID != user.ID {
		t.Errorf("verified user %s, want %s", stub.lastUserID, user.ID)
	}
	if stub.lastCode != "123456" {
		t.Errorf("code passed through as %q, want \"123456\"", stub.lastCode)
	}
	if got := len(repo.Sessions) - baseline; got != 1 {
		t.Errorf("the completed login created %d sessions, want 1", got)
	}
}

func TestCompleteMFALoginIssuesNothingWhenTheFactorIsWrong(t *testing.T) {
	stub := &stubMFA{enabled: true, verifyErr: errors.New("invalid code")}
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	svc.WithMFA(stub)
	baseline := registerUser(t, svc, tokens, repo, "mfa@example.com")

	challenge, _ := svc.Login(context.Background(), service.LoginParams{
		Email:    "mfa@example.com",
		Password: testPassword,
	})

	result, err := svc.CompleteMFALogin(context.Background(), challenge.MFAToken, "000000", "", service.SessionMetadata{})
	if err == nil {
		t.Fatal("expected an error for an invalid second factor")
	}
	if result != nil {
		t.Error("no tokens may be returned when the factor failed")
	}
	if got := len(repo.Sessions) - baseline; got != 0 {
		t.Errorf("a failed second factor created %d sessions, want 0", got)
	}
}

// A challenge token is the only thing that completes a login. Anything else —
// including a random string, or an access token — must be refused.
func TestCompleteMFALoginRejectsATokenThatIsNotAChallenge(t *testing.T) {
	stub := &stubMFA{enabled: true}
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	svc.WithMFA(stub)
	registerUser(t, svc, tokens, repo, "mfa@example.com")

	for _, token := range []string{"", "not-a-token", "access"} {
		_, err := svc.CompleteMFALogin(context.Background(), token, "123456", "", service.SessionMetadata{})
		if !errors.Is(err, common.ErrInvalidCredentials) {
			t.Errorf("token %q: expected ErrInvalidCredentials, got %v", token, err)
		}
	}
	if stub.verifyCalls != 0 {
		t.Error("a bad challenge token must be rejected before the factor is checked")
	}
}

// The account may be deactivated during the challenge window, so the state is
// re-read rather than trusted from the token's claims.
func TestCompleteMFALoginRefusesADeactivatedAccount(t *testing.T) {
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	svc.WithMFA(&stubMFA{enabled: true})
	registerUser(t, svc, tokens, repo, "mfa@example.com")

	challenge, _ := svc.Login(context.Background(), service.LoginParams{
		Email:    "mfa@example.com",
		Password: testPassword,
	})

	// Mutate the stored user: GetUserByEmail hands back a clone.
	repo.Users["mfa@example.com"].IsActive = false

	if _, err := svc.CompleteMFALogin(context.Background(), challenge.MFAToken, "123456", "", service.SessionMetadata{}); err == nil {
		t.Fatal("expected a deactivated account to be refused")
	}
}

func TestCompleteMFALoginPassesRecoveryCodesThrough(t *testing.T) {
	stub := &stubMFA{enabled: true}
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	svc.WithMFA(stub)
	registerUser(t, svc, tokens, repo, "mfa@example.com")

	challenge, _ := svc.Login(context.Background(), service.LoginParams{
		Email:    "mfa@example.com",
		Password: testPassword,
	})

	if _, err := svc.CompleteMFALogin(context.Background(), challenge.MFAToken, "", "ABCDE-FGHJK", service.SessionMetadata{}); err != nil {
		t.Fatalf("CompleteMFALogin with a recovery code: %v", err)
	}
	if stub.lastRecovery != "ABCDE-FGHJK" {
		t.Errorf("recovery code passed through as %q", stub.lastRecovery)
	}
}

func TestCompleteMFALoginIsUnavailableWhenMFAIsNotConfigured(t *testing.T) {
	svc, _, _, _ := servicetest.NewTestAuthService()

	if _, err := svc.CompleteMFALogin(context.Background(), "anything", "123456", "", service.SessionMetadata{}); err == nil {
		t.Error("expected an error when MFA is not configured")
	}
}
