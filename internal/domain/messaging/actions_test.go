package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type spyPaginator struct {
	calls       int
	latestCalls int
	lastToken   string
	lastPage    int
	text        string
	next        string
	err         error
}

func (p *spyPaginator) Page(_ context.Context, _ uuid.UUID, _, token string) (string, string, error) {
	p.calls++
	p.lastToken = token
	return p.text, p.next, p.err
}

func (p *spyPaginator) LatestPage(_ context.Context, _ uuid.UUID, _ string, page int) (string, string, error) {
	p.latestCalls++
	p.lastPage = page
	return p.text, p.next, p.err
}

func newPagedService(t *testing.T) (*Service, *fakeRepo, *spyPaginator, *spyQuota) {
	t.Helper()
	svc, repo, _, quota := newMeteredService(t)
	pager := &spyPaginator{text: "More places (13–24 of 30)"}
	return svc.WithPaginator(pager), repo, pager, quota
}

// A press reads back an answer that was already generated and already paid
// for. Charging for it would be charging twice for one generation.
func TestAButtonPressDoesNotSpendQuota(t *testing.T) {
	svc, repo, pager, quota := newPagedService(t)
	linkChat(t, repo, "9001")

	out, err := svc.HandleAction(context.Background(), InboundAction{
		Platform: PlatformTelegram, ChatID: "9001", Data: "p|abc|2|i",
	})
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if quota.calls != 0 {
		t.Errorf("a page press spent %d units of quota, want 0", quota.calls)
	}
	if pager.calls != 1 || pager.lastToken != "p|abc|2|i" {
		t.Errorf("paginator called %d times with %q", pager.calls, pager.lastToken)
	}
	if out.Text != "More places (13–24 of 30)" {
		t.Errorf("unexpected reply %q", out.Text)
	}
}

// When there is more after this page, the reply carries the offer.
func TestAPageOffersTheNextOne(t *testing.T) {
	svc, repo, pager, _ := newPagedService(t)
	linkChat(t, repo, "9002")
	pager.next = "p|abc|3|i"

	out, err := svc.HandleAction(context.Background(), InboundAction{
		Platform: PlatformTelegram, ChatID: "9002", Data: "p|abc|2|i",
	})
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if len(out.Buttons) != 1 || out.Buttons[0].Data != "p|abc|3|i" {
		t.Errorf("buttons = %+v, want one offering page 3", out.Buttons)
	}

	// The last page offers nothing.
	pager.next = ""
	out, err = svc.HandleAction(context.Background(), InboundAction{
		Platform: PlatformTelegram, ChatID: "9002", Data: "p|abc|9|i",
	})
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if len(out.Buttons) != 0 {
		t.Errorf("the last page still offered more: %+v", out.Buttons)
	}
}

// A button pressed from a chat that has since been unlinked gets the linking
// instruction, not a page of somebody's itinerary.
func TestAPressFromAnUnlinkedChatGetsTheInstruction(t *testing.T) {
	svc, _, pager, _ := newPagedService(t)

	out, err := svc.HandleAction(context.Background(), InboundAction{
		Platform: PlatformTelegram, ChatID: "not-linked", Data: "p|abc|2|i",
	})
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if pager.calls != 0 {
		t.Error("an unlinked chat reached the paginator")
	}
	if !strings.Contains(out.Text, "not linked") {
		t.Errorf("unexpected reply %q", out.Text)
	}
}

// A failure to render a page must not repeat the internal error into the chat.
func TestAFailedPageIsReportedPlainly(t *testing.T) {
	svc, repo, pager, _ := newPagedService(t)
	linkChat(t, repo, "9003")
	pager.err = errors.New("chatbridge: unreadable page token \"p|x\"")

	out, err := svc.HandleAction(context.Background(), InboundAction{
		Platform: PlatformTelegram, ChatID: "9003", Data: "p|x",
	})
	if err != nil {
		t.Fatalf("HandleAction returned an error rather than a reply: %v", err)
	}
	if strings.Contains(out.Text, "chatbridge") || strings.Contains(out.Text, "token") {
		t.Errorf("the internal error reached the chat: %q", out.Text)
	}
}

// "/more" is the fallback for when a keyboard is not usable. Like a press, it
// is a read and must not be metered.
func TestSlashMoreReadsWithoutSpendingQuota(t *testing.T) {
	svc, repo, pager, quota := newPagedService(t)
	linkChat(t, repo, "9004")

	for _, c := range []struct {
		text string
		page int
	}{
		{"/more", 2},
		{"/more 3", 3},
		{"/more@loci_bot", 2},
	} {
		pager.latestCalls, pager.lastPage = 0, 0
		out, err := svc.Handle(context.Background(), InboundMessage{
			Platform: PlatformTelegram, ChatID: "9004", Text: c.text,
		})
		if err != nil {
			t.Fatalf("%q: %v", c.text, err)
		}
		if pager.latestCalls != 1 || pager.lastPage != c.page {
			t.Errorf("%q asked for page %d after %d calls, want page %d",
				c.text, pager.lastPage, pager.latestCalls, c.page)
		}
		if out.Text == "" {
			t.Errorf("%q produced no reply", c.text)
		}
	}

	if quota.calls != 0 {
		t.Errorf("/more spent %d units of quota, want 0", quota.calls)
	}
}

// A page number that is not a number is a mistake worth naming, not a silent
// jump to page two.
func TestSlashMoreRejectsNonsenseArguments(t *testing.T) {
	svc, repo, pager, _ := newPagedService(t)
	linkChat(t, repo, "9005")

	for _, text := range []string{"/more two", "/more -1", "/more 0"} {
		out, err := svc.Handle(context.Background(), InboundMessage{
			Platform: PlatformTelegram, ChatID: "9005", Text: text,
		})
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if pager.latestCalls != 0 {
			t.Errorf("%q reached the paginator", text)
		}
		if !strings.Contains(out.Text, "/more 3") {
			t.Errorf("%q did not explain the form: %q", text, out.Text)
		}
	}
}

// A deployment with no paginator configured still answers rather than failing.
func TestPagingWithoutAPaginatorIsPolite(t *testing.T) {
	svc, repo, _ := newService(t)
	linkChat(t, repo, "9006")

	out, err := svc.HandleAction(context.Background(), InboundAction{
		Platform: PlatformTelegram, ChatID: "9006", Data: "p|abc|2|i",
	})
	if err != nil || out.Text == "" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}
