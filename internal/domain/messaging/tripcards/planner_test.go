package tripcards

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

type fakeSessions struct{ latest uuid.UUID }

func (f fakeSessions) GetUserChatSessions(context.Context, uuid.UUID, int, int) (*locitypes.ChatSessionsResponse, error) {
	if f.latest == uuid.Nil {
		return &locitypes.ChatSessionsResponse{}, nil
	}
	return &locitypes.ChatSessionsResponse{Sessions: []locitypes.ChatSession{{ID: f.latest}}}, nil
}

type fakeTrips struct{ bySession map[uuid.UUID]*trip.Trip }

func (f fakeTrips) LatestForSession(_ context.Context, _, session uuid.UUID) (*trip.Trip, error) {
	if t, ok := f.bySession[session]; ok {
		return t, nil
	}
	return nil, trip.ErrNotFound
}

type fakeActions struct {
	proposals []tripaction.Proposal
	applyErr  error
	proposed  int
}

func (f *fakeActions) Propose(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) ([]tripaction.Proposal, error) {
	f.proposed++
	return f.proposals, nil
}

func (f *fakeActions) ApplyCurrent(context.Context, uuid.UUID, uuid.UUID, *int) (*trip.Trip, *locitypes.ConversationMessage, error) {
	if f.applyErr != nil {
		return nil, nil, f.applyErr
	}
	return &trip.Trip{}, &locitypes.ConversationMessage{Content: "Dates set: 12 Nov – 17 Nov 2026."}, nil
}

func (f *fakeActions) Dismiss(context.Context, uuid.UUID, uuid.UUID) error { return nil }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestPropose_OnlyWhenTheLatestConversationMadeATrip(t *testing.T) {
	session := uuid.New()
	actions := &fakeActions{}
	p := New(fakeSessions{latest: session}, fakeTrips{}, actions, quiet)
	cards, err := p.Propose(context.Background(), uuid.New(), "a@b.c", "4 days in Rome")
	require.NoError(t, err)
	require.Empty(t, cards)
	require.Zero(t, actions.proposed, "no extraction call without a trip")

	p = New(fakeSessions{}, fakeTrips{}, actions, quiet)
	cards, err = p.Propose(context.Background(), uuid.New(), "a@b.c", "x")
	require.NoError(t, err)
	require.Empty(t, cards, "no conversation yet")
}

func TestPropose_CardsAndButtons(t *testing.T) {
	session, tripID := uuid.New(), uuid.New()
	link := "https://pestana.example"
	actions := &fakeActions{proposals: []tripaction.Proposal{
		{ID: uuid.New(), TripID: tripID, Summary: "Set the trip's dates to 12 Nov – 17 Nov 2026.", Action: tripaction.Action{Kind: tripaction.KindSetDates}},
		{
			ID: uuid.New(), TripID: tripID, Summary: "4★ hotels in Lisbon: pick one to stay at.", Action: tripaction.Action{Kind: tripaction.KindSearchHotels},
			Options: []tripaction.Option{
				{Label: "Hotel Avenida · 4★", Detail: "Av. da Liberdade", Stay: &trip.TripStay{Name: "Hotel Avenida"}},
				{Label: "Pestana · 4★", Stay: &trip.TripStay{Name: "Pestana", BookingURL: &link}},
			},
		},
		{
			ID: uuid.New(), TripID: tripID, Summary: "Flights NYC → Lisbon", Action: tripaction.Action{Kind: tripaction.KindSearchFlights},
			Options: []tripaction.Option{{Label: "Save this flight search", Flight: &trip.TripFlight{Links: []flights.Link{{Label: "Google Flights", URL: "https://g.example"}}}}},
		},
	}}
	p := New(fakeSessions{latest: session}, fakeTrips{bySession: map[uuid.UUID]*trip.Trip{session: {ID: tripID}}}, actions, quiet)
	cards, err := p.Propose(context.Background(), uuid.New(), "a@b.c", "the lot")
	require.NoError(t, err)
	require.Len(t, cards, 3)

	require.Equal(t, []string{"Confirm", "Not now"}, labels(cards[0]))
	require.Equal(t, []string{"Stay at 1", "Stay at 2", "Not now"}, labels(cards[1]))
	require.Contains(t, cards[1].Text, "1. Hotel Avenida · 4★ — Av. da Liberdade")
	require.Contains(t, cards[1].Text, "https://pestana.example")
	require.Equal(t, []string{"Save this flight", "Not now"}, labels(cards[2]))
	require.Contains(t, cards[2].Text, "Google Flights: https://g.example")
	for _, c := range cards {
		for _, b := range c.Buttons {
			require.LessOrEqual(t, len(b.Data), 64)
		}
	}
}

func labels(c messaging.TripCard) []string {
	out := make([]string, 0, len(c.Buttons))
	for _, b := range c.Buttons {
		out = append(out, b.Label)
	}
	return out
}

func TestApply_SaysWhatHappenedInPlainWords(t *testing.T) {
	cases := map[error]string{
		nil:                      "Dates set",
		tripaction.ErrNotPending: "already used",
		tripaction.ErrExpired:    "expired",
		tripaction.ErrNotFound:   "can't find",
		trip.ErrVersionConflict:  "changed while",
		trip.ErrInvalidEdit:      "can't be made",
		errors.New("db down"):    "Something went wrong",
	}
	for err, want := range cases {
		p := New(fakeSessions{}, fakeTrips{}, &fakeActions{applyErr: err}, quiet)
		require.Contains(t, p.Apply(context.Background(), uuid.New(), "a@b.c", uuid.New(), nil), want)
	}
}
