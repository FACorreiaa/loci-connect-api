package ai

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

func freeCfg(models ...string) config.AIConfig {
	specs := make([]config.AIProviderSpec, 0, len(models))
	for _, m := range models {
		specs = append(specs, config.AIProviderSpec{
			Provider: config.AIProviderOpenRouter,
			APIKey:   "free-key",
			Model:    m,
		})
	}
	return config.AIConfig{
		Provider:        config.AIProviderOpenRouter,
		APIKey:          "paid-key",
		Model:           "anthropic/claude-sonnet-4.5",
		FallbackEnabled: true,
		Fallbacks:       specs,
	}
}

// The whole point: a free-tier caller must never reach the primary, which is
// the key the operator pays for.
func TestTheFreeChainExcludesThePaidPrimary(t *testing.T) {
	cfg := freeCfg("z-ai/glm-5.2:free", "nvidia/nemotron-3-super-120b-a12b:free")

	client, err := NewFreeChatClient(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// The chain reports the model it would try first. On the free chain that is
	// the first fallback, not the configured primary.
	if got := client.Model(); got != "z-ai/glm-5.2:free" {
		t.Errorf("free chain head = %q, want the first free model", got)
	}
	if strings.Contains(client.Model(), "claude") {
		t.Error("the free chain starts on the paid primary")
	}
}

// With nothing free configured there is no floor, and saying so is better than
// handing back a client that silently bills the operator for free-tier traffic.
func TestNoFreeChainWithoutFallbacks(t *testing.T) {
	cfg := freeCfg()

	if _, err := NewFreeChatClient(t.Context(), cfg, slog.Default()); err == nil {
		t.Fatal("a free chain was built with no free models configured")
	}
}

// FallbackEnabled off means the operator has turned the floor off deliberately.
func TestNoFreeChainWhenFallbacksAreDisabled(t *testing.T) {
	cfg := freeCfg("z-ai/glm-5.2:free")
	cfg.FallbackEnabled = false

	if _, err := NewFreeChatClient(t.Context(), cfg, slog.Default()); err == nil {
		t.Fatal("a free chain was built while fallbacks are disabled")
	}
}

// The paid chain is unchanged by any of this: primary first, free models behind
// it as a backstop.
func TestThePaidChainStillLeadsWithThePrimary(t *testing.T) {
	cfg := freeCfg("z-ai/glm-5.2:free")

	client, err := NewChatClient(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := client.Model(); got != "anthropic/claude-sonnet-4.5" {
		t.Errorf("paid chain head = %q, want the configured primary", got)
	}
}

// A free chain of one is still a chain. It has no backstop, which is the
// operator's choice to make, not a reason to refuse to build it.
func TestASingleFreeModelIsEnough(t *testing.T) {
	cfg := freeCfg("z-ai/glm-5.2:free")

	client, err := NewFreeChatClient(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if client.Model() != "z-ai/glm-5.2:free" {
		t.Errorf("head = %q", client.Model())
	}
}

func withAnthropic(cfg config.AIConfig, scope string) config.AIConfig {
	cfg.Anthropic = config.AnthropicConfig{
		APIKey:   "sk-ant-test",
		First:    true,
		Model:    config.DefaultAnthropicModel,
		Thinking: config.AnthropicThinkingAdaptive,
		Scope:    scope,
	}
	return cfg
}

// The trial puts Claude ahead of the primary on the paid chain.
func TestThePaidChainLeadsWithAnthropicWhenFirst(t *testing.T) {
	cfg := withAnthropic(freeCfg("z-ai/glm-5.2:free"), config.AnthropicScopePaid)

	client, err := NewChatClient(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := client.Model(); got != config.DefaultAnthropicModel {
		t.Errorf("paid chain head = %q, want %q", got, config.DefaultAnthropicModel)
	}
	chain, ok := client.(*chainClient)
	if !ok {
		t.Fatalf("client is %T, want a chain so the primary stays behind Claude", client)
	}
	if got := chain.entries[1].model; got != "anthropic/claude-sonnet-4.5" {
		t.Errorf("second link = %q, want the configured primary", got)
	}
}

// A sealed key with ANTHROPIC_FIRST off is the rollback state: nothing moves.
func TestAnthropicKeyWithoutFirstLeavesTheChainsAlone(t *testing.T) {
	cfg := withAnthropic(freeCfg("z-ai/glm-5.2:free"), config.AnthropicScopeAll)
	cfg.Anthropic.First = false

	paid, err := NewChatClient(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("build paid: %v", err)
	}
	if got := paid.Model(); got != "anthropic/claude-sonnet-4.5" {
		t.Errorf("paid chain head = %q, want the primary", got)
	}
	if n := len(paid.(*chainClient).entries); n != 2 {
		t.Errorf("paid chain has %d links, want primary + 1 fallback", n)
	}
	free, err := NewFreeChatClient(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("build free: %v", err)
	}
	if got := free.Model(); got != "z-ai/glm-5.2:free" {
		t.Errorf("free chain head = %q, want the first free model", got)
	}
}

// ANTHROPIC_SCOPE=all (the default) puts Claude ahead of the free models too;
// the free models stay behind it.
func TestTheFreeChainLeadsWithAnthropicWhenScopeIsAll(t *testing.T) {
	cfg := withAnthropic(freeCfg("z-ai/glm-5.2:free"), config.AnthropicScopeAll)

	client, err := NewFreeChatClient(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	chain := client.(*chainClient)
	if got := chain.Model(); got != config.DefaultAnthropicModel {
		t.Errorf("free chain head = %q, want %q", got, config.DefaultAnthropicModel)
	}
	if len(chain.entries) != 2 || chain.entries[1].model != "z-ai/glm-5.2:free" {
		t.Errorf("free chain = %d links, want Claude then the free model", len(chain.entries))
	}
	for _, e := range chain.entries {
		if e.model == "anthropic/claude-sonnet-4.5" {
			t.Error("the free chain reaches the paid primary")
		}
	}
}

// ANTHROPIC_SCOPE=paid keeps the free tier on free models only.
func TestTheFreeChainSkipsAnthropicWhenScopeIsPaid(t *testing.T) {
	cfg := withAnthropic(freeCfg("z-ai/glm-5.2:free"), config.AnthropicScopePaid)

	client, err := NewFreeChatClient(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := client.Model(); got != "z-ai/glm-5.2:free" {
		t.Errorf("free chain head = %q, want the first free model", got)
	}
}

// Claude does not create a free floor where none was configured: without
// free models, free callers already reach the paid chain, which has it.
func TestAnthropicDoesNotCreateAFreeChain(t *testing.T) {
	cfg := withAnthropic(freeCfg(), config.AnthropicScopeAll)

	if _, err := NewFreeChatClient(t.Context(), cfg, slog.Default()); err == nil {
		t.Fatal("a free chain was built from the Anthropic link alone")
	}
}
