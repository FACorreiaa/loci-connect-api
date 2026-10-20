package preference

import (
	"context"
	"time"

	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	"github.com/google/uuid"
)

// annotateTimeout bounds how long a response waits on taste scoring. A match is
// a nicety; a slow one must not hold back the places themselves.
const annotateTimeout = 300 * time.Millisecond

// AnnotatePOIs sets taste_match on every place the scorer can score, across all
// the given lists, in one scoring call. Places it cannot score, and every place
// when scoring fails or times out, are left unset — clients show no match
// rather than a wrong one.
func AnnotatePOIs(ctx context.Context, s POIScorer, userID uuid.UUID, groups ...[]*poiv1.POIDetailedInfo) {
	if s == nil || userID == uuid.Nil {
		return
	}
	seen := map[string]bool{}
	var ids []string
	for _, group := range groups {
		for _, p := range group {
			if id := p.GetId(); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	matches := MatchesFor(ctx, s, userID, ids)
	if len(matches) == 0 {
		return
	}
	for _, group := range groups {
		for _, p := range group {
			if m, ok := matches[p.GetId()]; ok && p != nil {
				p.TasteMatch = &poiv1.TasteMatch{Score: m.Score, Reasons: m.Reasons}
			}
		}
	}
}

// MatchesFor scores ids within annotateTimeout. A failed or slow scoring call
// gives no matches, never an error: callers show the places without one.
func MatchesFor(ctx context.Context, s POIScorer, userID uuid.UUID, ids []string) map[string]Match {
	if s == nil || userID == uuid.Nil || len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, annotateTimeout)
	defer cancel()
	matches, err := s.ScorePOIs(ctx, userID, ids)
	if err != nil {
		return nil
	}
	return matches
}
