package assistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	assistantv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/hotelcompare"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
)

const pastedHotels = "what do you think about https://www.booking.com/Share-gpcrrRv 385 euros with breakfast vs https://www.booking.com/Share-6nDTCI 349 without breakfast"

type fakeHotels struct {
	rep *hotelcompare.Report
	err error
	got hotelcompare.Request
}

func (f *fakeHotels) Compare(_ context.Context, req hotelcompare.Request, _ func(string)) (*hotelcompare.Report, error) {
	f.got = req
	return f.rep, f.err
}

type fakeStays struct {
	tripID uuid.UUID
	err    error
}

func (f *fakeStays) ProposeStayChoice(_ context.Context, _, tripID, _ uuid.UUID, city, reportID string, stays []trip.TripStay) (*tripaction.Proposal, error) {
	f.tripID = tripID
	if f.err != nil {
		return nil, f.err
	}
	p := &tripaction.Proposal{
		ID: uuid.New(), TripID: tripID, Summary: "Pick the one to stay at in " + city + ".",
		Action: tripaction.Action{Kind: tripaction.KindChooseStay, City: city, ReportID: reportID},
	}
	for i := range stays {
		p.Options = append(p.Options, tripaction.Option{Label: stays[i].Name, Stay: &stays[i]})
	}
	return p, nil
}

type ownTrips struct{ trips []*trip.Trip }

func (o ownTrips) GetTrip(_ context.Context, id, userID uuid.UUID) (*trip.Trip, error) {
	for _, t := range o.trips {
		if t.ID == id && t.UserID == userID {
			return t, nil
		}
	}
	return nil, trip.ErrNotFound
}

func (o ownTrips) ListTrips(_ context.Context, userID uuid.UUID, _, _ int) ([]*trip.Trip, int, error) {
	var out []*trip.Trip
	for _, t := range o.trips {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, len(out), nil
}

func twoHotels(verdict string) *hotelcompare.Report {
	return &hotelcompare.Report{
		ID: uuid.New(), City: "Barcelona", Verdict: verdict, GeneratedAt: time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC),
		Hotels: []hotelcompare.Hotel{{Stay: trip.TripStay{Name: "Verdi 254"}}, {Stay: trip.TripStay{Name: "Hotel Casa Fuster"}}},
	}
}

type hotelFixture struct {
	*fixture
	hotels *fakeHotels
	stays  *fakeStays
}

// newHotelFixture is a service whose model must never be called: a pasted
// link is answered by the comparison, not by the tool loop.
func newHotelFixture(t *testing.T, rep *hotelcompare.Report, trips ...*trip.Trip) *hotelFixture {
	t.Helper()
	f := newFixture(t)
	for _, tr := range trips {
		tr.UserID = f.user
	}
	h := &hotelFixture{fixture: f, hotels: &fakeHotels{rep: rep}, stays: &fakeStays{}}
	f.svc = NewService(Deps{
		Model: f.model, Fallback: f.fall, Store: f.store,
		Sources: Sources{Trips: f.trips, Members: f.trips},
		Now:     func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
		Hotels:  h.hotels, StayChoices: h.stays, Trips: ownTrips{trips: trips},
	})
	return h
}

// events asks and returns every event in order.
func (h *hotelFixture) events(t *testing.T, req *assistantv1.AskAssistantRequest) []*assistantv1.AssistantEvent {
	t.Helper()
	var evs []*assistantv1.AssistantEvent
	require.NoError(t, h.svc.Ask(context.Background(), h.user, req, func(e *assistantv1.AssistantEvent) error {
		evs = append(evs, e)
		return nil
	}))
	return evs
}

func TestHotels_PastedLinksGetAReportACardAndAShortAnswer(t *testing.T) {
	barcelona := &trip.Trip{ID: uuid.New(), CityName: "Barcelona"}
	h := newHotelFixture(t, twoHotels("Take the first: breakfast is worth more than the difference."), barcelona)
	evs := h.events(t, question(pastedHotels))

	require.Len(t, evs, 5)
	require.Equal(t, "Comparing those hotels", evs[0].GetToolStatus().GetLabel())

	rep := evs[1].GetHotelReport()
	require.NotNil(t, rep)
	require.Len(t, rep.GetHotels(), 2)

	card := evs[2].GetProposal().GetTrip()
	require.NotNil(t, card)
	require.Equal(t, card.GetId(), rep.GetStayProposalId(), "the report names the card that follows it")
	require.Equal(t, "Barcelona", card.GetAction().GetChooseStay().GetCityName())
	require.Len(t, card.GetOptions(), 2)
	require.Equal(t, barcelona.ID, h.stays.tripID)

	require.Equal(t,
		"Take the first: breakfast is worth more than the difference. The details are on the card. Tap the one you want there to add it to your trip.",
		evs[3].GetTextDelta().GetText())
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_COMPLETE, evs[4].GetDone().GetReason())
	require.NotEmpty(t, evs[4].GetDone().GetConversationId(), "the exchange is remembered like any other")

	require.Empty(t, h.model.reqs, "the model's tool loop never sees a pasted link")
	require.Equal(t, pastedHotels, h.hotels.got.Text)
}

