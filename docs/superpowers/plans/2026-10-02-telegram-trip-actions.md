# Telegram trip actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The traveller gets the chat agent's trip proposals in Telegram as messages with buttons. Tapping a button applies or dismisses the proposal.

**Architecture:**
- **Which trip:** a Telegram message is about a trip when the user's latest chat session is the one that trip was generated in (`trips.source_session_id`).
- **Where it happens:** in `messaging.Service.Handle`, before the normal answer. A new `messaging.TripPlanner` interface asks `tripaction.Service.Propose`.
- **One message per proposal:** each proposal goes out as its own message with buttons, carried in a new `OutboundMessage.Extra`. A press clears only that message's keyboard.
- **Buttons:**
  - `a|<32 hex>|<option or ->` applies; `d|<32 hex>` dismisses. Both stay at 36 bytes or less.
  - `HandleAction` dispatches on these prefixes before the paginator.
  - Apply goes through a new `tripaction.Service.ApplyCurrent`. It applies against the trip's current version, since a 64-byte button cannot carry the version it was shown with.
  - The bridge gives apply presses a 3-minute timeout, because a re-plan runs a model generation.

**Tech Stack:** Go, pgx, testify. No proto change.

**Spec:** `docs/superpowers/specs/2026-09-30-trip-workflow-agent-actions-design.md` (Surfaces, Telegram). This is plan 5 of 5. Plans 1–3 are live.

## Global Constraints

- **Telegram `callback_data` ≤ 64 bytes.** Every token is tested against the limit.
- **Never lose the answer.** If proposing fails or finds nothing, the message is answered exactly as today.
- **Quota.** Proposing spends the turn's quota, which `Handle` has already spent. Button presses spend none (the existing `HandleAction` rule). They are rate-limited by the bridge's `perChatActions`.
- **Plain text only.** Telegram replies use no `parse_mode`. Links are bare https URLs.
- **Worktree:** `/private/tmp/api-telegram`, branch `feat/telegram-trip-actions`. `git add` explicit paths.
- **Integration tests need:** `DOCKER_HOST=unix:///Users/fernandocorreiachill/.orbstack/run/docker.sock TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock TESTCONTAINERS_RYUK_DISABLED=true`.

## Review Focus

1. **A user whose latest conversation produced no trip** gets the normal answer, and no extraction call is made. Pinned in Task 4.
2. **A planner error, or one that is slow** (extraction is bounded by `proposeTimeout`, 20s, inside `Propose`), never loses the answer. Pinned in Task 3.
3. **Tapping one proposal's button** strips only that message's keyboard. The other proposal messages keep theirs. Pinned in Task 5.
4. **A double tap, an expired proposal, or a proposal applied from the app first** returns plain-language text, never an error string. Pinned in Task 4.
5. **A re-plan press** gets the long timeout. A page press keeps 20s. Pinned in Task 5.

---

### Task 1: Trip for a session

**Files:**
- Modify `internal/domain/trip/repository.go`.
- Test: append to `internal/domain/trip/plan_integration_test.go`.

**Interfaces:**
- Produces:
  - `type SessionTrips interface{ LatestForSession(ctx, userID, sessionID uuid.UUID) (*Trip, error) }`
  - `func NewSessionTrips(db *pgxpool.Pool, logger *slog.Logger) SessionTrips`
  - It returns `ErrNotFound` when the session produced no trip for that user.

- [ ] **Step 1: Failing test** (append)

```go
func TestRepository_LatestForSession(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, logger)
	lookup := NewSessionTrips(testTripDB, logger)
	userID := newTripUser(t, "session-trip-"+uuid.NewString()+"@loci.test")
	session := uuid.New()
	s := session.String()

	saved, err := repo.SaveTrip(ctx, &Trip{UserID: userID, CityName: "Lisbon", Title: "Lisbon", SourceSessionID: &s}, 0)
	require.NoError(t, err)

	got, err := lookup.LatestForSession(ctx, userID, session)
	require.NoError(t, err)
	require.Equal(t, saved.ID, got.ID)

	_, err = lookup.LatestForSession(ctx, userID, uuid.New())
	require.ErrorIs(t, err, ErrNotFound, "a conversation that produced no trip")
	_, err = lookup.LatestForSession(ctx, newTripUser(t, "other-"+uuid.NewString()+"@loci.test"), session)
	require.ErrorIs(t, err, ErrNotFound, "someone else's trip")
}
```

