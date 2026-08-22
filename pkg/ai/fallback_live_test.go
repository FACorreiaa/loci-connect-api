package ai

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// TestLiveFallbackAnswers exercises the real requirement end to end: with
// no primary provider key configured, the free-tier fallback must still
// answer. It talks to OpenRouter, so it is opt-in.
//
//	AI_FALLBACK_OPENROUTER_API_KEY=sk-or-... go test ./pkg/ai/ -run TestLiveFallback -v
func TestLiveFallbackAnswers(t *testing.T) {
	key := os.Getenv("AI_FALLBACK_OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("set AI_FALLBACK_OPENROUTER_API_KEY to run the live fallback check")
	}

	cfg := config.AIConfig{
		Provider: config.AIProviderOpenRouter,
		// No primary key: this is the "no account configured" case.
		APIKey:           "",
		Model:            "",
		FallbackEnabled:  true,
		FallbackCooldown: time.Minute,
		Fallbacks: []config.AIProviderSpec{
			{Provider: config.AIProviderOpenRouter, APIKey: key, Model: "z-ai/glm-5.2:free"},
			{Provider: config.AIProviderOpenRouter, APIKey: key, Model: "nvidia/nemotron-3-super-120b-a12b:free"},
		},
		MaxRetries:      2,
		RetryBaseDelay:  500 * time.Millisecond,
		RetryMaxDelay:   4 * time.Second,
		GenerateTimeout: 90 * time.Second,
		StreamTimeout:   120 * time.Second,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	client, err := NewChatClient(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("NewChatClient: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	got, err := client.GenerateText(ctx, "Reply with exactly one word: Lisbon", nil)
	if err != nil {
		t.Fatalf("GenerateText through fallback chain: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("fallback returned an empty response")
	}
	t.Logf("answered by %q: %s", client.Model(), strings.TrimSpace(got))
}
