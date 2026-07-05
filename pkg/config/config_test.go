package config

import "testing"

func TestLoad_QuotaAndStripeDefaults(t *testing.T) {
	// Required values so Load passes validation regardless of host env.
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
