//go:build integration

package tripaction

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/social"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

func newPerson(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testDB.Exec(context.Background(), `INSERT INTO users (id, email) VALUES ($1, $2)`, id, "ta-"+id.String()+"@loci.test")
	require.NoError(t, err)
	return id
}

// Planning together on the real schema: a co-planner suggests a swap, a
// viewer reacts, the co-planner cannot apply it, the owner does, and the
// trip changes exactly as the owner's own edit would. A blocked member and a
// stranger see nothing.
func TestPlanTogetherEndToEnd(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := trip.NewRepository(testDB, log)
	socialRepo := social.NewRepository(testDB)
	socialSvc := social.NewService(socialRepo, nil, log)
	store := NewStore(testDB)
	notify := &fakeNotify{}
	svc := NewService(Deps{
		Store: store, Board: store, Trips: repo,
		Plans:   trip.NewService(repo, trip.NewPlanRepository(testDB, log), flights.DeepLinks{}),
		Members: trip.NewMemberAccess(trip.NewSharingRepository(testDB, log), trip.NewMembersRepository(testDB, log), socialSvc),
		Notify:  notify, Logger: log,
	})

	ana, rui, eve, zoe, stranger := newPerson(t), newPerson(t), newPerson(t), newPerson(t), newPerson(t)
	saved, err := repo.SaveTrip(ctx, &trip.Trip{
		UserID: ana, CityName: "Lisbon", Title: "Lisbon long weekend",
		Days: []trip.TripDay{
			{DayNumber: 1, Stops: []trip.TripStop{{Name: "Miradouro da Graça"}, {Name: "Alfama"}}},
			{DayNumber: 2},
		},
	}, 0)
	require.NoError(t, err)
	for who, role := range map[uuid.UUID]string{rui: "co_planner", eve: "viewer", zoe: "co_planner"} {
		_, err := testDB.Exec(ctx, `INSERT INTO trip_members (trip_id, user_id, role, invited_by) VALUES ($1, $2, $3, $4)`, saved.ID, who, role, ana)
		require.NoError(t, err)
	}
	require.NoError(t, socialRepo.Block(ctx, ana, zoe))

	graca := saved.Days[0].Stops[0]
	swap := Action{Kind: KindReplaceStop, StopID: graca.ID.String(), Stop: &trip.TripStop{Name: "Miradouro de Santa Luzia"}}

	v, err := svc.ProposeEdit(ctx, rui, saved.ID, swap, saved.Version)
	require.NoError(t, err)
	require.Equal(t, "Swap Miradouro da Graça for Miradouro de Santa Luzia on day 1.", v.Summary)
	require.Len(t, notify.sent, 1)
	require.Equal(t, []uuid.UUID{ana}, notify.sent[0].to, "the owner hears about the suggestion")

	_, err = svc.ProposeEdit(ctx, eve, saved.ID, swap, saved.Version)
	require.ErrorIs(t, err, ErrForbidden, "a viewer cannot suggest")
	_, err = svc.ProposeEdit(ctx, zoe, saved.ID, swap, saved.Version)
	require.ErrorIs(t, err, trip.ErrNotFound, "a blocked member sees nothing")
	_, err = svc.List(ctx, stranger, saved.ID, false)
	require.ErrorIs(t, err, trip.ErrNotFound)

	_, err = svc.React(ctx, eve, v.ID, 1)
	require.NoError(t, err)
	list, err := svc.List(ctx, ana, saved.ID, false)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 1, list[0].Tally.Up)

	_, _, err = svc.Apply(ctx, rui, v.ID, nil, saved.Version)
	require.ErrorIs(t, err, ErrForbidden, "a co-planner suggests; the owner applies")

	// The owner moved Alfama in between: applying builds on the trip as it is.
	moved, err := svc.d.Plans.MoveStop(ctx, ana, saved.ID, saved.Version, saved.Days[0].Stops[1].ID, saved.Days[1].ID, 0)
	require.NoError(t, err)
	_, _, err = svc.Apply(ctx, ana, v.ID, nil, saved.Version)
	require.ErrorIs(t, err, trip.ErrVersionConflict, "the owner applies against the trip they see")

	notify.sent = nil
	after, _, err := svc.Apply(ctx, ana, v.ID, nil, moved.Version)
	require.NoError(t, err)
	require.Equal(t, "Miradouro de Santa Luzia", after.Days[0].Stops[0].Name)
	require.Equal(t, "Alfama", after.Days[1].Stops[0].Name)
	require.Len(t, notify.sent, 1)
	require.ElementsMatch(t, []uuid.UUID{rui, eve, zoe}, notify.sent[0].to)

	reread, err := store.GetByID(ctx, v.ID)
	require.NoError(t, err)
	require.Equal(t, StatusApplied, reread.Status)
	require.Equal(t, ana, reread.AppliedBy)

	// A suggestion whose stop is gone is refused and stays open.
	gone, err := svc.ProposeEdit(ctx, rui, saved.ID, Action{Kind: KindRemoveStop, StopID: after.Days[1].Stops[0].ID.String()}, after.Version)
	require.NoError(t, err)
	cleared, err := svc.d.Plans.RemoveStop(ctx, ana, saved.ID, after.Version, after.Days[1].Stops[0].ID)
	require.NoError(t, err)
	_, _, err = svc.Apply(ctx, ana, gone.ID, nil, cleared.Version)
	require.ErrorIs(t, err, trip.ErrInvalidEdit)
	open, err := store.GetByID(ctx, gone.ID)
	require.NoError(t, err)
	require.Equal(t, StatusPending, open.Status)
	require.NoError(t, svc.Dismiss(ctx, rui, gone.ID), "the proposer takes it back")
}