- [ ] **Step 2:** Run `go test -tags=integration -p 1 -count=1 ./internal/domain/trip/ -run LatestForSession` (with the Docker env).
Expected: FAIL to compile.

- [ ] **Step 3: Implement** (append to `repository.go`)

```go
// SessionTrips finds the trip a chat conversation produced, for a chat
// platform that knows the conversation but not the trip (Telegram).
type SessionTrips interface {
	LatestForSession(ctx context.Context, userID, sessionID uuid.UUID) (*Trip, error)
}

// NewSessionTrips is the Postgres SessionTrips.
func NewSessionTrips(db *pgxpool.Pool, logger *slog.Logger) SessionTrips {
	return &repository{db: db, logger: logger.With(slog.String("component", "trip-session-lookup"))}
}

// LatestForSession is the user's most recently edited trip generated in
// sessionID (trips.source_session_id), or ErrNotFound.
func (r *repository) LatestForSession(ctx context.Context, userID, sessionID uuid.UUID) (*Trip, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT id FROM trips WHERE user_id = $1 AND source_session_id = $2
		ORDER BY updated_at DESC LIMIT 1`, userID, sessionID.String()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("trip for session: %w", err)
	}
	return r.GetTrip(ctx, id, userID)
}
```

- [ ] **Step 4:** Run it.
Expected: PASS.

- [ ] **Step 5:** Commit: `feat(trip): find the trip a conversation produced`.

---

### Task 2: `tripaction.Service.ApplyCurrent`

**Files:**
- Modify `internal/domain/tripaction/service.go`.
- Test in `internal/domain/tripaction/service_test.go`.

**Interfaces:**
- Produces: `func (s *Service) ApplyCurrent(ctx context.Context, userID, proposalID uuid.UUID, option *int) (*trip.Trip, *locitypes.ConversationMessage, error)`

- [ ] **Step 1: Failing test**

```go
func TestApplyCurrent_UsesTheTripsVersionNow(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	ps := proposeAll(t, f)
	f.plans.trips.t.Version = 9 // edited elsewhere since the proposal
	tr, msg, err := f.svc.ApplyCurrent(context.Background(), f.uid, ps[0].ID, nil)
	require.NoError(t, err)
	require.EqualValues(t, 10, tr.Version)
	require.NotNil(t, msg)
	_, _, err = f.svc.ApplyCurrent(context.Background(), f.uid, ps[0].ID, nil)
	require.ErrorIs(t, err, ErrNotPending, "still applied once")
	_, _, err = f.svc.ApplyCurrent(context.Background(), uuid.New(), ps[1].ID, intp(0))
	require.ErrorIs(t, err, ErrNotFound)
}
```

- [ ] **Step 2:** Run `go test ./internal/domain/tripaction/ -run ApplyCurrent`.
Expected: FAIL to compile.

- [ ] **Step 3: Implement** (after `Apply`)

```go
// ApplyCurrent applies a proposal against the trip as it is now. It is for
// surfaces whose button cannot carry the version it was shown with: a
// Telegram callback holds 64 bytes. The proposal still applies once and still
// expires; a change made in between is simply built on.
func (s *Service) ApplyCurrent(ctx context.Context, userID, proposalID uuid.UUID, option *int) (*trip.Trip, *locitypes.ConversationMessage, error) {
	p, err := s.d.Store.Get(ctx, proposalID, userID)
	if err != nil {
		return nil, nil, err
	}
	t, err := s.d.Trips.GetTrip(ctx, p.TripID, userID)
	if err != nil {
		return nil, nil, err
	}
	return s.Apply(ctx, userID, proposalID, option, t.Version)
}
```

- [ ] **Step 4:** Run `go test ./internal/domain/tripaction/`.
Expected: PASS.

- [ ] **Step 5:** Commit: `feat(tripaction): ApplyCurrent for surfaces without a version`.

---

### Task 3: Messaging — trip cards, buttons, dispatch

**Files:**
- Create `internal/domain/messaging/trip_actions.go`.
- Modify `internal/domain/messaging/service.go`:
  - add `Extra` to `OutboundMessage`;
  - add the `trips TripPlanner` field and `WithTripPlanner`;
  - add the `Handle` branch;
  - add the `HandleAction` dispatch;
  - add the `/help` line.
- Test in `internal/domain/messaging/trip_actions_test.go`.

**Interfaces:**
- Produces:
  - `ApplyToken(id uuid.UUID, option *int) string`, `DismissToken(id uuid.UUID) string`, `IsApplyToken(data string) bool`
  - `type TripCard struct{ Text string; Buttons []Button }`
  - `type TripPlanner interface{ Propose(ctx, userID uuid.UUID, email, text string) ([]TripCard, error); Apply(ctx, userID uuid.UUID, email string, proposalID uuid.UUID, option *int) string; Dismiss(ctx, userID uuid.UUID, proposalID uuid.UUID) string }`
  - `(*Service).WithTripPlanner(TripPlanner) *Service`
  - `OutboundMessage.Extra []OutboundMessage`

- [ ] **Step 1: Failing tests.** Reuse the existing fakes: `newFakeRepo`, `spyAnswerer`, `linkChat`, `send`, and `spyPaginator` from `actions_test.go`. Read those files first and match their names.

```go
package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type fakePlanner struct {
	cards    []TripCard
	err      error
	proposed []string
	applied  []string
	dismissed []uuid.UUID
}

