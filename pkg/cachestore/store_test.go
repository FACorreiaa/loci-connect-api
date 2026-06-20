package cachestore

import (
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestTieredStore_MemoryOnly(t *testing.T) {
	store, err := New(Config{}, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.Close() }()

	store.Set("k", "v", 0)
	got, ok := store.Get("k")
	if !ok || got != "v" {
		t.Fatalf("Get = (%v, %v), want (v, true)", got, ok)
	}
}

func TestTieredStore_NonStringNotMirroredToRedis(t *testing.T) {
	store, err := New(Config{}, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.Close() }()

	slice := []int{1, 2, 3}
	store.Set("nums", slice, time.Minute)
	got, ok := store.Get("nums")
	if !ok {
		t.Fatal("expected memory hit for non-string value")
	}
	if _, ok := got.([]int); !ok {
		t.Fatalf("got type %T, want []int", got)
	}
}

func TestTieredStore_RedisRoundTrip(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := Config{
		RedisURL:  "redis://" + mr.Addr() + "/0",
		KeyPrefix: "test:",
		LLMTTL:    time.Minute,
	}

	writer, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatalf("New writer: %v", err)
	}
	defer func() { _ = writer.Close() }()

	writer.Set("shared-key", "shared-value", time.Minute)

	reader, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatalf("New reader: %v", err)
	}
	defer func() { _ = reader.Close() }()

	got, ok := reader.Get("shared-key")
	if !ok {
		t.Fatal("expected redis-backed cache hit on fresh in-memory tier")
	}
	if got != "shared-value" {
		t.Fatalf("Get = %v, want shared-value", got)
	}
}

func TestTieredStore_Close_NoRedis(t *testing.T) {
	store, err := New(Config{}, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}