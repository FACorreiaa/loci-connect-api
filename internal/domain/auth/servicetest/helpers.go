package servicetest

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/repository"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/service"
)

// MockTokenManager implements TokenManager for tests.
type MockTokenManager struct {
	GenerateFunc func(userID, email, username, role string) (*service.TokenPair, error)
	AccessFunc   func(token string) (*service.Claims, error)
	RefreshFunc  func(token string) (*service.Claims, error)
}

func (m *MockTokenManager) GenerateTokenPair(userID, email, username, role string) (*service.TokenPair, error) {
	if m.GenerateFunc != nil {
		return m.GenerateFunc(userID, email, username, role)
	}
	return &service.TokenPair{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (m *MockTokenManager) ValidateAccessToken(tokenString string) (*service.Claims, error) {
	if m.AccessFunc != nil {
		return m.AccessFunc(tokenString)
	}
	return &service.Claims{UserID: "user"}, nil
}

func (m *MockTokenManager) ValidateRefreshToken(tokenString string) (*service.Claims, error) {
	if m.RefreshFunc != nil {
		return m.RefreshFunc(tokenString)
	}
	return &service.Claims{UserID: "user"}, nil
}

// MockEmailSender captures sent emails for assertions.
type MockEmailSender struct {
	VerificationSent bool
	ResetSent        bool
	WelcomeSent      bool
}

func (m *MockEmailSender) SendVerificationEmail(_, _, _ string) error {
	m.VerificationSent = true
	return nil
}

func (m *MockEmailSender) SendPasswordResetEmail(_, _, _ string) error {
	m.ResetSent = true
	return nil
}

func (m *MockEmailSender) SendWelcomeEmail(_, _ string) error {
	m.WelcomeSent = true
	return nil
}

// MockAuthRepo is an in-memory AuthRepository.
type MockAuthRepo struct {
	Users    map[string]*repository.User
	Sessions map[string]*repository.UserSession
	Tokens   map[string]*repository.UserToken
}

func NewMockAuthRepo() *MockAuthRepo {
	return &MockAuthRepo{
		Users:    make(map[string]*repository.User),
		Sessions: make(map[string]*repository.UserSession),
		Tokens:   make(map[string]*repository.UserToken),
	}
}

func (m *MockAuthRepo) CreateUser(_ context.Context, email, username, hashedPassword, displayName string) (*repository.User, error) {
	if _, exists := m.Users[email]; exists {
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
	m.Users[email] = user
	return CloneUser(user), nil
}

func (m *MockAuthRepo) GetUserByEmail(_ context.Context, email string) (*repository.User, error) {
	user, ok := m.Users[email]
	if !ok {
		return nil, common.ErrUserNotFound
	}
	return CloneUser(user), nil
}

func (m *MockAuthRepo) GetUserByID(_ context.Context, userID uuid.UUID) (*repository.User, error) {
	for _, user := range m.Users {
		if user.ID == userID {
			return CloneUser(user), nil
		}
	}
	return nil, common.ErrUserNotFound
}

func (m *MockAuthRepo) UpdateLastLogin(_ context.Context, userID uuid.UUID) error {
	for _, user := range m.Users {
		if user.ID == userID {
			now := time.Now()
			user.LastLoginAt = &now
			return nil
		}
	}
	return common.ErrUserNotFound
}

func (m *MockAuthRepo) CreateUserSession(_ context.Context, userID uuid.UUID, hashedRefreshToken, userAgent, clientIP string, expiresAt time.Time) (*repository.UserSession, error) {
	session := &repository.UserSession{
		ID:                 uuid.New(),
		UserID:             userID,
		HashedRefreshToken: hashedRefreshToken,
		UserAgent:          &userAgent,
		ClientIP:           &clientIP,
		ExpiresAt:          expiresAt,
		CreatedAt:          time.Now(),
	}
	m.Sessions[hashedRefreshToken] = session
	return session, nil
}

func (m *MockAuthRepo) GetUserSessionByToken(_ context.Context, hashedToken string) (*repository.UserSession, error) {
	session, ok := m.Sessions[hashedToken]
	if !ok || session.ExpiresAt.Before(time.Now()) {
		return nil, common.ErrSessionNotFound
	}
	return session, nil
}

func (m *MockAuthRepo) DeleteUserSession(_ context.Context, hashedToken string) error {
	delete(m.Sessions, hashedToken)
	return nil
}

func (m *MockAuthRepo) DeleteAllUserSessions(_ context.Context, userID uuid.UUID) error {
	for token, session := range m.Sessions {
		if session.UserID == userID {
			delete(m.Sessions, token)
		}
	}
	return nil
}

func (m *MockAuthRepo) CreateUserToken(_ context.Context, userID uuid.UUID, tokenHash, tokenType string, expiresAt time.Time) error {
	m.Tokens[tokenHash] = &repository.UserToken{
		TokenHash: tokenHash,
		UserID:    userID,
		Type:      tokenType,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	return nil
}

func (m *MockAuthRepo) GetUserTokenByHash(_ context.Context, tokenHash, tokenType string) (*repository.UserToken, error) {
	token, ok := m.Tokens[tokenHash]
	if !ok || token.Type != tokenType || token.ExpiresAt.Before(time.Now()) {
		return nil, common.ErrInvalidToken
	}
	return token, nil
}

func (m *MockAuthRepo) DeleteUserToken(_ context.Context, tokenHash string) error {
	delete(m.Tokens, tokenHash)
	return nil
}

func (m *MockAuthRepo) VerifyEmail(_ context.Context, userID uuid.UUID) error {
	for _, user := range m.Users {
		if user.ID == userID {
			now := time.Now()
			user.EmailVerifiedAt = &now
			return nil
		}
	}
	return common.ErrUserNotFound
}

func (m *MockAuthRepo) UpdatePassword(_ context.Context, userID uuid.UUID, hashedPassword string) error {
	for _, user := range m.Users {
		if user.ID == userID {
			user.HashedPassword = hashedPassword
			return nil
		}
	}
	return common.ErrUserNotFound
}

func (m *MockAuthRepo) CreateOrUpdateOAuthIdentity(_ context.Context, _, _ string, _ uuid.UUID, _, _ *string) error {
	return nil
}

func (m *MockAuthRepo) GetUserByOAuthIdentity(_ context.Context, _, _ string) (*repository.User, error) {
	return nil, common.ErrUserNotFound
}

// NewTestAuthService bundles the mocks with a configured AuthService.
func NewTestAuthService() (*service.AuthService, *MockAuthRepo, *MockTokenManager, *MockEmailSender) {
	repo := NewMockAuthRepo()
	tokenManager := &MockTokenManager{}
	emailSender := &MockEmailSender{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	authService := service.NewAuthService(repo, tokenManager, emailSender, logger, time.Hour)
	return authService, repo, tokenManager, emailSender
}

// CloneUser returns a deep copy of the provided user.
func CloneUser(u *repository.User) *repository.User {
	if u == nil {
		return nil
	}
	clone := *u
	return &clone
}

// WaitFor waits for a condition or times out.
func WaitFor(t *testing.T, condition func() bool) {
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

// MustHash hashes a password for tests.
func MustHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := service.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return hash
}

// AddUser inserts a user into the mock repo.
func AddUser(repo *MockAuthRepo, t *testing.T, email string, active bool, hashedPassword string) *repository.User {
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
	repo.Users[email] = user
	return user
}
