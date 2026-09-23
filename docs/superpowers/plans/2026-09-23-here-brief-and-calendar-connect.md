# Here Brief + Calendar Connect Go-Live — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** (A) The home hero on web and iOS describes where the traveller is standing: place name, weather, alerts, local news, travel disruption, what's on. (B) "Connect Google Calendar" and "Connect Calendly" actually work on web and iOS in production.

**Architecture:** (A) One new `LocalContextService.GetHereBrief` RPC resolves the place with the existing BigDataCloud geocoder, then fans out to the existing weather adapter, alert gatherer and feed aggregator (three Google News search feeds). Web and iOS each make one call. (B) All the code is already shipped (server `internal/domain/calendar`, web `ConnectedCalendars.tsx` + `/auth/oauth/[provider]/callback`, iOS `CalendarConnectService`). What's missing is provider configuration and credentials. Part B is console work, sealing and verification.

**Tech Stack:** Go 1.27 + connect-go, protobuf/buf (BSR), SolidStart + solid-query, SwiftUI + connect-swift, sealed-secrets + ArgoCD.

**Spec:** `loci-connect-server/docs/superpowers/specs/2026-09-23-here-brief-design.md` (Part A). Part B has no spec: it only configures code that already exists.

## Global Constraints

- No new paid APIs. Sources: BigDataCloud (keyless), the existing weather adapter, the existing `Gatherer`, the feed aggregator at `FEEDS_BASE_URL`.
- Coordinates are rounded to 2 decimals before any use, never logged and never stored.
- Every part of the brief degrades to empty on its own. `GetHereBrief` errors only on unauthenticated or invalid input.
- News lists respect the existing per-user news switch (`NewsTickerPrefs`).
- Max 5 items per list from the server; the UI shows 3.
- Never render the Lisbon fallback coordinate or a made-up place.
- Proto: generated code comes from `buf generate` only. Tag must contain `GetHereBrief` before the server `go get`s it.
- Stage files by name. Other sessions commit in the same checkouts, so never `git add -A`. A peer session owns `loci-ios` `Features/Search/*`, `POICardView.swift` and `AppleCalendar.swift`; do not touch them.
- Client: never run repo-wide `pnpm format`; format only the touched paths.
- Sealed secrets: name `loci-env`, namespace `horus`, exactly.

## Review Focus

1. **Aggregator echoes a different `feed_url` than we sent** (re-encoded query) → every item is dropped and all three lists come back empty. Pin: the service test asserts that items come back split, using the exact URLs `hereFeeds` produced (Task A3).
2. **Place with a region but no locality** (rural coordinates) → the lists must still be populated from the region. Pin: `TestHereFeedsRegionOnly` (Task A2).
3. **News switched off in Settings** → weather and place still show and the lists are empty; the band must not disappear entirely. Pin: `TestHereNewsRespectsSwitch` (Task A3) plus the web mapping test (Task A6).
4. **Location permission denied on iOS** → the section is hidden and there is no prompt from Discover. Pin: `HereBriefSection` only calls `CurrentLocation.fetch` when `authorizationStatus` is already authorized (Task A8).
5. **Geocoder down** → weather still returns and no RPC error. Pin: `TestGetHereBriefDegradesPerPart` (Task A4).

---

# Part A — Here brief

Working directories:
- proto: `~/Work/production/apps/Loci/loci-connect-proto`
- server: `~/Work/production/apps/Loci/loci-connect-server` (branch `feat/here-brief`, spec already committed)
- web: `~/Work/production/apps/Loci/loci-client`
- iOS: `~/Work/production/apps/Loci/loci-ios`

## File map

| Repo | File | Responsibility |
|---|---|---|
| proto | `proto/loci/localcontext/localcontext.proto` | `GetHereBrief` RPC + `HerePlace`, `HereBrief` messages |
| server | `internal/domain/localcontext/geocode.go` | `Place` type, `PlaceResolver`, `BigDataCloudGeocoder.Place` |
| server | `internal/domain/localcontext/signals.go` | `Gatherer.PlaceResolver()` accessor |
| server | `internal/domain/localcontext/here_news.go` (new) | `hereFeeds`, `splitHereItems`, `NewsTickerService.Here` |
| server | `internal/domain/localcontext/here_brief_handler.go` (new) | `WithPlaces`, `GetHereBrief`, `roundCoord` |
| server | `internal/domain/localcontext/handler.go` | extract `toWeatherDaysProto` |
| server | `internal/domain/localcontext/news_ticker_handler.go` | extract `toNewsItemsProto` |
| server | `cmd/api/dependencies.go` | `.WithPlaces(signals.PlaceResolver())` |
| web | `src/lib/api/hereBrief.ts` (new) | `useHereBrief`, `toHereBrief`, `roundCoord2` |
| web | `src/components/features/Dashboard/HereNowBand.tsx` (new) | the band |
| web | `src/components/features/Dashboard/DeskHero.tsx` | place name in the kicker |
| web | `src/components/features/Dashboard/LoggedInDashboard.tsx` | mount band |
| web | `src/components/LocalWeather.tsx` | export `conditionIcon`, `dayLabel` |
| iOS | `loci/loci/Features/Discover/UI/HereBriefSection.swift` (new) | section + loader |
| iOS | `loci/loci/Features/Discover/UI/DiscoverView.swift` | mount section, place line |

---

### Task A1: Proto contract + release

**Files:**
- Modify: `loci-connect-proto/proto/loci/localcontext/localcontext.proto` (messages after `GetNewsTickerResponse`, rpc in `service LocalContextService`)

**Interfaces:**
- Produces: Go `lcv1.GetHereBriefRequest{Latitude, Longitude}`, `lcv1.HereBrief{Place, Weather, WeatherIsEstimated, Alerts, Local, Disruption, WhatsOn, Stale}`, `lcv1.HerePlace{Locality, Region, CountryCode, CountryName}`; TS `GetHereBriefRequestSchema`, `client.getHereBrief`; Swift `Loci_Localcontext_GetHereBriefRequest`, `getHereBrief(request:headers:)`.

- [ ] **Step 1: Branch**

```bash
cd ~/Work/production/apps/Loci/loci-connect-proto
git fetch origin && git switch -c feat/here-brief origin/main
```

- [ ] **Step 2: Add messages** (after `SetNewsTickerEnabledResponse`)

```proto
message GetHereBriefRequest {
  double latitude = 1 [(buf.validate.field).double = {
    gte: -90
    lte: 90
  }];
  double longitude = 2 [(buf.validate.field).double = {
    gte: -180
    lte: 180
  }];
}

// HerePlace names a coordinate at town level. Any field may be empty: open
// sea has no country, and rural points often have a region but no town.
message HerePlace {
  string locality = 1;
  string region = 2;
  string country_code = 3 [(buf.validate.field).string = {max_len: 2}];
  string country_name = 4;
}

// HereBrief is what the home hero says about where the caller is standing.
// Every part degrades to empty on its own.
message HereBrief {
  HerePlace place = 1;
  // Today and the next two days.
  repeated WeatherDay weather = 2;
  bool weather_is_estimated = 3;
  repeated LocalAlert alerts = 4;
  // "Around you": headlines naming the town or region.
  repeated NewsTickerItem local = 5;
  // "Getting around": strikes, airports, trains, closures, weather warnings.
  repeated NewsTickerItem disruption = 6;
  // "What's on": festivals, events, exhibitions, concerts.
  repeated NewsTickerItem whats_on = 7;
  // True when a feed is serving cache after an upstream failure.
  bool stale = 8;
}
```

- [ ] **Step 3: Add rpc** (after `SetNewsTickerEnabled`)

