package linkimport

import (
	"context"
	"math"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// pinRadiusMeters is how far a place found by name may sit from where the
// link pinned it and still be the same place.
const pinRadiusMeters = 1000

// POIResolver matches a candidate to a place already in Loci: the city by
// name (never created), then the city's places by name (trigram), then by
// meaning. It never generates or stores a place: a stranger's page must not
// add rows to the shared catalogue, and generation per place would make one
// import cost ten model calls. A place Loci does not have yet is a miss.
type POIResolver struct {
	// FindCity is the existing city with this name; false when there is none.
	FindCity func(ctx context.Context, name string) (uuid.UUID, bool, error)
	// ByName is the city's places whose name is like this one.
	ByName func(ctx context.Context, cityID uuid.UUID, name string) ([]locitypes.POIDetailedInfo, error)
	// Semantic is poi.Service.SearchPOIsSemanticByCity; nil skips it.
	Semantic func(ctx context.Context, query string, cityID uuid.UUID, limit int) ([]locitypes.POIDetailedInfo, error)
}

// Resolve returns the place the candidate names, or nil.
func (r POIResolver) Resolve(ctx context.Context, c Candidate) (*Place, error) {
	city, _, _ := strings.Cut(c.City, ",")
	city = strings.TrimSpace(city)
	if city == "" {
		return nil, nil
	}
	cityID, ok, err := r.FindCity(ctx, city)
	if err != nil || !ok {
		return nil, err
	}
	found, err := r.ByName(ctx, cityID, c.Name)
	if err != nil {
		return nil, err
	}
	if p := bestMatch(c, found); p != nil {
		return p, nil
	}
	if r.Semantic == nil {
		return nil, nil
	}
	found, err = r.Semantic(ctx, c.Name, cityID, 10)
	if err != nil {
		return nil, err
	}
	return bestMatch(c, found), nil
}

// bestMatch is the result named exactly as the candidate, else the one that
// names it with the fewest extra words; never one away from the link's pin.
// Search order is by meaning, so the first match is often the wrong one.
func bestMatch(c Candidate, found []locitypes.POIDetailedInfo) *Place {
	want := normalize(c.Name)
	best, bestExtra := -1, 0
	for i, p := range found {
		if !nameMatch(c.Name, p.Name) {
			continue
		}
		if c.Lat != nil && c.Lon != nil && (p.Latitude != 0 || p.Longitude != 0) &&
			distanceMeters(*c.Lat, *c.Lon, p.Latitude, p.Longitude) > pinRadiusMeters {
			continue
		}
		extra := len(tokens(p.Name)) - len(tokens(c.Name))
		if normalize(p.Name) == want {
			extra = -1
		}
		if best < 0 || extra < bestExtra {
			best, bestExtra = i, extra
		}
	}
	if best < 0 {
		return nil
	}
	p := found[best]
	return &Place{
		ID: p.ID.String(), Name: p.Name, Address: p.Address, Category: p.Category,
		Latitude: p.Latitude, Longitude: p.Longitude,
	}
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

// generic words say what kind of place it is, not which one. A name needs
// at least one other word to match anything but itself.
var generic = map[string]bool{
	"museum": true, "museu": true, "museo": true, "musee": true, "gallery": true, "galeria": true,
	"garden": true, "gardens": true, "jardim": true, "jardin": true, "park": true, "parque": true, "parco": true,
	"church": true, "igreja": true, "iglesia": true, "chiesa": true, "cathedral": true, "se": true, "basilica": true,
	"national": true, "nacional": true, "nazionale": true, "botanical": true, "botanico": true,
	"cafe": true, "caffe": true, "coffee": true, "bar": true, "pub": true, "restaurant": true, "restaurante": true,
	"ristorante": true, "bistro": true, "hotel": true, "hostel": true, "market": true, "mercado": true, "mercato": true,
	"street": true, "rua": true, "calle": true, "via": true, "avenida": true, "square": true, "praca": true,
	"plaza": true, "piazza": true, "tower": true, "torre": true, "palace": true, "palacio": true, "palazzo": true,
	"beach": true, "praia": true, "playa": true, "bakery": true, "pastelaria": true, "padaria": true,
	"monastery": true, "mosteiro": true, "castle": true, "castelo": true, "castillo": true, "viewpoint": true,
	"miradouro": true, "mirador": true, "sao": true, "santa": true, "santo": true, "san": true, "saint": true, "st": true,
}

// nameMatch says the place named placeName is the one the link calls
// linkName: every word of the link's name is in the place's name, whole
// words only and accents and case aside ("Manteigaria" is "Manteigaria -
// Fábrica de Pastéis de Nata"; "Bar" is not "Barbearia"). Sharing the kind of
// place is not enough: "Museu Nacional do Azulejo" is not "Museu Nacional de
// Arqueologia", and "Bairro Alto Hotel" is not "Bairro Alto". A name of only
// generic words matches only itself.
func nameMatch(linkName, placeName string) bool {
	want, have := tokens(linkName), tokens(placeName)
	if len(want) == 0 || len(have) == 0 {
		return false
	}
	distinctive, letters := false, 0
	for t := range want {
		if !have[t] {
			return false
		}
		letters += len(t)
		if !generic[t] {
			distinctive = true
		}
	}
	if !distinctive {
		return normalize(linkName) == normalize(placeName)
	}
	return letters >= 4
}

func distanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earth = 6371000.0
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earth * math.Asin(math.Sqrt(h))
}
