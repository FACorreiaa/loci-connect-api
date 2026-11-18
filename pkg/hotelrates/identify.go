package hotelrates

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	"github.com/FACorreiaa/loci-connect-api/pkg/placematch"
)

// Hotel is what the partner knows about one hotel, beyond its price.
type Hotel struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Address     string  `json:"address,omitempty"`
	City        string  `json:"city,omitempty"`
	CountryCode string  `json:"country_code,omitempty"`
	// Stars is the hotel's class, 0 when the partner has none.
	Stars float64 `json:"stars,omitempty"`
	// Rating is the partner's guest score out of 10 over ReviewCount reviews.
	// Both are 0 when the partner has none; never show one without the other.
	Rating      float64 `json:"rating,omitempty"`
	ReviewCount int     `json:"review_count,omitempty"`
}

// Identifier finds the partner's record of a hotel a traveller named.
type Identifier interface {
	// Identify returns the hotel and true, or false when nothing is
	// plausibly the same place. It never guesses.
	Identify(ctx context.Context, q Query) (Hotel, bool, error)
	// Facilities lists what the hotel offers ("Free WiFi", "Lift"), at most
	// limit of them. An empty list is an answer, not an error.
	Facilities(ctx context.Context, hotelID string, limit int) ([]string, error)
}

const (
	ttlIdentity   = 7 * 24 * time.Hour
	ttlFacilities = 7 * 24 * time.Hour
	maxFacilities = 40
)

type cachedHotel struct {
	Hotel Hotel `json:"hotel"`
	Found bool  `json:"found"`
}

// Identify implements Identifier. With coordinates it matches as Rates does:
// a close name within a few hundred metres. Without them it needs the city
// and country and a name that is the same but for one word (placematch.NamedBy),
// and it answers false when two hotels fit equally well.
func (l *LiteAPI) Identify(ctx context.Context, q Query) (Hotel, bool, error) {
	hasGeo := q.Lat != 0 || q.Lon != 0
	ignore := ignoreFor(q.City)
	if len(placematch.Tokens(q.Name, ignore)) == 0 {
		return Hotel{}, false, nil
	}
	if !hasGeo && (q.City == "" || q.CountryCode == "") {
		return Hotel{}, false, nil
	}

	key := strings.Replace(matchKey(q, ignore), ":match:", ":identity:", 1)
	var hit cachedHotel
	if l.cacheGet(key, &hit) {
		return hit.Hotel, hit.Found, nil
	}

	ctx, cancel := context.WithTimeout(ctx, providerTimeoutSeconds*time.Second)
	defer cancel()

	attempts := []url.Values{l.hotelsQuery(q, hasGeo, distinctiveWord(q.Name, ignore))}
	if hasGeo {
		attempts = append(attempts, l.hotelsQuery(q, hasGeo, ""))
	}
	for _, params := range attempts {
		resp, err := httpx.GetJSONWithHeader[hotelsResponse](ctx, l.client, Source, l.cfg.BaseURL+"/data/hotels?"+params.Encode(), l.header())
		if err != nil {
			var se *httpx.StatusError
			if errors.As(err, &se) && se.Status >= 400 && se.Status < 500 && se.Status != http.StatusTooManyRequests {
				break
			}
			return Hotel{}, false, errors.Join(ErrUnavailable, err)
		}
		id := ""
		if hasGeo {
			id = pickHotel(q, resp.Data, hasGeo, ignore)
		} else {
			id = pickByName(q, resp.Data, ignore)
		}
		if id == "" {
			continue
		}
		for _, c := range resp.Data {
			if c.ID != id {
				continue
			}
			h := Hotel{
				ID: c.ID, Name: strings.TrimSpace(c.Name), Lat: c.Latitude, Lon: c.Longitude,
				Address: strings.TrimSpace(c.Address), City: strings.TrimSpace(c.City),
				CountryCode: strings.ToUpper(strings.TrimSpace(c.Country)), Stars: c.Stars,
			}
			if c.Rating > 0 && c.ReviewCount > 0 {
				h.Rating, h.ReviewCount = c.Rating, c.ReviewCount
			}
			l.cacheSet(key, cachedHotel{Hotel: h, Found: true}, ttlIdentity)
			l.cacheSet(matchKey(q, ignore), cachedMatch{ID: id}, ttlMatchHit)
			return h, true, nil
		}
	}
	l.cacheSet(key, cachedHotel{}, ttlMatchMiss)
	return Hotel{}, false, nil
}

// pickByName is the one candidate in q's city that q.Name names, or "" when
// none does or two do equally well.
func pickByName(q Query, candidates []liteHotel, ignore map[string]bool) string {
	best, bestExtra, tied := "", 0, false
	for _, c := range candidates {
		if c.ID == "" || placematch.Fold(c.City) != placematch.Fold(q.City) {
			continue
		}
		ok, extra := placematch.NamedBy(q.Name, c.Name, ignore)
		if !ok {
			continue
		}
		switch {
		case best == "" || extra < bestExtra:
			best, bestExtra, tied = c.ID, extra, false
		case extra == bestExtra:
			tied = true
		}
	}
	if tied {
		return ""
	}
	return best
}

type hotelDetailResponse struct {
	Data struct {
		HotelFacilities []string `json:"hotelFacilities"`
		Facilities      []struct {
			Name string `json:"name"`
		} `json:"facilities"`
	} `json:"data"`
}

// Facilities implements Identifier.
func (l *LiteAPI) Facilities(ctx context.Context, hotelID string, limit int) ([]string, error) {
	hotelID = strings.TrimSpace(hotelID)
	if hotelID == "" || limit <= 0 {
		return nil, nil
	}
	key := "hotelrates:lite:v1:facilities:" + hotelID
	var all []string
	if !l.cacheGet(key, &all) {
		ctx, cancel := context.WithTimeout(ctx, providerTimeoutSeconds*time.Second)
		defer cancel()
		resp, err := httpx.GetJSONWithHeader[hotelDetailResponse](ctx, l.client, Source,
			l.cfg.BaseURL+"/data/hotel?hotelId="+url.QueryEscape(hotelID), l.header())
		if err != nil {
			return nil, errors.Join(ErrUnavailable, err)
		}
		seen := map[string]bool{}
		add := func(name string) {
			name = strings.TrimSpace(name)
			if name == "" || seen[strings.ToLower(name)] || len(all) >= maxFacilities {
				return
			}
			seen[strings.ToLower(name)] = true
			all = append(all, name)
		}
		for _, f := range resp.Data.HotelFacilities {
			add(f)
		}
		for _, f := range resp.Data.Facilities {
			add(f.Name)
		}
		if all == nil {
			all = []string{}
		}
		l.cacheSet(key, all, ttlFacilities)
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
