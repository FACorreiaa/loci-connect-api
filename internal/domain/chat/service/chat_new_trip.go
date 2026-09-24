package service

import (
	"context"
	"log/slog"
	"strings"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// newTripCity reports whether a follow-up in an open session is really a
// request for a different city, and which one.
//
// A chat platform has no "new conversation" button: the Telegram bridge
// continues the most recent session with every message, on purpose, so that
// "make day two quieter" lands on the trip it refers to. The cost of that
// default was that "itinerary in warsaw" typed into a Lisbon session was
// classified as an edit to Lisbon and answered with a request to specify the
// change. Naming another city is the one signal that unambiguously means a
// new trip, and the same extractor the first message went through is the
// judge, so the two paths agree on what counts as a city.
//
// Extraction is cached on the message text, so the new-trip flow this hands
// off to pays nothing extra when it runs the same extraction again.
func (l *ServiceImpl) newTripCity(ctx context.Context, session *locitypes.ChatSession, message string) (string, bool) {
	extracted, _, err := l.extractCityCached(ctx, message)
	if err != nil {
		// Not knowing is not a reason to fail the turn: the session continues
		// as it always did.
		l.logger.WarnContext(ctx, "could not read a city out of the follow-up; continuing the session",
			slog.Any("error", err))
		return "", false
	}
	if !startsNewTrip(sessionCityName(session), extracted, session.CurrentItinerary) {
		return "", false
	}
	return strings.TrimSpace(extracted), true
}

// sessionCityName is the city a session is about. SessionContext carries it
// for sessions the stream created; the column on the session is the fallback.
func sessionCityName(session *locitypes.ChatSession) string {
	if session == nil {
		return ""
	}
	if name := strings.TrimSpace(session.SessionContext.CityName); name != "" {
		return name
	}
	return strings.TrimSpace(session.CityName)
}

// startsNewTrip decides whether a city read out of a follow-up message means
// the traveller has moved on from the session's city.
//
// Same city, in any spelling or with a country appended, continues. A name
// that is already a place in the plan continues too: the extractor is a text
// parser and will call a neighbourhood a city, and "replace Alfama with Belém"
// is an edit to Lisbon, not a trip to Belém. A session without a city cannot
// be continued at all — its city lookup fails — so any named city starts.
func startsNewTrip(sessionCity, extracted string, itinerary *locitypes.AiCityResponse) bool {
	extracted = strings.ToLower(strings.TrimSpace(extracted))
	if extracted == "" {
		return false
	}
	sessionCity = strings.ToLower(strings.TrimSpace(sessionCity))
	if sessionCity == "" {
		return true
	}
	if sessionCity == extracted || strings.Contains(sessionCity, extracted) || strings.Contains(extracted, sessionCity) {
		return false
	}
	if itinerary != nil {
		for _, poi := range itinerary.AIItineraryResponse.PointsOfInterest {
			if name := strings.ToLower(poi.Name); name != "" && strings.Contains(name, extracted) {
				return false
			}
		}
		for _, poi := range itinerary.PointsOfInterest {
			if name := strings.ToLower(poi.Name); name != "" && strings.Contains(name, extracted) {
				return false
			}
		}
	}
	return true
}
