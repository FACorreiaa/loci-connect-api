package placecheck

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
	"github.com/FACorreiaa/loci-connect-api/pkg/placematch"
	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

// Finder looks named places up on the map. *transit.Overpass is the one in
// production.
type Finder interface {
	FindPlaces(ctx context.Context, asks []transit.PlaceQuery, near transit.LatLon) ([]transit.PlaceMatch, error)
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
	// Category is what the plan says the place is ("Museum", "Cafe"), as
	// free text. It decides what on the map can be the answer: a sight, or
	// a business of that kind; see venueCategory.
	Category string
}

// venueWords are the words of a plan's category that mean a business, and
// which kind. Categories are free text from the model ("Fine Dining",
// "Cafe", "Bar", "Hotel"), so this reads words, not a fixed list of values.
// A category it does not know is not a business: the place is then looked
// for among sights, which can miss a restaurant and cannot mistake one for
// the monument it is named after.
var venueWords = func() map[string]transit.VenueKind {
	out := map[string]transit.VenueKind{}
	for kind, words := range map[transit.VenueKind][]string{
		transit.VenueFood: {
			"restaurant", "restaurante", "dining", "food", "eatery", "bistro", "tapas",
			"cafe", "coffee", "bakery", "pastry", "patisserie", "dessert", "gelato",
			"bar", "pub", "tavern", "taberna", "winery", "brewery", "nightlife", "nightclub", "club",
		},
		transit.VenueShop: {"shop", "shopping", "store", "boutique", "bookshop", "bookstore"},
		transit.VenueStay: {"hotel", "hostel", "guesthouse", "apartment", "lodging", "accommodation"},
	} {
		for _, w := range words {
			out[w] = kind
		}
	}
	return out
}()

// venueCategory is the kind of business a plan's category means, or zero.
func venueCategory(category string) transit.VenueKind {
	var kind transit.VenueKind
	for w := range placematch.Tokens(category) {
		kind |= venueWords[w]
	}
	return kind
}

// Service checks places against the map and remembers the answers.
type Service struct {
	finder Finder
	store  Store
	log    *slog.Logger
	now    func() time.Time
}

// NewService builds the service.
func NewService(finder Finder, store Store) *Service {
	return &Service{finder: finder, store: store, now: time.Now}
}

// WithLogger makes the service say which places it could not find. The names
// are places, not people, and reading them is how the matching gets better:
// a miss on a real plan is a case no fixture has.
func (s *Service) WithLogger(log *slog.Logger) *Service {
	s.log = log
	return s
}

// kindLabel names the kind of place an ask is for, for metrics and logs.
func kindLabel(v transit.VenueKind) string {
	switch v {
	case 0:
		return "sight"
	case transit.VenueFood:
		return "food"
	case transit.VenueShop:
		return "shop"
	case transit.VenueStay:
		return "stay"
	default:
		return "mixed"
	}
}

// record counts how each lookup came out and logs the places not found.
func (s *Service) record(due []POI, asks []transit.PlaceQuery, matches []transit.PlaceMatch) {
	for i, m := range matches {
		kind := kindLabel(asks[i].Venue)
		switch {
		case !m.Found:
			observability.RecordPlaceCheck("not_found", kind)
			if s.log != nil {
				s.log.Info("place check: not found on the map",
					slog.String("name", due[i].Name), slog.String("category", due[i].Category), slog.String("kind", kind))
			}
		case m.Place.OpeningHours == "":
			observability.RecordPlaceCheck("found_no_hours", kind)
		default:
			observability.RecordPlaceCheck("found_with_hours", kind)
		}
	}
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
		due = append(due, POI{ID: p.ID, Name: name, Category: p.Category})
	}
	if len(due) == 0 {
		return held, nil
	}

	asks := make([]transit.PlaceQuery, len(due))
	for i, p := range due {
		asks[i] = transit.PlaceQuery{Name: p.Name, Venue: venueCategory(p.Category)}
	}
	matches, err := s.finder.FindPlaces(ctx, asks, near)
	if err != nil {
		// A failed lookup may have answered some names and not others, and
		// does not say which. Nothing is written: an unanswered name saved
		// as "not found" would hide a real place for a week.
		for _, a := range asks {
			observability.RecordPlaceCheck("lookup_failed", kindLabel(a.Venue))
		}
		return held, fmt.Errorf("placecheck: map lookup: %w", err)
	}
	if len(matches) != len(due) {
		return held, fmt.Errorf("placecheck: map lookup answered %d of %d names", len(matches), len(due))
	}

	s.record(due, asks, matches)

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