```proto
  // GetHereBrief describes the place the caller is standing in: its name,
  // weather, alerts and three short headline lists. The RPC does not fail
  // because one source did.
  rpc GetHereBrief(GetHereBriefRequest) returns (HereBrief);
```

- [ ] **Step 4: Lint + generate**

Run: `buf lint && buf generate`
Expected: no lint output. `git status` shows only the `.proto` and the `gen/` files for `localcontext` (go, es, swift). If any generated header names a different `protoc-gen-go` version than `buf.gen.yaml` pins, stop: the tree was generated out of band.

- [ ] **Step 5: Commit, PR, merge**

```bash
git add proto/loci/localcontext/localcontext.proto gen/
git commit -m "Add GetHereBrief to LocalContextService"
git push -u origin feat/here-brief
gh pr create --fill --base main
```

The user merges (auto mode blocks merges).

- [ ] **Step 6: Tag and push to BSR from the merged commit** (a clean tree matters: `buf push` reads the working tree, `git tag` reads HEAD)

```bash
git switch main && git pull --ff-only && git status --short   # must print nothing
git tag --sort=-creatordate | head -1                          # e.g. v5.22.0 → next is v5.23.0
make tag VERSION=v5.23.0 && make push-tag VERSION=v5.23.0
make push
git show v5.23.0:proto/loci/localcontext/localcontext.proto | grep -c GetHereBrief   # expect 2
```

---

### Task A2: Server — place resolver and feed builder (pure parts)

**Files:**
- Modify: `internal/domain/localcontext/geocode.go`
- Modify: `internal/domain/localcontext/signals.go` (next to `CountryResolver()`)
- Create: `internal/domain/localcontext/here_news.go`
- Create test: `internal/domain/localcontext/here_news_test.go`
- Modify test: `internal/domain/localcontext/geocode_test.go`

**Interfaces:**
- Produces: `type Place struct{ Locality, Region, CountryCode, CountryName string }`; `type PlaceResolver interface{ Place(ctx, lat, lon float64) (Place, error) }`; `func (g *Gatherer) PlaceResolver() PlaceResolver`; `type hereFeed struct{ newsFeed; Kind string }`; `func hereFeeds(p Place) []hereFeed`; consts `hereKindLocal`, `hereKindDisruption`, `hereKindWhatsOn`.

- [ ] **Step 1: Bump proto in server**

```bash
cd ~/Work/production/apps/Loci/loci-connect-server
go get github.com/FACorreiaa/loci-connect-proto/v5@v5.23.0 && go mod tidy && go mod vendor
go build ./...
```

- [ ] **Step 2: Failing tests** — `here_news_test.go`

```go
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
```

Add to `geocode_test.go` (follow its existing `httptest` pattern; the fixture below is BigDataCloud's real shape):

```go
func TestBigDataCloudPlace(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"countryCode":"PT","countryName":"Portugal","city":"Viana do Castelo","locality":"Santa Maria Maior","principalSubdivision":"Viana do Castelo District"}`))
	}))
	defer srv.Close()
	g := NewBigDataCloudGeocoder(srv.URL, httpx.New(httpx.Config{Timeout: 2 * time.Second, RatePerSecond: 100, Burst: 100}), newSignalCache(newTestStore(t), nil))

	p, err := g.Place(context.Background(), 41.69, -8.83)
	if err != nil {
		t.Fatal(err)
	}
	want := Place{Locality: "Viana do Castelo", Region: "Viana do Castelo District", CountryCode: "PT", CountryName: "Portugal"}
	if p != want {
		t.Errorf("place = %+v, want %+v", p, want)
	}
	if _, err := g.Place(context.Background(), 41.69, -8.83); err != nil || hits != 1 {
		t.Errorf("second lookup should be cached: hits=%d err=%v", hits, err)
	}
	if _, err := g.Place(context.Background(), 41.79, -8.83); err != nil || hits != 2 {
		t.Errorf("0.1° away is another town key: hits=%d", hits)
	}
}

func TestBigDataCloudPlaceFallsBackToLocality(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"countryCode":"pt","countryName":"Portugal","city":"","locality":"Afife","principalSubdivision":""}`))
	}))
	defer srv.Close()
	g := NewBigDataCloudGeocoder(srv.URL, httpx.New(httpx.Config{Timeout: 2 * time.Second, RatePerSecond: 100, Burst: 100}), nil)
	p, err := g.Place(context.Background(), 41.77, -8.86)
	if err != nil || p.Locality != "Afife" || p.CountryCode != "PT" {
		t.Errorf("place = %+v err=%v", p, err)
	}
}
```

- [ ] **Step 3: Run, expect FAIL**

Run: `go test ./internal/domain/localcontext -run 'HereFeeds|BigDataCloudPlace' -count=1`
Expected: compile error, `undefined: hereFeeds` / `g.Place undefined`.

- [ ] **Step 4: Implement `geocode.go`**

Replace `bigDataCloudResponse` and `CountryCode` body with a shared lookup:

```go
type bigDataCloudResponse struct {
	CountryCode          string `json:"countryCode"`
	CountryName          string `json:"countryName"`
	City                 string `json:"city"`
	Locality             string `json:"locality"`
	PrincipalSubdivision string `json:"principalSubdivision"`
}

// sourcePlace namespaces town-level cache entries apart from the country ones:
// same upstream, different key precision.
const sourcePlace = SourceGeocode + "-place"

// Place is a coordinate named at town level. Any field may be empty.
type Place struct {
	Locality    string
	Region      string
	CountryCode string
	CountryName string
}

// PlaceResolver names the town a coordinate sits in.
type PlaceResolver interface {
	Place(ctx context.Context, lat, lon float64) (Place, error)
}

func (g *BigDataCloudGeocoder) lookup(ctx context.Context, lat, lon float64) (bigDataCloudResponse, error) {
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%f", lat))
	q.Set("longitude", fmt.Sprintf("%f", lon))
	q.Set("localityLanguage", "en")
	endpoint := g.baseURL + "/data/reverse-geocode-client?" + q.Encode()
	return httpx.GetJSON[bigDataCloudResponse](ctx, g.client, SourceGeocode, endpoint)
}

func (g *BigDataCloudGeocoder) CountryCode(ctx context.Context, lat, lon float64) (string, error) {
	key := fmt.Sprintf("%.1f,%.1f", lat, lon)

	if code, ok := cacheGet[string](g.cache, SourceGeocode, key); ok {
		return code, nil
	}

	body, err := g.lookup(ctx, lat, lon)
	if err != nil {
		return "", err
	}

	code := strings.ToUpper(strings.TrimSpace(body.CountryCode))
	if code == "" {
		// Open ocean and Antarctica genuinely have no country. That is a valid
		// answer, not an error — the caller simply skips country-scoped sources.
		return "", nil
	}

	cacheSet(g.cache, SourceGeocode, key, code, ttlGeocode)
	return code, nil
}

// Place names the town. Keyed at 0.01° (~1 km), not the 0.1° CountryCode
// uses: at ~11 km the town is often the neighbouring one.
func (g *BigDataCloudGeocoder) Place(ctx context.Context, lat, lon float64) (Place, error) {
	key := fmt.Sprintf("%.2f,%.2f", lat, lon)
	if p, ok := cacheGet[Place](g.cache, sourcePlace, key); ok {
		return p, nil
	}
	body, err := g.lookup(ctx, lat, lon)
	if err != nil {
		return Place{}, err
	}
	locality := strings.TrimSpace(body.City)
	if locality == "" {
		locality = strings.TrimSpace(body.Locality)
	}
	p := Place{
		Locality:    locality,
		Region:      strings.TrimSpace(body.PrincipalSubdivision),
		CountryCode: strings.ToUpper(strings.TrimSpace(body.CountryCode)),
		CountryName: strings.TrimSpace(body.CountryName),
	}
	cacheSet(g.cache, sourcePlace, key, p, ttlGeocode)
	return p, nil
}
```

