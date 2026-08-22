package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	// Load environment variables from .env files when present.
	_ "github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Auth          AuthConfig
	Subscription  SubscriptionConfig
	Stripe        StripeConfig
	Cache         CacheConfig
	Observability ObservabilityConfig
	Profiling     ProfilingConfig
	AI            AIConfig
}

type CacheConfig struct {
	RedisURL   string
	KeyPrefix  string
	LLMTTL     time.Duration
	GeoTTL     time.Duration
	CleanupTTL time.Duration
}

const (
	AIProviderGemini     = "gemini"
	AIProviderOpenRouter = "openrouter"
)

// defaultFallbackModels are OpenRouter's zero-cost models, ordered by
// suitability. Both advertise structured_outputs and response_format,
// which Loci's JSON-contract prompts require; the larger free models
// (nemotron-3-ultra, nemotron-3.5-lightning) do not, so they are
// deliberately excluded despite bigger context windows.
var defaultFallbackModels = []string{
	"z-ai/glm-5.2:free",
	"nvidia/nemotron-3-super-120b-a12b:free",
}

// AIProviderSpec identifies one link in the chat fallback chain.
type AIProviderSpec struct {
	Provider string
	APIKey   string
	Model    string
}

// AIConfig holds provider-neutral chat and embedding configuration.
//
// Provider/APIKey/Model describe the primary chat provider. When
// FallbackEnabled is set, Fallbacks lists additional providers tried in
// order after the primary fails with a provider-side error, so the app
// keeps working without a funded key. Embeddings never fall back: no
// free embedding model exists, so retrieval degrades to lexical search
// instead.
type AIConfig struct {
	Provider           string
	APIKey             string
	Model              string
	FallbackEnabled    bool
	Fallbacks          []AIProviderSpec
	FallbackCooldown   time.Duration
	EmbeddingModel     string
	EmbeddingDimension int
	MaxConcurrentCalls int
	MaxRetries         int
	RetryBaseDelay     time.Duration
	RetryMaxDelay      time.Duration
	// GenerateTimeout caps a single non-streaming LLM call (including retries).
	GenerateTimeout time.Duration
	// StreamTimeout caps a full streaming LLM call from start to last chunk.
	StreamTimeout time.Duration
}

type ServerConfig struct {
	Host                    string
	Port                    int
	BaseURL                 string
	RateLimitPerSecond      int
	RateLimitBurst          int
	IPRateLimitPerSecond    int
	IPRateLimitBurst        int
	IPRateLimitMaxEntries   int
	UserRateLimitPerSecond  int
	UserRateLimitBurst      int
	UserRateLimitMaxEntries int
	DefaultRPCTimeout       time.Duration
	ChatRPCTimeout          time.Duration
	// ChatStreamMaxTimeout caps client-requested deadlines on streaming RPCs.
	ChatStreamMaxTimeout time.Duration
	// AllowedOrigins are the CORS origins permitted for browser clients.
	AllowedOrigins []string
}

