package localcontext

import (
	"net/url"
	"strings"
)

// The three lists of the here brief. They double as feed kinds, so an item
// is routed back to its list by the feed URL it came from.
const (
	hereKindLocal      = "local"
	hereKindDisruption = "disruption"
	hereKindWhatsOn    = "whats_on"
)

type hereFeed struct {
	newsFeed
	Kind string
}

// hereSubject quotes the town and region for a Google News OR query,
// dropping empties and case-insensitive duplicates ("Porto" / "porto").
func hereSubject(p Place) string {
	var parts []string
	seen := map[string]bool{}
	for _, s := range []string{p.Locality, p.Region} {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		parts = append(parts, `"`+s+`"`)
	}
	return strings.Join(parts, " OR ")
}

// hereFeeds builds the three keyless Google News search feeds for a place.
// No town or region means no feeds: country-wide news is the ticker's job.
func hereFeeds(p Place) []hereFeed {
	subject := hereSubject(p)
	if subject == "" {
		return nil
	}
	cc := strings.ToUpper(strings.TrimSpace(p.CountryCode))
	feed := func(kind, q string) hereFeed {
		v := url.Values{"q": {q}, "hl": {"en"}}
		if cc != "" {
			v.Set("gl", cc)
			v.Set("ceid", cc+":en")
		}
		return hereFeed{newsFeed: newsFeed{URL: "https://news.google.com/rss/search?" + v.Encode(), Country: cc}, Kind: kind}
	}
	return []hereFeed{
		feed(hereKindLocal, subject),
		feed(hereKindDisruption, "("+subject+`) (strike OR airport OR train OR closure OR "weather warning" OR traffic)`),
		feed(hereKindWhatsOn, "("+subject+`) (festival OR event OR exhibition OR concert OR "things to do")`),
	}
}