In `signals.go`, after `CountryResolver()`:

```go
// PlaceResolver exposes the shared geocoder for town-level naming, when the
// configured resolver supports it. Nil-safe like CountryResolver.
func (g *Gatherer) PlaceResolver() PlaceResolver {
	if g == nil {
		return nil
	}
	pr, _ := g.country.(PlaceResolver)
	return pr
}
```

- [ ] **Step 5: Implement the feed builder in `here_news.go`**

```go
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
```

Note: `TestHereFeedsBothNames` asserts the *local* query equals the bare subject, and the others contain their keyword groups. That matches this code.

- [ ] **Step 6: Run, expect PASS**

Run: `go test ./internal/domain/localcontext -run 'HereFeeds|BigDataCloud' -count=1`
Expected: `ok`. Also run the whole package once: `go test ./internal/domain/localcontext -count=1` (the `CountryCode` refactor must not break existing geocode tests).

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/domain/localcontext/geocode.go internal/domain/localcontext/geocode_test.go internal/domain/localcontext/signals.go internal/domain/localcontext/here_news.go internal/domain/localcontext/here_news_test.go
git commit -m "Name the town a coordinate sits in and build here-brief feeds"
```

(`vendor/` is gitignored. Do not stage it.)

---

### Task A3: Server — here news service (fetch, split, dedupe, switch, cache)

**Files:**
- Modify: `internal/domain/localcontext/here_news.go`
- Modify test: `internal/domain/localcontext/here_news_test.go`

**Interfaces:**
- Consumes: `hereFeeds`, `Place`, `NewsTickerService.fetch`, `jsonFeedDoc`, `NewsItem`, `newsTickerHarness`/`memPrefs` from `news_ticker_test.go`.
- Produces: `type HereNews struct{ Stale bool; Local, Disruption, WhatsOn []NewsItem }`; `func (s *NewsTickerService) Here(ctx context.Context, userID uuid.UUID, p Place) (HereNews, error)`; const `hereNewsPerList = 5`.

- [ ] **Step 1: Failing tests** (append to `here_news_test.go`; add imports `context`, `fmt`, `log/slog`, `net/http`, `net/http/httptest`, `time`, `github.com/google/uuid`, `github.com/FACorreiaa/loci-connect-api/pkg/httpx`)

```go
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
		t.Errorf("local = %d items, first %q (want 5, newest first)", len(got.Local), got.Local[0].ID)
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
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/domain/localcontext -run HereNews -count=1`
Expected: `undefined: hereNewsPerList` / `svc.Here undefined`.

- [ ] **Step 3: Implement** (append to `here_news.go`; add imports `context`, `log/slog`, `sort`, `time`, `github.com/google/uuid`)

```go
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
```

Note the dedupe order. The `dup` item (published 0h ago) carries a disruption feed URL but a local item's URL. The local pass runs first and claims `https://x/l0`, so `dup` is skipped in the disruption pass. That is what the test asserts.

- [ ] **Step 4: Run, expect PASS**

Run: `go test ./internal/domain/localcontext -run 'HereNews|NewsTicker' -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/localcontext/here_news.go internal/domain/localcontext/here_news_test.go
git commit -m "Fetch here-brief headlines in one aggregator call"
```

---

### Task A4: Server — `GetHereBrief` handler + wiring

**Files:**
- Create: `internal/domain/localcontext/here_brief_handler.go`
- Create test: `internal/domain/localcontext/here_brief_handler_test.go`
- Modify: `internal/domain/localcontext/handler.go` (add `places PlaceResolver` field; extract `toWeatherDaysProto`)
- Modify: `internal/domain/localcontext/news_ticker_handler.go` (extract `toNewsItemsProto`)
- Modify: `cmd/api/dependencies.go:893-897`

**Interfaces:**
- Consumes: `PlaceResolver`, `NewsTickerService.Here`, `HereNews`, `ToLocalAlertsProto`, `newsUserID`, `WeatherAdapter`.
- Produces: `func (h *Handler) WithPlaces(p PlaceResolver) *Handler`; `func roundCoord(v float64) float64`; `func toWeatherDaysProto([]WeatherDay) []*lcv1.WeatherDay`; `func toNewsItemsProto([]NewsItem) []*lcv1.NewsTickerItem`.

- [ ] **Step 1: Failing tests** — `here_brief_handler_test.go`

```go
package localcontext

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type fakePlaces struct {
	p       Place
	err     error
	gotLat  float64
	gotLon  float64
}

func (f *fakePlaces) Place(_ context.Context, lat, lon float64) (Place, error) {
	f.gotLat, f.gotLon = lat, lon
	return f.p, f.err
}

type countingWeather struct {
	days []WeatherDay
	err  error
	n    int
}

func (f *countingWeather) Forecast(_ context.Context, _, _ float64, days int) ([]WeatherDay, error) {
	f.n = days
	return f.days, f.err
}

func authed() context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, uuid.New().String())
}

func TestRoundCoord(t *testing.T) {
	for in, want := range map[float64]float64{41.69412: 41.69, -8.83499: -8.83, -8.8351: -8.84, 0: 0} {
		if got := roundCoord(in); got != want {
			t.Errorf("roundCoord(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestGetHereBriefRequiresAuth(t *testing.T) {
	h := NewHandler(&countingWeather{}, false, slog.New(slog.NewTextHandler(discard{}, nil)))
	_, err := h.GetHereBrief(context.Background(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.69, Longitude: -8.83}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("want unauthenticated, got %v", err)
	}
}

func TestGetHereBriefHappyPath(t *testing.T) {
	svc, _ := hereHarness(t, &memPrefs{})
	places := &fakePlaces{p: herePlace}
	w := &countingWeather{days: []WeatherDay{{Date: time.Now(), HighC: 22, LowC: 14, Condition: "Clear"}}}
	h := NewHandler(w, false, slog.New(slog.NewTextHandler(discard{}, nil))).WithPlaces(places).WithNewsTicker(svc)

	resp, err := h.GetHereBrief(authed(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.694123, Longitude: -8.832987}))
	if err != nil {
		t.Fatal(err)
	}
	m := resp.Msg
	if places.gotLat != 41.69 || places.gotLon != -8.83 {
		t.Errorf("geocoder saw unrounded coords %v,%v", places.gotLat, places.gotLon)
	}
	if w.n != 3 || len(m.GetWeather()) != 1 {
		t.Errorf("weather: asked %d days, got %d", w.n, len(m.GetWeather()))
	}
	if m.GetPlace().GetLocality() != "Viana do Castelo" || m.GetPlace().GetCountryCode() != "PT" {
		t.Errorf("place = %+v", m.GetPlace())
	}
	if len(m.GetLocal()) != 5 || len(m.GetDisruption()) != 1 || len(m.GetWhatsOn()) != 1 {
		t.Errorf("lists = %d/%d/%d", len(m.GetLocal()), len(m.GetDisruption()), len(m.GetWhatsOn()))
	}
}

func TestGetHereBriefDegradesPerPart(t *testing.T) {
	// Geocoder down, weather down, no news service, no signals: still 200.
	h := NewHandler(&countingWeather{err: errors.New("boom")}, true, slog.New(slog.NewTextHandler(discard{}, nil))).
		WithPlaces(&fakePlaces{err: errors.New("geo down")})
	resp, err := h.GetHereBrief(authed(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.69, Longitude: -8.83}))
	if err != nil {
		t.Fatalf("a failing part must not fail the RPC: %v", err)
	}
	if len(resp.Msg.GetWeather()) != 0 || resp.Msg.GetPlace().GetLocality() != "" || !resp.Msg.GetWeatherIsEstimated() {
		t.Errorf("resp = %+v", resp.Msg)
	}

	// Geocoder down but weather up: weather still returned.
	h2 := NewHandler(&countingWeather{days: []WeatherDay{{Date: time.Now(), Condition: "Rain"}}}, false, slog.New(slog.NewTextHandler(discard{}, nil))).
		WithPlaces(&fakePlaces{err: errors.New("geo down")})
	resp2, err := h2.GetHereBrief(authed(), connect.NewRequest(&lcv1.GetHereBriefRequest{Latitude: 41.69, Longitude: -8.83}))
	if err != nil || len(resp2.Msg.GetWeather()) != 1 {
		t.Errorf("weather must survive a geocoder failure: %+v %v", resp2.Msg, err)
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/domain/localcontext -run 'HereBrief|RoundCoord' -count=1`
Expected: `h.GetHereBrief undefined`.

