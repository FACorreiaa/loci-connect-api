package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Generation-cache labels. Kept as constants so the chat service and the
// dashboards agree on the spelling.
const (
	// LLMCacheLayerMemory is the in-process store (cachestore.TieredStore).
	LLMCacheLayerMemory = "memory"
	// LLMCacheLayerDB is the durable llm_generations table.
	LLMCacheLayerDB = "db"
	// LLMCacheLayerNone means the answer came from the provider.
	LLMCacheLayerNone = "none"

	LLMCacheResultHit  = "hit"
	LLMCacheResultMiss = "miss"
	// LLMCacheResultBypass is a part that was never looked up: the query had
	// live words ("tonight", "open now"), or the domain is uncacheable.
	LLMCacheResultBypass = "bypass"
	// LLMCacheResultInvalid is a generation that was not written because its
	// output failed validation; the next identical request will miss again.
	LLMCacheResultInvalid = "invalid"
)

// LLMCacheRequestsTotal counts generation-cache lookups per answer part.
//
// A part (city_data, general_pois, itinerary, hotels, restaurants,
// activities) is one long provider stream, so each hit here is one stream
// not paid for. Hit rate by part is
//
//	sum(rate(loci_llm_cache_requests_total{result="hit"}[1h])) by (part)
//	  / sum(rate(loci_llm_cache_requests_total{result=~"hit|miss"}[1h])) by (part)
//
// and the layer label says whether the memory store or the database served
// it: memory hits should dominate on a warm pod, db hits right after a
// deploy. A hit rate falling towards zero for city_data, whose key holds
// nothing but the city and the model, means the key has become too
// specific — the same failure mode loci_external_cache_hits_total watches
// for the third-party adapters.
var LLMCacheRequestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "loci_llm_cache_requests_total",
		Help: "Generation cache lookups by answer part, cache layer and result",
	},
	[]string{"part", "layer", "result"},
)

// RecordLLMCacheLookup records the outcome of resolving one answer part
// against the generation cache. layer is where the answer came from
// (memory, db, none); result is hit, miss, bypass or invalid.
func RecordLLMCacheLookup(part, layer, result string) {
	LLMCacheRequestsTotal.WithLabelValues(part, layer, result).Inc()
}
