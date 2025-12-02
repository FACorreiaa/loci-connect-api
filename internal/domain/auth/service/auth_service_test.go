package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/repository"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/servicetest"
)

const (
	tokenTypeEmailVerification = "email_verification"
	tokenTypePasswordReset     = "password_reset"
)

func TestAuthService_RegisterUser_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, tokens, email := servicetest.NewTestAuthService()

	expectedPair := &service.TokenPair{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour),
		TokenType:    "Bearer",
	}
	tokens.GenerateFunc = func(_, _, _, _ string) (*service.TokenPair, error) {
		return expectedPair, nil
	}

	result, err := svc.RegisterUser(ctx, service.RegisterParams{
		Email:       "jane@example.com",
		Username:    "jane",
		Password:    "Str0ng!Pass",
		DisplayName: "Jane Doe",
	})
	if err != nil {
		t.Fatalf("RegisterUser() error = %v", err)
	}
	if result == nil {
		t.Fatalf("RegisterUser() result is nil")
	}
	if result.Tokens.AccessToken != expectedPair.AccessToken {
		t.Fatalf("expected access token %q, got %q", expectedPair.AccessToken, result.Tokens.AccessToken)
	}
	user, err := repo.GetUserByEmail(ctx, "jane@example.com")
	if err != nil {
		t.Fatalf("user persisted not found: %v", err)
	}
	servicetest.WaitFor(t, func() bool { return email.VerificationSent })
	if user.HashedPassword == "" {
		t.Fatalf("expected hashed password to be stored")
	}
}

func TestAuthService_RegisterUser_DuplicateEmail(t *testing.T) {
	svc, _, _, _ := servicetest.NewTestAuthService()
	ctx := context.Background()
	_, err := svc.RegisterUser(ctx, service.RegisterParams{
		Email:       "jane@example.com",
		Username:    "jane",
		Password:    "Str0ng!Pass",
		DisplayName: "Jane Doe",
	})
	if err != nil {
		t.Fatalf("unexpected error registering user: %v", err)
	}
	_, err = svc.RegisterUser(ctx, service.RegisterParams{
		Email:       "jane@example.com",
		Username:    "another",
		Password:    "Str0ng!Pass",
		DisplayName: "Jane Duplicate",
	})
	if err == nil {
		t.Fatalf("expected error for duplicate email")
	}
	if !errors.Is(err, common.ErrUserAlreadyExists) {
		t.Fatalf("expected ErrUserAlreadyExists, got %v", err)
	}
}

