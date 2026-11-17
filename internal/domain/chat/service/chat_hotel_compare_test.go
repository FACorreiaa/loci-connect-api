package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/hotelcompare"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

const hotelLinksMessage = "what do you think about https://www.booking.com/Share-gpcrrRv 385 euros with breakfast vs https://www.booking.com/Share-6nDTCI 349 without breakfast"

type fakeComparer struct {
	rep *hotelcompare.Report
	err error
	got hotelcompare.Request
}

func (f *fakeComparer) Compare(_ context.Context, req hotelcompare.Request, progress func(string)) (*hotelcompare.Report, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	progress(hotelcompare.StageLinks)
	progress(hotelcompare.StageWrite)
	return f.rep, nil
}

type fakeStayChoices struct {
	tripID uuid.UUID
	city   string
	stays  []trip.TripStay
	err    error
}

func (f *fakeStayChoices) ProposeStayChoice(_ context.Context, _, tripID, _ uuid.UUID, city, _ string, stays []trip.TripStay) (*tripaction.Proposal, error) {
	f.tripID, f.city, f.stays = tripID, city, stays
	if f.err != nil {
		return nil, f.err
	}
	return &tripaction.Proposal{ID: uuid.New(), TripID: tripID, Summary: "Pick the one to stay at in " + city + "."}, nil
}

type fakeTripRepo struct {
	trip.Repository
	trips []*trip.Trip
}

func (f fakeTripRepo) GetTrip(_ context.Context, id, userID uuid.UUID) (*trip.Trip, error) {
	for _, t := range f.trips {
		if t.ID == id && t.UserID == userID {
			return t, nil
		}
	}
	return nil, trip.ErrNotFound
}

func (f fakeTripRepo) ListTrips(_ context.Context, userID uuid.UUID, _, _ int) ([]*trip.Trip, int, error) {
	var out []*trip.Trip
	for _, t := range f.trips {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, len(out), nil
}

func barcelonaReport() *hotelcompare.Report {
	return &hotelcompare.Report{
		ID: uuid.New(), City: "Barcelona", Verdict: "Take the first.",
		Hotels: []hotelcompare.Hotel{{Stay: trip.TripStay{Name: "Verdi 254"}}, {Stay: trip.TripStay{Name: "Hotel Casa Fuster"}}},
	}
}

func compareTurn(ch chan locitypes.StreamEvent, uid uuid.UUID) common.ChatContext {
	return common.ChatContext{Ctx: context.Background(), UserID: uid, Message: hotelLinksMessage, EventCh: ch, HotelComparisonCapable: true}
}

func neverPlans(t *testing.T, l *ServiceImpl) {
	t.Helper()
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) {
		t.Fatal("a turn with hotel links must be compared, not planned")
		return nil, nil
	}
}

func TestHotelLinks_ReportThenStayCardThenComplete(t *testing.T) {
	uid := uuid.New()
	mine := &trip.Trip{ID: uuid.New(), UserID: uid, CityName: "Barcelona"}
	l := newStreamService(t, &TestLLMClient{})
	l.tripRepo = fakeTripRepo{trips: []*trip.Trip{mine, {ID: uuid.New(), UserID: uid, CityName: "Lisbon"}}}
	neverPlans(t, l)
	cmp, stays := &fakeComparer{rep: barcelonaReport()}, &fakeStayChoices{}
	l.SetHotelCompare(cmp, stays)

	ch := make(chan locitypes.StreamEvent, 10)
	require.NoError(t, l.ProcessUnifiedChatMessageStream(compareTurn(ch, uid)))
	evs := drainEvents(ch)
	require.Len(t, evs, 6)
	// It opens like any turn, so a client navigates and files the answer.
	require.Equal(t, locitypes.EventTypeStart, evs[0].Type)
	start := evs[0].Data.(map[string]any)
	require.Equal(t, string(locitypes.DomainAccommodation), start["domain"])
	_, err := uuid.Parse(start["session_id"].(string))
	require.NoError(t, err)
	evs = evs[1:]
	require.Equal(t, locitypes.EventTypeProgress, evs[0].Type)
	require.Equal(t, map[string]any{"status": hotelcompare.StageLinks}, evs[0].Data)

	require.Equal(t, locitypes.EventTypeHotelComparison, evs[2].Type)
	ev := evs[2].Data.(hotelcompare.Event)
	require.Equal(t, "Take the first.", evs[2].Message)
	require.Len(t, ev.Report.Hotels, 2)

	require.Equal(t, locitypes.EventTypeActionProposal, evs[3].Type)
	prop := evs[3].Data.(tripaction.Proposal)
	require.Equal(t, prop.ID.String(), ev.StayProposalID, "the report names the card that follows it")
	require.Equal(t, mine.ID, stays.tripID, "the traveller's one Barcelona trip")
	require.Equal(t, "Barcelona", stays.city)
	require.Len(t, stays.stays, 2)

	require.Equal(t, locitypes.EventTypeComplete, evs[4].Type)
	require.True(t, evs[4].IsFinal)
	require.Equal(t, hotelLinksMessage, cmp.got.Text)
}

