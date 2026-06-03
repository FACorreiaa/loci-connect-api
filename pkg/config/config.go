package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	// Load environment variables from .env files when present.
	_ "github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Auth          AuthConfig
	Observability ObservabilityConfig
	Profiling     ProfilingConfig
	Gemini        GeminiConfig
}

type GeminiConfig struct {
	APIKey string
	Model  string
}

type ServerConfig struct {
	Host               string
	Port               int
	BaseURL            string
	RateLimitPerSecond int
	RateLimitBurst     int
	// AllowedOrigins are the CORS origins permitted for browser clients.
	AllowedOrigins []string
}

type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

type AuthConfig struct {
	JWTSecret  string
	AdminEmail string
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
			Host:               getEnv("SERVER_HOST", "localhost"),
			Port:               getEnvAsInt("SERVER_PORT", 8080),
			BaseURL:            getEnv("BASE_URL", "http://localhost:8080"),
			RateLimitPerSecond: getEnvAsInt("SERVER_RATE_LIMIT_PER_SECOND", 100),
			RateLimitBurst:     getEnvAsInt("SERVER_RATE_LIMIT_BURST", 200),
			AllowedOrigins:     getEnvAsSlice("ALLOWED_ORIGINS", []string{"http://localhost:3000"}),
		},
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnvAsInt("DB_PORT", 5439),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			Database: getEnv("DB_NAME", "loci"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
		},
		Auth: AuthConfig{
			JWTSecret:  getEnv("JWT_SECRET", "changeme"),
			AdminEmail: getEnv("ADMIN_EMAIL", ""),
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
		},
	}

	if cfg.Gemini.APIKey == "" {
		return nil, errors.New("GEMINI_API_KEY is required")
	}

	if cfg.Gemini.Model == "" {
		return nil, errors.New("GEMINI_MODEL is required")
	}

	if cfg.Auth.JWTSecret == "" {
		return nil, errors.New("JWT_SECRET is required")
	}
	// In production, refuse to boot with the insecure default secret.
	if getEnv("APP_ENV", "development") == "production" && cfg.Auth.JWTSecret == "changeme" {
		return nil, errors.New("JWT_SECRET must not be the default value in production")
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
