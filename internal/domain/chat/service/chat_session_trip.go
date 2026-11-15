package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// SetSessionTrips attaches the lookup that stops a follow-up turn from saving
// a second trip for a session that already has one.
func (l *ServiceImpl) SetSessionTrips(s trip.SessionTrips) { l.sessionTrips = s }

// sessionTripToKeep is the id of the trip this turn should leave alone
// instead of saving a new one, or uuid.Nil when it should save as usual.
//
// Every generation used to save a trip. A follow-up typed under an itinerary
// resumes the same session and regenerates, so each follow-up left another
// trip behind with the same source session, and the traveller's edits on the
// first were on a trip nothing pointed at any more.
//
// Only a resumed session can already have a trip. A trip for a different city
// is not this turn's trip: "now do Warsaw" in a Lisbon session still gets its
// own. A turn already bound to a trip (or already told not to save) is not
// looked up at all. A lookup failure falls back to saving: a duplicate trip
// is the lesser harm than a generation with no trip.
func (l *ServiceImpl) sessionTripToKeep(ctx context.Context, cc common.ChatContext) uuid.UUID {
	if l.sessionTrips == nil || cc.SuppressTripSave || cc.TripID != uuid.Nil {
		return uuid.Nil
	}
	if cc.RequestedSessionID == uuid.Nil || cc.SessionID != cc.RequestedSessionID {
		return uuid.Nil
	}
	existing, err := l.sessionTrips.LatestForSession(ctx, cc.UserID, cc.SessionID)
	if err != nil {
		if !errors.Is(err, trip.ErrNotFound) {
			l.logger.WarnContext(ctx, "session trip lookup failed; saving a new trip",
				slog.String("session_id", cc.SessionID.String()), slog.Any("error", err))
		}
		return uuid.Nil
	}
	if existing == nil || !sameCity(existing.CityName, cc.CityName) {
		return uuid.Nil
	}
	return existing.ID
}

func sameCity(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
