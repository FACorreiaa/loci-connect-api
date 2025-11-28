package common

import "errors"

var (
	ErrUserNotFound       = errors.New("user not found")
	ErrUserAlreadyExists  = errors.New("user already exists")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrSessionNotFound    = errors.New("session not found")
	ErrInvalidCredentials = errors.New("invalid or expired credentials")
)
