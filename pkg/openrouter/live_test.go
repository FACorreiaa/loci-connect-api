package openrouter_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/openrouter"
)

// TestLiveOpenRouterChat makes a REAL call to OpenRouter using the configured
// key. Skipped unless OPENROUTER_API_KEY is present (loaded from ../../.env).
// Run: go test ./pkg/openrouter/ -run TestLiveOpenRouter -v
func TestLiveOpenRouterChat(t *testing.T) {
	// Opt-in only: this makes a real, billed OpenRouter call. Normal `go test ./...`
	// skips it. Run with: LOCI_LIVE_LLM=1 go test ./pkg/openrouter/ -run TestLiveOpenRouter -v
	if os.Getenv("LOCI_LIVE_LLM") != "1" {
		t.Skip("set LOCI_LIVE_LLM=1 to run the live OpenRouter test")
	}
	_ = godotenv.Load("../../.env")
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set; skipping live test")
	}

	model := os.Getenv("OPENROUTER_MODEL")
	if model == "" {
		model = "openrouter/auto"
	}

	cfg := config.AIConfig{
		Provider:        config.AIProviderOpenRouter,
		APIKey:          key,
		Model:           model,
		MaxRetries:      2,
		RetryBaseDelay:  500 * time.Millisecond,
		RetryMaxDelay:   4 * time.Second,
		GenerateTimeout: 60 * time.Second,
		StreamTimeout:   90 * time.Second,
	}

	client, err := openrouter.NewChatClient(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewChatClient: %v", err)
	}
	t.Logf("provider=openrouter model=%s", client.Model())

	ctx := context.Background()

	// 1) Unary generate.
	text, err := client.GenerateText(ctx, "Reply with exactly this token and nothing else: LOCI_OK", nil)
	if err != nil {
		t.Fatalf("GenerateText failed (key/budget/model issue?): %v", err)
	}
	t.Logf("unary response: %q", text)
	if strings.TrimSpace(text) == "" {
		t.Fatal("empty response from OpenRouter")
	}

	// 2) Streaming generate — exercises the path the chat stream uses.
	seq, err := client.GenerateStream(ctx, "List 3 things to do in Lisbon, one per line.", nil)
	if err != nil {
		t.Fatalf("GenerateStream open failed: %v", err)
	}
	var chunks int
	var sb strings.Builder
	for resp, err := range seq {
		if err != nil {
			t.Fatalf("stream chunk error: %v", err)
		}
		chunks++
		sb.WriteString(resp.Text())
	}
	t.Logf("stream chunks=%d, %d chars", chunks, sb.Len())
	if chunks == 0 || sb.Len() == 0 {
		t.Fatal("streaming produced no content")
	}
}
