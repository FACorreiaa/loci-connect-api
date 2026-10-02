package messaging

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type fakePlanner struct {
	cards     []TripCard
	err       error
	proposed  []string
	applied   []string
	dismissed []uuid.UUID
}

func (f *fakePlanner) Propose(_ context.Context, _ uuid.UUID, _, text string) ([]TripCard, error) {
	f.proposed = append(f.proposed, text)
	return f.cards, f.err
}

func (f *fakePlanner) Apply(_ context.Context, _ uuid.UUID, _ string, id uuid.UUID, option *int) string {
	o := "-"
	if option != nil {
		o = strconv.Itoa(*option)
	}
	f.applied = append(f.applied, id.String()+"/"+o)
	return "Dates set: 12 Nov – 17 Nov 2026."
}

func (f *fakePlanner) Dismiss(_ context.Context, _, id uuid.UUID) string {
	f.dismissed = append(f.dismissed, id)
	return "Okay, I'll leave that."
}

func TestTripTokens_FitTelegramAndRoundTrip(t *testing.T) {
	id := uuid.New()
	two := 2
	for _, tok := range []string{ApplyToken(id, nil), ApplyToken(id, &two), DismissToken(id)} {
		require.LessOrEqual(t, len(tok), 64, tok)
	}
	got, ok := parseTripToken(ApplyToken(id, &two))
	require.True(t, ok)
	require.True(t, got.apply)
	require.Equal(t, id, got.proposalID)
	require.Equal(t, 2, *got.option)
	got, ok = parseTripToken(ApplyToken(id, nil))
	require.True(t, ok)
	require.Nil(t, got.option)
	got, ok = parseTripToken(DismissToken(id))
	require.True(t, ok)
	require.False(t, got.apply)
	for _, bad := range []string{"p|abc|2|i", "a|nothex|1", "a|" + strings.Repeat("0", 32) + "|-1", "d", "", "a|" + strings.Repeat("g", 32) + "|-"} {
		_, ok := parseTripToken(bad)
		require.False(t, ok, bad)
	}
	require.True(t, IsApplyToken(ApplyToken(id, nil)))
	require.False(t, IsApplyToken(DismissToken(id)))
	require.False(t, IsApplyToken("p|abc|2|i"))
}

func TestHandle_ProposalsBecomeOneMessageEach(t *testing.T) {
	svc, repo, answerer := newService(t)
	planner := &fakePlanner{cards: []TripCard{
		{Text: "Set the trip's dates to 12 Nov – 17 Nov 2026.", Buttons: []Button{{Label: "Confirm", Data: "a|x|-"}}},
		{Text: "4★ hotels in Lisbon: pick one.", Buttons: []Button{{Label: "Stay at 1", Data: "a|y|0"}}},
	}}
	svc.WithTripPlanner(planner)
	linkChat(t, repo, "9001")
	out := send(t, svc, "9001", "12 to 17 Nov, 4-star hotels")
	require.Equal(t, tripsLead, out.Text)
	require.Len(t, out.Extra, 2)
	require.Equal(t, "Confirm", out.Extra[0].Buttons[0].Label)
	require.Equal(t, "Stay at 1", out.Extra[1].Buttons[0].Label)
	require.Zero(t, answerer.calls, "a turn that proposes changes is not also answered")
	require.Equal(t, []string{"12 to 17 Nov, 4-star hotels"}, planner.proposed)
}

func TestHandle_NoProposalsOrAPlannerErrorStillAnswers(t *testing.T) {
	for name, planner := range map[string]*fakePlanner{
		"nothing to change": {},
		"planner failed":    {err: errors.New("model down")},
	} {
		t.Run(name, func(t *testing.T) {
			svc, repo, answerer := newService(t)
			svc.WithTripPlanner(planner)
			linkChat(t, repo, "9001")
			out := send(t, svc, "9001", "what's good for dinner?")
			require.Empty(t, out.Extra)
			require.Equal(t, 1, answerer.calls)
			require.Equal(t, "here is a plan", out.Text)
		})
	}
}

func TestHandleAction_TripTokensNeverReachThePaginator(t *testing.T) {
	svc, repo, pages, _ := newPagedService(t)
	planner := &fakePlanner{}
	svc.WithTripPlanner(planner)
	linkChat(t, repo, "9001")
	id := uuid.New()
	one := 1
	out, err := svc.HandleAction(context.Background(), InboundAction{Platform: PlatformTelegram, ChatID: "9001", Data: ApplyToken(id, &one)})
	require.NoError(t, err)
	require.Equal(t, "Dates set: 12 Nov – 17 Nov 2026.", out.Text)
	require.Equal(t, []string{id.String() + "/1"}, planner.applied)
	out, err = svc.HandleAction(context.Background(), InboundAction{Platform: PlatformTelegram, ChatID: "9001", Data: DismissToken(id)})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{id}, planner.dismissed)
	require.Contains(t, out.Text, "leave that")
	require.Zero(t, pages.calls, "trip buttons are not pages")
}

func TestHandleAction_TripTokenWithoutAPlanner(t *testing.T) {
	svc, repo, pages, _ := newPagedService(t)
	linkChat(t, repo, "9001")
	out, err := svc.HandleAction(context.Background(), InboundAction{Platform: PlatformTelegram, ChatID: "9001", Data: ApplyToken(uuid.New(), nil)})
	require.NoError(t, err)
	require.Contains(t, out.Text, "cannot change trips")
	require.Zero(t, pages.calls)
}
