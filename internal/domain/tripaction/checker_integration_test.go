//go:build integration

package tripaction

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/social"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// checkerRig is the offer loop on the real schema: a saved trip nobody else
// is on, the real proposal store, and the service the RPC handlers call.
// What it does not hold is the map: which moves hold is the planner's rule,
// tested on its own, so the moves are handed in.
type checkerRig struct {
	svc   *Service
	repo  trip.Repository
	owner uuid.UUID
	trip  *trip.Trip
}

func newCheckerRig(t *testing.T) *checkerRig {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := trip.NewRepository(testDB, log)
	store := NewStore(testDB)
	svc := NewService(Deps{
		Store: store, Board: store, Checker: store, Trips: repo,
		Plans:   trip.NewService(repo, trip.NewPlanRepository(testDB, log), flights.DeepLinks{}),
		Members: trip.NewMemberAccess(trip.NewSharingRepository(testDB, log), trip.NewMembersRepository(testDB, log), social.NewService(social.NewRepository(testDB), nil, log)),
		Notify:  &fakeNotify{}, Logger: log,
	})
	owner := newPerson(t)
	monday := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 30)
	tuesday := monday.AddDate(0, 0, 1)
	saved, err := repo.SaveTrip(ctx, &trip.Trip{
		UserID: owner, CityName: "Lisbon", Title: "Lisbon",
		Days: []trip.TripDay{
			{DayNumber: 1, Date: &monday, Stops: []trip.TripStop{{Name: "Museu Nacional do Azulejo"}, {Name: "Museu Gulbenkian"}}},
			{DayNumber: 2, Date: &tuesday, Stops: []trip.TripStop{{Name: "Castelo de São Jorge"}}},
		},
	}, 0)
	require.NoError(t, err)
	return &checkerRig{svc: svc, repo: repo, owner: owner, trip: saved}
}

// reload reads the trip as the next owner read would.
func (r *checkerRig) reload(t *testing.T) *trip.Trip {
	t.Helper()
	got, err := r.repo.GetTrip(context.Background(), r.trip.ID, r.owner)
	require.NoError(t, err)
	r.trip = got
	return got
}

// cards is what the owner's trip page is given.
func (r *checkerRig) cards(t *testing.T) []View {
	t.Helper()
	list, err := r.svc.List(context.Background(), r.owner, r.trip.ID, false)
	require.NoError(t, err)
	return list
}

func stopNames(d trip.TripDay) []string {
	out := make([]string, 0, len(d.Stops))
	for _, s := range d.Stops {
		out = append(out, s.Name)
	}
	return out
}

// The whole loop for a trip nobody else is on: the owner is offered the
// move, sees it where the trip page looks, applies it, and the next read
// has the stop on the new day and no card left.
func TestCheckerOffer_OfferedSeenAppliedAndGone(t *testing.T) {
	r := newCheckerRig(t)
	ctx := context.Background()
	azulejo := r.trip.Days[0].Stops[0]
	move := trip.StopMove{StopID: azulejo.ID, ToDayID: r.trip.Days[1].ID, ToIndex: 1, Opens: 600, Closes: 1080}

	r.svc.OfferMoves(ctx, r.trip, []trip.StopMove{move})

	cards := r.cards(t)
	require.Len(t, cards, 1, "a solo owner is given the card")
	require.Contains(t, cards[0].Summary, "Museu Nacional do Azulejo is shut on")
	require.Contains(t, cards[0].Summary, "open 10:00–18:00")
	require.Equal(t, r.owner, cards[0].UserID)

	_, _, err := r.svc.Apply(ctx, r.owner, cards[0].ID, nil, r.trip.Version)
	require.NoError(t, err)

	after := r.reload(t)
	require.Equal(t, []string{"Museu Gulbenkian"}, stopNames(after.Days[0]))
	require.Equal(t, []string{"Castelo de São Jorge", "Museu Nacional do Azulejo"}, stopNames(after.Days[1]))

	// The next read: the stop is no longer shut, so there is nothing to move.
	r.svc.OfferMoves(ctx, after, nil)
	require.Empty(t, r.cards(t))
}

// A no is remembered: the same move is not put back on the next read.
func TestCheckerOffer_TurnedDownStaysDown(t *testing.T) {
	r := newCheckerRig(t)
	ctx := context.Background()
	move := trip.StopMove{StopID: r.trip.Days[0].Stops[0].ID, ToDayID: r.trip.Days[1].ID, ToIndex: 1, Opens: 600, Closes: 1080}

	r.svc.OfferMoves(ctx, r.trip, []trip.StopMove{move})
	cards := r.cards(t)
	require.Len(t, cards, 1)
	require.NoError(t, r.svc.Dismiss(ctx, r.owner, cards[0].ID))

	r.svc.OfferMoves(ctx, r.reload(t), []trip.StopMove{move})
	require.Empty(t, r.cards(t))
	require.Equal(t, []string{"Museu Nacional do Azulejo", "Museu Gulbenkian"}, stopNames(r.trip.Days[0]), "and the trip is as it was")
}

// Two stops offered the same day, applied in the other order. The second
// card was written for a day that already held the first; applied first, it
// still lands on that day, and the first card is rewritten for the trip as
// it now is before the owner applies it.
func TestCheckerOffer_TwoForTheSameDayAppliedOutOfOrder(t *testing.T) {
	r := newCheckerRig(t)
	ctx := context.Background()
	tue := r.trip.Days[1].ID
	azulejo, gulbenkian := r.trip.Days[0].Stops[0].ID, r.trip.Days[0].Stops[1].ID
	first := trip.StopMove{StopID: azulejo, ToDayID: tue, ToIndex: 1, Opens: 600, Closes: 1080}
	second := trip.StopMove{StopID: gulbenkian, ToDayID: tue, ToIndex: 2, Opens: 600, Closes: 1080}

	r.svc.OfferMoves(ctx, r.trip, []trip.StopMove{first, second})
	byStop := map[string]View{}
	for _, c := range r.cards(t) {
		byStop[c.Action.StopID] = c
	}
	require.Len(t, byStop, 2)

	_, _, err := r.svc.Apply(ctx, r.owner, byStop[gulbenkian.String()].ID, nil, r.trip.Version)
	require.NoError(t, err)
	after := r.reload(t)
	require.Equal(t, []string{"Castelo de São Jorge", "Museu Gulbenkian"}, stopNames(after.Days[1]))

	// The next read works the remaining move out again; here it is the same
	// move, and its card still stands.
	r.svc.OfferMoves(ctx, after, []trip.StopMove{first})
	cards := r.cards(t)
	require.Len(t, cards, 1)
	_, _, err = r.svc.Apply(ctx, r.owner, cards[0].ID, nil, after.Version)
	require.NoError(t, err)

	done := r.reload(t)
	require.Empty(t, done.Days[0].Stops)
	require.Equal(t, []string{"Castelo de São Jorge", "Museu Nacional do Azulejo", "Museu Gulbenkian"}, stopNames(done.Days[1]))
}
