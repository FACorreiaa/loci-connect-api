package llmerrors

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/genai"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"nil", nil, nil},
		{"api 429", genai.APIError{Code: 429, Message: "rate limit"}, ErrRateLimited},
		{"api 503", genai.APIError{Code: 503, Message: "overloaded"}, ErrUnavailable},
		{"api 400 passthrough", genai.APIError{Code: 400, Message: "bad request"}, nil},
		{"wrapped api 429", fmt.Errorf("call failed: %w", genai.APIError{Code: 429}), ErrRateLimited},
		{"resource exhausted string", errors.New("rpc error: RESOURCE_EXHAUSTED"), ErrRateLimited},
		{"quota string", errors.New("quota exceeded for model"), ErrRateLimited},
		// OpenRouter surfaces credit and auth failures as real status codes
		// via readAPIError, so these arrive as genai.APIError.
		{"api 402 out of credits", genai.APIError{Code: 402, Message: "requires more credits"}, ErrOutOfCredits},
		{"api 401 auth", genai.APIError{Code: 401, Message: "No auth credentials found"}, ErrAuthFailed},
		{"api 403 auth", genai.APIError{Code: 403, Message: "forbidden"}, ErrAuthFailed},
		{"wrapped api 402", fmt.Errorf("chat: %w", genai.APIError{Code: 402}), ErrOutOfCredits},
		{"credits string", errors.New("This request requires more credits"), ErrOutOfCredits},
		{"insufficient credits string", errors.New("Insufficient credits remaining"), ErrOutOfCredits},
		{"invalid key string", errors.New("Invalid API key provided"), ErrAuthFailed},
		// "requires more credits" must not be captured by the rate-limit
		// branch just because a provider also mentions quota.
		{"credits beats quota", errors.New("quota: insufficient credits"), ErrOutOfCredits},
		{"context canceled passthrough", context.Canceled, nil},
		{"deadline passthrough", context.DeadlineExceeded, nil},
		{"unknown passthrough", errors.New("something else"), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.err)
			if tt.err == nil {
				if got != nil {
					t.Fatalf("Classify(nil) = %v, want nil", got)
				}
				return
			}
			if tt.want != nil && !errors.Is(got, tt.want) {
				t.Fatalf("Classify(%v) = %v, want errors.Is %v", tt.err, got, tt.want)
			}
			if tt.want == nil && (errors.Is(got, ErrRateLimited) || errors.Is(got, ErrUnavailable) ||
				errors.Is(got, ErrOutOfCredits) || errors.Is(got, ErrAuthFailed)) {
				t.Fatalf("Classify(%v) = %v, want passthrough without sentinel", tt.err, got)
			}
			// The original error must remain inspectable.
			if !errors.Is(got, tt.err) && !errors.As(got, &genai.APIError{}) {
				var apiErr genai.APIError
				if !errors.As(got, &apiErr) {
					t.Fatalf("Classify(%v) lost the original error: %v", tt.err, got)
				}
			}
		})
	}
}

func TestTerminal(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"out of credits", Classify(genai.APIError{Code: 402}), true},
		{"auth failed", Classify(genai.APIError{Code: 401}), true},
		{"rate limited is not terminal", Classify(genai.APIError{Code: 429}), false},
		{"unavailable is not terminal", Classify(genai.APIError{Code: 503}), false},
		{"nil", nil, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := Terminal(tt.err); got != tt.want {
				t.Fatalf("Terminal(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestFailover(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"out of credits", Classify(genai.APIError{Code: 402}), true},
		{"auth failed", Classify(genai.APIError{Code: 403}), true},
		{"rate limited", Classify(genai.APIError{Code: 429}), true},
		{"unavailable", Classify(genai.APIError{Code: 500}), true},
		// The caller is already gone; failing over burns a second provider
		// for a response nobody will read.
		{"context canceled", context.Canceled, false},
		{"deadline exceeded", context.DeadlineExceeded, false},
		{"wrapped cancel", fmt.Errorf("stream: %w", context.Canceled), false},
		{"bad request is not failover", Classify(genai.APIError{Code: 400}), false},
		{"unknown is not failover", errors.New("boom"), false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := Failover(tt.err); got != tt.want {
				t.Fatalf("Failover(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
