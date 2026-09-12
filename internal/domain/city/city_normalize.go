package city

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// foldName reduces a city name to a comparable key: "Évora" -> "evora".
//
// This is needed because the database compares names with LOWER(), and
// LOWER('Evora') never equals 'Évora'. The trigram leg usually rescues it —
// similarity is around 0.5, comfortably over the 0.3 floor — but "usually" is
// not a design, and an origin city that resolves on most days and not others is
// worse than one that never does.
//
// Folding happens in Go, over the small candidate list the trigram index
// returns, rather than in an unaccented generated column: a table this size does
// not justify a migration, and the geocoder is given the *unfolded* name because
// its own index handles both spellings.
func foldName(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, s)
	if err != nil {
		// Transform only fails on malformed input; the un-folded name is still
		// a usable key, just a stricter one.
		folded = s
	}
	return strings.ToLower(strings.TrimSpace(folded))
}