type DatabaseConfig struct {
	Host            string
	Port            int
	User            string
	Password        string
	Database        string
	SSLMode         string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

type AuthConfig struct {
	JWTSecret string
	// JWTRefreshSecret signs refresh tokens. It must differ from JWTSecret:
	// with one shared key an access token verifies as a refresh token, so a
	// leaked access token could be exchanged for a fresh pair. Defaults to
	// JWTSecret so existing deployments keep booting, but production refuses
	// to start when the two are equal.
	JWTRefreshSecret string
	// Token lifetimes. These used to be hardcoded in dependencies.go while the
	// env vars sat in .env doing nothing, so the running config did not match
	// what the file claimed.
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	AdminEmail      string

	// MFASecretKey encrypts TOTP secrets at rest. Must be exactly 32 bytes.
	// Empty disables MFA entirely rather than storing secrets in plaintext —
	// see internal/domain/mfa.
	MFASecretKey string
	// MFARequiredForRole is a comma-separated list of roles that cannot turn MFA
	// off, e.g. "admin,owner". Empty means MFA is optional for everyone.
	MFARequiredForRole string
}

// SubscriptionConfig holds daily LLM request quotas per plan tier.
// ProDailyLLMLimit is a hidden fair-use cap; Pro is marketed as unlimited.
type SubscriptionConfig struct {
	FreeDailyLLMLimit int
	ProDailyLLMLimit  int
}

// StripeConfig holds Stripe API credentials and the two Pro price IDs.
type StripeConfig struct {
	APIKey         string
	WebhookSecret  string
	PriceIDMonthly string
	PriceIDAnnual  string
}

type ObservabilityConfig struct {
	MetricsEnabled bool
	MetricsPort    int
}

type ProfilingConfig struct {
	Enabled bool
	Port    int
}

// Load reads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Host:                    getEnv("SERVER_HOST", "localhost"),
			Port:                    getEnvAsInt("SERVER_PORT", 8080),
			BaseURL:                 getEnv("BASE_URL", "http://localhost:8080"),
			RateLimitPerSecond:      getEnvAsInt("SERVER_RATE_LIMIT_PER_SECOND", 100),
			RateLimitBurst:          getEnvAsInt("SERVER_RATE_LIMIT_BURST", 200),
			IPRateLimitPerSecond:    getEnvAsInt("SERVER_IP_RATE_LIMIT_PER_SECOND", 30),
			IPRateLimitBurst:        getEnvAsInt("SERVER_IP_RATE_LIMIT_BURST", 60),
			IPRateLimitMaxEntries:   getEnvAsInt("SERVER_IP_RATE_LIMIT_MAX_ENTRIES", 10_000),
			UserRateLimitPerSecond:  getEnvAsInt("SERVER_USER_RATE_LIMIT_PER_SECOND", 20),
			UserRateLimitBurst:      getEnvAsInt("SERVER_USER_RATE_LIMIT_BURST", 40),
			UserRateLimitMaxEntries: getEnvAsInt("SERVER_USER_RATE_LIMIT_MAX_ENTRIES", 10_000),
			DefaultRPCTimeout:       getEnvAsDurationSeconds("DEFAULT_RPC_TIMEOUT_SEC", 30*time.Second),
			ChatRPCTimeout:          getEnvAsDurationSeconds("CHAT_RPC_TIMEOUT_SEC", 3*time.Minute),
			ChatStreamMaxTimeout:    getEnvAsDurationSeconds("CHAT_STREAM_MAX_TIMEOUT_SEC", 10*time.Minute),
			AllowedOrigins:          getEnvAsSlice("ALLOWED_ORIGINS", []string{"http://localhost:3000"}),
		},
		Database: DatabaseConfig{
			Host:            getEnv("DB_HOST", "localhost"),
			Port:            getEnvAsInt("DB_PORT", 5439),
			User:            getEnv("DB_USER", "postgres"),
			Password:        getEnv("DB_PASSWORD", "postgres"),
			Database:        getEnv("DB_NAME", "loci"),
			SSLMode:         getEnv("DB_SSLMODE", "disable"),
			MaxConns:        int32(getEnvAsInt("DB_MAX_CONNS", 25)),
			MinConns:        int32(getEnvAsInt("DB_MIN_CONNS", 5)),
			MaxConnLifetime: getEnvAsDurationSeconds("DB_MAX_CONN_LIFETIME_SEC", 5*time.Minute),
			MaxConnIdleTime: getEnvAsDurationSeconds("DB_MAX_CONN_IDLE_SEC", 10*time.Minute),
		},
		Auth: AuthConfig{
			JWTSecret:        getEnv("JWT_SECRET", "changeme"),
			JWTRefreshSecret: getEnv("JWT_REFRESH_SECRET", ""),
			AccessTokenTTL:   getEnvAsDuration("JWT_ACCESS_TOKEN_TTL", time.Hour),
			RefreshTokenTTL:  getEnvAsDuration("JWT_REFRESH_TOKEN_TTL", 30*24*time.Hour),
			AdminEmail:       getEnv("ADMIN_EMAIL", ""),

			MFASecretKey:       getEnv("MFA_SECRET_KEY", ""),
			MFARequiredForRole: getEnv("MFA_REQUIRED_FOR_ROLE", ""),
		},
		Subscription: SubscriptionConfig{
			FreeDailyLLMLimit: getEnvAsInt("FREE_DAILY_LLM_LIMIT", 10),
			ProDailyLLMLimit:  getEnvAsInt("PRO_DAILY_LLM_LIMIT", 300),
		},
		Stripe: StripeConfig{
			APIKey:         getEnv("STRIPE_API_KEY", ""),
			WebhookSecret:  getEnv("STRIPE_WEBHOOK_SECRET", ""),
			PriceIDMonthly: getEnv("STRIPE_PRICE_ID_MONTHLY", ""),
			PriceIDAnnual:  getEnv("STRIPE_PRICE_ID_ANNUAL", ""),
		},
		Cache: CacheConfig{
			RedisURL:   getEnv("REDIS_URL", ""),
			KeyPrefix:  getEnv("CACHE_KEY_PREFIX", "loci:"),
			LLMTTL:     getEnvAsDurationSeconds("CACHE_LLM_TTL_SEC", 5*time.Minute),
			GeoTTL:     getEnvAsDurationSeconds("CACHE_GEO_TTL_SEC", 20*time.Minute),
			CleanupTTL: getEnvAsDurationSeconds("CACHE_CLEANUP_TTL_SEC", 10*time.Minute),
		},
		Observability: ObservabilityConfig{
			MetricsEnabled: getEnvAsBool("METRICS_ENABLED", true),
			MetricsPort:    getEnvAsInt("METRICS_PORT", 9090),
		},
		Profiling: ProfilingConfig{
			Enabled: getEnvAsBool("PPROF_ENABLED", false),
			Port:    getEnvAsInt("PPROF_PORT", 6060),
		},
		AI: loadAIConfig(),
	}

	if cfg.AI.Provider != AIProviderGemini && cfg.AI.Provider != AIProviderOpenRouter {
		return nil, fmt.Errorf("unsupported AI_PROVIDER %q", cfg.AI.Provider)
	}
	// A missing primary key is survivable only when the fallback chain can
	// answer in its place. That is the whole point of the chain: the app
	// must stay testable with no provider account configured at all.
	if cfg.AI.APIKey == "" && len(cfg.AI.Fallbacks) == 0 {
		return nil, fmt.Errorf("%s is required", providerAPIKeyEnv(cfg.AI.Provider))
	}

	if cfg.AI.Model == "" && cfg.AI.APIKey != "" {
		return nil, fmt.Errorf("%s is required", providerModelEnv(cfg.AI.Provider))
	}

	// Guard against a dev default silently becoming the production model.
	// Free models are rate-limited and shared; they are a local testing
	// floor, never a production serving path.
	if IsProduction() {
		if strings.HasSuffix(cfg.AI.Model, ":free") {
			return nil, fmt.Errorf("%s must not be a :free model in production, got %q",
				providerModelEnv(cfg.AI.Provider), cfg.AI.Model)
		}
		if cfg.AI.FallbackEnabled {
			return nil, errors.New("AI_FALLBACK_ENABLED must be false in production")
		}
	}
	if cfg.AI.EmbeddingModel == "" {
		return nil, fmt.Errorf("%s is required", providerEmbeddingModelEnv(cfg.AI.Provider))
	}
	if cfg.AI.EmbeddingDimension <= 0 {
		return nil, errors.New("AI_EMBEDDING_DIMENSION must be positive")
	}
	if cfg.AI.MaxRetries < 0 {
		return nil, errors.New("AI_MAX_RETRIES must be >= 0")
	}
	if cfg.AI.RetryBaseDelay < 0 || cfg.AI.RetryMaxDelay < 0 {
		return nil, errors.New("AI retry delays must be non-negative")
	}
	if cfg.AI.RetryMaxDelay > 0 && cfg.AI.RetryBaseDelay > cfg.AI.RetryMaxDelay {
		return nil, errors.New("AI_RETRY_BASE_DELAY must not exceed AI_RETRY_MAX_DELAY")
	}
	if cfg.Server.DefaultRPCTimeout <= 0 || cfg.Server.ChatRPCTimeout <= 0 {
		return nil, errors.New("RPC timeout values must be positive")
	}
	if cfg.AI.GenerateTimeout <= 0 || cfg.AI.StreamTimeout <= 0 {
		return nil, errors.New("AI timeout values must be positive")
	}

	if cfg.Auth.JWTSecret == "" {
		return nil, errors.New("JWT_SECRET is required")
	}
	// Refuse to boot with a known-insecure placeholder secret in ANY environment.
	// A weak secret in dev leaks into shared/staging deployments and forged tokens
	// are indistinguishable from real ones, so this is not production-only.
	switch cfg.Auth.JWTSecret {
	case "changeme", "replace-with-secure-env-var", "replace-with-secure-refresh-env-var":
		return nil, errors.New("JWT_SECRET must not be a default/placeholder value")
	}
	if len(cfg.Auth.JWTSecret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 characters")
	}

	// Fall back to the access secret so existing deployments keep working, but
	// say so, and refuse the shared-key setup outright in production.
	if cfg.Auth.JWTRefreshSecret == "" {
		cfg.Auth.JWTRefreshSecret = cfg.Auth.JWTSecret
	}
	if cfg.Auth.JWTRefreshSecret == cfg.Auth.JWTSecret && strings.EqualFold(getEnv("APP_ENV", "development"), "production") {
		return nil, errors.New("JWT_REFRESH_SECRET must be set and must differ from JWT_SECRET in production")
	}
	if len(cfg.Auth.JWTRefreshSecret) < 32 {
		return nil, errors.New("JWT_REFRESH_SECRET must be at least 32 characters")
	}

	if cfg.Auth.AccessTokenTTL <= 0 || cfg.Auth.RefreshTokenTTL <= 0 {
		return nil, errors.New("JWT token TTLs must be positive")
	}
	if cfg.Auth.RefreshTokenTTL <= cfg.Auth.AccessTokenTTL {
		return nil, errors.New("JWT_REFRESH_TOKEN_TTL must be longer than JWT_ACCESS_TOKEN_TTL")
	}

	return cfg, nil
}

