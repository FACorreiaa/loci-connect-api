package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/markbates/goth"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/auth/repository"
)

const (
	tokenTypeEmailVerification = "email_verification"
	tokenTypePasswordReset     = "password_reset"

	defaultSessionTTL = 30 * 24 * time.Hour
)

// ErrAccountInactive is returned when a user has been disabled.
var ErrAccountInactive = errors.New("account is deactivated")

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

	// MFARequired is true when the password was correct but a second factor is
	// still outstanding. Tokens is nil in that case — the whole point of step-up
	// is that no usable credential exists until VerifyMFA succeeds.
	MFARequired bool

	// MFAToken is the challenge token to pass back to VerifyMFA.
	MFAToken string
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

// MFAVerifier is the slice of the MFA service that login needs.
//
// A narrow port, not the whole mfa.Service: auth must be able to ask "does this
// user owe a second factor" and "is this one valid" without gaining the ability
// to enrol or disable anything.
type MFAVerifier interface {
	IsEnabled(ctx context.Context, userID uuid.UUID) (bool, error)
	VerifyForLogin(ctx context.Context, userID uuid.UUID, code, recoveryCode string) error
}

// AuthService coordinates AUTH business logic.
type AuthService struct {
	repo         repository.AuthRepository
	tokenManager TokenManager
	emailService EmailSender
	sessionTTL   time.Duration
	logger       *slog.Logger

	// mfa is nil when MFA is not configured, in which case login behaves exactly
	// as it did before.
	mfa MFAVerifier
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

// WithMFA enables login step-up. Without it, Login keeps its previous behaviour.
func (s *AuthService) WithMFA(v MFAVerifier) *AuthService {
	s.mfa = v
	return s
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

	// Step up before any token exists. Everything below this point — token pair,
	// session row, last-login stamp — is the second half of a login and must not
	// happen for a user who still owes a second factor.
	if s.mfa != nil {
		enabled, err := s.mfa.IsEnabled(ctx, user.ID)
		if err != nil {
			// Failing open here would silently disable MFA for everyone the moment
			// the MFA table became unreadable.
			return nil, fmt.Errorf("could not determine MFA status: %w", err)
		}
		if enabled {
			challenge, _, err := s.tokenManager.GenerateMFAChallengeToken(
				user.ID.String(), user.Email, user.Username, user.Role)
			if err != nil {
				return nil, err
			}
			return &LoginResult{
				User:        user,
				MFARequired: true,
				MFAToken:    challenge,
			}, nil
		}
	}

	return s.completeLogin(ctx, user, params.Metadata)
}

// CompleteMFALogin finishes a login that was challenged, after the second factor
// has been verified.
//
// Separate from Login so there is exactly one code path that mints tokens, and
// it is only reachable once a factor has been checked.
func (s *AuthService) CompleteMFALogin(ctx context.Context, challengeToken, code, recoveryCode string, meta SessionMetadata) (*LoginResult, error) {
	if s.mfa == nil {
		return nil, errors.New("MFA is not configured")
	}

	claims, err := s.tokenManager.ValidateMFAChallengeToken(challengeToken)
	if err != nil {
		return nil, common.ErrInvalidCredentials
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, common.ErrInvalidCredentials
	}

	if err := s.mfa.VerifyForLogin(ctx, userID, code, recoveryCode); err != nil {
		return nil, err
	}

	// Re-read the user rather than trusting the claims: the account may have been
	// deactivated during the challenge window.
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !user.IsActive {
		return nil, ErrAccountInactive
	}

	return s.completeLogin(ctx, user, meta)
}

// completeLogin issues the token pair and records the session. The only place
// that mints login tokens.
func (s *AuthService) completeLogin(ctx context.Context, user *repository.User, meta SessionMetadata) (*LoginResult, error) {
	tokens, err := s.tokenManager.GenerateTokenPair(user.ID.String(), user.Email, user.Username, user.Role)
	if err != nil {
		return nil, err
	}

	if err := s.createSession(ctx, user.ID, tokens.RefreshToken, meta); err != nil {
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
		emailCtx, cancel := backgroundEmailContext(ctx)
		go func(ctx context.Context, cancel context.CancelFunc, email, name, token string) {
			defer cancel()
			if err := s.emailService.SendPasswordResetEmail(email, name, token); err != nil {
				s.logger.WarnContext(ctx, "failed to send password reset email", slog.Any("error", err))
			}
		}(emailCtx, cancel, user.Email, user.DisplayName, resetToken)
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
func (s *AuthService) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
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

// ChangeEmail changes the email for an authenticated user after verifying their
// current password. The new email is collision-checked against existing users
// and refresh-token sessions are invalidated on success.
func (s *AuthService) ChangeEmail(ctx context.Context, userID, currentPassword, newEmail string) error {
	if userID == "" {
		return fmt.Errorf("user id required")
	}
	if newEmail == "" {
		return fmt.Errorf("new email required")
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

	// No-op if the email is unchanged.
	if user.Email == newEmail {
		return nil
	}

	// Collision-check: reject if another user already owns this email.
	if existing, err := s.repo.GetUserByEmail(ctx, newEmail); err == nil {
		if existing.ID != user.ID {
			return common.ErrUserAlreadyExists
		}
	} else if !errors.Is(err, common.ErrUserNotFound) {
		return err
	}

	if err := s.repo.UpdateEmail(ctx, userUUID, newEmail); err != nil {
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
			emailCtx, cancel := backgroundEmailContext(ctx)
			go func(ctx context.Context, cancel context.CancelFunc, email, name string) {
				defer cancel()
				if err := s.emailService.SendWelcomeEmail(email, name); err != nil {
					s.logger.WarnContext(ctx, "failed to send welcome email", slog.Any("error", err))
				}
			}(emailCtx, cancel, user.Email, user.DisplayName)
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
		emailCtx, cancel := backgroundEmailContext(ctx)
		go func(ctx context.Context, cancel context.CancelFunc, email, name, verificationToken string) {
			defer cancel()
			if err := s.emailService.SendVerificationEmail(email, name, verificationToken); err != nil {
				s.logger.WarnContext(ctx, "failed to send verification email", slog.Any("error", err))
			}
		}(emailCtx, cancel, user.Email, user.DisplayName, token)
	}
	return nil
}

func backgroundEmailContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// LoginOrRegisterOAuth handles OAuth authentication - finds existing user or creates new one
func (s *AuthService) LoginOrRegisterOAuth(ctx context.Context, provider string, gothUser *goth.User, meta SessionMetadata) (*LoginResult, bool, error) {
	isNewUser := false

	// Try to find existing user by OAuth identity
	user, err := s.repo.GetUserByOAuthIdentity(ctx, provider, gothUser.UserID)
	if errors.Is(err, common.ErrUserNotFound) {
		// No OAuth identity found, check if user exists by email
		user, err = s.repo.GetUserByEmail(ctx, gothUser.Email)
		if errors.Is(err, common.ErrUserNotFound) {
			// Create new user
			username := generateUsername(gothUser.NickName, gothUser.Email)
			displayName := gothUser.Name
			if displayName == "" {
				displayName = username
			}

			user, err = s.repo.CreateUser(ctx, gothUser.Email, username, "", displayName)
			if err != nil {
				return nil, false, fmt.Errorf("failed to create user: %w", err)
			}
			isNewUser = true
		} else if err != nil {
			return nil, false, err
		}

		// Link OAuth identity to user
		var accessToken, refreshToken *string
		if gothUser.AccessToken != "" {
			accessToken = &gothUser.AccessToken
		}
		if gothUser.RefreshToken != "" {
			refreshToken = &gothUser.RefreshToken
		}
		if err := s.repo.CreateOrUpdateOAuthIdentity(ctx, provider, gothUser.UserID, user.ID, accessToken, refreshToken); err != nil {
			return nil, false, fmt.Errorf("failed to link OAuth identity: %w", err)
		}
	} else if err != nil {
		return nil, false, err
	}

	if !user.IsActive {
		return nil, false, ErrAccountInactive
	}

	// Generate tokens
	tokens, err := s.tokenManager.GenerateTokenPair(user.ID.String(), user.Email, user.Username, user.Role)
	if err != nil {
		return nil, false, err
	}

	if err := s.createSession(ctx, user.ID, tokens.RefreshToken, meta); err != nil {
		return nil, false, err
	}

	_ = s.repo.UpdateLastLogin(ctx, user.ID)

	return &LoginResult{User: user, Tokens: tokens}, isNewUser, nil
}

// LoginOrRegisterPhone handles phone authentication - finds existing user or creates new one
func (s *AuthService) LoginOrRegisterPhone(ctx context.Context, phone string, meta SessionMetadata) (*LoginResult, bool, error) {
	isNewUser := false

	user, err := s.repo.GetUserByPhone(ctx, phone)
	if errors.Is(err, common.ErrUserNotFound) {
		// Create new user with phone
		username := "user_" + generateShortID()

		user, err = s.repo.CreateUserWithPhone(ctx, phone, username)
		if err != nil {
			return nil, false, fmt.Errorf("failed to create user: %w", err)
		}
		isNewUser = true
	} else if err != nil {
		return nil, false, err
	}

	if !user.IsActive {
		return nil, false, ErrAccountInactive
	}

	// Generate tokens (email may be empty for phone-only users)
	tokens, err := s.tokenManager.GenerateTokenPair(user.ID.String(), user.Email, user.Username, user.Role)
	if err != nil {
		return nil, false, err
	}

	if err := s.createSession(ctx, user.ID, tokens.RefreshToken, meta); err != nil {
		return nil, false, err
	}

	_ = s.repo.UpdateLastLogin(ctx, user.ID)

	return &LoginResult{User: user, Tokens: tokens}, isNewUser, nil
}

// generateUsername creates a username from OAuth profile data
func generateUsername(nickname, email string) string {
	if nickname != "" {
		// Clean the nickname
		clean := strings.ReplaceAll(nickname, " ", "_")
		clean = strings.ToLower(clean)
		return clean
	}
	// Use email prefix
	parts := strings.Split(email, "@")
	if len(parts) > 0 {
		return strings.ToLower(parts[0])
	}
	return "user_" + generateShortID()
}

// generateShortID creates a short unique identifier
func generateShortID() string {
	return uuid.New().String()[:8]
}