func TestHotels_NoTripWithTheCityMeansNoCardAndNoPromiseOfOne(t *testing.T) {
	h := newHotelFixture(t, twoHotels(""), &trip.Trip{ID: uuid.New(), CityName: "Lisbon"})
	evs := h.events(t, question(pastedHotels))
	require.Len(t, evs, 4)
	require.Empty(t, evs[1].GetHotelReport().GetStayProposalId())
	require.Equal(t, hotelNoVerdict, evs[2].GetTextDelta().GetText(), "no verdict: it says so, and promises no card to tap")
	require.Equal(t, uuid.Nil, h.stays.tripID)
}

func TestHotels_TheTripOnScreenIsTheOneOffered(t *testing.T) {
	start, end := time.Date(2027, 3, 4, 0, 0, 0, 0, time.UTC), time.Date(2027, 3, 8, 0, 0, 0, 0, time.UTC)
	shown := &trip.Trip{ID: uuid.New(), CityName: "Barcelona", StartDate: &start, EndDate: &end}
	other := &trip.Trip{ID: uuid.New(), CityName: "Barcelona"}
	h := newHotelFixture(t, twoHotels("Either."), shown, other)
	req := question(pastedHotels)
	req.Context = &assistantv1.ClientContext{Entities: []*assistantv1.ContextEntity{
		{Type: assistantv1.ContextEntityType_CONTEXT_ENTITY_TYPE_TRIP, Id: shown.ID.String()},
	}}
	evs := h.events(t, req)
	require.NotNil(t, evs[2].GetProposal(), "two trips have the city, but the one on screen settles it")
	require.Equal(t, shown.ID, h.stays.tripID)
	require.Equal(t, "Barcelona", h.hotels.got.CityHint)
	require.Equal(t, &start, h.hotels.got.CheckIn)

	// A trip named by the client that is not the caller's is not read.
	stranger := &trip.Trip{ID: uuid.New(), CityName: "Barcelona"}
	h2 := newHotelFixture(t, twoHotels("Either."))
	h2.svc.d.Trips = ownTrips{trips: []*trip.Trip{{ID: stranger.ID, UserID: uuid.New(), CityName: "Barcelona"}}}
	req.Context.Entities[0].Id = stranger.ID.String()
	evs = h2.events(t, req)
	require.Len(t, evs, 4, "no card")
	require.Empty(t, h2.hotels.got.CityHint)
}

func TestHotels_FallsBackToAnOrdinaryAnswer(t *testing.T) {
	// The comparison failed: the ask is answered by the model as usual.
	h := newHotelFixture(t, nil)
	h.hotels.err = errors.New("boom")
	h.model.turns = []scriptedTurn{{text: "I can't open links.", stop: "end_turn"}}
	r := h.ask(t, question(pastedHotels))
	require.Equal(t, "I can't open links.", r.text)
	require.Len(t, h.model.reqs, 1)

	// No link in the message: the comparison is never asked.
	h = newHotelFixture(t, twoHotels("x"))
	h.model.turns = []scriptedTurn{{text: "Gràcia is lovely.", stop: "end_turn"}}
	r = h.ask(t, question("where should I stay in Barcelona?"))
	require.Equal(t, "Gràcia is lovely.", r.text)
	require.Empty(t, h.hotels.got.Text)
}

func TestHotels_DailyLimitIsSaidPlainly(t *testing.T) {
	h := newHotelFixture(t, nil)
	h.hotels.err = hotelcompare.ErrDailyLimit
	r := h.ask(t, question(pastedHotels))
	require.Equal(t, hotelDailyLimit, r.text)
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_COMPLETE, r.done.GetReason())
	require.Empty(t, h.model.reqs)
}

func TestHotels_AnAskThatComparesGetsLongerThanAnAnswer(t *testing.T) {
	h := newHotelFixture(t, twoHotels("x"))
	require.True(t, h.svc.comparesHotels(question(pastedHotels)))
	require.False(t, h.svc.comparesHotels(question("hello")))
	require.Greater(t, hotelCompareBudget, h.svc.d.Limits.Ask)

	off := newFixture(t)
	require.False(t, off.svc.comparesHotels(question(pastedHotels)), "not configured: answered as any other message")
}
