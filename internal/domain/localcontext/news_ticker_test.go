package localcontext

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// --- pure selection -------------------------------------------------------

func TestCountryCodeFromProfile(t *testing.T) {
	cases := map[string]string{"PT": "PT", "pt": "PT", "Portugal": "PT", " portugal ": "PT", "United States": "US", "USA": "US", "Deutschland": "DE", "Nowhereland": ""}
	for in, want := range cases {
		if got := countryCodeFromName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestNewsCountriesOrderAndCap(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-48*time.Hour), now.Add(10*24*time.Hour)
	sel := newsCountries(newsInputs{
		Home:     "Portugal",
		NextTrip: "ES",
		Visited: []VisitedCountry{
			{Code: "FR", At: past},
			{Code: "IT", At: past.Add(-time.Hour)},
			{Code: "PT", At: past},
			{Code: "DE", At: past.Add(-2 * time.Hour)},
			{Code: "ES", At: future},
		},
	})
	if got := strings.Join(sel, ","); got != "PT,ES,FR,IT,DE" {
		t.Errorf("selection = %s (home, next trip, then visited newest-first, deduped, capped)", got)
	}
	if got := newsCountries(newsInputs{}); len(got) != 0 {
		t.Errorf("nothing known → nothing selected, got %v", got)
	}
}

func TestFeedsForCountries(t *testing.T) {
	feeds := feedsForCountries([]string{"PT", "ES"})
	if len(feeds) != 4 {
		t.Fatalf("two feeds per country, got %d: %v", len(feeds), feeds)
	}
	if !strings.Contains(feeds[0].URL, "gl=PT") || feeds[0].Country != "PT" {
		t.Errorf("first feed = %+v", feeds[0])
	}
	if !strings.Contains(feeds[1].URL, "q=") || !strings.Contains(feeds[1].URL, "Portugal") {
		t.Errorf("second feed should be the safety/weather query naming the country: %s", feeds[1].URL)
	}
	if got := feedsForCountries(make([]string, 0)); len(got) != 0 {
		t.Errorf("no countries → no feeds")
	}
	many := feedsForCountries([]string{"PT", "ES", "FR", "IT", "DE", "GB", "US"})
	if len(many) > maxNewsFeeds {
		t.Errorf("capped at %d feeds, got %d", maxNewsFeeds, len(many))
	}
}

// --- service with fakes ---------------------------------------------------

type fakeHome struct{ country string }

func (f fakeHome) HomeCountry(_ context.Context, _ uuid.UUID) (string, error) { return f.country, nil }

type fakeNextTrip struct{ code string }

func (f fakeNextTrip) NextTripCountry(_ context.Context, _ uuid.UUID, _ time.Time) (string, error) {
	return f.code, nil
}

type fakeVisited struct{ visits []VisitedCountry }

func (f fakeVisited) RecentCountries(_ context.Context, _ uuid.UUID, _ int) ([]VisitedCountry, error) {
	return f.visits, nil
}

type memPrefs struct{ enabled map[uuid.UUID]bool }

func (m *memPrefs) NewsTickerEnabled(_ context.Context, id uuid.UUID) (bool, error) {
	if v, ok := m.enabled[id]; ok {
		return v, nil
	}
	return true, nil
}

func (m *memPrefs) SetNewsTickerEnabled(_ context.Context, id uuid.UUID, v bool) error {
	if m.enabled == nil {
		m.enabled = map[uuid.UUID]bool{}
	}
	m.enabled[id] = v
	return nil
}

const feedsFixture = `{"version":"https://jsonfeed.org/version/1.1","title":"Merged timeline","items":[
 {"id":"a","url":"https://pub.pt/a","title":"Storm warning for Lisbon","date_published":"2026-09-17T11:00:00Z","_feeds":{"source_name":"Público","feed_url":"https://news.google.com/rss?hl=en&gl=PT&ceid=PT:en"}},
 {"id":"b","url":"https://pub.es/b","title":"Madrid strike","date_published":"2026-09-17T10:00:00Z","_feeds":{"source_name":"El País","feed_url":"https://news.google.com/rss?hl=en&gl=ES&ceid=ES:en"}}],
 "_feeds":{"warming":[],"stale":["https://news.google.com/rss?hl=en&gl=ES&ceid=ES:en"],"generated_at":"2026-09-17T12:00:00Z"}}`

func newsTickerHarness(t *testing.T, status int) (*NewsTickerService, *httptest.Server, uuid.UUID) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/items" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(feedsFixture))
	}))
	t.Cleanup(srv.Close)
	userID := uuid.New()
	svc := NewNewsTickerService(NewsTickerDeps{
		Client:   httpx.New(httpx.Config{Timeout: 2 * time.Second, RatePerSecond: 100, Burst: 100}),
		BaseURL:  srv.URL,
		Home:     fakeHome{country: "Portugal"},
		NextTrip: fakeNextTrip{code: "ES"},
		Visited:  fakeVisited{visits: []VisitedCountry{{Code: "ES", At: time.Now().Add(-time.Hour)}}},
		Prefs:    &memPrefs{},
		Cache:    newSignalCache(newTestStore(t), nil),
		Logger:   slog.New(slog.NewTextHandler(discard{}, nil)),
	})
	return svc, srv, userID
}

