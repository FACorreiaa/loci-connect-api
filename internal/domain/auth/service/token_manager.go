package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
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

	// GenerateMFAChallengeToken issues the short-lived token that stands in for a
	// half-completed login: the password was correct, the second factor is not in
	// yet. It grants no API access.
	GenerateMFAChallengeToken(userID, email, username, role string) (string, time.Time, error)
	ValidateMFAChallengeToken(tokenString string) (*Claims, error)
}

const (
	// purposeAccess and purposeMFAChallenge tag what a token is for.
	//
	// Defence in depth only: challenge tokens are signed with a different key, so
	// cross-use is already cryptographically impossible. The claim makes a
	// mistake visible rather than merely ineffective.
	purposeAccess       = "access"
	purposeMFAChallenge = "mfa_challenge"

	// mfaChallengeTTL is how long the user has to enter their code. Long enough
	// to pick up a phone, short enough that a leaked challenge token is close to
	// worthless.
	mfaChallengeTTL = 5 * time.Minute

	// mfaChallengeKeyLabel domain-separates the derived challenge signing key from
	// the access secret it is derived from.
	mfaChallengeKeyLabel = "loci/mfa-challenge-token/v1"
)

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

	// Purpose distinguishes an access token from an MFA challenge token. Empty on
	// tokens issued before MFA existed, which are still valid access tokens —
	// requiring it would sign out every current session on deploy.
	Purpose string `json:"purpose,omitempty"`

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
		Purpose:  purposeAccess,
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
	claims, err := tm.validateToken(tokenString, tm.accessTokenSecret)
	if err != nil {
		return nil, err
	}

	// An MFA challenge token must never open the API. It is signed with a
	// different key so it cannot reach this point, but a future refactor that
	// merged the keys would silently turn a half-login into a full one.
	if claims.Purpose != "" && claims.Purpose != purposeAccess {
		return nil, errors.New("token is not an access token")
	}
	return claims, nil
}

// GenerateMFAChallengeToken issues a token that proves only that the password
// step was passed.
func (tm *jwtTokenManager) GenerateMFAChallengeToken(userID, email, username, role string) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(mfaChallengeTTL)

	claims := &Claims{
		UserID:   userID,
		Email:    email,
		Username: username,
		Role:     role,
		Purpose:  purposeMFAChallenge,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(tm.mfaChallengeSecret())
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

// ValidateMFAChallengeToken validates a challenge token from VerifyMFA.
func (tm *jwtTokenManager) ValidateMFAChallengeToken(tokenString string) (*Claims, error) {
	claims, err := tm.validateToken(tokenString, tm.mfaChallengeSecret())
	if err != nil {
		return nil, err
	}

	// Symmetric to ValidateAccessToken: only a challenge token completes a login.
	if claims.Purpose != purposeMFAChallenge {
		return nil, errors.New("token is not an MFA challenge token")
	}
	return claims, nil
}

// mfaChallengeSecret derives the challenge signing key from the access secret.
//
// Derived rather than configured so MFA cannot be deployed with a missing or
// weak third secret. HMAC with a fixed label gives a key that is independent of
// the access secret in practice: a challenge token cannot be verified as an
// access token, or vice versa, even though only one secret is configured.
func (tm *jwtTokenManager) mfaChallengeSecret() []byte {
	mac := hmac.New(sha256.New, tm.accessTokenSecret)
	mac.Write([]byte(mfaChallengeKeyLabel))
	return mac.Sum(nil)
}

// ValidateRefreshToken validates a refresh token and returns claims
func (tm *jwtTokenManager) ValidateRefreshToken(tokenString string) (*Claims, error) {
	return tm.validateToken(tokenString, tm.refreshTokenSecret)
}

// validateToken is a helper function to validate tokens
func (tm *jwtTokenManager) validateToken(tokenString string, secret []byte) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
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
