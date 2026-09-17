package localcontext

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

// The breaking-news ticker on the traveller's desk. Headlines come from the
// cluster's shared feed aggregator (platform/infra/docs/feeds.md), selected
// per person: home country, next destination, recent visits. Headline,
// source, time and link only — never a body.
//
// It is deliberately not a SignalSource: alerts feed the go-score disruption
// penalty, and general news must never move a score.

const (
	// SourceNews labels aggregator calls in metrics and cache keys.
	SourceNews = "feeds"
	// maxNewsCountries caps the selection: home, next trip, three visited.
	maxNewsCountries = 5
	// maxNewsFeeds is what one aggregator call accepts.
	maxNewsFeeds = 25
	// newsSelectionCacheTTL is how long one person's ticker is reused.
	newsSelectionCacheTTL = ttlNews
)

// VisitedCountry is a place the person has been, newest first.
type VisitedCountry struct {
	Code string
	At   time.Time
}

// HomeSource, NextTripSource and VisitedSource are the three questions the
// selection asks. Adapters over the user, trip and travel-history domains
// live in cmd/api so this package imports none of them (trip already
// imports this package for packing).
type (
	HomeSource interface {
		HomeCountry(ctx context.Context, userID uuid.UUID) (string, error)
	}
	NextTripSource interface {
		NextTripCountry(ctx context.Context, userID uuid.UUID, now time.Time) (string, error)
	}
	VisitedSource interface {
		RecentCountries(ctx context.Context, userID uuid.UUID, limit int) ([]VisitedCountry, error)
	}
	// NewsTickerPrefs is the per-user switch.
	NewsTickerPrefs interface {
		NewsTickerEnabled(ctx context.Context, userID uuid.UUID) (bool, error)
		SetNewsTickerEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error
	}
)

// NewsItem is one headline as the handler renders it.
type NewsItem struct {
	ID          string
	Title       string
	URL         string
	Source      string
	PublishedAt time.Time
	CountryCode string
}

// NewsTicker is the strip for one person.
type NewsTicker struct {
	Enabled      bool
	Stale        bool
	CountryCodes []string
	Items        []NewsItem
}

// NewsTickerDeps wires the service.
type NewsTickerDeps struct {
	Client   *httpx.Client
	BaseURL  string
	Home     HomeSource
	NextTrip NextTripSource
	Visited  VisitedSource
	Prefs    NewsTickerPrefs
	Cache    *signalCache
	Logger   *slog.Logger
}

type NewsTickerService struct {
	d NewsTickerDeps
}

func NewNewsTickerService(d NewsTickerDeps) *NewsTickerService {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &NewsTickerService{d: d}
}

// SetEnabled flips the switch and drops the cached strip so the next read
// reflects it immediately.
func (s *NewsTickerService) SetEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error {
	return s.d.Prefs.SetNewsTickerEnabled(ctx, userID, enabled)
}