- [ ] **Step 3: Extract helpers**

In `handler.go`, add the field to `Handler` (after `news`):

```go
	// Optional, attached via WithPlaces. Nil leaves the here brief unnamed.
	places PlaceResolver
```

Add and use the helper (replace the loop in `GetLocalContext`):

```go
func toWeatherDaysProto(fc []WeatherDay) []*lcv1.WeatherDay {
	out := make([]*lcv1.WeatherDay, 0, len(fc))
	for _, d := range fc {
		out = append(out, &lcv1.WeatherDay{
			Date:       timestamppb.New(d.Date),
			HighC:      d.HighC,
			LowC:       d.LowC,
			Condition:  d.Condition,
			PrecipProb: d.PrecipProb,
		})
	}
	return out
}
```

In `GetLocalContext`: `out.Weather = toWeatherDaysProto(fc)`.

In `news_ticker_handler.go`:

```go
func toNewsItemsProto(items []NewsItem) []*lcv1.NewsTickerItem {
	out := make([]*lcv1.NewsTickerItem, 0, len(items))
	for _, it := range items {
		out = append(out, &lcv1.NewsTickerItem{
			Id: it.ID, Title: it.Title, Url: it.URL, Source: it.Source,
			PublishedAt: timestamppb.New(it.PublishedAt), CountryCode: it.CountryCode,
		})
	}
	return out
}
```

In `GetNewsTicker`, replace the loop with `out.Items = toNewsItemsProto(ticker.Items)`.

- [ ] **Step 4: Implement `here_brief_handler.go`**

```go
package localcontext

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
)

// hereWeatherDays is today plus the next two.
const hereWeatherDays = 3

// WithPlaces attaches the town-level geocoder for GetHereBrief. Optional:
// without it the brief has no place name and no news (the news queries need
// a town or region).
func (h *Handler) WithPlaces(p PlaceResolver) *Handler {
	h.places = p
	return h
}

// roundCoord keeps ~1 km of precision. Everything downstream — geocoder,
// weather, alerts, cache keys — sees only the rounded value, and none of it
// is logged.
func roundCoord(v float64) float64 {
	return math.Round(v*100) / 100
}

func (h *Handler) GetHereBrief(
	ctx context.Context,
	req *connect.Request[lcv1.GetHereBriefRequest],
) (*connect.Response[lcv1.HereBrief], error) {
	userID, err := newsUserID(ctx)
	if err != nil {
		return nil, err
	}
	lat, lon := roundCoord(req.Msg.GetLatitude()), roundCoord(req.Msg.GetLongitude())

	var place Place
	if h.places != nil {
		if p, err := h.places.Place(ctx, lat, lon); err != nil {
			h.logger.WarnContext(ctx, "here brief: place lookup failed", slog.Any("error", err))
		} else {
			place = p
		}
	}

	var (
		wg     sync.WaitGroup
		fc     []WeatherDay
		alerts []Alert
		news   HereNews
	)
	wg.Go(func() {
		days, err := h.weather.Forecast(ctx, lat, lon, hereWeatherDays)
		if err != nil {
			h.logger.WarnContext(ctx, "here brief: weather failed", slog.Any("error", err))
			return
		}
		fc = days
	})
	if h.signals.Enabled() {
		wg.Go(func() {
			start := time.Now().UTC()
			alerts = h.signals.Gather(ctx, lat, lon, start, start.AddDate(0, 0, hereWeatherDays))
		})
	}
	if h.news != nil {
		wg.Go(func() {
			n, err := h.news.Here(ctx, userID, place)
			if err != nil {
				h.logger.WarnContext(ctx, "here brief: news failed", slog.Any("error", err))
				return
			}
			news = n
		})
	}
	wg.Wait()

	return connect.NewResponse(&lcv1.HereBrief{
		Place: &lcv1.HerePlace{
			Locality: place.Locality, Region: place.Region,
			CountryCode: place.CountryCode, CountryName: place.CountryName,
		},
		Weather:            toWeatherDaysProto(fc),
		WeatherIsEstimated: h.estimated,
		Alerts:             ToLocalAlertsProto(alerts),
		Local:              toNewsItemsProto(news.Local),
		Disruption:         toNewsItemsProto(news.Disruption),
		WhatsOn:            toNewsItemsProto(news.WhatsOn),
		Stale:              news.Stale,
	}), nil
}
```

- [ ] **Step 5: Wire it** in `cmd/api/dependencies.go`, in the `d.LocalContextHandler = ...` chain:

```go
		WithNewsTicker(newsTicker).
		WithPlaces(signals.PlaceResolver())
```

(`signals` is nil when `SIGNALS_ENABLED=false`. `PlaceResolver()` is nil-safe and returns an untyped nil there, which the handler treats as "no place".)

- [ ] **Step 6: Run everything**

Run: `go test ./internal/domain/localcontext/... ./cmd/... -count=1 && go vet ./... && golangci-lint run`
Expected: `ok`, `0 issues.` (If golangci-lint hangs, check it is v2.13.2, not v2.13.0.)

- [ ] **Step 7: Commit, PR**

```bash
git add internal/domain/localcontext/here_brief_handler.go internal/domain/localcontext/here_brief_handler_test.go internal/domain/localcontext/handler.go internal/domain/localcontext/news_ticker_handler.go cmd/api/dependencies.go
git commit -m "Add GetHereBrief: place, weather, alerts and three headline lists"
git push -u origin feat/here-brief
gh pr create --fill --base main
```

---

### Task A5: Server deploy + prod proof

- [ ] **Step 1:** The user merges the server PR. Wait for CD to build the image.
- [ ] **Step 2: Promote.** The CD promote step is a no-op that reports success, so open the promote PR by hand in `~/Work/production/platform/infra`: bump the `loci-api` image tag in `apps/loci/api` to the new commit's tag. First verify that the tag's image contains the commit. The user merges it.
- [ ] **Step 3: Unauthenticated probe** (proves the RPC exists, because protovalidate runs before auth, then auth rejects):

```bash
curl -s -X POST https://api.lociai.fyi/loci.localcontext.LocalContextService/GetHereBrief \
  -H 'content-type: application/json' -d '{"latitude":41.69,"longitude":-8.83}'
```

Expected: `"code":"unauthenticated"`. `unimplemented` means the old image is still serving.

- [ ] **Step 4: Signed-in call.** The user runs the same call with their bearer token, or checks it through the web band after Task A7. Expected: `place.locality` is non-empty and at least one of `local`/`disruption`/`whatsOn` is non-empty.

---

### Task A6: Web — data hook

**Files:**
- Create: `loci-client/src/lib/api/hereBrief.ts`
- Create test: `loci-client/src/lib/api/hereBrief.test.ts`
- Modify: `loci-client/package.json` (proto bump)

