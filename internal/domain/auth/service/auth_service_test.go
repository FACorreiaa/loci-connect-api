package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/repository"
)

func TestAuthService_RegisterUser_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, tokens, email := newTestAuthService()

	expectedPair := &TokenPair{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour),
		TokenType:    "Bearer",
	}
	tokens.generateFunc = func(userID, email, username, role string) (*TokenPair, error) {
		return expectedPair, nil
	}

	result, err := svc.RegisterUser(ctx, RegisterParams{
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
	waitFor(t, func() bool { return email.verificationSent })
	if user.HashedPassword == "" {
		t.Fatalf("expected hashed password to be stored")
	}
}

func TestAuthService_RegisterUser_DuplicateEmail(t *testing.T) {
	svc, _, _, _ := newTestAuthService()
	ctx := context.Background()
	_, err := svc.RegisterUser(ctx, RegisterParams{
		Email:       "jane@example.com",
		Username:    "jane",
		Password:    "Str0ng!Pass",
		DisplayName: "Jane Doe",
	})
	if err != nil {
		t.Fatalf("unexpected error registering user: %v", err)
	}
	_, err = svc.RegisterUser(ctx, RegisterParams{
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
	svc, repo, tokens, _ := newTestAuthService()
	ctx := context.Background()
	password := "Str0ng!Pass"
	_, err := svc.RegisterUser(ctx, RegisterParams{
		Email:       "jane@example.com",
		Username:    "jane",
		Password:    password,
		DisplayName: "Jane Doe",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	tokens.generateFunc = func(userID, email, username, role string) (*TokenPair, error) {
		return &TokenPair{
			AccessToken:  "access",
			RefreshToken: "refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			TokenType:    "Bearer",
		}, nil
	}
	_, err = svc.Login(ctx, LoginParams{
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
	svc, repo, tokens, _ := newTestAuthService()
	if _, err := svc.RegisterUser(ctx, RegisterParams{
		Email:       "jane@example.com",
		Username:    "jane",
		Password:    "Str0ng!Pass",
		DisplayName: "Jane Doe",
	}); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	tokens.generateFunc = func(userID, email, username, role string) (*TokenPair, error) {
		return &TokenPair{
			AccessToken:  "login-access",
			RefreshToken: "login-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
		}, nil
	}

	result, err := svc.Login(ctx, LoginParams{
		Email:    "jane@example.com",
		Password: "Str0ng!Pass",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.Tokens.AccessToken != "login-access" {
		t.Fatalf("unexpected access token")
	}
	sessionHash := hashToken("login-refresh")
	if _, ok := repo.sessions[sessionHash]; !ok {
		t.Fatalf("expected session stored")
	}
	if repo.users["jane@example.com"].LastLoginAt == nil {
		t.Fatalf("expected last login timestamp set")
	}
}

func TestAuthService_Logout_RemovesSession(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := newTestAuthService()
	user := addUser(repo, t, "logout@example.com", true, "Hashed!Pass1")
	hashed := hashToken("refresh-token")
	repo.sessions[hashed] = &repository.UserSession{
		ID:        uuid.New(),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	if err := svc.Logout(ctx, "refresh-token"); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, ok := repo.sessions[hashed]; ok {
		t.Fatalf("session should be deleted")
	}
}

func TestAuthService_RefreshTokens_InvalidSession(t *testing.T) {
	ctx := context.Background()
	svc, repo, tokens, _ := newTestAuthService()
	user := addUser(repo, t, "refresh@example.com", true, "Hashed!Pass1")
	tokens.refreshFunc = func(token string) (*Claims, error) {
		return &Claims{UserID: user.ID.String()}, nil
	}

	_, err := svc.RefreshTokens(ctx, RefreshTokenParams{
		RefreshToken: "missing",
	})
	if err == nil || !errors.Is(err, common.ErrSessionNotFound) {
		t.Fatalf("expected session not found, got %v", err)
	}
}

func TestAuthService_ChangePassword_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := newTestAuthService()
	current := "Str0ng!Pass"
	hashed := mustHash(t, current)
	user := addUser(repo, t, "changepass@example.com", true, hashed)
	repo.sessions["session"] = &repository.UserSession{
		ID:        uuid.New(),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	if err := svc.ChangePassword(ctx, user.ID.String(), current, "NewPass!2"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if repo.users[user.Email].HashedPassword == hashed {
		t.Fatalf("password hash should change")
	}
	if len(repo.sessions) != 0 {
		t.Fatalf("sessions should be cleared")
	}
}

func TestAuthService_ResetPassword_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := newTestAuthService()
	hashed := mustHash(t, "OldPass!1")
	user := addUser(repo, t, "reset@example.com", true, hashed)
	token := "reset-token"
	repo.tokens[hashToken(token)] = &repository.UserToken{
		TokenHash: hashToken(token),
		UserID:    user.ID,
		Type:      tokenTypePasswordReset,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	repo.sessions["session"] = &repository.UserSession{
		ID:        uuid.New(),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	if err := svc.ResetPassword(ctx, token, "NewPass!2"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if repo.users[user.Email].HashedPassword == hashed {
		t.Fatalf("password not updated")
	}
	if len(repo.tokens) != 0 {
		t.Fatalf("token should be deleted")
	}
	if len(repo.sessions) != 0 {
		t.Fatalf("sessions should be cleared")
	}
}

func TestAuthService_VerifyEmail_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, email := newTestAuthService()
	user := addUser(repo, t, "verify@example.com", true, mustHash(t, "Str0ng!Pass"))

	token := "verify-token"
	hash := hashToken(token)
	repo.tokens[hash] = &repository.UserToken{
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
	if repo.users[user.Email].EmailVerifiedAt == nil {
		t.Fatalf("email not marked verified")
	}
	if _, ok := repo.tokens[hash]; ok {
		t.Fatalf("token should be deleted")
	}
	waitFor(t, func() bool { return email.welcomeSent })
}

func TestAuthService_ResendVerificationEmail(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, email := newTestAuthService()
	user := addUser(repo, t, "resend@example.com", true, mustHash(t, "Str0ng!Pass"))

	result, err := svc.ResendVerificationEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("ResendVerificationEmail: %v", err)
	}
	if result == nil || result.AlreadyVerified {
		t.Fatalf("expected resend to proceed")
	}
	waitFor(t, func() bool { return email.verificationSent })
	if len(repo.tokens) == 0 {
		t.Fatalf("verification token not stored")
	}

	now := time.Now()
	user.EmailVerifiedAt = &now
	email.verificationSent = false
	result, err = svc.ResendVerificationEmail(ctx, user.Email)
	if err != nil {
		t.Fatalf("ResendVerificationEmail verified: %v", err)
	}
	if !result.AlreadyVerified {
		t.Fatalf("expected AlreadyVerified flag")
	}
	if email.verificationSent {
		t.Fatalf("should not send email for verified user")
	}
}

func TestAuthService_RefreshTokens_Success(t *testing.T) {
	ctx := context.Background()
	svc, repo, tokens, _ := newTestAuthService()

	user, err := repo.CreateUser(ctx, "jane@example.com", "jane", "hashed", "Jane")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	user.IsActive = true
	repo.users[user.Email] = user

	session := &repository.UserSession{
		ID:                 uuid.New(),
		UserID:             user.ID,
		HashedRefreshToken: hashToken("refresh-token"),
		ExpiresAt:          time.Now().Add(time.Hour),
	}
	repo.sessions[session.HashedRefreshToken] = session

	tokens.refreshFunc = func(token string) (*Claims, error) {
		if token != "refresh-token" {
			return nil, errors.New("unexpected token")
		}
		return &Claims{UserID: user.ID.String()}, nil
	}
	tokens.generateFunc = func(userID, email, username, role string) (*TokenPair, error) {
		return &TokenPair{
			AccessToken:  "access-new",
			RefreshToken: "refresh-new",
			ExpiresAt:    time.Now().Add(2 * time.Hour),
		}, nil
	}

	res, err := svc.RefreshTokens(ctx, RefreshTokenParams{
		RefreshToken: "refresh-token",
	})
	if err != nil {
		t.Fatalf("RefreshTokens: %v", err)
	}
	if res.AccessToken != "access-new" {
		t.Fatalf("unexpected access token %s", res.AccessToken)
	}
	if _, ok := repo.sessions[hashToken("refresh-token")]; ok {
		t.Fatalf("old session should be deleted")
	}
	if _, ok := repo.sessions[hashToken("refresh-new")]; !ok {
		t.Fatalf("new session should be created")
	}
}

func TestAuthService_RequestPasswordReset(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, email := newTestAuthService()
	_, err := svc.RegisterUser(ctx, RegisterParams{
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
	waitFor(t, func() bool { return email.resetSent })
	if len(repo.tokens) == 0 {
		t.Fatalf("expected token to be stored")
	}
}

// --- Test helpers ---

type mockTokenManager struct {
	generateFunc func(userID, email, username, role string) (*TokenPair, error)
	accessFunc   func(token string) (*Claims, error)
	refreshFunc  func(token string) (*Claims, error)
}

func (m *mockTokenManager) GenerateTokenPair(userID, email, username, role string) (*TokenPair, error) {
	if m.generateFunc != nil {
		return m.generateFunc(userID, email, username, role)
	}
	return &TokenPair{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (m *mockTokenManager) ValidateAccessToken(tokenString string) (*Claims, error) {
	if m.accessFunc != nil {
		return m.accessFunc(tokenString)
	}
	return &Claims{UserID: "user"}, nil
}

func (m *mockTokenManager) ValidateRefreshToken(tokenString string) (*Claims, error) {
	if m.refreshFunc != nil {
		return m.refreshFunc(tokenString)
	}
	return &Claims{UserID: "user"}, nil
}

type mockEmailSender struct {
	verificationSent bool
	resetSent        bool
	welcomeSent      bool
}

func (m *mockEmailSender) SendVerificationEmail(toEmail, toName, token string) error {
	m.verificationSent = true
	return nil
}

func (m *mockEmailSender) SendPasswordResetEmail(toEmail, toName, token string) error {
	m.resetSent = true
	return nil
}

func (m *mockEmailSender) SendWelcomeEmail(toEmail, toName string) error {
	m.welcomeSent = true
	return nil
}

type mockAuthRepo struct {
	users    map[string]*repository.User
	sessions map[string]*repository.UserSession
	tokens   map[string]*repository.UserToken
}

func newMockAuthRepo() *mockAuthRepo {
	return &mockAuthRepo{
		users:    make(map[string]*repository.User),
		sessions: make(map[string]*repository.UserSession),
		tokens:   make(map[string]*repository.UserToken),
	}
}

func (m *mockAuthRepo) CreateUser(ctx context.Context, email, username, hashedPassword, displayName string) (*repository.User, error) {
	if _, exists := m.users[email]; exists {
		return nil, common.ErrUserAlreadyExists
	}
	user := &repository.User{
		ID:             uuid.New(),
		Email:          email,
		Username:       username,
		HashedPassword: hashedPassword,
		DisplayName:    displayName,
		Role:           "member",
		IsActive:       true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	m.users[email] = user
	return cloneUser(user), nil
}

func (m *mockAuthRepo) GetUserByEmail(ctx context.Context, email string) (*repository.User, error) {
	user, ok := m.users[email]
	if !ok {
		return nil, common.ErrUserNotFound
	}
	return cloneUser(user), nil
}

func (m *mockAuthRepo) GetUserByID(ctx context.Context, userID uuid.UUID) (*repository.User, error) {
	for _, user := range m.users {
		if user.ID == userID {
			return cloneUser(user), nil
		}
	}
	return nil, common.ErrUserNotFound
}

func (m *mockAuthRepo) UpdateLastLogin(ctx context.Context, userID uuid.UUID) error {
	for _, user := range m.users {
		if user.ID == userID {
			now := time.Now()
			user.LastLoginAt = &now
			return nil
		}
	}
	return common.ErrUserNotFound
}

func (m *mockAuthRepo) CreateUserSession(ctx context.Context, userID uuid.UUID, hashedRefreshToken, userAgent, clientIP string, expiresAt time.Time) (*repository.UserSession, error) {
	session := &repository.UserSession{
		ID:                 uuid.New(),
		UserID:             userID,
		HashedRefreshToken: hashedRefreshToken,
		UserAgent:          &userAgent,
		ClientIP:           &clientIP,
		ExpiresAt:          expiresAt,
		CreatedAt:          time.Now(),
	}
	m.sessions[hashedRefreshToken] = session
	return session, nil
}

func (m *mockAuthRepo) GetUserSessionByToken(ctx context.Context, hashedToken string) (*repository.UserSession, error) {
	session, ok := m.sessions[hashedToken]
	if !ok || session.ExpiresAt.Before(time.Now()) {
		return nil, common.ErrSessionNotFound
	}
	return session, nil
}

func (m *mockAuthRepo) DeleteUserSession(ctx context.Context, hashedToken string) error {
	delete(m.sessions, hashedToken)
	return nil
}

func (m *mockAuthRepo) DeleteAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	for token, session := range m.sessions {
		if session.UserID == userID {
			delete(m.sessions, token)
		}
	}
	return nil
}

func (m *mockAuthRepo) CreateUserToken(ctx context.Context, userID uuid.UUID, tokenHash, tokenType string, expiresAt time.Time) error {
	m.tokens[tokenHash] = &repository.UserToken{
		TokenHash: tokenHash,
		UserID:    userID,
		Type:      tokenType,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	return nil
}

func (m *mockAuthRepo) GetUserTokenByHash(ctx context.Context, tokenHash, tokenType string) (*repository.UserToken, error) {
	token, ok := m.tokens[tokenHash]
	if !ok || token.Type != tokenType || token.ExpiresAt.Before(time.Now()) {
		return nil, common.ErrInvalidToken
	}
	return token, nil
}

func (m *mockAuthRepo) DeleteUserToken(ctx context.Context, tokenHash string) error {
	delete(m.tokens, tokenHash)
	return nil
}

func (m *mockAuthRepo) VerifyEmail(ctx context.Context, userID uuid.UUID) error {
	for _, user := range m.users {
		if user.ID == userID {
			now := time.Now()
			user.EmailVerifiedAt = &now
			return nil
		}
	}
	return common.ErrUserNotFound
}

func (m *mockAuthRepo) UpdatePassword(ctx context.Context, userID uuid.UUID, hashedPassword string) error {
	for _, user := range m.users {
		if user.ID == userID {
			user.HashedPassword = hashedPassword
			return nil
		}
	}
	return common.ErrUserNotFound
}

func (m *mockAuthRepo) CreateOrUpdateOAuthIdentity(ctx context.Context, providerName, providerUserID string, userID uuid.UUID, accessToken, refreshToken *string) error {
	return nil
}

func (m *mockAuthRepo) GetUserByOAuthIdentity(ctx context.Context, providerName, providerUserID string) (*repository.User, error) {
	return nil, common.ErrUserNotFound
}

func newTestAuthService() (*AuthService, *mockAuthRepo, *mockTokenManager, *mockEmailSender) {
	repo := newMockAuthRepo()
	tokenManager := &mockTokenManager{}
	emailSender := &mockEmailSender{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	service := NewAuthService(repo, tokenManager, emailSender, logger, time.Hour)
	return service, repo, tokenManager, emailSender
}

// Utility helpers

func cloneUser(u *repository.User) *repository.User {
	if u == nil {
		return nil
	}
	clone := *u
	return &clone
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met before timeout")
}

func mustHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return hash
}

func addUser(repo *mockAuthRepo, t *testing.T, email string, active bool, hashedPassword string) *repository.User {
	t.Helper()
	user := &repository.User{
		ID:             uuid.New(),
		Email:          email,
		Username:       "user",
		HashedPassword: hashedPassword,
		DisplayName:    "Test User",
		Role:           "member",
		IsActive:       active,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	repo.users[email] = user
	return user
}
