package localcontext

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/concurrency"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

const gdeltBaseURL = "https://api.gdeltproject.org"

// Measured against the live API while this was written: a simple query answered
// in 23-26s, and every narrower query — quoted phrases, theme: operators,
// boolean groups — timed out past 60s. That is one to three orders of magnitude
// slower than every other source here (Open-Meteo answers in ~0.2s), and far
// past the Gatherer's fan-out budget.
//
// So this source never blocks a request. See NewsSource.
const (
	gdeltTimeout = 75 * time.Second
	gdeltTTL     = 20 * time.Minute
	// maxNewsAlerts caps what one destination can contribute. News is the
	// weakest evidence here and the noisiest source; without a cap a busy news
	// day would crowd out the hazard and holiday alerts that are actually
	// verified facts.
	maxNewsAlerts = 3
)

// NewsSource surfaces possible travel disruption — transport strikes, protests,
// closures — from GDELT's global news index.
//
// # Why this source is shaped so differently from the others
//
// GDELT is slow and imprecise, and both were measured rather than assumed:
//
//   - Latency: 23-26s for a query that completes; 60s+ timeouts for any query
//     narrow enough to be precise.
//   - Precision: a live `strike sourcecountry:FR` query returned five articles
//     — a hunger strike, Russian military strikes on Ukraine, a football match,
//     a climate feature, and one marginal union notice. Nothing a traveller
//     could act on. "Strike" is hopelessly polysemous, and `sourcecountry:`
//     filters by where the *publisher* is, not what the story is about.
//
// Two consequences, both deliberate:
//
//  1. **It never blocks a request.** Fetch returns whatever is cached and, if
//     that is stale, kicks off a detached refresh. A first call for a new
//     destination therefore returns nothing; a later one has data. Serving a
//     slightly old answer instantly is strictly better than making a trip view
//     wait half a minute for news.
//  2. **It is off by default.** GDELT_ENABLED defaults to false. Given the
//     precision measured above, this earns its place only where an operator has
//     looked at the output and decided it helps.
//
// A NewsClassifier can be attached to filter the noise properly. Without one,
// only the heuristic prefilter applies and the output should be treated as a
// lead, not a fact — which is why every alert it produces is Minor.
type NewsSource struct {
	baseURL    string
	client     *httpx.Client
	classifier NewsClassifier
	logger     *slog.Logger

	mu         sync.Mutex
	cache      map[string]newsEntry
	refreshing map[string]bool
	ttl        time.Duration
	now        func() time.Time
}

type newsEntry struct {
	alerts   []Alert
	cachedAt time.Time
}

// NewNewsSource builds the source. A nil classifier leaves only the heuristic
// prefilter in place.
func NewNewsSource(baseURL string, client *httpx.Client, classifier NewsClassifier, logger *slog.Logger) *NewsSource {
	if baseURL == "" {
		baseURL = gdeltBaseURL
	}
	return &NewsSource{
		baseURL:    baseURL,
		client:     client,
		classifier: classifier,
		logger:     logger,
		cache:      make(map[string]newsEntry),
		refreshing: make(map[string]bool),
		ttl:        gdeltTTL,
		now:        time.Now,
	}
}

func (s *NewsSource) Name() string { return SourceNews }

// Fetch returns cached alerts immediately and refreshes in the background when
// they are stale. It never waits on GDELT.
func (s *NewsSource) Fetch(ctx context.Context, req SignalRequest) ([]Alert, error) {
	if req.CountryCode == "" {
		return nil, nil
	}
	key := req.CountryCode

	s.mu.Lock()
	entry, hit := s.cache[key]
	fresh := hit && s.now().Sub(entry.cachedAt) < s.ttl
	alreadyRefreshing := s.refreshing[key]
	if !fresh && !alreadyRefreshing {
		s.refreshing[key] = true
	}
	s.mu.Unlock()

	if !fresh && !alreadyRefreshing {
		s.refreshInBackground(ctx, key)
	}

	// Filter whatever we hold to this caller's window. The cache is per
	// country, not per trip, so two travellers with different dates share the
	// same fetch.
	var out []Alert
	for _, a := range entry.alerts {
		if a.Date == nil || withinWindow(*a.Date, req.Start, req.End) {
			out = append(out, a)
		}
	}
	return out, nil
}

// refreshInBackground detaches the slow call from the request.
//
// context.WithoutCancel is what makes this work: the RPC's context dies when
// the response is written, which would cancel a 26s fetch every single time.
// This is the same pattern the chat handler uses to let an LLM call outlive the
// stream that started it.
func (s *NewsSource) refreshInBackground(ctx context.Context, country string) {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), gdeltTimeout+15*time.Second)

	concurrency.Run(s.logger, func() {
		defer cancel()
		defer func() {
			s.mu.Lock()
			delete(s.refreshing, country)
			s.mu.Unlock()
		}()

		alerts, err := s.load(detached, country)
		if err != nil {
			if s.logger != nil {
				s.logger.WarnContext(detached, "news: refresh failed; keeping previous alerts",
					slog.String("country", country), slog.Any("error", err))
			}
			return
		}

		s.mu.Lock()
		s.cache[country] = newsEntry{alerts: alerts, cachedAt: s.now()}
		s.mu.Unlock()
	})
}

