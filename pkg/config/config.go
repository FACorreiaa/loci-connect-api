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
	Gemini        GeminiConfig
}

type CacheConfig struct {
	RedisURL   string
	KeyPrefix  string
	LLMTTL     time.Duration
	GeoTTL     time.Duration
	CleanupTTL time.Duration
}

type GeminiConfig struct {
	APIKey             string
	Model              string
	EmbeddingModel     string
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
	JWTSecret  string
	AdminEmail string
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
			JWTSecret:  getEnv("JWT_SECRET", "changeme"),
			AdminEmail: getEnv("ADMIN_EMAIL", ""),
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
		Gemini: GeminiConfig{
			APIKey: getEnv("GEMINI_API_KEY", ""),
			Model:  getEnv("GEMINI_MODEL", ""),
			// gemini-embedding-001 is the GA embedding model; the old
			// experimental gemini-embedding-exp-03-07 returns 404 upstream.
			EmbeddingModel:     getEnv("GEMINI_EMBEDDING_MODEL", "gemini-embedding-001"),
			MaxConcurrentCalls: getEnvAsInt("GEMINI_MAX_CONCURRENT_CALLS", 10),
			MaxRetries:         getEnvAsInt("GEMINI_MAX_RETRIES", 3),
			RetryBaseDelay:     getEnvAsDurationMillis("GEMINI_RETRY_BASE_DELAY_MS", 500*time.Millisecond),
			RetryMaxDelay:      getEnvAsDurationMillis("GEMINI_RETRY_MAX_DELAY_MS", 8*time.Second),
			GenerateTimeout:    getEnvAsDurationSeconds("GEMINI_GENERATE_TIMEOUT_SEC", 30*time.Second),
			StreamTimeout:      getEnvAsDurationSeconds("GEMINI_STREAM_TIMEOUT_SEC", 2*time.Minute),
		},
	}

	if cfg.Gemini.APIKey == "" {
		return nil, errors.New("GEMINI_API_KEY is required")
	}

	if cfg.Gemini.Model == "" {
		return nil, errors.New("GEMINI_MODEL is required")
	}
	if cfg.Gemini.MaxRetries < 0 {
		return nil, errors.New("GEMINI_MAX_RETRIES must be >= 0")
	}
	if cfg.Gemini.RetryBaseDelay < 0 || cfg.Gemini.RetryMaxDelay < 0 {
		return nil, errors.New("GEMINI retry delays must be non-negative")
	}
	if cfg.Gemini.RetryMaxDelay > 0 && cfg.Gemini.RetryBaseDelay > cfg.Gemini.RetryMaxDelay {
		return nil, errors.New("GEMINI_RETRY_BASE_DELAY must not exceed GEMINI_RETRY_MAX_DELAY")
	}
	if cfg.Server.DefaultRPCTimeout <= 0 || cfg.Server.ChatRPCTimeout <= 0 {
		return nil, errors.New("RPC timeout values must be positive")
	}
	if cfg.Gemini.GenerateTimeout <= 0 || cfg.Gemini.StreamTimeout <= 0 {
		return nil, errors.New("GEMINI timeout values must be positive")
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

	return cfg, nil
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

func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if value, err := strconv.ParseBool(valueStr); err == nil {
		return value
	}
	return defaultValue
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
