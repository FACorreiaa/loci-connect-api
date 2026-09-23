package localcontext

import (
	"net/url"
	"strings"
	"testing"
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
