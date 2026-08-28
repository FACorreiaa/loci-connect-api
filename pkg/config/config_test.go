package config

import (
	"strings"
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

// An unset AI_PROVIDER must default to OpenRouter, so a deployment that never
// sets the variable boots on OPENROUTER_API_KEY and never asks for a Gemini key.
func TestLoad_DefaultsToOpenRouter(t *testing.T) {
	t.Setenv("AI_PROVIDER", "")
	t.Setenv("OPENROUTER_API_KEY", "test-openrouter-key")
	t.Setenv("JWT_SECRET", "test-secret-test-secret-test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.Provider != AIProviderOpenRouter {
		t.Errorf("provider = %q, want %q", cfg.AI.Provider, AIProviderOpenRouter)
	}
	if cfg.AI.APIKey != "test-openrouter-key" {
		t.Errorf("api key = %q", cfg.AI.APIKey)
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
	// Satisfies the weather-licence guard; this test is about JWT secrets.
	t.Setenv("OPENMETEO_API_KEY", "paid-key")

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

// baseEnv sets the non-AI values Load requires so each fallback test can
// vary only the AI settings it cares about.
func baseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret-test-secret-test-secret")
	t.Setenv("APP_ENV", "development")
	t.Setenv("AI_PROVIDER", AIProviderOpenRouter)
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("AI_FALLBACK_OPENROUTER_API_KEY", "")
	t.Setenv("AI_FALLBACK_ENABLED", "")
	t.Setenv("AI_FALLBACK_MODELS", "")
}

// The headline requirement: the app must boot and stay usable with no
// primary provider key configured at all, so it is testable locally
// without an OpenRouter account.
func TestLoad_NoPrimaryKeyBootsOnFallback(t *testing.T) {
	baseEnv(t)
	t.Setenv("AI_FALLBACK_OPENROUTER_API_KEY", "fallback-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with fallback only: %v", err)
	}
	if cfg.AI.APIKey != "" {
		t.Fatalf("primary APIKey = %q, want empty", cfg.AI.APIKey)
	}
	if !cfg.AI.FallbackEnabled {
		t.Fatal("FallbackEnabled = false, want true outside production")
	}
	if len(cfg.AI.Fallbacks) != len(defaultFallbackModels) {
		t.Fatalf("Fallbacks = %d, want %d", len(cfg.AI.Fallbacks), len(defaultFallbackModels))
	}
	if got := cfg.AI.Fallbacks[0].Model; got != defaultFallbackModels[0] {
		t.Fatalf("first fallback model = %q, want %q", got, defaultFallbackModels[0])
	}
	for i, spec := range cfg.AI.Fallbacks {
		if spec.APIKey != "fallback-key" {
			t.Fatalf("fallback[%d] APIKey = %q, want the fallback key", i, spec.APIKey)
		}
		if spec.Provider != AIProviderOpenRouter {
			t.Fatalf("fallback[%d] Provider = %q, want openrouter", i, spec.Provider)
		}
	}
}

func TestLoad_NoKeyAndNoFallbackStillFails(t *testing.T) {
	baseEnv(t)
	t.Setenv("AI_FALLBACK_ENABLED", "false")

	if _, err := Load(); err == nil {
		t.Fatal("Load: want error when neither a primary key nor a fallback is configured")
	}
}

func TestLoad_FallbackReusesPrimaryKeyWhenUnset(t *testing.T) {
	baseEnv(t)
	t.Setenv("OPENROUTER_API_KEY", "primary-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AI.Fallbacks) == 0 {
		t.Fatal("want fallbacks derived from the primary key")
	}
	if got := cfg.AI.Fallbacks[0].APIKey; got != "primary-key" {
		t.Fatalf("fallback APIKey = %q, want the primary key", got)
	}
}

func TestLoad_FallbackModelsOverride(t *testing.T) {
	baseEnv(t)
	t.Setenv("OPENROUTER_API_KEY", "primary-key")
	t.Setenv("AI_FALLBACK_MODELS", "a/one:free, b/two:free")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"a/one:free", "b/two:free"}
	if len(cfg.AI.Fallbacks) != len(want) {
		t.Fatalf("Fallbacks = %d, want %d", len(cfg.AI.Fallbacks), len(want))
	}
	for i, w := range want {
		if got := cfg.AI.Fallbacks[i].Model; got != w {
			t.Fatalf("fallback[%d] = %q, want %q (whitespace must be trimmed)", i, got, w)
		}
	}
}

// Production must not silently serve from the shared free tier.
func TestLoad_ProductionRejectsFallback(t *testing.T) {
	baseEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-refresh-secret-refresh")
	t.Setenv("OPENROUTER_API_KEY", "primary-key")
	t.Setenv("AI_FALLBACK_ENABLED", "true")

	if _, err := Load(); err == nil {
		t.Fatal("Load: want error when fallback is enabled in production")
	}
}

func TestLoad_ProductionRejectsFreePrimaryModel(t *testing.T) {
	baseEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-refresh-secret-refresh")
	t.Setenv("OPENROUTER_API_KEY", "primary-key")
	t.Setenv("OPENROUTER_MODEL", "z-ai/glm-5.2:free")
	t.Setenv("AI_FALLBACK_ENABLED", "false")

	if _, err := Load(); err == nil {
		t.Fatal("Load: want error when the production primary model is a :free model")
	}
}

