package ai

import (
	"log/slog"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

func TestAnthropicClientIsAnthropicOrNothing(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	// A paid primary configured beside it must never be what comes back.
	primary := config.AIConfig{
		Provider: config.AIProviderOpenRouter,
		APIKey:   "fake-openrouter-key",
		Model:    "deepseek/deepseek-v4-flash",
	}

	off := primary
	off.Anthropic = config.AnthropicConfig{APIKey: "fake-anthropic-key", First: false, Model: "claude-haiku-5-5"}
	client, err := NewAnthropicClient(off, logger)
	if err != nil {
		t.Fatalf("switched off: %v", err)
	}
	if client != nil {
		t.Fatalf("switched off: got a %T, want no client", client)
	}

	on := primary
	on.Anthropic = config.AnthropicConfig{APIKey: "fake-anthropic-key", First: true, Model: "claude-haiku-5-5"}
	client, err = NewAnthropicClient(on, logger)
	if err != nil {
		t.Fatalf("switched on: %v", err)
	}
	if client == nil {
		t.Fatal("switched on: got no client")
	}
	if got := client.Model(); got != "claude-haiku-5-5" {
		t.Errorf("model = %q, want Haiku", got)
	}
}
