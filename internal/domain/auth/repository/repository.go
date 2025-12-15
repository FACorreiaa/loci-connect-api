package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID  `db:"id"`
	Email           string     `db:"email"`
	Username        string     `db:"username"`
	HashedPassword  string     `db:"hashed_password"`
	DisplayName     string     `db:"display_name"`
	AvatarURL       *string    `db:"avatar_url"`
	Role            string     `db:"role"`
	IsActive        bool       `db:"is_active"`
	EmailVerifiedAt *time.Time `db:"email_verified_at"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
	LastLoginAt     *time.Time `db:"last_login_at"`
}

type UserSession struct {
	ID                 uuid.UUID `db:"id"`
	UserID             uuid.UUID `db:"user_id"`
	HashedRefreshToken string    `db:"hashed_refresh_token"`
	UserAgent          *string   `db:"user_agent"`
	ClientIP           *string   `db:"client_ip"`
	ExpiresAt          time.Time `db:"expires_at"`
	CreatedAt          time.Time `db:"created_at"`
}

type UserToken struct {
	TokenHash string    `db:"token_hash"`
	UserID    uuid.UUID `db:"user_id"`
	Type      string    `db:"type"`
	ExpiresAt time.Time `db:"expires_at"`
	CreatedAt time.Time `db:"created_at"`
}

type OAuthIdentity struct {
	ProviderName         string    `db:"provider_name"`
	ProviderUserID       string    `db:"provider_user_id"`
	UserID               uuid.UUID `db:"user_id"`
	ProviderAccessToken  *string   `db:"provider_access_token"`
	ProviderRefreshToken *string   `db:"provider_refresh_token"`
	CreatedAt            time.Time `db:"created_at"`
	UpdatedAt            time.Time `db:"updated_at"`
}

type AuthRepository interface {
	CreateUser(ctx context.Context, email, username, hashedPassword, displayName string) (*User, error)
	CreateUserWithPhone(ctx context.Context, phone, username string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByPhone(ctx context.Context, phone string) (*User, error)
	GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error)
	UpdateLastLogin(ctx context.Context, userID uuid.UUID) error

	CreateUserSession(ctx context.Context, userID uuid.UUID, hashedRefreshToken, userAgent, clientIP string, expiresAt time.Time) (*UserSession, error)
	GetUserSessionByToken(ctx context.Context, hashedToken string) (*UserSession, error)
	DeleteUserSession(ctx context.Context, hashedToken string) error
	DeleteAllUserSessions(ctx context.Context, userID uuid.UUID) error

	CreateUserToken(ctx context.Context, userID uuid.UUID, tokenHash, tokenType string, expiresAt time.Time) error
	GetUserTokenByHash(ctx context.Context, tokenHash, tokenType string) (*UserToken, error)
	DeleteUserToken(ctx context.Context, tokenHash string) error

	VerifyEmail(ctx context.Context, userID uuid.UUID) error
	UpdatePassword(ctx context.Context, userID uuid.UUID, hashedPassword string) error

	CreateOrUpdateOAuthIdentity(ctx context.Context, providerName, providerUserID string, userID uuid.UUID, accessToken, refreshToken *string) error
	GetUserByOAuthIdentity(ctx context.Context, providerName, providerUserID string) (*User, error)
}