**Interfaces:**
- Consumes: `client.getHereBrief` (TS from BSR), `WeatherDay`, `LocalAlert` from `~/lib/api/localContext`, `NewsTickerItem` from `~/lib/news/ticker`.
- Produces: `interface HereBriefData { place: { locality: string; region: string; countryCode: string; countryName: string }; weather: WeatherDay[]; estimated: boolean; alerts: LocalAlert[]; local: NewsTickerItem[]; disruption: NewsTickerItem[]; whatsOn: NewsTickerItem[]; stale: boolean }`; `toHereBrief(res: HereBrief): HereBriefData`; `roundCoord2(v: number): number`; `hasAnything(d: HereBriefData): boolean`; `placeLabel(d: HereBriefData | undefined): string`; `useHereBrief(lat: () => number | undefined, lon: () => number | undefined)`.

- [ ] **Step 1: Branch + bump proto**

```bash
cd ~/Work/production/apps/Loci/loci-client
git fetch origin && git switch -c feat/here-brief origin/main
pnpm buf-update   # moves @buf/loci_loci-proto.bufbuild_es to the latest BSR commit
grep -rn "GetHereBriefRequestSchema" node_modules/@buf/loci_loci-proto.bufbuild_es/loci/localcontext/localcontext_pb.d.ts | head -1
```

Expected: one match. If not, the BSR push in Task A1 did not happen.

- [ ] **Step 2: Failing test** — `hereBrief.test.ts`

```ts
import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  HereBriefSchema,
  HerePlaceSchema,
  NewsTickerItemSchema,
  WeatherDaySchema,
} from "@buf/loci_loci-proto.bufbuild_es/loci/localcontext/localcontext_pb.js";
import { hasAnything, placeLabel, roundCoord2, toHereBrief } from "./hereBrief";

const item = (id: string) =>
  create(NewsTickerItemSchema, {
    id,
    title: `T ${id}`,
    url: `https://x/${id}`,
    source: "S",
    publishedAt: timestampFromDate(new Date("2026-09-23T10:00:00Z")),
    countryCode: "PT",
  });

describe("hereBrief", () => {
  it("rounds to 2 decimals for the query key", () => {
    expect(roundCoord2(41.694123)).toBe(41.69);
    expect(roundCoord2(-8.8351)).toBe(-8.84);
  });

  it("maps every part", () => {
    const d = toHereBrief(
      create(HereBriefSchema, {
        place: create(HerePlaceSchema, { locality: "Viana do Castelo", countryCode: "PT" }),
        weather: [create(WeatherDaySchema, { highC: 22, lowC: 14, condition: "Clear" })],
        local: [item("a")],
        disruption: [item("b")],
        whatsOn: [item("c")],
        stale: true,
      }),
    );
    expect(d.place.locality).toBe("Viana do Castelo");
    expect(d.weather[0].highC).toBe(22);
    expect([d.local[0].id, d.disruption[0].id, d.whatsOn[0].id]).toEqual(["a", "b", "c"]);
    expect(d.local[0].publishedAt).toBe("2026-09-23T10:00:00.000Z");
    expect(d.stale).toBe(true);
    expect(hasAnything(d)).toBe(true);
    expect(placeLabel(d)).toBe("Viana do Castelo");
  });

  it("news switched off still shows weather", () => {
    const d = toHereBrief(
      create(HereBriefSchema, { weather: [create(WeatherDaySchema, { condition: "Rain" })] }),
    );
    expect(hasAnything(d)).toBe(true);
    expect(d.local).toEqual([]);
  });

  it("empty response is nothing, and place falls back to region", () => {
    expect(hasAnything(toHereBrief(create(HereBriefSchema, {})))).toBe(false);
    expect(
      placeLabel(toHereBrief(create(HereBriefSchema, { place: create(HerePlaceSchema, { region: "Minho" }) }))),
    ).toBe("Minho");
    expect(placeLabel(undefined)).toBe("");
  });
});
```

- [ ] **Step 3: Run, expect FAIL**

Run: `pnpm vitest run src/lib/api/hereBrief.test.ts`
Expected: `Failed to resolve import "./hereBrief"`.

- [ ] **Step 4: Implement** — `hereBrief.ts`

```ts
// Where the traveller is standing right now: place, weather, alerts and three
// short headline lists, from one GetHereBrief call. Only ever asked with a
// real position — never the hero's Lisbon stand-in.
import { createClient } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import {
  LocalContextService,
  GetHereBriefRequestSchema,
  type HereBrief,
  type NewsTickerItem as NewsTickerItemMsg,
} from "@buf/loci_loci-proto.bufbuild_es/loci/localcontext/localcontext_pb.js";
import { transport } from "../connect-transport";
import { useAppQuery } from "./authed-query";
import type { LocalAlert, WeatherDay } from "./localContext";
import type { NewsTickerItem } from "../news/ticker";

const client = createClient(LocalContextService, transport);

export interface HereBriefData {
  place: { locality: string; region: string; countryCode: string; countryName: string };
  weather: WeatherDay[];
  estimated: boolean;
  alerts: LocalAlert[];
  local: NewsTickerItem[];
  disruption: NewsTickerItem[];
  whatsOn: NewsTickerItem[];
  stale: boolean;
}

/** ~1 km. Walking across town should not refetch. */
export const roundCoord2 = (v: number): number => Math.round(v * 100) / 100;

const toItem = (it: NewsTickerItemMsg): NewsTickerItem => ({
  id: it.id,
  title: it.title,
  url: it.url,
  source: it.source,
  publishedAt: it.publishedAt ? timestampDate(it.publishedAt).toISOString() : "",
  countryCode: it.countryCode,
});

export const toHereBrief = (res: HereBrief): HereBriefData => ({
  place: {
    locality: res.place?.locality ?? "",
    region: res.place?.region ?? "",
    countryCode: res.place?.countryCode ?? "",
    countryName: res.place?.countryName ?? "",
  },
  weather: res.weather.map((w) => ({
    date: w.date ? timestampDate(w.date).toISOString() : "",
    highC: w.highC,
    lowC: w.lowC,
    condition: w.condition,
    precipProb: w.precipProb,
  })),
  estimated: res.weatherIsEstimated,
  alerts: res.alerts.map((a) => ({
    kind: a.kind,
    title: a.title,
    detail: a.detail,
    date: a.date ? timestampDate(a.date).toISOString() : undefined,
    severity: a.severity,
    source: a.source,
    lat: a.latitude,
    lon: a.longitude,
  })),
  local: res.local.map(toItem),
  disruption: res.disruption.map(toItem),
  whatsOn: res.whatsOn.map(toItem),
  stale: res.stale,
});

export const hasAnything = (d: HereBriefData): boolean =>
  d.weather.length + d.alerts.length + d.local.length + d.disruption.length + d.whatsOn.length > 0;

export const placeLabel = (d: HereBriefData | undefined): string =>
  d?.place.locality || d?.place.region || "";

export const useHereBrief = (lat: () => number | undefined, lon: () => number | undefined) =>
  useAppQuery(() => {
    const la = lat();
    const lo = lon();
    const rla = la == null ? undefined : roundCoord2(la);
    const rlo = lo == null ? undefined : roundCoord2(lo);
    return {
      queryKey: ["hereBrief", rla ?? null, rlo ?? null],
      enabled: rla != null && rlo != null,
      staleTime: 15 * 60 * 1000,
      queryFn: async (): Promise<HereBriefData> =>
        toHereBrief(
          await client.getHereBrief(create(GetHereBriefRequestSchema, { latitude: rla!, longitude: rlo! })),
        ),
    };
  });
