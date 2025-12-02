//revive:disable-next-line:var-naming
package common

import "errors"

var (
	ErrChatNotFound      = errors.New("chat not found")
	ErrSessionNotFound   = errors.New("session not found")
	ErrInvalidInput      = errors.New("invalid input")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrInternal          = errors.New("internal server error")
	ErrUserNotFound      = errors.New("user not found")
	ErrInvalidUUID       = errors.New("invalid UUID")
	ErrItineraryNotFound = errors.New("itinerary not found")
)
