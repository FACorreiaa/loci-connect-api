package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/hotelcompare"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
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

	// The turn opens like any other: a session of its own (or the one the
	// client is continuing) and a START naming it, which is what a client
	// navigates on and files the answer under.
	sessionID := l.compareSession(ctx, cc)
	l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeStart,
		Data: map[string]any{
			// The run is filed as a comparison so a finish nobody watched
			// links to this thread (runs.ResultPath); clients still read
			// ACCOMMODATION, as DomainToProto maps it.
			"domain": runs.DomainHotelComparison, "city": req.CityHint, "session_id": sessionID.String(),
		},
	}, 3)

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
		// The turn has started under its own session, so it ends here rather
		// than being handed to the planner half-way.
		l.logger.WarnContext(cc.Ctx, "hotel compare: failed", slog.Any("error", err))
		l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
			Type: locitypes.EventTypeError, IsFinal: true,
			Error: "I couldn't compare those hotels just now. Try again in a moment.",
		}, 3)
		return true
	}

	ev := hotelcompare.Event{Report: rep}
	var prop *tripaction.Proposal
	if l.stayChoices != nil && rep.City != "" {
		if tripID := hotelcompare.TripFor(ctx, l.tripRepo, cc.UserID, cc.TripID, rep.City, time.Now()); tripID != uuid.Nil {
			prop, err = l.stayChoices.ProposeStayChoice(ctx, cc.UserID, tripID, sessionID, rep.City, rep.ID.String(), rep.Stays())
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
	l.remember(ctx, sessionID, summary)
	l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeHotelComparison, Message: summary, Data: ev}, 3)
	if prop != nil {
		l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeActionProposal, Message: prop.Summary, Data: *prop}, 3)
	}
	navURL, routeType, query := runs.ResultPath(runs.DomainHotelComparison, sessionID, req.CityHint, uuid.Nil)
	l.sendEvent(cc.Ctx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeComplete, Data: "Turn completed.", IsFinal: true,
		Navigation: &locitypes.NavigationData{URL: navURL, RouteType: routeType, QueryParams: query},
	}, 3)
	return true
}

// compareSession is the session a comparison turn is filed under: the one
// the client is continuing when it is theirs, else a new one. The thread
// keeps the question and the verdict; the report card itself is not stored
// there (a client keeps the one it was sent).
//
// A session that cannot be stored still gets an id, so the turn is answered:
// losing the thread is better than losing the answer.
func (l *ServiceImpl) compareSession(ctx context.Context, cc common.ChatContext) uuid.UUID {
	question := locitypes.ConversationMessage{Role: "user", Content: cc.Message, Timestamp: time.Now()}
	if l.llmInteractionRepo == nil {
		return uuid.New()
	}
	if cc.RequestedSessionID != uuid.Nil {
		if existing, err := l.llmInteractionRepo.GetSession(ctx, cc.RequestedSessionID); err == nil && existing != nil && existing.UserID == cc.UserID {
			if err := l.llmInteractionRepo.AddMessageToSession(ctx, existing.ID, question); err != nil {
				l.logger.WarnContext(ctx, "hotel compare: question not added to the session", slog.Any("error", err))
			}
			return existing.ID
		}
	}
	id := uuid.New()
	now := time.Now()
	err := l.llmInteractionRepo.CreateSession(ctx, locitypes.ChatSession{
		ID: id, UserID: cc.UserID, ProfileID: cc.ProfileID, CityName: cc.CityName,
		ConversationHistory: []locitypes.ConversationMessage{question},
		SessionContext:      locitypes.SessionContext{CityName: cc.CityName, ConversationSummary: "Hotel comparison"},
		CreatedAt:           now, UpdatedAt: now, ExpiresAt: now.Add(24 * time.Hour), Status: "active",
	})
	if err != nil {
		l.logger.WarnContext(ctx, "hotel compare: session not stored", slog.Any("error", err))
	}
	return id
}

// remember adds the verdict to the turn's thread. Best effort.
func (l *ServiceImpl) remember(ctx context.Context, sessionID uuid.UUID, text string) {
	if l.llmInteractionRepo == nil {
		return
	}
	msg := locitypes.ConversationMessage{
		ID: uuid.New(), Role: locitypes.RoleAssistant, Content: text, MessageType: locitypes.TypeResponse, Timestamp: time.Now(),
	}
	if err := l.llmInteractionRepo.AddMessageToSession(ctx, sessionID, msg); err != nil {
		l.logger.WarnContext(ctx, "hotel compare: verdict not added to the session", slog.Any("error", err))
	}
}
