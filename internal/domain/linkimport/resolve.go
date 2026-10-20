package linkimport

import (
	"context"
	"math"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// pinRadiusMeters is how far a place found by name may sit from where the
// link pinned it and still be the same place.
const pinRadiusMeters = 1000

// POIResolver resolves a candidate through POI search, which finds the place
// in the database or has the model produce and store it. Search is
// poi.Service.SearchPOIsByQueryAndCity.
type POIResolver struct {
	Search func(ctx context.Context, query, city string) ([]locitypes.POIDetailedInfo, error)
}

// Resolve returns the first search result whose name is the candidate's and,
// when the link pinned the place, that sits near the pin. Search ranks by
// meaning, so its first result is often a similar place, not the named one.
func (r POIResolver) Resolve(ctx context.Context, c Candidate) (*Place, error) {
	if c.City == "" {
		return nil, nil
	}
	found, err := r.Search(ctx, c.Name, c.City)
	if err != nil {
		return nil, err
	}
	for _, p := range found {
		if !nameMatch(c.Name, p.Name) {
			continue
		}
		if c.Lat != nil && c.Lon != nil && (p.Latitude != 0 || p.Longitude != 0) &&
			distanceMeters(*c.Lat, *c.Lon, p.Latitude, p.Longitude) > pinRadiusMeters {
			continue
		}
		return &Place{
			ID: p.ID.String(), Name: p.Name, Address: p.Address, Category: p.Category,
			Latitude: p.Latitude, Longitude: p.Longitude,
		}, nil
	}
	return nil, nil
}

// stopwords are left out when names are compared.
var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "of": true, "de": true, "da": true, "do": true,
	"das": true, "dos": true, "del": true, "la": true, "le": true, "les": true, "el": true, "los": true,
	"il": true, "di": true, "e": true, "y": true, "et": true, "und": true, "der": true, "die": true,
}

// normalize folds case and accents and keeps letters, digits and spaces.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.Fields(normalize(s)) {
		if !stopwords[t] {
			out[t] = true
		}
	}
	return out
}

// nameMatch says two names are the same place: every word of the shorter is
// in the longer ("Manteigaria" in "Manteigaria - Fábrica de Pastéis de
// Nata"), or they share at least half their words. Whole words only, so
// "Bar" is not "Barbearia".
func nameMatch(a, b string) bool {
	ta, tb := tokens(a), tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	small, big := ta, tb
	if len(small) > len(big) {
		small, big = big, small
	}
	shared, letters := 0, 0
	for t := range small {
		if big[t] {
			shared++
			letters += len(t)
		}
	}
	if shared == len(small) && letters >= 4 {
		return true
	}
	union := len(ta) + len(tb) - shared
	return float64(shared)/float64(union) >= 0.5
}

func distanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earth = 6371000.0
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earth * math.Asin(math.Sqrt(h))
}