```

- [ ] **Step 5: Run, expect PASS**

Run: `pnpm vitest run src/lib/api/hereBrief.test.ts && pnpm typecheck`
Expected: 4 passed; no type errors.

- [ ] **Step 6: Commit**

```bash
git add package.json pnpm-lock.yaml src/lib/api/hereBrief.ts src/lib/api/hereBrief.test.ts
git commit -m "Add useHereBrief for the place the traveller is standing in"
```

---

### Task A7: Web — Here now band + hero kicker

**Files:**
- Create: `loci-client/src/components/features/Dashboard/HereNowBand.tsx`
- Modify: `loci-client/src/components/LocalWeather.tsx` (export `conditionIcon`, `dayLabel`)
- Modify: `loci-client/src/components/features/Dashboard/DeskHero.tsx` (kicker)
- Modify: `loci-client/src/components/features/Dashboard/LoggedInDashboard.tsx`

**Interfaces:**
- Consumes: `useHereBrief`, `hasAnything`, `placeLabel`, `HereBriefData`; `useUserLocation` from `~/contexts/LocationContext`; `timeAgo` from `~/lib/news/ticker`; `colorForSeverity` from `~/lib/theme-colors`; `formatCoord`.

- [ ] **Step 1: Export helpers** in `LocalWeather.tsx`: change `const conditionIcon =` → `export const conditionIcon =`, and `const dayLabel =` → `export const dayLabel =`.

- [ ] **Step 2: Create `HereNowBand.tsx`**

```tsx
// Where the traveller is standing: today's weather, alerts, and three short
// headline lists. Only for a real position; absent when there is nothing to say.
import { For, Show, createMemo } from "solid-js";
import { TriangleAlert } from "lucide-solid";
import { useUserLocation } from "~/contexts/LocationContext";
import { hasAnything, placeLabel, useHereBrief, type HereBriefData } from "~/lib/api/hereBrief";
import { conditionIcon, dayLabel } from "~/components/LocalWeather";
import { colorForSeverity } from "~/lib/theme-colors";
import { timeAgo, type NewsTickerItem } from "~/lib/news/ticker";

const VISIBLE = 3;

function HeadlineList(props: { title: string; items: NewsTickerItem[] }) {
  const now = () => new Date();
  return (
    <Show when={props.items.length > 0}>
      <div class="min-w-0">
        <p class="kicker mb-2">{props.title}</p>
        <ul class="space-y-2">
          <For each={props.items.slice(0, VISIBLE)}>
            {(item) => (
              <li>
                <a href={item.url} target="_blank" rel="noopener" class="group block">
                  <span class="line-clamp-2 text-sm text-foreground group-hover:underline">{item.title}</span>
                  <span class="text-[11px] text-muted-foreground">
                    {item.source} · {timeAgo(item.publishedAt, now())}
                  </span>
                </a>
              </li>
            )}
          </For>
        </ul>
      </div>
    </Show>
  );
}

function Today(props: { data: HereBriefData }) {
  const today = () => props.data.weather[0];
  return (
    <div class="min-w-0">
      <Show when={today()}>
        {(t) => {
          const Icon = conditionIcon(t().condition);
          return (
            <div class="mb-3 flex items-center gap-3">
              <Icon class="h-8 w-8 text-primary" aria-hidden="true" />
              <div>
                <p class="text-2xl tabular-nums">
                  {Math.round(t().highC)}° <span class="text-base text-muted-foreground">/ {Math.round(t().lowC)}°</span>
                </p>
                <p class="text-xs text-muted-foreground">
                  {t().condition}
                  <Show when={props.data.estimated}> · estimated</Show>
                </p>
              </div>
            </div>
          );
        }}
      </Show>
      <Show when={props.data.weather.length > 1}>
        <div class="mb-3 flex gap-3 text-xs text-muted-foreground">
          <For each={props.data.weather.slice(1)}>
            {(w) => (
              <span class="tabular-nums">
                {dayLabel(w.date)} {Math.round(w.highC)}°/{Math.round(w.lowC)}°
              </span>
            )}
          </For>
        </div>
      </Show>
      <Show when={props.data.alerts.length > 0}>
        <ul class="space-y-1">
          <For each={props.data.alerts.slice(0, 3)}>
            {(a) => (
              <li class="flex items-start gap-1.5 text-xs">
                <TriangleAlert
                  class="mt-0.5 h-3.5 w-3.5 shrink-0"
                  style={{ color: colorForSeverity(a.severity) }}
                  aria-hidden="true"
                />
                <span class="text-foreground">{a.title}</span>
              </li>
            )}
          </For>
        </ul>
      </Show>
    </div>
  );
}

export default function HereNowBand() {
  const { userLocation } = useUserLocation();
  const query = useHereBrief(
    () => userLocation()?.latitude,
    () => userLocation()?.longitude,
  );
  const data = createMemo(() => (query.data && hasAnything(query.data) ? query.data : undefined));

  return (
    <Show when={data()}>
      {(d) => (
        <section class="loci-card mb-8 rounded-2xl p-5 sm:p-6" aria-label="Here now">
          <div class="mb-4 flex items-baseline justify-between gap-4">
            <p class="kicker">Here now{placeLabel(d()) ? ` · ${placeLabel(d())}` : ""}</p>
            <Show when={d().stale}>
              <span class="text-[11px] text-muted-foreground">some sources delayed</span>
            </Show>
          </div>
          <div class="grid gap-6 md:grid-cols-[minmax(0,14rem)_repeat(3,minmax(0,1fr))]">
            <Today data={d()} />
            <HeadlineList title="Around you" items={d().local} />
            <HeadlineList title="Getting around" items={d().disruption} />
            <HeadlineList title="What's on" items={d().whatsOn} />
          </div>
        </section>
      )}
    </Show>
  );
}
```

- [ ] **Step 3: Kicker in `DeskHero.tsx`.** Import `useHereBrief` and `placeLabel`. solid-query dedupes the call because the band uses the same key. Replace the `(loc) => ...` child of the location `<Show>`:

```tsx
              {(loc) => (
                <p class="font-coord text-[10px] uppercase tracking-[0.2em] text-primary-foreground/65">
                  <Show when={placeLabel(here.data)}>{(name) => <>{name()} · </>}</Show>
                  {formatCoord(loc().latitude, loc().longitude)}
                </p>
              )}
```

Near the other hooks, after `const { userLocation } = useUserLocation();`:

```tsx
  // Same query key as HereNowBand, so this costs no second request.
  const here = useHereBrief(
    () => userLocation()?.latitude,
    () => userLocation()?.longitude,
  );
