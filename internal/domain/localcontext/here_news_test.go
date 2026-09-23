package localcontext

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

func feedQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestHereFeedsBothNames(t *testing.T) {
	feeds := hereFeeds(Place{Locality: "Viana do Castelo", Region: "Viana do Castelo District", CountryCode: "pt"})
	if len(feeds) != 3 {
		t.Fatalf("want 3 feeds, got %d", len(feeds))
	}
	kinds := []string{feeds[0].Kind, feeds[1].Kind, feeds[2].Kind}
	if strings.Join(kinds, ",") != "local,disruption,whats_on" {
		t.Errorf("kinds = %v", kinds)
	}
	q := feedQuery(t, feeds[0].URL)
	if q.Get("q") != `"Viana do Castelo" OR "Viana do Castelo District"` || q.Get("gl") != "PT" || q.Get("ceid") != "PT:en" {
		t.Errorf("local query = %v", q)
	}
	if !strings.Contains(feedQuery(t, feeds[1].URL).Get("q"), "strike OR airport") {
		t.Errorf("disruption query = %s", feeds[1].URL)
	}
	if !strings.Contains(feedQuery(t, feeds[2].URL).Get("q"), "festival OR event") {
		t.Errorf("whats_on query = %s", feeds[2].URL)
	}
	if feeds[0].Country != "PT" {
		t.Errorf("country = %q", feeds[0].Country)
	}
}

func TestHereFeedsRegionOnly(t *testing.T) {
	feeds := hereFeeds(Place{Region: "Minho", CountryCode: "PT"})
	if len(feeds) != 3 || feedQuery(t, feeds[0].URL).Get("q") != `"Minho"` {
		t.Fatalf("region-only place must still produce feeds: %+v", feeds)
	}
}

func TestHereFeedsDedupesSameName(t *testing.T) {
	q := feedQuery(t, hereFeeds(Place{Locality: "Porto", Region: "porto", CountryCode: "PT"})[0].URL).Get("q")
	if q != `"Porto"` {
		t.Errorf("same name twice should collapse, got %s", q)
	}
}

func TestHereFeedsNoNames(t *testing.T) {
	if got := hereFeeds(Place{CountryCode: "PT"}); len(got) != 0 {
		t.Errorf("no town or region → no feeds (country news is the ticker's job), got %d", len(got))
	}
}

func TestHereFeedsNoCountryOmitsGL(t *testing.T) {
	q := feedQuery(t, hereFeeds(Place{Locality: "Somewhere"})[0].URL)
	if q.Has("gl") || q.Has("ceid") || q.Get("hl") != "en" {
		t.Errorf("no country → hl only, got %v", q)
	}
}

var herePlace = Place{Locality: "Viana do Castelo", Region: "Viana do Castelo District", CountryCode: "PT", CountryName: "Portugal"}

// hereHarness answers each aggregator call with the items of the one feed it
// asked for, the way the aggregator tags them, and counts the calls.
func hereHarness(t *testing.T, prefs *memPrefs) (*NewsTickerService, *int) {
	t.Helper()
	feeds := hereFeeds(herePlace)
	var mu sync.Mutex
	hits := 0
	item := func(id, url, feed string, hoursAgo int) string {
		return fmt.Sprintf(`{"id":%q,"url":%q,"title":"T %s","date_published":%q,"_feeds":{"source_name":"S","feed_url":%q}}`,
			id, url, id, time.Now().Add(-time.Duration(hoursAgo)*time.Hour).UTC().Format(time.RFC3339), feed)
	}
	byFeed := map[string][]string{}
	for i := range 7 { // 7 local items → capped at 5
		byFeed[feeds[0].URL] = append(byFeed[feeds[0].URL], item(fmt.Sprint("l", i), fmt.Sprint("https://x/l", i), feeds[0].URL, i+1))
	}
	byFeed[feeds[1].URL] = []string{
		item("d0", "https://x/d0", feeds[1].URL, 1),
		item("dup", "https://x/l0", feeds[1].URL, 0), // same URL as a local item → dropped from disruption
	}
	byFeed[feeds[2].URL] = []string{item("w0", "https://x/w0", feeds[2].URL, 2)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		asked := r.URL.Query().Get("feeds")
		if strings.Count(asked, "news.google.com") != 1 {
			t.Errorf("one feed per aggregator call, got %q", asked)
		}
		_, _ = w.Write([]byte(`{"items":[` + strings.Join(byFeed[asked], ",") + `],"_feeds":{"warming":[],"stale":[]}}`))
	}))
	t.Cleanup(srv.Close)
	svc := NewNewsTickerService(NewsTickerDeps{
		Client:  httpx.New(httpx.Config{Timeout: 2 * time.Second, RatePerSecond: 100, Burst: 100}),
		BaseURL: srv.URL,
		Prefs:   prefs,
		Cache:   newSignalCache(newTestStore(t), nil),
		Logger:  slog.New(slog.NewTextHandler(discard{}, nil)),
	})
	return svc, &hits
}

