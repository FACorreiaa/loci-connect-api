package city

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/geo"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
	"golang.org/x/sync/singleflight"
)

// nearbyCityRadiusKm is how far from supplied coordinates we will still call a
// stored city "the same place". A weekend origin given as raw coordinates is
// normally a phone's fix inside the city it names.
const nearbyCityRadiusKm = 25

// maxSuggestions caps what rides back on an unresolvable-city error. The
// suggestions travel in response metadata, which is not the place for a long
// list.
const maxSuggestions = 5

// homonymRadiusKm is how far from a caller's own position another city of the
// same name stops being a plausible answer.
//
// City names repeat across the world, and population alone picks the wrong one
// surprisingly often: asked for "Beja" it returns Béja in Tunisia (61,568)
// rather than Beja in Portugal (34,760), which is not a weekend trip from Porto
// under any reading. Generous on purpose — it only has to separate "somewhere
// you might drive to" from "a different continent", and when no position is
// known it does nothing at all.
const homonymRadiusKm = 1500

// ResolveSource records how a city was resolved, for logging and metrics.
type ResolveSource string

const (
	ResolvedFromClient   ResolveSource = "client"
	ResolvedFromDB       ResolveSource = "db"
	ResolvedFromGeocoder ResolveSource = "geocoder"
)

// ResolveQuery asks for a city by name, by coordinates, or by both.
type ResolveQuery struct {
	Name string
	// CountryCode optionally disambiguates, e.g. "PT" to mean Porto in
	// Portugal rather than Porto in Brazil.
	CountryCode string
	// Lat and Lon are client-supplied coordinates. When either is non-zero they
	// win outright: coordinates are unambiguous and a name is not.
	Lat, Lon float64
	// NearLat and NearLon bias the answer toward somewhere the caller already
	// knows about — for a comparison, the origin city. It is only a preference
	// between places of the same name, never a filter: a city genuinely far
	// away is still returned when it is the only match.
	NearLat, NearLon float64
}

func (q ResolveQuery) hasBias() bool { return q.NearLat != 0 || q.NearLon != 0 }

// Resolved is a city that is guaranteed to have coordinates.
//
// That guarantee is the point of the type. Callers used to receive a CityDetail
// whose CenterLatitude might be nil and had to fail late, per feature, with
// their own wording.
type Resolved struct {
	City locitypes.CityDetail
	// Lat and Lon are never zero-by-accident: a Resolved is only built once
	// real coordinates are in hand.
	Lat, Lon float64
	Source   ResolveSource
	// Created reports whether this resolve wrote a new row.
	Created bool
}

