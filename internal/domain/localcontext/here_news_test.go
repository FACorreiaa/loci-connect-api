package localcontext

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

// hereHarness serves items whose feed_url is exactly what hereFeeds produced,
// which is how the aggregator tags them.
func hereHarness(t *testing.T, prefs *memPrefs) (*NewsTickerService, *int) {
	t.Helper()
	feeds := hereFeeds(herePlace)
	hits := 0
	item := func(id, url, feed string, hoursAgo int) string {
		return fmt.Sprintf(`{"id":%q,"url":%q,"title":"T %s","date_published":%q,"_feeds":{"source_name":"S","feed_url":%q}}`,
			id, url, id, time.Now().Add(-time.Duration(hoursAgo)*time.Hour).UTC().Format(time.RFC3339), feed)
	}
	var items []string
	for i := range 7 { // 7 local items → capped at 5
		items = append(items, item(fmt.Sprint("l", i), fmt.Sprint("https://x/l", i), feeds[0].URL, i+1))
	}
	items = append(items,
		item("d0", "https://x/d0", feeds[1].URL, 1),
		item("dup", "https://x/l0", feeds[1].URL, 0), // same URL as a local item → dropped from disruption
		item("w0", "https://x/w0", feeds[2].URL, 2),
		item("stray", "https://x/s", "https://elsewhere/feed", 0), // unknown feed → ignored
	)
	body := `{"items":[` + strings.Join(items, ",") + `],"_feeds":{"warming":[],"stale":[]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if got := strings.Count(r.URL.Query().Get("feeds"), "news.google.com"); got != 3 {
			t.Errorf("one aggregator call with 3 feeds, got %d", got)
		}
		_, _ = w.Write([]byte(body))
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
	if _, err := svc.Here(context.Background(), uuid.New(), herePlace); err != nil || *hits != 1 {
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