func TestAuthService_Login_InvalidPassword(t *testing.T) {
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	ctx := context.Background()
	password := "Str0ng!Pass"
	_, err := svc.RegisterUser(ctx, service.RegisterParams{
		Email:       "jane@example.com",
		Username:    "jane",
		Password:    password,
		DisplayName: "Jane Doe",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	tokens.GenerateFunc = func(_, _, _, _ string) (*service.TokenPair, error) {
		return &service.TokenPair{
			AccessToken:  "access",
			RefreshToken: "refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			TokenType:    "Bearer",
		}, nil
	}
	_, err = svc.Login(ctx, service.LoginParams{
		Email:    "jane@example.com",
		Password: "WrongPass!1",
	})
	if err == nil {
		t.Fatalf("expected invalid credentials error")
	}
	if !errors.Is(err, common.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	user, _ := repo.GetUserByEmail(ctx, "jane@example.com")
	if user.LastLoginAt != nil {
		t.Fatalf("last login should not be updated on failed login")
	}
}

func TestAuthService_Login_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	if _, err := svc.RegisterUser(ctx, service.RegisterParams{
		Email:       "jane@example.com",
		Username:    "jane",
		Password:    "Str0ng!Pass",
		DisplayName: "Jane Doe",
	}); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	tokens.GenerateFunc = func(_, _, _, _ string) (*service.TokenPair, error) {
		return &service.TokenPair{
			AccessToken:  "login-access",
			RefreshToken: "login-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
		}, nil
	}

	result, err := svc.Login(ctx, service.LoginParams{
		Email:    "jane@example.com",
		Password: "Str0ng!Pass",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.Tokens.AccessToken != "login-access" {
		t.Fatalf("unexpected access token")
	}
	sessionHash := hashTestToken("login-refresh")
	if _, ok := repo.Sessions[sessionHash]; !ok {
		t.Fatalf("expected session stored")
	}
	if repo.Users["jane@example.com"].LastLoginAt == nil {
		t.Fatalf("expected last login timestamp set")
	}
}

func TestAuthService_Logout_RemovesSession(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := servicetest.NewTestAuthService()
	user := servicetest.AddUser(repo, t, "logout@example.com", true, "Hashed!Pass1")
	hashed := hashTestToken("refresh-token")
	repo.Sessions[hashed] = &repository.UserSession{
		ID:        uuid.New(),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	if err := svc.Logout(ctx, "refresh-token"); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, ok := repo.Sessions[hashed]; ok {
		t.Fatalf("session should be deleted")
	}
}

func TestAuthService_RefreshTokens_InvalidSession(t *testing.T) {
	ctx := context.Background()
	svc, repo, tokens, _ := servicetest.NewTestAuthService()
	user := servicetest.AddUser(repo, t, "refresh@example.com", true, "Hashed!Pass1")
	tokens.RefreshFunc = func(_ string) (*service.Claims, error) {
		return &service.Claims{UserID: user.ID.String()}, nil
	}

	_, err := svc.RefreshTokens(ctx, service.RefreshTokenParams{
		RefreshToken: "missing",
	})
	if err == nil || !errors.Is(err, common.ErrSessionNotFound) {
		t.Fatalf("expected session not found, got %v", err)
	}
}

func TestAuthService_ChangePassword_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := servicetest.NewTestAuthService()
	current := "Str0ng!Pass"
	hashed := servicetest.MustHash(t, current)
	user := servicetest.AddUser(repo, t, "changepass@example.com", true, hashed)
	repo.Sessions["session"] = &repository.UserSession{
		ID:        uuid.New(),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	if err := svc.ChangePassword(ctx, user.ID.String(), current, "NewPass!2"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if repo.Users[user.Email].HashedPassword == hashed {
		t.Fatalf("password hash should change")
	}
	if len(repo.Sessions) != 0 {
		t.Fatalf("sessions should be cleared")
	}
}

func TestAuthService_ResetPassword_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := servicetest.NewTestAuthService()
	hashed := servicetest.MustHash(t, "OldPass!1")
	user := servicetest.AddUser(repo, t, "reset@example.com", true, hashed)
	token := "reset-token"
	repo.Tokens[hashTestToken(token)] = &repository.UserToken{
		TokenHash: hashTestToken(token),
		UserID:    user.ID,
		Type:      tokenTypePasswordReset,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	repo.Sessions["session"] = &repository.UserSession{
		ID:        uuid.New(),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	if err := svc.ResetPassword(ctx, token, "NewPass!2"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if repo.Users[user.Email].HashedPassword == hashed {
		t.Fatalf("password not updated")
	}
	if len(repo.Tokens) != 0 {
		t.Fatalf("token should be deleted")
	}
	if len(repo.Sessions) != 0 {
		t.Fatalf("sessions should be cleared")
	}
}

func TestAuthService_VerifyEmail_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, email := servicetest.NewTestAuthService()
	user := servicetest.AddUser(repo, t, "verify@example.com", true, servicetest.MustHash(t, "Str0ng!Pass"))

	token := "verify-token"
	hash := hashTestToken(token)
	repo.Tokens[hash] = &repository.UserToken{
		TokenHash: hash,
		UserID:    user.ID,
		Type:      tokenTypeEmailVerification,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	userID, err := svc.VerifyEmail(ctx, token)
	if err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	if userID != user.ID {
		t.Fatalf("unexpected user id")
	}
	if repo.Users[user.Email].EmailVerifiedAt == nil {
		t.Fatalf("email not marked verified")
	}
	if _, ok := repo.Tokens[hash]; ok {
		t.Fatalf("token should be deleted")
	}
	servicetest.WaitFor(t, func() bool { return email.WelcomeSent })
}

func TestAuthService_ResendVerificationEmail(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, email := servicetest.NewTestAuthService()
	user := servicetest.AddUser(repo, t, "resend@example.com", true, servicetest.MustHash(t, "Str0ng!Pass"))

	result, err := svc.ResendVerificationEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("ResendVerificationEmail: %v", err)
	}
	if result == nil || result.AlreadyVerified {
		t.Fatalf("expected resend to proceed")
	}
	servicetest.WaitFor(t, func() bool { return email.VerificationSent })
	if len(repo.Tokens) == 0 {
		t.Fatalf("verification token not stored")
	}

	now := time.Now()
	user.EmailVerifiedAt = &now
	email.VerificationSent = false
	result, err = svc.ResendVerificationEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("ResendVerificationEmail verified: %v", err)
	}
	if !result.AlreadyVerified {
		t.Fatalf("expected AlreadyVerified flag")
	}
	if email.VerificationSent {
		t.Fatalf("should not send email for verified user")
	}
}

func TestAuthService_RefreshTokens_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, tokens, _ := servicetest.NewTestAuthService()

	user, err := repo.CreateUser(ctx, "jane@example.com", "jane", "hashed", "Jane")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	user.IsActive = true
	repo.Users[user.Email] = user

	session := &repository.UserSession{
		ID:                 uuid.New(),
		UserID:             user.ID,
		HashedRefreshToken: hashTestToken("refresh-token"),
		ExpiresAt:          time.Now().Add(time.Hour),
	}
	repo.Sessions[session.HashedRefreshToken] = session

	tokens.RefreshFunc = func(token string) (*service.Claims, error) {
		if token != "refresh-token" {
			return nil, errors.New("unexpected token")
		}
		return &service.Claims{UserID: user.ID.String()}, nil
	}
	tokens.GenerateFunc = func(_, _, _, _ string) (*service.TokenPair, error) {
		return &service.TokenPair{
			AccessToken:  "access-new",
			RefreshToken: "refresh-new",
			ExpiresAt:    time.Now().Add(2 * time.Hour),
		}, nil
	}

	res, err := svc.RefreshTokens(ctx, service.RefreshTokenParams{
		RefreshToken: "refresh-token",
	})
	if err != nil {
		t.Fatalf("RefreshTokens: %v", err)
	}
	if res.AccessToken != "access-new" {
		t.Fatalf("unexpected access token %s", res.AccessToken)
	}
	if _, ok := repo.Sessions[hashTestToken("refresh-token")]; ok {
		t.Fatalf("old session should be deleted")
	}
	if _, ok := repo.Sessions[hashTestToken("refresh-new")]; !ok {
		t.Fatalf("new session should be created")
	}
}

func TestAuthService_RequestPasswordReset(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, email := servicetest.NewTestAuthService()
	_, err := svc.RegisterUser(ctx, service.RegisterParams{
		Email:       "jane@example.com",
		Username:    "jane",
		Password:    "Str0ng!Pass",
		DisplayName: "Jane Doe",
	})
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	if err := svc.RequestPasswordReset(ctx, "jane@example.com"); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	servicetest.WaitFor(t, func() bool { return email.ResetSent })
	if len(repo.Tokens) == 0 {
		t.Fatalf("expected token to be stored")
	}
}

func hashTestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
