package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/hotelcompare"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// HotelComparer answers a message that has hotel listing links in it
// (*hotelcompare.Service).
type HotelComparer interface {
	Compare(ctx context.Context, req hotelcompare.Request, progress func(string)) (*hotelcompare.Report, error)
}

// StayChoiceProposer offers a report's hotels as the stay on a trip
// (*tripaction.Service).
type StayChoiceProposer interface {
	ProposeStayChoice(ctx context.Context, userID, tripID, sessionID uuid.UUID, city, reportID string, stays []trip.TripStay) (*tripaction.Proposal, error)
}

// SetHotelCompare lets a message with hotel links in it be answered with a
// comparison report. stays may be nil: the report then carries no card to
// add a hotel to a trip, and the client offers its own trip picker. A nil
// comparer (the default) answers every turn as before.
func (l *ServiceImpl) SetHotelCompare(c HotelComparer, stays StayChoiceProposer) {
	l.hotelCompare, l.stayChoices = c, stays
}

// compareBudget bounds a comparison: two redirects, a handful of lookups and
// two model calls, one of which may search the web.
const compareBudget = 80 * time.Second

// compareHotels answers a turn whose message has hotel listing links in it:
// progress, the report, the card that adds one of its hotels to a trip when
// one of the traveller's trips has the city, then completion. false means
// the turn is not a comparison and is answered as usual.
//
// Only a client that says it renders reports gets one, and a resumed turn
// is replayed from its buffer, not compared again.
func (l *ServiceImpl) compareHotels(cc common.ChatContext) bool {
	if l.hotelCompare == nil || !cc.HotelComparisonCapable || cc.ResumeToken != "" || cc.StopRun ||
		!hotelcompare.HasLinks(cc.Message) {
		return false
	}
	ctx, cancel := context.WithTimeout(cc.Ctx, compareBudget)
	defer cancel()

	req := hotelcompare.Request{UserID: cc.UserID, Text: cc.Message, CityHint: cc.CityName}
	if cc.TripID != uuid.Nil && l.tripRepo != nil {
		if t, err := l.tripRepo.GetTrip(ctx, cc.TripID, cc.UserID); err == nil {
			if req.CityHint == "" {
				req.CityHint = t.CityName
			}
			req.CheckIn, req.CheckOut = t.StartDate, t.EndDate
		}
	}

	rep, err := l.hotelCompare.Compare(ctx, req, func(stage string) {
		l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeProgress, Data: map[string]any{"status": stage},
		}, 3)
	})
	switch {
	case errors.Is(err, hotelcompare.ErrDailyLimit):
		l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeError, IsFinal: true,
			Error: "You've compared as many hotels as Loci can check in a day. Try again tomorrow.",
		}, 3)
		return true
	case err != nil:
		l.logger.WarnContext(cc.Ctx, "hotel compare: failed; answering as usual", slog.Any("error", err))
		return false
	}

	ev := hotelcompare.Event{Report: rep}
	var prop *tripaction.Proposal
	if l.stayChoices != nil && rep.City != "" {
		if tripID := hotelcompare.TripFor(ctx, l.tripRepo, cc.UserID, cc.TripID, rep.City, time.Now()); tripID != uuid.Nil {
			prop, err = l.stayChoices.ProposeStayChoice(ctx, cc.UserID, tripID, cc.RequestedSessionID, rep.City, rep.ID.String(), rep.Stays())
			if err != nil {
				// The report still stands; the client's own picker can save a stay.
				l.logger.WarnContext(cc.Ctx, "hotel compare: offering the stays failed", slog.Any("error", err))
				prop = nil
			}
		}
	}
	if prop != nil {
		ev.StayProposalID = prop.ID.String()
	}

	summary := rep.Verdict
	if summary == "" {
		summary = "Here is what I could check about these hotels."
	}
	l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeHotelComparison, Message: summary, Data: ev}, 3)
	if prop != nil {
		l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeActionProposal, Message: prop.Summary, Data: *prop}, 3)
	}
	l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeComplete, Data: "Turn completed.", IsFinal: true,
	}, 3)
	return true
}
