package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
)

type fakeRepo struct {
	links   map[string]Link    // keyed by platform+chat
	codes   map[string]codeRow // keyed by hash
	cursor  map[string]int64   // keyed by platform+account
	touched int
}

type codeRow struct {
	userID    uuid.UUID
	platform  string
	expiresAt time.Time
	redeemed  bool
}

func chatKey(platform, externalID string) string { return platform + ":" + externalID }

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		links:  map[string]Link{},
		codes:  map[string]codeRow{},
		cursor: map[string]int64{},
	}
}

func (f *fakeRepo) LinkForChat(_ context.Context, platform, externalID string) (Link, error) {
	l, ok := f.links[chatKey(platform, externalID)]
	if !ok {
		return Link{}, ErrNotLinked
	}
	return l, nil
}

func (f *fakeRepo) LinkForUser(_ context.Context, userID uuid.UUID, platform string) (Link, error) {
	for _, l := range f.links {
		if l.UserID == userID && l.Platform == platform {
			return l, nil
		}
	}
	return Link{}, ErrNotLinked
}

func (f *fakeRepo) Unlink(_ context.Context, userID uuid.UUID, platform string) error {
	for k, l := range f.links {
		if l.UserID == userID && l.Platform == platform {
			delete(f.links, k)
			return nil
		}
	}
	return ErrNotLinked
}

func (f *fakeRepo) UnlinkChat(_ context.Context, platform, externalID string) error {
	k := chatKey(platform, externalID)
	if _, ok := f.links[k]; !ok {
		return ErrNotLinked
	}
	delete(f.links, k)
	return nil
}

func (f *fakeRepo) TouchLink(_ context.Context, platform, externalID, displayName string) error {
	k := chatKey(platform, externalID)
	l, ok := f.links[k]
	if !ok {
		return ErrNotLinked
	}
	now := time.Now()
	l.LastSeenAt = &now
	if displayName != "" {
		l.DisplayName = displayName
	}
	f.links[k] = l
	f.touched++
	return nil
}

func (f *fakeRepo) CreateCode(_ context.Context, userID uuid.UUID, platform string, codeHash []byte, expiresAt time.Time) error {
	f.codes[string(codeHash)] = codeRow{userID: userID, platform: platform, expiresAt: expiresAt}
	return nil
}

func (f *fakeRepo) RedeemCode(_ context.Context, platform, externalID, displayName string, codeHash []byte, now time.Time) (Link, error) {
	row, ok := f.codes[string(codeHash)]
	if !ok || row.redeemed || row.platform != platform || !row.expiresAt.After(now) {
		return Link{}, ErrBadCode
	}
	row.redeemed = true
	f.codes[string(codeHash)] = row

	l := Link{UserID: row.userID, Platform: platform, ExternalID: externalID, DisplayName: displayName, LinkedAt: now}
	f.links[chatKey(platform, externalID)] = l
	return l, nil
}

func (f *fakeRepo) Cursor(_ context.Context, platform, accountID string) (int64, error) {
	return f.cursor[chatKey(platform, accountID)], nil
}

func (f *fakeRepo) SetCursor(_ context.Context, platform, accountID string, updateID int64) error {
	k := chatKey(platform, accountID)
	if updateID > f.cursor[k] {
		f.cursor[k] = updateID
	}
	return nil
}

// spyAnswerer records what reached the model, which is how the tests below
// prove that commands did not.
type spyAnswerer struct {
	calls     int
	lastID    uuid.UUID
	lastEmail string
	last      string
	answer    string
	err       error
}

func (s *spyAnswerer) Answer(_ context.Context, userID uuid.UUID, email, text string) (string, error) {
	s.calls++
	s.lastID = userID
	s.lastEmail = email
	s.last = text
	if s.err != nil {
		return "", s.err
	}
	if s.answer == "" {
		return "here is a plan", nil
	}
	return s.answer, nil
}

func newService(t *testing.T) (*Service, *fakeRepo, *spyAnswerer) {
	t.Helper()
	repo, answerer := newFakeRepo(), &spyAnswerer{}
	return NewService(repo, answerer, "@loci_bot", nil), repo, answerer
}

func linkChat(t *testing.T, repo *fakeRepo, chatID string) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	repo.links[chatKey(PlatformTelegram, chatID)] = Link{
		UserID: userID, Platform: PlatformTelegram, ExternalID: chatID,
		LinkedAt: time.Now(), Email: "traveller@example.com",
	}
	return userID
}