// Suggestion is a near-miss offered back to the user as "did you mean".
type Suggestion struct {
	Name        string  `json:"name"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code,omitempty"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
}

var (
	// ErrCityUnresolvable means the name is not a place we can find. The user
	// has to change their input.
	ErrCityUnresolvable = errors.New("city could not be resolved")
	// ErrGeocoderUnavailable means the provider failed. The input was fine and
	// retrying is the right advice, so callers must not report this as a bad
	// argument.
	ErrGeocoderUnavailable = errors.New("geocoder unavailable")
)

// AmbiguousCityError is an unresolvable city that has near-misses worth showing.
//
// It wraps ErrCityUnresolvable so callers that only care about the category can
// use errors.Is and ignore the suggestions entirely.
type AmbiguousCityError struct {
	Query       string
	Suggestions []Suggestion
}

func (e *AmbiguousCityError) Error() string {
	if len(e.Suggestions) == 0 {
		return fmt.Sprintf("no city named %q", e.Query)
	}
	names := make([]string, 0, len(e.Suggestions))
	for _, s := range e.Suggestions {
		names = append(names, s.Name)
	}
	return fmt.Sprintf("no city named %q; did you mean %s", e.Query, strings.Join(names, ", "))
}

func (e *AmbiguousCityError) Unwrap() error { return ErrCityUnresolvable }

// Resolver turns a typed city name into coordinates, creating the row if needed.
type Resolver interface {
	Resolve(ctx context.Context, q ResolveQuery) (*Resolved, error)
	Suggest(ctx context.Context, query string, limit int) ([]locitypes.CityDetail, error)
}

// ResolverImpl resolves cities from the database first and a geocoder second.
//
// It exists because the cities table was never a gazetteer: the only rows in it
// are ones some earlier LLM turn happened to create, so any feature that asked
// "where is the city the user typed?" worked only for cities that had already
// been chatted about. /compare returned a 400 for Porto.
type ResolverImpl struct {
	repo   Repository
	fwd    geocode.Forward
	logger *slog.Logger
	group  singleflight.Group
}

var _ Resolver = (*ResolverImpl)(nil)

// NewResolver builds a resolver. A nil Forward is supported and means
// database-only resolution, which is exactly the old behaviour.
func NewResolver(repo Repository, fwd geocode.Forward, logger *slog.Logger) *ResolverImpl {
	return &ResolverImpl{repo: repo, fwd: fwd, logger: logger}
}

func (r *ResolverImpl) Resolve(ctx context.Context, q ResolveQuery) (*Resolved, error) {
	q.Name = strings.TrimSpace(q.Name)

	// Coordinates are unambiguous, so they short-circuit everything.
	if q.Lat != 0 || q.Lon != 0 {
		return r.resolveFromCoordinates(ctx, q), nil
	}
	if q.Name == "" {
		return nil, fmt.Errorf("%w: no city name or coordinates given", ErrCityUnresolvable)
	}

	// Collapse concurrent resolves of the same city. Eight candidate columns, or
	// N users comparing the same pair, would otherwise each pay for a geocoder
	// call and race to insert the same row. ON CONFLICT already protects
	// correctness; this protects the bill.
	key := foldName(q.Name) + "|" + strings.ToUpper(q.CountryCode)
	out, err, _ := r.group.Do(key, func() (any, error) {
		return r.resolve(ctx, q)
	})
	if err != nil {
		return nil, err
	}
	return out.(*Resolved), nil
}

func (r *ResolverImpl) resolve(ctx context.Context, q ResolveQuery) (*Resolved, error) {
	candidates, err := r.repo.FindCityCandidates(ctx, q.Name, 10)
	if err != nil {
		// A database failure is not "no such city". Surfacing it as one would
		// tell the user to fix a name that was never the problem.
		return nil, fmt.Errorf("look up city %q: %w", q.Name, err)
	}

	best, coordless := pickCandidate(candidates, q.Name, q.CountryCode)
	if best != nil {
		return &Resolved{
			City:   *best,
			Lat:    *best.CenterLatitude,
			Lon:    *best.CenterLongitude,
			Source: ResolvedFromDB,
		}, nil
	}

	// Nothing usable stored. Ask the geocoder — either to place a city we have
	// never seen, or to fill in the coordinates a stub row is missing.
	place, err := r.geocodeBest(ctx, q)
	if err != nil {
		return nil, err
	}

	if coordless != nil {
		return r.enrich(ctx, *coordless, place), nil
	}
	return r.create(ctx, place), nil
}

// resolveFromCoordinates honours client-supplied coordinates and tries to name
// them.
//
// Attaching a stored city is what gives the column a city_id, and therefore its
// POIs; without it, supplying coordinates silently produced an emptier answer
// than supplying a name. A miss here is not an error — the coordinates alone are
// enough to compare with.
func (r *ResolverImpl) resolveFromCoordinates(ctx context.Context, q ResolveQuery) *Resolved {
	res := &Resolved{
		City:   locitypes.CityDetail{Name: q.Name},
		Lat:    q.Lat,
		Lon:    q.Lon,
		Source: ResolvedFromClient,
	}

	near, err := r.repo.FindCityNear(ctx, q.Lat, q.Lon, nearbyCityRadiusKm)
	if err != nil {
		r.logger.WarnContext(ctx, "could not look for a city near supplied coordinates",
			slog.Float64("lat", q.Lat), slog.Float64("lon", q.Lon), slog.Any("error", err))
		return res
	}
	if near != nil {
		res.City = *near
		if q.Name != "" {
			// The caller's own label wins: they typed it, and it may be the
			// neighbourhood or the airport they mean.
			res.City.Name = q.Name
		}
	}
	return res
}

// geocodeBest returns the single best place for a query, or a typed error.
func (r *ResolverImpl) geocodeBest(ctx context.Context, q ResolveQuery) (geocode.Place, error) {
	if r.fwd == nil {
		return geocode.Place{}, &AmbiguousCityError{Query: q.Name}
	}

	places, err := r.fwd.Search(ctx, q.Name, maxSuggestions)
	if err != nil {
		if errors.Is(err, geocode.ErrUnavailable) {
			// Deliberately not falling back to a coordless row here. Doing so
			// would resurrect "city missing coordinates" during an outage —
			// a confusing message about the user's input for a problem that is
			// entirely ours.
			return geocode.Place{}, fmt.Errorf("%w: %v", ErrGeocoderUnavailable, err)
		}
		return geocode.Place{}, fmt.Errorf("geocode %q: %w", q.Name, err)
	}

	ranked := rankPlaces(places, q.Name, q.CountryCode, q)
	if len(ranked) == 0 {
		return geocode.Place{}, &AmbiguousCityError{
			Query:       q.Name,
			Suggestions: toSuggestions(places),
		}
	}
	return ranked[0], nil
}

func (r *ResolverImpl) enrich(ctx context.Context, existing locitypes.CityDetail, place geocode.Place) *Resolved {
	if err := r.repo.EnrichCity(ctx, existing.ID, place.Lat, place.Lon, place.Country, place.Admin1); err != nil {
		r.logger.WarnContext(ctx, "could not backfill city coordinates; continuing with geocoded values",
			slog.String("city_id", existing.ID.String()),
			slog.String("city", existing.Name),
			slog.Any("error", err))
	}
	if existing.Country == "" || existing.Country == "Unknown" {
		existing.Country = place.Country
	}
	existing.CenterLatitude = &place.Lat
	existing.CenterLongitude = &place.Lon

	return &Resolved{City: existing, Lat: place.Lat, Lon: place.Lon, Source: ResolvedFromGeocoder}
}

func (r *ResolverImpl) create(ctx context.Context, place geocode.Place) *Resolved {
	detail := locitypes.CityDetail{
		// The geocoder's canonical spelling, never the user's typing, so a
		// search for "Prto" cannot mint a row called Prto.
		Name:            place.Name,
		Country:         place.Country,
		StateProvince:   place.Admin1,
		CenterLatitude:  &place.Lat,
		CenterLongitude: &place.Lon,
	}

	id, err := r.repo.SaveCity(ctx, detail)
	if err != nil {
		// A write failure must not fail the resolve. We know where the city is;
		// a comparison column without POIs is worth far more than a 500.
		r.logger.WarnContext(ctx, "could not persist geocoded city; continuing without an id",
			slog.String("city", place.Name), slog.Any("error", err))
		return &Resolved{City: detail, Lat: place.Lat, Lon: place.Lon, Source: ResolvedFromGeocoder}
	}

	detail.ID = id
	return &Resolved{City: detail, Lat: place.Lat, Lon: place.Lon, Source: ResolvedFromGeocoder, Created: true}
}

// Suggest returns stored cities matching a query, for autocomplete. It never
// writes: a field that searches on every keystroke must not create rows.
func (r *ResolverImpl) Suggest(ctx context.Context, query string, limit int) ([]locitypes.CityDetail, error) {
	return r.repo.SearchCitiesByName(ctx, query, limit)
}

// pickCandidate chooses the best stored row, and separately reports the best
// row that has no coordinates.
//
// Returning both is what lets the caller tell "we have never heard of this city"
// apart from "we have a stub row that needs filling in" — which are the same
// thing to a lookup, but call for opposite writes: an insert versus a backfill
// that must not orphan the POIs already pointing at the stub.
func pickCandidate(candidates []locitypes.CityDetail, name, countryCode string) (best, coordless *locitypes.CityDetail) {
	folded := foldName(name)
	cc := strings.ToUpper(strings.TrimSpace(countryCode))

	for i := range candidates {
		c := &candidates[i]
		if cc != "" && !strings.EqualFold(c.Country, cc) && !countryMatches(c.Country, cc) {
			continue
		}
		hasCoords := c.CenterLatitude != nil && c.CenterLongitude != nil
		exact := foldName(c.Name) == folded

		if hasCoords {
			if best == nil || (exact && foldName(best.Name) != folded) {
				best = c
			}
			continue
		}
		if coordless == nil || (exact && foldName(coordless.Name) != folded) {
			coordless = c
		}
	}
	return best, coordless
}

// countryMatches allows a stored country name to satisfy a country-code filter.
// The column is free text — "PT", "Portugal" and "Unknown" all occur — so an
// exact code comparison alone would discard perfectly good rows.
func countryMatches(stored, code string) bool {
	s := foldName(stored)
	switch {
	case s == "" || s == "unknown":
		// An unlabelled row is not evidence against the filter.
		return true
	case len(s) == 2:
		return strings.EqualFold(s, code)
	default:
		return false
	}
}

// rankPlaces orders geocoder hits so the first one is the city a person meant.
func rankPlaces(places []geocode.Place, name, countryCode string, bias ResolveQuery) []geocode.Place {
	folded := foldName(name)
	cc := strings.ToUpper(strings.TrimSpace(countryCode))

	// Populated places only. Without this, rivers, regions and airports that
	// share a city's name compete with it — and some of them outrank it.
	out := make([]geocode.Place, 0, len(places))
	for _, p := range places {
		if strings.HasPrefix(p.FeatureCode, "PPL") {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}

	if cc != "" {
		filtered := make([]geocode.Place, 0, len(out))
		for _, p := range out {
			if strings.EqualFold(p.CountryCode, cc) {
				filtered = append(filtered, p)
			}
		}
		// An unmatched filter narrows to nothing; prefer an imperfect answer
		// over none, since the caller can still see the country we picked.
		if len(filtered) > 0 {
			out = filtered
		}
	}

	// Stable, so equally-ranked hits keep the provider's own relevance order.
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := nameRank(out[i].Name, folded), nameRank(out[j].Name, folded)
		if li != lj {
			return li < lj
		}
		// Somewhere the caller could plausibly go beats somewhere larger on
		// another continent. Checked before population, because population is
		// exactly what gets this wrong.
		if bias.hasBias() {
			ni, nj := withinReach(out[i], bias), withinReach(out[j], bias)
			if ni != nj {
				return ni
			}
		}
		// Population is the tie-break that makes Porto mean Portugal's second
		// city rather than a Brazilian village of 1,500 people.
		return out[i].Population > out[j].Population
	})
	return out
}

// nameRank scores how closely a result's name matches what was typed: exact
// beats prefix beats everything else.
func nameRank(candidate, folded string) int {
	c := foldName(candidate)
	switch {
	case c == folded:
		return 0
	case strings.HasPrefix(c, folded):
		return 1
	case strings.Contains(c, folded):
		return 2
	default:
		return 3
	}
}

// withinReach reports whether a place is close enough to the caller's own
// position to be the one they meant.
func withinReach(p geocode.Place, bias ResolveQuery) bool {
	return geo.HaversineKm(bias.NearLat, bias.NearLon, p.Lat, p.Lon) <= homonymRadiusKm
}

func toSuggestions(places []geocode.Place) []Suggestion {
	if len(places) > maxSuggestions {
		places = places[:maxSuggestions]
	}
	out := make([]Suggestion, 0, len(places))
	for _, p := range places {
		out = append(out, Suggestion{
			Name:        p.Name,
			Country:     p.Country,
			CountryCode: p.CountryCode,
			Lat:         p.Lat,
			Lon:         p.Lon,
		})
	}
	return out
}
