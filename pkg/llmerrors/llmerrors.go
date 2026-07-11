// Package llmerrors provides typed sentinel errors for LLM provider
// failures so transports (Connect handlers, MCP tools) can translate
// them consistently without inspecting provider-specific error types.
package llmerrors

import (
	"context"
	"errors"
	"fmt"
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
		case apiErr.Code == 429:
			return fmt.Errorf("%w: %w", ErrRateLimited, err)
		case apiErr.Code >= 500:
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return err
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "resource_exhausted") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "quota") {
		return fmt.Errorf("%w: %w", ErrRateLimited, err)
	}
	return err
}
