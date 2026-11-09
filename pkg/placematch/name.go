// Package placematch decides whether two records name the same place. It is
// shared by the providers that match an LLM-written place to a dataset
// record (accessibility.cloud, hotel rates), where a wrong match is worse
// than none.
package placematch

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// NameSimilarity is the overlap of two names' word sets, ignoring case and
// accents, with containment counted as a match ("Pastéis de Belém" vs
// "Antiga Confeitaria de Belém - Pastéis de Belém"). Words in ignore are
// dropped from both sides first.
func NameSimilarity(a, b string, ignore ...map[string]bool) float64 {
	ta, tb := Tokens(a, ignore...), Tokens(b, ignore...)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for t := range ta {
		if tb[t] {
			inter++
		}
	}
	smaller := min(len(ta), len(tb))
	if inter == smaller {
		return 1
	}
	union := len(ta) + len(tb) - inter
	return float64(inter) / float64(union)
}

var stopWords = map[string]bool{"the": true, "de": true, "da": true, "do": true, "of": true, "la": true, "le": true, "el": true, "a": true, "o": true}

// Fold lowercases s and strips its accents. The transformer is built per
// call: a transform.Chain keeps state between calls and is not safe to share
// across goroutines, and callers match places concurrently.
func Fold(s string) string {
	foldAccents := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(foldAccents, strings.ToLower(s))
	if err != nil {
		return strings.ToLower(s)
	}
	return folded
}

// Tokens is the set of a name's folded words, without stop words or any word
// in ignore.
func Tokens(s string, ignore ...map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(Fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if stopWords[w] {
			continue
		}
		skip := false
		for _, ig := range ignore {
			if ig[w] {
				skip = true
				break
			}
		}
		if !skip {
			out[w] = true
		}
	}
	return out
}
