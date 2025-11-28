package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/repository"
)

const (
	tokenTypeEmailVerification = "email_verification"
	tokenTypePasswordReset     = "password_reset"

	defaultSessionTTL = 30 * 24 * time.Hour
)

var (
	// ErrAccountInactive is returned when a user has been disabled.
	ErrAccountInactive = errors.New("account is deactivated")
)

// SessionMetadata captures client information useful for audit trails.
type SessionMetadata struct {
	UserAgent string
	ClientIP  string
}

// RegisterParams contains the required data for user registration.
type RegisterParams struct {
	Email       string
	Username    string
	Password    string
	DisplayName string
	Metadata    SessionMetadata
}

// RegisterResult contains the data returned after registration.
type RegisterResult struct {
	User                      *repository.User
	Tokens                    *TokenPair
	EmailVerificationRequired bool
}

// LoginParams represents the payload for a login attempt.
type LoginParams struct {
	Email    string
	Password string
	Metadata SessionMetadata
}

// LoginResult is produced after a successful login.
type LoginResult struct {
	User   *repository.User
	Tokens *TokenPair
}

// RefreshTokenParams contains the data needed to refresh tokens.
type RefreshTokenParams struct {
	RefreshToken string
	Metadata     SessionMetadata
}

// ResendVerificationResult communicates whether the user was already verified.
type ResendVerificationResult struct {
	AlreadyVerified bool
}

// AuthService coordinates AUTH business logic.
type AuthService struct {
	repo         repository.AuthRepository
	tokenManager TokenManager
	emailService EmailSender
	sessionTTL   time.Duration
	logger       *slog.Logger
}

// NewAuthService constructs a new AuthService.
func NewAuthService(
	repo repository.AuthRepository,
	tokenManager TokenManager,
	emailService EmailSender,
	logger *slog.Logger,
	sessionTTL time.Duration,
) *AuthService {
	if sessionTTL <= 0 {
		sessionTTL = defaultSessionTTL
	}

	return &AuthService{
		repo:         repo,
		tokenManager: tokenManager,
		emailService: emailService,
		sessionTTL:   sessionTTL,
		logger:       logger,
	}
}

// RegisterUser creates a new user account, issues tokens, and sends verification email.
func (s *AuthService) RegisterUser(ctx context.Context, params RegisterParams) (*RegisterResult, error) {
	if err := ValidatePassword(params.Password); err != nil {
		return nil, err
	}

	if _, err := s.repo.GetUserByEmail(ctx, params.Email); err == nil {
		return nil, common.ErrUserAlreadyExists
	} else if !errors.Is(err, common.ErrUserNotFound) {
		return nil, err
	}

	hashedPassword, err := HashPassword(params.Password)
	if err != nil {
		return nil, err
	}

	user, err := s.repo.CreateUser(ctx, params.Email, params.Username, hashedPassword, params.DisplayName)
	if err != nil {
		return nil, err
	}

	tokens, err := s.tokenManager.GenerateTokenPair(user.ID.String(), user.Email, user.Username, user.Role)
	if err != nil {
		return nil, err
	}

	if err := s.createSession(ctx, user.ID, tokens.RefreshToken, params.Metadata); err != nil {
		return nil, err
	}

	if err := s.sendEmailVerification(ctx, user); err != nil {
		return nil, err
	}

	return &RegisterResult{
		User:                      user,
		Tokens:                    tokens,
		EmailVerificationRequired: true,
	}, nil
}

// Login authenticates a user against stored credentials.
func (s *AuthService) Login(ctx context.Context, params LoginParams) (*LoginResult, error) {
	user, err := s.repo.GetUserByEmail(ctx, params.Email)
	if err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, ErrAccountInactive
	}

	if !ComparePassword(user.HashedPassword, params.Password) {
		return nil, common.ErrInvalidCredentials
	}

	tokens, err := s.tokenManager.GenerateTokenPair(user.ID.String(), user.Email, user.Username, user.Role)
	if err != nil {
		return nil, err
	}

	if err := s.createSession(ctx, user.ID, tokens.RefreshToken, params.Metadata); err != nil {
		return nil, err
	}

	if err := s.repo.UpdateLastLogin(ctx, user.ID); err != nil && s.logger != nil {
		s.logger.Warn("failed to update last login", "error", err)
	}

	return &LoginResult{
		User:   user,
		Tokens: tokens,
	}, nil
}

// Logout removes the refresh token session.
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return fmt.Errorf("refresh token required")
	}

	hashedToken := hashToken(refreshToken)
	if err := s.repo.DeleteUserSession(ctx, hashedToken); err != nil && !errors.Is(err, common.ErrSessionNotFound) {
		return err
	}
	return nil
}

// RefreshTokens validates the refresh token and issues a new pair.
func (s *AuthService) RefreshTokens(ctx context.Context, params RefreshTokenParams) (*TokenPair, error) {
	claims, err := s.tokenManager.ValidateRefreshToken(params.RefreshToken)
	if err != nil {
		return nil, err
	}

	hashedToken := hashToken(params.RefreshToken)
	if _, err := s.repo.GetUserSessionByToken(ctx, hashedToken); err != nil {
		return nil, err
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, err
	}

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, ErrAccountInactive
	}

	_ = s.repo.DeleteUserSession(ctx, hashedToken)

	tokens, err := s.tokenManager.GenerateTokenPair(user.ID.String(), user.Email, user.Username, user.Role)
	if err != nil {
		return nil, err
	}

	if err := s.createSession(ctx, user.ID, tokens.RefreshToken, params.Metadata); err != nil {
		return nil, err
	}

	return tokens, nil
}

