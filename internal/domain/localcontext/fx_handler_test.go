package localcontext

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
)

func fxHandler(t *testing.T, body string, status int) *Handler {
	t.Helper()
	url, _ := serve(t, body, status)
	h := NewHandler(StubWeather{}, true, slog.New(slog.NewTextHandler(discard{}, nil)))
	return h.WithFX(NewFXAdapter(url, testClient()), "EUR", 6.5, 1.75)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestGetFxRates_ReturnsRates(t *testing.T) {
	h := fxHandler(t, frankfurterFixture, http.StatusOK)

	resp, err := h.GetFxRates(context.Background(), connect.NewRequest(&lcv1.GetFxRatesRequest{
		Quotes: []string{"USD"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Msg.Rates) != 1 {
		t.Fatalf("got %d rates", len(resp.Msg.Rates))
	}
	r := resp.Msg.Rates[0]
	if r.Base != "EUR" || r.Quote != "USD" || r.Rate != 1.1645 {
		t.Errorf("got %+v", r)
	}
	// A rate without its date is a trap: the ECB publishes once a working day.
	if r.AsOf == nil {
		t.Error("a rate must carry the date it was published")
	}
}

// A caller holding a destination should not have to know its currency.
func TestGetFxRates_ResolvesCurrencyFromCountry(t *testing.T) {
	h := fxHandler(t, frankfurterFixture, http.StatusOK)
	cc := "US"

	resp, err := h.GetFxRates(context.Background(), connect.NewRequest(&lcv1.GetFxRatesRequest{
		CountryCode: &cc,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Msg.Rates) != 1 || resp.Msg.Rates[0].Quote != "USD" {
		t.Errorf("expected USD from country US, got %+v", resp.Msg.Rates)
	}
}

// Knowing the country and being unable to price it is worth saying: an empty
// response is indistinguishable from a failure.
func TestGetFxRates_UnpriceableCountryIsNamed(t *testing.T) {
	h := fxHandler(t, frankfurterFixture, http.StatusOK)
	cc := "VN"

	resp, err := h.GetFxRates(context.Background(), connect.NewRequest(&lcv1.GetFxRatesRequest{
		CountryCode: &cc,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Msg.Rates) != 0 {
		t.Errorf("expected no rates, got %+v", resp.Msg.Rates)
	}
	if len(resp.Msg.Unsupported) != 1 || resp.Msg.Unsupported[0] != "VN" {
		t.Errorf("expected VN named as unsupported, got %v", resp.Msg.Unsupported)
	}
}

// A provider outage must not fail the RPC — the same convention the rest of
// this handler follows.
func TestGetFxRates_ProviderFailureDegrades(t *testing.T) {
	h := fxHandler(t, `{"message":"not found"}`, http.StatusNotFound)

	resp, err := h.GetFxRates(context.Background(), connect.NewRequest(&lcv1.GetFxRatesRequest{
		Quotes: []string{"USD"},
	}))
	if err != nil {
		t.Fatalf("a provider outage must not fail the RPC, got %v", err)
	}
	if len(resp.Msg.Rates) != 0 {
		t.Errorf("got %+v", resp.Msg.Rates)
	}
}

// Unconfigured must not look like a server fault.
func TestGetFxRates_UnconfiguredIsEmptyNotAnError(t *testing.T) {
	h := NewHandler(StubWeather{}, true, slog.New(slog.NewTextHandler(discard{}, nil)))

	resp, err := h.GetFxRates(context.Background(), connect.NewRequest(&lcv1.GetFxRatesRequest{
		Quotes: []string{"USD"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Msg.Rates) != 0 {
		t.Errorf("got %+v", resp.Msg.Rates)
	}
}

// --- drive cost ------------------------------------------------------------

func TestEstimateDriveCost_RPC(t *testing.T) {
	h := fxHandler(t, frankfurterFixture, http.StatusOK)

	resp, err := h.EstimateDriveCost(context.Background(), connect.NewRequest(&lcv1.EstimateDriveCostRequest{
		DistanceKm: 313,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := resp.Msg.Estimate
	if e == nil {
		t.Fatal("expected an estimate")
	}
	if e.Cost != 35.6 {
		t.Errorf("cost: got %v, want 35.60", e.Cost)
	}
	if e.Currency != "EUR" {
		t.Errorf("currency: got %q", e.Currency)
	}
	if !strings.Contains(e.Assumptions, "Fuel only") {
		t.Errorf("an estimate must state its assumptions, got %q", e.Assumptions)
	}
}

// The arithmetic needs no provider, so it must answer even with FX switched off.
func TestEstimateDriveCost_WorksWithoutFXConfigured(t *testing.T) {
	h := NewHandler(StubWeather{}, true, slog.New(slog.NewTextHandler(discard{}, nil)))

	resp, err := h.EstimateDriveCost(context.Background(), connect.NewRequest(&lcv1.EstimateDriveCostRequest{
		DistanceKm: 100,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Msg.Estimate == nil || resp.Msg.Estimate.Cost <= 0 {
		t.Errorf("expected a usable estimate, got %+v", resp.Msg.Estimate)
	}
	if resp.Msg.Estimate.Currency != "EUR" {
		t.Errorf("currency should fall back to EUR, got %q", resp.Msg.Estimate.Currency)
	}
}

func TestEstimateDriveCost_ZeroDistanceCostsNothing(t *testing.T) {
	h := fxHandler(t, frankfurterFixture, http.StatusOK)

	resp, err := h.EstimateDriveCost(context.Background(), connect.NewRequest(&lcv1.EstimateDriveCostRequest{
		DistanceKm: 0,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Msg.Estimate.Cost != 0 {
		t.Errorf("got %v, want 0", resp.Msg.Estimate.Cost)
	}
	// Even a zero estimate states its assumptions, so the panel never shows a
	// bare number.
	if resp.Msg.Estimate.Assumptions == "" {
		t.Error("assumptions must always be populated")
	}
}
