package observability

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func counterValue(t *testing.T, part, layer, result string) float64 {
	t.Helper()
	var m dto.Metric
	if err := LLMCacheRequestsTotal.WithLabelValues(part, layer, result).Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return m.GetCounter().GetValue()
}

// The counter is the only Prometheus-side view of the generation cache; a
// label that lands in the wrong slot makes the hit-rate query silently
// count zero. Pin the label order and that each call increments by one.
func TestRecordLLMCacheLookup(t *testing.T) {
	LLMCacheRequestsTotal.Reset()

	RecordLLMCacheLookup("itinerary", LLMCacheLayerMemory, LLMCacheResultHit)
	RecordLLMCacheLookup("itinerary", LLMCacheLayerMemory, LLMCacheResultHit)
	RecordLLMCacheLookup("itinerary", LLMCacheLayerDB, LLMCacheResultHit)
	RecordLLMCacheLookup("city_data", LLMCacheLayerNone, LLMCacheResultMiss)
	RecordLLMCacheLookup("hotels", LLMCacheLayerNone, LLMCacheResultBypass)

	cases := []struct {
		part, layer, result string
		want                float64
	}{
		{"itinerary", "memory", "hit", 2},
		{"itinerary", "db", "hit", 1},
		{"city_data", "none", "miss", 1},
		{"hotels", "none", "bypass", 1},
		{"itinerary", "none", "hit", 0},
	}
	for _, tc := range cases {
		if got := counterValue(t, tc.part, tc.layer, tc.result); got != tc.want {
			t.Errorf("{part=%s,layer=%s,result=%s} = %v, want %v", tc.part, tc.layer, tc.result, got, tc.want)
		}
	}
}