func (f *fakePlanner) Propose(_ context.Context, _ uuid.UUID, _, text string) ([]TripCard, error) {
	f.proposed = append(f.proposed, text)
	return f.cards, f.err
}

func (f *fakePlanner) Apply(_ context.Context, _ uuid.UUID, _ string, id uuid.UUID, option *int) string {
	o := "-"
	if option != nil {
		o = string(rune('0' + *option))
	}
	f.applied = append(f.applied, id.String()+"/"+o)
	return "Dates set: 12 Nov – 17 Nov 2026."
}

func (f *fakePlanner) Dismiss(_ context.Context, _ uuid.UUID, id uuid.UUID) string {
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
	for _, bad := range []string{"p|abc|2|i", "a|nothex|1", "a|" + strings.Repeat("0", 32) + "|-1", "d", ""} {
		_, ok := parseTripToken(bad)
		require.False(t, ok, bad)
	}
	require.True(t, IsApplyToken(ApplyToken(id, nil)))
	require.False(t, IsApplyToken(DismissToken(id)))
}

func TestHandle_ProposalsBecomeOneMessageEach(t *testing.T) {
	svc, repo, answerer := newService(t) // use the existing helper's return shape
	planner := &fakePlanner{cards: []TripCard{
		{Text: "Set the trip's dates to 12 Nov – 17 Nov 2026.", Buttons: []Button{{Label: "Confirm", Data: "a|x|-"}}},
		{Text: "4★ hotels in Lisbon: pick one.", Buttons: []Button{{Label: "Stay at 1", Data: "a|y|0"}}},
	}}
	svc.WithTripPlanner(planner)
	linkChat(t, repo, "9001")
	out := send(t, svc, "9001", "12 to 17 Nov, 4-star hotels")
	require.Len(t, out.Extra, 2)
	require.Equal(t, "Confirm", out.Extra[0].Buttons[0].Label)
	require.Empty(t, answerer.calls, "a turn that proposes changes is not also answered")
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
			require.Len(t, answerer.calls, 1)
		})
	}
}

