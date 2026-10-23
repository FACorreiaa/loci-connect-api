//go:build integration

package tripaction

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func newUserAndTrip(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid := uuid.New()
	_, err := testDB.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, $2)`, uid, "ta-"+uid.String()+"@loci.test")
	require.NoError(t, err)
	var tid uuid.UUID
	require.NoError(t, testDB.QueryRow(ctx,
		`INSERT INTO trips (user_id, city_name, title, constraints, version) VALUES ($1, 'Lisbon', 'Lisbon', '{}', 1) RETURNING id`,
		uid).Scan(&tid))
	return uid, tid
}

func TestStore_RoundTripAndOneShotTransitions(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	uid, tid := newUserAndTrip(t)

	p := &Proposal{
		UserID: uid, TripID: tid,
		Action:    Action{Kind: KindSearchHotels, City: "Lisbon", MinStars: 4, MaxStars: 4},
		Summary:   "4★ hotels in Lisbon",
		Options:   []Option{{Label: "Hotel Avenida · 4★", Stay: &trip.TripStay{CityName: "Lisbon", Name: "Hotel Avenida", StarRating: "4"}}},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, store.Create(ctx, p))
	require.NotEqual(t, uuid.Nil, p.ID)
	require.Equal(t, StatusPending, p.Status)

	got, err := store.Get(ctx, p.ID, uid)
	require.NoError(t, err)
	require.Equal(t, p.Action, got.Action)
	require.Equal(t, "Hotel Avenida", got.Options[0].Stay.Name)
	require.Equal(t, uuid.Nil, got.SessionID, "no session stored as NULL, read back as Nil")

	_, err = store.Get(ctx, p.ID, uuid.New())
	require.ErrorIs(t, err, ErrNotFound, "someone else's proposal")

	require.NoError(t, store.Transition(ctx, p.ID, StatusPending, StatusApplied))
	require.ErrorIs(t, store.Transition(ctx, p.ID, StatusPending, StatusApplied), ErrNotPending, "applied once")
}

func TestBoard_SharedProposalsReactionsAndClosing(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	owner, tid := newUserAndTrip(t)
	member := uuid.New()
	_, err := testDB.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, $2)`, member, "ta-"+member.String()+"@loci.test")
	require.NoError(t, err)

	edit := &Proposal{
		UserID: member, TripID: tid, Source: SourceEditor, BaseVersion: 1,
		Action:    Action{Kind: KindAddStop, DayID: uuid.NewString(), Stop: &trip.TripStop{Name: "Pastéis de Belém"}},
		Summary:   "Add Pastéis de Belém to day 1.",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, store.Create(ctx, edit))
	chat := &Proposal{UserID: owner, TripID: tid, Action: Action{Kind: KindRegenerateDays, Days: 4}, ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, store.Create(ctx, chat))

	// Anyone on the trip reads a member's proposal by id; Get stays proposer-only.
	got, err := store.GetByID(ctx, edit.ID)
	require.NoError(t, err)
	require.Equal(t, SourceEditor, got.Source)
	require.Equal(t, int64(1), got.BaseVersion)
	require.Equal(t, "Pastéis de Belém", got.Action.Stop.Name)
	_, err = store.Get(ctx, edit.ID, owner)
	require.ErrorIs(t, err, ErrNotFound)
	gotChat, err := store.GetByID(ctx, chat.ID)
	require.NoError(t, err)
	require.Equal(t, SourceChat, gotChat.Source, "chat cards default to chat")
	require.Zero(t, gotChat.BaseVersion, "a chat card stores no base version")

	require.NoError(t, store.React(ctx, edit.ID, member, 1))
	require.NoError(t, store.React(ctx, edit.ID, owner, 1))
	require.NoError(t, store.React(ctx, edit.ID, owner, -1), "a second reaction replaces the first")
	tallies, err := store.Tallies(ctx, []uuid.UUID{edit.ID, chat.ID}, owner)
	require.NoError(t, err)
	require.Equal(t, Tally{Up: 1, Down: 1, Mine: -1}, tallies[edit.ID])
	require.Equal(t, Tally{}, tallies[chat.ID])
	require.NoError(t, store.React(ctx, edit.ID, owner, 0))
	tallies, err = store.Tallies(ctx, []uuid.UUID{edit.ID}, owner)
	require.NoError(t, err)
	require.Equal(t, Tally{Up: 1}, tallies[edit.ID])

	list, err := store.ListForTrip(ctx, tid, nil)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, chat.ID, list[0].ID, "newest first")

	require.NoError(t, store.Transition(ctx, edit.ID, StatusPending, StatusApplied))
	require.NoError(t, store.MarkApplied(ctx, edit.ID, owner))
	open, err := store.ListForTrip(ctx, tid, nil)
	require.NoError(t, err)
	require.Len(t, open, 1, "applied ones leave the open list")
	since := time.Now().Add(-time.Hour)
	all, err := store.ListForTrip(ctx, tid, &since)
	require.NoError(t, err)
	require.Len(t, all, 2)
	applied, err := store.GetByID(ctx, edit.ID)
	require.NoError(t, err)
	require.Equal(t, owner, applied.AppliedBy)

	var resolved *time.Time
	require.NoError(t, testDB.QueryRow(ctx, `SELECT resolved_at FROM trip_action_proposals WHERE id = $1`, edit.ID).Scan(&resolved))
	require.NotNil(t, resolved)
	require.NoError(t, store.Transition(ctx, edit.ID, StatusApplied, StatusPending))
	require.NoError(t, testDB.QueryRow(ctx, `SELECT resolved_at FROM trip_action_proposals WHERE id = $1`, edit.ID).Scan(&resolved))
	require.Nil(t, resolved, "giving a proposal back reopens it")
}
