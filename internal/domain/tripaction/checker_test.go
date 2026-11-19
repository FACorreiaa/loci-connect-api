package tripaction

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// The memory store keeps the checker's offers the way the table does: one
// open offer per stop, and only an open offer of the checker's can be deleted.
func (m *memStore) CreateChecker(_ context.Context, p *Proposal) (bool, error) {
	for _, q := range m.byID {
		if q.TripID == p.TripID && q.Source == SourceChecker && q.Status == StatusPending && q.Action.StopID == p.Action.StopID {
			return false, nil
		}
	}
	p.ID, p.Status, p.Source, p.CreatedAt = uuid.New(), StatusPending, SourceChecker, time.Now()
	cp := *p
	m.byID[p.ID] = &cp
	return true, nil
}

func (m *memStore) DeleteChecker(_ context.Context, id uuid.UUID) error {
	if p, ok := m.byID[id]; ok && p.Source == SourceChecker && p.Status == StatusPending {
		delete(m.byID, id)
	}
	return nil
}

// checked is a trip of three dated days, Mon 13 to Wed 15 April 2026, with a
// museum on the Monday, and the service set up to keep the checker's offers.
type checked struct {
	*together
	trip          *trip.Trip
	museum        uuid.UUID
	mon, tue, wed uuid.UUID
	toTue, toWed  trip.StopMove
	monday        time.Time
}

func newChecked(t *testing.T) *checked {
	t.Helper()
	g := newTogether(t)
	c := &checked{
		together: g, museum: uuid.New(), mon: uuid.New(), tue: uuid.New(), wed: uuid.New(),
		monday: time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
	}
	day := func(id uuid.UUID, n int32, stops ...trip.TripStop) trip.TripDay {
		date := c.monday.AddDate(0, 0, int(n-1))
		return trip.TripDay{ID: id, DayNumber: n, Date: &date, Stops: stops}
	}
	c.trip = g.plans.trips.t
	c.trip.Days = []trip.TripDay{
		day(c.mon, 1, trip.TripStop{ID: c.museum, Name: "Museu Nacional do Azulejo"}),
		day(c.tue, 2, trip.TripStop{ID: uuid.New(), Name: "Castelo de São Jorge"}),
		day(c.wed, 3),
	}
	c.toTue = trip.StopMove{StopID: c.museum, ToDayID: c.tue, ToIndex: 1, Opens: 600, Closes: 1080}
	c.toWed = trip.StopMove{StopID: c.museum, ToDayID: c.wed, Opens: 600, Closes: 1080}

	d := g.svc.d
	d.Checker = g.store
	g.svc = NewService(d)
	return c
}

func (c *checked) offers(t *testing.T) []Proposal {
	t.Helper()
	all, err := c.board.ListForTrip(context.Background(), c.tid, nil)
	require.NoError(t, err)
	var out []Proposal
	for _, p := range all {
		if p.Source == SourceChecker {
			out = append(out, p)
		}
	}
	return out
}

func TestOfferMoves_OffersTheMoveToTheOwnerAndTellsNobody(t *testing.T) {
	c := newChecked(t)
	ctx := context.Background()

	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{c.toTue})

	got := c.offers(t)
	require.Len(t, got, 1)
	p := got[0]
	require.Equal(t, "Museu Nacional do Azulejo is shut on Mon 13 Apr. Move it to day 2 (Tue 14 Apr), when it is open 10:00–18:00.", p.Summary)
	require.Equal(t, c.uid, p.UserID)
	require.Equal(t, Action{Kind: KindMoveStop, StopID: c.museum.String(), ToDayID: c.tue.String(), ToIndex: 1}, p.Action)
	require.Equal(t, c.now.Add(checkerOfferTTL), p.ExpiresAt)
	require.Empty(t, c.notify.sent, "nobody asked; no push")
	require.Equal(t, []string{"checker_move_offered"}, c.events.events)

	// The owner sees it among the trip's suggestions and can apply it.
	views, err := c.svc.List(ctx, c.uid, c.tid, false)
	require.NoError(t, err)
	require.Len(t, views, 1)
	_, _, err = c.svc.Apply(ctx, c.uid, p.ID, nil, c.trip.Version)
	require.NoError(t, err)
}

