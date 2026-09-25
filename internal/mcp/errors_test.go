package mcp

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// A tool error is read by an agent and, through it, by a person. Neither
// needs to know which model the chain reached for.
func TestToolErrorHidesTheModel(t *testing.T) {
	for _, in := range []error{
		llmerrors.Classify(genai.APIError{Code: 402, Message: "This request requires more credits"}),
		fmt.Errorf("chat: %w", genai.APIError{Code: 400, Message: "nvidia/nemotron-3-ultra-550b-a55b:free is not a valid model ID"}),
		fmt.Errorf("openrouter: %w", llmerrors.ErrStreamStalled),
	} {
		msg := strings.ToLower(toolError(in).Error())
		for _, secret := range []string{"openrouter", "nvidia", ":free", "credits", "message:"} {
			if strings.Contains(msg, secret) {
				t.Fatalf("tool error leaked %q: %q", secret, msg)
			}
		}
	}
}
