package service

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCacheStore(t *testing.T, cfg cachestore.Config) cachestore.Store {
	t.Helper()
	store, err := cachestore.New(cfg, slog.Default())
	require.NoError(t, err)
	return store
}

// TestCacheTTL verifies production cache defaults (5m LLM TTL).
func TestCacheTTL(t *testing.T) {
	c := newTestCacheStore(t, cachestore.Config{LLMTTL: cachestore.DefaultLLMTTL})

	c.Set("test-key", "test-value", 0)

	val, found := c.Get("test-key")
	assert.True(t, found)
	assert.Equal(t, "test-value", val)
}

// TestCacheKeyGeneration tests that cache keys are generated correctly based on content
func TestCacheKeyGeneration(t *testing.T) {
	userID := uuid.New()
	profileID := uuid.New()

	tests := []struct {
		name        string
		city        string
		message     string
		domain      string
		preferences string
		expectSame  bool
		compareWith int // index of test case to compare with (-1 for none)
	}{
		{
			name:        "Base case - Paris with Art preferences",
			city:        "Paris",
			message:     "Show me around",
			domain:      "itinerary",
			preferences: "Art & Museums",
			compareWith: -1,
		},
		{
			name:        "Same params should generate same key",
			city:        "Paris",
			message:     "Show me around",
			domain:      "itinerary",
			preferences: "Art & Museums",
			expectSame:  true,
			compareWith: 0,
		},
		{
			name:        "Different preferences should generate different key",
			city:        "Paris",
			message:     "Show me around",
			domain:      "itinerary",
			preferences: "Food & Nightlife",
			expectSame:  false,
			compareWith: 0,
		},
		{
			name:        "Different city should generate different key",
			city:        "London",
			message:     "Show me around",
			domain:      "itinerary",
			preferences: "Art & Museums",
			expectSame:  false,
			compareWith: 0,
		},
		{
			name:        "Different message should generate different key",
			city:        "Paris",
			message:     "Plan my trip",
			domain:      "itinerary",
			preferences: "Art & Museums",
			expectSame:  false,
			compareWith: 0,
		},
		{
			name:        "Different domain should generate different key",
			city:        "Paris",
			message:     "Show me around",
			domain:      "dining",
			preferences: "Art & Museums",
			expectSame:  false,
			compareWith: 0,
		},
	}

	keys := make([]string, len(tests))

	for i, tc := range tests {
		cacheKeyData := map[string]any{
			"user_id":     userID.String(),
			"profile_id":  profileID.String(),
			"city":        tc.city,
			"message":     tc.message,
			"domain":      tc.domain,
			"preferences": tc.preferences,
		}
		cacheKeyBytes, err := json.Marshal(cacheKeyData)
		require.NoError(t, err)
		hash := md5.Sum(cacheKeyBytes)
		keys[i] = hex.EncodeToString(hash[:])

		if tc.compareWith >= 0 {
			if tc.expectSame {
				assert.Equal(t, keys[tc.compareWith], keys[i], tc.name)
			} else {
				assert.NotEqual(t, keys[tc.compareWith], keys[i], tc.name)
			}
		}
	}
}

// TestCacheKeyUniquenessAcrossCombinations ensures unique keys for varied inputs
func TestCacheKeyUniquenessAcrossCombinations(t *testing.T) {
	userID := uuid.New()
	profileID := uuid.New()

	cities := []string{"Paris", "London", "Tokyo"}
	preferences := []string{"Art & Museums", "Food & Nightlife", "Outdoor Adventures"}
	domains := []string{"itinerary", "dining", "activities"}

	keyMap := make(map[string]bool)
	keyCount := 0

	for _, city := range cities {
		for _, pref := range preferences {
			for _, domain := range domains {
				cacheKeyData := map[string]any{
					"user_id":     userID.String(),
					"profile_id":  profileID.String(),
					"city":        city,
					"message":     "Show me around",
					"domain":      domain,
					"preferences": pref,
				}
				cacheKeyBytes, _ := json.Marshal(cacheKeyData)
				hash := md5.Sum(cacheKeyBytes)
				key := hex.EncodeToString(hash[:])

				if !keyMap[key] {
					keyMap[key] = true
					keyCount++
				}
			}
		}
	}

	expectedCount := len(cities) * len(preferences) * len(domains)
	assert.Equal(t, expectedCount, keyCount,
		"Should generate unique keys for all combinations")
	assert.Equal(t, expectedCount, len(keyMap),
		"All keys should be unique")
}

// TestCacheEviction tests that cache items are evicted after TTL
func TestCacheEviction(t *testing.T) {
	shortTTL := 100 * time.Millisecond
	testCache := newTestCacheStore(t, cachestore.Config{
		LLMTTL:     shortTTL,
		CleanupTTL: 50 * time.Millisecond,
	})

	testCache.Set("test-key", "test-value", 0)

	val, found := testCache.Get("test-key")
	assert.True(t, found)
	assert.Equal(t, "test-value", val)

	time.Sleep(150 * time.Millisecond)

	_, found = testCache.Get("test-key")
	assert.False(t, found, "Cache item should be evicted after TTL")
}