func send(t *testing.T, svc *Service, chatID, text string) OutboundMessage {
	t.Helper()
	out, err := svc.Handle(t.Context(), InboundMessage{
		Platform: PlatformTelegram, ChatID: chatID, DisplayName: "Fernando", Text: text,
	})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	return out
}

// The whole point: a question from a linked chat is answered as its owner.
func TestALinkedChatIsAnsweredAsItsOwner(t *testing.T) {
	svc, repo, answerer := newService(t)
	userID := linkChat(t, repo, "555")

	out := send(t, svc, "555", "three days in Lisbon")

	if answerer.calls != 1 {
		t.Fatalf("answerer called %d times, want 1", answerer.calls)
	}
	if answerer.lastID != userID {
		t.Error("the question was answered as somebody else")
	}
	if out.Text != "here is a plan" {
		t.Errorf("reply = %q", out.Text)
	}
}

// A command must never cost a generation. Charging somebody's daily quota to
// read /help would be absurd, and it is one interface change away at any time.
func TestCommandsNeverReachTheModel(t *testing.T) {
	for _, text := range []string{"/start", "/help", "/unlink", "/help@loci_bot", "  /HELP  "} {
		t.Run(text, func(t *testing.T) {
			svc, repo, answerer := newService(t)
			linkChat(t, repo, "555")

			out := send(t, svc, "555", text)

			if answerer.calls != 0 {
				t.Errorf("%q reached the model", text)
			}
			if out.Text == "" {
				t.Errorf("%q got no reply", text)
			}
		})
	}
}

// A slash is punctuation, not a refusal.
func TestAnUnknownCommandIsStillAQuestion(t *testing.T) {
	svc, repo, answerer := newService(t)
	linkChat(t, repo, "555")

	send(t, svc, "555", "/lisbon in march")

	if answerer.calls != 1 {
		t.Fatal("an unrecognised command was not answered")
	}
	if !strings.Contains(answerer.last, "lisbon") {
		t.Errorf("the model received %q", answerer.last)
	}
}

func TestUnlinkDisconnectsTheChat(t *testing.T) {
	svc, repo, _ := newService(t)
	linkChat(t, repo, "555")

	out := send(t, svc, "555", "/unlink")

	if len(repo.links) != 0 {
		t.Error("the link survived /unlink")
	}
	if !strings.Contains(strings.ToLower(out.Text), "disconnected") {
		t.Errorf("reply = %q", out.Text)
	}

	// And the chat is now a stranger again.
	after := send(t, svc, "555", "three days in Lisbon")
	if !strings.Contains(after.Text, "not linked") {
		t.Errorf("reply after unlinking = %q", after.Text)
	}
}

// An unlinked chat may reach exactly one thing: a code.
func TestAnUnlinkedChatIsToldHowToLinkAndNothingElse(t *testing.T) {
	svc, _, answerer := newService(t)

	for _, text := range []string{"three days in Lisbon", "/help", "hello", ""} {
		out := send(t, svc, "999", text)
		if !strings.Contains(out.Text, "not linked") {
			t.Errorf("%q got %q, want the linking instruction", text, out.Text)
		}
	}
	if answerer.calls != 0 {
		t.Error("an unlinked chat reached the model")
	}
}

func TestACodeLinksTheChatToItsIssuer(t *testing.T) {
	svc, repo, _ := newService(t)
	userID := uuid.New()

	code, expiresAt, err := svc.IssueCode(t.Context(), userID, PlatformTelegram)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !expiresAt.After(time.Now()) {
		t.Error("the code expired before it was issued")
	}

	out := send(t, svc, "555", code)
	if !strings.Contains(strings.ToLower(out.Text), "linked") {
		t.Fatalf("reply = %q", out.Text)
	}

	link, err := repo.LinkForChat(t.Context(), PlatformTelegram, "555")
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if link.UserID != userID {
		t.Error("the chat was linked to somebody other than the code's issuer")
	}
}

// Telegram delivers a deep link as "/start CODE".
func TestACodeArrivingAsAStartArgumentStillLinks(t *testing.T) {
	svc, repo, _ := newService(t)
	userID := uuid.New()

	code, _, err := svc.IssueCode(t.Context(), userID, PlatformTelegram)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	send(t, svc, "555", "/start "+code)

	link, err := repo.LinkForChat(t.Context(), PlatformTelegram, "555")
	if err != nil || link.UserID != userID {
		t.Fatalf("link = %+v, err = %v", link, err)
	}
}

