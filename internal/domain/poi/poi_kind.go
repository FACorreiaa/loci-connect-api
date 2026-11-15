package poi

import (
	"regexp"
	"strings"
)

// poiKindSQL is what kind of place a row is. Generation writes it to one of
// two columns depending on which save path stored the row: category on some,
// poi_type on the rest, almost never both. Reading only category left most
// places in a freshly planned city with no kind at all.
const poiKindSQL = `COALESCE(NULLIF(btrim(category), ''), NULLIF(btrim(poi_type), ''), '')`

// categoryWordsPattern is the Postgres regular expression that matches a
// lowercased kind containing any of words as a whole word, with an optional
// plural: "bar" finds "tapas bar" and "wine bars" but not "barber" or
// "barrio". Empty when there are no words, which means no narrowing.
//
// Kinds are free text from the model ("Tapas Bar", "Historic Café"), so
// asking for an exact "bar" found nothing in a city full of them.
func categoryWordsPattern(words []string) string {
	var alts []string
	seen := map[string]bool{}
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		alts = append(alts, regexp.QuoteMeta(w))
	}
	if len(alts) == 0 {
		return ""
	}
	return `\m(` + strings.Join(alts, "|") + `)s?\M`
}
