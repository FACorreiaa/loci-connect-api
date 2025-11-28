package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID
	Email           string
	Username        string
	HashedPassword  string
	DisplayName     string
	AvatarURL       *string
	Role            string
	IsActive        bool
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastLoginAt     *time.Time
}

type UserSession struct {
	ID                 uuid.UUID
	UserID             uuid.UUID
	HashedRefreshToken string
	UserAgent          *string
	ClientIP           *string
	ExpiresAt          time.Time
	CreatedAt          time.Time
}

type UserToken struct {
	TokenHash string
	UserID    uuid.UUID
	Type      string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type OAuthIdentity struct {
	ProviderName         string
	ProviderUserID       string
	UserID               uuid.UUID
	ProviderAccessToken  *string
	ProviderRefreshToken *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type AuthRepository interface {
	CreateUser(ctx context.Context, email, username, hashedPassword, displayName string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
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