// People paste twice when the first reply is slow.
func TestACodeWorksOnce(t *testing.T) {
	svc, _, _ := newService(t)

	code, _, err := svc.IssueCode(t.Context(), uuid.New(), PlatformTelegram)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	send(t, svc, "555", code)
	out := send(t, svc, "777", code)

	if !strings.Contains(out.Text, "expired or was already used") {
		t.Errorf("second use got %q", out.Text)
	}
}

func TestAnExpiredCodeIsRefused(t *testing.T) {
	repo, answerer := newFakeRepo(), &spyAnswerer{}
	svc := NewService(repo, answerer, "@loci_bot", nil)

	code, _, err := svc.IssueCode(t.Context(), uuid.New(), PlatformTelegram)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Well past the code's life.
	svc.WithClock(func() time.Time { return time.Now().Add(codeTTL + time.Hour) })

	out := send(t, svc, "555", code)
	if !strings.Contains(out.Text, "expired") {
		t.Errorf("reply = %q", out.Text)
	}
}

// An unknown, an expired and a used code all answer the same way. Anything else
// turns the bot into an oracle for which codes exist.
func TestEveryBadCodeAnswersIdentically(t *testing.T) {
	svc, _, _ := newService(t)

	used, _, err := svc.IssueCode(t.Context(), uuid.New(), PlatformTelegram)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	send(t, svc, "111", used)

	unknown := "ABCDEFGH"
	if NormaliseCode(unknown) != unknown {
		t.Fatalf("the test's own placeholder is not a well-formed code")
	}

	first := send(t, svc, "222", used).Text
	second := send(t, svc, "333", unknown).Text
	if first != second {
		t.Errorf("a used code says %q but an unknown one says %q", first, second)
	}
}

// The upstream error can name models, providers and internal paths, none of
// which the sender can act on.
func TestAFailureToAnswerNeverLeaksTheReason(t *testing.T) {
	svc, repo, answerer := newService(t)
	linkChat(t, repo, "555")
	answerer.err = errors.New("openrouter 401: key sk-or-v1-secret rejected")

	out := send(t, svc, "555", "three days in Lisbon")

	if strings.Contains(out.Text, "sk-or-v1") || strings.Contains(out.Text, "openrouter") {
		t.Fatalf("the reply leaks the upstream error: %q", out.Text)
	}
	if out.Text == "" {
		t.Error("a failure produced no reply at all")
	}
}

func TestAnsweringRecordsThatTheChatIsLive(t *testing.T) {
	svc, repo, _ := newService(t)
	linkChat(t, repo, "555")

	send(t, svc, "555", "three days in Lisbon")

	if repo.touched == 0 {
		t.Error("last_seen_at was never stamped; the settings page cannot tell a live chat from a forgotten one")
	}
}

func TestIssueCodeRefusesPlatformsLociDoesNotLink(t *testing.T) {
	svc, _, _ := newService(t)
	if _, _, err := svc.IssueCode(t.Context(), uuid.New(), "whatsapp"); err == nil {
		t.Error("a code was issued for a platform with no adapter")
	}
}

// spyQuota records what was metered, and can refuse.
type spyQuota struct {
	calls  int
	lastID uuid.UUID
	last   string
	err    error
}

func (q *spyQuota) ConsumeQuota(_ context.Context, userID uuid.UUID, email string) error {
	q.calls++
	q.lastID = userID
	q.last = email
	return q.err
}

func newMeteredService(t *testing.T) (*Service, *fakeRepo, *spyAnswerer, *spyQuota) {
	t.Helper()
	svc, repo, answerer := newService(t)
	quota := &spyQuota{}
	return svc.WithQuota(quota), repo, answerer, quota
}

