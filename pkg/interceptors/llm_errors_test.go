package interceptors

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// Every provider failure leaves as a generic Unavailable. The message is
// what a client shows, so it must not name the model, the provider, or quote
// the provider's own prose ("requires more credits" is our bill, not theirs).
func TestProviderErrorsLeaveWithoutDetail(t *testing.T) {
	cases := map[string]error{
		"out of credits": llmerrors.Classify(genai.APIError{Code: 402, Message: "This request requires more credits"}),
		"auth failed":    llmerrors.Classify(genai.APIError{Code: 401, Message: "No auth credentials found"}),
		"model retired":  llmerrors.Classify(genai.APIError{Code: 404, Message: "This model is unavailable for free"}),
		"stalled":        fmt.Errorf("openrouter: %w", llmerrors.ErrStreamStalled),
		"empty":          llmerrors.ErrEmptyResponse,
		"unclassified":   fmt.Errorf("chat: %w", genai.APIError{Code: 400, Message: "nvidia/nemotron-3-ultra-550b-a55b:free is not a valid model ID"}),
	}
	for name, in := range cases {
		out := mapLLMError(in)
		var cerr *connect.Error
		if !errors.As(out, &cerr) || cerr.Code() != connect.CodeUnavailable {
			t.Fatalf("%s: want CodeUnavailable, got %v", name, out)
		}
		lower := strings.ToLower(cerr.Message())
		for _, secret := range []string{"openrouter", "nvidia", ":free", "credits", "auth", "message:"} {
			if strings.Contains(lower, secret) {
				t.Fatalf("%s: message leaked %q: %q", name, secret, cerr.Message())
			}
		}
	}
}

// Errors that are not the provider's are left to the handler's own mapping.
func TestNonProviderErrorsPassThrough(t *testing.T) {
	in := errors.New("city not found")
	if out := mapLLMError(in); out != in {
		t.Fatalf("rewrote a non-provider error: %v", out)
	}
}