func TestHotelLinks_NoTripToOfferTheStayTo(t *testing.T) {
	uid := uuid.New()
	past := time.Now().AddDate(0, -1, 0)
	cases := map[string][]*trip.Trip{
		"no trip has the city": {{ID: uuid.New(), UserID: uid, CityName: "Lisbon"}},
		"two trips have it":    {{ID: uuid.New(), UserID: uid, CityName: "Barcelona"}, {ID: uuid.New(), UserID: uid, CityName: "barcelona"}},
		"the only one is over": {{ID: uuid.New(), UserID: uid, CityName: "Barcelona", EndDate: &past}},
		"it is someone else's": {{ID: uuid.New(), UserID: uuid.New(), CityName: "Barcelona"}},
	}
	for name, trips := range cases {
		t.Run(name, func(t *testing.T) {
			l := newStreamService(t, &TestLLMClient{})
			l.tripRepo = fakeTripRepo{trips: trips}
			neverPlans(t, l)
			stays := &fakeStayChoices{}
			l.SetHotelCompare(&fakeComparer{rep: barcelonaReport()}, stays)
			ch := make(chan locitypes.StreamEvent, 10)
			require.NoError(t, l.ProcessUnifiedChatMessageStream(compareTurn(ch, uid)))
			evs := drainEvents(ch)
			require.Len(t, evs, 5, "start, progress twice, the report, completion: no card")
			require.Empty(t, evs[3].Data.(hotelcompare.Event).StayProposalID)
			require.Equal(t, uuid.Nil, stays.tripID, "nothing was proposed")
		})
	}
}

func TestHotelLinks_BoundTripGivesTheCityAndDates(t *testing.T) {
	uid := uuid.New()
	start, end := time.Date(2027, 3, 4, 0, 0, 0, 0, time.UTC), time.Date(2027, 3, 8, 0, 0, 0, 0, time.UTC)
	bound := &trip.Trip{ID: uuid.New(), UserID: uid, CityName: "Barcelona", StartDate: &start, EndDate: &end}
	l := newStreamService(t, &TestLLMClient{})
	l.tripRepo = fakeTripRepo{trips: []*trip.Trip{bound, {ID: uuid.New(), UserID: uid, CityName: "Barcelona"}}}
	l.SetTripActions(&fakeProposer{out: []tripaction.Proposal{{Summary: "must not be asked"}}})
	neverPlans(t, l)
	cmp, stays := &fakeComparer{rep: barcelonaReport()}, &fakeStayChoices{}
	l.SetHotelCompare(cmp, stays)

	ch := make(chan locitypes.StreamEvent, 10)
	cc := compareTurn(ch, uid)
	cc.TripID = bound.ID
	require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
	evs := drainEvents(ch)
	require.Equal(t, locitypes.EventTypeHotelComparison, evs[3].Type, "compared, not turned into trip changes")
	require.Equal(t, "Barcelona", evs[0].Data.(map[string]any)["city"], "the trip's city, for a client that titles the page by it")
	require.Equal(t, "Barcelona", cmp.got.CityHint)
	require.Equal(t, &start, cmp.got.CheckIn)
	require.Equal(t, bound.ID, stays.tripID, "the trip the chat is about, though another has the city too")

	// A report about a city the bound trip does not have offers no card.
	other := barcelonaReport()
	other.City = "Madrid"
	l.SetHotelCompare(&fakeComparer{rep: other}, &fakeStayChoices{})
	ch = make(chan locitypes.StreamEvent, 10)
	cc.EventCh = ch
	require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
	require.Len(t, drainEvents(ch), 5)
}

