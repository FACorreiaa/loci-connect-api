package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Grounding metrics answer one question: how much of what Loci tells a user is
// traceable to a row we actually retrieved, and how much did the model make up?
//
// Before evidence packets the answer was unknowable — the model named places
// from memory and the ids were matched back afterwards, so a fabrication and a
// retrieval were indistinguishable by the time anything was persisted.
var (
	// CitedPOIsTotal counts identifiers the model attached to a recommendation,
	// split by whether that identifier was in the packet we gave it.
	//
	// outcome="grounded"   — cited an identifier we retrieved
	// outcome="fabricated" — cited an identifier that was never offered
	//
	// The ratio fabricated/(grounded+fabricated) is the hallucination rate.
	CitedPOIsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_llm_cited_pois_total",
			Help: "POI identifiers cited by the model, by grounding outcome",
		},
		[]string{"outcome"},
	)

	// OfferedPOIsTotal counts places placed in front of the model. Compared with
	// the grounded count it gives recall: how much of what we retrieved was
	// actually used.
	OfferedPOIsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "loci_llm_offered_pois_total",
			Help: "POIs included in evidence packets sent to the model",
		},
	)

	// GroundedGenerationsTotal counts generations that cited at least one
	// retrieved place. A generation with a non-empty packet and zero citations
	// is one the model chose to answer from memory.
	GroundedGenerationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_llm_grounded_generations_total",
			Help: "Generations by whether any retrieved place was cited",
		},
		[]string{"grounded"},
	)

	// UngroundedTurnsTotal counts turns that ran with no usable evidence packet
	// at all, labelled by why. These are the turns that still behave the way the
	// whole system used to, and the label says whether that is a corpus problem
	// (no_candidates), a data problem (city_unresolved), or a bug
	// (retrieval_failed, assembly_failed).
	UngroundedTurnsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "loci_llm_ungrounded_turns_total",
			Help: "Generations that ran without an evidence packet, by reason",
		},
		[]string{"reason"},
	)
)

// RecordGrounding records the outcome of verifying one generation against the
// packet it was given.
func RecordGrounding(groundedCount, fabricatedCount, offeredCount int) {
	if groundedCount > 0 {
		CitedPOIsTotal.WithLabelValues("grounded").Add(float64(groundedCount))
	}
	if fabricatedCount > 0 {
		CitedPOIsTotal.WithLabelValues("fabricated").Add(float64(fabricatedCount))
	}
	if offeredCount > 0 {
		OfferedPOIsTotal.Add(float64(offeredCount))
	}

	label := "false"
	if groundedCount > 0 {
		label = "true"
	}
	GroundedGenerationsTotal.WithLabelValues(label).Inc()
}

// RecordUngroundedTurn records a turn that produced no evidence packet.
func RecordUngroundedTurn(reason string) {
	UngroundedTurnsTotal.WithLabelValues(reason).Inc()
}
