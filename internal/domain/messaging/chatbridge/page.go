package chatbridge

import (
	"strconv"
	"strings"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// A page token is the whole of a "More" button's state.
//
// Telegram hands back at most 64 bytes with a press, which is enough for an
// index into something stored and not enough for anything else — no query, no
// city, no list of names. That constraint is the design: the token says which
// session, which page and which list, and everything else is read back out of
// the answer the session already stored.
//
// The token lives here rather than in the telegram package because it is the
// renderer that knows what it means. The platform adapter only enforces its own
// byte limit, and so stays as ignorant of sessions as it is of itineraries.
//
// Format: "p|<32 hex>|<page>|<section>" — 40 bytes at a three-digit page.
const (
	pageTokenPrefix = "p"
	pageTokenSep    = "|"
)

// sectionCodes map a section to the single byte the token can afford.
var sectionCodes = map[locitypes.SessionPOISection]string{
	locitypes.SectionItinerary:   "i",
	locitypes.SectionGeneral:     "g",
	locitypes.SectionRestaurants: "r",
	locitypes.SectionHotels:      "h",
	locitypes.SectionActivities:  "a",
}

var sectionsByCode = func() map[string]locitypes.SessionPOISection {
	out := make(map[string]locitypes.SessionPOISection, len(sectionCodes))
	for section, code := range sectionCodes {
		out[code] = section
	}
	return out
}()

// encodePage builds the token for the next page.
func encodePage(sessionID uuid.UUID, page int, section locitypes.SessionPOISection) string {
	code, ok := sectionCodes[section]
	if !ok {
		code = sectionCodes[locitypes.SectionItinerary]
	}
	return strings.Join([]string{
		pageTokenPrefix,
		strings.ReplaceAll(sessionID.String(), "-", ""),
		strconv.Itoa(page),
		code,
	}, pageTokenSep)
}

// decodePage reads a token back.
//
// The token only ever comes from a keyboard this bot sent, but it is treated as
// untrusted anyway: a malformed one is rejected rather than defaulted to page
// one, because a default would be page one of whatever session id happened to
// parse. The session it names is still ownership-checked downstream, which is
// what makes carrying a session id in a user-visible payload safe at all.
func decodePage(token string) (uuid.UUID, int, locitypes.SessionPOISection, bool) {
	parts := strings.Split(token, pageTokenSep)
	if len(parts) != 4 || parts[0] != pageTokenPrefix {
		return uuid.Nil, 0, "", false
	}

	sessionID, err := uuid.Parse(parts[1])
	if err != nil || sessionID == uuid.Nil {
		return uuid.Nil, 0, "", false
	}

	page, err := strconv.Atoi(parts[2])
	if err != nil || page < 1 {
		return uuid.Nil, 0, "", false
	}

	section, ok := sectionsByCode[parts[3]]
	if !ok {
		return uuid.Nil, 0, "", false
	}
	return sessionID, page, section, true
}
