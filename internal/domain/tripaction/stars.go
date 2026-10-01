package tripaction

import (
	"math"
	"strconv"
	"strings"
)

// starsOf reads a hotel's star rating as stored: "4", "4.5", "4 stars",
// "4★" or "★★★★". A number wins over glyphs ("4★" is four stars, not one);
// glyphs are counted only when there is no number. ok is false when it says
// nothing usable.
func starsOf(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.') {
		end++
	}
	if end == 0 {
		if n := strings.Count(s, "★"); n > 0 {
			return float64(n), n <= 5
		}
		return 0, false
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	if err != nil || v <= 0 || v > 5 {
		return 0, false
	}
	return v, true
}

// starsWithin keeps a hotel whose whole stars fall in lo..hi. A 4.5 is a
// four-star. 0..5 is no filter at all, and keeps hotels with no rating; any
// narrower range drops them, since nobody asking for four stars wants a guess.
func starsWithin(rating string, lo, hi int) bool {
	if lo == 0 && hi == 5 {
		return true
	}
	v, ok := starsOf(rating)
	if !ok {
		return false
	}
	whole := int(math.Floor(v))
	return whole >= lo && whole <= hi
}