func TestHotelLinks_AnsweredAsUsualWhenNotAComparison(t *testing.T) {
	uid := uuid.New()
	cases := map[string]func(cc *common.ChatContext, l *ServiceImpl){
		"an older client cannot render a report": func(cc *common.ChatContext, _ *ServiceImpl) { cc.HotelComparisonCapable = false },
		"no hotel link in the message": func(cc *common.ChatContext, _ *ServiceImpl) {
			cc.Message = "hotels in Barcelona near https://example.com"
		},
		"a resumed turn is replayed": func(cc *common.ChatContext, _ *ServiceImpl) { cc.ResumeToken = "tok" },
		"comparison is switched off": func(_ *common.ChatContext, l *ServiceImpl) { l.SetHotelCompare(nil, nil) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			l := newStreamService(t, &TestLLMClient{})
			l.SetHotelCompare(&fakeComparer{rep: barcelonaReport()}, &fakeStayChoices{})
			planned := false
			l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) {
				planned = true
				return &locitypes.AiCityResponse{}, nil
			}
			ch := make(chan locitypes.StreamEvent, 10)
			cc := compareTurn(ch, uid)
			cc.StopRun = false
			change(&cc, l)
			// StopRun keeps the unit test out of multi-city planning; it is
			// set after the comparison check has had its say.
			require.False(t, l.compareHotels(cc))
			cc.StopRun = true
			require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
			drainEvents(ch)
			require.True(t, planned)
		})
	}
}

func TestHotelLinks_DailyLimitSaysSoAndStops(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	neverPlans(t, l)
	l.SetHotelCompare(&fakeComparer{err: hotelcompare.ErrDailyLimit}, nil)
	ch := make(chan locitypes.StreamEvent, 10)
	require.NoError(t, l.ProcessUnifiedChatMessageStream(compareTurn(ch, uuid.New())))
	evs := drainEvents(ch)
	require.Len(t, evs, 2)
	require.Equal(t, locitypes.EventTypeStart, evs[0].Type)
	require.Equal(t, locitypes.EventTypeError, evs[1].Type)
	require.Contains(t, evs[1].Error, "Try again tomorrow")
}

// A turn that has started under its own session ends there: it is not handed
// to the planner half-way, which would open a second session on the client.
func TestHotelLinks_AFailedComparisonSaysSoAndStops(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	neverPlans(t, l)
	l.SetHotelCompare(&fakeComparer{err: errors.New("boom")}, nil)
	ch := make(chan locitypes.StreamEvent, 10)
	require.NoError(t, l.ProcessUnifiedChatMessageStream(compareTurn(ch, uuid.New())))
	evs := drainEvents(ch)
	require.Len(t, evs, 2)
	require.Equal(t, locitypes.EventTypeError, evs[1].Type)
	require.True(t, evs[1].IsFinal)
	require.NotContains(t, evs[1].Error, "boom", "an operator's error is not read to a traveller")
}

func TestHotelLinks_AFailedCardStillLeavesTheReport(t *testing.T) {
	uid := uuid.New()
	l := newStreamService(t, &TestLLMClient{})
	l.tripRepo = fakeTripRepo{trips: []*trip.Trip{{ID: uuid.New(), UserID: uid, CityName: "Barcelona"}}}
	neverPlans(t, l)
	l.SetHotelCompare(&fakeComparer{rep: barcelonaReport()}, &fakeStayChoices{err: errors.New("store down")})
	ch := make(chan locitypes.StreamEvent, 10)
	require.NoError(t, l.ProcessUnifiedChatMessageStream(compareTurn(ch, uid)))
	evs := drainEvents(ch)
	require.Len(t, evs, 5)
	require.Empty(t, evs[3].Data.(hotelcompare.Event).StayProposalID)
}
