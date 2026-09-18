package placeintel

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"
)

// Canonical claim vocabulary.
//
// Corroboration in SubmitPlaceClaim is an exact `LOWER(value) = LOWER(value)`
// match across two distinct users. Free prose therefore never corroborates:
// two scouts describing the same opening hours as "9am-5pm" and "09:00–17:00"
// produce two claims that never meet, so place_facts stays empty and the whole
// contribution loop is inert. The fix is to make every answer a token drawn
// from a fixed vocabulary, normalised to one spelling before it is stored or
// compared.
//
// This table is mirrored on the client in
// loci-client/src/lib/place-facts/vocabulary.ts. The two must be changed
// together — a token that exists on only one side is a claim that can never be
// submitted, or one that is rejected after the user has typed it.

// fieldKind says how a field's value is built, which decides how it normalises.
type fieldKind int

const (
	// kindSingle is one token from the vocabulary.
	kindSingle fieldKind = iota
	// kindMulti is a set of tokens, serialised sorted and comma-joined so that
	// two scouts who pick the same set produce a byte-identical string.
	kindMulti
	// kindStructured is a canonical serialised form validated by pattern, not
	// by a token list.
	kindStructured
)

type vocabulary struct {
	kind   fieldKind
	tokens []string
}

var claimVocabulary = map[placev1.PlaceFactField]vocabulary{
	placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL: {
		kind:   kindSingle,
		tokens: []string{"quiet", "moderate", "busy", "packed"},
	},
	placev1.PlaceFactField_PLACE_FACT_FIELD_NOISE_LEVEL: {
		kind:   kindSingle,
		tokens: []string{"quiet", "conversational", "lively", "loud"},
	},
	placev1.PlaceFactField_PLACE_FACT_FIELD_PRICE_LEVEL: {
		kind:   kindSingle,
		tokens: []string{"budget", "moderate", "pricey", "splurge"},
	},
	placev1.PlaceFactField_PLACE_FACT_FIELD_CHILD_FRIENDLY: {
		kind:   kindSingle,
		tokens: []string{"yes", "limited", "no"},
	},
	placev1.PlaceFactField_PLACE_FACT_FIELD_DOG_FRIENDLY: {
		kind:   kindSingle,
		tokens: []string{"yes", "outdoor_only", "no"},
	},
	placev1.PlaceFactField_PLACE_FACT_FIELD_DIETARY: {
		kind:   kindMulti,
		tokens: []string{"vegetarian", "vegan", "gluten_free", "halal", "kosher", "none"},
	},
	placev1.PlaceFactField_PLACE_FACT_FIELD_ACCESSIBILITY: {
		kind:   kindMulti,
		tokens: []string{"step_free", "accessible_wc", "lift", "wide_doors", "tactile", "none"},
	},
	placev1.PlaceFactField_PLACE_FACT_FIELD_VIBE: {
		kind:   kindMulti,
		tokens: []string{"cosy", "lively", "romantic", "touristy", "local", "quiet", "work_friendly", "outdoorsy"},
	},
	placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS: {kind: kindStructured},
}

// contributableFields is every field a scout can be asked about, in the order
// they are offered. Derived from the vocabulary so the two cannot drift.
var contributableFields = []placev1.PlaceFactField{
	placev1.PlaceFactField_PLACE_FACT_FIELD_OPENING_HOURS,
	placev1.PlaceFactField_PLACE_FACT_FIELD_PRICE_LEVEL,
	placev1.PlaceFactField_PLACE_FACT_FIELD_ACCESSIBILITY,
	placev1.PlaceFactField_PLACE_FACT_FIELD_DIETARY,
	placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL,
	placev1.PlaceFactField_PLACE_FACT_FIELD_NOISE_LEVEL,
	placev1.PlaceFactField_PLACE_FACT_FIELD_CHILD_FRIENDLY,
	placev1.PlaceFactField_PLACE_FACT_FIELD_DOG_FRIENDLY,
	placev1.PlaceFactField_PLACE_FACT_FIELD_VIBE,
}

// dayOrder lets opening-hours segments be sorted into one canonical order, so
// "sat 10:00-14:00; mon-fri 09:00-17:00" and the same two segments the other
// way round compare equal.
var dayOrder = map[string]int{
	"mon": 0, "tue": 1, "wed": 2, "thu": 3, "fri": 4, "sat": 5, "sun": 6,
}

