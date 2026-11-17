package assistant

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	assistantv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/hotelcompare"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
)

// hotelCompareBudget bounds an ask that compares hotels: redirects, lookups
// and two model calls, one of which may search the web.
const hotelCompareBudget = 80 * time.Second

// What the assistant says around a comparison. Written here, never by a
// model: the report's verdict is the only model-written text read aloud, and
// it was checked and flattened before it got here.
const (
	hotelStatus     = "Comparing those hotels"
	hotelNoVerdict  = "I couldn't settle it, but the card shows what I could check about each one."
	hotelSeeCard    = " The details are on the card."
	hotelPickOnCard = " Tap the one you want there to add it to your trip."
	hotelDailyLimit = "I've compared as many hotels as I can check in a day. Try again tomorrow."
)

type (
	// HotelComparer answers a message with hotel listing links in it
	// (*hotelcompare.Service).
	HotelComparer interface {
		Compare(ctx context.Context, req hotelcompare.Request, progress func(string)) (*hotelcompare.Report, error)
	}
	// StayChoiceProposer offers a report's hotels as the stay on a trip
	// (*tripaction.Service). The card it stores is confirmed like any other
	// trip card, through ConfirmAction.
	StayChoiceProposer interface {
		ProposeStayChoice(ctx context.Context, userID, tripID, sessionID uuid.UUID, city, reportID string, stays []trip.TripStay) (*tripaction.Proposal, error)
	}
)

func (s *Service) comparesHotels(req *assistantv1.AskAssistantRequest) bool {
	return s.d.Hotels != nil && hotelcompare.HasLinks(req.GetText())
}

// compareHotels answers an ask that pastes hotel links. It is not a tool the
// model may call: the links are followed and the report written by the
// comparison service, and the assistant only says the verdict and shows the
// cards. That keeps a pasted link from ever being something the model is
// asked to open, and keeps the model from naming a hotel to save.
//
// false means the ask is not a comparison, or comparing failed, and it is
// answered as usual.
func (a *askRun) compareHotels() (outcome, bool) {
	s := a.s
	if !s.comparesHotels(a.req) {
		return outcome{}, false
	}
	a.sendStatus(hotelStatus)
	if a.gone != nil {
		return outcome{gone: a.gone}, true
	}

	shown := a.shownTrip()
	req := hotelcompare.Request{UserID: a.userID, Text: a.req.GetText(), CityHint: a.req.GetContext().GetCity()}
	if shown != nil {
		if req.CityHint == "" {
			req.CityHint = shown.CityName
		}
		req.CheckIn, req.CheckOut = shown.StartDate, shown.EndDate
	}
	rep, err := s.d.Hotels.Compare(a.ctx, req, nil)
	switch {
	case errors.Is(err, hotelcompare.ErrDailyLimit):
		a.sendText(hotelDailyLimit)
		return a.said1(), true
	case err != nil:
		s.d.Logger.Warn("assistant: hotel comparison failed; answering as usual", slog.Any("error", err))
		return outcome{}, false
	}

	var prop *tripaction.Proposal
	if s.d.StayChoices != nil && s.d.Trips != nil {
		preferred := uuid.Nil
		if shown != nil {
			preferred = shown.ID
		}
		if tripID := hotelcompare.TripFor(a.ctx, s.d.Trips, a.userID, preferred, rep.City, a.now); tripID != uuid.Nil {
			prop, err = s.d.StayChoices.ProposeStayChoice(a.ctx, a.userID, tripID, uuid.Nil, rep.City, rep.ID.String(), rep.Stays())
			if err != nil {
				s.d.Logger.Warn("assistant: offering the compared hotels failed", slog.Any("error", err))
				prop = nil
			}
		}
	}
	proposalID := ""
	if prop != nil {
		proposalID = prop.ID.String()
	}

	a.send(&assistantv1.AssistantEvent{Event: &assistantv1.AssistantEvent_HotelReport{
		HotelReport: hotelcompare.ToProto(rep, proposalID),
	}})
	if prop != nil {
		a.offered++
		a.send(&assistantv1.AssistantEvent{Event: &assistantv1.AssistantEvent_Proposal{Proposal: &assistantv1.Proposal{
			Kind: &assistantv1.Proposal_Trip{Trip: tripaction.ToProto(prop)},
		}}})
	}

	said := rep.Verdict
	if said == "" {
		said = hotelNoVerdict
	} else {
		said += hotelSeeCard
	}
	if prop != nil {
		said += hotelPickOnCard
	}
	a.sendText(said)
	return a.said1(), true
}

// said1 is the outcome of an ask answered without the model loop.
func (a *askRun) said1() outcome {
	if a.gone != nil {
		return outcome{gone: a.gone}
	}
	return outcome{reason: assistantv1.DoneReason_DONE_REASON_COMPLETE}
}

func (a *askRun) send(e *assistantv1.AssistantEvent) {
	if a.gone == nil {
		a.gone = a.emit(e)
	}
}

// shownTrip is the caller's own trip on the screen they asked from, if any.
// Only a trip they own: a stay is the owner's to set.
func (a *askRun) shownTrip() *trip.Trip {
	if a.s.d.Trips == nil {
		return nil
	}
	for _, e := range a.req.GetContext().GetEntities() {
		if e.GetType() != assistantv1.ContextEntityType_CONTEXT_ENTITY_TYPE_TRIP {
			continue
		}
		id, err := uuid.Parse(e.GetId())
		if err != nil {
			continue
		}
		if t, err := a.s.d.Trips.GetTrip(a.ctx, id, a.userID); err == nil {
			return t
		}
	}
	return nil
}
