package hotelrates

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

func testCache(t *testing.T) cachestore.Store {
	t.Helper()
	c, err := cachestore.New(cachestore.Config{}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func testClient() *httpx.Client {
	return httpx.New(httpx.Config{MaxRetries: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, RatePerSecond: 1000, Burst: 1000})
}

func stay() Stay {
	return Stay{
		Checkin:  time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC),
		Checkout: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		Adults:   2,
		Currency: "EUR",
	}
}

// fakeLiteAPI serves /data/hotels from a fixed catalogue and /hotels/rates
// from a price table, recording what it was asked.
type fakeLiteAPI struct {
	t         *testing.T
	hotels    []liteHotel
	prices    map[string]string // hotel id -> offerRetailRate JSON
	hotelHits atomic.Int32
	rateHits  atomic.Int32
	mu        sync.Mutex
	lastRates ratesRequest
	queries   []url.Values
	status    int
}

func (f *fakeLiteAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("X-API-Key"); got != "test-key" {
		f.t.Errorf("X-API-Key = %q", got)
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	switch r.URL.Path {
	case "/data/hotels":
		f.hotelHits.Add(1)
		q := r.URL.Query()
		f.mu.Lock()
		f.queries = append(f.queries, q)
		f.mu.Unlock()
		name := strings.ToLower(q.Get("hotelName"))
		var out []liteHotel
		for _, h := range f.hotels {
			if name == "" || strings.Contains(strings.ToLower(h.Name), name) {
				out = append(out, h)
			}
		}
		_ = json.NewEncoder(w).Encode(hotelsResponse{Data: out})
	case "/hotels/rates":
		f.rateHits.Add(1)
		body, _ := io.ReadAll(r.Body)
		var req ratesRequest
		if err := json.Unmarshal(body, &req); err != nil {
			f.t.Errorf("rates body: %v", err)
		}
		f.mu.Lock()
		f.lastRates = req
		f.mu.Unlock()
		var parts []string
		for _, id := range req.HotelIDs {
			if p, ok := f.prices[id]; ok {
				parts = append(parts, `{"hotelId":"`+id+`","roomTypes":[`+p+`]}`)
			}
		}
		_, _ = io.WriteString(w, `{"data":[`+strings.Join(parts, ",")+`]}`)
	default:
		f.t.Errorf("unexpected path %q", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func newFake(t *testing.T) (*fakeLiteAPI, *LiteAPI) {
	t.Helper()
	f := &fakeLiteAPI{
		t: t,
		hotels: []liteHotel{
			{ID: "lp1", Name: "Hotel Arts Barcelona", Latitude: 41.3868, Longitude: 2.1963, City: "Barcelona", Country: "ES"},
			{ID: "lp2", Name: "Casa Fuster Hotel", Latitude: 41.3980, Longitude: 2.1560, City: "Barcelona", Country: "ES"},
			{ID: "lp3", Name: "Arts Hostel Gràcia", Latitude: 41.4040, Longitude: 2.1570, City: "Barcelona", Country: "ES"},
		},
		prices: map[string]string{
			"lp1": `{"offerRetailRate":{"amount":701.5,"currency":"EUR"}},{"offerRetailRate":{"amount":640.0,"currency":"EUR"}}`,
			// Array-typed price, as the schema documents it.
			"lp2": `{"offerRetailRate":[{"amount":300.0,"currency":"EUR"}]}`,
		},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	margin := 10.0
	p := NewLiteAPI(LiteAPIConfig{
		BaseURL:       srv.URL,
		APIKey:        "test-key",
		WhitelabelURL: "https://book.example.com/",
		Margin:        &margin,
	}, testClient(), testCache(t))
	return f, p
}

func TestRatesMatchesPricesAndOrders(t *testing.T) {
	f, p := newFake(t)
	got, err := p.Rates(context.Background(), []Query{
		{Key: "a", Name: "Casa Fuster", Lat: 41.3982, Lon: 2.1562, City: "Barcelona", CountryCode: "es"},
		{Key: "b", Name: "Hotel Arts", Lat: 41.3870, Lon: 2.1960, City: "Barcelona", CountryCode: "ES"},
		{Key: "c", Name: "Nonexistent Palace", Lat: 41.39, Lon: 2.17, City: "Barcelona", CountryCode: "ES"},
	}, stay())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Key != "a" || got[1].Key != "b" || got[2].Key != "c" {
		t.Fatalf("order/keys wrong: %+v", got)
	}

	if got[0].Status != StatusPriced || got[0].TotalMinor != 30000 || got[0].NightlyMinor != 15000 || got[0].Currency != "EUR" {
		t.Errorf("casa fuster = %+v", got[0])
	}
	// Cheapest room type wins.
	if got[1].Status != StatusPriced || got[1].TotalMinor != 64000 || got[1].NightlyMinor != 32000 {
		t.Errorf("arts = %+v", got[1])
	}
	if got[2].Status != StatusUnmatched || got[2].BookingURL != "" {
		t.Errorf("unknown hotel = %+v", got[2])
	}

	u, err := url.Parse(got[0].BookingURL)
	if err != nil || u.Host != "book.example.com" || u.Path != "/hotels/lp2" {
		t.Fatalf("booking url %q", got[0].BookingURL)
	}
	q := u.Query()
	if q.Get("checkin") != "2026-11-13" || q.Get("checkout") != "2026-11-15" || q.Get("currency") != "EUR" {
		t.Errorf("booking query %v", q)
	}
	occ, _ := base64.StdEncoding.DecodeString(q.Get("occupancies"))
	if string(occ) != `[{"adults":2}]` {
		t.Errorf("occupancies %s", occ)
	}

	req := f.lastRates
	if req.Checkin != "2026-11-13" || req.Checkout != "2026-11-15" || req.Currency != "EUR" ||
		req.GuestNationality != "US" || req.MaxRatesPerHotel != 1 || req.Margin == nil || *req.Margin != 10 ||
		len(req.Occupancies) != 1 || req.Occupancies[0].Adults != 2 {
		t.Errorf("rates request %+v", req)
	}
	if len(req.HotelIDs) != 2 {
		t.Errorf("priced ids %v, want the two matches", req.HotelIDs)
	}
}

func TestRatesUsesCache(t *testing.T) {
	f, p := newFake(t)
	q := []Query{{Key: "a", Name: "Casa Fuster", Lat: 41.3982, Lon: 2.1562, City: "Barcelona", CountryCode: "ES"}}
	for range 3 {
		if _, err := p.Rates(context.Background(), q, stay()); err != nil {
			t.Fatal(err)
		}
	}
	if f.hotelHits.Load() != 1 || f.rateHits.Load() != 1 {
		t.Errorf("hits hotels=%d rates=%d, want 1 and 1", f.hotelHits.Load(), f.rateHits.Load())
	}
	// A different stay is a different price.
	s := stay()
	s.Checkout = s.Checkout.AddDate(0, 0, 1)
	if _, err := p.Rates(context.Background(), q, s); err != nil {
		t.Fatal(err)
	}
	if f.hotelHits.Load() != 1 || f.rateHits.Load() != 2 {
		t.Errorf("after new stay hotels=%d rates=%d, want 1 and 2", f.hotelHits.Load(), f.rateHits.Load())
	}
}

func TestRatesSoldOutWhenMatchedButUnpriced(t *testing.T) {
	_, p := newFake(t)
	got, err := p.Rates(context.Background(), []Query{
		{Key: "x", Name: "Arts Hostel Gracia", Lat: 41.4041, Lon: 2.1571, City: "Barcelona", CountryCode: "ES"},
	}, stay())
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != StatusSoldOut || got[0].Provider != ProviderName {
		t.Errorf("got %+v, want sold out", got[0])
	}
}

func TestRatesProviderDownIsUnavailable(t *testing.T) {
	f, p := newFake(t)
	f.status = http.StatusServiceUnavailable
	_, err := p.Rates(context.Background(), []Query{
		{Key: "a", Name: "Casa Fuster", Lat: 41.3982, Lon: 2.1562, City: "Barcelona", CountryCode: "ES"},
	}, stay())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestRatesRefusedQueryIsUnmatchedNotError(t *testing.T) {
	f, p := newFake(t)
	f.status = http.StatusBadRequest
	got, err := p.Rates(context.Background(), []Query{
		{Key: "a", Name: "Casa Fuster", Lat: 41.3982, Lon: 2.1562, City: "Barcelona", CountryCode: "ZZ"},
	}, stay())
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != StatusUnmatched {
		t.Errorf("got %+v", got[0])
	}
}

func TestPickHotel(t *testing.T) {
	cands := []liteHotel{
		{ID: "arts", Name: "Hotel Arts Barcelona", Latitude: 41.3868, Longitude: 2.1963, City: "Barcelona"},
		{ID: "arts-hostel", Name: "Arts Hostel", Latitude: 41.3870, Longitude: 2.1965, City: "Barcelona"},
		{ID: "far", Name: "Majestic Hotel & Spa", Latitude: 41.3950, Longitude: 2.1630, City: "Barcelona"},
		{ID: "lisbon", Name: "Majestic", Latitude: 38.72, Longitude: -9.14, City: "Lisboa"},
	}
	ig := ignoreFor("Barcelona")
	cases := []struct {
		name string
		q    Query
		geo  bool
		want string
	}{
		{"generic words and city ignored", Query{Name: "Arts", Lat: 41.3869, Lon: 2.1962}, true, "arts"},
		{"accents and case", Query{Name: "HÒTEL ARTS", Lat: 41.3869, Lon: 2.1962}, true, "arts"},
		{"too far away", Query{Name: "Majestic Hotel", Lat: 41.3800, Lon: 2.1900}, true, ""},
		{"no name overlap", Query{Name: "Mandarin Oriental", Lat: 41.3869, Lon: 2.1962}, true, ""},
		{"no geo needs same city", Query{Name: "Majestic Hotel Spa", City: "Barcelona"}, false, "far"},
		{"no geo wrong city", Query{Name: "Majestic", City: "Porto"}, false, ""},
	}
	for _, c := range cases {
		if got := pickHotel(c.q, cands, c.geo, ig); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestMatchSkipsNameThatIsOnlyGenericWords(t *testing.T) {
	f, p := newFake(t)
	got, err := p.Rates(context.Background(), []Query{
		{Key: "a", Name: "Hotel Barcelona", Lat: 41.39, Lon: 2.17, City: "Barcelona", CountryCode: "ES"},
	}, stay())
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Status != StatusUnmatched || f.hotelHits.Load() != 0 {
		t.Errorf("got %+v, hits %d", got[0], f.hotelHits.Load())
	}
}

func TestToMinorAndNights(t *testing.T) {
	if ToMinor(142.555, "EUR") != 14256 || ToMinor(18000, "JPY") != 18000 || ToMinor(1.2345, "KWD") != 1235 {
		t.Error("ToMinor exponent handling")
	}
	if stay().Nights() != 2 {
		t.Error("nights")
	}
}
