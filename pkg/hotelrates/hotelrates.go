// Package hotelrates fetches live nightly prices for hotels Loci recommends.
//
// The hotels themselves come from the LLM; a Provider matches each one to a
// partner hotel by name and coordinates and prices the requested stay. Prices
// are fetched live and only held in a short TTL cache: they go stale within
// minutes, and partners' terms forbid storing them.
package hotelrates

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
)

// ErrUnavailable wraps provider failures (network, 5xx, bad payload), so
// callers can tell "partner is down" from "no price for this hotel".
var ErrUnavailable = errors.New("hotel rates unavailable")

// Query is one hotel to price.
type Query struct {
	// Key is the caller's id for the hotel, echoed back on its Rate.
	Key         string
	Name        string
	Lat, Lon    float64
	City        string
	CountryCode string
}

// Stay is what is being priced. Dates are calendar dates (UTC midnight).
type Stay struct {
	Checkin  time.Time
	Checkout time.Time
	Adults   int
	Currency string
}

// Nights is the number of nights between checkin and checkout.
func (s Stay) Nights() int {
	return int(math.Round(s.Checkout.Sub(s.Checkin).Hours() / 24))
}

// Status says whether a hotel got a price.
type Status int

const (
	StatusUnmatched Status = iota
	StatusPriced
	StatusSoldOut
)

// Rate is the cheapest bookable rate for one queried hotel.
type Rate struct {
	Key          string
	Status       Status
	NightlyMinor int64
	TotalMinor   int64
	Currency     string
	Provider     string
	BookingURL   string
	FetchedAt    time.Time
}

// Provider prices a batch of hotels for one stay. It returns one Rate per
// query, in query order. A hotel it cannot match with confidence is
// StatusUnmatched, never a guess.
type Provider interface {
	Rates(ctx context.Context, hotels []Query, stay Stay) ([]Rate, error)
}

// ToMinor converts a decimal amount to the currency's minor units.
func ToMinor(amount float64, currency string) int64 {
	return int64(math.Round(amount * math.Pow10(CurrencyExponent(currency))))
}

// CurrencyExponent is the number of minor-unit digits ISO 4217 gives a
// currency. Most have two; the exceptions Loci users are likely to see are
// listed.
func CurrencyExponent(currency string) int {
	switch strings.ToUpper(currency) {
	case "JPY", "KRW", "VND", "CLP", "ISK", "PYG", "UGX", "XAF", "XOF", "IDR":
		return 0
	case "BHD", "KWD", "OMR", "JOD", "TND", "LYD", "IQD":
		return 3
	default:
		return 2
	}
}

// Unmatched builds an all-unmatched result, for when pricing is off.
func Unmatched(hotels []Query) []Rate {
	out := make([]Rate, len(hotels))
	for i, h := range hotels {
		out[i] = Rate{Key: h.Key, Status: StatusUnmatched}
	}
	return out
}
