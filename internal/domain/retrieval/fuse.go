package retrieval

import (
	"sort"

	"github.com/google/uuid"
)

// rrfK is the Reciprocal Rank Fusion constant. 60 is the value from the original
// Cormack et al. paper and the de-facto default.
//
// It controls how sharply rank position matters: the contribution of a result at
// rank i is 1/(k+i), so with k=60 the gap between rank 1 and rank 2 is small and
// the gap between rank 1 and rank 50 is large but not overwhelming. A smaller k
// would let whichever lane happens to rank first dominate; a larger one would
// flatten the lanes into a popularity contest.
const rrfK = 60.0

// Ranked is one lane's opinion: an ordered list of POI ids, best first.
type Ranked struct {
	// Reason labels what this lane is, and is carried onto the fused result so a
	// recommendation can say why it surfaced.
	Reason MatchReason
	IDs    []uuid.UUID
}

// Fused is one result after fusion.
type Fused struct {
	POIID uuid.UUID
	Score float64
	// Reason is the lane that found it, or MatchBoth when more than one did.
	Reason MatchReason
	// Lanes counts how many lanes returned this result. Two lanes agreeing is a
	// materially stronger signal than either lane alone.
	Lanes int
}

// FuseRRF combines ranked lists by Reciprocal Rank Fusion.
//
// RRF is used here instead of the weighted score blend the existing hybrid
// search performs in SQL, because the lanes produce incomparable numbers:
// ts_rank_cd is unbounded and corpus-dependent, cosine similarity is [0,2], and
// inverse distance is a different quantity again. Normalising them against each
// other requires constants nobody can justify and that silently rot as the
// corpus grows. RRF discards the magnitudes and keeps only the orderings, which
// are the part each lane is actually reliable about.
//
// A result found by several lanes accumulates their contributions, so agreement
// between lexical and semantic retrieval naturally floats to the top.
//
// Empty and nil lanes are ignored. Output is ordered by score descending, with
// the POI id as a deterministic tiebreak so identical inputs always produce an
// identical order.
func FuseRRF(lanes ...Ranked) []Fused {
	scores := make(map[uuid.UUID]float64)
	laneCount := make(map[uuid.UUID]int)
	reasons := make(map[uuid.UUID]MatchReason)

	for _, lane := range lanes {
		seen := make(map[uuid.UUID]struct{}, len(lane.IDs))
		rank := 0
		for _, id := range lane.IDs {
			if id == uuid.Nil {
				continue
			}
			// A lane that repeats an id must not be able to vote twice.
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			rank++

			scores[id] += 1.0 / (rrfK + float64(rank))
			laneCount[id]++
			if existing, ok := reasons[id]; !ok {
				reasons[id] = lane.Reason
			} else if existing != lane.Reason {
				reasons[id] = MatchBoth
			}
		}
	}

	out := make([]Fused, 0, len(scores))
	for id, score := range scores {
		out = append(out, Fused{
			POIID:  id,
			Score:  score,
			Reason: reasons[id],
			Lanes:  laneCount[id],
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].POIID.String() < out[j].POIID.String()
	})

	return out
}