func TestNewsTickerServiceSelectsCountriesAndMapsItems(t *testing.T) {
	svc, _, userID := newsTickerHarness(t, http.StatusOK)
	out, err := svc.Ticker(context.Background(), userID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Enabled || !out.Stale {
		t.Errorf("enabled=%v stale=%v", out.Enabled, out.Stale)
	}
	if strings.Join(out.CountryCodes, ",") != "PT,ES" {
		t.Errorf("countries = %v", out.CountryCodes)
	}
	if len(out.Items) != 2 || out.Items[0].Title != "Storm warning for Lisbon" || out.Items[0].CountryCode != "PT" || out.Items[1].CountryCode != "ES" {
		t.Errorf("items = %+v", out.Items)
	}
	if out.Items[0].Source != "Público" || out.Items[0].URL != "https://pub.pt/a" {
		t.Errorf("item[0] = %+v", out.Items[0])
	}
}

func TestNewsTickerServiceCachesPerUser(t *testing.T) {
	svc, srv, userID := newsTickerHarness(t, http.StatusOK)
	calls := 0
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(feedsFixture))
	})
	for i := 0; i < 3; i++ {
		if _, err := svc.Ticker(context.Background(), userID, 10); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("upstream calls = %d, want 1 (cached)", calls)
	}
}

func TestNewsTickerServiceDisabledSkipsUpstream(t *testing.T) {
	svc, srv, userID := newsTickerHarness(t, http.StatusOK)
	calls := 0
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte(feedsFixture)) })
	if err := svc.SetEnabled(context.Background(), userID, false); err != nil {
		t.Fatal(err)
	}
	out, err := svc.Ticker(context.Background(), userID, 10)
	if err != nil || out.Enabled || len(out.Items) != 0 || calls != 0 {
		t.Errorf("out=%+v err=%v calls=%d", out, err, calls)
	}
}

func TestNewsTickerServiceUpstreamFailureIsEmptyNotError(t *testing.T) {
	svc, _, userID := newsTickerHarness(t, http.StatusBadGateway)
	out, err := svc.Ticker(context.Background(), userID, 10)
	if err != nil {
		t.Fatalf("a dead aggregator must not fail the call: %v", err)
	}
	if !out.Enabled || len(out.Items) != 0 || !out.Stale {
		t.Errorf("out = %+v", out)
	}
}

// --- handler --------------------------------------------------------------

func TestGetNewsTickerHandlerRequiresAuthAndDelegates(t *testing.T) {
	svc, _, userID := newsTickerHarness(t, http.StatusOK)
	h := NewHandler(nil, true, slog.New(slog.NewTextHandler(discard{}, nil))).WithNewsTicker(svc)

	if _, err := h.GetNewsTicker(context.Background(), connect.NewRequest(&lcv1.GetNewsTickerRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("no user in context → unauthenticated, got %v", err)
	}
	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, userID.String())
	resp, err := h.GetNewsTicker(ctx, connect.NewRequest(&lcv1.GetNewsTickerRequest{Limit: 5}))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Msg.GetEnabled() || len(resp.Msg.GetItems()) != 2 || resp.Msg.GetItems()[0].GetCountryCode() != "PT" || resp.Msg.GetItems()[0].GetPublishedAt() == nil {
		t.Errorf("resp = %+v", resp.Msg)
	}
	set, err := h.SetNewsTickerEnabled(ctx, connect.NewRequest(&lcv1.SetNewsTickerEnabledRequest{Enabled: false}))
	if err != nil || set.Msg.GetEnabled() {
		t.Errorf("set = %+v err=%v", set.Msg, err)
	}
	resp, _ = h.GetNewsTicker(ctx, connect.NewRequest(&lcv1.GetNewsTickerRequest{}))
	if resp.Msg.GetEnabled() {
		t.Error("switch should persist")
	}
}

func TestGetNewsTickerWithoutServiceIsDisabled(t *testing.T) {
	h := NewHandler(nil, true, slog.New(slog.NewTextHandler(discard{}, nil)))
	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, uuid.New().String())
	resp, err := h.GetNewsTicker(ctx, connect.NewRequest(&lcv1.GetNewsTickerRequest{}))
	if err != nil || resp.Msg.GetEnabled() {
		t.Errorf("no service configured → enabled=false, err=%v", err)
	}
}
