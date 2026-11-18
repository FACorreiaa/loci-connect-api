package placecheck

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

// Finder looks named places up on the map. *transit.Overpass is the one in
// production.
type Finder interface {
	FindPlaces(ctx context.Context, names []string, near transit.LatLon) ([]transit.PlaceMatch, error)
}

// Store keeps verifications.
type Store interface {
	// Get returns the verifications held for the given places, fresh or not.
	// A place never looked up has no entry.
	Get(ctx context.Context, poiIDs []string) (map[string]Verification, error)
	// Save writes the verifications, replacing any held for the same place,
	// and moves each matched place to where the map says it is.
	Save(ctx context.Context, vs []Verification) error
}

// POI names one place to check.
type POI struct {
	ID   string
	Name string
}

// Service checks places against the map and remembers the answers.
type Service struct {
	finder Finder
	store  Store
	now    func() time.Time
}

// NewService builds the service.
func NewService(finder Finder, store Store) *Service {
	return &Service{finder: finder, store: store, now: time.Now}
}

// CheckPOIs returns what is known about each place, looking up on the map
// those never checked or checked too long ago. near is the centre of the
// city the places are in: a model's own coordinates for a place are too far
// off to search around.
//
// The map being unreachable is not a failure of the call: the answers
// already held (stale ones included) come back with the error, so a caller
// can show them and try again later. Places missing from the result have
// never been checked.
func (s *Service) CheckPOIs(ctx context.Context, near transit.LatLon, pois []POI) (map[string]Verification, error) {
	seen := map[string]bool{}
	ids := make([]string, 0, len(pois))
	for _, p := range pois {
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		ids = append(ids, p.ID)
	}
	if len(ids) == 0 {
		return map[string]Verification{}, nil
	}

	held, err := s.store.Get(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("placecheck: read: %w", err)
	}
	now := s.now().UTC()

	var due []POI
	asked := map[string]bool{}
	for _, p := range pois {
		name := strings.TrimSpace(p.Name)
		if p.ID == "" || name == "" || asked[p.ID] {
			continue
		}
		if v, ok := held[p.ID]; ok && v.Fresh(now) {
			continue
		}
		asked[p.ID] = true
		due = append(due, POI{ID: p.ID, Name: name})
	}
	if len(due) == 0 {
		return held, nil
	}

	names := make([]string, len(due))
	for i, p := range due {
		names[i] = p.Name
	}
	matches, err := s.finder.FindPlaces(ctx, names, near)
	if err != nil {
		// A failed lookup may have answered some names and not others, and
		// does not say which. Nothing is written: an unanswered name saved
		// as "not found" would hide a real place for a week.
		return held, fmt.Errorf("placecheck: map lookup: %w", err)
	}
	if len(matches) != len(due) {
		return held, fmt.Errorf("placecheck: map lookup answered %d of %d names", len(matches), len(due))
	}

	fresh := make([]Verification, len(due))
	for i, p := range due {
		fresh[i] = verification(p.ID, matches[i], now)
	}
	if err := s.store.Save(ctx, fresh); err != nil {
		return held, fmt.Errorf("placecheck: save: %w", err)
	}
	for _, v := range fresh {
		held[v.POIID] = v
	}
	return held, nil
}

func verification(poiID string, m transit.PlaceMatch, now time.Time) Verification {
	if !m.Found {
		return Verification{POIID: poiID, Source: SourceOSM, CheckedAt: now, ExpiresAt: now.Add(ttlUnmatched)}
	}
	p := m.Place
	return Verification{
		POIID:       poiID,
		Matched:     true,
		Source:      SourceOSM,
		ExternalID:  fmt.Sprintf("%s/%d", p.OSMType, p.OSMID),
		WikidataID:  p.Wikidata,
		MatchedName: p.Name,
		Kind:        p.Kind,
		Lat:         p.Lat,
		Lon:         p.Lon,
		HoursRaw:    p.OpeningHours,
		Confidence:  p.Confidence,
		CheckedAt:   now,
		ExpiresAt:   now.Add(ttlMatched),
	}
}