func loadAIConfig() AIConfig {
	provider := strings.ToLower(strings.TrimSpace(getEnv("AI_PROVIDER", AIProviderOpenRouter)))
	cfg := AIConfig{
		Provider:           provider,
		EmbeddingDimension: getEnvAsInt("AI_EMBEDDING_DIMENSION", 768),
		MaxConcurrentCalls: getEnvAsIntFallback("AI_MAX_CONCURRENT_CALLS", "GEMINI_MAX_CONCURRENT_CALLS", 10),
		MaxRetries:         getEnvAsIntFallback("AI_MAX_RETRIES", "GEMINI_MAX_RETRIES", 3),
		RetryBaseDelay:     getEnvAsDurationMillisFallback("AI_RETRY_BASE_DELAY_MS", "GEMINI_RETRY_BASE_DELAY_MS", 500*time.Millisecond),
		RetryMaxDelay:      getEnvAsDurationMillisFallback("AI_RETRY_MAX_DELAY_MS", "GEMINI_RETRY_MAX_DELAY_MS", 8*time.Second),
		GenerateTimeout:    getEnvAsDurationSecondsFallback("AI_GENERATE_TIMEOUT_SEC", "GEMINI_GENERATE_TIMEOUT_SEC", 30*time.Second),
		StreamTimeout:      getEnvAsDurationSecondsFallback("AI_STREAM_TIMEOUT_SEC", "GEMINI_STREAM_TIMEOUT_SEC", 2*time.Minute),
	}

	switch provider {
	case AIProviderGemini:
		cfg.APIKey = getEnv("GEMINI_API_KEY", "")
		cfg.Model = getEnv("GEMINI_MODEL", "")
		cfg.EmbeddingModel = getEnv("GEMINI_EMBEDDING_MODEL", "gemini-embedding-001")
	case AIProviderOpenRouter:
		cfg.APIKey = getEnv("OPENROUTER_API_KEY", "")
		cfg.Model = getEnv("OPENROUTER_MODEL", "openrouter/auto")
		cfg.EmbeddingModel = getEnv("OPENROUTER_EMBEDDING_MODEL", "google/gemini-embedding-001")
	default:
		cfg.Provider = provider
	}

	cfg.FallbackEnabled = getEnvAsBool("AI_FALLBACK_ENABLED", !IsProduction())
	cfg.FallbackCooldown = getEnvAsDurationSeconds("AI_FALLBACK_COOLDOWN_SEC", 5*time.Minute)
	if cfg.FallbackEnabled {
		cfg.Fallbacks = loadFallbacks()
	}

	return cfg
}