// ValidateAccessToken validates an access token and returns its claims.
func (s *AuthService) ValidateAccessToken(_ context.Context, accessToken string) (*Claims, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("access token required")
	}
	return s.tokenManager.ValidateAccessToken(accessToken)
}

// RequestPasswordReset kicks off the reset workflow.
func (s *AuthService) RequestPasswordReset(ctx context.Context, email string) error {
	if email == "" {
		return fmt.Errorf("email required")
	}

	user, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, common.ErrUserNotFound) {
			return nil
		}
		return err
	}

	resetToken, err := GeneratePasswordResetToken()
	if err != nil {
		return err
	}

	if err := s.repo.CreateUserToken(ctx, user.ID, hashToken(resetToken), tokenTypePasswordReset, time.Now().Add(time.Hour)); err != nil {
		return err
	}

	if s.emailService != nil {
		go s.emailService.SendPasswordResetEmail(user.Email, user.DisplayName, resetToken)
	}

	return nil
}

// ResetPassword verifies a reset token and changes the password.
func (s *AuthService) ResetPassword(ctx context.Context, resetToken, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}

	hashedToken := hashToken(resetToken)
	userToken, err := s.repo.GetUserTokenByHash(ctx, hashedToken, tokenTypePasswordReset)
	if err != nil {
		return err
	}

	hashedPassword, err := HashPassword(newPassword)
	if err != nil {
		return err
	}

	if err := s.repo.UpdatePassword(ctx, userToken.UserID, hashedPassword); err != nil {
		return err
	}

	_ = s.repo.DeleteUserToken(ctx, hashedToken)
	_ = s.repo.DeleteAllUserSessions(ctx, userToken.UserID)

	return nil
}

// ChangePassword changes the password for an authenticated user.
func (s *AuthService) ChangePassword(ctx context.Context, userID string, currentPassword, newPassword string) error {
	if userID == "" {
		return fmt.Errorf("user id required")
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return err
	}

	user, err := s.repo.GetUserByID(ctx, userUUID)
	if err != nil {
		return err
	}

	if !ComparePassword(user.HashedPassword, currentPassword) {
		return common.ErrInvalidCredentials
	}

	if err := ValidatePassword(newPassword); err != nil {
		return err
	}

	hashedPassword, err := HashPassword(newPassword)
	if err != nil {
		return err
	}

	if err := s.repo.UpdatePassword(ctx, userUUID, hashedPassword); err != nil {
		return err
	}

	_ = s.repo.DeleteAllUserSessions(ctx, userUUID)
	return nil
}

// VerifyEmail validates the verification token.
func (s *AuthService) VerifyEmail(ctx context.Context, verificationToken string) (uuid.UUID, error) {
	if verificationToken == "" {
		return uuid.Nil, fmt.Errorf("verification token required")
	}

	hashedToken := hashToken(verificationToken)
	userToken, err := s.repo.GetUserTokenByHash(ctx, hashedToken, tokenTypeEmailVerification)
	if err != nil {
		return uuid.Nil, err
	}

	if err := s.repo.VerifyEmail(ctx, userToken.UserID); err != nil {
		return uuid.Nil, err
	}

	_ = s.repo.DeleteUserToken(ctx, hashedToken)

	if s.emailService != nil {
		if user, err := s.repo.GetUserByID(ctx, userToken.UserID); err == nil {
			go s.emailService.SendWelcomeEmail(user.Email, user.DisplayName)
		}
	}

	return userToken.UserID, nil
}

// ResendVerificationEmail sends a new verification email when necessary.
func (s *AuthService) ResendVerificationEmail(ctx context.Context, email string) (*ResendVerificationResult, error) {
	if email == "" {
		return nil, fmt.Errorf("email required")
	}

	user, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, common.ErrUserNotFound) {
			return &ResendVerificationResult{}, nil
		}
		return nil, err
	}

	if user.EmailVerifiedAt != nil {
		return &ResendVerificationResult{AlreadyVerified: true}, nil
	}

	if err := s.sendEmailVerification(ctx, user); err != nil {
		return nil, err
	}

	return &ResendVerificationResult{}, nil
}

func (s *AuthService) createSession(ctx context.Context, userID uuid.UUID, refreshToken string, meta SessionMetadata) error {
	userAgent := meta.UserAgent
	if userAgent == "" {
		userAgent = "unknown"
	}
	clientIP := meta.ClientIP
	if clientIP == "" {
		clientIP = "unknown"
	}

	_, err := s.repo.CreateUserSession(ctx, userID, hashToken(refreshToken), userAgent, clientIP, time.Now().Add(s.sessionTTL))
	return err
}

func (s *AuthService) sendEmailVerification(ctx context.Context, user *repository.User) error {
	token, err := GenerateVerificationToken()
	if err != nil {
		return err
	}

	if err := s.repo.CreateUserToken(ctx, user.ID, hashToken(token), tokenTypeEmailVerification, time.Now().Add(24*time.Hour)); err != nil {
		return err
	}

	if s.emailService != nil {
		go s.emailService.SendVerificationEmail(user.Email, user.DisplayName, token)
	}
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
