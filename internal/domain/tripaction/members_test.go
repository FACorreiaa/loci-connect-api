package tripaction

import (
	"context"
	"sort"
	"testing"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// --- planning-together fakes ---

type memBoard struct {
	*memStore
	reactions map[uuid.UUID]map[uuid.UUID]int
	appliedBy map[uuid.UUID]uuid.UUID
}

func (m *memBoard) GetByID(_ context.Context, id uuid.UUID) (*Proposal, error) {
	p, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (m *memBoard) ListForTrip(_ context.Context, tripID uuid.UUID, closedSince *time.Time) ([]Proposal, error) {
	var out []Proposal
	for _, p := range m.byID {
		if p.TripID == tripID && (p.Status == StatusPending || closedSince != nil) {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *memBoard) React(_ context.Context, proposalID, userID uuid.UUID, value int) error {
	if m.reactions[proposalID] == nil {
		m.reactions[proposalID] = map[uuid.UUID]int{}
	}
	if value == 0 {
		delete(m.reactions[proposalID], userID)
		return nil
	}
	m.reactions[proposalID][userID] = value
	return nil
}

func (m *memBoard) Tallies(_ context.Context, ids []uuid.UUID, viewer uuid.UUID) (map[uuid.UUID]Tally, error) {
	out := map[uuid.UUID]Tally{}
	for _, id := range ids {
		var t Tally
		for who, v := range m.reactions[id] {
			if v > 0 {
				t.Up++
			} else {
				t.Down++
			}
			if who == viewer {
				t.Mine = v
			}
		}
		out[id] = t
	}
	return out, nil
}

func (m *memBoard) MarkApplied(_ context.Context, id, by uuid.UUID) error {
	m.appliedBy[id] = by
	return nil
}

type fakeMembers struct {
	trips    *fakeTrips
	roles    map[uuid.UUID]trip.Role
	mayApply bool
}

func (f *fakeMembers) CoPlannersMayApply(context.Context, uuid.UUID) (bool, error) {
	return f.mayApply, nil
}

func (f *fakeMembers) Resolve(_ context.Context, viewer, tripID uuid.UUID) (*trip.Trip, trip.Role, error) {
	t := f.trips.t
	if t.ID != tripID {
		return nil, trip.RoleNone, trip.ErrNotFound
	}
	role := f.roles[viewer]
	if viewer == t.UserID {
		role = trip.RoleOwner
	}
	if role == trip.RoleNone {
		return nil, trip.RoleNone, trip.ErrNotFound
	}
	cp := *t
	return &cp, role, nil
}

func (f *fakeMembers) Everyone(_ context.Context, t *trip.Trip) ([]uuid.UUID, error) {
	out := []uuid.UUID{t.UserID}
	for id := range f.roles {
		out = append(out, id)
	}
	return out, nil
}

func (f *fakeMembers) PublicUsers(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error) {
	out := map[uuid.UUID]*socialv1.PublicUser{}
	for _, id := range ids {
		name := "Owner"
		if f.roles[id] == trip.RoleCoPlanner {
			name = "Ana"
		}
		out[id] = &socialv1.PublicUser{Id: id.String(), DisplayName: name}
	}
	return out, nil
}

type sentPush struct {
	to   []uuid.UUID
	body string
}

type fakeNotify struct{ sent []sentPush }

func (f *fakeNotify) TripChanged(_ context.Context, to []uuid.UUID, _ uuid.UUID, _, body string) {
	f.sent = append(f.sent, sentPush{to: to, body: body})
}

type fakeAnalytics struct{ events []string }

func (f *fakeAnalytics) Capture(_, event string, _ map[string]any) {
	f.events = append(f.events, event)
}

type together struct {
	*fixture
	board            *memBoard
	members          *fakeMembers
	notify           *fakeNotify
	events           *fakeAnalytics
	co, viewer, nope uuid.UUID
	day, stop        uuid.UUID
}

func newTogether(t *testing.T) *together {
	t.Helper()
	f := newFixture(t, `{"actions":[]}`, nil)
	day, stop := uuid.New(), uuid.New()
	f.plans.trips.t.Days = []trip.TripDay{
		{ID: day, DayNumber: 1, Stops: []trip.TripStop{{ID: stop, Name: "Belém Tower"}}},
		{ID: uuid.New(), DayNumber: 2},
	}
	g := &together{
		fixture: f, notify: &fakeNotify{}, events: &fakeAnalytics{},
		board: &memBoard{memStore: f.store, reactions: map[uuid.UUID]map[uuid.UUID]int{}, appliedBy: map[uuid.UUID]uuid.UUID{}},
		co:    uuid.New(), viewer: uuid.New(), nope: uuid.New(), day: day, stop: stop,
	}
	members := &fakeMembers{trips: f.plans.trips, roles: map[uuid.UUID]trip.Role{g.co: trip.RoleCoPlanner, g.viewer: trip.RoleViewer}}
	g.members = members
	d := f.svc.d
	d.Members, d.Board, d.Notify, d.Analytics = members, g.board, g.notify, g.events
	f.svc = NewService(d)
	return g
}

func (g *together) addStop(name string) Action {
	return Action{Kind: KindAddStop, DayID: g.day.String(), Stop: &trip.TripStop{ID: g.stop, Name: name}}
}

// --- tests ---

func TestProposeEdit_CoPlannerSuggestsAndOwnerHears(t *testing.T) {
	g := newTogether(t)
	ctx := context.Background()

	v, err := g.svc.ProposeEdit(ctx, g.co, g.tid, g.addStop("Pastéis de Belém"), 3)
	require.NoError(t, err)
	require.Equal(t, "Add Pastéis de Belém to day 1.", v.Summary)
	require.Equal(t, SourceEditor, v.Source)
	require.Equal(t, int64(3), v.BaseVersion)
	require.Equal(t, g.now.Add(memberProposalTTL), v.ExpiresAt)
	require.Equal(t, uuid.Nil, v.Action.Stop.ID, "a suggested stop never carries an existing stop's id")
	require.Equal(t, "Ana", v.Proposer.GetDisplayName())

	require.Len(t, g.notify.sent, 1)
	require.Equal(t, []uuid.UUID{g.uid}, g.notify.sent[0].to)
	require.Equal(t, "Ana suggested: Add Pastéis de Belém to day 1.", g.notify.sent[0].body)
	require.Equal(t, []string{"trip_proposal_created"}, g.events.events)
}

func TestProposeEdit_Refusals(t *testing.T) {
	g := newTogether(t)
	ctx := context.Background()

	_, err := g.svc.ProposeEdit(ctx, g.viewer, g.tid, g.addStop("X"), 3)
	require.ErrorIs(t, err, ErrForbidden, "a viewer only looks")
	_, err = g.svc.ProposeEdit(ctx, g.nope, g.tid, g.addStop("X"), 3)
	require.ErrorIs(t, err, trip.ErrNotFound, "a stranger does not learn the trip exists")
	_, err = g.svc.ProposeEdit(ctx, g.co, g.tid, Action{Kind: KindRemoveStop, StopID: uuid.NewString()}, 3)
	require.ErrorIs(t, err, ErrInvalid, "a stop the trip does not have")
	_, err = g.svc.ProposeEdit(ctx, g.co, g.tid, Action{Kind: KindSetDates, StartDate: "2026-11-01", EndDate: "2026-11-02"}, 3)
	require.ErrorIs(t, err, ErrInvalid, "only stop edits come from the editor")
	require.Empty(t, g.store.byID)
	require.Empty(t, g.notify.sent)
}

func TestReactAndList(t *testing.T) {
	g := newTogether(t)
	ctx := context.Background()
	v, err := g.svc.ProposeEdit(ctx, g.co, g.tid, g.addStop("Pastéis de Belém"), 3)
	require.NoError(t, err)

	_, err = g.svc.React(ctx, g.viewer, v.ID, 1)
	require.NoError(t, err)
	got, err := g.svc.React(ctx, g.uid, v.ID, -1)
	require.NoError(t, err)
	require.Equal(t, Tally{Up: 1, Down: 1, Mine: -1}, got.Tally)

	_, err = g.svc.React(ctx, g.nope, v.ID, 1)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = g.svc.React(ctx, g.viewer, v.ID, 2)
	require.ErrorIs(t, err, ErrInvalid)

	// An expired card drops off the list.
	stale := Proposal{UserID: g.uid, TripID: g.tid, Action: Action{Kind: KindRegenerateDays, Days: 4}, ExpiresAt: g.now.Add(-time.Minute)}
	require.NoError(t, g.store.Create(ctx, &stale))

	list, err := g.svc.List(ctx, g.viewer, g.tid, false)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, v.ID, list[0].ID)
	require.Equal(t, 1, list[0].Tally.Mine)
	_, err = g.svc.List(ctx, g.nope, g.tid, false)
	require.ErrorIs(t, err, trip.ErrNotFound)
}

func TestApply_OnlyOwnerAndEveryoneElseHears(t *testing.T) {
	g := newTogether(t)
	ctx := context.Background()
	v, err := g.svc.ProposeEdit(ctx, g.co, g.tid, g.addStop("Pastéis de Belém"), 3)
	require.NoError(t, err)
	g.notify.sent = nil

	_, _, err = g.svc.Apply(ctx, g.co, v.ID, nil, 3)
	require.ErrorIs(t, err, ErrForbidden, "a co-planner suggests; the owner applies")
	_, _, err = g.svc.Apply(ctx, g.nope, v.ID, nil, 3)
	require.ErrorIs(t, err, ErrNotFound)

	tr, msg, err := g.svc.Apply(ctx, g.uid, v.ID, nil, 3)
	require.NoError(t, err)
	require.Equal(t, int64(4), tr.Version)
	require.Equal(t, []string{"add_stop:Pastéis de Belém"}, g.plans.applied)
	require.Equal(t, "Add Pastéis de Belém to day 1.", msg.Content)
	require.Equal(t, StatusApplied, g.store.byID[v.ID].Status)
	require.Equal(t, g.uid, g.board.appliedBy[v.ID])

	require.Len(t, g.notify.sent, 1)
	require.ElementsMatch(t, []uuid.UUID{g.co, g.viewer}, g.notify.sent[0].to, "everyone but the owner who applied it")
	require.Equal(t, "Owner took Ana's suggestion: Add Pastéis de Belém to day 1.", g.notify.sent[0].body)
	require.Contains(t, g.events.events, "trip_proposal_applied")

	_, _, err = g.svc.Apply(ctx, g.uid, v.ID, nil, 4)
	require.ErrorIs(t, err, ErrNotPending, "a second tap applies nothing")
}

func TestApply_CoPlannerWhenTheOwnerLetsThem(t *testing.T) {
	g := newTogether(t)
	g.members.mayApply = true
	ctx := context.Background()
	v, err := g.svc.ProposeEdit(ctx, g.co, g.tid, g.addStop("Pastéis de Belém"), 3)
	require.NoError(t, err)
	g.notify.sent = nil

	_, _, err = g.svc.Apply(ctx, g.viewer, v.ID, nil, 3)
	require.ErrorIs(t, err, ErrForbidden, "a viewer never applies")

	tr, _, err := g.svc.Apply(ctx, g.co, v.ID, nil, 3)
	require.NoError(t, err)
	require.Equal(t, int64(4), tr.Version)
	require.Equal(t, g.co, g.board.appliedBy[v.ID])
	require.Len(t, g.notify.sent, 1)
	require.ElementsMatch(t, []uuid.UUID{g.uid, g.viewer}, g.notify.sent[0].to, "the owner hears too")
}

func TestDismiss_CoPlannerWhoMayApplyStillOnlyTakesBackTheirOwn(t *testing.T) {
	g := newTogether(t)
	g.members.mayApply = true
	ctx := context.Background()
	mine, err := g.svc.ProposeEdit(ctx, g.uid, g.tid, g.addStop("A"), 3)
	require.NoError(t, err)

	require.ErrorIs(t, g.svc.Dismiss(ctx, g.co, mine.ID), ErrForbidden, "waving away the owner's card is the owner's call")
}

func TestApply_StopGoneKeepsProposalOpen(t *testing.T) {
	g := newTogether(t)
	ctx := context.Background()
	v, err := g.svc.ProposeEdit(ctx, g.co, g.tid, Action{Kind: KindRemoveStop, StopID: g.stop.String()}, 3)
	require.NoError(t, err)
	g.plans.fail = trip.ErrStopNotFound // the owner removed it by hand meanwhile

	_, _, err = g.svc.Apply(ctx, g.uid, v.ID, nil, 3)
	require.ErrorIs(t, err, trip.ErrInvalidEdit)
	require.Equal(t, StatusPending, g.store.byID[v.ID].Status, "given back, for the owner to dismiss")
}

func TestDismiss_OwnerOrProposer(t *testing.T) {
	g := newTogether(t)
	ctx := context.Background()
	a, err := g.svc.ProposeEdit(ctx, g.co, g.tid, g.addStop("A"), 3)
	require.NoError(t, err)
	b, err := g.svc.ProposeEdit(ctx, g.co, g.tid, g.addStop("B"), 3)
	require.NoError(t, err)

	require.ErrorIs(t, g.svc.Dismiss(ctx, g.viewer, a.ID), ErrForbidden)
	require.NoError(t, g.svc.Dismiss(ctx, g.co, a.ID), "the proposer takes theirs back")
	require.NoError(t, g.svc.Dismiss(ctx, g.uid, b.ID), "the owner turns any down")
	require.Equal(t, StatusDismissed, g.store.byID[a.ID].Status)
	require.Equal(t, StatusDismissed, g.store.byID[b.ID].Status)
}

func TestApply_SoloTripTellsNoOne(t *testing.T) {
	g := newTogether(t)
	g.svc.d.Members.(*fakeMembers).roles = map[uuid.UUID]trip.Role{}
	ctx := context.Background()
	v, err := g.svc.ProposeEdit(ctx, g.uid, g.tid, g.addStop("A"), 3)
	require.NoError(t, err)
	_, _, err = g.svc.Apply(ctx, g.uid, v.ID, nil, 3)
	require.NoError(t, err)
	require.Empty(t, g.notify.sent, "the owner's own suggestion to themselves pushes no one")
	require.NotContains(t, g.events.events, "trip_proposal_applied", "a trip planned alone is not a together event")
}

type fakeSpots struct {
	found []locitypes.POIDetailedInfo
	asked []string
}

func (f *fakeSpots) SearchPOIsByQueryAndCity(_ context.Context, query, city string) ([]locitypes.POIDetailedInfo, error) {
	f.asked = append(f.asked, query+"@"+city)
	return f.found, nil
}

// chat makes the next extraction reply `reply`, with a place search that
// finds Pastéis de Belém.
func (g *together) chat(reply string) (*scripted, *fakeSpots) {
	llm := &scripted{replies: []string{reply, reply}}
	spots := &fakeSpots{found: []locitypes.POIDetailedInfo{{ID: uuid.New(), Name: "Pastéis de Belém", Description: "Custard tarts"}}}
	g.svc.d.LLM, g.svc.d.Spots = llm, spots
	return llm, spots
}

func TestPropose_CoPlannerChatSuggestsToTheOwner(t *testing.T) {
	g := newTogether(t)
	_, spots := g.chat(`{"actions":[{"kind":"add_stop","day":1,"place":"custard tarts"},{"kind":"search_hotels","city":"Lisbon","min_stars":4,"max_stars":4}]}`)
	ctx := context.Background()

	got, err := g.svc.Propose(ctx, g.co, g.tid, uuid.Nil, "add custard tarts on day 1 and find 4-star hotels")
	require.NoError(t, err)
	require.Len(t, got, 1, "a hotel pick is the owner's to make in their own chat")
	p := got[0]
	require.Equal(t, "Add Pastéis de Belém to day 1.", p.Summary)
	require.Equal(t, g.day.String(), p.Action.DayID)
	require.Equal(t, "Custard tarts", p.Action.Stop.Notes)
	require.Equal(t, SourceChat, p.Source)
	require.Equal(t, g.co, p.UserID)
	require.Equal(t, g.now.Add(memberProposalTTL), p.ExpiresAt)
	require.Equal(t, []string{"custard tarts@Lisbon"}, spots.asked)

	require.Len(t, g.notify.sent, 1)
	require.Equal(t, []uuid.UUID{g.uid}, g.notify.sent[0].to)
	require.Equal(t, "Ana suggested: Add Pastéis de Belém to day 1.", g.notify.sent[0].body)
	require.Equal(t, []string{"trip_proposal_created"}, g.events.events)

	// The owner applies it like any other suggestion.
	_, _, err = g.svc.Apply(ctx, g.uid, p.ID, nil, 3)
	require.NoError(t, err)
	require.Equal(t, []string{"add_stop:Pastéis de Belém"}, g.plans.applied)
}

func TestPropose_StopsByNameAndWhatCannotBeFound(t *testing.T) {
	g := newTogether(t)
	g.chat(`{"actions":[
		{"kind":"remove_stop","day":2,"stop_name":"belém"},
		{"kind":"move_stop","day":1,"stop_name":"Belém Tower","to_day":2},
		{"kind":"remove_stop","day":1,"stop_name":"Sintra Palace"}]}`)
	got, err := g.svc.Propose(context.Background(), g.uid, g.tid, uuid.Nil, "drop belém, or move it to day 2, and drop Sintra")
	require.NoError(t, err)
	require.Len(t, got, 2, "a stop the trip doesn't have is no card")
	require.Equal(t, g.stop.String(), got[0].Action.StopID, "the model had the day wrong; the name still finds it")
	require.Equal(t, "Remove Belém Tower from day 1.", got[0].Summary)
	require.Equal(t, "Move Belém Tower from day 1 to day 2.", got[1].Summary)
	require.Equal(t, proposalTTL, got[0].ExpiresAt.Sub(g.now), "the owner's own cards keep the chat TTL")
	require.Empty(t, g.notify.sent, "the owner's own cards push no one")
	require.Equal(t, []string{"trip_proposal_created", "trip_proposal_created"}, g.events.events, "the trip has members")
}

func TestPropose_PlaceNotFoundIsNoCard(t *testing.T) {
	g := newTogether(t)
	_, spots := g.chat(`{"actions":[{"kind":"add_stop","day":1,"place":"unicorn bar"}]}`)
	spots.found = nil
	got, err := g.svc.Propose(context.Background(), g.co, g.tid, uuid.Nil, "add a unicorn bar")
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, g.notify.sent)
}

func TestPropose_ViewerProposesNothing(t *testing.T) {
	g := newTogether(t)
	llm, _ := g.chat(`{"actions":[{"kind":"remove_stop","day":1,"stop_name":"Belém Tower"}]}`)
	got, err := g.svc.Propose(context.Background(), g.viewer, g.tid, uuid.Nil, "remove the tower")
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, llm.prompts, "no model call for someone who can't suggest")
	_, err = g.svc.Propose(context.Background(), g.nope, g.tid, uuid.Nil, "remove the tower")
	require.ErrorIs(t, err, trip.ErrNotFound)
}
