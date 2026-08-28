package localcontext

import (
	"log/slog"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
)

func newTestStore(t *testing.T) cachestore.Store {
	t.Helper()
	store, err := cachestore.New(cachestore.Config{}, slog.New(slog.NewTextHandler(discard{}, nil)))
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSignalCache_RoundTrips(t *testing.T) {
	c := newSignalCache(newTestStore(t), nil)

	cacheSet(c, "test", "k", []WeatherDay{{HighC: 21.5, Condition: "Rain"}}, time.Minute)

	got, ok := cacheGet[[]WeatherDay](c, "test", "k")
	if !ok {
		t.Fatal("expected a hit")
	}
	if len(got) != 1 || got[0].HighC != 21.5 || got[0].Condition != "Rain" {
		t.Errorf("got %+v", got)
	}
}

// Redis only mirrors string values, so payloads are JSON-encoded. A struct with
// unexported fields would round-trip as zeroes — silently, which is the danger.
func TestSignalCache_StoresStringsSoRedisCanMirror(t *testing.T) {
	store := newTestStore(t)
	c := newSignalCache(store, nil)

	cacheSet(c, "test", "k", map[string]int{"a": 1}, time.Minute)

	raw, ok := store.Get(cacheKeyFor("test", "k"))
	if !ok {
		t.Fatal("expected the value in the store")
	}
	if _, isString := raw.(string); !isString {
		t.Errorf("cached value must be a string for Redis mirroring, got %T", raw)
	}
}

func TestSignalCache_MissOnUnknownKey(t *testing.T) {
	c := newSignalCache(newTestStore(t), nil)
	if _, ok := cacheGet[[]WeatherDay](c, "test", "absent"); ok {
		t.Error("expected a miss")
	}
}

func TestSignalCache_NamespacesBySource(t *testing.T) {
	c := newSignalCache(newTestStore(t), nil)

	cacheSet(c, "gdacs", "k", []string{"gdacs"}, time.Minute)
	cacheSet(c, "usgs", "k", []string{"usgs"}, time.Minute)

	g, _ := cacheGet[[]string](c, "gdacs", "k")
	u, _ := cacheGet[[]string](c, "usgs", "k")
	if len(g) != 1 || g[0] != "gdacs" || len(u) != 1 || u[0] != "usgs" {
		t.Errorf("sources collided: %v / %v", g, u)
	}
}

func TestSignalCache_ExpiresAfterTTL(t *testing.T) {
	c := newSignalCache(newTestStore(t), nil)

	cacheSet(c, "test", "k", []string{"v"}, 20*time.Millisecond)
	if _, ok := cacheGet[[]string](c, "test", "k"); !ok {
		t.Fatal("expected a hit before the TTL")
	}

	// go-cache evicts lazily on read, so a real wait is the honest check.
	time.Sleep(60 * time.Millisecond)
	if _, ok := cacheGet[[]string](c, "test", "k"); ok {
		t.Error("expected a miss after the TTL")
	}
}

// A payload whose shape changed across a deploy must read as a miss, not a
// panic and not a zero value presented as real data.
func TestSignalCache_UndecodableValueIsAMiss(t *testing.T) {
	store := newTestStore(t)
	c := newSignalCache(store, nil)

	store.Set(cacheKeyFor("test", "k"), "not json at all", time.Minute)
	if _, ok := cacheGet[[]WeatherDay](c, "test", "k"); ok {
		t.Error("expected a miss for an undecodable payload")
	}
}

// A non-string value (something else wrote this key) must also read as a miss.
func TestSignalCache_NonStringValueIsAMiss(t *testing.T) {
	store := newTestStore(t)
	c := newSignalCache(store, nil)

	store.Set(cacheKeyFor("test", "k"), 42, time.Minute)
	if _, ok := cacheGet[int](c, "test", "k"); ok {
		t.Error("expected a miss for a non-string value")
	}
}

// Caching is optional: every adapter must still work with no store at all.
func TestSignalCache_NilIsSafe(t *testing.T) {
	var c *signalCache
	cacheSet(c, "test", "k", []string{"v"}, time.Minute)
	if _, ok := cacheGet[[]string](c, "test", "k"); ok {
		t.Error("a nil cache must always miss")
	}

	empty := newSignalCache(nil, nil)
	cacheSet(empty, "test", "k", []string{"v"}, time.Minute)
	if _, ok := cacheGet[[]string](empty, "test", "k"); ok {
		t.Error("a cache with no store must always miss")
	}
}
