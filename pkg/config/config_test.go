package config

import (
	"testing"
	"time"
)

func TestLoad_QuotaAndStripeDefaults(t *testing.T) {
	// Required values so Load passes validation regardless of host env.
	t.Setenv("AI_PROVIDER", AIProviderGemini)
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("GEMINI_MODEL", "gemini-test")
	t.Setenv("JWT_SECRET", "test-secret-test-secret-test-secret")
	t.Setenv("FREE_DAILY_LLM_LIMIT", "")
	t.Setenv("PRO_DAILY_LLM_LIMIT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Subscription.FreeDailyLLMLimit != 10 {
		t.Errorf("FreeDailyLLMLimit default = %d, want 10", cfg.Subscription.FreeDailyLLMLimit)
	}
	if cfg.Subscription.ProDailyLLMLimit != 300 {
		t.Errorf("ProDailyLLMLimit default = %d, want 300", cfg.Subscription.ProDailyLLMLimit)
	}
}

func TestLoad_QuotaAndStripeOverrides(t *testing.T) {
	t.Setenv("AI_PROVIDER", AIProviderGemini)
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("GEMINI_MODEL", "gemini-test")
	t.Setenv("JWT_SECRET", "test-secret-test-secret-test-secret")
	t.Setenv("FREE_DAILY_LLM_LIMIT", "25")
	t.Setenv("PRO_DAILY_LLM_LIMIT", "1000")
	t.Setenv("STRIPE_API_KEY", "sk_test_abc")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_abc")
	t.Setenv("STRIPE_PRICE_ID_MONTHLY", "price_m")
	t.Setenv("STRIPE_PRICE_ID_ANNUAL", "price_a")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Subscription.FreeDailyLLMLimit != 25 {
		t.Errorf("FreeDailyLLMLimit = %d, want 25", cfg.Subscription.FreeDailyLLMLimit)
	}
	if cfg.Subscription.ProDailyLLMLimit != 1000 {
		t.Errorf("ProDailyLLMLimit = %d, want 1000", cfg.Subscription.ProDailyLLMLimit)
	}
	if cfg.Stripe.APIKey != "sk_test_abc" {
		t.Errorf("Stripe.APIKey = %q", cfg.Stripe.APIKey)
	}
	if cfg.Stripe.WebhookSecret != "whsec_abc" {
		t.Errorf("Stripe.WebhookSecret = %q", cfg.Stripe.WebhookSecret)
	}
	if cfg.Stripe.PriceIDMonthly != "price_m" || cfg.Stripe.PriceIDAnnual != "price_a" {
		t.Errorf("price IDs = %q/%q", cfg.Stripe.PriceIDMonthly, cfg.Stripe.PriceIDAnnual)
	}
}

func TestLoad_OpenRouter(t *testing.T) {
	t.Setenv("AI_PROVIDER", AIProviderOpenRouter)
	t.Setenv("OPENROUTER_API_KEY", "test-openrouter-key")
	t.Setenv("OPENROUTER_MODEL", "")
	t.Setenv("OPENROUTER_EMBEDDING_MODEL", "")
	t.Setenv("JWT_SECRET", "test-secret-test-secret-test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.Provider != AIProviderOpenRouter {
		t.Errorf("provider = %q", cfg.AI.Provider)
	}
	if cfg.AI.Model != "openrouter/auto" {
		t.Errorf("model = %q", cfg.AI.Model)
	}
	if cfg.AI.EmbeddingModel != "google/gemini-embedding-001" {
		t.Errorf("embedding model = %q", cfg.AI.EmbeddingModel)
	}
	if cfg.AI.EmbeddingDimension != 768 {
		t.Errorf("embedding dimension = %d", cfg.AI.EmbeddingDimension)
	}
}

func TestLoad_RejectsUnknownAIProvider(t *testing.T) {
	t.Setenv("AI_PROVIDER", "unknown")
	t.Setenv("JWT_SECRET", "test-secret-test-secret-test-secret")

	_, err := Load()
	if err == nil {
		t.Fatal("Load returned nil error")
	}
}

// baseAuthEnv sets the minimum needed for Load() to reach the auth checks.
func baseAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AI_PROVIDER", AIProviderGemini)
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("GEMINI_MODEL", "gemini-test")
	t.Setenv("JWT_SECRET", "test-secret-test-secret-test-secret")
	t.Setenv("JWT_REFRESH_SECRET", "")
	t.Setenv("JWT_ACCESS_TOKEN_TTL", "")
	t.Setenv("JWT_REFRESH_TOKEN_TTL", "")
	t.Setenv("APP_ENV", "development")
}

// Outside production a missing refresh secret falls back to the access secret
// so existing deployments keep booting.
func TestLoad_RefreshSecretFallsBackOutsideProduction(t *testing.T) {
	baseAuthEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.JWTRefreshSecret != cfg.Auth.JWTSecret {
		t.Errorf("expected the refresh secret to fall back to the access secret")
	}
}

// Sharing one key means an access token verifies as a refresh token, so a
// leaked access token buys a 30-day session. Production must refuse to boot.
func TestLoad_ProductionRejectsSharedJWTSecret(t *testing.T) {
	baseAuthEnv(t)
	t.Setenv("APP_ENV", "production")

	if _, err := Load(); err == nil {
		t.Fatal("expected Load to reject a shared JWT secret in production")
	}

	t.Setenv("JWT_REFRESH_SECRET", "different-refresh-secret-that-is-long-enough")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with a distinct refresh secret: %v", err)
	}
	if cfg.Auth.JWTRefreshSecret == cfg.Auth.JWTSecret {
		t.Error("refresh secret should not equal the access secret")
	}
}

// JWT_ACCESS_TOKEN_TTL / JWT_REFRESH_TOKEN_TTL sat in .env being ignored while
// the lifetimes were hardcoded, so the file lied about the running config.
func TestLoad_TokenTTLsComeFromEnv(t *testing.T) {
	baseAuthEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.AccessTokenTTL != time.Hour {
		t.Errorf("default access TTL = %s, want 1h", cfg.Auth.AccessTokenTTL)
	}
	if cfg.Auth.RefreshTokenTTL != 30*24*time.Hour {
		t.Errorf("default refresh TTL = %s, want 720h", cfg.Auth.RefreshTokenTTL)
	}

	t.Setenv("JWT_ACCESS_TOKEN_TTL", "15m")
	t.Setenv("JWT_REFRESH_TOKEN_TTL", "168h")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load with TTL overrides: %v", err)
	}
	if cfg.Auth.AccessTokenTTL != 15*time.Minute {
		t.Errorf("access TTL = %s, want 15m", cfg.Auth.AccessTokenTTL)
	}
	if cfg.Auth.RefreshTokenTTL != 168*time.Hour {
		t.Errorf("refresh TTL = %s, want 168h", cfg.Auth.RefreshTokenTTL)
	}
}

// A refresh token that expires before the access token it renews is a
// misconfiguration that would strand users mid-session.
func TestLoad_RejectsRefreshTTLShorterThanAccessTTL(t *testing.T) {
	baseAuthEnv(t)
	t.Setenv("JWT_ACCESS_TOKEN_TTL", "24h")
	t.Setenv("JWT_REFRESH_TOKEN_TTL", "1h")

	if _, err := Load(); err == nil {
		t.Fatal("expected Load to reject a refresh TTL shorter than the access TTL")
	}
}
