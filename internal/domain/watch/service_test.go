package watch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// ---- fakes ---------------------------------------------------------------

type fakeRepo struct {
	mu       sync.Mutex
	watches  map[uuid.UUID]Watch
	disabled []uuid.UUID
	claimErr error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{watches: map[uuid.UUID]Watch{}} }

func (r *fakeRepo) Create(_ context.Context, w Watch) (Watch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.ID = uuid.New()
	w.CreatedAt = time.Now()
	r.watches[w.ID] = w
	return w, nil
}

func (r *fakeRepo) CountByUser(_ context.Context, userID uuid.UUID) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, w := range r.watches {
		if w.UserID == userID {
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) List(_ context.Context, userID uuid.UUID, sessionID *uuid.UUID) ([]Watch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []Watch{}
	for _, w := range r.watches {
		if w.UserID == userID && (sessionID == nil || w.SessionID == *sessionID) {
			out = append(out, w)
		}
	}
	return out, nil
}

func (r *fakeRepo) Delete(_ context.Context, userID, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.watches[id]
	if !ok || w.UserID != userID {
		return ErrNotFound
	}
	delete(r.watches, id)
	return nil
}

func (r *fakeRepo) ClaimDue(_ context.Context, now time.Time, limit int) ([]Watch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimErr != nil {
		return nil, r.claimErr
	}
	var due []Watch
	for id, w := range r.watches {
		if len(due) == limit {
			break
		}
		if w.Enabled && !w.NextRunAt.After(now) {
			due = append(due, w)
			w.NextRunAt = nextSlot(w.NextRunAt, w.Interval(), now)
			w.LastRunAt = &now
			r.watches[id] = w
		}
	}
	return due, nil
}

func (r *fakeRepo) Disable(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disabled = append(r.disabled, id)
	if w, ok := r.watches[id]; ok {
		w.Enabled = false
		r.watches[id] = w
	}
	return nil
}

type fakeSessions struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]*locitypes.ChatSession
	appended map[uuid.UUID][]locitypes.ConversationMessage
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{
		sessions: map[uuid.UUID]*locitypes.ChatSession{},
		appended: map[uuid.UUID][]locitypes.ConversationMessage{},
	}
}

func (f *fakeSessions) add(userID uuid.UUID, city string) uuid.UUID {
	id := uuid.New()
	f.sessions[id] = &locitypes.ChatSession{ID: id, UserID: userID, CityName: city}
	return id
}

func (f *fakeSessions) GetSession(_ context.Context, id uuid.UUID) (*locitypes.ChatSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return s, nil
}

func (f *fakeSessions) AddMessageToSession(_ context.Context, id uuid.UUID, m locitypes.ConversationMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[id]; !ok {
		return fmt.Errorf("session %s not found", id)
	}
	f.appended[id] = append(f.appended[id], m)
	return nil
}

type fakeGen struct {
	reply   string
	err     error
	prompts []string
}

func (g *fakeGen) GenerateText(_ context.Context, prompt string, _ *genai.GenerateContentConfig) (string, error) {
	g.prompts = append(g.prompts, prompt)
	return g.reply, g.err
}

type fakeNotifier struct{ posted []Watch }

func (n *fakeNotifier) WatchPosted(_ context.Context, w Watch, _ locitypes.ConversationMessage) {
	n.posted = append(n.posted, w)
}

var fixedNow = time.Date(2026, 9, 23, 10, 30, 0, 0, time.UTC)

func newTestService(gen TextGenerator) (*Service, *fakeRepo, *fakeSessions) {
	repo, sessions := newFakeRepo(), newFakeSessions()
	s := NewService(repo, sessions, gen, nil)
	s.now = func() time.Time { return fixedNow }
	return s, repo, sessions
}

func sampleProposal() Proposal {
	first := fixedNow.Add(21*time.Hour + 30*time.Minute) // tomorrow 08:00
	return Proposal{
		Title:           "Rain in Lisbon",
		ScheduleHuman:   "Every day at 08:00",
		IntervalMinutes: 1440,
		Spec:            "tell me if it will rain in Lisbon",
		FirstRunAt:      &first,
	}
}

