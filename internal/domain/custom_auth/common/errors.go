package common

import "errors"

var (
	// OAuth errors
	ErrOAuthProviderNotConfigured = errors.New("OAuth provider not configured")
	ErrOAuthInvalidState          = errors.New("invalid OAuth state")
	ErrOAuthTokenExchange         = errors.New("failed to exchange OAuth code")
	ErrOAuthUserFetch             = errors.New("failed to fetch OAuth user info")

	// Phone auth errors
	ErrPhoneVerificationFailed = errors.New("phone verification failed")
	ErrInvalidVerificationCode = errors.New("invalid verification code")
	ErrPhoneSendFailed         = errors.New("failed to send verification code")
)