func TestOfferMoves_SaysOnlyTheDayWhenTheStopHasNoTime(t *testing.T) {
	c := newChecked(t)
	c.svc.OfferMoves(context.Background(), c.trip, []trip.StopMove{{StopID: c.museum, ToDayID: c.tue}})
	got := c.offers(t)
	require.Len(t, got, 1)
	require.Equal(t, "Museu Nacional do Azulejo is shut on Mon 13 Apr. Move it to day 2 (Tue 14 Apr), when it is open.", got[0].Summary)
}

func TestOfferMoves_OneOfferPerStopHoweverOftenTheTripIsRead(t *testing.T) {
	c := newChecked(t)
	for range 3 {
		c.svc.OfferMoves(context.Background(), c.trip, []trip.StopMove{c.toTue})
	}
	require.Len(t, c.offers(t), 1)
	require.Len(t, c.events.events, 1)
}

func TestOfferMoves_TakesBackAnOfferThatNoLongerHolds(t *testing.T) {
	c := newChecked(t)
	ctx := context.Background()
	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{c.toTue})
	require.Len(t, c.offers(t), 1)

	// The stop was moved by hand, or the hours changed: no move holds now.
	c.svc.OfferMoves(ctx, c.trip, nil)
	require.Empty(t, c.offers(t))
}

func TestOfferMoves_ReplacesAnOfferWhenAnotherDayIsNowTheOne(t *testing.T) {
	c := newChecked(t)
	ctx := context.Background()
	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{c.toTue})
	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{c.toWed})

	got := c.offers(t)
	require.Len(t, got, 1)
	require.Equal(t, c.wed.String(), got[0].Action.ToDayID)
}

func TestOfferMoves_DoesNotRepeatAMoveTheOwnerTurnedDown(t *testing.T) {
	c := newChecked(t)
	ctx := context.Background()
	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{c.toTue})
	require.NoError(t, c.svc.Dismiss(ctx, c.uid, c.offers(t)[0].ID))

	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{c.toTue})
	require.Empty(t, c.offers(t), "the same move, again, after a no")

	// A different day is a different question.
	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{c.toWed})
	require.Len(t, c.offers(t), 1)
}

func TestOfferMoves_RenewsAnOfferThatRanOut(t *testing.T) {
	c := newChecked(t)
	ctx := context.Background()
	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{c.toTue})
	first := c.offers(t)[0]

	c.now = c.now.Add(checkerOfferTTL + time.Minute)
	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{c.toTue})

	got := c.offers(t)
	require.Len(t, got, 1)
	require.NotEqual(t, first.ID, got[0].ID)
	require.Equal(t, c.now.Add(checkerOfferTTL), got[0].ExpiresAt)
}

func TestOfferMoves_OffersNothingForAStopOrDayNotOnTheTrip(t *testing.T) {
	c := newChecked(t)
	ctx := context.Background()
	c.svc.OfferMoves(ctx, c.trip, []trip.StopMove{
		{StopID: uuid.New(), ToDayID: c.tue},
		{StopID: c.museum, ToDayID: uuid.New()},
	})
	require.Empty(t, c.offers(t))
}

func TestOfferMoves_OffersNothingOnATripWithNoDates(t *testing.T) {
	c := newChecked(t)
	for i := range c.trip.Days {
		c.trip.Days[i].Date = nil
	}
	c.svc.OfferMoves(context.Background(), c.trip, []trip.StopMove{c.toTue})
	require.Empty(t, c.offers(t))
}

func TestOfferMoves_LeavesEveryoneElsesSuggestionsAlone(t *testing.T) {
	c := newChecked(t)
	ctx := context.Background()
	v, err := c.svc.ProposeEdit(ctx, c.co, c.tid, Action{Kind: KindMoveStop, StopID: c.museum.String(), ToDayID: c.tue.String()}, c.trip.Version)
	require.NoError(t, err)

	c.svc.OfferMoves(ctx, c.trip, nil)

	kept, err := c.board.GetByID(ctx, v.ID)
	require.NoError(t, err)
	require.Equal(t, StatusPending, kept.Status)
}

func TestOfferMoves_DoesNothingUnlessSwitchedOn(t *testing.T) {
	c := newChecked(t)
	d := c.svc.d
	d.Checker = nil
	svc := NewService(d)
	svc.OfferMoves(context.Background(), c.trip, []trip.StopMove{c.toTue})
	require.Empty(t, c.offers(t))
}