func TestLoad_ProductionDefaultsFallbackOff(t *testing.T) {
	baseEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-refresh-secret-refresh")
	t.Setenv("OPENROUTER_API_KEY", "primary-key")
	// Satisfies the weather-licence guard; this test is about the AI fallback.
	t.Setenv("OPENMETEO_API_KEY", "paid-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.FallbackEnabled {
		t.Fatal("FallbackEnabled = true in production, want false by default")
	}
	if len(cfg.AI.Fallbacks) != 0 {
		t.Fatalf("Fallbacks = %d in production, want 0", len(cfg.AI.Fallbacks))
	}
}

// --- weather provider licence guard ----------------------------------------
//
// Open-Meteo's free tier is non-commercial by their terms, and they name "apps
// that have subscriptions" as commercial use. Loci sells a Pro plan, so a
// production boot on the free tier is a licence breach — and a silent one,
// because the API keeps answering. These tests exist so it cannot be silent.

// prodEnv sets the minimum needed to reach the production guards.
func prodEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("AI_PROVIDER", AIProviderOpenRouter)
	t.Setenv("OPENROUTER_API_KEY", "test-openrouter-key")
	t.Setenv("OPENROUTER_MODEL", "openai/gpt-4o-mini")
	t.Setenv("OPENROUTER_EMBEDDING_MODEL", "google/gemini-embedding-001")
	t.Setenv("JWT_SECRET", "test-secret-test-secret-test-secret")
	t.Setenv("JWT_REFRESH_SECRET", "test-refresh-secret-test-refresh-secret")
	t.Setenv("AI_FALLBACK_ENABLED", "false")
	// Clear anything inherited so each case states its own weather config.
	t.Setenv("OPENMETEO_API_KEY", "")
	t.Setenv("OPENWEATHER_API_KEY", "")
	t.Setenv("WEATHER_PROVIDER", "")
	t.Setenv("AIR_QUALITY_ENABLED", "")
}

func TestLoad_ProductionRejectsFreeOpenMeteo(t *testing.T) {
	prodEnv(t)

	_, err := Load()
	if err == nil {
		t.Fatal("expected production to refuse the non-commercial free tier")
	}
	if !strings.Contains(err.Error(), "OPENMETEO_API_KEY") {
		t.Errorf("the error must name the way out, got %q", err)
	}
}

// A paid key grants a commercial licence, so it satisfies the guard outright.
func TestLoad_ProductionAllowsPaidOpenMeteo(t *testing.T) {
	prodEnv(t)
	t.Setenv("OPENMETEO_API_KEY", "paid-key")

	if _, err := Load(); err != nil {
		t.Fatalf("a paid key must be accepted: %v", err)
	}
}

// Air quality follows the weather provider, so OpenWeather now covers both and
// air quality no longer has to be switched off to stay licence-clean.
func TestLoad_ProductionAllowsOpenWeatherWithAirQualityOn(t *testing.T) {
	prodEnv(t)
	t.Setenv("WEATHER_PROVIDER", "openweather")
	t.Setenv("OPENWEATHER_API_KEY", "ow-key")
	t.Setenv("AIR_QUALITY_ENABLED", "true")

	if _, err := Load(); err != nil {
		t.Fatalf("openweather serves air quality too: %v", err)
	}
}

// Selecting openweather without a key is the dangerous case: the adapter falls
// back to Open-Meteo, so the config reads clean while the free tier is called.
func TestLoad_ProductionRejectsOpenWeatherWithoutAKey(t *testing.T) {
	prodEnv(t)
	t.Setenv("WEATHER_PROVIDER", "openweather")
	t.Setenv("OPENWEATHER_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected the silent fallback to Open-Meteo to be rejected")
	}
	if !strings.Contains(err.Error(), "OPENWEATHER_API_KEY") {
		t.Errorf("the error must name the missing key, got %q", err)
	}
}

// The stub makes no external call, but air quality would still reach
// Open-Meteo, because it only follows openweather.
func TestLoad_ProductionStubStillGuardsAirQuality(t *testing.T) {
	prodEnv(t)
	t.Setenv("WEATHER_PROVIDER", "stub")
	t.Setenv("AIR_QUALITY_ENABLED", "true")

	if _, err := Load(); err == nil {
		t.Fatal("expected air quality to be caught behind the stub provider")
	}

	t.Setenv("AIR_QUALITY_ENABLED", "false")
	if _, err := Load(); err != nil {
		t.Fatalf("stub with air quality off must boot: %v", err)
	}
}

func TestLoad_ProductionAllowsOpenWeatherWithAirQualityOff(t *testing.T) {
	prodEnv(t)
	t.Setenv("WEATHER_PROVIDER", "openweather")
	t.Setenv("OPENWEATHER_API_KEY", "ow-key")
	t.Setenv("AIR_QUALITY_ENABLED", "false")

	if _, err := Load(); err != nil {
		t.Fatalf("openweather with air quality off must boot: %v", err)
	}
}

// An unset WEATHER_PROVIDER with an OpenWeather key already present resolves to
// openweather, matching NewWeatherAdapterFromEnv. The guard must agree with the
// adapter, or it would reject a deployment that never touches Open-Meteo.
func TestLoad_ProductionUnsetProviderFollowsTheOpenWeatherKey(t *testing.T) {
	prodEnv(t)
	t.Setenv("OPENWEATHER_API_KEY", "ow-key")
	t.Setenv("AIR_QUALITY_ENABLED", "false")

	if _, err := Load(); err != nil {
		t.Fatalf("an existing OpenWeather key implies openweather: %v", err)
	}
}

// Development is not commercial use, so none of this applies outside production.
func TestLoad_DevelopmentAllowsFreeOpenMeteo(t *testing.T) {
	prodEnv(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("AI_FALLBACK_ENABLED", "")

	if _, err := Load(); err != nil {
		t.Fatalf("the free tier is fine outside production: %v", err)
	}
}
