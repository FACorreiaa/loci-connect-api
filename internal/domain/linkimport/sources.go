package linkimport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var googleHost = regexp.MustCompile(`^google\.[a-z]{2,3}(\.[a-z]{2})?$`)

func detectSource(u *url.URL) Source {
	host := strings.ToLower(u.Hostname())
	bare := strings.TrimPrefix(strings.TrimPrefix(host, "www."), "m.")
	switch {
	case host == "maps.app.goo.gl",
		bare == "goo.gl" && strings.HasPrefix(u.Path, "/maps"),
		strings.HasPrefix(host, "maps.google."),
		googleHost.MatchString(bare) && strings.HasPrefix(u.Path, "/maps"):
		return SourceGoogleMaps
	case bare == "tiktok.com" || strings.HasSuffix(host, ".tiktok.com"):
		return SourceTikTok
	case bare == "instagram.com" || strings.HasSuffix(host, ".instagram.com"):
		return SourceInstagram
	case bare == "youtube.com" || strings.HasSuffix(host, ".youtube.com") || bare == "youtu.be":
		return SourceYouTube
	}
	return SourceWeb
}

var (
	// !3d<lat>!4d<lon> is the place itself, inside a Maps data= segment.
	mapsPin = regexp.MustCompile(`!3d(-?\d+(?:\.\d+)?)!4d(-?\d+(?:\.\d+)?)`)
	// @<lat>,<lon>,<zoom> is only where the map was centred.
	mapsAt     = regexp.MustCompile(`@(-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?)`)
	coordsOnly = regexp.MustCompile(`^\s*-?\d+(?:\.\d+)?\s*,\s*-?\d+(?:\.\d+)?\s*$`)
)

// mapsPlace reads a Google Maps URL: the place's name, and where it is when
// the URL says. ok is false for a URL that points at a map, not a place.
func mapsPlace(u *url.URL) (name string, lat, lon *float64, ok bool) {
	segs := strings.Split(u.Path, "/")
	for i, s := range segs {
		if s == "place" && i+1 < len(segs) && segs[i+1] != "" && !strings.HasPrefix(segs[i+1], "@") {
			name = strings.ReplaceAll(segs[i+1], "+", " ")
			break
		}
	}
	if name == "" {
		q := u.Query()
		name = firstNonEmpty(q.Get("q"), q.Get("query"))
		if coordsOnly.MatchString(name) {
			name = ""
		}
	}
	// A searched address ("Manteigaria, R. do Loreto 2, Lisboa"): the place is
	// what comes before the street.
	name, _, _ = strings.Cut(name, ",")
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, nil, false
	}
	raw := u.String()
	if m := mapsPin.FindStringSubmatch(raw); m != nil {
		lat, lon = parseCoords(m[1], m[2])
	} else if m := mapsAt.FindStringSubmatch(raw); m != nil {
		lat, lon = parseCoords(m[1], m[2])
	}
	return clip(name, maxNameRunes), lat, lon, true
}

func parseCoords(a, b string) (*float64, *float64) {
	lat, err1 := strconv.ParseFloat(a, 64)
	lon, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return nil, nil
	}
	return &lat, &lon
}

// readMaps finds the place a Maps link points at. Short links are followed,
// and every hop is tried: in the EU the last one is often a consent page
// that carries the real URL in ?continue=.
func (s *Service) readMaps(ctx context.Context, u *url.URL) (*Candidate, error) {
	urls := []string{u.String()}
	host := strings.ToLower(u.Hostname())
	if host == "maps.app.goo.gl" || strings.HasSuffix(host, "goo.gl") {
		// Stop at the first hop that names a place: the Maps page behind it
		// is large, and Google often refuses it from a datacenter.
		page, err := s.fetch.GetUntil(ctx, u.String(), func(hop *url.URL) bool {
			_, _, _, ok := mapsPlace(hop)
			return detectSource(hop) == SourceGoogleMaps && ok
		})
		urls = append(urls, page.Hops...)
		urls = append(urls, page.FinalURL)
		if err != nil && len(page.Hops) <= 1 {
			return nil, fetchErr(err)
		}
	}
	for _, raw := range urls {
		hop, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if strings.HasPrefix(hop.Hostname(), "consent.") {
			if next, err := url.Parse(hop.Query().Get("continue")); err == nil {
				hop = next
			}
		}
		if detectSource(hop) != SourceGoogleMaps {
			continue
		}
		if name, lat, lon, ok := mapsPlace(hop); ok {
			return &Candidate{Name: name, Lat: lat, Lon: lon}, nil
		}
	}
	return nil, fmt.Errorf("%w: the Maps link doesn't point at a place", ErrUnreadable)
}

func mapsDoc(c Candidate) Doc {
	text := "Google Maps place: " + c.Name
	if c.Lat != nil && c.Lon != nil {
		text += fmt.Sprintf(" at %.5f,%.5f", *c.Lat, *c.Lon)
	}
	return Doc{Title: c.Name, Text: text}
}

// readTikTok reads the video's caption through TikTok's public oEmbed, which
// answers without a browser; the page itself is the fallback.
func (s *Service) readTikTok(ctx context.Context, u *url.URL) (Doc, error) {
	page, err := s.fetch.Get(ctx, "https://www.tiktok.com/oembed?url="+url.QueryEscape(u.String()))
	if err == nil {
		var o struct {
			Title  string `json:"title"`
			Author string `json:"author_name"`
		}
		if json.Unmarshal(page.Body, &o) == nil && strings.TrimSpace(o.Title) != "" {
			doc := Doc{Title: collapse(o.Title), Text: collapse(o.Title)}
			if o.Author != "" {
				doc.Description = "TikTok video by @" + o.Author
			}
			return doc, nil
		}
	}
	return s.readPage(ctx, u.String())
}