// loadFallbacks builds the ordered fallback chain. Every entry is an
// OpenRouter model: the free tier is the only zero-cost provider wired
// into the app, and its key is read separately so it can be a spend-
// capped key distinct from the primary one.
func loadFallbacks() []AIProviderSpec {
	key := getEnv("AI_FALLBACK_OPENROUTER_API_KEY", getEnv("OPENROUTER_API_KEY", ""))
	if key == "" {
		return nil
	}

	models := getEnvAsSlice("AI_FALLBACK_MODELS", defaultFallbackModels)
	specs := make([]AIProviderSpec, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		specs = append(specs, AIProviderSpec{
			Provider: AIProviderOpenRouter,
			APIKey:   key,
			Model:    model,
		})
	}
	return specs
}

// IsProduction reports whether the process is running as production.
func IsProduction() bool {
	return strings.EqualFold(getEnv("APP_ENV", "development"), "production")
}

func providerAPIKeyEnv(provider string) string {
	if provider == AIProviderOpenRouter {
		return "OPENROUTER_API_KEY"
	}
	return "GEMINI_API_KEY"
}

func providerModelEnv(provider string) string {
	if provider == AIProviderOpenRouter {
		return "OPENROUTER_MODEL"
	}
	return "GEMINI_MODEL"
}