```

- [ ] **Step 4: Mount** in `LoggedInDashboard.tsx`: `import HereNowBand from "./HereNowBand";` and place `<HereNowBand />` between `<DeskHero />` and `<InSeasonBand />`.

- [ ] **Step 5: Checks**

```bash
pnpm typecheck && pnpm lint --deny-warnings && pnpm test
pnpm exec oxfmt src/lib/api/hereBrief.ts src/lib/api/hereBrief.test.ts src/components/features/Dashboard/HereNowBand.tsx src/components/features/Dashboard/DeskHero.tsx src/components/features/Dashboard/LoggedInDashboard.tsx src/components/LocalWeather.tsx
```

Expected: all green. Formatting touches only these files.

- [ ] **Step 6: Visual check.** Run `pnpm dev` against the prod API (after Task A5) and sign in. Check at 390px and at desktop width, in light and dark:
  - the band shows under the hero
  - the kicker reads `<town> · 41.69° N 8.83° W`
  - with browser location blocked, the band is absent and the kicker falls back to "Plan"

Use the Chrome extension if it is connected; otherwise, list this as owed.

- [ ] **Step 7: Commit, PR**

```bash
git add src/components/features/Dashboard/HereNowBand.tsx src/components/features/Dashboard/DeskHero.tsx src/components/features/Dashboard/LoggedInDashboard.tsx src/components/LocalWeather.tsx
git commit -m "Show where the traveller is standing under the hero"
git push -u origin feat/here-brief && gh pr create --fill --base main
```

After the user merges: grep the live bundle for `api.lociai.fyi` (not `localhost:8000`). If the Workers Builds deploy won the race, re-dispatch the Actions deploy.

---

### Task A8: iOS — Here brief section

**Files:**
- Create: `loci-ios/loci/loci/Features/Discover/UI/HereBriefSection.swift`
- Modify: `loci-ios/loci/loci/Features/Discover/UI/DiscoverView.swift` (hero + mount)

**Interfaces:**
- Consumes: `SettingsClients.localContext` (`Loci_Localcontext_LocalContextServiceClient`), `CurrentLocation.fetch()`, `Loci_Localcontext_HereBrief`, theme tokens `Color.lociInk`, `.lociForest`, `.lociCard`, `.lociBorder`, fonts `.lociHeadline()`, `.lociCaption(_:)`, `.lociBody()`, `LociTheme.cornerRadius`.
- Produces: `@Observable final class HereBriefModel { var brief: Loci_Localcontext_HereBrief?; var placeName: String; func load() async }`; `struct HereBriefSection: View { let model: HereBriefModel }`.

iOS takes the proto from the sibling checkout (`relativePath = "../../loci-connect-proto"`). After Task A1, make sure that checkout is on `main` at the tagged commit so `gen/swift` has `GetHereBrief`.

- [ ] **Step 1: Branch** (base on `origin/main`; do not stack on `feat/ios-nearby-walk` or the peer's `feat/ios-results-parity`)

```bash
cd ~/Work/production/apps/Loci/loci-ios
git fetch origin && git switch -c feat/ios-here-brief origin/main
grep -c "GetHereBrief" ../loci-connect-proto/gen/swift/loci/localcontext/localcontext.connect.swift   # expect ≥1
```

- [ ] **Step 2: Create `HereBriefSection.swift`**

```swift
import CoreLocation
import LociConnectProto
import SwiftUI

/// Where the traveller is standing (web: HereNowBand). Loads only when location
/// is already authorised — Discover never raises the permission prompt; Nearby does.
@MainActor @Observable
final class HereBriefModel {
  var brief: Loci_Localcontext_HereBrief?

  var placeName: String {
    guard let place = brief?.place else { return "" }
    return place.locality.isEmpty ? place.region : place.locality
  }

  var hasAnything: Bool {
    guard let b = brief else { return false }
    return !(b.weather.isEmpty && b.alerts.isEmpty && b.local.isEmpty && b.disruption.isEmpty && b.whatsOn.isEmpty)
  }

  func load() async {
    let status = CLLocationManager().authorizationStatus
    guard status == .authorizedWhenInUse || status == .authorizedAlways else { return }
    guard let coord = try? await CurrentLocation.fetch(timeout: .seconds(8)) else { return }
    var req = Loci_Localcontext_GetHereBriefRequest()
    req.latitude = (coord.latitude * 100).rounded() / 100
    req.longitude = (coord.longitude * 100).rounded() / 100
    let res = await SettingsClients.localContext.getHereBrief(request: req, headers: [:])
    if let message = res.message { brief = message }
  }
}

struct HereBriefSection: View {
  let model: HereBriefModel
  @Environment(\.openURL) private var openURL

  var body: some View {
    if model.hasAnything, let brief = model.brief {
      VStack(alignment: .leading, spacing: 16) {
        Text(model.placeName.isEmpty ? "Here now" : "Here now · \(model.placeName)")
          .font(.lociHeadline())
          .foregroundStyle(Color.lociInk)
        today(brief)
        list("Around you", brief.local)
        list("Getting around", brief.disruption)
        list("What's on", brief.whatsOn)
        if brief.stale {
          Text("Some sources delayed").font(.lociCaption(11)).foregroundStyle(.secondary)
        }
      }
      .padding(16)
      .frame(maxWidth: .infinity, alignment: .leading)
      .background(Color.lociCard, in: RoundedRectangle(cornerRadius: LociTheme.cornerRadius, style: .continuous))
      .overlay(RoundedRectangle(cornerRadius: LociTheme.cornerRadius, style: .continuous).stroke(Color.lociBorder))
    }
  }

  @ViewBuilder private func today(_ brief: Loci_Localcontext_HereBrief) -> some View {
    if let t = brief.weather.first {
      HStack(alignment: .firstTextBaseline, spacing: 8) {
        Text("\(Int(t.highC.rounded()))° / \(Int(t.lowC.rounded()))°").font(.lociBody()).monospacedDigit()
        Text(brief.weatherIsEstimated ? "\(t.condition) · estimated" : t.condition)
          .font(.lociCaption(13)).foregroundStyle(.secondary)
      }
    }
    ForEach(Array(brief.alerts.prefix(3).enumerated()), id: \.offset) { _, alert in
      Label(alert.title, systemImage: "exclamationmark.triangle")
        .font(.lociCaption(13))
        .foregroundStyle(alert.severity >= 0.6 ? Color.red : Color.lociInk)
    }
  }

  @ViewBuilder private func list(_ title: String, _ items: [Loci_Localcontext_NewsTickerItem]) -> some View {
    if !items.isEmpty {
      VStack(alignment: .leading, spacing: 8) {
        Text(title.uppercased()).font(.lociCaption(11)).tracking(1.2).foregroundStyle(.secondary)
        ForEach(items.prefix(3), id: \.id) { item in
          Button {
            if let url = URL(string: item.url) { openURL(url) }
          } label: {
            VStack(alignment: .leading, spacing: 2) {
              Text(item.title).font(.lociBody()).foregroundStyle(Color.lociInk).lineLimit(2).multilineTextAlignment(.leading)
              Text(item.source).font(.lociCaption(11)).foregroundStyle(.secondary)
            }
          }
          .buttonStyle(.plain)
        }
      }
    }
  }
}
```

If a theme token named here does not exist, grep `Core/` for the actual token (e.g. `rg "static let loci" loci/loci/Core`) and use it. Do not add new tokens.

- [ ] **Step 3: Mount in `DiscoverView.swift`**

Add state: `@State private var here = HereBriefModel()`.

In `hero`, directly under `Text("Where to next?")`:

```swift
      if !here.placeName.isEmpty {
        Text(here.placeName).font(.lociCaption(13)).foregroundStyle(Color.lociForest)
      }
```

In the `VStack` body, after `hero`: `HereBriefSection(model: here)`.

Load on appear and on refresh. Change `.task { if page == nil { await load() } }` to:

```swift
      .task {
        async let brief: Void = here.load()
        if page == nil { await load() }
        await brief
      }