// A co-planner's chat message becomes a suggestion on the real schema, named
// by day and stop, and the owner applies it.
func TestPlanTogetherFromChat(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := trip.NewRepository(testDB, log)
	socialSvc := social.NewService(social.NewRepository(testDB), nil, log)
	store := NewStore(testDB)
	reply := `{"actions":[{"kind":"replace_stop","day":1,"stop_name":"Alfama","place":"pastries"}]}`
	svc := NewService(Deps{
		LLM:   &scripted{replies: []string{reply}},
		Store: store, Board: store, Trips: repo,
		Plans:   trip.NewService(repo, trip.NewPlanRepository(testDB, log), flights.DeepLinks{}),
		Members: trip.NewMemberAccess(trip.NewSharingRepository(testDB, log), trip.NewMembersRepository(testDB, log), socialSvc),
		Spots:   &fakeSpots{found: []locitypes.POIDetailedInfo{{Name: "Pastéis de Belém"}}},
		Notify:  &fakeNotify{}, Logger: log,
	})

	ana, rui := newPerson(t), newPerson(t)
	saved, err := repo.SaveTrip(ctx, &trip.Trip{
		UserID: ana, CityName: "Lisbon", Title: "Lisbon",
		Days: []trip.TripDay{{DayNumber: 1, Stops: []trip.TripStop{{Name: "Miradouro da Graça"}, {Name: "Alfama"}}}},
	}, 0)
	require.NoError(t, err)
	_, err = testDB.Exec(ctx, `INSERT INTO trip_members (trip_id, user_id, role, invited_by) VALUES ($1, $2, 'co_planner', $3)`, saved.ID, rui, ana)
	require.NoError(t, err)

	got, err := svc.Propose(ctx, rui, saved.ID, uuid.Nil, "swap Alfama for pastries")
	require.NoError(t, err)
	require.Len(t, got, 1)
	stored, err := store.GetByID(ctx, got[0].ID)
	require.NoError(t, err)
	require.Equal(t, SourceChat, stored.Source)
	require.Equal(t, rui, stored.UserID)
	require.Equal(t, saved.Days[0].Stops[1].ID.String(), stored.Action.StopID)

	after, _, err := svc.Apply(ctx, ana, got[0].ID, nil, saved.Version)
	require.NoError(t, err)
	require.Equal(t, "Pastéis de Belém", after.Days[0].Stops[1].Name)
}
