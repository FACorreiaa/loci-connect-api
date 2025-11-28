package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenManager defines the behavior required for token operations.
type TokenManager interface {
	GenerateTokenPair(userID, email, username, role string) (*TokenPair, error)
	ValidateAccessToken(tokenString string) (*Claims, error)
	ValidateRefreshToken(tokenString string) (*Claims, error)
}

type jwtTokenManager struct {
	accessTokenSecret  []byte
	refreshTokenSecret []byte
	accessTokenTTL     time.Duration
	refreshTokenTTL    time.Duration
}

// TokenPair represents access and refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
}

// Claims represents JWT claims
type Claims struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// NewTokenManager creates a new token manager
func NewTokenManager(accessSecret, refreshSecret []byte, accessTTL, refreshTTL time.Duration) TokenManager {
	return &jwtTokenManager{
		accessTokenSecret:  accessSecret,
		refreshTokenSecret: refreshSecret,
		accessTokenTTL:     accessTTL,
		refreshTokenTTL:    refreshTTL,
	}
}

// GenerateTokenPair generates both access and refresh tokens
func (tm *jwtTokenManager) GenerateTokenPair(userID, email, username, role string) (*TokenPair, error) {
	now := time.Now()
	accessExpiresAt := now.Add(tm.accessTokenTTL)
	refreshExpiresAt := now.Add(tm.refreshTokenTTL)

	// Generate access token
	accessClaims := &Claims{
		UserID:   userID,
		Email:    email,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(accessExpiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
	}

	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString(tm.accessTokenSecret)
	if err != nil {
		return nil, err
	}

	// Generate refresh token
	refreshClaims := &Claims{
		UserID:   userID,
		Email:    email,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(refreshExpiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
	}

	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString(tm.refreshTokenSecret)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
		ExpiresAt:    accessExpiresAt,
		TokenType:    "Bearer",
	}, nil
}

// ValidateAccessToken validates an access token and returns claims
func (tm *jwtTokenManager) ValidateAccessToken(tokenString string) (*Claims, error) {
	return tm.validateToken(tokenString, tm.accessTokenSecret)
}

// ValidateRefreshToken validates a refresh token and returns claims
func (tm *jwtTokenManager) ValidateRefreshToken(tokenString string) (*Claims, error) {
	return tm.validateToken(tokenString, tm.refreshTokenSecret)
}

// validateToken is a helper function to validate tokens
func (tm *jwtTokenManager) validateToken(tokenString string, secret []byte) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}

// GenerateVerificationToken generates a random token for email verification
func GenerateVerificationToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// GeneratePasswordResetToken generates a random token for password reset
func GeneratePasswordResetToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
