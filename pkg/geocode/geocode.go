// Package geocode turns a typed place name into candidate coordinates.
//
// It exists because the server had no forward geocoder at all. The only
// geocoding here was BigDataCloud's reverse lookup (coordinates -> country),
// and every feature that needed "the user typed a city, where is it?" answered
// it by searching the `cities` table — a table only ever written as a byproduct
// of an LLM chat turn. A city nobody had chatted about simply did not exist, and
// /compare returned "origin city not found: Porto" for Portugal's second city.
//
// Deliberately provider-shaped and domain-free, like pkg/geo and pkg/httpx: it
// knows nothing about the cities table, and the city domain owns the rules for
// what to do with a result.
package geocode

import (
	"context"
	"errors"
)

// Place is one candidate location from a provider.
type Place struct {
	// Name is the provider's canonical spelling, which is what should be
	// persisted — never the user's typing, or "Prto" becomes a row.
	Name        string
	Country     string
	CountryCode string // ISO-3166-1 alpha-2, e.g. "PT"
	// Admin1 is the first-level subdivision, mapped to state_province.
	Admin1     string
	Lat, Lon   float64
	Population int
	// FeatureCode is the GeoNames class, e.g. "PPLA" for a first-order
	// administrative capital. Callers filter on it to drop the rivers,
	// regions and airports that share a city's name.
	FeatureCode string
}

// Forward resolves a place name to candidate coordinates.
//
// A narrow interface so consumers depend on "something that can find a place",
// not on Open-Meteo. A nil Forward is a supported configuration everywhere it is
// consumed, meaning "no geocoding available; use the database only".
type Forward interface {
	Search(ctx context.Context, name string, limit int) ([]Place, error)
}

// ErrUnavailable means the provider failed, not that the name was wrong.
//
// The distinction is the whole point of this sentinel: a caller that maps a
// provider outage to InvalidArgument tells the user to fix input that was never
// the problem, and hides the outage from every dashboard that counts 5xx.
var ErrUnavailable = errors.New("geocoder unavailable")