func TestHandleAction_TripTokensNeverReachThePaginator(t *testing.T) {
	svc, repo, pages := newPagedService(t) // existing helper
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
	require.Empty(t, pages.calls, "trip buttons are not pages")
}
```

Adapt the helper calls (`newService`, `newPagedService`, `send`, `linkChat`, the spies' call-log field names) to the signatures in `service_test.go` and `actions_test.go`. Record any adaptation as a ruling.

- [ ] **Step 2:** Run `go test ./internal/domain/messaging/ -run 'TripTokens|Proposals|NoProposals|TripTokensNever'`.
Expected: FAIL to compile.

- [ ] **Step 3: Implement** `internal/domain/messaging/trip_actions.go`:

```go
package messaging

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Trip action buttons. A press carries the proposal's id (and, for a pick,
// the option), never the change itself: the proposal lives on the server.
// "a|<32 hex>|<option or ->" is at most 36 bytes, well under Telegram's 64.
const (
	applyTokenPrefix   = "a"
	dismissTokenPrefix = "d"
	tripTokenSep       = "|"
	noOption           = "-"
)

func hex32(id uuid.UUID) string { return strings.ReplaceAll(id.String(), "-", "") }

// ApplyToken is the data of a button that applies a trip proposal.
func ApplyToken(proposalID uuid.UUID, option *int) string {
	opt := noOption
	if option != nil {
		opt = strconv.Itoa(*option)
	}
	return applyTokenPrefix + tripTokenSep + hex32(proposalID) + tripTokenSep + opt
}

// DismissToken is the data of a button that drops a trip proposal.
func DismissToken(proposalID uuid.UUID) string {
	return dismissTokenPrefix + tripTokenSep + hex32(proposalID)
}

// IsApplyToken reports whether a press applies a trip proposal: the one press
// that can run a model generation (a re-plan), so it gets longer than a page.
func IsApplyToken(data string) bool {
	return strings.HasPrefix(data, applyTokenPrefix+tripTokenSep)
}

type tripToken struct {
	apply      bool
	proposalID uuid.UUID
	option     *int
}

func parseTripToken(data string) (tripToken, bool) {
	parts := strings.Split(data, tripTokenSep)
	switch {
	case len(parts) == 3 && parts[0] == applyTokenPrefix:
	case len(parts) == 2 && parts[0] == dismissTokenPrefix:
	default:
		return tripToken{}, false
	}
	if len(parts[1]) != 32 {
		return tripToken{}, false
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return tripToken{}, false
	}
	tok := tripToken{apply: parts[0] == applyTokenPrefix, proposalID: id}
	if tok.apply && parts[2] != noOption {
		n, err := strconv.Atoi(parts[2])
		if err != nil || n < 0 {
			return tripToken{}, false
		}
		tok.option = &n
	}
	return tok, true
}

// TripCard is one proposed trip change: a message and its buttons.
type TripCard struct {
	Text    string
	Buttons []Button
}

// TripPlanner proposes and applies changes to the trip a chat is about.
type TripPlanner interface {
	// Propose returns cards when text asks to change the trip the user's
	// latest conversation produced, and none (with a nil error) otherwise.
	Propose(ctx context.Context, userID uuid.UUID, email, text string) ([]TripCard, error)
	// Apply makes the change and returns what to tell the traveller: the
	// confirmation, or in plain words why it could not be made.
	Apply(ctx context.Context, userID uuid.UUID, email string, proposalID uuid.UUID, option *int) string
	// Dismiss drops the proposal and returns the reply.
	Dismiss(ctx context.Context, userID uuid.UUID, proposalID uuid.UUID) string
}

// WithTripPlanner lets messages about a trip propose changes to it, applied
// with a button. Without it every message is answered as before.
func (s *Service) WithTripPlanner(p TripPlanner) *Service {
	s.trips = p
	return s
}

