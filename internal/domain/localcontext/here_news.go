package localcontext

import (
	"context"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
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

const (
	hereNewsPerList = 5
	ttlHereNews     = 10 * time.Minute
	sourceHereNews  = SourceNews + "-here"
)

// HereNews is the three headline lists of the here brief.
type HereNews struct {
	Stale      bool
	Local      []NewsItem
	Disruption []NewsItem
	WhatsOn    []NewsItem
}

// Here fetches the three lists for a place in one aggregator call. Like
// Ticker, an unreachable aggregator yields empty lists, not an error, and the
// person's news switch turns the lists off.
func (s *NewsTickerService) Here(ctx context.Context, userID uuid.UUID, p Place) (HereNews, error) {
	enabled, err := s.d.Prefs.NewsTickerEnabled(ctx, userID)
	if err != nil {
		return HereNews{}, err
	}
	feeds := hereFeeds(p)
	if !enabled || len(feeds) == 0 {
		return HereNews{}, nil
	}
	key := strings.ToLower(p.Locality + "|" + p.Region + "|" + p.CountryCode)
	if cached, ok := cacheGet[HereNews](s.d.Cache, sourceHereNews, key); ok {
		return cached, nil
	}
	plain := make([]newsFeed, len(feeds))
	for i, f := range feeds {
		plain[i] = f.newsFeed
	}
	doc, err := s.fetch(ctx, plain, 3*hereNewsPerList*2)
	if err != nil {
		s.d.Logger.WarnContext(ctx, "here news: aggregator failed; returning empty", slog.Any("error", err))
		return HereNews{}, nil
	}
	out := splitHereItems(doc, feeds)
	cacheSet(s.d.Cache, sourceHereNews, key, out, ttlHereNews)
	return out, nil
}

// splitHereItems routes each item to its list by the feed it came from,
// newest first, dropping a URL already used by an earlier list (local, then
// disruption, then what's on) and capping each list.
func splitHereItems(doc jsonFeedDoc, feeds []hereFeed) HereNews {
	kindByURL := make(map[string]string, len(feeds))
	countryByURL := make(map[string]string, len(feeds))
	for _, f := range feeds {
		kindByURL[f.URL] = f.Kind
		countryByURL[f.URL] = f.Country
	}
	items := append(doc.Items[:0:0], doc.Items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].DatePublished.After(items[j].DatePublished) })

	out := HereNews{Stale: len(doc.Feeds.Stale) > 0}
	seen := map[string]bool{}
	for _, kind := range []string{hereKindLocal, hereKindDisruption, hereKindWhatsOn} {
		list := &out.Local
		switch kind {
		case hereKindDisruption:
			list = &out.Disruption
		case hereKindWhatsOn:
			list = &out.WhatsOn
		}
		for _, it := range items {
			if len(*list) >= hereNewsPerList {
				break
			}
			if kindByURL[it.Feeds.FeedURL] != kind || seen[it.URL] {
				continue
			}
			seen[it.URL] = true
			*list = append(*list, NewsItem{
				ID: it.ID, Title: it.Title, URL: it.URL, Source: it.Feeds.SourceName,
				PublishedAt: it.DatePublished, CountryCode: countryByURL[it.Feeds.FeedURL],
			})
		}
	}
	return out
}
