package chatbridge

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestPageTokenRoundTrips(t *testing.T) {
	sessionID := uuid.New()
	for _, section := range []locitypes.SessionPOISection{
		locitypes.SectionItinerary, locitypes.SectionGeneral,
		locitypes.SectionRestaurants, locitypes.SectionHotels,
		locitypes.SectionActivities,
	} {
		for _, page := range []int{1, 2, 9, 99, 999} {
			token := encodePage(sessionID, page, section)

			gotID, gotPage, gotSection, ok := decodePage(token)
			if !ok {
				t.Fatalf("%q did not decode", token)
			}
			if gotID != sessionID || gotPage != page || gotSection != section {
				t.Errorf("%q decoded to %s/%d/%s, want %s/%d/%s",
					token, gotID, gotPage, gotSection, sessionID, page, section)
			}
		}
	}
}

// Telegram rejects a payload over 64 bytes, so an oversized token is a failed
// send rather than a dead button. The worst case has to fit with room to spare.
func TestPageTokenFitsTelegramsLimit(t *testing.T) {
	token := encodePage(uuid.Max, 999, locitypes.SectionRestaurants)
	if len(token) > 64 {
		t.Fatalf("token %q is %d bytes, over Telegram's 64", token, len(token))
	}
	t.Logf("worst case is %d bytes: %q", len(token), token)
}

// A malformed token must be rejected, never quietly treated as page one — a
// default would be page one of whatever session id happened to parse.
func TestMalformedTokensAreRejected(t *testing.T) {
	valid := encodePage(uuid.New(), 2, locitypes.SectionItinerary)

	for _, token := range []string{
		"",
		"p",
		"p|not-a-uuid|2|i",
		"p|" + strings.Repeat("0", 32) + "|2|i", // the nil UUID names no session
		"p|" + strings.ReplaceAll(uuid.New().String(), "-", "") + "|0|i",
		"p|" + strings.ReplaceAll(uuid.New().String(), "-", "") + "|-1|i",
		"p|" + strings.ReplaceAll(uuid.New().String(), "-", "") + "|two|i",
		"p|" + strings.ReplaceAll(uuid.New().String(), "-", "") + "|2|z",
		"x|" + strings.ReplaceAll(uuid.New().String(), "-", "") + "|2|i",
		valid + "|extra",
		strings.TrimSuffix(valid, "|i"),
	} {
		if _, page, _, ok := decodePage(token); ok {
			t.Errorf("%q was accepted as page %d", token, page)
		}
	}
}