```

and `.refreshable { await load() }` to `.refreshable { async let b: Void = here.load(); await load(); await b }`.

- [ ] **Step 4: Build + tests**

```bash
cd ~/Work/production/apps/Loci/loci-ios/loci
xcodebuild -scheme loci -destination 'platform=iOS Simulator,name=iPhone 17' build test 2>&1 | tail -20
```

Expected: `** TEST SUCCEEDED **`.

- [ ] **Step 5: Simulator check.** Run the app, set the simulated location to 41.69,-8.83 (Features → Location → Custom), grant location in Nearby once, then open Discover. Expected: the place line and the section appear. With location reset to "Don't allow", the section is absent and there is no prompt. Screenshot both.

- [ ] **Step 6: Commit, PR**

```bash
git add loci/loci/Features/Discover/UI/HereBriefSection.swift loci/loci/Features/Discover/UI/DiscoverView.swift
git commit -m "Show where the traveller is standing on Discover"
git push -u origin feat/ios-here-brief && gh pr create --fill --base main
```

---

# Part B — Calendar connect go-live (Google Calendar + Calendly)

**What already exists (verified 2026-09-23):**
- Server `internal/domain/calendar/oauth.go` reads `GOOGLE_CALENDAR_CLIENT_ID/SECRET`, falling back to the sign-in `GOOGLE_CLIENT_ID/SECRET`. It also reads `CALENDLY_CLIENT_ID/SECRET`, `OAUTH_CALLBACK_URL` and `SESSION_SECRET` (state HMAC).
- The redirect URIs it sends are `https://lociai.fyi/auth/oauth/google-calendar/callback` and `.../calendly/callback`. `OAUTH_CALLBACK_URL` is already set in `apps/loci/data/config.yaml`.
- The web callback page (`src/lib/auth/oauth-callback-action.ts`) either postMessages the opener (web popup) or bounces to `loci://oauth2redirect/<provider>` for iOS `ASWebAuthenticationSession`. Both providers are in `NATIVE_PROVIDERS`.
- iOS `CalendarConnectService` + `CalendarConnectionsView` do start → web auth → complete.
- **Sealed today:** `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` and `SESSION_SECRET` are sealed. `CALENDLY_*` is **not** sealed. Result: `StartCalendarConnect(calendly)` returns `FailedPrecondition`, which the apps show as "That calendar isn't available right now." Google gets past start, but it only works if the console steps below are done.
- The API pod has no egress policy, so it can already reach `oauth2.googleapis.com`, `www.googleapis.com`, `auth.calendly.com` and `api.calendly.com`.

No code changes are planned. If a verification step fails, debug it with superpowers:systematic-debugging; don't guess.

### Task B1: Google Cloud console (you do this; ~15 min)

In the Google Cloud project that owns the **web** OAuth client sealed as `GOOGLE_CLIENT_ID`. This is not the iOS client `1062609475304-00i5…`.

- [ ] **APIs & Services → Library → Google Calendar API → Enable.**
- [ ] **Credentials → the web OAuth client → Authorized redirect URIs**, add exactly (no trailing slash):
  - `https://lociai.fyi/auth/oauth/google-calendar/callback`
  - `http://localhost:3000/auth/oauth/google-calendar/callback` (local dev)
- [ ] **OAuth consent screen → Data access → Add scopes:** `https://www.googleapis.com/auth/calendar.events` and `https://www.googleapis.com/auth/userinfo.email`.
- [ ] **Publishing status decision.** `calendar.events` is a *sensitive* scope:
  - **Testing mode:** add each tester's Google account under *Audience → Test users* (max 100). Refresh tokens expire after **7 days**, so connections silently die weekly. Fine for proving it works.
  - **In production, unverified:** users see a "Google hasn't verified this app" interstitial, capped at 100 users.
  - **Verified (needed for strangers):** submit for verification. Google needs a homepage and a privacy policy on `lociai.fyi` that say what calendar data is used for, a scope justification ("adds a trip's days to the user's calendar and shows their existing events next to trips"), and a demo video of the consent and usage flow. This takes days to weeks, so start it now if calendar matters for the first 10 strangers.

### Task B2: Calendly developer app (you do this; ~10 min)

- [ ] At `https://developer.calendly.com` → **Create new app**. Kind: **Web**. Environment: **Production**.
- [ ] Redirect URIs: `https://lociai.fyi/auth/oauth/calendly/callback` and, for dev, `http://localhost:3000/auth/oauth/calendly/callback`. (Calendly may reject `http://` on a production app. If it does, create a second **Sandbox** app for localhost.)
- [ ] Copy the **Client ID** and **Client secret**. The secret is shown once.

### Task B3: Seal Calendly credentials (you run it; the plaintext never goes through chat)

Per `platform/infra/secrets/loci/README.md`:

- [ ] Branch: `cd ~/Work/production/platform/infra && git fetch origin && git switch -c secrets/loci-calendly origin/main`
- [ ] Write the plaintext file:

```bash
umask 077
${EDITOR:-vi} /tmp/loci-calendly.env
```

```
CALENDLY_CLIENT_ID=<from B2>
CALENDLY_CLIENT_SECRET=<from B2>
```

(Optional: if you created a dedicated Google client for calendar instead of reusing sign-in, add `GOOGLE_CALENDAR_CLIENT_ID=` and `GOOGLE_CALENDAR_CLIENT_SECRET=` lines too.)

- [ ] Seal and merge into the existing secret. The name `loci-env` and namespace `horus` must be exact, or the key silently never appears:

```bash
kubectl create secret generic loci-env -n horus \
  --from-env-file=/tmp/loci-calendly.env --dry-run=client -o json \
| kubeseal --cert ~/.kube/sealed-secrets-maat.pem \
           --format yaml --merge-into secrets/loci/loci-env.yaml
rm -f /tmp/loci-calendly.env
git diff --stat secrets/loci/loci-env.yaml
grep -cE '^    CALENDLY_' secrets/loci/loci-env.yaml   # expect 2
```

- [ ] Commit only that file, PR, merge:

```bash
git add secrets/loci/loci-env.yaml
git commit -m "Seal Calendly OAuth credentials for Loci"
git push -u origin secrets/loci-calendly && gh pr create --fill --base main
```

- [ ] After ArgoCD syncs, confirm and restart (the pod reads env only at start):

```bash
export KUBECONFIG=~/.kube/maat.yaml
kubectl get secret -n horus loci-env -o go-template='{{range $k,$v := .data}}{{$k}}{{"\n"}}{{end}}' | grep CALENDLY
kubectl -n horus rollout restart deploy/loci-api && kubectl -n horus rollout status deploy/loci-api
```

### Task B4: Verify end-to-end on web and iOS

Use the account you added as a Google test user.

- [ ] **Web, Google:** lociai.fyi → Settings → Connected calendars → Connect Google Calendar. The popup shows consent listing "See, edit, share… events" and email. After you approve, the row shows your email. Then:
  - `/calendar` lists your real events
  - on a trip, *Add to calendar* creates the events in Google Calendar (check in Google Calendar itself)
- [ ] **Web, Calendly:** Connect Calendly → Calendly consent → the row shows your Calendly name, and booked meetings appear on `/calendar`.
- [ ] **iOS (TestFlight build ≥ the one with `CalendarConnectionsView`):** Calendar tab → Connections → Connect Google Calendar. The sheet opens `accounts.google.com`, you approve, and the sheet closes by itself (the `loci://` bounce), then the row shows connected. Repeat for Calendly.
- [ ] **Disconnect** on each platform removes the row, and reconnecting works.

**Failure → cause table** (check this before debugging):

| Symptom | Cause | Fix |
|---|---|---|
| "That calendar isn't available right now." / `failed_precondition` | env var missing in the pod | B3 not merged or pod not restarted |
| Google `Error 400: redirect_uri_mismatch` | URI not registered on *that* client | B1 redirect URI, exact string |
| Google `Error 403: access_denied`, "has not completed the Google verification process" | Testing mode, account not a test user | B1 add test user |
| Google API `403 accessNotConfigured` in server logs | Calendar API not enabled | B1 enable API |
| Calendly `invalid_redirect_uri` | URI mismatch on the Calendly app | B2 |
| iOS sheet stays open on a lociai.fyi page | the callback page didn't bounce: web client older than `1a4ca8d`, or a stale service worker | hard-reload lociai.fyi in Safari, then check the live bundle has `oauth2redirect` |
| Worked, broke a week later (Google) | Testing-mode 7-day refresh-token expiry | publish + verify (B1) |

---

## Order of work

Part B's console steps (B1, B2) are yours and don't depend on anything, so start them in parallel with Part A. B3 needs B2. B4 needs B1 and B3.

Part A: A1 → (user merge + tag) → A2 → A3 → A4 → (user merge) → A5 → A6 → A7 → A8. A6–A8 need A5 deployed before their visual checks, but A6/A7 code can be written against the BSR package as soon as A1 is pushed.