// ---- Create --------------------------------------------------------------

func TestCreateStoresAndPostsConfirmation(t *testing.T) {
	s, repo, sessions := newTestService(&fakeGen{})
	user := uuid.New()
	sid := sessions.add(user, "Lisbon")

	w, msg, err := s.Create(context.Background(), user, sid, sampleProposal())
	require.NoError(t, err)
	require.Equal(t, sid, w.SessionID)
	require.Equal(t, *sampleProposal().FirstRunAt, w.NextRunAt)
	require.Len(t, repo.watches, 1)

	require.Equal(t, locitypes.OriginProactive, msg.Origin)
	require.Equal(t, "Standing task", msg.SourceLabel)
	require.Equal(t, locitypes.RoleAssistant, msg.Role)
	require.Equal(t, "Got it — I'll watch “Rain in Lisbon” every day at 08:00 and ping you here with what I find.", msg.Content)
	require.Equal(t, []locitypes.ConversationMessage{msg}, sessions.appended[sid])
}

func TestCreateWithoutFirstRunStartsOneIntervalOut(t *testing.T) {
	s, _, sessions := newTestService(nil)
	user := uuid.New()
	sid := sessions.add(user, "")
	p := sampleProposal()
	p.FirstRunAt = nil
	p.IntervalMinutes = 180

	w, _, err := s.Create(context.Background(), user, sid, p)
	require.NoError(t, err)
	require.Equal(t, fixedNow.Add(3*time.Hour), w.NextRunAt)
}

func TestCreateRefusesSomeoneElsesThread(t *testing.T) {
	s, repo, sessions := newTestService(nil)
	sid := sessions.add(uuid.New(), "Lisbon")

	_, _, err := s.Create(context.Background(), uuid.New(), sid, sampleProposal())
	require.ErrorIs(t, err, ErrSessionAbsent)
	require.Empty(t, repo.watches)
	require.Empty(t, sessions.appended[sid])
}

func TestCreateRefusesMissingThread(t *testing.T) {
	s, _, _ := newTestService(nil)
	_, _, err := s.Create(context.Background(), uuid.New(), uuid.New(), sampleProposal())
	require.ErrorIs(t, err, ErrSessionAbsent)
}