type gdeltResponse struct {
	Articles []gdeltArticle `json:"articles"`
}

type gdeltArticle struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Domain        string `json:"domain"`
	Language      string `json:"language"`
	SourceCountry string `json:"sourcecountry"`
	SeenDate      string `json:"seendate"`
}

func (s *NewsSource) load(ctx context.Context, country string) ([]Alert, error) {
	// Deliberately a loose query. The narrow ones that would cut noise do not
	// complete — quoted phrases and theme: operators both timed out past 60s
	// against the live API — so filtering happens here instead of upstream.
	q := url.Values{}
	q.Set("query", fmt.Sprintf("strike sourcecountry:%s", strings.ToUpper(country)))
	q.Set("mode", "artlist")
	q.Set("format", "json")
	q.Set("timespan", "3d")
	q.Set("maxrecords", "30")

	endpoint := s.baseURL + "/api/v2/doc/doc?" + q.Encode()
	body, err := httpx.GetJSON[gdeltResponse](ctx, s.client, SourceNews, endpoint)
	if err != nil {
		return nil, err
	}

	candidates := make([]gdeltArticle, 0, len(body.Articles))
	for _, a := range body.Articles {
		if a.Title == "" || !looksLikeTravelDisruption(a.Title) {
			continue
		}
		candidates = append(candidates, a)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	keep := candidates
	if s.classifier != nil {
		titles := make([]string, len(candidates))
		for i, a := range candidates {
			titles[i] = a.Title
		}
		verdicts, cerr := s.classifier.Classify(ctx, country, titles)
		if cerr != nil {
			// Without the classifier the heuristic result stands. It is worse,
			// but the alerts are Minor and clearly sourced either way.
			if s.logger != nil {
				s.logger.WarnContext(ctx, "news: classification failed; falling back to heuristics",
					slog.Any("error", cerr))
			}
		} else {
			keep = keep[:0]
			for i, v := range verdicts {
				if i < len(candidates) && v.Relevant {
					keep = append(keep, candidates[i])
				}
			}
		}
	}

	if len(keep) > maxNewsAlerts {
		keep = keep[:maxNewsAlerts]
	}

	out := make([]Alert, 0, len(keep))
	for _, a := range keep {
		seen := parseGDELTTime(a.SeenDate)
		var date *time.Time
		if !seen.IsZero() {
			d := seen
			date = &d
		}
		out = append(out, Alert{
			Kind:  AlertStrike,
			Title: a.Title,
			// Naming the outlet matters more here than for any other source:
			// this is a headline, not a measurement, and the user should be able
			// to weigh it themselves.
			Detail: fmt.Sprintf("Reported by %s — unverified, check locally before relying on it", a.Domain),
			Date:   date,
			// Always Minor. A news article is the weakest evidence in this
			// system and must never outweigh a measured hazard.
			Severity: SeverityMinor,
			Source:   SourceNews,
		})
	}
	return out, nil
}

// disruptionTerms are the senses of "strike" and friends worth surfacing.
var disruptionTerms = []string{
	"transport", "rail", "train", "metro", "subway", "tram", "bus",
	"airport", "airline", "flight", "ferry", "port",
	"grève", "greve", "huelga", "sciopero", "streik", "staking",
	"walkout", "shutdown", "closure", "closed", "cancel",
}

// falseSenseTerms are the meanings of "strike" that have nothing to do with
// travel. Every one of these was observed in a single live query: Russian
// military strikes on Ukraine, activists on hunger strike, and a football
// match all came back under `strike sourcecountry:FR`.
var falseSenseTerms = []string{
	"airstrike", "air strike", "missile", "drone strike", "military",
	"lightning", "gold strike", "oil strike",
	"strike force", "strikeout", "strike out", "bowling",
	"goal", "striker", "football", "soccer", "league", "ligue", "match",
	// Hunger strikes, in the languages the disruption terms below also cover.
	// The English-only list missed "grève de la faim" in a real result — the
	// query is scoped by country, so the headlines are usually not in English.
	"hunger strike", "grève de la faim", "greve de la faim",
	"huelga de hambre", "sciopero della fame", "hungerstreik",
}

// looksLikeTravelDisruption is a cheap prefilter run before any model call.
//
// It cannot be accurate — that is what the classifier is for — but it removes
// the obviously-wrong senses for free, and it is the only filter at all when no
// classifier is configured.
func looksLikeTravelDisruption(title string) bool {
	t := strings.ToLower(title)
	for _, bad := range falseSenseTerms {
		if strings.Contains(t, bad) {
			return false
		}
	}
	for _, good := range disruptionTerms {
		if strings.Contains(t, good) {
			return true
		}
	}
	return false
}

// parseGDELTTime reads GDELT's compact timestamp, e.g. 20260827T183000Z.
func parseGDELTTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{"20060102T150405Z", time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
