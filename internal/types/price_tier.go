package locitypes

import (
	"strings"
	"unicode"
)

// PriceTier reads a restaurant's price band, 1 ($) to 4 ($$$$), from the
// free-text price fields the model fills ("$$", "€€€", "moderate",
// "$$-$$$"). It returns 0 when the text says nothing usable. Deriving it at
// the edge, rather than asking the model for another field, means results
// already stored and cached get a tier too.
func PriceTier(fields ...string) int32 {
	for _, f := range fields {
		if t := priceTier(f); t > 0 {
			return t
		}
	}
	return 0
}

func priceTier(s string) int32 {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0
	}
	// A range ("$$-$$$", "$$ – $$$") is read by its lower end, the price a
	// diner can count on.
	if i := strings.IndexAny(s, "-–/"); i > 0 {
		s = strings.TrimSpace(s[:i])
	}

	symbols, other := 0, 0
	for _, r := range s {
		switch {
		case strings.ContainsRune("$€£¥₩₹", r):
			symbols++
		case unicode.IsSpace(r):
		default:
			other++
		}
	}
	if symbols > 0 && other == 0 {
		return int32(min(symbols, 4))
	}
	if len(s) == 1 && s[0] >= '1' && s[0] <= '4' {
		return int32(s[0] - '0')
	}

	switch {
	case strings.Contains(s, "very expensive"), strings.Contains(s, "luxury"), strings.Contains(s, "splurge"), strings.Contains(s, "fine dining"):
		return 4
	case strings.Contains(s, "inexpensive"), strings.Contains(s, "budget"), strings.Contains(s, "cheap"):
		return 1
	case strings.Contains(s, "expensive"), strings.Contains(s, "upscale"), strings.Contains(s, "pricey"):
		return 3
	case strings.Contains(s, "moderate"), strings.Contains(s, "mid"):
		return 2
	}
	return 0
}
