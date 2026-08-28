package localcontext

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const frankfurterFixture = `{"amount":1.0,"base":"EUR","date":"2026-08-27","rates":{"GBP":0.8574,"JPY":185.61,"USD":1.1645}}`

func fxAdapter(t *testing.T, body string, status int) (*FXAdapter, *int64) {
	t.Helper()
	url, hits := serve(t, body, status)
	return NewFXAdapter(url, testClient(), testCache(t)), hits
}

func TestFX_ReturnsRates(t *testing.T) {
	a, _ := fxAdapter(t, frankfurterFixture, http.StatusOK)

	rates, unsupported, err := a.Rates(context.Background(), "EUR", []string{"USD", "GBP", "JPY"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unsupported) != 0 {
		t.Errorf("unexpected unsupported: %v", unsupported)
	}
	if len(rates) != 3 {
		t.Fatalf("expected 3 rates, got %d", len(rates))
	}

	byQuote := map[string]FxRate{}
	for _, r := range rates {
		byQuote[r.Quote] = r
	}
	if got := byQuote["USD"].Rate; got != 1.1645 {
		t.Errorf("USD rate: got %v, want 1.1645", got)
	}
	if byQuote["USD"].Base != "EUR" {
		t.Errorf("base: got %q", byQuote["USD"].Base)
	}
	// The date matters: a stale rate presented as current is worse than none.
	if byQuote["USD"].AsOf.IsZero() {
		t.Error("a rate must carry the date it was published")
	}
}

// A currency the ECB does not publish must be named, not silently dropped —
// "no rate available for X" is honest, showing nothing looks like a bug.
func TestFX_UnsupportedQuotesAreReportedNotDropped(t *testing.T) {
	a, hits := fxAdapter(t, frankfurterFixture, http.StatusOK)

	rates, unsupported, err := a.Rates(context.Background(), "EUR", []string{"USD", "VND", "ARS"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unsupported) != 2 {
		t.Errorf("expected VND and ARS reported, got %v", unsupported)
	}
	if len(rates) == 0 {
		t.Error("the supported quote should still be returned")
	}
	// Unsupported codes must be filtered before the call — the API 404s on them
	// and would lose the whole batch.
	if *hits != 1 {
		t.Errorf("expected exactly 1 upstream call, got %d", *hits)
	}
}

func TestFX_AllQuotesUnsupportedMakesNoCall(t *testing.T) {
	a, hits := fxAdapter(t, frankfurterFixture, http.StatusOK)

	rates, unsupported, err := a.Rates(context.Background(), "EUR", []string{"VND", "ARS"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rates) != 0 || len(unsupported) != 2 {
		t.Errorf("rates %v unsupported %v", rates, unsupported)
	}
	if *hits != 0 {
		t.Errorf("expected no upstream call, got %d", *hits)
	}
}

func TestFX_UnsupportedBaseIsAnError(t *testing.T) {
	a, _ := fxAdapter(t, frankfurterFixture, http.StatusOK)

	if _, _, err := a.Rates(context.Background(), "VND", []string{"USD"}); err == nil {
		t.Fatal("expected an error for a base the ECB does not publish")
	}
}

func TestFX_DefaultsToEURAndSkipsTheBaseAsAQuote(t *testing.T) {
	a, _ := fxAdapter(t, frankfurterFixture, http.StatusOK)

	rates, _, err := a.Rates(context.Background(), "", []string{"eur", "USD", "usd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range rates {
		if r.Base != "EUR" {
			t.Errorf("base: got %q, want EUR", r.Base)
		}
		if r.Quote == "EUR" {
			t.Error("the base must not be quoted against itself")
		}
	}
}

// The ECB publishes once a working day, so re-fetching is spending a rate limit
// on a constant. The key must not depend on the caller's argument order.
func TestFX_CachesRegardlessOfQuoteOrder(t *testing.T) {
	a, hits := fxAdapter(t, frankfurterFixture, http.StatusOK)
	ctx := context.Background()

	if _, _, err := a.Rates(ctx, "EUR", []string{"USD", "GBP", "JPY"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err := a.Rates(ctx, "EUR", []string{"JPY", "USD", "GBP"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *hits != 1 {
		t.Errorf("expected 1 upstream call for the same set, got %d", *hits)
	}
}

func TestFX_UpstreamFailureIsAnError(t *testing.T) {
	a, _ := fxAdapter(t, `{"message":"not found"}`, http.StatusNotFound)

	if _, _, err := a.Rates(context.Background(), "EUR", []string{"USD"}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestFX_EmptyRatesIsAnError(t *testing.T) {
	a, _ := fxAdapter(t, `{"base":"EUR","date":"2026-08-27","rates":{}}`, http.StatusOK)

	if _, _, err := a.Rates(context.Background(), "EUR", []string{"USD"}); err == nil {
		t.Fatal("expected an error rather than a silent empty result")
	}
}

// --- country → currency ----------------------------------------------------

func TestCurrencyForCountry(t *testing.T) {
	cases := map[string]string{
		"PT": "EUR", "pt": "EUR", " DE ": "EUR", "ME": "EUR",
		"GB": "GBP", "US": "USD", "JP": "JPY", "CH": "CHF",
		"EC": "USD", "GL": "DKK", "LI": "CHF",
		// Not published by the ECB, so deliberately unmapped: naming a currency
		// we cannot price helps nobody.
		"VN": "", "AR": "", "EG": "", "": "",
	}
	for country, want := range cases {
		if got := CurrencyForCountry(country); got != want {
			t.Errorf("%q: got %q, want %q", country, got, want)
		}
	}
}

// Every currency the map points at must be one we can actually fetch.
func TestCountryCurrencyMapOnlyUsesSupportedCurrencies(t *testing.T) {
	for country, currency := range countryCurrency {
		if !SupportedCurrency(currency) {
			t.Errorf("%s maps to %s, which the ECB does not publish", country, currency)
		}
	}
}

func TestSupportedCurrency(t *testing.T) {
	if !SupportedCurrency("usd") || !SupportedCurrency(" EUR ") {
		t.Error("should normalise case and whitespace")
	}
	if SupportedCurrency("XYZ") || SupportedCurrency("") {
		t.Error("should reject unknown codes")
	}
}

// --- drive cost ------------------------------------------------------------

func TestEstimateDriveCost(t *testing.T) {
	got := EstimateDriveCost(DriveCostInput{
		DistanceKm: 313, LitresPer100Km: 6.5, PricePerLitre: 1.75, Currency: "EUR",
	})
	// 313 km * 6.5/100 = 20.345 L; * 1.75 = 35.60 EUR
	if got.Litres != 20.35 {
		t.Errorf("litres: got %v, want 20.35", got.Litres)
	}
	if got.Cost != 35.6 {
		t.Errorf("cost: got %v, want 35.60", got.Cost)
	}
	if got.Currency != "EUR" {
		t.Errorf("currency: got %q", got.Currency)
	}
}

// A bare figure about someone's money invites misplaced trust or dismissal;
// only the assumptions let a user correct it against their own car.
func TestEstimateDriveCost_AlwaysStatesItsAssumptions(t *testing.T) {
	got := EstimateDriveCost(DriveCostInput{DistanceKm: 100})
	if got.Assumptions == "" {
		t.Fatal("an estimate must state what it rests on")
	}
	for _, want := range []string{"6.5", "1.75", "Fuel only", "tolls"} {
		if !strings.Contains(got.Assumptions, want) {
			t.Errorf("assumptions should mention %q, got %q", want, got.Assumptions)
		}
	}
}

func TestEstimateDriveCost_FallsBackOnMissingInputs(t *testing.T) {
	got := EstimateDriveCost(DriveCostInput{DistanceKm: 100})
	if got.Currency != "EUR" {
		t.Errorf("currency should default to EUR, got %q", got.Currency)
	}
	if got.Cost <= 0 {
		t.Errorf("a 100km drive should cost something, got %v", got.Cost)
	}

	// Zero and negative consumption or price must not produce a free or
	// negative trip.
	for _, in := range []DriveCostInput{
		{DistanceKm: 100, LitresPer100Km: 0, PricePerLitre: 0},
		{DistanceKm: 100, LitresPer100Km: -5, PricePerLitre: -2},
	} {
		if c := EstimateDriveCost(in); c.Cost <= 0 {
			t.Errorf("expected a positive cost from %+v, got %v", in, c.Cost)
		}
	}
}

func TestEstimateDriveCost_NegativeDistanceIsZero(t *testing.T) {
	got := EstimateDriveCost(DriveCostInput{DistanceKm: -50})
	if got.DistanceKm != 0 || got.Cost != 0 {
		t.Errorf("a negative distance must cost nothing, got %+v", got)
	}
}