func TestCreateValidatesProposal(t *testing.T) {
	s, _, sessions := newTestService(nil)
	user := uuid.New()
	sid := sessions.add(user, "")

	for name, mutate := range map[string]func(*Proposal){
		"too frequent": func(p *Proposal) { p.IntervalMinutes = 30 },
		"too rare":     func(p *Proposal) { p.IntervalMinutes = MaxIntervalMinutes + 1 },
		"no title":     func(p *Proposal) { p.Title = "  " },
		"no spec":      func(p *Proposal) { p.Spec = "" },
		"no schedule":  func(p *Proposal) { p.ScheduleHuman = "" },
	} {
		t.Run(name, func(t *testing.T) {
			p := sampleProposal()
			mutate(&p)
			_, _, err := s.Create(context.Background(), user, sid, p)
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestCreateEnforcesPerUserLimit(t *testing.T) {
	s, _, sessions := newTestService(nil)
	user := uuid.New()
	sid := sessions.add(user, "")
	for range MaxWatchesPerUser {
		_, _, err := s.Create(context.Background(), user, sid, sampleProposal())
		require.NoError(t, err)
	}
	_, _, err := s.Create(context.Background(), user, sid, sampleProposal())
	require.ErrorIs(t, err, ErrLimitReached)
}

func TestProposeRejectsUnknownZone(t *testing.T) {
	s, _, _ := newTestService(nil)
	_, err := s.Propose(context.Background(), "every day check the tide", "Mars/Olympus_Mons")
	require.ErrorIs(t, err, ErrUnknownZone)
}

// ---- Run -----------------------------------------------------------------

func TestRunAppendsProactiveMessageAndNotifies(t *testing.T) {
	gen := &fakeGen{reply: "  Dry until Thursday; take a light jacket for the evening.  "}
	s, _, sessions := newTestService(gen)
	n := &fakeNotifier{}
	s.WithNotifier(n)
	user := uuid.New()
	sid := sessions.add(user, "Lisbon")
	w := Watch{ID: uuid.New(), UserID: user, SessionID: sid, Title: "Rain", ScheduleHuman: "Every day at 08:00", IntervalMinutes: 1440, Spec: "tell me if it will rain", Enabled: true}

	require.NoError(t, s.Run(context.Background(), w))

	got := sessions.appended[sid]
	require.Len(t, got, 1)
	require.Equal(t, "Dry until Thursday; take a light jacket for the evening.", got[0].Content)
	require.Equal(t, locitypes.OriginProactive, got[0].Origin)
	require.Equal(t, SourceLabel, got[0].SourceLabel)
	require.Equal(t, locitypes.RoleAssistant, got[0].Role)
	require.Equal(t, fixedNow, got[0].Timestamp)
	require.Len(t, n.posted, 1)

	require.Len(t, gen.prompts, 1)
	require.Contains(t, gen.prompts[0], "Task: tell me if it will rain")
	require.Contains(t, gen.prompts[0], "Lisbon")
}

func TestRunDisablesWatchWhoseThreadIsGone(t *testing.T) {
	s, repo, _ := newTestService(&fakeGen{reply: "x"})
	w := Watch{ID: uuid.New(), UserID: uuid.New(), SessionID: uuid.New(), IntervalMinutes: 60, Enabled: true}

	err := s.Run(context.Background(), w)
	require.ErrorIs(t, err, ErrSessionAbsent)
	require.Equal(t, []uuid.UUID{w.ID}, repo.disabled)
}

func TestRunPostsNothingWhenModelFails(t *testing.T) {
	s, _, sessions := newTestService(&fakeGen{err: errors.New("provider down")})
	n := &fakeNotifier{}
	s.WithNotifier(n)
	user := uuid.New()
	sid := sessions.add(user, "")

	err := s.Run(context.Background(), Watch{ID: uuid.New(), UserID: user, SessionID: sid, IntervalMinutes: 60, Enabled: true})
	require.Error(t, err)
	require.Empty(t, sessions.appended[sid])
	require.Empty(t, n.posted)
}

func TestRunDueRunsOnlyDueWatchesAndAdvancesThem(t *testing.T) {
	gen := &fakeGen{reply: "update"}
	s, repo, sessions := newTestService(gen)
	user := uuid.New()
	sid := sessions.add(user, "")

	due := Watch{ID: uuid.New(), UserID: user, SessionID: sid, IntervalMinutes: 60, Enabled: true, NextRunAt: fixedNow.Add(-time.Minute)}
	later := Watch{ID: uuid.New(), UserID: user, SessionID: sid, IntervalMinutes: 60, Enabled: true, NextRunAt: fixedNow.Add(time.Hour)}
	off := Watch{ID: uuid.New(), UserID: user, SessionID: sid, IntervalMinutes: 60, Enabled: false, NextRunAt: fixedNow.Add(-time.Hour)}
	for _, w := range []Watch{due, later, off} {
		repo.watches[w.ID] = w
	}

	posted, err := s.RunDue(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, posted)
	require.Len(t, sessions.appended[sid], 1)
	require.True(t, repo.watches[due.ID].NextRunAt.After(fixedNow))

	// Same minute again: already advanced, nothing to do.
	posted, err = s.RunDue(context.Background(), 10)
	require.NoError(t, err)
	require.Zero(t, posted)
}

func TestRunDueWithoutModelDoesNothing(t *testing.T) {
	s, repo, _ := newTestService(nil)
	repo.claimErr = errors.New("must not be called")
	posted, err := s.RunDue(context.Background(), 10)
	require.NoError(t, err)
	require.Zero(t, posted)
}

func TestRunDueReportsClaimFailure(t *testing.T) {
	s, repo, _ := newTestService(&fakeGen{})
	repo.claimErr = errors.New("db down")
	_, err := s.RunDue(context.Background(), 10)
	require.Error(t, err)
}