// Ticker builds the strip. An unreachable aggregator yields an empty, stale
// strip rather than an error: news is a nicety and must never fail the desk.
func (s *NewsTickerService) Ticker(ctx context.Context, userID uuid.UUID, limit int) (NewsTicker, error) {
	enabled, err := s.d.Prefs.NewsTickerEnabled(ctx, userID)
	if err != nil {
		return NewsTicker{}, err
	}
	if !enabled {
		return NewsTicker{Enabled: false}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	now := time.Now()
	in := s.inputs(ctx, userID, now)
	codes := newsCountries(in)
	out := NewsTicker{Enabled: true, CountryCodes: codes}
	feeds := feedsForCountries(codes)
	if len(feeds) == 0 {
		return out, nil
	}
	key := fmt.Sprintf("%s|%d", strings.Join(codes, ","), limit)
	if cached, ok := cacheGet[NewsTicker](s.d.Cache, SourceNews, key); ok {
		return cached, nil
	}
	doc, err := s.fetch(ctx, feeds, limit)
	if err != nil {
		s.d.Logger.WarnContext(ctx, "news ticker: aggregator unavailable; serving empty", slog.Any("error", err))
		out.Stale = true
		return out, nil
	}
	countryByFeed := make(map[string]string, len(feeds))
	for _, f := range feeds {
		countryByFeed[f.URL] = f.Country
	}
	for _, it := range doc.Items {
		out.Items = append(out.Items, NewsItem{
			ID: it.ID, Title: it.Title, URL: it.URL, Source: it.Feeds.SourceName,
			PublishedAt: it.DatePublished, CountryCode: countryByFeed[it.Feeds.FeedURL],
		})
	}
	out.Stale = len(doc.Feeds.Stale) > 0
	cacheSet(s.d.Cache, SourceNews, key, out, newsSelectionCacheTTL)
	return out, nil
}

// inputs gathers the three answers, each optional: a failed lookup logs and
// contributes nothing.
func (s *NewsTickerService) inputs(ctx context.Context, userID uuid.UUID, now time.Time) newsInputs {
	var in newsInputs
	if s.d.Home != nil {
		if home, err := s.d.Home.HomeCountry(ctx, userID); err == nil {
			in.Home = home
		} else {
			s.d.Logger.DebugContext(ctx, "news ticker: home country", slog.Any("error", err))
		}
	}
	if s.d.NextTrip != nil {
		if code, err := s.d.NextTrip.NextTripCountry(ctx, userID, now); err == nil {
			in.NextTrip = code
		} else {
			s.d.Logger.DebugContext(ctx, "news ticker: next trip", slog.Any("error", err))
		}
	}
	if s.d.Visited != nil {
		if visits, err := s.d.Visited.RecentCountries(ctx, userID, 20); err == nil {
			in.Visited = visits
		} else {
			s.d.Logger.DebugContext(ctx, "news ticker: visited", slog.Any("error", err))
		}
	}
	return in
}

type jsonFeedDoc struct {
	Items []struct {
		ID            string    `json:"id"`
		URL           string    `json:"url"`
		Title         string    `json:"title"`
		DatePublished time.Time `json:"date_published"`
		Feeds         struct {
			SourceName string `json:"source_name"`
			FeedURL    string `json:"feed_url"`
		} `json:"_feeds"`
	} `json:"items"`
	Feeds struct {
		Warming []string `json:"warming"`
		Stale   []string `json:"stale"`
	} `json:"_feeds"`
}

func (s *NewsTickerService) fetch(ctx context.Context, feeds []newsFeed, limit int) (jsonFeedDoc, error) {
	urls := make([]string, 0, len(feeds))
	for _, f := range feeds {
		urls = append(urls, f.URL)
	}
	q := url.Values{"feeds": {strings.Join(urls, ",")}, "limit": {fmt.Sprint(limit)}}
	endpoint := strings.TrimRight(s.d.BaseURL, "/") + "/v1/items?" + q.Encode()
	return httpx.GetJSON[jsonFeedDoc](ctx, s.d.Client, SourceNews, endpoint)
}

// --- selection (pure) ------------------------------------------------------

type newsInputs struct {
	Home     string
	NextTrip string
	Visited  []VisitedCountry
}

// newsCountries orders home, next trip, then visits newest-first, deduped,
// capped. Free-text home names are resolved through countryCodeFromName.
func newsCountries(in newsInputs) []string {
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		code := countryCodeFromName(raw)
		if code == "" || seen[code] || len(out) >= maxNewsCountries {
			return
		}
		seen[code] = true
		out = append(out, code)
	}
	add(in.Home)
	add(in.NextTrip)
	visits := make([]VisitedCountry, len(in.Visited))
	copy(visits, in.Visited)
	sort.SliceStable(visits, func(i, j int) bool { return visits[i].At.After(visits[j].At) })
	for _, v := range visits {
		add(v.Code)
	}
	return out
}

type newsFeed struct {
	URL     string
	Country string
}

// feedsForCountries builds two keyless Google News feeds per country: the
// country's top stories and a query for the things a traveller must know
// about (weather warnings, storms, strikes, safety). Google News RSS is
// undocumented but stable; the aggregator's stale-while-error cache covers
// the days it hiccups.
func feedsForCountries(codes []string) []newsFeed {
	var out []newsFeed
	for _, code := range codes {
		if len(out)+2 > maxNewsFeeds {
			break
		}
		name := countryName(code)
		out = append(out,
			newsFeed{URL: fmt.Sprintf("https://news.google.com/rss?hl=en&gl=%s&ceid=%s:en", code, code), Country: code},
			newsFeed{URL: fmt.Sprintf("https://news.google.com/rss/search?q=%s&hl=en&gl=%s&ceid=%s:en",
				url.QueryEscape(name+" (weather warning OR storm OR strike OR safety OR travel)"), code, code), Country: code},
		)
	}
	return out
}
