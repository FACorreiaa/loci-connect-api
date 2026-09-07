package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
	calls  int
	lastID uuid.UUID
	last   string
	answer string
	err    error
}

func (s *spyAnswerer) Answer(_ context.Context, userID uuid.UUID, text string) (string, error) {
	s.calls++
	s.lastID = userID
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
		UserID: userID, Platform: PlatformTelegram, ExternalID: chatID, LinkedAt: time.Now(),
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
