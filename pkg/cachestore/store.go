package cachestore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
)

const (
	DefaultLLMTTL       = 5 * time.Minute
	DefaultGeoTTL       = 20 * time.Minute
	DefaultCleanupEvery = 10 * time.Minute
)

// Config controls in-process and optional Redis-backed caching.
type Config struct {
	RedisURL   string
	KeyPrefix  string
	LLMTTL     time.Duration
	GeoTTL     time.Duration
	CleanupTTL time.Duration
}

// Store is a small key/value cache used by chat and POI services.
type Store interface {
	Get(key string) (any, bool)
	Set(key string, value any, ttl time.Duration)
	Delete(key string)
	// Close releases Redis connections when configured; memory-only stores no-op.
	Close() error
}

// TieredStore keeps all values in memory and mirrors string payloads to Redis when configured.
// String values cover LLM stream responses and serialized nearby POI JSON for multi-instance deployments.
type TieredStore struct {
	memory      *cache.Cache
	redisClient *redis.Client
	prefix      string
	defaultTTL  time.Duration
	logger      *slog.Logger
}

// New builds a TieredStore. When RedisURL is empty, only in-memory caching is used.
func New(cfg Config, logger *slog.Logger) (Store, error) {
	if cfg.LLMTTL <= 0 {
		cfg.LLMTTL = DefaultLLMTTL
	}
	if cfg.GeoTTL <= 0 {
		cfg.GeoTTL = DefaultGeoTTL
	}
	if cfg.CleanupTTL <= 0 {
		cfg.CleanupTTL = DefaultCleanupEvery
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "loci:"
	}
	if logger == nil {
		logger = slog.Default()
	}

	store := &TieredStore{
		memory:     cache.New(cfg.LLMTTL, cfg.CleanupTTL),
		prefix:     cfg.KeyPrefix,
		defaultTTL: cfg.LLMTTL,
		logger:     logger,
	}

	if cfg.RedisURL == "" {
		logger.Info("cache using in-memory store only")
		return store, nil
	}

	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	store.redisClient = client
	logger.Info("cache using memory + redis tier", "prefix", cfg.KeyPrefix)
	return store, nil
}

// Close closes the Redis client when configured.
func (s *TieredStore) Close() error {
	if s.redisClient == nil {
		return nil
	}
	return s.redisClient.Close()
}

func (s *TieredStore) effectiveTTL(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	return s.defaultTTL
}

func (s *TieredStore) Get(key string) (any, bool) {
	if v, ok := s.memory.Get(key); ok {
		return v, true
	}
	if s.redisClient == nil {
		return nil, false
	}
	data, err := s.redisClient.Get(context.Background(), s.prefix+key).Bytes()
	if err != nil {
		return nil, false
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		s.logger.Warn("failed to decode redis cache entry", "key", key, "error", err)
		return nil, false
	}
	s.memory.Set(key, value, cache.DefaultExpiration)
	return value, true
}

func (s *TieredStore) Set(key string, value any, ttl time.Duration) {
	d := s.effectiveTTL(ttl)
	s.memory.Set(key, value, d)
	if s.redisClient == nil {
		return
	}
	str, ok := value.(string)
	if !ok {
		return
	}
	payload, err := json.Marshal(str)
	if err != nil {
		s.logger.Warn("failed to encode redis cache entry", "key", key, "error", err)
		return
	}
	if err := s.redisClient.Set(context.Background(), s.prefix+key, payload, d).Err(); err != nil {
		s.logger.Warn("failed to write redis cache entry", "key", key, "error", err)
	}
}

func (s *TieredStore) Delete(key string) {
	s.memory.Delete(key)
	if s.redisClient != nil {
		if err := s.redisClient.Del(context.Background(), s.prefix+key).Err(); err != nil {
			s.logger.Warn("failed to delete redis cache entry", "key", key, "error", err)
		}
	}
}