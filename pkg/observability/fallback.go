package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Fallback metrics answer one question: is Loci still answering from the
// provider we intended, or has it quietly slid down to a shared free-tier
// model?
//
// The distinction matters because degradation is invisible from the
// outside — a fallback answer looks like a normal answer. In production
// the chain is disabled and out-of-credits is a hard failure, so any
// FallbackActivationsTotal increment there means configuration drift and
// should page.
var (
	// FallbackActivationsTotal counts requests served by a provider other
	// than the one ahead of it in the chain.
	//
	// reason distinguishes the cause: "out_of_credits", "auth_failed",
	// "rate_limited", "unavailable". A rising out_of_credits count is a
	// billing problem; a rising rate_limited count means the free tier is
	// saturated and the chain is doing real work.
	FallbackActivationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_llm_fallback_activations_total",
			Help: "LLM requests served by a fallback provider, by model and cause",
		},
		[]string{"from_model", "to_model", "reason"},
	)

	// FallbackProviderBenchedTotal counts credentials taken out of
	// rotation after a terminal rejection (401/402). Unlike an activation
	// this fires once per cooldown window rather than once per request,
	// so it measures distinct incidents rather than blast radius.
	FallbackProviderBenchedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_llm_fallback_provider_benched_total",
			Help: "LLM credentials benched after a terminal provider rejection",
		},
		[]string{"model", "reason"},
	)
)