func providerEmbeddingModelEnv(provider string) string {
	if provider == AIProviderOpenRouter {
		return "OPENROUTER_EMBEDDING_MODEL"
	}
	return "GEMINI_EMBEDDING_MODEL"
}

// getEnvAsSlice reads a comma-separated env var into a string slice.
func getEnvAsSlice(key string, defaultValue []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return defaultValue
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return defaultValue
	}
	return out
}

// DSN returns the database connection string
func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode,
	)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return defaultValue
}

func getEnvAsIntFallback(key, legacyKey string, defaultValue int) int {
	if os.Getenv(key) != "" {
		return getEnvAsInt(key, defaultValue)
	}
	return getEnvAsInt(legacyKey, defaultValue)
}

func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if value, err := strconv.ParseBool(valueStr); err == nil {
		return value
	}
	return defaultValue
}

// getEnvAsDuration parses a Go duration string ("15m", "168h"). Unlike the
// *Seconds helpers it takes the unit from the value, which is what the
// JWT_*_TTL variables have always been written as.
func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(valueStr)
	if err != nil || d <= 0 {
		return defaultValue
	}
	return d
}

func getEnvAsDurationSeconds(key string, defaultValue time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	seconds, err := strconv.Atoi(valueStr)
	if err != nil || seconds <= 0 {
		return defaultValue
	}
	return time.Duration(seconds) * time.Second
}

func getEnvAsDurationSecondsFallback(key, legacyKey string, defaultValue time.Duration) time.Duration {
	if os.Getenv(key) != "" {
		return getEnvAsDurationSeconds(key, defaultValue)
	}
	return getEnvAsDurationSeconds(legacyKey, defaultValue)
}

func getEnvAsDurationMillis(key string, defaultValue time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	millis, err := strconv.Atoi(valueStr)
	if err != nil || millis < 0 {
		return defaultValue
	}
	return time.Duration(millis) * time.Millisecond
}

func getEnvAsDurationMillisFallback(key, legacyKey string, defaultValue time.Duration) time.Duration {
	if os.Getenv(key) != "" {
		return getEnvAsDurationMillis(key, defaultValue)
	}
	return getEnvAsDurationMillis(legacyKey, defaultValue)
}
