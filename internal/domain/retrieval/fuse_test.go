package retrieval

import (
	"testing"

	"github.com/google/uuid"
)

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func TestFuseRRFRanksAgreementAboveEitherLane(t *testing.T) {
	id := ids(4)
	agreed, lexOnly, semOnly, filler := id[0], id[1], id[2], id[3]

	// agreed is second in both lanes; lexOnly and semOnly are first in one lane
	// and absent from the other.
	fused := FuseRRF(
		Ranked{Reason: MatchLexical, IDs: []uuid.UUID{lexOnly, agreed, filler}},
		Ranked{Reason: MatchSemantic, IDs: []uuid.UUID{semOnly, agreed, filler}},
	)

	if len(fused) != 4 {
		t.Fatalf("expected 4 fused results, got %d", len(fused))
	}
	if fused[0].POIID != agreed {
		t.Errorf("agreement did not win: top result is %s, want %s", fused[0].POIID, agreed)
	}
	if fused[0].Reason != MatchBoth {
		t.Errorf("agreed result reason = %q, want %q", fused[0].Reason, MatchBoth)
	}
	if fused[0].Lanes != 2 {
		t.Errorf("agreed result Lanes = %d, want 2", fused[0].Lanes)
	}
}

func TestFuseRRFKeepsSingleLaneReason(t *testing.T) {
	id := ids(2)

	fused := FuseRRF(Ranked{Reason: MatchLexical, IDs: id})

	if len(fused) != 2 {
		t.Fatalf("expected 2 results, got %d", len(fused))
	}
	for _, f := range fused {
		if f.Reason != MatchLexical {
			t.Errorf("reason = %q, want %q", f.Reason, MatchLexical)
		}
		if f.Lanes != 1 {
			t.Errorf("Lanes = %d, want 1", f.Lanes)
		}
	}
	if fused[0].POIID != id[0] {
		t.Error("single-lane fusion did not preserve the lane's order")
	}
}

// A lane that repeats an id must not be able to vote for it twice — otherwise a
// buggy or adversarial lane could promote anything by listing it repeatedly.
func TestFuseRRFIgnoresDuplicatesWithinALane(t *testing.T) {
	id := ids(2)
	repeated, other := id[0], id[1]

	fused := FuseRRF(Ranked{
		Reason: MatchLexical,
		IDs:    []uuid.UUID{other, repeated, repeated, repeated, repeated},
	})

	if len(fused) != 2 {
		t.Fatalf("expected 2 unique results, got %d", len(fused))
	}
	if fused[0].POIID != other {
		t.Errorf("repetition beat a genuinely higher rank: top is %s, want %s",
			fused[0].POIID, other)
	}
	for _, f := range fused {
		if f.Lanes != 1 {
			t.Errorf("Lanes = %d for %s, want 1", f.Lanes, f.POIID)
		}
	}
}

func TestFuseRRFHandlesEmptyAndNilLanes(t *testing.T) {
	if got := FuseRRF(); len(got) != 0 {
		t.Errorf("FuseRRF() with no lanes returned %d results", len(got))
	}
	if got := FuseRRF(Ranked{Reason: MatchLexical}); len(got) != 0 {
		t.Errorf("FuseRRF() with an empty lane returned %d results", len(got))
	}

	id := ids(1)
	got := FuseRRF(
		Ranked{Reason: MatchLexical, IDs: nil},
		Ranked{Reason: MatchSemantic, IDs: id},
	)
	if len(got) != 1 || got[0].POIID != id[0] {
		t.Errorf("a nil lane broke fusion: %+v", got)
	}
}

func TestFuseRRFSkipsNilUUIDs(t *testing.T) {
	id := ids(1)

	got := FuseRRF(Ranked{Reason: MatchLexical, IDs: []uuid.UUID{uuid.Nil, id[0], uuid.Nil}})

	if len(got) != 1 || got[0].POIID != id[0] {
		t.Errorf("nil uuid leaked into fusion: %+v", got)
	}
}

// Fusion feeds a packet whose id is a hash of the candidate order, so identical
// input must always produce identical output.
func TestFuseRRFIsDeterministic(t *testing.T) {
	id := ids(5)
	lanes := []Ranked{
		{Reason: MatchLexical, IDs: []uuid.UUID{id[0], id[1], id[2]}},
		{Reason: MatchSemantic, IDs: []uuid.UUID{id[2], id[3], id[4]}},
	}

	first := FuseRRF(lanes...)
	for i := 0; i < 20; i++ {
		next := FuseRRF(lanes...)
		if len(next) != len(first) {
			t.Fatalf("result length varied between runs: %d vs %d", len(next), len(first))
		}
		for j := range first {
			if next[j].POIID != first[j].POIID {
				t.Fatalf("order varied between runs at position %d", j)
			}
		}
	}
}

// Ties must break on a stable key rather than on map iteration order.
func TestFuseRRFTiesBreakDeterministically(t *testing.T) {
	a := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	b := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	// Both are rank 1 in their own lane, so their scores are identical.
	fused := FuseRRF(
		Ranked{Reason: MatchLexical, IDs: []uuid.UUID{b}},
		Ranked{Reason: MatchSemantic, IDs: []uuid.UUID{a}},
	)

	if len(fused) != 2 {
		t.Fatalf("expected 2 results, got %d", len(fused))
	}
	if fused[0].Score != fused[1].Score {
		t.Fatalf("expected tied scores, got %v and %v", fused[0].Score, fused[1].Score)
	}
	if fused[0].POIID != a {
		t.Errorf("tiebreak = %s, want the lower id %s", fused[0].POIID, a)
	}
}