// TestConcurrentCacheAccess tests thread-safety of cache operations
func TestConcurrentCacheAccess(t *testing.T) {
	testCache := newTestCacheStore(t, cachestore.Config{LLMTTL: cachestore.DefaultLLMTTL})

	var wg sync.WaitGroup
	numGoroutines := 100

	for i := range numGoroutines {
		wg.Go(func() {
			key := fmt.Sprintf("key-%d", i)
			value := fmt.Sprintf("value-%d", i)
			testCache.Set(key, value, 0)
		})
	}

	wg.Wait()

	for i := range numGoroutines {
		key := fmt.Sprintf("key-%d", i)
		expectedValue := fmt.Sprintf("value-%d", i)

		val, found := testCache.Get(key)
		assert.True(t, found, fmt.Sprintf("Key %s should exist", key))
		assert.Equal(t, expectedValue, val, fmt.Sprintf("Value for %s should match", key))
	}

	for i := range numGoroutines {
		wg.Go(func() {
			key := fmt.Sprintf("key-%d", i)
			expectedValue := fmt.Sprintf("value-%d", i)

			val, found := testCache.Get(key)
			assert.True(t, found)
			assert.Equal(t, expectedValue, val)
		})
	}

	wg.Wait()
}

// TestCacheDifferentPreferencesSameCity verifies different preferences generate different cache keys
func TestCacheDifferentPreferencesSameCity(t *testing.T) {
	userID := uuid.New()
	profileID := uuid.New()
	city := "Paris"
	domain := string(locitypes.DomainItinerary)

	pref1 := "Art & Museums"
	pref2 := "Food & Nightlife"

	cacheKeyData1 := map[string]any{
		"user_id":     userID.String(),
		"profile_id":  profileID.String(),
		"city":        city,
		"message":     "Show me around",
		"domain":      domain,
		"preferences": pref1,
	}
	cacheKeyBytes1, _ := json.Marshal(cacheKeyData1)
	hash1 := md5.Sum(cacheKeyBytes1)
	cacheKey1 := hex.EncodeToString(hash1[:])

	cacheKeyData2 := map[string]any{
		"user_id":     userID.String(),
		"profile_id":  profileID.String(),
		"city":        city,
		"message":     "Show me around",
		"domain":      domain,
		"preferences": pref2,
	}
	cacheKeyBytes2, _ := json.Marshal(cacheKeyData2)
	hash2 := md5.Sum(cacheKeyBytes2)
	cacheKey2 := hex.EncodeToString(hash2[:])

	assert.NotEqual(t, cacheKey1, cacheKey2, "Different preferences should produce different keys")

	testCache := newTestCacheStore(t, cachestore.Config{LLMTTL: cachestore.DefaultLLMTTL})
	testCache.Set(cacheKey1, "Art response", 0)
	testCache.Set(cacheKey2, "Food response", 0)

	val1, found1 := testCache.Get(cacheKey1)
	val2, found2 := testCache.Get(cacheKey2)

	assert.True(t, found1)
	assert.True(t, found2)
	assert.Equal(t, "Art response", val1)
	assert.Equal(t, "Food response", val2)
}

// TestPartCacheKeyFormat verifies part-specific cache key suffixes
func TestPartCacheKeyFormat(t *testing.T) {
	baseKey := "abc123def456"

	partKeys := map[string]string{
		"city_data":    baseKey + "_city_data",
		"general_pois": baseKey + "_general_pois",
		"itinerary":    baseKey + "_itinerary",
		"hotels":       baseKey + "_hotels",
		"restaurants":  baseKey + "_restaurants",
		"activities":   baseKey + "_activities",
		"nearby_pois":  baseKey + "_nearby_pois",
	}

	for part, expectedKey := range partKeys {
		assert.True(t, strings.HasSuffix(expectedKey, "_"+part) || part == "nearby_pois" && strings.HasSuffix(expectedKey, "_nearby_pois"),
			"Part key should have correct suffix for %s", part)
		assert.True(t, strings.HasPrefix(expectedKey, baseKey),
			"Part key should start with base key for %s", part)
	}
}

// TestCacheStorageAndRetrieval tests basic set/get cycle
func TestCacheStorageAndRetrieval(t *testing.T) {
	testCases := []struct {
		key   string
		value string
	}{
		{"city_data_key", `{"city": "Paris"}`},
		{"itinerary_key", `{"days": 3}`},
		{"nearby_key", `[{"name": "Cafe"}]`},
	}

	testCache := newTestCacheStore(t, cachestore.Config{LLMTTL: cachestore.DefaultLLMTTL})

	for _, tc := range testCases {
		t.Run(tc.key, func(t *testing.T) {
			testCache.Set(tc.key, tc.value, 0)

			retrieved, found := testCache.Get(tc.key)
			assert.True(t, found)
			assert.Equal(t, tc.value, retrieved)
		})
	}
}

// TestCacheMultipleEntries tests storing multiple entries independently
func TestCacheMultipleEntries(t *testing.T) {
	testCache := newTestCacheStore(t, cachestore.Config{LLMTTL: cachestore.DefaultLLMTTL})

	numEntries := 50
	for i := range numEntries {
		key := fmt.Sprintf("key-%d", i)
		value := fmt.Sprintf("value-%d", i)
		testCache.Set(key, value, 0)
	}

	for i := range numEntries {
		key := fmt.Sprintf("key-%d", i)
		expectedValue := fmt.Sprintf("value-%d", i)

		val, found := testCache.Get(key)
		assert.True(t, found)
		assert.Equal(t, expectedValue, val)
	}
}
