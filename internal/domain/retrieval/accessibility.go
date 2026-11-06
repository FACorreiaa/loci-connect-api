package retrieval

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// minVerifiedContributors is how many travellers must agree before a fact is
// called verified — the same threshold Render uses to choose between
// "Verified" and "Reported".
const minVerifiedContributors = 2

// VerifiedAccessFact is one crowd-verified accessibility token for a place
// ("step_free", "accessible_wc", "lift", "wide_doors", "tactile", "none").
type VerifiedAccessFact struct {
	Token            string
	ContributorCount int32
	VerifiedAt       time.Time
}

// VerifiedAccessibility returns the unexpired, crowd-verified accessibility
// facts for the given places, keyed by POI id. Single-contributor reports are
// left out: they are not verified, and an accessibility claim shown to a
// wheelchair user must be.
func (a *Assembler) VerifiedAccessibility(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]VerifiedAccessFact, error) {
	if a == nil || a.db == nil || len(ids) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != uuid.Nil {
			keys = append(keys, id.String())
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}

	rows, err := a.db.Query(ctx, `
		SELECT poi_id, value, contributor_count, verified_at
		FROM place_facts
		WHERE poi_id = ANY($1)
		  AND field = 'accessibility'
		  AND contributor_count >= $2
		  AND expires_at > NOW()
		ORDER BY poi_id, confidence DESC, value`, keys, minVerifiedContributors)
	if err != nil {
		return nil, fmt.Errorf("querying verified accessibility facts: %w", err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID][]VerifiedAccessFact)
	for rows.Next() {
		var poiID string
		var f VerifiedAccessFact
		if err := rows.Scan(&poiID, &f.Token, &f.ContributorCount, &f.VerifiedAt); err != nil {
			return nil, fmt.Errorf("reading verified accessibility fact: %w", err)
		}
		id, err := uuid.Parse(poiID)
		if err != nil {
			continue
		}
		out[id] = append(out[id], f)
	}
	return out, rows.Err()
}
