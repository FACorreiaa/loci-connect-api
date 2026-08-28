package localcontext

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

const frankfurterBaseURL = "https://api.frankfurter.dev"

// SourceFX names the provider in metrics and on the wire.
const SourceFX = "frankfurter"

// FxRate is one currency pair at a point in time.
type FxRate struct {
	Base  string    `json:"base"`
	Quote string    `json:"quote"`
	Rate  float64   `json:"rate"`
	AsOf  time.Time `json:"as_of"`
}

// FXAdapter fetches reference exchange rates.
//
// Not a SignalSource: an exchange rate is not an alert. It never penalises the
// go-score and never appears in the alert list — it is context a traveller
// reads, not a warning.
//
// Backed by Frankfurter, which republishes the ECB reference rates: keyless,
// fast (~0.2s), and exact rather than modelled. The catch is coverage — the ECB
// publishes 30 currencies, so anywhere else returns no rate at all. That is the
// right failure: a made-up rate about someone's money is worse than none.
type FXAdapter struct {
	baseURL string
	client  *httpx.Client

	cache *signalCache
}

// NewFXAdapter builds the adapter. An empty baseURL uses the public endpoint.
func NewFXAdapter(baseURL string, client *httpx.Client, cache *signalCache) *FXAdapter {
	if baseURL == "" {
		baseURL = frankfurterBaseURL
	}
	return &FXAdapter{baseURL: baseURL, client: client, cache: cache}
}

type frankfurterResponse struct {
	Base  string             `json:"base"`
	Date  string             `json:"date"`
	Rates map[string]float64 `json:"rates"`
}

// Rates returns the rate from base into each quote currency.
//
// Quotes the ECB does not publish are returned in `unsupported` rather than
// silently dropped: a client showing "no rate available for X" is honest, and a
// client showing nothing at all looks like a bug.
func (a *FXAdapter) Rates(ctx context.Context, base string, quotes []string) (rates []FxRate, unsupported []string, err error) {
	base = normaliseCurrency(base)
	if base == "" {
		base = "EUR"
	}
	if !SupportedCurrency(base) {
		return nil, nil, fmt.Errorf("fx: base currency %q is not published by the ECB", base)
	}

	wanted := make([]string, 0, len(quotes))
	seen := map[string]bool{base: true}
	for _, q := range quotes {
		q = normaliseCurrency(q)
		if q == "" || seen[q] {
			continue
		}
		seen[q] = true
		if !SupportedCurrency(q) {
			unsupported = append(unsupported, q)
			continue
		}
		wanted = append(wanted, q)
	}
	if len(wanted) == 0 {
		return nil, unsupported, nil
	}
	// Sorted so the cache key is stable regardless of the caller's ordering.
	sort.Strings(wanted)

	key := base + ">" + strings.Join(wanted, ",")

	if cached, ok := cacheGet[[]FxRate](a.cache, SourceFX, key); ok {
		return cached, unsupported, nil
	}

	q := url.Values{}
	q.Set("base", base)
	q.Set("symbols", strings.Join(wanted, ","))

	endpoint := a.baseURL + "/v1/latest?" + q.Encode()
	body, err := httpx.GetJSON[frankfurterResponse](ctx, a.client, SourceFX, endpoint)
	if err != nil {
		return nil, unsupported, err
	}
	if len(body.Rates) == 0 {
		return nil, unsupported, fmt.Errorf("fx: no rates in response for %s", key)
	}

	asOf, _ := time.Parse("2006-01-02", body.Date)
	out := make([]FxRate, 0, len(body.Rates))
	for _, quote := range wanted {
		rate, ok := body.Rates[quote]
		if !ok {
			unsupported = append(unsupported, quote)
			continue
		}
		out = append(out, FxRate{Base: base, Quote: quote, Rate: rate, AsOf: asOf})
	}

	cacheSet(a.cache, SourceFX, key, out, ttlFx)
	return out, unsupported, nil
}

