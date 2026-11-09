package flights

import (
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeResolver map[string]string

func (f fakeResolver) ResolveName(name string) (string, bool) {
	code, ok := f[name]
	return code, ok
}

var namesOnly = Query{
	Origin: Place{Name: "Lisbon"}, Destination: Place{Name: "Barcelona"},
	Depart: date("2026-12-04"), Passengers: 1,
}

// The zero value is pinned by TestDeepLinks' golden URLs; this checks the
// constructor with nothing configured gives the same thing.
func TestZeroValueUnchanged(t *testing.T) {
	require.Equal(t, DeepLinks{}.Links(namesOnly), NewDeepLinks(nil, "", slog.Default()).Links(namesOnly))
}

func TestResolverFillsSkyscannerOnly(t *testing.T) {
	links := DeepLinks{Resolver: fakeResolver{"Lisbon": "LIS", "Barcelona": "BCN"}}.Links(namesOnly)
	require.Len(t, links, 2)
	require.Equal(t, "google_flights", links[0].Provider)
	require.Contains(t, links[0].URL, "Flights+to+Barcelona+from+Lisbon")
	require.Equal(t, "skyscanner", links[1].Provider)
	require.Contains(t, links[1].URL, "/lis/bcn/261204/")
	require.False(t, links[1].Sponsored)
}

func TestResolverKeepsSentIATA(t *testing.T) {
	q := namesOnly
	q.Origin.IATA = "OPO"
	links := DeepLinks{Resolver: fakeResolver{"Lisbon": "LIS", "Barcelona": "BCN"}}.Links(q)
	require.Contains(t, links[1].URL, "/opo/bcn/")
}

func TestResolverMissLeavesGoogleOnly(t *testing.T) {
	links := DeepLinks{Resolver: fakeResolver{"Lisbon": "LIS"}}.Links(namesOnly)
	require.Len(t, links, 1)
	require.Equal(t, "google_flights", links[0].Provider)
}

func TestTemplateWrapsSkyscanner(t *testing.T) {
	plain := DeepLinks{Resolver: fakeResolver{"Lisbon": "LIS", "Barcelona": "BCN"}}.Links(namesOnly)[1].URL
	d := NewDeepLinks(fakeResolver{"Lisbon": "LIS", "Barcelona": "BCN"}, "https://aff.example/r?u={url}", slog.Default())
	links := d.Links(namesOnly)
	require.Equal(t, "https://aff.example/r?u="+url.QueryEscape(plain), links[1].URL)
	require.True(t, links[1].Sponsored)
	require.False(t, links[0].Sponsored)
	require.False(t, strings.Contains(links[0].URL, "aff.example"))
}

func TestInvalidTemplateIgnored(t *testing.T) {
	require.False(t, ValidTemplate("https://aff.example/r"))
	require.False(t, ValidTemplate("http://aff.example/r?u={url}"))
	require.True(t, ValidTemplate("https://aff.example/r?u={url}"))

	d := NewDeepLinks(fakeResolver{"Lisbon": "LIS", "Barcelona": "BCN"}, "https://aff.example/r", slog.Default())
	links := d.Links(namesOnly)
	require.True(t, strings.HasPrefix(links[1].URL, "https://www.skyscanner.net/"))
	require.False(t, links[1].Sponsored)
}
