package service

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// signupHookTimeout bounds the work done after an account is created, so a
// slow hook adds at most this much to a signup response.
const signupHookTimeout = 3 * time.Second

// SignupHook runs once a new account exists. It is best effort: it logs its
// own failures and never fails the signup. inviteCode is whatever the client
// sent, unvalidated, and may be empty.
type SignupHook interface {
	OnSignup(ctx context.Context, userID uuid.UUID, inviteCode string)
}

// RunSignupHook calls hook, if any, on a context the caller cannot cancel: a
// client that hangs up as its account is created still gets its invite
// recorded.
func RunSignupHook(ctx context.Context, hook SignupHook, userID uuid.UUID, inviteCode string) {
	if hook == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signupHookTimeout)
	defer cancel()
	hook.OnSignup(ctx, userID, inviteCode)
}