const (
	dayPattern = `(?:mon|tue|wed|thu|fri|sat|sun)`
	// 24:00 is allowed as a closing time so "open until midnight" does not have
	// to be told as 23:59.
	timePattern  = `(?:(?:[01]\d|2[0-3]):[0-5]\d|24:00)`
	rangePattern = timePattern + `-` + timePattern
)

// openingHoursSegment matches one canonical segment, e.g. "mon-fri 09:00-17:00",
// "sat 10:00-14:00,19:00-23:00" or "sun closed".
var openingHoursSegment = regexp.MustCompile(
	`^` + dayPattern + `(?:-` + dayPattern + `)? (?:closed|` + rangePattern + `(?:,` + rangePattern + `)*)$`,
)

var whitespace = regexp.MustCompile(`\s+`)

// errEmptyValue is returned for a claim with nothing in it.
var errEmptyValue = errors.New("claim value is empty")

// normalizeValue turns a submitted claim value into the one spelling that gets
// stored and compared. Anything outside the vocabulary is an error rather than
// a silently-stored variant, because a variant is indistinguishable from a fact
// that simply never corroborates.
func normalizeValue(field placev1.PlaceFactField, raw string) (string, error) {
	value := strings.ToLower(whitespace.ReplaceAllString(strings.TrimSpace(raw), " "))
	if value == "" {
		return "", errEmptyValue
	}

	vocab, ok := claimVocabulary[field]
	if !ok {
		return "", fmt.Errorf("field %s cannot be contributed", field.String())
	}

	switch vocab.kind {
	case kindSingle:
		if !contains(vocab.tokens, value) {
			return "", fmt.Errorf("%q is not a valid %s value", value, fieldName(field))
		}
		return value, nil

	case kindMulti:
		parts := strings.Split(value, ",")
		seen := make(map[string]struct{}, len(parts))
		tokens := make([]string, 0, len(parts))
		for _, part := range parts {
			token := strings.TrimSpace(part)
			if token == "" {
				continue
			}
			if !contains(vocab.tokens, token) {
				return "", fmt.Errorf("%q is not a valid %s value", token, fieldName(field))
			}
			if _, duplicate := seen[token]; duplicate {
				continue
			}
			seen[token] = struct{}{}
			tokens = append(tokens, token)
		}
		if len(tokens) == 0 {
			return "", errEmptyValue
		}
		// "none" is a statement that the set is empty, so it cannot be combined
		// with the very things it denies.
		if len(tokens) > 1 && contains(tokens, "none") {
			return "", fmt.Errorf("%s cannot be both \"none\" and something else", fieldName(field))
		}
		sort.Strings(tokens)
		return strings.Join(tokens, ","), nil

	case kindStructured:
		return normalizeOpeningHours(value)
	}

	return "", fmt.Errorf("field %s cannot be contributed", field.String())
}

// normalizeOpeningHours validates and canonically orders an opening-hours
// string such as "mon-fri 09:00-17:00; sat 10:00-14:00; sun closed".
func normalizeOpeningHours(value string) (string, error) {
	rawSegments := strings.Split(value, ";")
	segments := make([]string, 0, len(rawSegments))
	for _, rawSegment := range rawSegments {
		segment := whitespace.ReplaceAllString(strings.TrimSpace(rawSegment), " ")
		segment = strings.ReplaceAll(segment, ", ", ",")
		if segment == "" {
			continue
		}
		if !openingHoursSegment.MatchString(segment) {
			return "", fmt.Errorf("%q is not canonical opening hours (expected e.g. \"mon-fri 09:00-17:00\")", segment)
		}
		segments = append(segments, segment)
	}
	if len(segments) == 0 {
		return "", errEmptyValue
	}
	sort.SliceStable(segments, func(i, j int) bool {
		return dayOrder[segments[i][:3]] < dayOrder[segments[j][:3]]
	})
	return strings.Join(segments, "; "), nil
}

func contains(tokens []string, value string) bool {
	for _, token := range tokens {
		if token == value {
			return true
		}
	}
	return false
}
