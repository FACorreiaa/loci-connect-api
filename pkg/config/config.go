package config

import (
	"fmt"
	_ "github.com/joho/godotenv"
	"os"
	"strconv"
)

// Config holds all application configuration
type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Auth          AuthConfig
	Observability ObservabilityConfig
	Profiling     ProfilingConfig
}

type ServerConfig struct {
	Host               string
	Port               int
	RateLimitPerSecond int
	RateLimitBurst     int
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
	JWTSecret string
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
			RateLimitPerSecond: getEnvAsInt("SERVER_RATE_LIMIT_PER_SECOND", 100),
			RateLimitBurst:     getEnvAsInt("SERVER_RATE_LIMIT_BURST", 200),
		},
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnvAsInt("DB_PORT", 5438),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			Database: getEnv("DB_NAME", "loci"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
		},
		Auth: AuthConfig{
			JWTSecret: getEnv("JWT_SECRET", "changeme"),
		},
		Observability: ObservabilityConfig{
			MetricsEnabled: getEnvAsBool("METRICS_ENABLED", true),
			MetricsPort:    getEnvAsInt("METRICS_PORT", 9090),
		},
		Profiling: ProfilingConfig{
			Enabled: getEnvAsBool("PPROF_ENABLED", false),
			Port:    getEnvAsInt("PPROF_PORT", 6060),
		},
	}

	return cfg, nil
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
