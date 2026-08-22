// Package llmerrors provides typed sentinel errors for LLM provider
// failures so transports (Connect handlers, MCP tools) can translate
// them consistently without inspecting provider-specific error types.
package llmerrors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/genai"
)

// ErrRateLimited marks an LLM call rejected by the provider for rate or
// quota reasons (HTTP 429 / RESOURCE_EXHAUSTED) after retries were
// exhausted. Transports should surface it as a retryable "try again
// later" condition, distinct from the user's own subscription quota.
var ErrRateLimited = errors.New("llm provider rate limited")

// ErrUnavailable marks a transient provider outage (HTTP 5xx) after
// retries were exhausted.
var ErrUnavailable = errors.New("llm provider unavailable")

// ErrOutOfCredits marks a provider refusing the call because the account
// has no credit left (HTTP 402). Unlike ErrRateLimited this is terminal
// for the credential: retrying the same key never succeeds, so callers
// should fail over to another provider rather than back off.
var ErrOutOfCredits = errors.New("llm provider out of credits")

// ErrAuthFailed marks a missing, malformed, or rejected credential
// (HTTP 401/403). Also terminal for the credential.
var ErrAuthFailed = errors.New("llm provider authentication failed")

// Terminal reports whether err means the credential that produced it is
// unusable, so retrying it is pointless and the caller should move on to
// another provider.
func Terminal(err error) bool {
	return errors.Is(err, ErrOutOfCredits) || errors.Is(err, ErrAuthFailed)
}

// Failover reports whether err is a provider-side failure that another
// provider might survive. Context cancellation is deliberately excluded:
// the caller is already gone, so burning a second provider is waste.
func Failover(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return Terminal(err) ||
		errors.Is(err, ErrRateLimited) ||
		errors.Is(err, ErrUnavailable)
}

// Classify wraps err with the matching sentinel so callers can use
// errors.Is. Context cancellation and deadline errors pass through
// unchanged; unrecognized errors are returned as-is.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == http.StatusPaymentRequired:
			return fmt.Errorf("%w: %w", ErrOutOfCredits, err)
		case apiErr.Code == http.StatusUnauthorized || apiErr.Code == http.StatusForbidden:
			return fmt.Errorf("%w: %w", ErrAuthFailed, err)
		case apiErr.Code == http.StatusTooManyRequests:
			return fmt.Errorf("%w: %w", ErrRateLimited, err)
		case apiErr.Code >= 500:
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return err
	}

	// Providers that do not surface a status code still describe these
	// conditions in prose. Check credits before quota: OpenRouter's 402
	// body says "requires more credits", and a bare "credit" match must
	// not be swallowed by the rate-limit branch below.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "insufficient credit") ||
		strings.Contains(msg, "requires more credit") ||
		strings.Contains(msg, "negative credit"):
		return fmt.Errorf("%w: %w", ErrOutOfCredits, err)
	case strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "no auth credentials") ||
		strings.Contains(msg, "unauthorized"):
		return fmt.Errorf("%w: %w", ErrAuthFailed, err)
	case strings.Contains(msg, "resource_exhausted") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "quota"):
		return fmt.Errorf("%w: %w", ErrRateLimited, err)
	}
	return err
}
