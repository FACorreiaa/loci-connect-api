// Package forge generates, reviews and publishes City Packs offline.
//
// Nothing here runs inside a request. Generation costs real model calls and is
// deliberately outside the metered RPC path, and publishing is a human
// decision, so the whole pipeline lives in a binary somebody runs on purpose.
package forge

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed seeds.json
var seedsJSON []byte

// Themes is the taste vocabulary a pack can be cut for. It is fixed: the
// catalog filters on it and the client mirrors it, so adding one means
// changing both.
var Themes = []string{
	"food", "art", "outdoors", "architecture", "nightlife", "family", "local_life",
}

// DayWindow is when a pack's stops may start, in minutes from midnight.
//
// Nightlife gets the evening. Asking every theme for 09:00 to 22:00 made the
// model squeeze a night out into office hours: a bar at 09:00 and a club "until
// 3 AM" slotted at 13:45. The window ends at 23:59 rather than past midnight
// because the client prints minutes as HH:MM and 1500 would read as 25:00; a
// late stop's duration carries the night on from there.
func DayWindow(theme string) (start, end int) {
	if theme == "nightlife" {
		return 14 * 60, 23*60 + 59
	}
	return 9 * 60, 22 * 60
}

// clock renders minutes from midnight as HH:MM.
func clock(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// ValidTheme reports whether a theme is in the vocabulary.
func ValidTheme(t string) bool {
	for _, v := range Themes {
		if v == t {
			return true
		}
	}
	return false
}

// Seed is one cell of the generation matrix: a city, when it is worth going,
// and what the pack is about.
//
// The list is ported from SEASONAL_PICKS in the client, which already decided
// which cities are worth showing and in which months, plus a set of the most
// visited cities outside Europe. Themes were assigned by hand from each hook.
// Correcting one is an edit here, and the review step before publishing is
// where a wrong one should be caught.
type Seed struct {
	City        string `json:"city"`
	CountryCode string `json:"country_code"`
	Months      []int  `json:"months"`
	Hook        string `json:"hook"`
	Days        int    `json:"days"`
	Theme       string `json:"theme"`
}

// slugStopWords are dropped from a hook so the URL reads as a name rather
// than a sentence.
var slugStopWords = map[string]bool{"the": true, "a": true, "an": true, "of": true, "for": true}

// transliterate maps the accented characters the city list actually contains
// onto ascii, so a slug is safe in a URL and a filename.
var transliterate = strings.NewReplacer(
	"ø", "o", "å", "a", "ä", "a", "ã", "a", "á", "a", "à", "a",
	"ü", "u", "ú", "u", "ö", "o", "ó", "o", "ô", "o", "õ", "o",
	"é", "e", "è", "e", "ê", "e", "í", "i", "ç", "c", "ñ", "n", "ß", "ss",
)

// slugify lowercases, transliterates and hyphenates, dropping anything that is
// not a letter, a digit or a separator.
func slugify(s string) string {
	s = transliterate.Replace(strings.ToLower(strings.TrimSpace(s)))

	var b strings.Builder
	prevDash := true // leading dashes are suppressed
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Slug is the pack's stable address: the city, what the trip is for, and how
// long it runs.
//
// The hook rather than the theme carries the identity. Lisbon in spring and
// Lisbon under the jacarandas are the same city, the same theme and the same
// length, but they are two different products — keying on the theme gave them
// one slug, and the second would have failed the unique constraint on write.
func (s Seed) Slug() string {
	parts := []string{slugify(s.City)}
	for _, w := range strings.Fields(slugify(s.Hook)) {
		for _, token := range strings.Split(w, "-") {
			if token != "" && !slugStopWords[token] {
				parts = append(parts, token)
			}
		}
	}
	return fmt.Sprintf("%s-%dday", strings.Join(parts, "-"), s.Days)
}

// Title reads the way the card should.
func (s Seed) Title() string {
	return fmt.Sprintf("%d days in %s for %s", s.Days, s.City, s.Hook)
}

// LoadSeeds returns the embedded matrix.
func LoadSeeds() ([]Seed, error) {
	var seeds []Seed
	if err := json.Unmarshal(seedsJSON, &seeds); err != nil {
		return nil, fmt.Errorf("parse seeds: %w", err)
	}
	for i, s := range seeds {
		if s.City == "" || len(s.Months) == 0 {
			return nil, fmt.Errorf("seed %d is incomplete", i)
		}
		if !ValidTheme(s.Theme) {
			return nil, fmt.Errorf("seed %d (%s) has unknown theme %q", i, s.City, s.Theme)
		}
		if s.Days <= 0 {
			seeds[i].Days = 3
		}
	}
	return seeds, nil
}

// Filter narrows the matrix by city and theme.
func Filter(seeds []Seed, city, theme string) []Seed {
	if city == "" && theme == "" {
		return seeds
	}
	out := []Seed{}
	for _, s := range seeds {
		if city != "" && !strings.EqualFold(s.City, city) {
			continue
		}
		if theme != "" && s.Theme != theme {
			continue
		}
		out = append(out, s)
	}
	return out
}
