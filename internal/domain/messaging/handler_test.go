package messaging

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	messagingv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/messaging"
)

func asCaller(t *testing.T, userID uuid.UUID) context.Context {
	t.Helper()
	return interceptors.ContextWithClaims(t.Context(), &interceptors.Claims{UserID: userID.String()})
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// With no bot there is no service. The page is told so — an empty bot handle
// is the proto's signal — rather than shown an error it can only render as a
// spinner.
func TestWithoutABotGetLinkHasNoHandle(t *testing.T) {
	h := NewHandler(nil, quiet())

	got, err := h.GetLink(asCaller(t, uuid.New()), connect.NewRequest(&messagingv1.GetLinkRequest{}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Msg.GetBotHandle() != "" || got.Msg.GetLink() != nil {
		t.Errorf("got %+v, want no bot handle and no link", got.Msg)
	}

	_, err = h.CreateLinkCode(asCaller(t, uuid.New()), connect.NewRequest(&messagingv1.CreateLinkCodeRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("create code: code = %v, want FailedPrecondition", connect.CodeOf(err))
	}

	if _, err := h.Unlink(asCaller(t, uuid.New()), connect.NewRequest(&messagingv1.UnlinkRequest{})); err != nil {
		t.Errorf("unlink with nothing to unlink: %v", err)
	}
}

// Not linked is the ordinary state and the page's cue to offer a code; it
// must arrive as an empty answer carrying the bot handle, not as an error.
func TestNotLinkedIsAnEmptyAnswerWithTheBotHandle(t *testing.T) {
	svc, _, _ := newService(t)
	h := NewHandler(svc, quiet())

	got, err := h.GetLink(asCaller(t, uuid.New()), connect.NewRequest(&messagingv1.GetLinkRequest{}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Msg.GetLink() != nil {
		t.Error("a link was reported for an account with none")
	}
	if got.Msg.GetBotHandle() != "@loci_bot" {
		t.Errorf("bot handle = %q", got.Msg.GetBotHandle())
	}
}

func TestALinkedChatIsReported(t *testing.T) {
	svc, repo, _ := newService(t)
	h := NewHandler(svc, quiet())
	userID := linkChat(t, repo, "chat-1")

	got, err := h.GetLink(asCaller(t, userID), connect.NewRequest(&messagingv1.GetLinkRequest{Platform: "telegram"}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	link := got.Msg.GetLink()
	if link == nil {
		t.Fatal("no link reported")
	}
	if link.GetPlatform() != PlatformTelegram || link.GetLinkedAt() == nil {
		t.Errorf("link = %+v", link)
	}
	if link.GetLastSeenAt() != nil {
		t.Error("a chat that has never been used reports a last-seen time")
	}
}

// The code comes back exactly once, with when it stops working and where to
// send it — everything the page needs to show the next step.
func TestACodeIsIssuedWithItsExpiryAndDestination(t *testing.T) {
	svc, repo, _ := newService(t)
	h := NewHandler(svc, quiet())
	before := time.Now()

	got, err := h.CreateLinkCode(asCaller(t, uuid.New()), connect.NewRequest(&messagingv1.CreateLinkCodeRequest{}))
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	code := got.Msg.GetCode()
	if len(code) != codeLength {
		t.Errorf("code %q is not %d characters", code, codeLength)
	}
	if got.Msg.GetBotHandle() != "@loci_bot" {
		t.Errorf("bot handle = %q", got.Msg.GetBotHandle())
	}
	expires := got.Msg.GetExpiresAt().AsTime()
	if expires.Before(before.Add(codeTTL-time.Minute)) || expires.After(before.Add(codeTTL+time.Minute)) {
		t.Errorf("expires at %v, want about %v from now", expires, codeTTL)
	}

	// Only the hash is stored, so a database read cannot be redeemed.
	if len(repo.codes) != 1 {
		t.Fatalf("%d codes stored", len(repo.codes))
	}
	for hash := range repo.codes {
		if hash == code || hash == NormaliseCode(code) {
			t.Error("the code is stored in the clear")
		}
	}
}

func TestACodeIssuedFromSettingsLinksTheChatThatSendsIt(t *testing.T) {
	svc, _, _ := newService(t)
	h := NewHandler(svc, quiet())
	userID := uuid.New()

	issued, err := h.CreateLinkCode(asCaller(t, userID), connect.NewRequest(&messagingv1.CreateLinkCodeRequest{}))
	if err != nil {
		t.Fatalf("create code: %v", err)
	}

	send(t, svc, "chat-9", issued.Msg.GetCode())

	got, err := h.GetLink(asCaller(t, userID), connect.NewRequest(&messagingv1.GetLinkRequest{}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Msg.GetLink() == nil {
		t.Fatal("the chat that redeemed the code is not linked to the issuer")
	}
}

func TestAnUnknownPlatformIsRefused(t *testing.T) {
	svc, _, _ := newService(t)
	h := NewHandler(svc, quiet())

	_, err := h.CreateLinkCode(asCaller(t, uuid.New()), connect.NewRequest(&messagingv1.CreateLinkCodeRequest{Platform: "irc"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestUnlinkFromSettingsRemovesTheLinkAndIsIdempotent(t *testing.T) {
	svc, repo, _ := newService(t)
	h := NewHandler(svc, quiet())
	userID := linkChat(t, repo, "chat-2")

	if _, err := h.Unlink(asCaller(t, userID), connect.NewRequest(&messagingv1.UnlinkRequest{})); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	got, err := h.GetLink(asCaller(t, userID), connect.NewRequest(&messagingv1.GetLinkRequest{}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Msg.GetLink() != nil {
		t.Error("still linked after unlink")
	}

	// Pressing the button twice finds the state it wanted.
	if _, err := h.Unlink(asCaller(t, userID), connect.NewRequest(&messagingv1.UnlinkRequest{})); err != nil {
		t.Errorf("second unlink: %v", err)
	}
}

func TestEveryRPCRequiresACaller(t *testing.T) {
	h := NewHandler(nil, quiet())
	ctx := t.Context()

	if _, err := h.GetLink(ctx, connect.NewRequest(&messagingv1.GetLinkRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("get: code = %v", connect.CodeOf(err))
	}
	if _, err := h.CreateLinkCode(ctx, connect.NewRequest(&messagingv1.CreateLinkCodeRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("create code: code = %v", connect.CodeOf(err))
	}
	if _, err := h.Unlink(ctx, connect.NewRequest(&messagingv1.UnlinkRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("unlink: code = %v", connect.CodeOf(err))
	}
}
