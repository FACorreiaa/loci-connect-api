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
			if tt.want == nil && (errors.Is(got, ErrRateLimited) || errors.Is(got, ErrUnavailable)) {
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