// The gap this closes: a question asked over Telegram used to cost nothing,
// because the webhook is mounted outside the interceptor chain that meters
// the same question asked in the app.
func TestAnAnsweredMessageCostsTheOwnerARequest(t *testing.T) {
	svc, repo, answerer, quota := newMeteredService(t)
	userID := linkChat(t, repo, "555")

	send(t, svc, "555", "three days in Lisbon")

	if quota.calls != 1 {
		t.Fatalf("quota consumed %d times, want once", quota.calls)
	}
	if quota.lastID != userID {
		t.Errorf("metered %s, want the linked owner %s", quota.lastID, userID)
	}
	// The address is what the complimentary-account list is matched on, so a
	// half-identified caller is metered differently here than in the app.
	if quota.last != "traveller@example.com" {
		t.Errorf("metered without the owner's address: %q", quota.last)
	}
	if answerer.calls != 1 {
		t.Errorf("the answerer ran %d times", answerer.calls)
	}
}

func TestQuotaIsSpentBeforeTheModelRuns(t *testing.T) {
	svc, repo, answerer, quota := newMeteredService(t)
	linkChat(t, repo, "555")
	quota.err = &subscription.QuotaExceededError{Plan: "free", Limit: 10}

	out := send(t, svc, "555", "three days in Lisbon")

	if answerer.calls != 0 {
		t.Error("a refused message still reached the model")
	}
	if !strings.Contains(out.Text, "reset") {
		t.Errorf("the refusal should say when the limit lifts: %q", out.Text)
	}
	if out.Silent {
		t.Error("the refusal was not sent")
	}
}

// Reading the instructions must not cost a generation. That guarantee predates
// metering; this is what pins it now that there is something to spend.
func TestCommandsAndCodesAreFree(t *testing.T) {
	t.Run("a command", func(t *testing.T) {
		svc, repo, _, quota := newMeteredService(t)
		linkChat(t, repo, "555")

		for _, command := range []string{"/help", "/start"} {
			send(t, svc, "555", command)
		}
		if quota.calls != 0 {
			t.Errorf("commands consumed %d requests", quota.calls)
		}
	})

	t.Run("an unlinked chat", func(t *testing.T) {
		svc, _, _, quota := newMeteredService(t)

		send(t, svc, "555", "hello?")
		send(t, svc, "555", "ABCD1234")

		// Nobody to charge, and nothing was spent on their behalf.
		if quota.calls != 0 {
			t.Errorf("an unlinked chat consumed %d requests", quota.calls)
		}
	})

	t.Run("an empty message", func(t *testing.T) {
		svc, repo, _, quota := newMeteredService(t)
		linkChat(t, repo, "555")

		send(t, svc, "555", "   ")
		if quota.calls != 0 {
			t.Errorf("an empty message consumed %d requests", quota.calls)
		}
	})
}

// A blip in the counter must not become a free pass, but it must not read as
// "you are out of requests" either.
func TestAMeteringFailureRefusesWithoutBlamingThePlan(t *testing.T) {
	svc, repo, answerer, quota := newMeteredService(t)
	linkChat(t, repo, "555")
	quota.err = errors.New("the database went away")

	out := send(t, svc, "555", "three days in Lisbon")

	if answerer.calls != 0 {
		t.Error("the model ran despite the counter failing")
	}
	if strings.Contains(out.Text, "plan") && strings.Contains(out.Text, "reset") {
		t.Errorf("a counter failure should not read as a quota refusal: %q", out.Text)
	}
	if out.Text == "" {
		t.Error("the sender was told nothing")
	}
}

// A service built without a quota answers as it always did, which is what the
// tests that are not about metering rely on.
func TestWithoutAQuotaNothingIsMetered(t *testing.T) {
	svc, repo, answerer := newService(t)
	linkChat(t, repo, "555")

	send(t, svc, "555", "three days in Lisbon")

	if answerer.calls != 1 {
		t.Errorf("the answerer ran %d times, want once", answerer.calls)
	}
}

// spokenBy builds an inbound recording whose transcript is text, recording
// whether it was ever asked for.
func spokenBy(text string, err error) (InboundMessage, *bool) {
	asked := new(bool)
	return InboundMessage{
		Platform: PlatformTelegram, ChatID: "555", DisplayName: "Fernando",
		Audio: func(context.Context, uuid.UUID) (string, error) {
			*asked = true
			return text, err
		},
	}, asked
}