func normaliseCurrency(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// ecbCurrencies is what Frankfurter republishes from the ECB. Asking for
// anything else returns 404, so this is checked before the call rather than
// after.
var ecbCurrencies = map[string]bool{
	"AUD": true, "BRL": true, "CAD": true, "CHF": true, "CNY": true,
	"CZK": true, "DKK": true, "EUR": true, "GBP": true, "HKD": true,
	"HUF": true, "IDR": true, "ILS": true, "INR": true, "ISK": true,
	"JPY": true, "KRW": true, "MXN": true, "MYR": true, "NOK": true,
	"NZD": true, "PHP": true, "PLN": true, "RON": true, "SEK": true,
	"SGD": true, "THB": true, "TRY": true, "USD": true, "ZAR": true,
}

// SupportedCurrency reports whether the ECB publishes a rate for this currency.
func SupportedCurrency(code string) bool {
	return ecbCurrencies[normaliseCurrency(code)]
}

// countryCurrency maps ISO-3166 alpha-2 to ISO-4217.
//
// Deliberately covers only countries whose currency the ECB actually publishes,
// because a country we can name a currency for but cannot price is no more
// useful than one we cannot name. Anywhere else resolves to "" and the caller
// simply shows no rate.
var countryCurrency = map[string]string{
	// Eurozone, plus the microstates and territories that use the euro.
	"AT": "EUR", "BE": "EUR", "HR": "EUR", "CY": "EUR", "EE": "EUR",
	"FI": "EUR", "FR": "EUR", "DE": "EUR", "GR": "EUR", "IE": "EUR",
	"IT": "EUR", "LV": "EUR", "LT": "EUR", "LU": "EUR", "MT": "EUR",
	"NL": "EUR", "PT": "EUR", "SK": "EUR", "SI": "EUR", "ES": "EUR",
	"AD": "EUR", "MC": "EUR", "SM": "EUR", "VA": "EUR", "ME": "EUR", "XK": "EUR",

	"US": "USD", "EC": "USD", "SV": "USD", "PA": "USD", "TL": "USD",
	"PR": "USD", "GU": "USD", "VI": "USD",

	"GB": "GBP",
	"AU": "AUD", "NR": "AUD", "TV": "AUD", "KI": "AUD",
	"NZ": "NZD", "CK": "NZD", "NU": "NZD", "TK": "NZD",
	"CH": "CHF", "LI": "CHF",
	"DK": "DKK", "GL": "DKK", "FO": "DKK",
	"NO": "NOK", "SJ": "NOK",
	"ZA": "ZAR",

	"BR": "BRL", "CA": "CAD", "CN": "CNY", "CZ": "CZK", "HK": "HKD",
	"HU": "HUF", "ID": "IDR", "IL": "ILS", "IN": "INR", "IS": "ISK",
	"JP": "JPY", "KR": "KRW", "MX": "MXN", "MY": "MYR", "PH": "PHP",
	"PL": "PLN", "RO": "RON", "SE": "SEK", "SG": "SGD", "TH": "THB",
	"TR": "TRY",
}

// CurrencyForCountry returns the ISO-4217 code for a country, or "" when we
// cannot price it.
func CurrencyForCountry(countryCode string) string {
	return countryCurrency[strings.ToUpper(strings.TrimSpace(countryCode))]
}

// --- drive cost ------------------------------------------------------------

// DriveCost is a fuel estimate for one leg, with the assumptions attached.
type DriveCost struct {
	DistanceKm float64 `json:"distance_km"`
	Litres     float64 `json:"litres"`
	Cost       float64 `json:"cost"`
	Currency   string  `json:"currency"`
	// Assumptions states what the number rests on, in words.
	//
	// The same principle as the go-score and the packing suggestions: a bare
	// figure about someone's money invites either misplaced trust or dismissal,
	// and only the assumptions let a user correct it against their own car.
	Assumptions string `json:"assumptions"`
}

// DriveCostInput is what the estimate is computed from.
type DriveCostInput struct {
	DistanceKm     float64
	LitresPer100Km float64
	PricePerLitre  float64
	Currency       string
}

// EstimateDriveCost prices a drive leg.
//
// A pure function over already-gathered inputs — no I/O, no clock, no env reads
// — for the same reason Score is: the arithmetic should be identical everywhere
// it is shown and testable without a network.
func EstimateDriveCost(in DriveCostInput) DriveCost {
	if in.DistanceKm < 0 {
		in.DistanceKm = 0
	}
	if in.LitresPer100Km <= 0 {
		in.LitresPer100Km = defaultLitresPer100Km
	}
	if in.PricePerLitre <= 0 {
		in.PricePerLitre = defaultPricePerLitre
	}
	currency := normaliseCurrency(in.Currency)
	if currency == "" {
		currency = "EUR"
	}

	litres := in.DistanceKm * in.LitresPer100Km / 100
	cost := litres * in.PricePerLitre

	assumptions := fmt.Sprintf(assumptionsFmt, in.LitresPer100Km, in.PricePerLitre, currency)

	return DriveCost{
		DistanceKm:  round2(in.DistanceKm),
		Litres:      round2(litres),
		Cost:        round2(cost),
		Currency:    currency,
		Assumptions: assumptions,
	}
}

const (
	defaultLitresPer100Km = 6.5
	defaultPricePerLitre  = 1.75

	// assumptionsFmt is hoisted so the estimate can never be built without one.
	assumptionsFmt = "Fuel only, assuming %.1f L/100km and %.2f %s per litre. " +
		"Excludes tolls, parking and any hire cost."
)

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