func TestHereNewsSplitsDedupesAndCaps(t *testing.T) {
	svc, hits := hereHarness(t, &memPrefs{})
	got, err := svc.Here(context.Background(), uuid.New(), herePlace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Local) != hereNewsPerList || got.Local[0].ID != "l0" {
		t.Fatalf("local = %d items (want 5, newest first): %+v", len(got.Local), got.Local)
	}
	if len(got.Disruption) != 1 || got.Disruption[0].ID != "d0" {
		t.Errorf("disruption = %+v (duplicate URL must be dropped)", got.Disruption)
	}
	if len(got.WhatsOn) != 1 || got.WhatsOn[0].CountryCode != "PT" {
		t.Errorf("whats_on = %+v", got.WhatsOn)
	}
	if *hits != 3 {
		t.Errorf("one aggregator call per list, got %d", *hits)
	}
	if _, err := svc.Here(context.Background(), uuid.New(), herePlace); err != nil || *hits != 3 {
		t.Errorf("second call for the same place should be cached: hits=%d", *hits)
	}
}

func TestHereNewsRespectsSwitch(t *testing.T) {
	id := uuid.New()
	svc, hits := hereHarness(t, &memPrefs{enabled: map[uuid.UUID]bool{id: false}})
	got, err := svc.Here(context.Background(), id, herePlace)
	if err != nil || len(got.Local)+len(got.Disruption)+len(got.WhatsOn) != 0 || *hits != 0 {
		t.Errorf("news off → empty and no upstream call: %+v hits=%d err=%v", got, *hits, err)
	}
}

func TestHereNewsUpstreamFailureIsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer srv.Close()
	svc := NewNewsTickerService(NewsTickerDeps{
		Client: httpx.New(httpx.Config{Timeout: 2 * time.Second, RatePerSecond: 100, Burst: 100}), BaseURL: srv.URL,
		Prefs: &memPrefs{}, Logger: slog.New(slog.NewTextHandler(discard{}, nil)),
	})
	got, err := svc.Here(context.Background(), uuid.New(), herePlace)
	if err != nil || len(got.Local) != 0 {
		t.Errorf("aggregator down → empty, no error: %+v %v", got, err)
	}
}

// A feed the aggregator has never polled answers "warming" with no items.
// That empty answer must not be cached, and must be flagged, so the next
// request (and the client) picks the headlines up once they arrive.
func TestHereNewsWarmingIsNotCached(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		asked := r.URL.Query().Get("feeds")
		if n <= 3 {
			_, _ = w.Write([]byte(`{"items":[],"_feeds":{"warming":[` + fmt.Sprintf("%q", asked) + `],"stale":[]}}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"items":[{"id":"x","url":"https://x/%d","title":"T","date_published":%q,"_feeds":{"source_name":"S","feed_url":%q}}],"_feeds":{"warming":[],"stale":[]}}`,
			n, time.Now().UTC().Format(time.RFC3339), asked)
	}))
	defer srv.Close()
	svc := NewNewsTickerService(NewsTickerDeps{
		Client: httpx.New(httpx.Config{Timeout: 2 * time.Second, RatePerSecond: 100, Burst: 100}), BaseURL: srv.URL,
		Prefs: &memPrefs{}, Cache: newSignalCache(newTestStore(t), nil), Logger: slog.New(slog.NewTextHandler(discard{}, nil)),
	})
	first, err := svc.Here(context.Background(), uuid.New(), herePlace)
	if err != nil || !first.Stale || len(first.Local) != 0 {
		t.Fatalf("warming → empty and stale, got %+v err=%v", first, err)
	}
	second, err := svc.Here(context.Background(), uuid.New(), herePlace)
	if err != nil || len(second.Local) != 1 || hits != 6 {
		t.Errorf("warming result must not be cached: hits=%d second=%+v", hits, second)
	}
}