// tripsLead heads a turn that proposes changes; each proposal follows as its
// own message, so a press clears only its own buttons.
const tripsLead = "Here's what I can change on your trip. Tap to confirm:"
```

In `service.go`:
- **`OutboundMessage`:** add the field

  ```go
  	// Extra replies follow this one, each with its own buttons. Trip proposals
  	// go one per message, so pressing one clears only its own keyboard.
  	Extra []OutboundMessage
  ```

- **`Service` struct:** add `trips TripPlanner`.
- **`Handle`:** insert this immediately before `answer, next, err := s.answerer.Answer(...)`:

  ```go
  	if s.trips != nil {
  		cards, err := s.trips.Propose(ctx, link.UserID, link.Email, text)
  		if err != nil {
  			// Proposing is an extra: its failure never costs the traveller the answer.
  			s.logger.WarnContext(ctx, "could not propose trip changes",
  				slog.String("user_id", link.UserID.String()), slog.String("error", err.Error()))
  		} else if len(cards) > 0 {
  			out := OutboundMessage{Text: tripsLead}
  			for _, c := range cards {
  				out.Extra = append(out.Extra, OutboundMessage{Text: c.Text, Buttons: c.Buttons})
  			}
  			return out, nil
  		}
  	}
  ```

- **`HandleAction`:** move the `paginator == nil` check below `TouchLink`, and insert this between them:

  ```go
  	if tok, ok := parseTripToken(in.Data); ok {
  		if s.trips == nil {
  			return OutboundMessage{Text: "I cannot change trips from here right now."}, nil
  		}
  		if tok.apply {
  			return OutboundMessage{Text: s.trips.Apply(ctx, link.UserID, link.Email, tok.proposalID, tok.option)}, nil
  		}
  		return OutboundMessage{Text: s.trips.Dismiss(ctx, link.UserID, tok.proposalID)}, nil
  	}
  ```

- **`/help`:** add a line to the text in `handleCommand`, in the help's existing voice: "Right after I plan a trip, ask me to change it — dates, hotels by stars, more days, flights — and I'll offer buttons to confirm."

- [ ] **Step 4:** Run `go test ./internal/domain/messaging/...`.
Expected: PASS.

- [ ] **Step 5:** Commit: `feat(messaging): trip proposals as Telegram messages with buttons`.

---

### Task 4: The planner (`internal/domain/messaging/tripcards`)

**Files:**
- Create `internal/domain/messaging/tripcards/planner.go`.
- Test in `internal/domain/messaging/tripcards/planner_test.go`.

**Interfaces:**
- Consumes:
  - `messaging.TripCard`, `messaging.Button`, `ApplyToken`, `DismissToken` (Task 3)
  - `trip.SessionTrips` (Task 1)
  - `tripaction.Service.Propose`, `ApplyCurrent` (Task 2), `Dismiss`
- Produces `func New(sessions Sessions, trips trip.SessionTrips, actions Actions, logger *slog.Logger) *Planner`, which implements `messaging.TripPlanner`.

- [ ] **Step 1: Failing tests**

```go
package tripcards

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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
		{ID: uuid.New(), TripID: tripID, Summary: "4★ hotels in Lisbon: pick one to stay at.", Action: tripaction.Action{Kind: tripaction.KindSearchHotels},
			Options: []tripaction.Option{
				{Label: "Hotel Avenida · 4★", Detail: "Av. da Liberdade", Stay: &trip.TripStay{Name: "Hotel Avenida"}},
				{Label: "Pestana · 4★", Stay: &trip.TripStay{Name: "Pestana", BookingURL: &link}},
			}},
		{ID: uuid.New(), TripID: tripID, Summary: "Flights NYC → Lisbon", Action: tripaction.Action{Kind: tripaction.KindSearchFlights},
			Options: []tripaction.Option{{Label: "Save this flight search", Flight: &trip.TripFlight{Links: []flights.Link{{Label: "Google Flights", URL: "https://g.example"}}}}}},
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
		nil:                       "Dates set",
		tripaction.ErrNotPending:  "already used",
		tripaction.ErrExpired:     "expired",
		tripaction.ErrNotFound:    "can't find",
		trip.ErrVersionConflict:   "changed while",
		trip.ErrInvalidEdit:       "can't be made",
		errors.New("db down"):     "Something went wrong",
	}
	for err, want := range cases {
		p := New(fakeSessions{}, fakeTrips{}, &fakeActions{applyErr: err}, quiet)
		require.Contains(t, p.Apply(context.Background(), uuid.New(), "a@b.c", uuid.New(), nil), want)
	}
}
```

Add `"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"` to the test's imports.

- [ ] **Step 2:** Run `go test ./internal/domain/messaging/tripcards/`.
Expected: FAIL to compile.

- [ ] **Step 3: Implement** `planner.go`:

```go
// Package tripcards turns the chat agent's trip proposals into chat-platform
// messages with buttons (Telegram), and applies the one a button names.
package tripcards

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Sessions finds the user's latest conversation (the chat service).
type Sessions interface {
	GetUserChatSessions(ctx context.Context, userID uuid.UUID, page, limit int) (*locitypes.ChatSessionsResponse, error)
}

