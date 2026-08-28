package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// External-provider metrics answer one question: are the third-party data
// sources behind trip context actually answering, or are we quietly showing
// people a trip with no weather, no holidays and no hazards?
//
// The distinction matters because every one of these adapters degrades
// silently by design — a provider failure logs a warning and returns an empty
// result rather than failing the RPC, so a dead source looks exactly like a
// destination with nothing going on. These counters are the only way to tell
// those apart from the outside.
//
// Nearly all of these are free public APIs with usage policies rather than
// contracts, so a rising retryable_error or client_error count is as likely to
// mean "we are being throttled" as "they are down".
var (
	// ExternalRequestsTotal counts outbound calls to third-party data
	// providers.
	//
	// outcome is one of "ok", "retryable_error" (429/5xx, a retry followed),
	// "client_error" (4xx, we asked wrongly and gave up), "transport_error"
	// (connection refused, DNS, timeout) or "read_error".
	//
	// A source whose ok count drops to zero while the app keeps serving is the
	// signal that matters: it means that dimension of trip context has silently
	// disappeared.
	ExternalRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_external_requests_total",
			Help: "Outbound requests to third-party data providers, by source and outcome",
		},
		[]string{"source", "outcome"},
	)

	// ExternalRequestDuration measures how long providers take to answer.
	//
	// These calls sit inside user-facing RPCs, so the tail here is trip-view
	// latency. Buckets are weighted towards the 8s client timeout rather than
	// towards fast responses, because the interesting question is how close to
	// timing out a provider runs.
	ExternalRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "loci_external_request_duration_seconds",
			Help:    "Latency of outbound third-party data provider requests",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 4, 8, 16},
		},
		[]string{"source"},
	)

	// ExternalCacheHitsTotal counts requests served from an adapter's cache
	// instead of the network.
	//
	// This is the quota guard. These are free tiers with rate limits, and the
	// caches are what keeps a trip view that renders five cities from making
	// five upstream calls. A hit ratio falling towards zero means a cache key
	// has become too specific — the usual cause is coordinates that stopped
	// being rounded.
	ExternalCacheHitsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_external_cache_hits_total",
			Help: "Third-party provider responses served from cache",
		},
		[]string{"source"},
	)
)
