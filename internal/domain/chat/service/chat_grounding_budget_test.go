package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
)

// The packet has to be bigger than the answer. A model asked for forty places
// out of exactly forty candidates cannot honour the category mix, skip
// somewhere already visited, or drop a bad fit — it has to use all of them or
// invent the difference.
func TestEvidenceLimitFor(t *testing.T) {
	cases := []struct {
		target, want int
	}{
		// No caller decided: fall back to the packet size an unstated trip
		// length is worth.
		{0, retrieval.DefaultEvidence},
		{-1, retrieval.DefaultEvidence},

		// Headroom above the target.
		{8, 18},
		{12, 22},
		{24, 34},
		{40, 50},

		// The ceiling still wins: a Pro answer at fifty asks for sixty, which
		// is MaxEvidence exactly.
		{50, retrieval.MaxEvidence},
		{100, retrieval.MaxEvidence},
	}
	for _, c := range cases {
		if got := evidenceLimitFor(c.target); got != c.want {
			t.Errorf("evidenceLimitFor(%d) = %d, want %d", c.target, got, c.want)
		}
	}
}

// The largest answer anyone can ask for must still fit inside the packet
// ceiling, or a Pro itinerary is grounded in fewer places than it names.
func TestMaxEvidenceCoversTheLargestTarget(t *testing.T) {
	if evidenceLimitFor(poiTargetMaxPro) < poiTargetMaxPro {
		t.Fatalf("a %d-place answer is grounded in only %d candidates",
			poiTargetMaxPro, evidenceLimitFor(poiTargetMaxPro))
	}
}

// Raising MaxEvidence from twenty to sixty triples the evidence block, and that
// block is rendered into the prompt in full. This is the guard that stops a
// later change to Render — a longer description, another verified fact —
// quietly doubling every prompt on the path.
func TestGroundedPromptStaysUnderBudget(t *testing.T) {
	evidence := make([]retrieval.Evidence, retrieval.MaxEvidence)
	for i := range evidence {
		evidence[i] = retrieval.Evidence{
			POIID:       uuid.New(),
			Name:        fmt.Sprintf("A Place With A Reasonably Long Name %d", i),
			Category:    "Historical Site",
			Description: strings.Repeat("x", retrieval.MaxDescriptionChars),
			Address:     "Rua de Uma Morada Bastante Longa 123, Funchal, Madeira",
			MatchReason: retrieval.MatchLexical,
		}
	}
	packet := &retrieval.ContextPacket{Evidence: evidence}

	prompt := groundPrompt(
		getPersonalizedItineraryPrompt(fixtureCity, fixtureRequest, "", poiTargetMaxPro, 10, false),
		packet,
	)

	const budget = 60_000
	if len(prompt) > budget {
		t.Fatalf("a %d-evidence prompt is %d bytes, over the %d budget",
			len(evidence), len(prompt), budget)
	}
	t.Logf("%d evidence entries render %d bytes", len(evidence), len(prompt))
}
