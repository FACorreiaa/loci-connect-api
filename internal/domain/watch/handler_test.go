package watch

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

func asUser(id uuid.UUID) context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, id.String())
}

func codeOf(t *testing.T, err error) connect.Code {
	t.Helper()
	var ce *connect.Error
	require.True(t, errors.As(err, &ce), "want a connect error, got %v", err)
	return ce.Code()
}

func newTestHandler() (*Handler, *fakeRepo, *fakeSessions) {
	s, repo, sessions := newTestService(&fakeGen{reply: "ok"})
	return NewHandler(s), repo, sessions
}

func TestHandlerRequiresAuthentication(t *testing.T) {
	h, _, _ := newTestHandler()
	ctx := context.Background()

	_, err := h.ProposeWatch(ctx, connect.NewRequest(&chatv1.ProposeWatchRequest{Text: "every day x"}))
	require.Equal(t, connect.CodeUnauthenticated, codeOf(t, err))
	_, err = h.CreateWatch(ctx, connect.NewRequest(&chatv1.CreateWatchRequest{}))
	require.Equal(t, connect.CodeUnauthenticated, codeOf(t, err))
	_, err = h.ListWatches(ctx, connect.NewRequest(&chatv1.ListWatchesRequest{}))
	require.Equal(t, connect.CodeUnauthenticated, codeOf(t, err))
	_, err = h.DeleteWatch(ctx, connect.NewRequest(&chatv1.DeleteWatchRequest{}))
	require.Equal(t, connect.CodeUnauthenticated, codeOf(t, err))
}

func TestProposeWatchReturnsCard(t *testing.T) {
	h, repo, _ := newTestHandler()
	res, err := h.ProposeWatch(asUser(uuid.New()), connect.NewRequest(&chatv1.ProposeWatchRequest{
		Text:     "every morning at 8 tell me if it will rain in Lisbon",
		Timezone: "Europe/Lisbon",
	}))
	require.NoError(t, err)
	p := res.Msg.GetProposal()
	require.Equal(t, "Tell me if it will rain in Lisbon", p.GetTitle())
	require.Equal(t, "Every day at 08:00", p.GetScheduleHuman())
	require.EqualValues(t, 1440, p.GetIntervalMinutes())
	require.Equal(t, "tell me if it will rain in Lisbon", p.GetSpec())
	require.NotNil(t, p.GetFirstRunAt())
	require.Empty(t, repo.watches, "proposing stores nothing")
}

func TestProposeWatchBadZoneIsInvalidArgument(t *testing.T) {
	h, _, _ := newTestHandler()
	_, err := h.ProposeWatch(asUser(uuid.New()), connect.NewRequest(&chatv1.ProposeWatchRequest{
		Text: "every day check the tide", Timezone: "Nowhere/Land",
	}))
	require.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))
}

func TestCreateListDeleteRoundTrip(t *testing.T) {
	h, _, sessions := newTestHandler()
	user := uuid.New()
	sid := sessions.add(user, "Lisbon")
	ctx := asUser(user)

	created, err := h.CreateWatch(ctx, connect.NewRequest(&chatv1.CreateWatchRequest{
		SessionId: sid.String(),
		Proposal: &chatv1.WatchProposal{
			Title:           "Rain in Lisbon",
			ScheduleHuman:   "Every day at 08:00",
			IntervalMinutes: 1440,
			Spec:            "tell me if it will rain in Lisbon",
			FirstRunAt:      timestamppb.New(fixedNow.Add(24 * time.Hour)),
		},
	}))
	require.NoError(t, err)
	w := created.Msg.GetWatch()
	require.Equal(t, sid.String(), w.GetSessionId())
	require.True(t, w.GetEnabled())
	require.Nil(t, w.GetLastRunAt())

	conf := created.Msg.GetConfirmation()
	require.Equal(t, chatv1.MessageOrigin_MESSAGE_ORIGIN_PROACTIVE, conf.GetOrigin())
	require.Equal(t, "Standing task", conf.GetSourceLabel())
	require.Equal(t, chatv1.MessageRole_MESSAGE_ROLE_ASSISTANT, conf.GetRole())
	require.Contains(t, conf.GetContent(), "Got it — I'll watch “Rain in Lisbon”")

	listed, err := h.ListWatches(ctx, connect.NewRequest(&chatv1.ListWatchesRequest{SessionId: sid.String()}))
	require.NoError(t, err)
	require.Len(t, listed.Msg.GetWatches(), 1)
	require.Equal(t, w.GetId(), listed.Msg.GetWatches()[0].GetId())

	// Another user sees none of it and cannot delete it.
	other := asUser(uuid.New())
	listed, err = h.ListWatches(other, connect.NewRequest(&chatv1.ListWatchesRequest{}))
	require.NoError(t, err)
	require.Empty(t, listed.Msg.GetWatches())
	_, err = h.DeleteWatch(other, connect.NewRequest(&chatv1.DeleteWatchRequest{Id: w.GetId()}))
	require.Equal(t, connect.CodeNotFound, codeOf(t, err))

	_, err = h.DeleteWatch(ctx, connect.NewRequest(&chatv1.DeleteWatchRequest{Id: w.GetId()}))
	require.NoError(t, err)
	listed, err = h.ListWatches(ctx, connect.NewRequest(&chatv1.ListWatchesRequest{}))
	require.NoError(t, err)
	require.Empty(t, listed.Msg.GetWatches())
}

func TestCreateWatchErrorsMapToCodes(t *testing.T) {
	h, _, sessions := newTestHandler()
	user := uuid.New()
	ctx := asUser(user)
	valid := &chatv1.WatchProposal{Title: "t", ScheduleHuman: "Every hour", IntervalMinutes: 60, Spec: "s"}

	// Someone else's thread.
	foreign := sessions.add(uuid.New(), "")
	_, err := h.CreateWatch(ctx, connect.NewRequest(&chatv1.CreateWatchRequest{SessionId: foreign.String(), Proposal: valid}))
	require.Equal(t, connect.CodeNotFound, codeOf(t, err))

	// Malformed session id.
	_, err = h.CreateWatch(ctx, connect.NewRequest(&chatv1.CreateWatchRequest{SessionId: "nope", Proposal: valid}))
	require.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))

	// Missing proposal.
	own := sessions.add(user, "")
	_, err = h.CreateWatch(ctx, connect.NewRequest(&chatv1.CreateWatchRequest{SessionId: own.String()}))
	require.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))

	// Too frequent.
	tooOften := &chatv1.WatchProposal{Title: "t", ScheduleHuman: "x", IntervalMinutes: 5, Spec: "s"}
	_, err = h.CreateWatch(ctx, connect.NewRequest(&chatv1.CreateWatchRequest{SessionId: own.String(), Proposal: tooOften}))
	require.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))

	// Over the per-user cap.
	for range MaxWatchesPerUser {
		_, err = h.CreateWatch(ctx, connect.NewRequest(&chatv1.CreateWatchRequest{SessionId: own.String(), Proposal: valid}))
		require.NoError(t, err)
	}
	_, err = h.CreateWatch(ctx, connect.NewRequest(&chatv1.CreateWatchRequest{SessionId: own.String(), Proposal: valid}))
	require.Equal(t, connect.CodeResourceExhausted, codeOf(t, err))
}

func TestInternalErrorsDoNotLeakDetail(t *testing.T) {
	err := toConnectError(errors.New(`pq: relation "chat_watches" does not exist`))
	require.Equal(t, connect.CodeInternal, codeOf(t, err))
	require.NotContains(t, err.Error(), "chat_watches")
}