// Actions is the trip-action service.
type Actions interface {
	Propose(ctx context.Context, userID, tripID, sessionID uuid.UUID, message string) ([]tripaction.Proposal, error)
	ApplyCurrent(ctx context.Context, userID, proposalID uuid.UUID, option *int) (*trip.Trip, *locitypes.ConversationMessage, error)
	Dismiss(ctx context.Context, userID, proposalID uuid.UUID) error
}

type Planner struct {
	sessions Sessions
	trips    trip.SessionTrips
	actions  Actions
	logger   *slog.Logger
}

func New(sessions Sessions, trips trip.SessionTrips, actions Actions, logger *slog.Logger) *Planner {
	return &Planner{sessions: sessions, trips: trips, actions: actions, logger: logger}
}

var _ messaging.TripPlanner = (*Planner)(nil)

// asUser is the context the chat service expects for a linked chat's user,
// as chatbridge builds it.
func asUser(ctx context.Context, userID uuid.UUID, email string) context.Context {
	return interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: userID.String(), Email: email})
}

// Propose looks for changes only when the user's latest conversation produced
// a trip. Otherwise "4 days in Rome" would be read as a change to whatever
// trip they planned last.
func (p *Planner) Propose(ctx context.Context, userID uuid.UUID, email, text string) ([]messaging.TripCard, error) {
	ctx = asUser(ctx, userID, email)
	res, err := p.sessions.GetUserChatSessions(ctx, userID, 1, 1)
	if err != nil || res == nil || len(res.Sessions) == 0 {
		return nil, err
	}
	sessionID := res.Sessions[0].ID
	t, err := p.trips.LatestForSession(ctx, userID, sessionID)
	if errors.Is(err, trip.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	proposals, err := p.actions.Propose(ctx, userID, t.ID, sessionID, text)
	if err != nil || len(proposals) == 0 {
		return nil, err
	}
	cards := make([]messaging.TripCard, 0, len(proposals))
	for _, pr := range proposals {
		cards = append(cards, card(pr))
	}
	return cards, nil
}

// card is one proposal as plain text and buttons. Hotels list their options
// with a numbered "Stay at N" each; a flight shows its links (Telegram
// previews bare URLs) and saves with one press; the rest confirm.
func card(p tripaction.Proposal) messaging.TripCard {
	var b strings.Builder
	b.WriteString(p.Summary)
	var buttons []messaging.Button
	switch p.Action.Kind {
	case tripaction.KindSearchHotels:
		for i, o := range p.Options {
			fmt.Fprintf(&b, "\n%d. %s", i+1, o.Label)
			if o.Detail != "" {
				b.WriteString(" — " + o.Detail)
			}
			if o.Stay != nil && o.Stay.BookingURL != nil {
				b.WriteString("\n   " + *o.Stay.BookingURL)
			}
			opt := i
			buttons = append(buttons, messaging.Button{Label: fmt.Sprintf("Stay at %d", i+1), Data: messaging.ApplyToken(p.ID, &opt)})
		}
	case tripaction.KindSearchFlights:
		if len(p.Options) > 0 && p.Options[0].Flight != nil {
			for _, l := range p.Options[0].Flight.Links {
				fmt.Fprintf(&b, "\n%s: %s", l.Label, l.URL)
			}
			zero := 0
			buttons = append(buttons, messaging.Button{Label: "Save this flight", Data: messaging.ApplyToken(p.ID, &zero)})
		}
	default:
		buttons = append(buttons, messaging.Button{Label: "Confirm", Data: messaging.ApplyToken(p.ID, nil)})
	}
	buttons = append(buttons, messaging.Button{Label: "Not now", Data: messaging.DismissToken(p.ID)})
	return messaging.TripCard{Text: b.String(), Buttons: buttons}
}

// Apply makes the change and says, in plain words, what happened.
func (p *Planner) Apply(ctx context.Context, userID uuid.UUID, email string, proposalID uuid.UUID, option *int) string {
	_, msg, err := p.actions.ApplyCurrent(asUser(ctx, userID, email), userID, proposalID, option)
	switch {
	case err == nil:
		if msg != nil && msg.Content != "" {
			return msg.Content
		}
		return "Done."
	case errors.Is(err, tripaction.ErrNotPending):
		return "That one was already used or dismissed."
	case errors.Is(err, tripaction.ErrExpired):
		return "That suggestion has expired. Ask me again."
	case errors.Is(err, tripaction.ErrNotFound), errors.Is(err, trip.ErrNotFound):
		return "I can't find that suggestion any more."
	case errors.Is(err, tripaction.ErrNoOption):
		return "Pick one of the options first."
	case errors.Is(err, trip.ErrVersionConflict):
		return "The trip changed while I was working on it. Ask me again."
	case errors.Is(err, trip.ErrInvalidEdit):
		return "That change can't be made to this trip."
	default:
		p.logger.ErrorContext(ctx, "could not apply a trip change from chat",
			slog.String("user_id", userID.String()), slog.String("error", err.Error()))
		return "Something went wrong making that change. Try again in a moment."
	}
}

// Dismiss drops the proposal. A second press, or one already used, reads the same.
func (p *Planner) Dismiss(ctx context.Context, userID uuid.UUID, proposalID uuid.UUID) string {
	if err := p.actions.Dismiss(ctx, userID, proposalID); err != nil && !errors.Is(err, tripaction.ErrNotPending) {
		p.logger.WarnContext(ctx, "could not dismiss a trip change", slog.String("error", err.Error()))
	}
	return "Okay, I'll leave that."
}
```

Check two names against the code before running:
- **Claims:** `interceptors.ContextWithClaims` and the `Claims` field names, against `chatbridge.go:80`.
- **Session id type:** `locitypes.ChatSession.ID`'s type. If it is a string, parse it.

- [ ] **Step 4:** Run `go test ./internal/domain/messaging/...`.
Expected: PASS.

- [ ] **Step 5:** Commit: `feat(messaging): plan trip changes from a chat — the trip its conversation produced`.

---

### Task 5: Telegram bridge — extra messages and the apply timeout

**Files:**
- Modify `internal/domain/messaging/telegram/bridge.go`.
- Test in `internal/domain/messaging/telegram/buttons_test.go`.

- [ ] **Step 1: Failing tests.** Use the existing fake API server and `newBridge` from `buttons_test.go`, and match their helper names:
  - `TestExtraRepliesEachCarryTheirOwnKeyboard`: a handler whose `Handle` returns `OutboundMessage{Text: "lead", Extra: [{Text: "dates", Buttons: [Confirm]}, {Text: "hotels", Buttons: [Stay at 1]}]}`.
    - Expect three `sendMessage` calls in order: "lead" with no keyboard, then "dates", then "hotels", each with its own one-button keyboard.
  - `TestApplyPressGetsTheLongTimeout`: a handler that records `ctx.Deadline()` in `HandleAction`.
    - With `Data = messaging.ApplyToken(uuid.New(), nil)`, the deadline is more than 2 minutes away.
    - With `Data = "p|abc|2|i"`, it is 20s or less.

- [ ] **Step 2:** Run `go test ./internal/domain/messaging/telegram/ -run 'ExtraReplies|LongTimeout'`.
Expected: FAIL.

- [ ] **Step 3: Implement.** In `bridge.go`:

```go
// applyTimeout bounds a press that applies a trip proposal. Re-planning a
// trip's days is a model generation, about a minute on the free chain; a page
// is a read and keeps pageTimeout.
const applyTimeout = 3 * time.Minute
```

In `answerCallback`, replace `pageCtx, cancel := context.WithTimeout(ctx, pageTimeout)` with:

```go
	timeout := pageTimeout
	if messaging.IsApplyToken(q.Data) {
		timeout = applyTimeout
	}
	pageCtx, cancel := context.WithTimeout(ctx, timeout)
```

In `sendWithButtons`, send the extras after the reply:

```go
func (b bridge) sendWithButtons(ctx context.Context, chatID string, out messaging.OutboundMessage) {
	if err := b.client.SendMessageWithMarkup(ctx, chatID, out.Text, keyboardFor(b.logger, out.Buttons)); err != nil {
		b.logger.ErrorContext(ctx, "could not send a telegram reply",
			slog.String("error", err.Error()))
		return
	}
	// One message per trip proposal, so a press clears only its own buttons.
	for _, extra := range out.Extra {
		b.sendWithButtons(ctx, chatID, extra)
	}
}
```

Check that the text-message path's send context (`sendTimeout`) is long enough for up to 5 sends. If the timeout is shorter than about 15s, give the extras their own per-message context.

- [ ] **Step 4:** Run `go test ./internal/domain/messaging/...`.
Expected: PASS.

- [ ] **Step 5:** Commit: `feat(telegram): send trip proposals as their own messages; longer timeout for applying one`.

---

### Task 6: Wiring, suite, ship

- [ ] **Step 1:** In `cmd/api/dependencies.go`, inside the block that builds `tripActions` (after `d.ChatHandler = d.ChatHandler.WithTripActions(tripActions)`), add:

```go
			// Telegram gets the same proposals as buttons. The bridge was built
			// earlier (initMessaging); WithTripPlanner attaches to it in place.
			if d.Messaging != nil {
				d.Messaging.WithTripPlanner(tripcards.New(d.ChatService, trip.NewSessionTrips(d.DB.Pool, d.Logger), tripActions, d.Logger))
			}
```

Add the `tripcards` import. `d.ChatService` must satisfy `tripcards.Sessions`; it already offers `GetUserChatSessions` to chatbridge.

- [ ] **Step 2:** Run `go build ./... && go test -p 4 ./... && go test -tags=integration -p 1 -count=1 ./internal/domain/trip/ ./internal/domain/tripaction/ && make lint-go` (with the Docker env).
Expected: all pass.

- [ ] **Step 3:** Final review, then PR, merge, and a promote PR (`INFRA_TOKEN` is unset, so promote by hand).

- [ ] **Step 4:** Prove it with the local test bot (the memory note "loci-telegram-grounding-images" has the setup):
  1. Plan "3 days in Lisbon".
  2. Then send "12 to 15 November and 4-star hotels".
  3. Expect two button messages.
  4. Tap "Confirm" on the dates card, then "Stay at 1".
  5. The trip shows on web with its dates and stay.

---

## Spec deviations (deliberate)

- **The trip comes from the latest conversation,** not a trip id in the request. Telegram has no trip context, and the spec's "the latest session's trip" is exactly this.
- **Telegram applies against the trip's current version** (`ApplyCurrent`). A 64-byte button cannot carry the version it was shown with.
- **No proto change.** ContinueChat stays as it is; the bridge proposes before answering.