// The hole this closes: anybody can message a bot. Fetching and transcribing a
// recording costs money, so a chat with no account behind it must not be able
// to start that — otherwise a stranger spends the owner's credits a minute of
// audio at a time, which is worse than the metering gap it would be working
// around.
func TestAnUnlinkedChatIsNeverTranscribed(t *testing.T) {
	svc, _, answerer, quota := newMeteredService(t)
	in, asked := spokenBy("three days in Lisbon", nil)

	out, err := svc.Handle(t.Context(), in)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if *asked {
		t.Error("a recording from an unlinked chat was transcribed")
	}
	if quota.calls != 0 {
		t.Error("an unlinked chat spent somebody's quota")
	}
	if answerer.calls != 0 {
		t.Error("an unlinked chat reached the model")
	}
	if !strings.Contains(out.Text, "not linked") {
		t.Errorf("the reply should explain how to link: %q", out.Text)
	}
}

func TestARecordingIsPaidForBeforeItIsFetched(t *testing.T) {
	svc, repo, answerer, quota := newMeteredService(t)
	linkChat(t, repo, "555")
	quota.err = &subscription.QuotaExceededError{Plan: "free", Limit: 10}

	in, asked := spokenBy("three days in Lisbon", nil)
	out, err := svc.Handle(t.Context(), in)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	// Out of quota costs nothing: no download, no transcription, no model.
	if *asked {
		t.Error("a recording was fetched for an account with no requests left")
	}
	if answerer.calls != 0 {
		t.Error("the model ran for an account with no requests left")
	}
	if !strings.Contains(out.Text, "reset") {
		t.Errorf("the refusal should say when the limit lifts: %q", out.Text)
	}
}

func TestARecordingIsAnsweredAsThoughItWereTyped(t *testing.T) {
	svc, repo, answerer, quota := newMeteredService(t)
	userID := linkChat(t, repo, "555")

	in, asked := spokenBy("  three days in Lisbon  ", nil)
	out, err := svc.Handle(t.Context(), in)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if !*asked {
		t.Fatal("the recording was never transcribed")
	}
	if quota.calls != 1 {
		t.Errorf("quota consumed %d times, want once", quota.calls)
	}
	if answerer.calls != 1 {
		t.Fatalf("the model ran %d times", answerer.calls)
	}
	// Same session, same account, same everything as typing it would be.
	if answerer.lastID != userID {
		t.Errorf("answered as %s, want the linked owner %s", answerer.lastID, userID)
	}
	if answerer.last != "three days in Lisbon" {
		t.Errorf("the model saw %q, want the trimmed transcript", answerer.last)
	}
	if out.Text != "here is a plan" {
		t.Errorf("reply = %q", out.Text)
	}
}

func TestSilenceAndFailureAreToldApart(t *testing.T) {
	t.Run("nothing was said", func(t *testing.T) {
		svc, repo, answerer, _ := newMeteredService(t)
		linkChat(t, repo, "555")

		in, _ := spokenBy("   ", nil)
		out, err := svc.Handle(t.Context(), in)
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		if answerer.calls != 0 {
			t.Error("an empty transcript still reached the model")
		}
		if !strings.Contains(out.Text, "hear anything") {
			t.Errorf("reply = %q, want it to say nothing was heard", out.Text)
		}
	})

	t.Run("it could not be understood", func(t *testing.T) {
		svc, repo, answerer, _ := newMeteredService(t)
		linkChat(t, repo, "555")

		in, _ := spokenBy("", errors.New("the provider went away"))
		out, err := svc.Handle(t.Context(), in)
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		if answerer.calls != 0 {
			t.Error("a failed transcription still reached the model")
		}
		// Different advice: silence means speak again, a failure means the
		// keyboard is the way through.
		if !strings.Contains(out.Text, "type it") {
			t.Errorf("reply = %q, want it to offer typing instead", out.Text)
		}
	})
}

// Nobody says "slash help", so a transcript never carries the slash
// parseCommand matches on. Running commands over a transcript would only make
// "help" land as an unanswerable question.
func TestASpokenMessageIsNotTreatedAsACommand(t *testing.T) {
	svc, repo, answerer, _ := newMeteredService(t)
	linkChat(t, repo, "555")

	in, _ := spokenBy("/unlink", nil)
	if _, err := svc.Handle(t.Context(), in); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if answerer.calls != 1 {
		t.Errorf("the model ran %d times, want the transcript answered as a question", answerer.calls)
	}
	if _, err := repo.LinkForChat(t.Context(), PlatformTelegram, "555"); err != nil {
		t.Error("a spoken \"/unlink\" disconnected the chat")
	}
}
